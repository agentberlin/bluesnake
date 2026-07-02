package main

// Desktop parallel multi-crawl behaviour (issue #78): with
// speed.max_concurrent_crawls > 1 in the default profile, the app's queue runs
// two crawls at once; every uiObserver event carries its crawl id so two
// concurrent crawls' progress streams and feeds never cross; PauseCrawl(id)
// pauses only the addressed crawl; and the embedded MCP backend sees both
// crawls and enforces the same capacity contract as the standalone server.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/mcp"
	"github.com/agentberlin/bluesnake/internal/store"
)

// slowBrokenSite serves a home page linking three slow children plus one
// immediately-404ing URL unique to the site — the 404 lands in the live feed,
// so a cross-crawl feed leak is detectable by URL.
func slowBrokenSite(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch {
		case r.URL.Path == "/":
			fmt.Fprintf(w, `<html><body><a href="/broken-%s">x</a> <a href="/a">a</a> <a href="/b">b</a> <a href="/c">c</a></body></html>`, tag)
		case strings.HasPrefix(r.URL.Path, "/broken-"):
			w.WriteHeader(404)
		default:
			select {
			case <-time.After(120 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `<html><body><p>x</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// eventLog captures the uiObserver's emitted events headlessly (no Wails
// runtime in tests).
type eventLog struct {
	mu     sync.Mutex
	events []struct {
		name string
		data []interface{}
	}
}

func (l *eventLog) emit(event string, data ...interface{}) {
	l.mu.Lock()
	l.events = append(l.events, struct {
		name string
		data []interface{}
	}{event, data})
	l.mu.Unlock()
}

func (l *eventLog) count(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.events {
		if e.name == name {
			n++
		}
	}
	return n
}

// lastProgressPerCrawl returns the last crawl:progress payload seen per crawl id.
func (l *eventLog) lastProgressPerCrawl() map[string]ProgressSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]ProgressSnapshot{}
	for _, e := range l.events {
		if e.name != "crawl:progress" || len(e.data) == 0 {
			continue
		}
		if ps, ok := e.data[0].(ProgressSnapshot); ok {
			out[ps.CrawlID] = ps
		}
	}
	return out
}

func waitCond(t *testing.T, cond func() bool, msg string) {
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

// newParallelApp builds a headless App over a temp store dir whose default
// profile enables two parallel crawls, with the observer's events captured.
func newParallelApp(t *testing.T) (*App, *eventLog) {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "default-audit.yaml"),
		[]byte("speed:\n  max_concurrent_crawls: 2\n  max_threads: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.storeDir = dir
	a.ensureQueue()
	log := &eventLog{}
	a.obs.emit = log.emit
	if err := a.disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.disp.Shutdown)
	return a, log
}

func TestDesktopParallelCrawlsEventsDoNotCross(t *testing.T) {
	srvA := slowBrokenSite(t, "a")
	srvB := slowBrokenSite(t, "b")
	a, log := newParallelApp(t)

	if a.queueW != 2 {
		t.Fatalf("queueW = %d, want 2 (speed.max_concurrent_crawls from the default profile)", a.queueW)
	}

	for _, u := range []string{srvA.URL, srvB.URL} {
		if _, err := a.StartCrawl(StartRequest{Mode: "spider", URL: u + "/", Threads: 1, MaxDepth: -1}); err != nil {
			t.Fatalf("StartCrawl(%s): %v", u, err)
		}
	}

	// Both crawls go live concurrently: two crawl:started events with distinct
	// ids, and RunningProgress lists both. (The executor registers a run just
	// before its OnStart fires, so wait on the events, not only the snapshots.)
	waitCond(t, func() bool { return len(a.RunningProgress()) == 2 }, "two crawls running concurrently")
	waitCond(t, func() bool { return log.count("crawl:started") == 2 }, "two crawl:started events")
	running := a.RunningProgress()
	idByHost := map[string]string{} // "a"/"b" -> crawl id
	for _, p := range running {
		switch {
		case strings.HasPrefix(p.Seed, srvA.URL):
			idByHost["a"] = p.CrawlID
		case strings.HasPrefix(p.Seed, srvB.URL):
			idByHost["b"] = p.CrawlID
		}
	}
	if idByHost["a"] == "" || idByHost["b"] == "" || idByHost["a"] == idByHost["b"] {
		t.Fatalf("running crawls = %+v, want one per site with distinct ids", running)
	}

	// The embedded MCP backend sees both crawls and rejects a third start with
	// the running ids named — the same contract as the standalone server.
	b := &desktopBackend{app: a}
	if got := len(b.Running()); got != 2 {
		t.Errorf("embedded MCP Running() = %d crawls, want 2", got)
	}
	if _, err := b.StartCrawl(context.Background(), startReqForTest(srvA.URL+"/")); err == nil ||
		!strings.Contains(err.Error(), idByHost["a"]) || !strings.Contains(err.Error(), idByHost["b"]) {
		t.Errorf("third start via embedded MCP = %v, want capacity rejection naming both crawls", err)
	}

	// Pause ONLY crawl A; B keeps running and completes on its own.
	a.PauseCrawl(idByHost["a"])
	waitCond(t, func() bool {
		running := a.RunningProgress()
		return len(running) == 1 && running[0].CrawlID == idByHost["b"]
	}, "crawl A paused while B keeps running")
	waitCond(t, func() bool { return len(a.RunningProgress()) == 0 }, "crawl B to finish")
	waitCond(t, func() bool { return log.count("crawl:done") == 2 }, "both done events")

	if got := crawlStatusIn(t, a.storeDir, idByHost["a"]); got != store.StatusInterrupted {
		t.Errorf("paused crawl A = %q, want interrupted (resumable)", got)
	}
	if got := crawlStatusIn(t, a.storeDir, idByHost["b"]); got != store.StatusCompleted {
		t.Errorf("crawl B = %q, want completed (undisturbed by A's pause)", got)
	}

	// Feeds never cross: each crawl's last progress payload carries only its own
	// site's notable URLs (each site 404s a URL unique to it).
	last := log.lastProgressPerCrawl()
	for tag, id := range idByHost {
		ps, ok := last[id]
		if !ok {
			t.Errorf("no progress event for crawl %s", id)
			continue
		}
		if ps.CrawlID != id {
			t.Errorf("progress payload for %s carries id %s", id, ps.CrawlID)
		}
		other := "b"
		if tag == "b" {
			other = "a"
		}
		for _, f := range ps.Feed {
			if strings.Contains(f.URL, "/broken-"+other) {
				t.Errorf("crawl %s's feed contains %s — another crawl's page leaked into it", id, f.URL)
			}
		}
	}
}

// startReqForTest builds the minimal MCP start payload for a seed.
func startReqForTest(url string) mcp.StartRequest {
	return mcp.StartRequest{URL: url, Config: map[string]any{"speed.max_threads": 1}}
}

// crawlStatusIn reads a crawl's stored registry status.
func crawlStatusIn(t *testing.T, dir, id string) string {
	t.Helper()
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == id {
			return in.Status
		}
	}
	return ""
}
