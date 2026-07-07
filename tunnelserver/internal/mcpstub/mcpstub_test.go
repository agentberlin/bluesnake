package mcpstub

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve runs one request through Serve and returns the recorder.
func serve(t *testing.T, method, body string, snap *Snapshot) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://abc.t.snake.blue/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	Serve(rec, req, snap)
	return rec
}

// result unmarshals the "result" member of a single JSON-RPC response body.
func result(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response %s: %v", body, err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", resp.Error)
	}
	return resp.Result
}

func TestInitializeWithoutSnapshot(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	res := result(t, rec.Body.Bytes())
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want echo of supported version", res["protocolVersion"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != "bluesnake" {
		t.Errorf("serverInfo = %v", res["serverInfo"])
	}
	instr, _ := res["instructions"].(string)
	if !strings.Contains(instr, "OFFLINE") {
		t.Errorf("instructions missing offline note: %q", instr)
	}
}

func TestInitializeUnknownVersionOffersLatest(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`, nil)
	res := result(t, rec.Body.Bytes())
	if res["protocolVersion"] != latestProtocol {
		t.Errorf("protocolVersion = %v, want %s", res["protocolVersion"], latestProtocol)
	}
}

func TestInitializeWithSnapshot(t *testing.T) {
	snap := &Snapshot{
		ServerInfo:   json.RawMessage(`{"name":"bluesnake","version":"9.9.9"}`),
		Instructions: "real instructions",
	}
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, snap)
	res := result(t, rec.Body.Bytes())
	info, _ := res["serverInfo"].(map[string]any)
	if info["version"] != "9.9.9" {
		t.Errorf("serverInfo = %v, want snapshot's", res["serverInfo"])
	}
	instr, _ := res["instructions"].(string)
	if !strings.Contains(instr, "real instructions") || !strings.Contains(instr, "OFFLINE") {
		t.Errorf("instructions = %q, want snapshot's plus offline note", instr)
	}
}

func TestPing(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":7,"method":"ping"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if res := result(t, rec.Body.Bytes()); len(res) != 0 {
		t.Errorf("ping result = %v, want {}", res)
	}
}

func TestToolsListFallsBackToEmpty(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	res := result(t, rec.Body.Bytes())
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Errorf("tools = %v, want empty list", res["tools"])
	}
}

func TestToolsListServesSnapshot(t *testing.T) {
	snap := &Snapshot{ToolsResult: json.RawMessage(`{"tools":[{"name":"start_crawl","description":"d","inputSchema":{}}]}`)}
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, snap)
	if !strings.Contains(rec.Body.String(), "start_crawl") {
		t.Errorf("body missing snapshot tools: %s", rec.Body.String())
	}
}

func TestToolsCallIsError(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"start_crawl","arguments":{}}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (tool errors are results, not transport errors)", rec.Code)
	}
	res := result(t, rec.Body.Bytes())
	if res["isError"] != true {
		t.Errorf("isError = %v, want true", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", res["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "start_crawl") || !strings.Contains(text, "not running") {
		t.Errorf("offline message = %q", text)
	}
}

func TestNotificationGets202(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestUnknownMethod(t *testing.T) {
	rec := serve(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, nil)
	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Error == nil {
		t.Fatalf("want rpc error, got %s", rec.Body.String())
	}
	if resp.Error.Code != codeMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, codeMethodNotFound)
	}
}

func TestParseError(t *testing.T) {
	rec := serve(t, http.MethodPost, `{not json`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "-32700") {
		t.Errorf("body = %s, want parse error", rec.Body.String())
	}
}

func TestBatch(t *testing.T) {
	body := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"x"}}]`
	rec := serve(t, http.MethodPost, body, nil)
	var replies []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &replies); err != nil {
		t.Fatalf("batch response not an array: %s", rec.Body.String())
	}
	if len(replies) != 2 {
		t.Errorf("batch replies = %d, want 2 (notification excluded)", len(replies))
	}
}

func TestBatchAllNotifications(t *testing.T) {
	rec := serve(t, http.MethodPost, `[{"jsonrpc":"2.0","method":"notifications/initialized"}]`, nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestNonPostRejected(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodHead} {
		rec := serve(t, m, "", nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, rec.Code)
		}
	}
	rec := serve(t, http.MethodGet, "", nil)
	if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want POST", allow)
	}
}

func TestDecode(t *testing.T) {
	if Decode(nil) != nil {
		t.Error("Decode(nil) != nil")
	}
	if Decode([]byte("{corrupt")) != nil {
		t.Error("Decode(corrupt) != nil")
	}
	s := Decode([]byte(`{"instructions":"hi","toolsResult":{"tools":[]}}`))
	if s == nil || s.Instructions != "hi" {
		t.Errorf("Decode = %+v", s)
	}
}

// fakeLocal imitates the local MCP server's transport for probe tests: one
// JSON-RPC request per POST, answered by method.
func fakeLocal(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("probe sent unparsable body: %v", err)
		}
		var res any
		switch req.Method {
		case "initialize":
			res = map[string]any{
				"protocolVersion": latestProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "bluesnake", "version": "1.2.3"},
				"instructions":    "live instructions",
			}
		case "tools/list":
			res = map[string]any{"tools": []map[string]any{{"name": "start_crawl", "inputSchema": map[string]any{}}}}
		default:
			t.Errorf("probe called unexpected method %q", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(marshalResponse(req.ID, res, nil))
	}))
}

func TestProbe(t *testing.T) {
	local := fakeLocal(t)
	defer local.Close()
	addr := strings.TrimPrefix(local.URL, "http://")

	raw, err := Probe(context.Background(), func() (net.Conn, error) {
		return net.Dial("tcp", addr)
	}, "abc.t.snake.blue")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	snap := Decode(raw)
	if snap == nil {
		t.Fatal("snapshot did not round-trip")
	}
	if snap.Instructions != "live instructions" {
		t.Errorf("Instructions = %q", snap.Instructions)
	}
	if !strings.Contains(string(snap.ServerInfo), "1.2.3") {
		t.Errorf("ServerInfo = %s", snap.ServerInfo)
	}
	if !strings.Contains(string(snap.ToolsResult), "start_crawl") {
		t.Errorf("ToolsResult = %s", snap.ToolsResult)
	}
}

func TestProbeErrorPaths(t *testing.T) {
	// Local server down: dial fails.
	if _, err := Probe(context.Background(), func() (net.Conn, error) {
		return nil, context.DeadlineExceeded
	}, "abc.t.snake.blue"); err == nil {
		t.Error("Probe with failing dial should error")
	}

	// Local server answers with an RPC error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, _ = w.Write(marshalResponse(req.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "nope"}))
	}))
	defer bad.Close()
	addr := strings.TrimPrefix(bad.URL, "http://")
	if _, err := Probe(context.Background(), func() (net.Conn, error) {
		return net.Dial("tcp", addr)
	}, "abc.t.snake.blue"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Probe rpc-error err = %v", err)
	}
}
