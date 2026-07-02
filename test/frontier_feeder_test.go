package acceptance

// The bounded-frontier gates (issue #77, MEMORY-SCALING.md §5.2/§5.3): per-crawl
// RAM must not scale with the DISCOVERED frontier — the axis the Phase-3/#69
// goroutine gate was structurally blind to (goroutines were bounded while the
// in-RAM work queue still grew frontier-linearly). The fixture is the bfab
// facet shape: a handful of crawled hub pages that admit tens of thousands of
// URLs which are never fetched (discovered ≫ crawled).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// facetServer serves "/" linking to `hubs` hub pages, each linking to
// fanout-per-hub unique synthetic URLs (404s — they are admitted, never
// usefully fetched). Paths are padded so each queued URL carries a realistic
// string cost. To isolate the FRONTIER axis, scale the hub COUNT and keep the
// per-hub fanout constant: scaling fanout-per-hub instead scales every page's
// transient parse/marshal working set with it, and that (bounded, per-worker)
// cost drowns the retained-queue signal the memory gate measures.
func facetServer(t *testing.T, hubs, fanoutPerHub int) *httptest.Server {
	t.Helper()
	pad := strings.Repeat("x", 40)
	mux := http.NewServeMux()
	var seedBody strings.Builder
	for h := 0; h < hubs; h++ {
		seedBody.WriteString(fmt.Sprintf(`<a href="/hub%d">h</a> `, h))
		var hubBody strings.Builder
		for i := 0; i < fanoutPerHub; i++ {
			hubBody.WriteString(fmt.Sprintf(`<a href="/facet/%s/%d-%d">f</a> `, pad, h, i))
		}
		body := hubBody.String()
		mux.HandleFunc(fmt.Sprintf("/hub%d", h), func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `<html><head><title>hub</title></head><body>%s</body></html>`, body)
		})
	}
	home := seedBody.String()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<html><head><title>seed</title></head><body>%s</body></html>`, home)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// queuelessSink hides the store's work-queue capability by shadowing ClaimBatch
// with an incompatible signature, forcing the engine onto its in-RAM FIFO
// fallback — the pre-#77 frontier-linear architecture — while every other
// capability (Sink, Dedup, content authority, ...) still promotes. It exists so
// the memory gate below can prove its own detector works: the identical harness
// must SEE the frontier-linear slope when the bounded queue is taken away.
type queuelessSink struct{ *store.Crawl }

func (queuelessSink) ClaimBatch() {}

// facetCrawlPeak crawls the facet fixture with the given total fanout and
// returns the crawl's peak live-heap growth over the pre-crawl baseline,
// sampled with forced GCs (HeapInuse after GC ≈ live bytes; §13.9 protocol).
func facetCrawlPeak(t *testing.T, fanout int, hideQueue bool) uint64 {
	t.Helper()
	const fanoutPerHub = 500 // constant per-page cost — only the frontier scales
	hubs := fanout / fanoutPerHub
	srv := facetServer(t, hubs, fanoutPerHub)
	cfg := config.Default()
	cfg.Speed.MaxThreads = 4
	cfg.Limits.MaxURLs = hubs + 2 // crawl the seed + every hub; facets stay frontier-only

	st, err := store.CreateCrawl(t.TempDir(), []string{srv.URL + "/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var sink crawler.Sink = st
	if hideQueue {
		sink = queuelessSink{st}
	}
	c, err := crawler.New(cfg, crawler.WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak atomic.Uint64
	sample := func() {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		for {
			cur := peak.Load()
			if m.HeapInuse <= cur || peak.CompareAndSwap(cur, m.HeapInuse) {
				return
			}
		}
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
				sample()
			}
		}
	}()
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	sample() // a crawl shorter than one tick still gets measured

	if p := peak.Load(); p > base.HeapInuse {
		return p - base.HeapInuse
	}
	return 0
}

// TestFrontierRAMSlopeFlat is the #77 "done" gate: peak crawl RAM must be flat
// on the FRONTIER axis. Two facet crawls whose discovered frontiers differ by
// ~50k URLs must peak within a few MB of each other — the store-backed queue
// keeps only a bounded window in RAM. (The crawled hub count differs too, but
// crawled pages are stream-and-dropped — their retained cost is pinned ~0 by
// the SD-12 retention sentinel — so the slope here is the frontier's.) The
// second half proves the detector: the identical harness with the work-queue
// capability hidden (the pre-#77 in-RAM queue) must show the frontier-linear
// slope the gate exists to forbid. If THAT arm goes flat the gate is blind,
// not the engine fixed.
func TestFrontierRAMSlopeFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-crawl memory gate — skipped in -short")
	}
	const small, large = 5_000, 55_000
	const maxFlatSlope = 4 << 20 // bounded queue: window + batches, way under the ~10MB linear cost
	const minLinearSlope = 5 << 20

	smallPeak := facetCrawlPeak(t, small, false)
	largePeak := facetCrawlPeak(t, large, false)
	slope := int64(largePeak) - int64(smallPeak)
	t.Logf("store-backed queue: peak(+%dk URLs) - peak(+%dk URLs) = %+.1f MB",
		large/1000, small/1000, float64(slope)/(1<<20))
	if slope > maxFlatSlope {
		t.Errorf("peak RAM grew %.1f MB across a %dk-URL frontier delta — RAM is still frontier-linear (want < %d MB)",
			float64(slope)/(1<<20), (large-small)/1000, maxFlatSlope>>20)
	}

	memSmall := facetCrawlPeak(t, small, true)
	memLarge := facetCrawlPeak(t, large, true)
	memSlope := int64(memLarge) - int64(memSmall)
	t.Logf("in-RAM queue fallback: slope = %+.1f MB", float64(memSlope)/(1<<20))
	if memSlope < minLinearSlope {
		t.Errorf("detector check failed: the in-RAM queue arm grew only %.1f MB (want > %d MB) — the harness cannot see the failure mode it gates",
			float64(memSlope)/(1<<20), minLinearSlope>>20)
	}
}

// TestMaxURLsTerminatesWithLargeAdmittedFrontier (WP-21): a crawl whose budget
// is exhausted while thousands of admitted rows sit unclaimed must still
// terminate — the feeder keeps draining them through the (cheap) over-budget
// path exactly as the in-RAM queue did, every drained row is FrontierDone'd,
// and the crawl seals completed with the budget spent exactly.
func TestMaxURLsTerminatesWithLargeAdmittedFrontier(t *testing.T) {
	srv := facetServer(t, 1, 3000)
	cfg := config.Default()
	cfg.Speed.MaxThreads = 4
	cfg.Limits.MaxURLs = 2 // seed + hub; the 3000 facets are admitted, never fetched

	dir := t.TempDir()
	id := straightCrawlRunner(t, dir, srv.URL+"/", cfg) // fails the test unless status == completed

	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fetched, err := st.FetchedCount()
	if err != nil {
		t.Fatal(err)
	}
	if fetched != cfg.Limits.MaxURLs {
		t.Errorf("fetch slots consumed = %d, want exactly MaxURLs = %d", fetched, cfg.Limits.MaxURLs)
	}
	pending, err := st.PendingFrontier()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("%d frontier rows left after a completed capped crawl, want 0 (over-budget rows are FrontierDone'd)", len(pending))
	}
}

// TestEveryURLFetchedExactlyOnce (WP-08): under the feeder + N workers, no URL
// may ever be fetched twice — a feeder/worker double-claim would double-fetch
// and double-count inlinks. A fully-interconnected site makes every page a
// contended re-discovery target while the server counts real HTTP hits.
func TestEveryURLFetchedExactlyOnce(t *testing.T) {
	const pages = 30
	var mu sync.Mutex
	hits := map[string]int{}
	var body strings.Builder
	for i := 0; i < pages; i++ {
		body.WriteString(fmt.Sprintf(`<a href="/p%d">p</a> `, i))
	}
	page := fmt.Sprintf(`<html><head><title>p</title></head><body>%s</body></html>`, body.String())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		fmt.Fprint(w, page)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Speed.MaxThreads = 8
	straightCrawlRunner(t, t.TempDir(), srv.URL+"/", cfg)

	mu.Lock()
	defer mu.Unlock()
	for path, n := range hits {
		// Site-level probes (robots.txt, sitemap/llms discovery) are outside the
		// frontier; only the crawl pages pin the exactly-once contract.
		if path != "/" && !strings.HasPrefix(path, "/p") {
			continue
		}
		if n != 1 {
			t.Errorf("%s fetched %d times, want exactly once (double-claim)", path, n)
		}
	}
}

// TestResumeRecoversOrphanedClaimedRows (EC-01): rows a crash orphaned at
// claimed=1 — invisible to the feeder's WHERE claimed=0 — must be reset by the
// engine's Recover at resume start, or those URLs are silently lost. The forge
// below marks EVERY pending row claimed (a worst-case crash: the whole buffer
// plus the feeder's hands in flight), then the resumed crawl must still land on
// the straight crawl's exact page set.
func TestResumeRecoversOrphanedClaimedRows(t *testing.T) {
	srv := equivServer(t)
	seed := srv.URL + "/"
	dir := t.TempDir()

	straightID := straightCrawlRunner(t, dir, seed, equivCfg())

	// Session 1: interrupt after 3 of the 7 pages (the production pause path).
	obs := &pauseObs{after: 3}
	e := runner.New(dir, obs)
	obs.exec = e
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: seed, ConfigYAML: mustYAML(t, equivCfg())}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusInterrupted {
		t.Fatalf("session 1 status = %q, want interrupted", status)
	}
	id := obs.id()

	// Forge the hard-crash state: every surviving frontier row claimed.
	func() {
		st, err := store.OpenCrawl(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.DB().Exec(`UPDATE frontier SET claimed = 1`); err != nil {
			t.Fatal(err)
		}
	}()

	status, err = runner.New(dir, nil).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resume status = %q, want completed", status)
	}

	sPages, _, sCrawled, _ := snapshot(t, dir, straightID, srv.URL)
	rPages, _, rCrawled, _ := snapshot(t, dir, id, srv.URL)
	if rCrawled != sCrawled {
		t.Errorf("resumed crawl recorded %d pages, straight %d — orphaned claimed rows were lost (EC-01)", rCrawled, sCrawled)
	}
	for url := range sPages {
		if _, ok := rPages[url]; !ok {
			t.Errorf("%s crawled straight but missing after the orphaned-claims resume", url)
		}
	}
}
