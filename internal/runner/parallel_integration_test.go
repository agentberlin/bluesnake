package runner

// #74 R9: the REAL parallel crawl-all composition — queue.Dispatcher (W drain
// loops) + runner.Executor + queue.MemStore + one shared limiter — run
// end-to-end, exactly as `projects crawl-all --parallel` wires it. The pieces
// were each unit-tested; this pins the composition under -race (the runner
// package is in RACE_PKGS): both member crawls complete, each claimed exactly
// once, and the SUM of in-flight page fetches across the parallel crawls never
// exceeds the shared global cap.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// groupObs counts OnStart/OnDone across parallel member crawls, closing done
// when every member has finished (the groupObserver shape crawl-all uses).
type groupObs struct {
	total int
	done  chan struct{}

	mu       sync.Mutex
	starts   []string
	finished int
	statuses []string
}

func (o *groupObs) OnStart(crawlID, seed string) {
	o.mu.Lock()
	o.starts = append(o.starts, crawlID)
	o.mu.Unlock()
}
func (o *groupObs) OnPage(*crawler.PageRecord) {}

func (o *groupObs) OnDone(out Outcome) {
	o.mu.Lock()
	o.finished++
	o.statuses = append(o.statuses, out.Status)
	if o.finished == o.total {
		close(o.done)
	}
	o.mu.Unlock()
}

func TestParallelCrawlAllComposition(t *testing.T) {
	const G = 2
	var cur, max int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404) // bypasses the limiter; keep it out of the gauge
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
			for i := 0; i < 12; i++ {
				fmt.Fprintf(w, `<a href="/p%d">p</a>`, i)
			}
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	lim := limiter.New(G, 1, 0)
	obs := &groupObs{total: 2, done: make(chan struct{})}
	disp := queue.New(queue.NewMemStore(),
		New(dir, obs, WithLimiter(lim)),
		queue.WithConcurrency(2))
	if err := disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := disp.Enqueue(queue.JobSpec{
			URL:        srv.URL + "/",
			ConfigYAML: "speed:\n  max_threads: 5\n", // 2 crawls × 5 workers ≫ G
		}, "project", "proj-1", fmt.Sprintf("member-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-obs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("parallel member crawls did not finish")
	}
	disp.Shutdown()

	obs.mu.Lock()
	starts, statuses := obs.starts, obs.statuses
	obs.mu.Unlock()
	if len(starts) != 2 || starts[0] == starts[1] {
		t.Fatalf("started crawls = %v, want 2 distinct (each job claimed exactly once)", starts)
	}
	for _, s := range statuses {
		if s != store.StatusCompleted {
			t.Errorf("member crawl finished %q, want completed", s)
		}
	}
	jobs, _ := disp.List()
	for _, j := range jobs {
		if j.Status != store.JobDone {
			t.Errorf("job %s = %q, want done", j.Label, j.Status)
		}
	}
	got := atomic.LoadInt64(&max)
	if got > G {
		t.Errorf("peak concurrent page fetches across the parallel crawls = %d, want <= %d (one shared global cap)", got, G)
	}
	if got < G {
		t.Errorf("peak concurrency = %d; the shared cap never bound (10 would-be workers should reach %d)", got, G)
	}
	// Starvation check: both crawls actually crawled their whole member site.
	for _, id := range starts {
		if got := crawledCount(t, dir, id); got != 13 { // "/" + 12 leaves
			t.Errorf("crawl %s fetched %d pages, want 13 (no starvation under the shared cap)", id, got)
		}
	}
}
