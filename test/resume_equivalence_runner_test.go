package acceptance

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"gopkg.in/yaml.v3"
)

// This file pins resume equivalence THROUGH THE PRODUCTION EXECUTOR — the
// runner.Executor + runner.sink path every surface (CLI, MCP, desktop) actually
// drives. TestResumeEquivalence (resume_equivalence_test.go) pins the same
// contract at the library level with a bare *store.Crawl sink; that sink
// implements every optional store capability, so it cannot see a capability the
// production sink wrapper fails to carry (the #74 R2/R3 class). Here session 1
// interrupts and session 2 resumes through Executor.Run, so the full production
// wiring — sink wrapper, resume-state load, finalize — is what's under test.

// pauseObs pauses the executor after N observed pages (runner.Observer).
type pauseObs struct {
	exec  *runner.Executor
	after int

	mu      sync.Mutex
	pages   int
	crawlID string
}

func (o *pauseObs) OnStart(crawlID, seed string) {
	o.mu.Lock()
	o.crawlID = crawlID
	o.mu.Unlock()
}

func (o *pauseObs) OnPage(*crawler.PageRecord) {
	o.mu.Lock()
	o.pages++
	n := o.pages
	o.mu.Unlock()
	if o.after > 0 && n == o.after {
		o.exec.Pause()
	}
}

func (o *pauseObs) OnDone(runner.Outcome) {}

func (o *pauseObs) id() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.crawlID
}

func mustYAML(t *testing.T, cfg *config.Config) string {
	t.Helper()
	b, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

// straightCrawlRunner runs one uninterrupted crawl to completion through the
// production executor.
func straightCrawlRunner(t *testing.T, dir, seed string, cfg *config.Config) string {
	t.Helper()
	obs := &pauseObs{}
	e := runner.New(dir, obs)
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: seed, ConfigYAML: mustYAML(t, cfg)}, nil)
	if err != nil {
		t.Fatalf("straight run: %v", err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("straight run status = %q, want completed", status)
	}
	return obs.id()
}

// interruptResumeCrawlRunner interrupts a crawl after `after` pages via the
// executor's Pause (the production pause path), then resumes it to completion
// through a second executor — the exact two-session lifecycle the desktop, MCP
// and CLI pause+resume drive.
func interruptResumeCrawlRunner(t *testing.T, dir, seed string, cfg *config.Config, after int) string {
	t.Helper()
	obs := &pauseObs{after: after}
	e := runner.New(dir, obs)
	obs.exec = e
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: seed, ConfigYAML: mustYAML(t, cfg)}, nil)
	if err != nil {
		t.Fatalf("session 1: %v", err)
	}
	if status != store.StatusInterrupted {
		t.Fatalf("session 1 status = %q, want interrupted", status)
	}
	id := obs.id()

	status, err = runner.New(dir, nil).Run(context.Background(),
		queue.JobSpec{ResumeID: id}, nil)
	if err != nil {
		t.Fatalf("session 2 (resume): %v", err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("session 2 status = %q, want completed", status)
	}
	return id
}

// TestResumeEquivalence_ThroughRunner is the through-the-production-sink twin of
// TestResumeEquivalence: a pause+resume driven through runner.Executor must
// finalise byte-identically to a straight crawl. It fails when the production
// sink drops a resume capability the bare store has — e.g. the edge-seq
// continuation (#74 R2: session-2 edges undercut session-1's, flipping
// first-wins discovered_from) — the exact class the library-level test is
// structurally blind to.
func TestResumeEquivalence_ThroughRunner(t *testing.T) {
	srv := equivServer(t)
	seed := srv.URL + "/"
	dir := t.TempDir()

	straightID := straightCrawlRunner(t, dir, seed, equivCfg())
	resumeID := interruptResumeCrawlRunner(t, dir, seed, equivCfg(), 3)

	sPages, sIssues, sCrawled, sTotal := snapshot(t, dir, straightID, srv.URL)
	rPages, rIssues, rCrawled, rTotal := snapshot(t, dir, resumeID, srv.URL)

	if sCrawled != rCrawled || sTotal != rTotal {
		t.Errorf("registry counts differ:\n  straight: crawled=%d total=%d\n  resumed:  crawled=%d total=%d",
			sCrawled, sTotal, rCrawled, rTotal)
	}
	if len(sPages) != len(rPages) {
		t.Errorf("page-set size differs: straight=%d resumed=%d", len(sPages), len(rPages))
	}
	for url, sp := range sPages {
		rp, ok := rPages[url]
		if !ok {
			t.Errorf("%s present in straight crawl but missing from resumed crawl", url)
			continue
		}
		if sp.Inlinks != rp.Inlinks {
			t.Errorf("%s inlinks differ: straight=%d resumed=%d", url, sp.Inlinks, rp.Inlinks)
		}
		if sp.DiscoveredFrom != rp.DiscoveredFrom {
			t.Errorf("%s discovered_from differs: straight=%q resumed=%q", url, sp.DiscoveredFrom, rp.DiscoveredFrom)
		}
		if sp.UniqueInlinks != rp.UniqueInlinks {
			t.Errorf("%s unique_inlinks differ: straight=%d resumed=%d", url, sp.UniqueInlinks, rp.UniqueInlinks)
		}
		if sp.UniqueOutlinks != rp.UniqueOutlinks {
			t.Errorf("%s unique_outlinks differ: straight=%d resumed=%d", url, sp.UniqueOutlinks, rp.UniqueOutlinks)
		}
		if sp.Depth != rp.Depth {
			t.Errorf("%s depth differs: straight=%d resumed=%d", url, sp.Depth, rp.Depth)
		}
		if sp.State != rp.State || sp.Scope != rp.Scope || sp.Indexable != rp.Indexable {
			t.Errorf("%s state/scope/indexable differ: straight=%+v resumed=%+v", url, sp, rp)
		}
		if math.Abs(sp.LinkScore-rp.LinkScore) > 0.01 {
			t.Errorf("%s link_score differs: straight=%.4f resumed=%.4f", url, sp.LinkScore, rp.LinkScore)
		}
	}
	if len(sIssues) != len(rIssues) {
		t.Errorf("issue-check set differs: straight=%v resumed=%v", sIssues, rIssues)
	}
	for id, n := range sIssues {
		if rIssues[id] != n {
			t.Errorf("issue %q count differs: straight=%d resumed=%d", id, n, rIssues[id])
		}
	}
}

// capRoutes is built so a page crawled only in session 2 (/b) discovers NEW
// URLs (/f, /g, /h) in a depth bucket (depth 2) that already filled its
// MaxURLsPerDepth budget in session 1 (/c, /d, /e). A resume that restarts the
// per-bucket counters at zero (#74 R3) admits /g and /h — pages a straight
// crawl never crawls.
var capRoutes = map[string]string{
	"/":  `<a href="/a">a</a> <a href="/b">b</a>`,
	"/a": `<a href="/c">c</a> <a href="/d">d</a> <a href="/e">e</a>`,
	"/b": `<a href="/f">f</a> <a href="/g">g</a> <a href="/h">h</a>`,
	"/c": `c leaf`, "/d": `d leaf`, "/e": `e leaf`,
	"/f": `f leaf`, "/g": `g leaf`, "/h": `h leaf`,
}

func capServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range capRoutes {
		p, b := path, body
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != p {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title></head><body>%s</body></html>", p, b)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestResume_NoOverAdmitPerBucket_ThroughRunner pins FR-08 through the
// production sink (doc §8 test #4): under MaxURLsPerDepth, a paused+resumed
// crawl must admit exactly the set a straight crawl admits. With the counter
// rehydration inert on the production path (#74 R3) the resumed session grants
// the depth-2 bucket a fresh budget and over-admits.
func TestResume_NoOverAdmitPerBucket_ThroughRunner(t *testing.T) {
	srv := capServer(t)
	seed := srv.URL + "/"

	cfg := func() *config.Config {
		c := config.Default()
		c.Speed.MaxThreads = 1 // deterministic order → deterministic interrupt point
		c.Limits.MaxURLsPerDepth = 4
		return c
	}

	// Straight: / -> a,b admitted (depth 1); a -> c,d,e admitted (depth 2 = 3);
	// b -> f admitted (depth 2 = 4), g and h rejected. 7 pages total.
	straightDir := t.TempDir()
	straightID := straightCrawlRunner(t, straightDir, seed, cfg())
	sPages, _, _, _ := snapshot(t, straightDir, straightID, srv.URL)

	// Interrupt after 2 pages (/, /a): the depth-2 bucket already holds c,d,e.
	// Session 2 crawls /b, whose discoveries must count against that budget.
	resumeDir := t.TempDir()
	resumeID := interruptResumeCrawlRunner(t, resumeDir, seed, cfg(), 2)
	rPages, _, _, _ := snapshot(t, resumeDir, resumeID, srv.URL)

	if len(rPages) != len(sPages) {
		t.Errorf("page-set size differs under a per-depth cap: straight=%d resumed=%d (over-admission on resume)",
			len(sPages), len(rPages))
	}
	for url := range rPages {
		if _, ok := sPages[url]; !ok {
			t.Errorf("%s crawled only by the resumed crawl — the per-depth cap did not bind across the interrupt", url)
		}
	}
	for url := range sPages {
		if _, ok := rPages[url]; !ok {
			t.Errorf("%s crawled only by the straight crawl — resumed crawl under-admitted", url)
		}
	}
}
