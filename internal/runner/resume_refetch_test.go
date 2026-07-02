package runner

// EC-02 (MEMORY-SCALING.md §13.0): a crash between Page() and FrontierDone()
// leaves an already-crawled URL with a stale frontier row. A resume must NOT
// re-fetch it. TestResume_ClaimedRowWithPageAlreadyWritten_NotRefetched drives a
// real pause+resume through the production Executor, injects that stale pair, and
// asserts the server is never hit a second time for the affected URL (so the
// MaxURLs budget is not double-charged and no wasted round-trip occurs).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestResume_ClaimedRowWithPageAlreadyWritten_NotRefetched(t *testing.T) {
	const chain = 5 // /, /l1, /l2, /l3, /l4

	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		next := ""
		switch r.URL.Path {
		case "/":
			next = "/l1"
		default:
			var k int
			if _, err := fmt.Sscanf(r.URL.Path, "/l%d", &k); err == nil && k < chain-1 {
				next = fmt.Sprintf("/l%d", k+1)
			}
		}
		if next != "" {
			fmt.Fprintf(w, `<html><head><title>P</title></head><body><a href="%s">next</a></body></html>`, next)
		} else {
			fmt.Fprint(w, `<html><head><title>End</title></head><body><p>end</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)

	// Interrupt after 2 pages (/, /l1 crawled; /l2 pending).
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	crawlID := obs.startID

	// Inject the EC-02 crash window: re-insert a frontier row for an already-
	// crawled URL (/l1), as a crash between its Page() and FrontierDone() would.
	claimed := srv.URL + "/l1"
	func() {
		st, err := store.OpenCrawl(dir, crawlID)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if got := hits["/l1"]; got != 1 {
			t.Fatalf("precondition: /l1 hit %d times in session 1, want 1", got)
		}
		if _, err := st.DB().Exec(
			`INSERT OR IGNORE INTO frontier(url, depth, redirect_hops, source) VALUES(?,1,0,?)`,
			claimed, srv.URL+"/"); err != nil {
			t.Fatalf("inject stale frontier row: %v", err)
		}
	}()

	// Resume to completion.
	status, err := New(dir, &recObs{}).Run(context.Background(),
		queue.JobSpec{ResumeID: crawlID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resumed crawl status = %q, want completed", status)
	}

	// The headline EC-02 proof: the already-crawled URL was NOT re-fetched.
	mu.Lock()
	got := hits["/l1"]
	mu.Unlock()
	if got != 1 {
		t.Errorf("/l1 fetched %d times across the resume, want exactly 1 — a stale frontier row re-fetched an already-crawled page (EC-02)", got)
	}
	// And the crawl finished with the full chain, budget intact.
	if c := crawledCount(t, dir, crawlID); c != chain {
		t.Errorf("crawled %d pages, want %d (no double-charge / re-fetch)", c, chain)
	}
}
