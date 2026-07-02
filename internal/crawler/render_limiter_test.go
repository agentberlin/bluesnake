package crawler

// REN-01 / #76: Chrome renders are a first-class bounded resource under the
// process-wide limiter — a render-slot pool DISTINCT from fetch slots. These
// tests drive the contract from MEMORY-SCALING.md §13.8 without needing Chrome:
// a fake renderer (the pageRenderer seam) observes render concurrency with an
// atomic gauge exactly like the fixture servers observe fetch concurrency.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/render"
)

// renderGauge is the atomic in-flight render counter + max watermark shared by
// every fake renderer in a test (SUM across crawls, the §13.7 harness shape).
type renderGauge struct{ cur, max int64 }

func (g *renderGauge) enter() {
	c := atomic.AddInt64(&g.cur, 1)
	for {
		m := atomic.LoadInt64(&g.max)
		if c <= m || atomic.CompareAndSwapInt64(&g.max, m, c) {
			break
		}
	}
}
func (g *renderGauge) exit()           { atomic.AddInt64(&g.cur, -1) }
func (g *renderGauge) inFlight() int64 { return atomic.LoadInt64(&g.cur) }
func (g *renderGauge) peak() int64     { return atomic.LoadInt64(&g.max) }

// fakeRenderer implements pageRenderer without Chrome. Renders hold the gauge
// for their whole duration; blockPath renders park until the crawl ctx is
// cancelled (a "slow page" for pause/cancel tests) and return ctx.Err() exactly
// like a chromedp tab torn down mid-navigation.
type fakeRenderer struct {
	gauge     *renderGauge
	delay     time.Duration
	blockPath string // suffix; "" = never block

	mu       sync.Mutex
	inFlight map[string]bool
}

func (f *fakeRenderer) Render(ctx context.Context, url string) (*render.Result, error) {
	if f.gauge != nil {
		f.gauge.enter()
		defer f.gauge.exit()
	}
	f.mu.Lock()
	if f.inFlight == nil {
		f.inFlight = map[string]bool{}
	}
	f.inFlight[url] = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.inFlight, url)
		f.mu.Unlock()
	}()

	if f.blockPath != "" && strings.HasSuffix(url, f.blockPath) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &render.Result{HTML: "<html><body><p>rendered</p></body></html>"}, nil
}

func (f *fakeRenderer) Close() {}

func (f *fakeRenderer) rendering(url string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight[url]
}

// renderSite serves "/" linking to n leaves, every page with UNIQUE body bytes
// so the identical-content short-circuit never suppresses a render.
func renderSite(t *testing.T, n int, onPage func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt", "/llms.txt", "/llms-full.txt":
			w.WriteHeader(404) // out-of-band site files: not page fetches
			return
		}
		if onPage != nil {
			onPage(r)
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < n; i++ {
				fmt.Fprint(w, link(fmt.Sprintf("/p%d", i)))
			}
			return
		}
		fmt.Fprintf(w, "<p>unique leaf %s</p>", r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// jsCrawler builds a rendering-mode crawler with an injected fake renderer.
func jsCrawler(t *testing.T, threads int, lim *limiter.Limiter, fr *fakeRenderer, sink Sink) *Crawler {
	t.Helper()
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Speed.MaxThreads = threads
	c, err := New(cfg, WithSink(sink), WithLimiter(lim), withRenderer(fr))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestGlobalLimiter_RenderSlotsBounded (REN-01, §13.0 critical) pins the core
// invariant: across M parallel JS-mode crawls sharing one limiter, the SUM of
// concurrent renders never exceeds the render cap — and the cap actually binds
// (with 2×5 workers it must be reached) — while every crawl still completes.
func TestGlobalLimiter_RenderSlotsBounded(t *testing.T) {
	const R = 2
	const leaves = 11 // 12 pages per crawl
	gauge := &renderGauge{}
	srv := renderSite(t, leaves, nil)

	lim := limiter.New(0, 1, R) // unlimited fetches: isolate the render axis
	var wg sync.WaitGroup
	sinks := make([]*capSink, 2)
	for i := 0; i < 2; i++ {
		sinks[i] = newCapSink()
		fr := &fakeRenderer{gauge: gauge, delay: 20 * time.Millisecond}
		c := jsCrawler(t, 5, lim, fr, sinks[i])
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := gauge.peak(); got > R {
		t.Errorf("peak concurrent renders across 2 crawls = %d, want <= %d (global render cap)", got, R)
	}
	if got := gauge.peak(); got < R {
		t.Errorf("peak concurrent renders = %d; the render cap never bound (10 would-be workers should reach %d)", got, R)
	}
	for i, s := range sinks {
		if got := len(s.snapshot()); got != leaves+1 {
			t.Errorf("crawl %d recorded %d pages, want %d (all crawls must complete under the cap)", i, got, leaves+1)
		}
	}
}

// TestRenderSlotReleasedOnCancelMidRender (REN-01) pins slot hygiene: repeated
// start+cancel cycles with a render in flight must not leak render slots — a
// fresh crawl afterwards still gets the FULL render cap (peak == R) and
// completes rather than wedging on a depleted semaphore.
func TestRenderSlotReleasedOnCancelMidRender(t *testing.T) {
	const R = 2
	lim := limiter.New(0, 1, R) // shared across every cycle — leaks accumulate here

	for cycle := 0; cycle < 3; cycle++ {
		gauge := &renderGauge{}
		srv := renderSite(t, 5, nil)
		fr := &fakeRenderer{gauge: gauge, blockPath: "/"} // every render parks until cancel
		c := jsCrawler(t, 3, lim, fr, newCapSink())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := c.Run(ctx, srv.URL+"/")
			done <- err
		}()
		waitFor(t, 5*time.Second, func() bool { return gauge.inFlight() >= 1 },
			"cycle %d: no render ever started", cycle)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("cycle %d: %v", cycle, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("cycle %d: crawl did not return after cancel mid-render", cycle)
		}
	}

	// Fresh crawl on the SAME limiter: full cap available, crawl completes.
	gauge := &renderGauge{}
	srv := renderSite(t, 11, nil)
	fr := &fakeRenderer{gauge: gauge, delay: 20 * time.Millisecond}
	sink := newCapSink()
	c := jsCrawler(t, 5, lim, fr, sink)
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if got := gauge.peak(); got != R {
		t.Errorf("fresh crawl peak concurrent renders = %d, want exactly %d (leaked slot ⇒ <, unbounded ⇒ >)", got, R)
	}
	if got := len(sink.snapshot()); got != 12 {
		t.Errorf("fresh crawl recorded %d pages, want 12", got)
	}
}

// TestRenderSlotDoesNotPinFetchSlot (REN-01) pins the no-nested-acquire rule: a
// held render slot must not also hold a fetch slot. With ONE global fetch slot
// and renders in flight, page fetches must still proceed — the fixture must
// observe a fetch arriving WHILE a render is in flight, which is impossible if
// the render pinned the only fetch slot.
func TestRenderSlotDoesNotPinFetchSlot(t *testing.T) {
	const leaves = 9
	gauge := &renderGauge{}
	var overlap atomic.Bool
	srv := renderSite(t, leaves, func(*http.Request) {
		if gauge.inFlight() > 0 {
			overlap.Store(true)
		}
	})

	lim := limiter.New(1, 1, 1) // tiny caps on BOTH axes
	fr := &fakeRenderer{gauge: gauge, delay: 60 * time.Millisecond}
	sink := newCapSink()
	c := jsCrawler(t, 4, lim, fr, sink)

	done := make(chan error, 1)
	go func() {
		_, err := c.Run(context.Background(), srv.URL+"/")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("crawl deadlocked — render slot vs fetch slot nested-acquire?")
	}

	if got := len(sink.snapshot()); got != leaves+1 {
		t.Errorf("recorded %d pages, want %d", got, leaves+1)
	}
	if !overlap.Load() {
		t.Error("no page fetch ever overlapped an in-flight render — the render is pinning the only fetch slot")
	}
}

// pendSink records FrontierDone calls so tests can assert an item was LEFT
// PENDING (no FrontierDone) for resume.
type pendSink struct {
	*capSink
	mu   sync.Mutex
	done map[string]bool
}

func newPendSink() *pendSink { return &pendSink{capSink: newCapSink(), done: map[string]bool{}} }

func (s *pendSink) FrontierDone(url string) error {
	s.mu.Lock()
	s.done[url] = true
	s.mu.Unlock()
	return nil
}

func (s *pendSink) isDone(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done[url]
}

// TestPauseInterruptsInFlightRender (REN-01 / #74 N2a follow-through) pins the
// pause contract for renders: pausing during a slow render returns promptly,
// the mid-render item is NOT recorded (a degraded raw-only record would be
// permanent — resume never re-renders a processed page) and NOT marked
// FrontierDone, and a resume re-fetches and re-renders it.
func TestPauseInterruptsInFlightRender(t *testing.T) {
	srv := renderSite(t, 1, nil) // "/" + "/p0"
	slow := srv.URL + "/p0"

	// Session 1: "/" renders instantly; "/p0" parks mid-render until pause.
	fr := &fakeRenderer{gauge: &renderGauge{}, blockPath: "/p0"}
	sink := newPendSink()
	c := jsCrawler(t, 2, nil, fr, sink)
	ctx, cancel := context.WithCancel(context.Background())
	type out struct {
		res *Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := c.Run(ctx, srv.URL+"/")
		done <- out{res, err}
	}()
	waitFor(t, 5*time.Second, func() bool { return fr.rendering(slow) },
		"the slow page never reached its render")
	cancel() // graceful pause

	var o out
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pause during an in-flight render did not return promptly")
	}
	if o.err != nil {
		t.Fatal(o.err)
	}
	if !o.res.Interrupted {
		t.Error("Result.Interrupted = false after pause")
	}
	pages := sink.snapshot()
	if pages[slow] != nil {
		t.Errorf("mid-render page was recorded (state %q) — it must stay unrecorded/pending for resume", pages[slow].State)
	}
	if sink.isDone(slow) {
		t.Error("mid-render page was marked FrontierDone — resume would never re-fetch it")
	}
	if pages[srv.URL+"/"] == nil {
		t.Fatal("the seed page (rendered before the pause) should be recorded")
	}

	// Session 2 (resume): the pending item is re-fetched AND re-rendered.
	fr2 := &fakeRenderer{gauge: &renderGauge{}}
	sink2 := newPendSink()
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Speed.MaxThreads = 2
	c2, err := New(cfg, WithSink(sink2), withRenderer(fr2), WithResume(Resume{
		Processed: []string{srv.URL + "/"},
		Fetched:   1,
		Pending:   []frontier.Item{{URL: slow, Depth: 1}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	rec := sink2.snapshot()[slow]
	if rec == nil {
		t.Fatal("resume did not re-fetch the pending mid-render page")
	}
	if rec.State != StateCrawled {
		t.Errorf("resumed page state = %q, want crawled", rec.State)
	}
	if rec.JSDiff == nil {
		t.Error("resumed page has no JSDiff — it was re-fetched but not re-rendered")
	}
	if !sink2.isDone(slow) {
		t.Error("resumed page not marked FrontierDone")
	}
}

// waitFor polls cond until true or the deadline, failing the test with msg.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf(msg, args...)
}
