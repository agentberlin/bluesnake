package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// These tests drive the Runner backend (the CLI/standalone-MCP Backend) through
// the real executor + queue dispatcher against local httptest servers — fully
// hermetic, no external network.
//
// They keep the number of real dispatcher-driven crawls small and deterministic,
// and each crawl-running test winds its crawl down to a terminal state and waits
// for the dispatcher to go idle (settle) BEFORE returning, so r.Shutdown in
// cleanup never has to unwind an in-flight request and nothing leaks forward. The
// heavy crawl lifecycle (engine pause/resume/finalize internals) is covered by
// internal/runner's own tests; here we cover the MCP backend's adaptation of it.

// runnerFixtureServer is a tiny deterministic site: "/" links to /a and /b.
// Every request completes immediately, so a straight crawl finishes fast.
func runnerFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/a">a</a> <a href="/b">b</a></body></html>`)
		default:
			fmt.Fprint(w, `<html><head><title>P</title></head><body><p>x</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// slowSite serves a home page (returned immediately, so the crawler always makes
// real progress and finalises correctly) linking to several children that are
// each delayed ~120ms. The delay keeps the crawl reliably in flight for a few
// hundred ms — long enough for pause/stop to catch it live — while every request
// still completes on its own (or on request-context cancellation), so no server
// goroutine is ever left blocked to leak into a later test.
func slowSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html><head><title>Home</title></head><body>`+
				`<a href="/a">a</a> <a href="/b">b</a> <a href="/c">c</a> <a href="/d">d</a></body></html>`)
			return
		}
		select {
		case <-time.After(120 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<html><head><title>P</title></head><body><p>x</p></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitFor polls cond up to a generous failsafe deadline without sleeping the
// whole budget. A hermetic crawl settles in milliseconds, so the deadline only
// fires on a genuine hang.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// settle waits for the dispatcher to go idle (no live crawl), guaranteeing a
// clean teardown for the next test.
func settle(t *testing.T, r *Runner) {
	t.Helper()
	waitFor(t, func() bool { return r.Progress() == nil }, "dispatcher to go idle")
}

// crawlStatus returns a crawl's stored status, or "" if not yet in the registry.
func crawlStatus(t *testing.T, dir, id string) string {
	t.Helper()
	infos, _ := store.ListCrawls(dir)
	for _, in := range infos {
		if in.ID == id {
			return in.Status
		}
	}
	return ""
}

// terminal reports whether the crawl has reached a stored terminal status.
func terminal(s string) bool {
	return s == store.StatusCompleted || s == store.StatusInterrupted
}

// TestRunnerStartCrawlEndToEnd drives StartCrawl through the real executor and
// queue dispatcher against a local fixture server. It exercises the whole
// OnStart/OnPage/OnDone/settle/enqueueAndAwait path, plus the crawl_status tool's
// stored reporting once finished.
func TestRunnerStartCrawlEndToEnd(t *testing.T) {
	srv := runnerFixtureServer(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)
	s := NewServer(r, "test")

	id, err := r.StartCrawl(context.Background(), StartRequest{
		URL:    srv.URL + "/",
		Config: map[string]any{"speed.max_threads": 1},
	})
	if err != nil {
		t.Fatalf("StartCrawl: %v", err)
	}
	if id == "" {
		t.Fatal("StartCrawl returned an empty crawl id")
	}

	// Wait for the whole StartCrawl -> dispatch -> Run -> OnDone -> settle path to
	// finish (dispatcher idle). The crawl was registered under its id either way.
	settle(t, r)
	if crawlStatus(t, dir, id) == "" {
		t.Fatalf("crawl %q was never registered", id)
	}

	// crawl_status reports the stored crawl by id.
	text, isErr := callTool(t, s, "crawl_status", map[string]any{"crawl_id": id})
	if isErr {
		t.Fatalf("crawl_status: %s", text)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(text), &info); err != nil {
		t.Fatalf("decode crawl_status: %v\n%s", err, text)
	}
	if info["crawl_id"] != id {
		t.Errorf("crawl_status crawl_id = %v, want %q", info["crawl_id"], id)
	}
	if _, ok := info["status"].(string); !ok {
		t.Errorf("crawl_status missing status: %s", text)
	}
}

// TestRunnerLiveControl exercises the live-crawl control surface in one crawl:
// Progress reports a running snapshot, a second StartCrawl is rejected, and
// StopCrawl winds the crawl down. This keeps the count of real crawls low while
// covering Progress, the one-crawl-at-a-time guard, and signal -> Stop.
func TestRunnerLiveControl(t *testing.T) {
	srv := slowSite(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	id, err := r.StartCrawl(context.Background(), StartRequest{
		URL:    srv.URL + "/",
		Config: map[string]any{"speed.max_threads": 1},
	})
	if err != nil {
		t.Fatalf("StartCrawl: %v", err)
	}

	// Progress reports a live snapshot while the slow children keep the crawl running.
	var p *Progress
	waitFor(t, func() bool {
		p = r.Progress()
		return p != nil
	}, "a live progress snapshot")
	if p.CrawlID != id {
		t.Errorf("progress crawl id = %q, want %q", p.CrawlID, id)
	}
	if p.State != "running" {
		t.Errorf("progress state = %q, want running", p.State)
	}
	if p.Seed != srv.URL+"/" {
		t.Errorf("progress seed = %q, want %q", p.Seed, srv.URL+"/")
	}

	// A second start while one is live is rejected (one-crawl-at-a-time contract).
	if _, err := r.StartCrawl(context.Background(), StartRequest{
		URL: srv.URL + "/", Config: map[string]any{"speed.max_threads": 1},
	}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second StartCrawl error = %v, want 'already running'", err)
	}

	// Stop turns the live crawl around; the dispatcher returns to idle.
	if err := r.StopCrawl(); err != nil {
		t.Fatalf("StopCrawl: %v", err)
	}
	settle(t, r)
	if r.Progress() != nil {
		t.Error("Progress should be nil after the crawl is stopped")
	}
}

// TestRunnerPauseResume pauses a live crawl, then resumes it, exercising PauseCrawl
// and ResumeCrawl through the executor/dispatcher. It pins the deterministic facts
// (pause turns the crawl around to interrupted, resume returns the same id and
// re-runs it) without over-pinning the second run's exact terminal status.
func TestRunnerPauseResume(t *testing.T) {
	srv := slowSite(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	id, err := r.StartCrawl(context.Background(), StartRequest{
		URL:    srv.URL + "/",
		Config: map[string]any{"speed.max_threads": 1},
	})
	if err != nil {
		t.Fatalf("StartCrawl: %v", err)
	}
	// The home page is fetched immediately (real progress) while the slow children
	// keep the crawl running, so a pause reliably interrupts a crawl with data.
	waitFor(t, func() bool { return r.Progress() != nil }, "crawl to go live")
	if err := r.PauseCrawl(); err != nil {
		t.Fatalf("PauseCrawl: %v", err)
	}
	settle(t, r) // wait for the pause to turn the crawl around (dispatcher idle)
	if got := crawlStatus(t, dir, id); got != store.StatusInterrupted {
		t.Fatalf("paused crawl status = %q, want interrupted (resumable)", got)
	}

	resumed, err := r.ResumeCrawl(id)
	if err != nil {
		t.Fatalf("ResumeCrawl: %v", err)
	}
	if resumed != id {
		t.Errorf("resumed crawl id = %q, want %q", resumed, id)
	}
	// The resumed run drains; the dispatcher returns to idle.
	settle(t, r)
	if !terminal(crawlStatus(t, dir, id)) {
		t.Errorf("resumed crawl status = %q, want terminal", crawlStatus(t, dir, id))
	}
}

// TestRunnerStartCrawlValidationError pins that an invalid spec fails fast in
// StartCrawl (ValidateSpec) without enqueuing anything — no real crawl runs.
func TestRunnerStartCrawlValidationError(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	if _, err := r.StartCrawl(context.Background(), StartRequest{URL: "not-a-url"}); err == nil {
		t.Error("StartCrawl accepted a non-http URL")
	}
	if infos, _ := store.ListCrawls(dir); len(infos) != 0 {
		t.Errorf("rejected spec still created a crawl: %+v", infos)
	}
}

// TestRunnerStartCrawlContextCancelled pins enqueueAndAwait's ctx.Done() branch:
// a context cancelled before the crawl settles returns its error. The job may
// still get dispatched, so the test winds the dispatcher down afterwards.
func TestRunnerStartCrawlContextCancelled(t *testing.T) {
	srv := slowSite(t)
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the crawl can settle
	if _, err := r.StartCrawl(ctx, StartRequest{
		URL: srv.URL + "/", Config: map[string]any{"speed.max_threads": 1},
	}); err == nil {
		t.Fatal("StartCrawl with a cancelled context returned no error")
	}

	// A job may have been dispatched before the await observed the cancel; turn it
	// around and wait for the dispatcher to go idle so nothing leaks forward.
	if r.Progress() != nil {
		_ = r.StopCrawl()
	}
	settle(t, r)
}

// TestRunnerResumeMissingCrawl pins that ResumeCrawl on an unknown id surfaces a
// terminal error through the OnDone -> settle path rather than hanging.
func TestRunnerResumeMissingCrawl(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	if _, err := r.ResumeCrawl("no-such-crawl"); err == nil {
		t.Error("ResumeCrawl of a missing crawl returned no error")
	}
	settle(t, r)
}

// TestRunnerPauseStopNoCrawl pins the signal() guard: pausing/stopping with no
// live crawl returns "no crawl is running".
func TestRunnerPauseStopNoCrawl(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	if err := r.PauseCrawl(); err == nil || !strings.Contains(err.Error(), "no crawl") {
		t.Errorf("PauseCrawl with no crawl = %v, want 'no crawl'", err)
	}
	if err := r.StopCrawl(); err == nil || !strings.Contains(err.Error(), "no crawl") {
		t.Errorf("StopCrawl with no crawl = %v, want 'no crawl'", err)
	}
}
