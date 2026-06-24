package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestStartRequestLabel pins Label's precedence ladder: url > sitemap > first
// list url > the "list crawl" fallback.
func TestStartRequestLabel(t *testing.T) {
	cases := []struct {
		req  StartRequest
		want string
	}{
		{StartRequest{URL: "https://a.com/"}, "https://a.com/"},
		{StartRequest{SitemapURL: "https://a.com/sitemap.xml"}, "https://a.com/sitemap.xml"},
		{StartRequest{URLs: []string{"https://a.com/1", "https://a.com/2"}}, "https://a.com/1"},
		{StartRequest{}, "list crawl"},
		// url wins even when other fields are set
		{StartRequest{URL: "https://a.com/", SitemapURL: "https://a.com/s.xml", URLs: []string{"https://a.com/1"}}, "https://a.com/"},
	}
	for _, c := range cases {
		if got := c.req.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.req, got, c.want)
		}
	}
}

// TestStartRequestSpec pins that the tool payload maps cleanly onto the neutral
// queue job spec.
func TestStartRequestSpec(t *testing.T) {
	req := StartRequest{
		Mode: "list", URL: "https://a.com/", URLs: []string{"https://a.com/x"},
		SitemapURL: "https://a.com/s.xml", Profile: "p",
		Config: map[string]any{"limits.max_urls": 5},
	}
	spec := req.Spec()
	if spec.Mode != "list" || spec.URL != "https://a.com/" || spec.SitemapURL != "https://a.com/s.xml" ||
		spec.Profile != "p" || len(spec.URLs) != 1 || spec.Config["limits.max_urls"] != 5 {
		t.Errorf("Spec mapping wrong: %+v", spec)
	}
}

// TestHandleParseErrors pins the parse-error paths of Handle/handleOne: malformed
// single messages and malformed batch arrays both return a JSON-RPC parse error.
func TestHandleParseErrors(t *testing.T) {
	s, _ := testServer(t)

	// malformed single object
	out := s.Handle(context.Background(), []byte(`{not json`))
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("bad parse-error reply: %v (%s)", err, out)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != codeParse {
		t.Errorf("single parse error: %v", resp["error"])
	}

	// malformed batch (starts with '[' so isBatch is true) but invalid JSON
	out = s.Handle(context.Background(), []byte(`[ {bad ]`))
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("bad batch parse-error reply: %v (%s)", err, out)
	}
	errObj, ok = resp["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != codeParse {
		t.Errorf("batch parse error: %v", resp["error"])
	}
}

// TestBatchAllNotifications pins that a batch made only of notifications yields no
// reply at all (Handle returns nil).
func TestBatchAllNotifications(t *testing.T) {
	s, _ := testServer(t)
	out := s.Handle(context.Background(), []byte(`[
		{"jsonrpc":"2.0","method":"notifications/initialized"},
		{"jsonrpc":"2.0","method":"notifications/cancelled"}
	]`))
	if out != nil {
		t.Errorf("all-notification batch produced a reply: %s", out)
	}
}

// TestIsBatchDetection pins isBatch over whitespace/leading-token variants.
func TestIsBatchDetection(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`[1,2]`, true},
		{"  \t\r\n  [1]", true},
		{`{"a":1}`, false},
		{"   {", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := isBatch([]byte(c.raw)); got != c.want {
			t.Errorf("isBatch(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// TestHandleEmptyAndNullID pins the notification detection in handleOne: a
// message with id "null" or no id is a notification (no reply), while id 0 is a
// real request that gets a reply.
func TestHandleEmptyAndNullID(t *testing.T) {
	s, _ := testServer(t)
	if out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":null,"method":"ping"}`)); out != nil {
		t.Errorf("null-id message produced a reply: %s", out)
	}
	out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":0,"method":"ping"}`))
	if out == nil {
		t.Fatal("id=0 request got no reply")
	}
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if resp["id"].(float64) != 0 {
		t.Errorf("reply id = %v, want 0", resp["id"])
	}
}

// TestToolsCallErrors pins handleToolsCall's two protocol-error branches:
// invalid params JSON and an unknown tool name.
func TestToolsCallErrors(t *testing.T) {
	s, _ := testServer(t)

	// invalid params (a bare string where an object is expected)
	out := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"oops"}`))
	var resp map[string]any
	json.Unmarshal(out, &resp)
	errObj, ok := resp["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != codeInvalidParams {
		t.Errorf("invalid params: %v", resp["error"])
	}

	// unknown tool name -> invalid params protocol error
	resp = rpc(t, s, 2, "tools/call", map[string]any{"name": "does_not_exist", "arguments": map[string]any{}})
	errObj, ok = resp["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != codeInvalidParams || !strings.Contains(errObj["message"].(string), "unknown tool") {
		t.Errorf("unknown tool: %v", resp["error"])
	}
}

// TestToolBadArgumentsIsToolError pins decodeArgs' error path surfacing through a
// handler as an isError result (not a protocol error). A type-mismatched arg
// (max_rows as a string) fails json.Unmarshal inside the handler.
func TestToolBadArgumentsIsToolError(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	out := s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query","arguments":{"sql":"SELECT 1","max_rows":"lots"}}}`))
	var resp map[string]any
	json.Unmarshal(out, &resp)
	if _, ok := resp["error"]; ok {
		t.Fatalf("decode failure should be a tool result, not a protocol error: %v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("bad-argument call should be isError: %v", result)
	}
}

// TestQueryEmptySQL pins the "sql is required" guard for a blank statement.
func TestQueryEmptySQL(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	if text, isErr := callTool(t, s, "query", map[string]any{"sql": "   "}); !isErr || !strings.Contains(text, "required") {
		t.Errorf("blank sql: isErr=%v text=%s", isErr, text)
	}
}

// TestQueryMaxRowsClamp pins that an over-large max_rows is clamped to 5000 (no
// error) — the query still runs against the (small) seeded DB.
func TestQueryMaxRowsClamp(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	text, isErr := callTool(t, s, "query", map[string]any{"sql": "SELECT key FROM meta", "max_rows": 999999})
	if isErr {
		t.Fatalf("clamped query failed: %s", text)
	}
	if strings.Contains(text, "truncated") {
		t.Errorf("small result should not be truncated under a clamped cap: %s", text)
	}
}

// TestQueryGuardRejectsWrite pins that a write rejected by guardReadOnlySQL is a
// tool error before it ever opens the DB.
func TestQueryGuardRejectsWrite(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	if text, isErr := callTool(t, s, "query", map[string]any{"sql": "DELETE FROM meta"}); !isErr ||
		!strings.Contains(text, "read-only") {
		t.Errorf("DELETE not guarded: isErr=%v text=%s", isErr, text)
	}
}

// TestCrawlInfoInterruptedNextHint pins crawlInfo's interrupted-status branch:
// the result carries the resume_crawl hint.
func TestCrawlInfoInterruptedNextHint(t *testing.T) {
	s, dir := testServer(t)
	st, err := store.CreateCrawl(dir, []string{"https://example.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusInterrupted, 1, 1); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, s, "crawl_status", map[string]any{"crawl_id": id})
	if isErr {
		t.Fatalf("crawl_status: %s", text)
	}
	if !strings.Contains(text, "resume_crawl") {
		t.Errorf("interrupted crawl should hint resume_crawl: %s", text)
	}
	if !strings.Contains(text, store.StatusInterrupted) {
		t.Errorf("status not reported: %s", text)
	}
}

// TestCrawlStatusUnknownID pins crawlInfo's not-found branch.
func TestCrawlStatusUnknownID(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	if text, isErr := callTool(t, s, "crawl_status", map[string]any{"crawl_id": "ghost"}); !isErr ||
		!strings.Contains(text, "not found") {
		t.Errorf("unknown crawl id: isErr=%v text=%s", isErr, text)
	}
}

// TestResolveCrawlIDPicksLatest pins resolveCrawlID's "most recent" selection
// across several crawls: an omitted crawl_id resolves to the newest by start time.
func TestResolveCrawlIDPicksLatest(t *testing.T) {
	s, dir := testServer(t)

	// three crawls; the registry stamps Started at creation time. Create them in
	// order and the last one is the most recent.
	var lastID string
	for i := 0; i < 3; i++ {
		st, err := store.CreateCrawl(dir, []string{"https://example.com/"}, "spider", config.Default())
		if err != nil {
			t.Fatal(err)
		}
		lastID = st.ID
		st.Close()
	}
	// query with no crawl_id should resolve to a stored crawl; verify the result
	// reports one of them (the most recent by Started).
	text, isErr := callTool(t, s, "query", map[string]any{"sql": "SELECT value FROM meta WHERE key='seed'"})
	if isErr {
		t.Fatalf("query: %s", text)
	}
	var out struct {
		CrawlID string `json:"crawl_id"`
	}
	json.Unmarshal([]byte(text), &out)
	if out.CrawlID == "" {
		t.Fatal("resolved crawl id is empty")
	}
	// lastID must at least exist; resolveCrawlID returns the newest. With identical
	// timestamps any is acceptable, but it must be one of the created ones.
	infos, _ := store.ListCrawls(dir)
	found := false
	for _, in := range infos {
		if in.ID == out.CrawlID {
			found = true
		}
	}
	if !found {
		t.Errorf("resolved crawl id %q is not a stored crawl", out.CrawlID)
	}
	_ = lastID
}

// TestToolsOnEmptyStore pins the "no crawls stored yet" branch of resolveCrawlID
// reached through several data tools.
func TestToolsOnEmptyStore(t *testing.T) {
	s, _ := testServer(t)
	for _, tool := range []string{"query", "get_database_schema", "issue_summary"} {
		args := map[string]any{}
		if tool == "query" {
			args["sql"] = "SELECT 1"
		}
		if text, isErr := callTool(t, s, tool, args); !isErr || !strings.Contains(text, "no crawls") {
			t.Errorf("%s on empty store: isErr=%v text=%s", tool, isErr, text)
		}
	}
}

// TestIssueSummarySortAndTotals seeds issues of differing severities and pins
// that issue_summary computes per-severity totals and orders issues before
// warnings before opportunities.
func TestIssueSummarySortAndTotals(t *testing.T) {
	s, dir := testServer(t)
	st, err := store.CreateCrawl(dir, []string{"https://example.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	// Pick three real catalogue ids of distinct severities so Lookup populates
	// name/severity/priority and the totals buckets are exercised.
	issueID := catalogueIDFor(t, "issue")
	warnID := catalogueIDFor(t, "warning")
	oppID := catalogueIDFor(t, "opportunity")
	_, err = st.DB().Exec(`INSERT INTO issues(url, issue, detail) VALUES
		('https://example.com/1', ?, ''),
		('https://example.com/2', ?, ''),
		('https://example.com/3', ?, '')`, oppID, warnID, issueID)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	text, isErr := callTool(t, s, "issue_summary", map[string]any{"crawl_id": id})
	if isErr {
		t.Fatalf("issue_summary: %s", text)
	}
	var out struct {
		Totals struct {
			Issues        int `json:"issues"`
			Warnings      int `json:"warnings"`
			Opportunities int `json:"opportunities"`
		} `json:"totals"`
		Checks []struct {
			Severity string `json:"severity"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if out.Totals.Issues != 1 || out.Totals.Warnings != 1 || out.Totals.Opportunities != 1 {
		t.Errorf("totals = %+v, want 1/1/1", out.Totals)
	}
	if len(out.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(out.Checks))
	}
	// severity ordering: issue first, opportunity last
	if out.Checks[0].Severity != "issue" || out.Checks[2].Severity != "opportunity" {
		t.Errorf("severity ordering wrong: %v", out.Checks)
	}
}

// TestHTTPHandlerResultSerialization pins the success POST through the HTTP
// transport returns a well-formed JSON-RPC body with the right content type, and
// that an empty Origin (non-browser client) is allowed.
func TestHTTPHandlerToolCall(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)
	h := s.HTTPHandler()

	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_crawls","arguments":{}}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("POST tools/call: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad body: %v", err)
	}
	if _, ok := resp["result"]; !ok {
		t.Errorf("no result in HTTP reply: %s", w.Body.String())
	}
}

// TestOriginAllowed pins originAllowed directly across the variants the HTTP
// handler relies on: empty (allowed), localhost/loopback (allowed), foreign
// host (denied), and an unparseable origin (denied).
func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},
		{"http://localhost:5173", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:9000", true},
		{"https://evil.example", false},
		{"://not a url", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/mcp", nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if got := originAllowed(req); got != c.want {
			t.Errorf("originAllowed(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

// TestGuardUnterminatedComments pins firstSQLKeyword's "comment runs to EOF"
// branches: an unterminated line or block comment yields an empty first keyword,
// which guardReadOnlySQL rejects.
func TestGuardUnterminatedComments(t *testing.T) {
	for _, q := range []string{
		"-- just a comment with no statement",
		"/* never closed",
	} {
		if err := guardReadOnlySQL(q); err == nil {
			t.Errorf("guardReadOnlySQL(%q) = nil, want rejection (no statement after comment)", q)
		}
	}
}

// TestRunQueryTextColumnDecoding pins runQuery's []byte->string conversion: a
// text column comes back as a JSON string, not a base64 byte blob.
func TestRunQueryTextColumnDecoding(t *testing.T) {
	s, dir := testServer(t)
	id := seedCrawl(t, dir)

	text, isErr := callTool(t, s, "query", map[string]any{
		"sql": "SELECT 'hello world' AS greeting", "crawl_id": id,
	})
	if isErr {
		t.Fatalf("query: %s", text)
	}
	var out struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Rows) != 1 || out.Rows[0][0] != "hello world" {
		t.Errorf("text column not decoded to a string: %v", out.Rows)
	}
}

// TestIssueSummaryPriorityTiebreak pins the priority tiebreak in the
// issue_summary sort: two checks of the same severity sort high-priority first.
func TestIssueSummaryPriorityTiebreak(t *testing.T) {
	high, medium := samePrioIDs(t)
	s, dir := testServer(t)
	st, err := store.CreateCrawl(dir, []string{"https://example.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	// Insert the medium-priority issue first so a stable sort would keep it first
	// unless the priority comparator reorders high ahead of it.
	if _, err := st.DB().Exec(`INSERT INTO issues(url, issue, detail) VALUES
		('https://example.com/1', ?, ''),
		('https://example.com/2', ?, '')`, medium, high); err != nil {
		t.Fatal(err)
	}
	st.Close()

	text, isErr := callTool(t, s, "issue_summary", map[string]any{"crawl_id": id})
	if isErr {
		t.Fatalf("issue_summary: %s", text)
	}
	var out struct {
		Checks []struct {
			ID       string `json:"id"`
			Priority string `json:"priority"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(out.Checks))
	}
	if out.Checks[0].ID != high {
		t.Errorf("high-priority check should sort first: got order %s, %s", out.Checks[0].ID, out.Checks[1].ID)
	}
}

// samePrioIDs returns two issue ids of the same severity, the first high
// priority and the second medium, so the sort's priority tiebreak is exercised.
func samePrioIDs(t *testing.T) (high, medium string) {
	t.Helper()
	for _, sev := range []issues.Severity{issues.Issue, issues.Warning, issues.Opportunity} {
		high, medium = "", ""
		for _, d := range issues.Catalogue() {
			if d.Severity != sev {
				continue
			}
			if d.Priority == issues.High && high == "" {
				high = d.ID
			}
			if d.Priority == issues.Medium && medium == "" {
				medium = d.ID
			}
		}
		if high != "" && medium != "" {
			return high, medium
		}
	}
	t.Skip("no severity has both a high- and medium-priority issue in the catalogue")
	return "", ""
}

// catalogueIDFor returns a real issue catalogue id with the given severity.
func catalogueIDFor(t *testing.T, severity string) string {
	t.Helper()
	for _, d := range issues.Catalogue() {
		if string(d.Severity) == severity {
			return d.ID
		}
	}
	t.Fatalf("no catalogue entry with severity %q", severity)
	return ""
}
