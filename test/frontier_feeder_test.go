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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/urlutil"
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

// sustainSamples is how many consecutive 100 ms samples a heap level must hold
// to count as the crawl's peak (see sustainedPeak).
const sustainSamples = 3

// sustainedPeak returns the highest level the samples held for sustainSamples
// consecutive samples: the max, over every window of that length, of the
// window's min. A series shorter than one window counts as a single window.
func sustainedPeak(samples []uint64) uint64 {
	w := min(sustainSamples, len(samples))
	var peak uint64
	for i := 0; w > 0 && i+w <= len(samples); i++ {
		low := samples[i]
		for _, s := range samples[i+1 : i+w] {
			low = min(low, s)
		}
		peak = max(peak, low)
	}
	return peak
}

func TestSustainedPeak(t *testing.T) {
	for _, tc := range []struct {
		samples []uint64
		want    uint64
	}{
		{[]uint64{1, 9, 1, 9, 1}, 1},    // isolated spikes are not a peak
		{[]uint64{1, 9, 9, 1, 1}, 1},    // nor is a two-sample burst
		{[]uint64{1, 5, 6, 7, 2}, 5},    // a level held for the whole window counts
		{[]uint64{1, 2, 3, 4, 5, 6}, 4}, // steady growth: the window's floor
		{[]uint64{3, 8}, 3},             // shorter than a window: one window
		{nil, 0},
	} {
		if got := sustainedPeak(tc.samples); got != tc.want {
			t.Errorf("sustainedPeak(%v) = %d, want %d", tc.samples, got, tc.want)
		}
	}
}

// facetCrawlPeak crawls the facet fixture with the given total fanout and
// returns the crawl's sustained peak live-heap growth over the pre-crawl
// baseline, sampled every 100 ms with forced GCs (HeapInuse after GC ≈ live
// bytes; §13.9 protocol).
//
// The peak is sustained (sustainedPeak), not the single highest sample. A
// forced GC also counts whatever the 4 workers hold mid-page: a hub's parse
// tree, its 500 extracted links, the page record's JSON, ~1 MB each. That
// working set is bounded by the thread count, not the frontier, but one sample
// can catch several of them at once, and the max over a crawl's samples grows
// with how many samples it takes. The 55k crawl takes ~11x more than the 5k
// one, so the plain max turned per-page spikes into a spurious slope: -2.1..
// +6.5 MB for the bounded queue, over the old 4 MB gate about 1 run in 20 at
// <= 4 CPUs. Retained frontier state lives as long as its URLs stay queued,
// i.e. seconds, so requiring 300 ms of persistence drops the spikes and keeps
// it (measured in-RAM slope: see TestFrontierRAMSlopeFlat).
func facetCrawlPeak(t *testing.T, fanout int, hideQueue bool) uint64 {
	t.Helper()
	const fanoutPerHub = 500 // constant per-page cost — only the frontier scales
	hubs := fanout / fanoutPerHub
	srv := facetServer(t, hubs, fanoutPerHub)
	cfg := config.Default()
	cfg.Speed.MaxThreads = 4
	// The measured frontier must be the nominal `fanout` URLs on EVERY run, so the
	// facets are kept frontier-only by a robots.txt Disallow, not by the fetch
	// budget alone. The robots gate runs before the fetch-slot reservation, so a
	// facet never spends a slot a hub needed. The budget alone was racy: hubs are
	// published one by one while the first hubs are already being parsed, so some
	// facets get queued ahead of later hubs (FIFO in the in-RAM arm; an empty
	// depth-1 claim in the store arm). Each fetched facet then starved a hub of its
	// slot, and with it that hub's 500 URLs — measured: 142/210 hubs at 105k,
	// 104–107/110 at 55k. Blocked facets drain as cheaply as over-budget ones
	// did (no fetch; no record, ShowBlockedInternal off).
	robotsPath := filepath.Join(t.TempDir(), "robots.txt")
	if err := os.WriteFile(robotsPath, []byte("User-agent: *\nDisallow: /facet/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Robots.Custom = []config.CustomRobots{{Host: urlutil.Host(srv.URL), File: robotsPath}}
	cfg.Robots.ShowBlockedInternal = false
	// The seed + every hub, plus one spare slot: a facet that ever gets fetched
	// then shows up in the Crawled check below instead of silently taking a
	// hub's place.
	cfg.Limits.MaxURLs = hubs + 2

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

	// Appended by the sampler goroutine, then once more by this one after
	// wg.Wait(), so the two never touch it concurrently.
	var samples []uint64
	sample := func() {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		samples = append(samples, m.HeapInuse)
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
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	sample() // a crawl shorter than one tick still gets measured

	if res.Crawled != hubs+1 {
		t.Fatalf("fixture drift: crawled %d pages, want exactly the seed + %d hubs — the frontier under test is not the nominal %d URLs",
			res.Crawled, hubs, fanout)
	}
	if p := sustainedPeak(samples); p > base.HeapInuse {
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
//
// Margins, from repeated runs at GOMAXPROCS 2/4/10 (58 of the bounded arm, 34
// of the in-RAM arm; sustained peaks, see facetCrawlPeak): the bounded queue
// measures -0.4..+1.2 MB (median +0.5); the in-RAM queue measures +5.3..+7.8
// MB (median +6.7). That is ~140 B per queued URL: an ~80 B URL string plus a
// 48 B frontier.Item slot in a slice grown ~1.25x at a time.
func TestFrontierRAMSlopeFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-crawl memory gate — skipped in -short")
	}
	const small, large = 5_000, 55_000
	// 2.5 MB: >2x the worst bounded-queue run, and failed by any regression
	// that retains over ~1/3 of the in-RAM queue's per-URL cost. It was 4 MB
	// under the plain max, whose spike bias added ~2 MB to every flat run (median
	// +1.9 MB), so a leak of ~2 MB already failed there. Dropping the bias
	// without lowering the threshold would have let a larger leak through.
	const maxFlatSlope = 5 << 19
	// The detector floor is the gate's threshold + 25% (3.1 MB): the in-RAM
	// queue must fail the gate by a clear margin, not graze it. It sits ~2 MB
	// from both sides, above the worst flat run and below the weakest linear one,
	// so neither a noisy peak nor a blind harness lands on the wrong side. Tying
	// it to maxFlatSlope keeps the two in step: tighten the gate and the detector
	// must still prove it can see a failure of the tighter gate.
	const minLinearSlope = maxFlatSlope + maxFlatSlope/4

	smallPeak := facetCrawlPeak(t, small, false)
	largePeak := facetCrawlPeak(t, large, false)
	slope := int64(largePeak) - int64(smallPeak)
	t.Logf("store-backed queue: peak(+%dk URLs) - peak(+%dk URLs) = %+.1f MB",
		large/1000, small/1000, float64(slope)/(1<<20))
	if slope > maxFlatSlope {
		t.Errorf("peak RAM grew %.1f MB across a %dk-URL frontier delta — RAM is still frontier-linear (want < %.1f MB)",
			float64(slope)/(1<<20), (large-small)/1000, float64(maxFlatSlope)/(1<<20))
	}

	memSmall := facetCrawlPeak(t, small, true)
	memLarge := facetCrawlPeak(t, large, true)
	memSlope := int64(memLarge) - int64(memSmall)
	t.Logf("in-RAM queue fallback: slope = %+.1f MB (%.0f B/URL)",
		float64(memSlope)/(1<<20), float64(memSlope)/float64(large-small))
	if memSlope < minLinearSlope {
		t.Errorf("detector check failed: the in-RAM queue arm grew only %.1f MB (want > %.1f MB) — the harness cannot see the failure mode it gates",
			float64(memSlope)/(1<<20), float64(minLinearSlope)/(1<<20))
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
	// The site-check pass probes the root out-of-band by design (AI-bot live
	// probes: one "/" fetch per fetcher UA + a control) — exclude it so the
	// counter pins only the frontier's exactly-once contract.
	cfg.SiteChecks.Enabled = "never"
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
