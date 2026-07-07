// Package mcpstub is the tunnel server's offline MCP responder. When a
// registered tunnel has no live session (the user's app is closed), the
// gateway serves /mcp from this stub instead of failing with 502: the MCP
// lifecycle (initialize, ping, tools/list) succeeds, so LLM clients keep
// treating the connector as healthy, and every tools/call returns HTTP 200
// with an `isError` tool result explaining that the local app is not running
// — text the model reads and relays to the user. A transport-level failure
// would instead mark the whole connector dead and force the user through a
// manual reconnect that fixes nothing.
//
// The stub mirrors the local server's transport contract (internal/mcp):
// POST-only JSON-RPC answered as application/json, 202 for notifications,
// 405 for GET/DELETE, single messages and batches alike. The local server is
// stateless (no session ids), which is what makes the stub/real handover
// seamless in both directions. tunnelserver must stay extractable with only
// internal/tunnel/wire, so the few JSON-RPC shapes are duplicated here
// rather than imported from internal/mcp.
package mcpstub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// maxBody caps request and probe-response bodies, matching the local MCP
// server's own limit.
const maxBody = 8 << 20

// latestProtocol / supportedProtocols mirror internal/mcp. The stub only
// answers while the app is offline; if the app learns newer revisions before
// this list does, the stub degrades to offering latestProtocol, which
// clients accept.
const latestProtocol = "2025-06-18"

var supportedProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// offlineNote is appended to the initialize instructions so a model that
// connects while the app is closed knows what state it is in.
const offlineNote = "NOTE: the local bluesnake instance is currently OFFLINE — the bluesnake app is not running on the user's machine. Tool calls will return an error until the user opens the bluesnake app (or runs `bluesnake mcp --public`); the tunnel reconnects automatically within seconds."

// offlineCallText is the body of every offline tools/call result. It is
// written for the model: say what happened, rule out the fix that doesn't
// work (reconnecting the connector), and give the fix that does.
const offlineCallText = "the bluesnake app is not running on the user's machine (or is unreachable), so the call could not be executed. The connector itself is healthy — reconnecting or reconfiguring it will not help. Ask the user to open the bluesnake desktop app (or run `bluesnake mcp --public` in a terminal); the tunnel comes back automatically within a few seconds, then retry this call."

// Snapshot is what the gateway captured from the real local server the last
// time its tunnel connected: enough to answer initialize and tools/list
// faithfully while the app is offline. Serialized into the tunnels table so
// it survives tunnel-server restarts.
type Snapshot struct {
	ServerInfo   json.RawMessage `json:"serverInfo,omitempty"`
	Instructions string          `json:"instructions,omitempty"`
	ToolsResult  json.RawMessage `json:"toolsResult,omitempty"`
}

// Decode parses a stored snapshot, returning nil for empty or corrupt bytes
// — the stub then falls back to generic defaults.
func Decode(raw []byte) *Snapshot {
	if len(raw) == 0 {
		return nil
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

// Serve answers one public /mcp request for an offline tunnel. snap may be
// nil (no snapshot captured yet). The transport behavior deliberately
// matches internal/mcp/http.go so clients cannot tell stub from real at this
// layer.
func Serve(w http.ResponseWriter, r *http.Request, snap *Snapshot) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	resp := handle(body, snap)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // notification: no reply due
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp)
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 (mirrors internal/mcp)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeMethodNotFound = -32601
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func marshalResponse(id json.RawMessage, result any, rpcErr *rpcError) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		out, _ = json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id,
			Error: &rpcError{Code: codeParse, Message: "result serialization failed: " + err.Error()}})
	}
	return out
}

// handle turns one wire message — a single request or a batch — into the
// serialized reply, or nil when no reply is due (notifications).
func handle(raw []byte, snap *Snapshot) []byte {
	if isBatch(raw) {
		var msgs []json.RawMessage
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return marshalResponse(nil, nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()})
		}
		var replies []json.RawMessage
		for _, m := range msgs {
			if r := handleOne(m, snap); r != nil {
				replies = append(replies, r)
			}
		}
		if len(replies) == 0 {
			return nil
		}
		out, _ := json.Marshal(replies)
		return out
	}
	return handleOne(raw, snap)
}

func isBatch(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

func handleOne(raw []byte, snap *Snapshot) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return marshalResponse(nil, nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()})
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"

	result, rpcErr := dispatch(&req, snap)
	if notification {
		return nil
	}
	return marshalResponse(req.ID, result, rpcErr)
}

func dispatch(req *rpcRequest, snap *Snapshot) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return initializeResult(req.Params, snap), nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		if snap != nil && len(snap.ToolsResult) > 0 {
			return snap.ToolsResult, nil
		}
		return map[string]any{"tools": []any{}}, nil
	case "tools/call":
		return offlineCallResult(req.Params), nil
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		return nil, nil // notifications: acknowledged by silence
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}
	}
}

func initializeResult(params json.RawMessage, snap *Snapshot) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := latestProtocol
	if supportedProtocols[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}
	serverInfo := json.RawMessage(`{"name":"bluesnake","title":"bluesnake — website crawler & SEO auditor","version":"unknown"}`)
	instructions := offlineNote
	if snap != nil {
		if len(snap.ServerInfo) > 0 {
			serverInfo = snap.ServerInfo
		}
		if snap.Instructions != "" {
			instructions = snap.Instructions + "\n\n" + offlineNote
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      serverInfo,
		"instructions":    instructions,
	}
}

// offlineCallResult is the heart of the stub: a *successful* tools/call
// response whose result carries isError — the MCP mechanism for execution
// errors the model should see and act on, as opposed to protocol errors that
// clients treat as a broken server.
func offlineCallResult(params json.RawMessage) any {
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params, &p)
	msg := "The tool did not run: " + offlineCallText
	if p.Name != "" {
		msg = fmt.Sprintf("Tool %q did not run: %s", p.Name, offlineCallText)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

// ---------------------------------------------------------------------------
// Connect-time probe

// maxSnapshot bounds a persisted snapshot so a hostile tunnel client
// advertising an enormous tool list cannot bloat the control-plane database.
const maxSnapshot = 1 << 20

// Probe fetches a fresh Snapshot from the real local server down a
// just-connected tunnel, exactly as an MCP client would: one initialize,
// one tools/list. dial opens a new stream on the tunnel session; host is the
// tunnel's public host (the client end rewrites it to the local address).
// It returns the serialized snapshot ready for Decode and persistence.
func Probe(ctx context.Context, dial func() (net.Conn, error), host string) ([]byte, error) {
	transport := &http.Transport{
		DialContext:        func(context.Context, string, string) (net.Conn, error) { return dial() },
		DisableCompression: true,
	}
	defer transport.CloseIdleConnections()
	cl := &http.Client{Transport: transport}
	url := "http://" + host + "/mcp"

	var snap Snapshot
	var initRes struct {
		ServerInfo   json.RawMessage `json:"serverInfo"`
		Instructions string          `json:"instructions"`
	}
	const initReq = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + latestProtocol + `","capabilities":{},"clientInfo":{"name":"bluesnake-tunnelserver","version":"1"}}}`
	if err := rpcCall(ctx, cl, url, initReq, &initRes); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	snap.ServerInfo = initRes.ServerInfo
	snap.Instructions = initRes.Instructions

	var toolsRes json.RawMessage
	if err := rpcCall(ctx, cl, url, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, &toolsRes); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	snap.ToolsResult = toolsRes

	out, err := json.Marshal(&snap)
	if err != nil {
		return nil, err
	}
	if len(out) > maxSnapshot {
		return nil, fmt.Errorf("snapshot too large (%d bytes)", len(out))
	}
	return out, nil
}

// rpcCall POSTs one JSON-RPC request and unmarshals its result into out.
func rpcCall(ctx context.Context, cl *http.Client, url, body string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result) == 0 {
		return fmt.Errorf("empty result")
	}
	return json.Unmarshal(rpcResp.Result, out)
}
