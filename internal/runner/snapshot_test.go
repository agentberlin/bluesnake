package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// mixedStatusServer serves one page of each status class the live breakdown
// separates: "/" links to two 200 leaves, a 301 and a 404. Every other path
// (robots.txt, sitemaps, llms.txt) is a 404 that is never recorded as a page,
// so a full crawl records exactly five pages.
func mixedStatusServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><body><a href="/a">a</a> <a href="/b">b</a> <a href="/old">old</a> <a href="/gone">gone</a></body></html>`)
		case "/a", "/b":
			fmt.Fprint(w, `<html><head><title>Leaf</title></head><body><p>leaf</p></body></html>`)
		case "/old":
			http.Redirect(w, r, "/a", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// snapObs reads the executor's live snapshot at each lifecycle point, the way
// a progress surface would, and can pause the crawl after N pages.
type snapObs struct {
	exec       *Executor
	pauseAfter int

	mu                  sync.Mutex
	pages               int
	finalizingMidCrawl  bool
	atStart, atDone     Snapshot
	startLive, doneLive bool
}

func (o *snapObs) OnStart(crawlID, _ string) {
	s, ok := o.exec.SnapshotCrawl(crawlID)
	o.mu.Lock()
	o.atStart, o.startLive = s, ok
	o.mu.Unlock()
}

func (o *snapObs) OnPage(crawlID string, _ *crawler.PageRecord) {
	s, _ := o.exec.SnapshotCrawl(crawlID)
	o.mu.Lock()
	o.pages++
	n := o.pages
	if s.Finalizing {
		o.finalizingMidCrawl = true
	}
	o.mu.Unlock()
	if o.pauseAfter > 0 && n == o.pauseAfter {
		o.exec.Pause()
	}
}

func (o *snapObs) OnDone(out Outcome) {
	s, ok := o.exec.SnapshotCrawl(out.CrawlID)
	o.mu.Lock()
	o.atDone, o.doneLive = s, ok
	o.mu.Unlock()
}

// runSnap runs one job through a fresh executor with a snapObs attached.
func runSnap(t *testing.T, dir string, spec queue.JobSpec, pauseAfter int) *snapObs {
	t.Helper()
	obs := &snapObs{pauseAfter: pauseAfter}
	obs.exec = New(dir, obs)
	if _, err := obs.exec.Run(context.Background(), spec, nil); err != nil {
		t.Fatal(err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if !obs.startLive || !obs.doneLive {
		t.Fatalf("snapshot not live at OnStart (%v) / OnDone (%v)", obs.startLive, obs.doneLive)
	}
	return obs
}

type breakdown struct{ total, s2, s3, s4, s5, blocked, noresp, indexable int }

func breakdownOf(s Snapshot) breakdown {
	return breakdown{s.Total, s.S2xx, s.S3xx, s.S4xx, s.S5xx, s.Blocked, s.NoResponse, s.Indexable}
}

// TestSnapshotFinalizingFlag pins the phase flag a headless progress feed
// uses to tell post-crawl analysis from a stalled crawl: clear while pages
// stream, set once the engine has returned. OnDone still reads a live
// snapshot, and its counters are final by then.
func TestSnapshotFinalizingFlag(t *testing.T) {
	srv := mixedStatusServer(t)
	obs := runSnap(t, t.TempDir(), queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, 0)

	if obs.atStart.Finalizing || obs.finalizingMidCrawl {
		t.Errorf("Finalizing set while the crawl was running (start=%v, mid-crawl=%v)", obs.atStart.Finalizing, obs.finalizingMidCrawl)
	}
	if !obs.atDone.Finalizing {
		t.Error("Finalizing not set at OnDone, after the engine returned")
	}
	want := breakdown{total: 5, s2: 3, s3: 1, s4: 1, indexable: 3} // "/", /a, /b; /old; /gone
	if got := breakdownOf(obs.atDone); got != want {
		t.Errorf("final breakdown = %+v, want %+v", got, want)
	}
}

// TestResumeSeedsLiveBreakdown pins that a resumed crawl's live status
// breakdown covers the whole crawl, like its processed/discovered counters.
// Before, the breakdown restarted at zero on resume while processed carried
// over, so a resumed progress feed could report e.g. processed 5 with a single
// 2xx page.
func TestResumeSeedsLiveBreakdown(t *testing.T) {
	srv := mixedStatusServer(t)
	spec := queue.JobSpec{URL: srv.URL + "/", Config: single(1)}

	straight := runSnap(t, t.TempDir(), spec, 0)

	dir := t.TempDir()
	first := runSnap(t, dir, spec, 2)
	if first.atDone.Total >= straight.atDone.Total {
		t.Fatalf("pause after 2 pages still processed %d of %d; nothing left to resume", first.atDone.Total, straight.atDone.Total)
	}
	resumed := runSnap(t, dir, queue.JobSpec{ResumeID: first.atDone.CrawlID}, 0)

	if resumed.atStart.Total == 0 {
		t.Fatal("resumed crawl did not carry the first session's processed count")
	}
	// At start the resumed session has processed nothing itself, so its whole
	// breakdown is the first session's, and it adds up to processed.
	s := resumed.atStart
	if sum := s.S2xx + s.S3xx + s.S4xx + s.S5xx + s.Blocked + s.NoResponse; sum != s.Total {
		t.Errorf("resumed breakdown at start sums to %d, processed = %d: %+v", sum, s.Total, breakdownOf(s))
	}
	if got, want := breakdownOf(resumed.atDone), breakdownOf(straight.atDone); got != want {
		t.Errorf("resumed crawl's final live breakdown = %+v, want the straight crawl's %+v", got, want)
	}
}

// TestLiveBreakdownMatchesStoredBreakdown pins the live classification
// (run.onPage) to the stored one (store.PageBreakdown), which seeds a resumed
// crawl's feed and is the bundle header's status_counts. Every kind of page
// goes through the production sink, so each is persisted and counted live
// exactly as in a crawl; the two breakdowns must agree and each must count
// every page exactly once, including pages the status classes miss.
func TestLiveBreakdownMatchesStoredBreakdown(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &run{st: st}
	s := &sink{Crawl: st, r: r}

	pages := []*crawler.PageRecord{
		{URL: "https://ex.com/", State: crawler.StateCrawled, StatusCode: 200, Indexable: true},
		{URL: "https://ex.com/old", State: crawler.StateCrawled, StatusCode: 301},
		{URL: "https://ex.com/gone", State: crawler.StateCrawled, StatusCode: 404},
		{URL: "https://ex.com/boom", State: crawler.StateCrawled, StatusCode: 503},
		{URL: "https://ex.com/odd", State: crawler.StateCrawled, StatusCode: 999},
		{URL: "https://ex.com/big", State: crawler.StateSkippedTooLarge, StatusCode: 200, Indexable: true},
		{URL: "https://ex.com/block", State: crawler.StateBlockedRobots, IndexabilityStatus: "Blocked by Robots.txt"},
		{URL: "https://ex.com/err", State: crawler.StateError, FetchError: "EOF"},
		{URL: "https://ex.com/zero", State: crawler.StateCrawled, StatusCode: 0},
		{URL: "https://ex.com/switch", State: crawler.StateCrawled, StatusCode: 101, Indexable: true},
		{URL: "https://ex.com/early", State: crawler.StateSkippedTooLarge, StatusCode: 103},
	}
	for _, p := range pages {
		p.Scope = "internal"
		if err := s.Page(p); err != nil {
			t.Fatal(err)
		}
	}

	live := r.snapshot()
	stored, err := st.StatusCounts()
	if err != nil {
		t.Fatal(err)
	}
	liveCounts := store.StatusCounts{
		S2xx: live.S2xx, S3xx: live.S3xx, S4xx: live.S4xx, S5xx: live.S5xx,
		Blocked: live.Blocked, NoResponse: live.NoResponse, Indexable: live.Indexable,
	}
	if liveCounts != stored {
		t.Errorf("live breakdown %+v disagrees with the stored %+v", liveCounts, stored)
	}
	if sum := live.S2xx + live.S3xx + live.S4xx + live.S5xx + live.Blocked + live.NoResponse; sum != live.Total || live.Total != len(pages) {
		t.Errorf("live breakdown sums to %d, processed = %d, pages = %d: %+v", sum, live.Total, len(pages), breakdownOf(live))
	}
	want := store.StatusCounts{S2xx: 2, S3xx: 1, S4xx: 1, S5xx: 2, Blocked: 1, NoResponse: 4, Indexable: 3}
	if stored != want {
		t.Errorf("stored breakdown = %+v, want %+v", stored, want)
	}
}
