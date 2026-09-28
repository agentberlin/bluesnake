package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
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
