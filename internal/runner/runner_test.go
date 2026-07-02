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

// fixtureServer is a tiny deterministic site: "/" links to /a and /b.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/a">a</a> <a href="/b">b</a></body></html>`)
		default:
			fmt.Fprint(w, `<html><head><title>P</title></head><body><p>x</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recObs records lifecycle callbacks and can pause the executor after N pages.
type recObs struct {
	exec       *Executor
	pauseAfter int

	mu      sync.Mutex
	starts  int
	startID string
	pages   int
	dones   int
	outcome Outcome
}

func (o *recObs) OnStart(crawlID, seed string) {
	o.mu.Lock()
	o.starts++
	o.startID = crawlID
	o.mu.Unlock()
}

func (o *recObs) OnPage(rec *crawler.PageRecord) {
	o.mu.Lock()
	o.pages++
	n := o.pages
	o.mu.Unlock()
	if o.pauseAfter > 0 && n == o.pauseAfter {
		o.exec.Pause()
	}
}

func (o *recObs) OnDone(out Outcome) {
	o.mu.Lock()
	o.dones++
	o.outcome = out
	o.mu.Unlock()
}

func single(threads int) map[string]any { return map[string]any{"speed.max_threads": threads} }

func TestExecutorRunCompletes(t *testing.T) {
	srv := fixtureServer(t)
	dir := t.TempDir()
	obs := &recObs{}
	e := New(dir, obs)

	var cbID string
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)},
		func(id string) { cbID = id })
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("status = %q, want completed", status)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.starts != 1 || obs.dones != 1 {
		t.Fatalf("observer starts=%d dones=%d, want 1/1", obs.starts, obs.dones)
	}
	if cbID == "" || cbID != obs.startID || cbID != obs.outcome.CrawlID {
		t.Fatalf("crawl id mismatch: cb=%q start=%q outcome=%q", cbID, obs.startID, obs.outcome.CrawlID)
	}
	if obs.pages < 3 {
		t.Errorf("observed %d pages, want >=3 (/, /a, /b)", obs.pages)
	}
	if obs.outcome.Status != store.StatusCompleted || obs.outcome.Crawled < 3 {
		t.Errorf("outcome = %+v, want completed with >=3 crawled", obs.outcome)
	}

	// idle now: no live snapshot
	if _, ok := e.Snapshot(); ok {
		t.Error("Snapshot ok=true after the crawl finished, want idle")
	}

	// registry records it completed
	infos, _ := store.ListCrawls(dir)
	if len(infos) != 1 || infos[0].Status != store.StatusCompleted {
		t.Fatalf("registry = %+v, want one completed crawl", infos)
	}
}

func TestExecutorPauseThenResume(t *testing.T) {
	srv := fixtureServer(t)
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 1}
	e := New(dir, obs)
	obs.exec = e

	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusInterrupted {
		t.Fatalf("paused crawl status = %q, want interrupted (resumable)", status)
	}
	crawlID := obs.startID
	infos, _ := store.ListCrawls(dir)
	if len(infos) != 1 || infos[0].Status != store.StatusInterrupted {
		t.Fatalf("registry after pause = %+v, want one interrupted crawl", infos)
	}

	// resume the same crawl to completion
	resObs := &recObs{}
	re := New(dir, resObs)
	status, err = re.Run(context.Background(), queue.JobSpec{ResumeID: crawlID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resumed crawl status = %q, want completed", status)
	}
	infos, _ = store.ListCrawls(dir)
	if len(infos) != 1 || infos[0].ID != crawlID || infos[0].Status != store.StatusCompleted {
		t.Fatalf("registry after resume = %+v, want the same crawl now completed", infos)
	}
}

func TestExecutorRejectsBadSpec(t *testing.T) {
	dir := t.TempDir()
	e := New(dir, nil)
	if _, err := e.Run(context.Background(), queue.JobSpec{URL: "not-a-url"}, nil); err == nil {
		t.Error("bad spider URL accepted")
	}
	if _, err := e.Run(context.Background(), queue.JobSpec{Mode: "list"}, nil); err == nil {
		t.Error("list mode with no URLs accepted")
	}
	// nothing should have been registered
	if infos, _ := store.ListCrawls(dir); len(infos) != 0 {
		t.Errorf("rejected specs still created crawls: %+v", infos)
	}
}

// TestSinkForwardsSitemapEntry pins that the executor's teeing sink forwards the
// optional SitemapEntry extension to the store — without it the sitemap analyses
// silently do nothing. (Previously pinned at the MCP layer's runnerSink.)
func TestSinkForwardsSitemapEntry(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s := &sink{Crawl: st, r: &run{st: st}}
	if err := s.SitemapEntry("http://ex.com/sitemap.xml", "http://ex.com/page"); err != nil {
		t.Fatal(err)
	}
	idx, err := st.SitemapIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := idx["http://ex.com/page"]; len(got) != 1 || got[0] != "http://ex.com/sitemap.xml" {
		t.Errorf("sitemap entry not forwarded through the tee: index = %v", idx)
	}
}

func TestBuildConfigOverridesAndListMode(t *testing.T) {
	dir := t.TempDir()
	cfg, err := BuildConfig(dir, queue.JobSpec{Config: map[string]any{
		"speed.max_threads": 9, "limits.max_depth": 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 9 || cfg.Limits.MaxDepth != 2 {
		t.Errorf("overrides not applied: threads=%d depth=%d", cfg.Speed.MaxThreads, cfg.Limits.MaxDepth)
	}

	listCfg, err := BuildConfig(dir, queue.JobSpec{Mode: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if listCfg.Mode != "list" {
		t.Errorf("list mode not set on config: %q", listCfg.Mode)
	}

	seeds, mode, err := ResolveSeeds(context.Background(), config.Default(),
		queue.JobSpec{Mode: "list", URLs: []string{"https://e.com/a", "https://e.com/b"}})
	if err != nil || mode != "list" || len(seeds) != 2 {
		t.Fatalf("ResolveSeeds list = %v %q %v", seeds, mode, err)
	}
}
