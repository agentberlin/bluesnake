package crawler

// Stream-and-drop test support + the retention probe (MEMORY-SCALING.md §5.4,
// §13.6). Once the live Result no longer carries the page map, crawler-package
// tests assert against the records streamed to a capturing Sink, and reproduce
// the finalize aggregate pass (depth/inlinks/discovered_from) over them using
// the same exported recompute entry points finalize calls.

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
)

// capSink captures every streamed PageRecord (latest-wins by URL). It snapshots
// the scalar fields at record() time; Facts (minus the freed ContentText) are
// shared, which is what the depth/inlinks replay needs (Facts.Links survive).
type capSink struct {
	mu    sync.Mutex
	pages map[string]*PageRecord
}

func newCapSink() *capSink { return &capSink{pages: map[string]*PageRecord{}} }

func (s *capSink) Page(rec *PageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.pages[rec.URL] = &cp
	return nil
}
func (s *capSink) FrontierDone(string) error { return nil }

func (s *capSink) snapshot() map[string]*PageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*PageRecord, len(s.pages))
	for k, v := range s.pages {
		out[k] = v
	}
	return out
}

// pageSnapshotter is any capturing sink that can hand back the records it saw.
type pageSnapshotter interface{ snapshot() map[string]*PageRecord }

// runCap runs c (built WithSink(sink)) over the seeds and returns the captured
// records with the finalize aggregate pass applied — the direct-Run analogue of
// the crawl() helper.
func runCap(t *testing.T, c *Crawler, sink pageSnapshotter, seeds ...string) *crawlT {
	t.Helper()
	res, err := c.Run(context.Background(), seeds...)
	if err != nil {
		t.Fatal(err)
	}
	return capFinalize(c, sink.snapshot(), res, seeds...)
}

// crawlT bundles a captured crawl's records with the result counts, mirroring
// the old *Result shape so test assertions stay readable.
type crawlT struct {
	Pages       map[string]*PageRecord
	Crawled     int
	Total       int
	Interrupted bool
	Duration    time.Duration
}

// capFinalize reproduces, over the captured records, exactly what the PRODUCTION
// finalize does for a completed crawl — using the same entry points, not a
// parallel in-RAM path: shortest-path depth via the depth CSR over the stored
// `links` superset (RecomputeDepthsFromLinks), and hyperlink-gated inlinks +
// first-wins/seed-locked discovered_from over the gated `edges` the crawl recorded
// (the GatedEdges the records carry — store.SaveInlinksFromEdges semantics). The
// crawler-package tests use it in place of the dropped live page map; building the
// finalize inputs from the captured records keeps it in this package (the store /
// finalize packages import crawler, so a crawler-package test cannot import them).
func capFinalize(c *Crawler, pages map[string]*PageRecord, res *Result, seeds ...string) *crawlT {
	// Depth: feed RecomputeDepthsFromLinks the link superset + redirect edges built
	// from the captured Facts.Links, exactly as finalize feeds it store.LinkRows.
	var links []LinkRow
	redirects := map[string]string{}
	urls := make([]string, 0, len(pages))
	for url, rec := range pages {
		urls = append(urls, url)
		if rec.RedirectURL != "" {
			redirects[url] = rec.RedirectURL
		}
		if rec.Facts != nil {
			for _, l := range rec.Facts.Links {
				links = append(links, LinkRow{Src: url, Dst: l.URL, Type: string(l.Type), Nofollow: l.Nofollow})
			}
		}
	}
	for url, d := range c.RecomputeDepthsFromLinks(links, redirects, urls, seeds) {
		if rec, ok := pages[url]; ok {
			rec.Depth = d
		}
	}

	// Inlinks (hyperlink-gated count) + first-wins discovered_from (lowest-seq edge
	// source, seed-locked) over the captured gated edges — the same computation
	// store.SaveInlinksFromEdges runs in SQL over the edges table.
	inl := map[string]int{}
	firstSeq := map[string]int64{}
	from := map[string]string{}
	for src, rec := range pages {
		for _, e := range rec.GatedEdges {
			if e.Hyperlink {
				inl[e.Dst]++
			}
			if s, seen := firstSeq[e.Dst]; !seen || e.Seq < s {
				firstSeq[e.Dst] = e.Seq
				from[e.Dst] = src
			}
		}
	}
	seedSet := map[string]bool{}
	for _, s := range c.NormalizeSeeds(seeds...) {
		seedSet[s] = true
	}
	for url, rec := range pages {
		rec.Inlinks = inl[url]
		if seedSet[url] {
			rec.DiscoveredFrom = "" // seed-lock
		} else {
			rec.DiscoveredFrom = from[url]
		}
	}
	return &crawlT{
		Pages:       pages,
		Crawled:     res.Crawled,
		Total:       res.Total,
		Interrupted: res.Interrupted,
		Duration:    res.Duration,
	}
}

// finalizerSink records each streamed PageRecord by attaching a finalizer that
// fires when the record is garbage-collected. It deliberately retains NOTHING,
// so after Run() the only thing that could keep a record alive is the crawler.
type finalizerSink struct {
	recorded  atomic.Int64
	collected atomic.Int64
}

func (s *finalizerSink) Page(rec *PageRecord) error {
	s.recorded.Add(1)
	runtime.SetFinalizer(rec, func(*PageRecord) { s.collected.Add(1) })
	return nil
}
func (s *finalizerSink) FrontierDone(string) error { return nil }

// TestNoPageRecordRetainedAfterRun (SD-12) is the stream-and-drop done-signal:
// a *PageRecord's footprint must not scale with crawled-page count. We attach a
// GC finalizer to every streamed record and assert all are collectable once the
// crawl drains — proving the crawler retains no page records in RAM. (Written
// RED against the pre-rework crawler, whose c.pages map held every record.)
func TestNoPageRecordRetainedAfterRun(t *testing.T) {
	bodies := map[string]string{"/": ""}
	var root strings.Builder
	const n = 25
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("/p%d", i)
		root.WriteString(link(p))
		bodies[p] = "<html><body><p>leaf body number " + fmt.Sprint(i) + "</p></body></html>"
	}
	bodies["/"] = "<html><body>" + root.String() + "</body></html>"
	s := newSite(t, bodies)

	sink := &finalizerSink{}
	cfg := config.Default()
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if res.Interrupted {
		t.Fatal("crawl interrupted unexpectedly")
	}
	want := sink.recorded.Load()
	if want < n {
		t.Fatalf("recorded only %d pages, want >= %d", want, n)
	}

	deadline := time.Now().Add(8 * time.Second)
	for sink.collected.Load() < want && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.collected.Load(); got != want {
		t.Errorf("only %d/%d streamed PageRecords were garbage-collected; the crawler "+
			"still retains page records in RAM (stream-and-drop incomplete)", got, want)
	}
}

// goroutinePeakForFanout crawls a one-page → `fanout` children fixture with N
// worker threads and returns (peak live goroutines during the crawl, the
// pre-crawl baseline). It is the measurement core of TestGoroutinesBoundedByThreads.
func goroutinePeakForFanout(t *testing.T, fanout, threads int) (peak, base int) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < fanout; i++ {
		b.WriteString(link(fmt.Sprintf("/p%d", i)))
	}
	bodies := map[string]string{"/": "<html><body>" + b.String() + "</body></html>"}
	for i := 0; i < fanout; i++ {
		bodies[fmt.Sprintf("/p%d", i)] = "<p>leaf</p>"
	}
	s := newSite(t, bodies)

	cfg := config.Default()
	cfg.Speed.MaxThreads = threads
	sink := newCapSink()
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}

	base = runtime.NumGoroutine()
	var maxG int64
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				g := int64(runtime.NumGoroutine())
				for {
					old := atomic.LoadInt64(&maxG)
					if g <= old || atomic.CompareAndSwapInt64(&maxG, old, g) {
						break
					}
				}
			}
		}
	}()
	res, err := c.Run(context.Background(), s.server.URL+"/")
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	if res.Crawled < fanout {
		t.Fatalf("crawled %d, want >= %d (whole fan-out)", res.Crawled, fanout)
	}
	return int(atomic.LoadInt64(&maxG)), base
}

// TestGoroutinesBoundedByThreads (§8.2 #1, the Phase-3/6 done-gate) pins the
// live goroutine count to the worker-pool size, independent of the discovered
// frontier. A high-fan-out fixture (one page → thousands of children) makes the
// gap glaring: goroutine-per-URL spawns ~one goroutine per admitted URL, all
// parked on the thread semaphore. A bounded worker pool keeps it at ~N. Run at
// TWO fan-out sizes (doc §8.2 #1 asks for two): the peak must stay under the same
// N-based bound at both, proving it does not scale with the frontier.
func TestGoroutinesBoundedByThreads(t *testing.T) {
	const threads = 5
	for _, fanout := range []int{1500, 3000} {
		peak, base := goroutinePeakForFanout(t, fanout, threads)
		// workers (N) + the crawl's own helpers + httptest's per-conn goroutines +
		// this sampler, with generous slack — but nowhere near the frontier size.
		// The SAME bound at fanout=1500 and fanout=3000 is the independence proof.
		limit := base + threads + 40
		if peak > limit {
			t.Errorf("fanout=%d: peak live goroutines = %d, want <= %d (base %d + threads %d + slack); "+
				"goroutine count scales with the admitted frontier, not the worker pool",
				fanout, peak, limit, base, threads)
		}
	}
}
