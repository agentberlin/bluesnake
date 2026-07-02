package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// These tests drive the real `bluesnake resume` command in-process (cobra
// Execute), pinning the CLI resume contract that used to live only in a
// direct crawler.New call site the test suite never exercised (#74 R1/R4):
// exit codes, the pre-edges refusal, and --force config persistence.

// runCmd executes the root command with args, returning combined output and
// the process exit code the main() contract would produce.
func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return buf.String(), 0
	}
	code := 1
	var ee exitErr
	if errors.As(err, &ee) {
		code = ee.code
	}
	return buf.String(), code
}

// leafServer serves a one-page site (every path is a plain leaf), so a forged
// pending frontier row resolves to a real fetchable URL.
func leafServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Leaf</title></head><body><p>leaf</p></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// forgeInterrupted builds a stored interrupted crawl: one crawled page (the
// seed, carrying non-zero inlinks/discovered_from aggregates) and one pending
// frontier row (/next on the fixture server).
func forgeInterrupted(t *testing.T, dir string, srv *httptest.Server) string {
	t.Helper()
	cfg := config.Default()
	cfg.Speed.MaxThreads = 1
	seed := srv.URL + "/"
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := &crawler.PageRecord{
		URL: seed, Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Facts:      &parse.Facts{Links: []parse.Link{{Type: parse.Hyperlink, URL: srv.URL + "/next"}}},
		GatedEdges: []crawler.GatedEdge{{Dst: srv.URL + "/next", Hyperlink: true, Seq: 1}},
	}
	if err := st.Page(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Admit(frontier.Item{URL: srv.URL + "/next", Depth: 1, Source: seed}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(dir, st.ID, store.StatusInterrupted, 1, 1); err != nil {
		t.Fatal(err)
	}
	return st.ID
}

// pageAggregates reads (inlinks, discovered_from) for one URL.
func pageAggregates(t *testing.T, dir, id, url string) (int, string) {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	rec := pages[url]
	if rec == nil {
		t.Fatalf("%s not in store", url)
	}
	return rec.Inlinks, rec.DiscoveredFrom
}

func TestResumeCmd_OverrideWithoutForceRefused(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)
	out, code := runCmd(t, "resume", id, "--store-dir", dir, "--set", "speed.max_threads=9")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("output does not mention --force:\n%s", out)
	}
}

func TestResumeCmd_UnknownCrawlIsConfigError(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "resume", "no-such-crawl", "--store-dir", dir)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (open error)", code)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("output does not name the missing crawl:\n%s", out)
	}
}

// TestResumeCmd_PreEdgesRefusedNotCorrupted is the #74 R1 pin: the CLI resume
// of a crawl that predates the gated edges table must REFUSE (the runner path
// already did) instead of running finalize over the empty edges table and
// zeroing every page's inlinks/discovered_from.
func TestResumeCmd_PreEdgesRefusedNotCorrupted(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)

	// Forge the pre-edges state the v4 forward-migration produces, with non-zero
	// aggregates that corruption would visibly destroy.
	func() {
		st, err := store.OpenCrawl(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, q := range []string{
			`INSERT OR REPLACE INTO meta(key, value) VALUES('pre_edges','1')`,
			`DELETE FROM edges`,
			`UPDATE pages SET inlinks = 7, discovered_from = 'forged://src'`,
		} {
			if _, err := st.DB().Exec(q); err != nil {
				t.Fatalf("forge: %v", err)
			}
		}
	}()

	out, code := runCmd(t, "resume", id, "--store-dir", dir)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (refusal)", code)
	}
	if !strings.Contains(out, "re-crawl") {
		t.Errorf("output does not carry the re-crawl refusal:\n%s", out)
	}
	inlinks, from := pageAggregates(t, dir, id, srv.URL+"/")
	if inlinks != 7 || from != "forged://src" {
		t.Errorf("stored aggregates corrupted by a refused resume: inlinks=%d discovered_from=%q, want 7/forged://src",
			inlinks, from)
	}
}

// TestResumeCmd_CompletedCrawlRefused pins #74 N9: a completed crawl has
// nothing to resume; accepting it briefly de-completes the registry row.
func TestResumeCmd_CompletedCrawlRefused(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)
	if err := store.SetStatus(dir, id, store.StatusCompleted, 1, 1); err != nil {
		t.Fatal(err)
	}
	out, code := runCmd(t, "resume", id, "--store-dir", dir)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (refusal), output:\n%s", code, out)
	}
	// The refusal must leave the registry row untouched (still completed).
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == id && in.Status != store.StatusCompleted {
			t.Errorf("registry status = %q after refused resume, want completed", in.Status)
		}
	}
}

// TestResumeCmd_ForcePersistsConfig pins the --force contract: the validated
// override becomes the crawl's frozen config BEFORE the resume runs, so the
// crawl carries one durable config across this and any later resume.
func TestResumeCmd_ForcePersistsConfig(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)

	out, code := runCmd(t, "resume", id, "--store-dir", dir, "--force", "--set", "speed.max_threads=7")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, output:\n%s", code, out)
	}
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfgYAML, err := st.Meta("config")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		t.Fatalf("frozen config no longer loads after --force: %v", err)
	}
	if cfg.Speed.MaxThreads != 7 {
		t.Errorf("frozen config max_threads = %d, want the --force override 7 persisted", cfg.Speed.MaxThreads)
	}
}

// TestResumeCmd_HappyPathCompletes pins the whole-path contract: a genuine
// interrupted crawl resumed via the CLI drains, finalises and exits 0.
func TestResumeCmd_HappyPathCompletes(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv)

	out, code := runCmd(t, "resume", id, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, output:\n%s", code, out)
	}
	if !strings.Contains(out, "crawled") {
		t.Errorf("summary missing from output:\n%s", out)
	}
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range infos {
		if in.ID == id {
			found = true
			if in.Status != store.StatusCompleted {
				t.Errorf("registry status = %q, want completed", in.Status)
			}
		}
	}
	if !found {
		t.Fatalf("crawl %s missing from registry", id)
	}
}
