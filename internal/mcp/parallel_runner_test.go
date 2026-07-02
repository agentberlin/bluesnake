package mcp

// Parallel multi-crawl behaviour of the MCP surface (issue #78): with
// speed.max_concurrent_crawls > 1 in the default profile, two start_crawls run
// concurrently with distinct ids, a start beyond capacity is rejected naming
// the running crawls, crawl_status/pause_crawl address one specific crawl (and
// an ambiguous no-id call errors with the id list), and the shared limiter
// keeps the global fetch cap process-wide across the parallel crawls (GL-08
// per-surface wiring).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// writeDefaultProfile persists a default profile into storeDir, the config the
// Runner reads its process-level wiring from at construction.
func writeDefaultProfile(t *testing.T, storeDir, yaml string) {
	t.Helper()
	dir := filepath.Join(storeDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default-audit.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// slowSites returns two independent slow fixture sites, so two parallel crawls
// each have their own origin and stay live long enough to be controlled.
func slowSites(t *testing.T) (a, b *httptest.Server) {
	return slowSite(t), slowSite(t)
}

func TestRunnerParallelStartsTwoCrawls(t *testing.T) {
	srvA, srvB := slowSites(t)
	dir := t.TempDir()
	writeDefaultProfile(t, dir, "speed:\n  max_concurrent_crawls: 2\n")
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)
	s := NewServer(r, "test")

	idA, err := r.StartCrawl(context.Background(), StartRequest{
		URL: srvA.URL + "/", Config: map[string]any{"speed.max_threads": 1},
	})
	if err != nil {
		t.Fatalf("first StartCrawl: %v", err)
	}
	idB, err := r.StartCrawl(context.Background(), StartRequest{
		URL: srvB.URL + "/", Config: map[string]any{"speed.max_threads": 1},
	})
	if err != nil {
		t.Fatalf("second StartCrawl (one slot still free): %v", err)
	}
	if idA == idB || idA == "" || idB == "" {
		t.Fatalf("parallel starts returned ids %q, %q — want two distinct crawls", idA, idB)
	}

	// Both crawls are live at once.
	waitFor(t, func() bool { return len(r.Running()) == 2 }, "both crawls running concurrently")

	// A third start is rejected, naming the running crawls.
	if _, err := r.StartCrawl(context.Background(), StartRequest{
		URL: srvA.URL + "/", Config: map[string]any{"speed.max_threads": 1},
	}); err == nil || !strings.Contains(err.Error(), idA) || !strings.Contains(err.Error(), idB) {
		t.Fatalf("third StartCrawl = %v, want a capacity rejection naming %s and %s", err, idA, idB)
	}

	// crawl_status with no id is ambiguous while two crawls run: it must error
	// and list the live ids, never guess.
	text, isErr := callTool(t, s, "crawl_status", map[string]any{})
	if !isErr || !strings.Contains(text, idA) || !strings.Contains(text, idB) {
		t.Fatalf("ambiguous crawl_status: isErr=%v text=%s, want an error listing both ids", isErr, text)
	}

	// crawl_status with an id addresses exactly that crawl.
	for _, want := range []struct{ id, seed string }{{idA, srvA.URL + "/"}, {idB, srvB.URL + "/"}} {
		text, isErr := callTool(t, s, "crawl_status", map[string]any{"crawl_id": want.id})
		if isErr {
			t.Fatalf("crawl_status(%s): %s", want.id, text)
		}
		var p Progress
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			t.Fatalf("decode crawl_status: %v\n%s", err, text)
		}
		if p.CrawlID != want.id || p.Seed != want.seed || p.State != "running" {
			t.Errorf("crawl_status(%s) = id %s seed %s state %s, want the addressed live crawl", want.id, p.CrawlID, p.Seed, p.State)
		}
	}

	// pause_crawl with no id is ambiguous too.
	if text, isErr := callTool(t, s, "pause_crawl", map[string]any{}); !isErr || !strings.Contains(text, "crawl_id") {
		t.Fatalf("ambiguous pause_crawl: isErr=%v text=%s, want an error asking for crawl_id", isErr, text)
	}

	// pause_crawl(idA) pauses ONLY crawl A; B keeps running.
	if text, isErr := callTool(t, s, "pause_crawl", map[string]any{"crawl_id": idA}); isErr {
		t.Fatalf("pause_crawl(%s): %s", idA, text)
	}
	waitFor(t, func() bool {
		running := r.Running()
		return len(running) == 1 && running[0].CrawlID == idB
	}, "crawl A paused while B keeps running")
	if got := crawlStatus(t, dir, idA); got != store.StatusInterrupted {
		t.Errorf("paused crawl A status = %q, want interrupted (resumable)", got)
	}

	// stop_crawl with no id now unambiguously addresses B.
	if text, isErr := callTool(t, s, "stop_crawl", map[string]any{}); isErr {
		t.Fatalf("stop_crawl (single live crawl left): %s", text)
	}
	settle(t, r)
	if got := crawlStatus(t, dir, idB); got != store.StatusCompleted {
		t.Errorf("stopped crawl B status = %q, want completed", got)
	}
}

// TestRunnerParallelGlobalCapProcessWide (GL-08, MCP wiring): two MCP-started
// crawls of 4 threads each run under speed.max_global_threads=2 — the observed
// peak of concurrent page fetches across BOTH crawls never exceeds 2, proving
// the Runner injected ONE shared limiter rather than per-crawl caps.
func TestRunnerParallelGlobalCapProcessWide(t *testing.T) {
	const G = 2
	var cur, max int64
	gauge := func() *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				w.WriteHeader(404) // fetched outside the limiter; keep it out of the gauge
				return
			}
			c := atomic.AddInt64(&cur, 1)
			for {
				m := atomic.LoadInt64(&max)
				if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond) // hold the slot long enough to overlap
			atomic.AddInt64(&cur, -1)
			w.Header().Set("Content-Type", "text/html")
			if r.URL.Path == "/" {
				for i := 0; i < 10; i++ {
					fmt.Fprintf(w, `<a href="/p%d">p</a>`, i)
				}
				return
			}
			fmt.Fprint(w, "<p>leaf</p>")
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	srvA, srvB := gauge(), gauge()

	dir := t.TempDir()
	writeDefaultProfile(t, dir, fmt.Sprintf("speed:\n  max_concurrent_crawls: 2\n  max_global_threads: %d\n", G))
	r := NewRunner(dir)
	t.Cleanup(r.Shutdown)

	for _, u := range []string{srvA.URL + "/", srvB.URL + "/"} {
		if _, err := r.StartCrawl(context.Background(), StartRequest{
			URL: u, Config: map[string]any{"speed.max_threads": 4}, // 2×4 would-be workers ≫ G
		}); err != nil {
			t.Fatalf("StartCrawl(%s): %v", u, err)
		}
	}
	settle(t, r)

	if got := atomic.LoadInt64(&max); got > G {
		t.Errorf("peak concurrent page fetches across both MCP crawls = %d, want <= %d (one shared process-wide limiter)", got, G)
	}
}
