package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// handlerFixtureServer is the same tiny deterministic site used elsewhere, kept
// local so this file is self-contained.
func handlerFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body><a href="/a">a</a></body></html>`))
		default:
			_, _ = w.Write([]byte(`<html><head><title>P</title></head><body><p>x</p></body></html>`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// slowHandlerSite serves a home page (returned immediately, so the crawler always
// produces a real result and finalises correctly) linking to several children
// that are each delayed ~120ms. The delay keeps the crawl reliably in flight long
// enough for a pause/stop to catch it live, while every request still completes
// on its own (or on context cancellation), so nothing leaks into a later test.
func slowHandlerSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body>` +
				`<a href="/a">a</a> <a href="/b">b</a> <a href="/c">c</a> <a href="/d">d</a></body></html>`))
			return
		}
		select {
		case <-time.After(120 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`<html><head><title>P</title></head><body><p>x</p></body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStartCrawlToolSuccess drives the start_crawl TOOL handler (not just the
// backend method) end to end and pins that it returns the crawl_id + running
// state JSON the model relies on.
func TestStartCrawlToolSuccess(t *testing.T) {
	srv := handlerFixtureServer(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)
	s := NewServer(r, "test")

	text, isErr := callTool(t, s, "start_crawl", map[string]any{
		"url":    srv.URL + "/",
		"config": map[string]any{"speed.max_threads": 1},
	})
	if isErr {
		t.Fatalf("start_crawl tool: %s", text)
	}
	var out struct {
		CrawlID string `json:"crawl_id"`
		State   string `json:"state"`
		Next    string `json:"next"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode start_crawl: %v\n%s", err, text)
	}
	if out.CrawlID == "" || out.State != "running" || out.Next == "" {
		t.Errorf("start_crawl result = %+v", out)
	}

	// Wait for the dispatched crawl to finish (dispatcher idle); it was registered.
	settle(t, r)
	if infos, _ := store.ListCrawls(dir); len(infos) != 1 || infos[0].ID != out.CrawlID {
		t.Errorf("registry = %+v, want one crawl with id %q", infos, out.CrawlID)
	}
}

// TestPauseResumeStopToolsSuccess drives the pause_crawl / resume_crawl /
// stop_crawl TOOL handlers' success branches against a live crawl.
func TestPauseResumeStopToolsSuccess(t *testing.T) {
	srv := slowHandlerSite(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)
	s := NewServer(r, "test")

	// Start, then pause via the tool while the crawl is held at the slow children.
	startText, isErr := callTool(t, s, "start_crawl", map[string]any{
		"url": srv.URL + "/", "config": map[string]any{"speed.max_threads": 1},
	})
	if isErr {
		t.Fatalf("start_crawl: %s", startText)
	}
	var started struct {
		CrawlID string `json:"crawl_id"`
	}
	json.Unmarshal([]byte(startText), &started)

	waitFor(t, func() bool { return r.Progress() != nil }, "crawl to go live")
	text, isErr := callTool(t, s, "pause_crawl", map[string]any{})
	if isErr || !strings.Contains(text, "pausing") {
		t.Fatalf("pause_crawl tool: isErr=%v text=%s", isErr, text)
	}
	// Pause cancels the in-flight (slow) requests; the home page is already crawled,
	// so the crawl finalises as interrupted and stays resumable. Wait for the
	// dispatcher to go idle, then assert the resumable status.
	waitFor(t, func() bool { return r.Progress() == nil }, "dispatcher idle after pause")
	if infos, _ := store.ListCrawls(dir); len(infos) != 1 || infos[0].Status != store.StatusInterrupted {
		t.Fatalf("after pause: %+v, want one interrupted crawl", infos)
	}

	// Resume via the tool returns the same id in the running state.
	text, isErr = callTool(t, s, "resume_crawl", map[string]any{"crawl_id": started.CrawlID})
	if isErr {
		t.Fatalf("resume_crawl tool: %s", text)
	}
	var resumed struct {
		CrawlID string `json:"crawl_id"`
		State   string `json:"state"`
	}
	json.Unmarshal([]byte(text), &resumed)
	if resumed.CrawlID != started.CrawlID || resumed.State != "running" {
		t.Errorf("resume_crawl result = %+v", resumed)
	}

	// The resumed crawl drains; the dispatcher returns to idle.
	waitFor(t, func() bool { return r.Progress() == nil }, "resumed crawl to wind down")

	// stop_crawl with no live crawl now is a clean tool error (signal guard).
	if errText, isErr := callTool(t, s, "stop_crawl", map[string]any{}); !isErr ||
		!strings.Contains(errText, "no crawl") {
		t.Errorf("stop_crawl after the crawl ended: isErr=%v text=%s", isErr, errText)
	}
}

// TestResumeCrawlToolUnknownID pins the resume_crawl tool's error surfacing for a
// non-existent crawl id.
func TestResumeCrawlToolUnknownID(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)
	s := NewServer(r, "test")

	if text, isErr := callTool(t, s, "resume_crawl", map[string]any{"crawl_id": "ghost"}); !isErr {
		t.Errorf("resume_crawl of a missing crawl should error: %s", text)
	}
}

// TestListProfilesWithSavedProfile writes a profile YAML and pins that
// list_profiles surfaces its display name (the "# Name" header branch) and
// get_profile_config renders that named profile as YAML.
func TestListProfilesWithSavedProfile(t *testing.T) {
	s, dir := testServer(t)

	profDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "# Fast Audit\nspeed:\n  max_threads: 20\n"
	if err := os.WriteFile(filepath.Join(profDir, "fast-audit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, s, "list_profiles", map[string]any{})
	if isErr {
		t.Fatalf("list_profiles: %s", text)
	}
	if !strings.Contains(text, "Fast Audit") {
		t.Errorf("saved profile name missing from list: %s", text)
	}

	text, isErr = callTool(t, s, "get_profile_config", map[string]any{"profile": "Fast Audit"})
	if isErr {
		t.Fatalf("get_profile_config: %s", text)
	}
	if !strings.Contains(text, "max_threads: 20") {
		t.Errorf("profile config did not reflect saved value: %s", text)
	}
}

// TestListCrawlsToolShape pins list_crawls' JSON shape (count + per-crawl fields)
// across multiple crawls of differing status.
func TestListCrawlsToolShape(t *testing.T) {
	s, dir := testServer(t)

	a := seedCrawl(t, dir)
	bSt, err := store.CreateCrawl(dir, []string{"https://b.com/"}, "list", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	b := bSt.ID
	bSt.Close()
	if err := store.SetStatus(dir, b, store.StatusCompleted, 5, 7); err != nil {
		t.Fatal(err)
	}

	text, isErr := callTool(t, s, "list_crawls", map[string]any{})
	if isErr {
		t.Fatalf("list_crawls: %s", text)
	}
	var out struct {
		Count  int `json:"count"`
		Crawls []struct {
			CrawlID string `json:"crawl_id"`
			Status  string `json:"status"`
			Total   int    `json:"total"`
		} `json:"crawls"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode list_crawls: %v\n%s", err, text)
	}
	if out.Count != 2 {
		t.Fatalf("count = %d, want 2", out.Count)
	}
	seen := map[string]bool{}
	for _, c := range out.Crawls {
		seen[c.CrawlID] = true
	}
	if !seen[a] || !seen[b] {
		t.Errorf("list_crawls missing a crawl: %s", text)
	}
}

// TestDecodeArgsErrorBranches pins decodeArgs' error path across the tools that
// take typed args: feeding a wrong-typed field surfaces a tool error rather than
// a protocol error. Each call exercises a distinct handler's decode guard.
func TestDecodeArgsErrorBranches(t *testing.T) {
	s, dir := testServer(t)
	seedCrawl(t, dir)

	cases := []struct {
		tool string
		args string // raw JSON arguments object with a type-mismatched field
	}{
		{"list_config_options", `{"section": 123}`},
		{"get_profile_config", `{"profile": 5}`},
		{"crawl_status", `{"crawl_id": true}`},
		{"resume_crawl", `{"crawl_id": 9}`},
		{"get_database_schema", `{"crawl_id": []}`},
		{"issue_summary", `{"crawl_id": {}}`},
		{"start_crawl", `{"url": 7}`},
	}
	for _, c := range cases {
		raw := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + c.tool +
			`","arguments":` + c.args + `}}`
		out := s.Handle(context.Background(), []byte(raw))
		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("%s: bad reply: %v", c.tool, err)
		}
		if _, ok := resp["error"]; ok {
			t.Errorf("%s: decode failure should be a tool result, not protocol error: %v", c.tool, resp["error"])
			continue
		}
		result := resp["result"].(map[string]any)
		if isErr, _ := result["isError"].(bool); !isErr {
			t.Errorf("%s: type-mismatched args should be isError", c.tool)
		}
	}
}

// TestDecodeArgsEmptyIsNoop pins decodeArgs' early return for empty arguments: a
// tool called with null arguments still works (uses defaults).
func TestDecodeArgsEmptyIsNoop(t *testing.T) {
	s, _ := testServer(t)
	// list_config_options with no arguments at all returns the full catalogue.
	out := s.Handle(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_config_options"}}`))
	var resp map[string]any
	json.Unmarshal(out, &resp)
	result := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Errorf("list_config_options with no args should succeed: %v", result)
	}
}
