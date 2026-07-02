package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestValidateSpec(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		spec queue.JobSpec
		ok   bool
	}{
		{"spider ok", queue.JobSpec{URL: "https://e.com/"}, true},
		{"spider bad url", queue.JobSpec{URL: "nope"}, false},
		{"list empty", queue.JobSpec{Mode: "list"}, false},
		{"list with urls", queue.JobSpec{Mode: "list", URLs: []string{"https://e.com/"}}, true},
		{"list with sitemap", queue.JobSpec{Mode: "list", SitemapURL: "https://e.com/s.xml"}, true},
		{"bad mode", queue.JobSpec{Mode: "weird", URL: "https://e.com/"}, false},
		{"resume deferred", queue.JobSpec{ResumeID: "x"}, true},
		{"config yaml ok", queue.JobSpec{URL: "https://e.com/", ConfigYAML: "speed:\n  max_threads: 3\n"}, true},
		{"config yaml bad key", queue.JobSpec{URL: "https://e.com/", ConfigYAML: "bogus_key: 1\n"}, false},
		{"bad config override", queue.JobSpec{URL: "https://e.com/", Config: map[string]any{"robots.mode": "nonsense"}}, false},
	}
	for _, c := range cases {
		err := ValidateSpec(dir, c.spec)
		if (err == nil) != c.ok {
			t.Errorf("%s: ValidateSpec err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestProfileLoadingAndListing(t *testing.T) {
	dir := t.TempDir()
	if e := New(dir, nil); e.StoreDir() != dir {
		t.Errorf("StoreDir = %q, want %q", e.StoreDir(), dir)
	}

	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "my-profile.yaml"), []byte("# My Profile\nspeed:\n  max_threads: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := ListProfileNames(dir)
	if len(names) != 1 || names[0] != "My Profile" {
		t.Fatalf("ListProfileNames = %v, want [My Profile]", names)
	}
	cfg, err := LoadProfile(dir, "My Profile")
	if err != nil || cfg.Speed.MaxThreads != 7 {
		t.Fatalf("LoadProfile(My Profile) = %v (threads=%d)", err, cfg.Speed.MaxThreads)
	}
	if _, err := LoadProfile(dir, ""); err != nil { // empty -> built-in defaults (no default file)
		t.Errorf("LoadProfile(default) errored: %v", err)
	}
	if _, err := LoadProfile(dir, "does-not-exist"); err == nil {
		t.Error("LoadProfile of a missing named profile should error")
	}
}

// variedServer links to pages spanning the status-code buckets the snapshot
// counts (2xx, 3xx, 4xx, 5xx).
func variedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<a href="/ok">o</a><a href="/r">r</a><a href="/nf">n</a><a href="/err">e</a>`)
		case "/r":
			http.Redirect(w, r, "/ok", 301)
		case "/nf":
			w.WriteHeader(404)
		case "/err":
			w.WriteHeader(500)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>http://%s/ok</loc></url></urlset>`, r.Host)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<p>ok</p>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// liveSnapObs captures a live Snapshot during the crawl (proving Snapshot works
// while a crawl is in flight) and tallies the per-status counters.
type liveSnapObs struct {
	exec *Executor
	mu   sync.Mutex
	snap Snapshot
	ok   bool
}

func (o *liveSnapObs) OnStart(string, string) {}
func (o *liveSnapObs) OnPage(crawlID string, _ *crawler.PageRecord) {
	if s, ok := o.exec.SnapshotCrawl(crawlID); ok {
		o.mu.Lock()
		o.snap, o.ok = s, true
		o.mu.Unlock()
	}
}
func (o *liveSnapObs) OnDone(Outcome) {}

func TestExecutorVariedStatusesAndLiveSnapshot(t *testing.T) {
	srv := variedServer(t)
	dir := t.TempDir()
	obs := &liveSnapObs{}
	e := New(dir, obs)
	obs.exec = e

	status, err := e.Run(context.Background(), queue.JobSpec{
		URL:    srv.URL + "/",
		Config: map[string]any{"speed.max_threads": 1, "extraction.store_html": true, "extraction.store_warc": true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("status = %q, want completed", status)
	}

	obs.mu.Lock()
	defer obs.mu.Unlock()
	if !obs.ok {
		t.Fatal("never captured a live Snapshot during the crawl")
	}
	if obs.snap.Threads != 1 {
		t.Errorf("snapshot threads = %d, want 1", obs.snap.Threads)
	}

	// final stored graph spans the buckets (3xx redirect, 4xx, 5xx all present)
	st, err := store.OpenCrawl(dir, obs.snap.CrawlID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	var s3, s4, s5 int
	for _, p := range pages {
		switch {
		case p.StatusCode >= 500:
			s5++
		case p.StatusCode >= 400:
			s4++
		case p.StatusCode >= 300:
			s3++
		}
	}
	if s3 == 0 || s4 == 0 || s5 == 0 {
		t.Errorf("expected 3xx/4xx/5xx pages, got s3=%d s4=%d s5=%d", s3, s4, s5)
	}
}

// stopObs stops the crawl after the first page; Stop finalises it as completed.
type stopObs struct {
	exec *Executor
	mu   sync.Mutex
	n    int
}

func (o *stopObs) OnStart(string, string) {}
func (o *stopObs) OnPage(string, *crawler.PageRecord) {
	o.mu.Lock()
	o.n++
	first := o.n == 1
	o.mu.Unlock()
	if first {
		o.exec.Stop()
	}
}
func (o *stopObs) OnDone(Outcome) {}

func TestExecutorResumeMissingErrors(t *testing.T) {
	dir := t.TempDir()
	obs := &recObs{} // recObs.OnDone bumps dones — proves the open-failure terminal callback fires
	e := New(dir, obs)
	if _, err := e.Run(context.Background(), queue.JobSpec{ResumeID: "20990101-000000-deadbe"}, nil); err == nil {
		t.Fatal("resume of a missing crawl should error")
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.dones != 1 {
		t.Errorf("OnDone fired %d times on open failure, want 1", obs.dones)
	}
}

func TestExecutorResumeCorruptConfigErrors(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"https://e.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.SetMeta("config", "unknown_field: 1\n"); err != nil { // unloadable config
		t.Fatal(err)
	}
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusInterrupted, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, nil).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil); err == nil {
		t.Fatal("resume with an unloadable frozen config should error")
	}
}

func TestExecutorListMode(t *testing.T) {
	srv := variedServer(t)
	dir := t.TempDir()
	status, err := New(dir, nil).Run(context.Background(), queue.JobSpec{
		Mode: "list", URLs: []string{srv.URL + "/ok", srv.URL + "/nf"},
		Config: map[string]any{"speed.max_threads": 1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("list-mode status = %q, want completed", status)
	}
}

func TestExecutorListModeSitemap(t *testing.T) {
	srv := variedServer(t)
	dir := t.TempDir()
	status, err := New(dir, nil).Run(context.Background(), queue.JobSpec{
		Mode: "list", SitemapURL: srv.URL + "/sitemap.xml",
		Config: map[string]any{"speed.max_threads": 1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("sitemap list-mode status = %q, want completed", status)
	}
}

func TestExecutorStopFinalisesCompleted(t *testing.T) {
	srv := variedServer(t)
	dir := t.TempDir()
	obs := &stopObs{}
	e := New(dir, obs)
	obs.exec = e

	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: map[string]any{"speed.max_threads": 1}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("stopped crawl status = %q, want completed", status)
	}
}
