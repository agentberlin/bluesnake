package runner

// Parallel multi-crawl integration at the dispatcher+executor level (issue #78,
// MEMORY-SCALING §13.5): registry consistency under M real crawls (GL-12),
// shutdown pausing ALL in-flight crawls over the persistent store (GL-13),
// resume under parallel load (X-02), and MaxURLs termination while a sibling
// holds the single finalize slot (X-04).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// leafSite serves a home page linking `leaves` fast leaf pages.
func leafSite(t *testing.T, leaves int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < leaves; i++ {
				fmt.Fprintf(w, `<a href="/p%d">p</a>`, i)
			}
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// slowLeafSite is leafSite with ~120ms leaves, keeping a crawl reliably live.
func slowLeafSite(t *testing.T, leaves int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < leaves; i++ {
				fmt.Fprintf(w, `<a href="/p%d">p</a>`, i)
			}
			return
		}
		select {
		case <-time.After(120 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitUntil(t *testing.T, cond func() bool, msg string) {
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

// TestParallelCrawlsRegistryWritesConsistent (GL-12): M=4 real crawls through
// W=4 drain loops sharing one registry — exactly M rows, every one terminal
// completed, and each row carries ITS OWN site's counts (a clobbered write
// would swap or zero them).
func TestParallelCrawlsRegistryWritesConsistent(t *testing.T) {
	const M = 4
	dir := t.TempDir()
	want := map[string]int{} // seed -> expected total pages
	seeds := make([]string, M)
	for i := 0; i < M; i++ {
		srv := leafSite(t, 2+i) // distinct sizes make clobbered counts visible
		seeds[i] = srv.URL + "/"
		want[seeds[i]] = 3 + i // home + leaves
	}

	obs := &groupObs{total: M, done: make(chan struct{})}
	disp := queue.New(queue.NewMemStore(),
		New(dir, obs, WithLimiter(limiter.New(0, 1, 0))),
		queue.WithConcurrency(M))
	if err := disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if _, err := disp.Enqueue(queue.JobSpec{
			URL: seed, ConfigYAML: "speed:\n  max_threads: 2\n",
		}, "manual", "", seed); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-obs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("parallel crawls did not finish")
	}
	disp.Shutdown()

	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != M {
		t.Fatalf("registry holds %d crawls, want exactly %d", len(infos), M)
	}
	for _, in := range infos {
		if in.Status != store.StatusCompleted {
			t.Errorf("crawl %s (%s) status = %q, want completed", in.ID, in.Seed, in.Status)
		}
		if in.Total != want[in.Seed] || in.Crawled != want[in.Seed] {
			t.Errorf("crawl %s (%s) counts = crawled %d / total %d, want %d/%d (registry write clobbered?)",
				in.ID, in.Seed, in.Crawled, in.Total, want[in.Seed], want[in.Seed])
		}
	}
}

// TestShutdownPausesAllInFlightCrawls (GL-13, dispatcher+SQLiteStore level):
// with M=3 crawls live across W=3 loops over the PERSISTENT job store,
// Shutdown turns every one around — all three crawls land interrupted
// (resumable), all three jobs land interrupted, Shutdown waits for every loop,
// and the crawl goroutines wind down (no leak).
func TestShutdownPausesAllInFlightCrawls(t *testing.T) {
	const M = 3
	dir := t.TempDir()
	baseline := runtime.NumGoroutine()

	exec := New(dir, nil, WithLimiter(limiter.New(0, 1, 0)))
	disp := queue.New(queue.NewSQLiteStore(dir), exec, queue.WithConcurrency(M))
	if err := disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < M; i++ {
		srv := slowLeafSite(t, 30)
		if _, err := disp.Enqueue(queue.JobSpec{
			URL: srv.URL + "/", ConfigYAML: "speed:\n  max_threads: 1\n",
		}, "manual", "", fmt.Sprintf("m%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, func() bool { return len(exec.Snapshots()) == M }, "all crawls live")

	done := make(chan struct{})
	go func() { disp.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Shutdown did not return — a drain loop (or crawl) was left running")
	}

	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != M {
		t.Fatalf("registry holds %d crawls, want %d", len(infos), M)
	}
	for _, in := range infos {
		if in.Status != store.StatusInterrupted {
			t.Errorf("crawl %s status = %q, want interrupted (resumable) — Shutdown abandoned it", in.ID, in.Status)
		}
	}
	jobs, err := disp.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Status != store.JobInterrupted {
			t.Errorf("job %s = %q, want interrupted", j.Label, j.Status)
		}
	}
	// No goroutine leak: everything the M crawls spawned winds down.
	waitUntil(t, func() bool { return runtime.NumGoroutine() <= baseline+4 },
		fmt.Sprintf("goroutines to wind down (baseline %d, now %d)", baseline, runtime.NumGoroutine()))
}

// pauseOneObs pauses ONE specific crawl (by seed) after it has recorded n pages,
// using the addressed PauseCrawl — the parallel-safe version of recObs.
type pauseOneObs struct {
	exec       *Executor
	seedSubstr string
	pauseAfter int

	mu     sync.Mutex
	pages  map[string]int
	target string // crawl id of the seed-matched crawl, set on start
}

func (o *pauseOneObs) OnStart(crawlID, seed string) {
	if strings.Contains(seed, o.seedSubstr) {
		o.mu.Lock()
		o.target = crawlID
		o.mu.Unlock()
	}
}
func (o *pauseOneObs) OnPage(crawlID string, _ *crawler.PageRecord) {
	o.mu.Lock()
	if o.pages == nil {
		o.pages = map[string]int{}
	}
	o.pages[crawlID]++
	hit := crawlID == o.target && o.pages[crawlID] == o.pauseAfter
	o.mu.Unlock()
	if hit {
		o.exec.PauseCrawl(crawlID)
	}
}
func (o *pauseOneObs) OnDone(Outcome) {}

// TestResumeUnderParallelLoad (X-02): crawl A is paused mid-crawl, then RESUMED
// while crawls B and C run — all three sharing one limiter, one registry, and
// W=3 drain loops. A's resume must rehydrate correctly (it finishes the whole
// site, every page exactly once) and must not disturb B/C (both complete
// fully).
func TestResumeUnderParallelLoad(t *testing.T) {
	dir := t.TempDir()
	srvA := leafSite(t, 6) // 7 pages total
	lim := limiter.New(2, 1, 0)

	// Phase 1: crawl A alone, paused after 2 pages (interrupted, resumable).
	obsA := &pauseOneObs{seedSubstr: srvA.URL, pauseAfter: 2}
	execA := New(dir, obsA, WithLimiter(lim))
	obsA.exec = execA
	dispA := queue.New(queue.NewMemStore(), execA)
	if err := dispA.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobA, err := dispA.Enqueue(queue.JobSpec{URL: srvA.URL + "/", ConfigYAML: "speed:\n  max_threads: 1\n"}, "manual", "", "A")
	if err != nil {
		t.Fatal(err)
	}
	crawlA, err := dispA.AwaitCrawl(context.Background(), jobA.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return len(execA.Snapshots()) == 0 }, "crawl A to pause")
	dispA.Shutdown()
	if got := crawlStatusOf(t, dir, crawlA); got != store.StatusInterrupted {
		t.Fatalf("phase-1 crawl A = %q, want interrupted", got)
	}

	// Phase 2: B and C run; A resumes into the SAME parallel dispatcher.
	obs := &groupObs{total: 3, done: make(chan struct{})}
	exec := New(dir, obs, WithLimiter(lim))
	disp := queue.New(queue.NewMemStore(), exec, queue.WithConcurrency(3))
	if err := disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	bc := map[string]bool{}
	for _, name := range []string{"B", "C"} {
		srv := slowLeafSite(t, 12) // 13 pages, live for ~1.5s under threads 1
		bc[srv.URL+"/"] = true
		if _, err := disp.Enqueue(queue.JobSpec{URL: srv.URL + "/", ConfigYAML: "speed:\n  max_threads: 1\n"}, "manual", "", name); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, func() bool { return len(exec.Snapshots()) == 2 }, "B and C live")
	if _, err := disp.Enqueue(queue.JobSpec{ResumeID: crawlA}, "manual", "", "resume A"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-obs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("resume-under-load crawls did not finish")
	}
	disp.Shutdown()

	// A finished the whole site — every page exactly once (rehydrated dedup +
	// counters), same as a straight crawl.
	if got := crawledCount(t, dir, crawlA); got != 7 {
		t.Errorf("resumed crawl A fetched %d pages, want 7 (rehydration under load)", got)
	}
	if got := crawlStatusOf(t, dir, crawlA); got != store.StatusCompleted {
		t.Errorf("resumed crawl A = %q, want completed", got)
	}
	// B and C were untouched by A's rehydration: both completed, full counts.
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if !bc[in.Seed] {
			continue
		}
		if in.Status != store.StatusCompleted || in.Total != 13 {
			t.Errorf("sibling crawl %s (%s) = %q with total %d, want completed/13 (disturbed by A's resume?)",
				in.ID, in.Seed, in.Status, in.Total)
		}
	}
}

// TestMaxURLsTerminationWhileFinalizeSlotHeld (X-04): crawl A reaches its
// MaxURLs budget and wants to finalize while a sibling holds the single
// finalize slot (F=1). A must simply WAIT for the slot — worker termination
// must not deadlock against finalize — and complete once the slot frees.
func TestMaxURLsTerminationWhileFinalizeSlotHeld(t *testing.T) {
	dir := t.TempDir()
	srv := leafSite(t, 10)
	lim := limiter.New(0, 1, 0)

	// The test plays the sibling: it holds the one finalize slot up front.
	lim.AcquireFinalize(context.Background())

	obs := &groupObs{total: 1, done: make(chan struct{})}
	disp := queue.New(queue.NewMemStore(), New(dir, obs, WithLimiter(lim)))
	if err := disp.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := disp.Enqueue(queue.JobSpec{
		URL:        srv.URL + "/",
		ConfigYAML: "speed:\n  max_threads: 2\nlimits:\n  max_urls: 3\n",
	}, "manual", "", "A"); err != nil {
		t.Fatal(err)
	}

	// A hits MaxURLs, drains its workers, and blocks on the finalize slot —
	// it must be parked there, not deadlocked or done.
	select {
	case <-obs.done:
		t.Fatal("crawl finalized while the sibling held the only finalize slot")
	case <-time.After(600 * time.Millisecond):
	}

	lim.ReleaseFinalize() // the sibling finishes; A's finalize can run
	select {
	case <-obs.done:
	case <-time.After(30 * time.Second):
		t.Fatal("crawl never finalized after the slot freed — deadlock between MaxURLs termination and the finalize cap")
	}
	disp.Shutdown()

	obs.mu.Lock()
	status := obs.statuses[0]
	obs.mu.Unlock()
	if status != store.StatusCompleted {
		t.Errorf("MaxURLs-terminated crawl = %q, want completed", status)
	}
}

// crawlStatusOf reads a crawl's stored registry status.
func crawlStatusOf(t *testing.T, dir, id string) string {
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
