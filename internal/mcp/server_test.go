package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	return NewServer(NewRunner(dir), "test"), dir
}

// rpc sends one request through Handle and returns the decoded response.
func rpc(t *testing.T, s *Server, id int, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out := s.Handle(context.Background(), raw)
	if out == nil {
		t.Fatalf("no response for %s", method)
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("bad response JSON: %v", err)
	}
	return resp
}

// callTool runs tools/call and returns the text content and isError flag.
func callTool(t *testing.T, s *Server, name string, args any) (string, bool) {
	t.Helper()
	resp := rpc(t, s, 7, "tools/call", map[string]any{"name": name, "arguments": args})
	if e, ok := resp["error"]; ok && e != nil {
		t.Fatalf("tools/call %s: protocol error %v", name, e)
	}
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	isErr, _ := result["isError"].(bool)
	return text, isErr
}

func seedCrawl(t *testing.T, dir string) string {
	t.Helper()
	st, err := store.CreateCrawl(dir, "proj", []string{"https://example.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInitializeNegotiation(t *testing.T) {
	s, _ := testServer(t)
	resp := rpc(t, s, 1, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	result := resp["result"].(map[string]any)
	if got := result["protocolVersion"]; got != "2025-03-26" {
		t.Errorf("supported version not echoed: got %v", got)
	}
	if name := result["serverInfo"].(map[string]any)["name"]; name != "bluesnake" {
		t.Errorf("server name = %v", name)
	}
	if result["instructions"] == "" {
		t.Error("instructions missing")
	}

	resp = rpc(t, s, 2, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	if got := resp["result"].(map[string]any)["protocolVersion"]; got != latestProtocol {
		t.Errorf("unsupported version: got %v, want %s", got, latestProtocol)
	}
}

func TestToolsList(t *testing.T) {
	s, _ := testServer(t)
	resp := rpc(t, s, 1, "tools/list", nil)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		tl := raw.(map[string]any)
		names[tl["name"].(string)] = true
		if tl["description"] == "" {
			t.Errorf("tool %v has no description", tl["name"])
		}
		if _, ok := tl["inputSchema"].(map[string]any); !ok {
			t.Errorf("tool %v has no inputSchema", tl["name"])
		}
	}
	want := []string{
		"list_config_options", "list_profiles", "get_profile_config",
		"start_crawl", "crawl_status", "pause_crawl", "resume_crawl", "stop_crawl",
		"list_crawls", "get_database_schema", "query", "issue_summary",
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing tool %s", n)
		}
	}
	if len(tools) != len(want) {
		t.Errorf("tool count = %d, want %d", len(tools), len(want))
	}
}

func TestQueryTool(t *testing.T) {
	s, dir := testServer(t)
	id := seedCrawl(t, dir)

	// crawl_id omitted resolves to the most recent crawl
	text, isErr := callTool(t, s, "query", map[string]any{"sql": "SELECT key FROM meta ORDER BY key"})
	if isErr {
		t.Fatalf("query failed: %s", text)
	}
	for _, key := range []string{"config", "mode", "seed", id} {
		if !strings.Contains(text, key) {
			t.Errorf("query result missing %q: %s", key, text)
		}
	}

	// writes must not pass the read-only connection
	text, isErr = callTool(t, s, "query", map[string]any{"sql": "INSERT INTO meta(key,value) VALUES('x','y')", "crawl_id": id})
	if !isErr {
		t.Fatalf("INSERT over the query tool succeeded: %s", text)
	}

	// row cap is honoured and reported
	text, isErr = callTool(t, s, "query", map[string]any{"sql": "SELECT key FROM meta", "max_rows": 1})
	if isErr {
		t.Fatalf("capped query failed: %s", text)
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation note: %s", text)
	}

	// unknown crawl id is a tool error, not a panic
	if _, isErr = callTool(t, s, "query", map[string]any{"sql": "SELECT 1", "crawl_id": "no-such-crawl"}); !isErr {
		t.Error("unknown crawl id should error")
	}
	// bad SQL surfaces sqlite's message for self-correction
	if text, isErr = callTool(t, s, "query", map[string]any{"sql": "SELECT FROM"}); !isErr || !strings.Contains(text, "sqlite") {
		t.Errorf("bad SQL: isErr=%v text=%s", isErr, text)
	}
}

func TestGetDatabaseSchema(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	text, isErr := callTool(t, s, "get_database_schema", map[string]any{})
	if isErr {
		t.Fatalf("schema failed: %s", text)
	}
	for _, want := range []string{"CREATE TABLE", "pages", "links", "issues", "json_extract"} {
		if !strings.Contains(text, want) {
			t.Errorf("schema missing %q", want)
		}
	}
}

func TestCrawlStatusAndList(t *testing.T) {
	s, dir := testServer(t)

	if _, isErr := callTool(t, s, "crawl_status", map[string]any{}); !isErr {
		t.Error("crawl_status with no crawls should error")
	}

	id := seedCrawl(t, dir)
	text, isErr := callTool(t, s, "crawl_status", map[string]any{})
	if isErr || !strings.Contains(text, id) {
		t.Errorf("crawl_status: isErr=%v text=%s", isErr, text)
	}
	text, isErr = callTool(t, s, "list_crawls", map[string]any{})
	if isErr || !strings.Contains(text, id) || !strings.Contains(text, "https://example.com/") {
		t.Errorf("list_crawls: isErr=%v text=%s", isErr, text)
	}
}

func TestIssueSummaryEmptyCrawl(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	text, isErr := callTool(t, s, "issue_summary", map[string]any{})
	if isErr {
		t.Fatalf("issue_summary failed: %s", text)
	}
	if !strings.Contains(text, "totals") || !strings.Contains(text, "checks") {
		t.Errorf("issue_summary shape: %s", text)
	}
}

// TestIssueSummaryCountsDistinctURLs pins that issue_summary reports affected
// URLs, not raw occurrence rows: a page storing the same issue id with two
// distinct details plus a second page with one detail is 2 affected URLs, not 3.
func TestIssueSummaryCountsDistinctURLs(t *testing.T) {
	s, dir := testServer(t)
	st, err := store.CreateCrawl(dir, "proj", []string{"https://example.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.SaveIssues([]issues.Occurrence{
		{URL: "https://example.com/r", IssueID: "structured_validation_error", Detail: "Recipe: missing required property name"},
		{URL: "https://example.com/r", IssueID: "structured_validation_error", Detail: "Recipe: missing required property image"},
		{URL: "https://example.com/a", IssueID: "structured_validation_error", Detail: "Article: missing author"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, s, "issue_summary", map[string]any{"crawl_id": id})
	if isErr {
		t.Fatalf("issue_summary failed: %s", text)
	}
	var out struct {
		Checks []struct {
			ID           string `json:"id"`
			URLsAffected int    `json:"urls_affected"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode issue_summary: %v\n%s", err, text)
	}
	got := -1
	for _, c := range out.Checks {
		if c.ID == "structured_validation_error" {
			got = c.URLsAffected
		}
	}
	if got != 2 {
		t.Errorf("structured_validation_error urls_affected = %d, want 2 (distinct URLs, not 3 detail rows)", got)
	}
}

func TestListConfigOptions(t *testing.T) {
	s, _ := testServer(t)
	text, isErr := callTool(t, s, "list_config_options", map[string]any{})
	if isErr {
		t.Fatalf("list_config_options failed: %s", text)
	}
	def := config.Default()
	if !strings.Contains(text, `"speed.max_threads"`) {
		t.Error("missing speed.max_threads")
	}
	if !strings.Contains(text, fmt.Sprintf(`"default": %d`, def.Speed.MaxThreads)) {
		t.Errorf("speed.max_threads default %d not in catalogue", def.Speed.MaxThreads)
	}
	if strings.Contains(text, "extraction.pdf") || strings.Contains(text, `"storage.dir"`) {
		t.Error("catalogue lists knobs the engine ignores")
	}

	text, isErr = callTool(t, s, "list_config_options", map[string]any{"section": "speed"})
	if isErr || strings.Contains(text, `"key": "limits.max_urls"`) {
		t.Errorf("section filter leaked other sections: %s", text)
	}
	if _, isErr = callTool(t, s, "list_config_options", map[string]any{"section": "nope"}); !isErr {
		t.Error("unknown section should error")
	}
}

func TestStartCrawlValidation(t *testing.T) {
	s, _ := testServer(t)
	if text, isErr := callTool(t, s, "start_crawl", map[string]any{"url": "example.com"}); !isErr || !strings.Contains(text, "http") {
		t.Errorf("bad URL accepted: %s", text)
	}
	if _, isErr := callTool(t, s, "start_crawl", map[string]any{"mode": "list"}); !isErr {
		t.Error("list mode without urls accepted")
	}
	if text, isErr := callTool(t, s, "start_crawl", map[string]any{
		"url": "https://example.com/", "config": map[string]any{"speed.max_threads": "lots"},
	}); !isErr {
		t.Errorf("bad config override accepted: %s", text)
	}
	if text, isErr := callTool(t, s, "start_crawl", map[string]any{
		"url": "https://example.com/", "profile": "no such profile",
	}); !isErr || !strings.Contains(text, "profile") {
		t.Errorf("unknown profile accepted: %s", text)
	}
}

func TestPauseStopWithoutCrawl(t *testing.T) {
	s, _ := testServer(t)
	if text, isErr := callTool(t, s, "pause_crawl", nil); !isErr || !strings.Contains(text, "no crawl") {
		t.Errorf("pause: %v %s", isErr, text)
	}
	if text, isErr := callTool(t, s, "stop_crawl", nil); !isErr || !strings.Contains(text, "no crawl") {
		t.Errorf("stop: %v %s", isErr, text)
	}
}

func TestGetProfileConfigDefaults(t *testing.T) {
	s, _ := testServer(t)
	text, isErr := callTool(t, s, "get_profile_config", map[string]any{})
	if isErr || !strings.Contains(text, "max_threads:") {
		t.Errorf("profile config: isErr=%v %s", isErr, text)
	}
	if _, isErr := callTool(t, s, "get_profile_config", map[string]any{"profile": "ghost"}); !isErr {
		t.Error("unknown profile should error")
	}
}

func TestNotificationsAndUnknownMethod(t *testing.T) {
	s, _ := testServer(t)
	if out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); out != nil {
		t.Errorf("notification produced a reply: %s", out)
	}
	resp := rpc(t, s, 9, "no/such/method", nil)
	errObj, ok := resp["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method: %v", resp["error"])
	}
}

func TestBatchRequests(t *testing.T) {
	s, _ := testServer(t)
	out := s.Handle(context.Background(), []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","id":2,"method":"tools/list"}
	]`))
	var replies []map[string]any
	if err := json.Unmarshal(out, &replies); err != nil || len(replies) != 2 {
		t.Fatalf("batch: %v (%s)", err, out)
	}
}

func TestHTTPHandler(t *testing.T) {
	s, _ := testServer(t)
	h := s.HTTPHandler()

	post := func(body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, ""); w.Code != 200 ||
		w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("POST ping: %d %s", w.Code, w.Body.String())
	}
	if w := post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, ""); w.Code != 202 {
		t.Errorf("notification: want 202, got %d", w.Code)
	}
	if w := post(`{}`, "https://evil.example"); w.Code != 403 {
		t.Errorf("foreign origin: want 403, got %d", w.Code)
	}
	if w := post(`{"jsonrpc":"2.0","id":1,"method":"ping"}`, "http://localhost:5173"); w.Code != 200 {
		t.Errorf("localhost origin: want 200, got %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/mcp", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("GET: want 405, got %d", w.Code)
	}
}
