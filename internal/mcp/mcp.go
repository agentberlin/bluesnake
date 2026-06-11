// Package mcp implements a Model Context Protocol server over the bluesnake
// crawl engine, so LLM agents (Claude Code, Claude Desktop, any MCP client)
// can start and control crawls and analyse stored crawl data.
//
// The protocol layer is hand-rolled JSON-RPC 2.0 — like the robots parser and
// the WARC writer, it is small enough that a dependency would cost more than
// it saves. The Server is transport-agnostic; the streamable-HTTP transport
// (http.go) serves it from both `bluesnake mcp` and the desktop app's MCP
// toggle.
//
// Crawl control goes through the Backend interface: the CLI uses the
// self-contained Runner; the desktop app supplies an adapter around its Wails
// session so MCP-started crawls stream live into the UI. Data access is
// deliberately thin — one read-only SQL tool over the per-crawl SQLite
// database plus a schema-description tool — because the LLM is better at SQL
// than any fixed export surface.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Protocol revisions this server accepts. Initialize echoes the client's
// version when supported, otherwise offers the latest.
const latestProtocol = "2025-06-18"

var supportedProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

const serverInstructions = `bluesnake is a website crawler and SEO auditor (Screaming Frog parity) running locally on this machine.

Typical workflow:
1. start_crawl with a seed URL — it returns a crawl_id immediately and crawls in the background.
2. Poll crawl_status until state is no longer "running" (a few seconds between calls is plenty).
3. issue_summary for the audit verdict, then query for anything deeper.

Crawl results live in one SQLite database per crawl. get_database_schema returns the exact tables and columns; query accepts any single read-only SQLite statement against them. Configuration knobs for start_crawl are discoverable with list_config_options. One crawl runs at a time per server; stored crawls from past runs are always queryable.`

// Server is a transport-agnostic MCP server: Handle turns one incoming
// JSON-RPC message (or batch) into a reply.
type Server struct {
	backend Backend
	version string
	tools   []Tool
}

func NewServer(b Backend, version string) *Server {
	s := &Server{backend: b, version: version}
	s.tools = s.buildTools()
	return s
}

// ---------------------------------------------------------------------------
// JSON-RPC 2.0

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
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
		// result failed to serialize; report instead of dropping the reply
		out, _ = json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id,
			Error: &rpcError{Code: codeParse, Message: "result serialization failed: " + err.Error()}})
	}
	return out
}

// Handle processes one wire message — a single request or a batch array —
// and returns the serialized reply, or nil when no reply is due
// (notifications).
func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	if isBatch(raw) {
		var msgs []json.RawMessage
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return marshalResponse(nil, nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()})
		}
		var replies []json.RawMessage
		for _, m := range msgs {
			if r := s.handleOne(ctx, m); r != nil {
				replies = append(replies, r)
			}
		}
		if len(replies) == 0 {
			return nil
		}
		out, _ := json.Marshal(replies)
		return out
	}
	return s.handleOne(ctx, raw)
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

func (s *Server) handleOne(ctx context.Context, raw []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return marshalResponse(nil, nil, &rpcError{Code: codeParse, Message: "parse error: " + err.Error()})
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"

	result, rpcErr := s.dispatch(ctx, &req)
	if notification {
		return nil
	}
	return marshalResponse(req.ID, result, rpcErr)
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params), nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return s.handleToolsList(), nil
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	case "notifications/initialized", "notifications/cancelled", "notifications/roots/list_changed":
		return nil, nil // notifications: acknowledged by silence
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}
	}
}

func (s *Server) handleInitialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := latestProtocol
	if supportedProtocols[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    "bluesnake",
			"title":   "bluesnake — website crawler & SEO auditor",
			"version": s.version,
		},
		"instructions": serverInstructions,
	}
}

func (s *Server) handleToolsList() any {
	tools := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return map[string]any{"tools": tools}
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	for _, t := range s.tools {
		if t.Name != p.Name {
			continue
		}
		text, err := t.handler(ctx, p.Arguments)
		if err != nil {
			// Tool failures are results, not protocol errors — the model
			// reads them and self-corrects (e.g. fixes its SQL).
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", p.Name)}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}
