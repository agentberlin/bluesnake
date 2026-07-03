package main

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/store"
)

// mkStoredCrawl freezes a crawl into the app's store the way a real crawl
// starts, so "last crawl setup" resolution has something to find.
func mkStoredCrawl(t *testing.T, dir, seed string, mutate func(*config.Config)) string {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()
	return id
}

// The form's quick knobs are absolute overrides — but only when the user
// actually touched them. Untouched knobs arrive as sentinels (rate -1, zero
// values elsewhere) and emit no override, so the selected base (last crawl
// setup, app settings, or a profile) shows through unmangled.
func TestStartRequestUntouchedKnobs(t *testing.T) {
	spec := StartRequest{Mode: "spider", URL: "https://ex.com", ConfigSource: "last", Rate: -1}.toSpec()
	if spec.ConfigSource != "last" {
		t.Errorf("configSource lost in toSpec: %q", spec.ConfigSource)
	}
	if len(spec.Config) != 0 {
		t.Errorf("untouched knobs must emit no overrides, got %v", spec.Config)
	}

	// touched knobs still override absolutely — including rate 0 (= unlimited)
	spec = StartRequest{Mode: "spider", URL: "https://ex.com", Rate: 0, Threads: 2}.toSpec()
	if got := spec.Config["speed.max_urls_per_sec"]; got != float64(0) {
		t.Errorf("rate 0 must stay an explicit unlimited override, got %v", got)
	}
	if got := spec.Config["speed.max_threads"]; got != 2 {
		t.Errorf("threads override = %v, want 2", got)
	}
}

// SetupPreview resolves through the same runner.ResolveBase path FreezeSpec
// freezes through, so the card can never show a setup a crawl wouldn't run.
func TestSetupPreview(t *testing.T) {
	a := testApp(t)
	id := mkStoredCrawl(t, a.storeDir, "https://ex.com/", func(c *config.Config) {
		c.Limits.MaxDepth = 3
		c.Speed.MaxThreads = 7
		c.Speed.MaxURLsPerSec = 2
		c.Rendering.Mode = "javascript"
		c.SiteChecks.Enabled = "never"
	})

	p, err := a.SetupPreview("last", "", "https://ex.com/anything")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "last" || p.CrawlID != id || p.Started == 0 {
		t.Fatalf("last preview provenance = %+v, want source last, crawl %s", p, id)
	}
	if p.Depth != 3 || p.Threads != 7 || p.Rate != 2 || p.Rendering != "javascript" || p.SiteChecks != "off" {
		t.Errorf("last preview knobs = %+v, want 3/7/2/javascript/off", p)
	}

	// never crawled -> app settings fallback, visibly
	p, err = a.SetupPreview("last", "", "https://never.example/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "app" || p.CrawlID != "" {
		t.Errorf("fallback preview = %+v, want source app", p)
	}

	// a named profile resolves as itself
	writeDesktopProfile(t, a, "Slow", "speed:\n  max_threads: 3\n")
	p, err = a.SetupPreview("", "Slow", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "profile" || p.Threads != 3 {
		t.Errorf("profile preview = %+v, want source profile, threads 3", p)
	}
}

// writeDesktopProfile saves a named profile exactly like the Settings page.
func writeDesktopProfile(t *testing.T, a *App, name, body string) {
	t.Helper()
	if err := a.ensureDefaultProfile(); err != nil { // creates the profiles dir
		t.Fatal(err)
	}
	if err := a.SaveProfileYAML(name, "# "+name+"\n"+body); err != nil {
		t.Fatal(err)
	}
}

// Crawl-all's default mode: every member crawls with its own site's last
// setup, falling back to the app settings for never-crawled members — per
// (project, member) storage is not needed because the setup belongs to the
// domain (#88).
func TestProjectCrawlAllPerSiteSetups(t *testing.T) {
	a := testApp(t)
	a.ensureQueue()
	pa := NewProjectApp(a)

	s, err := project.Open(a.storeDir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject("", "https://main.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(p.ID, "rival.com", project.RoleCompetitor); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// main.com remembers a heavy setup; rival.com was never crawled
	mkStoredCrawl(t, a.storeDir, "https://main.com/", func(c *config.Config) { c.Speed.MaxThreads = 7 })

	n, err := pa.CrawlAll(p.ID, StartRequest{ConfigSource: "last", Rate: -1})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("CrawlAll enqueued %d jobs, want 2", n)
	}

	jobs, err := a.disp.List()
	if err != nil {
		t.Fatal(err)
	}
	byLabel := map[string]*config.Config{}
	for _, j := range jobs {
		if j.Spec.ConfigSource != "last" {
			t.Errorf("job %s config source = %q, want last (provenance)", j.Label, j.Spec.ConfigSource)
		}
		cfg, err := config.Load([]byte(j.Spec.ConfigYAML))
		if err != nil {
			t.Fatalf("job %s frozen config: %v", j.Label, err)
		}
		byLabel[j.Label] = cfg
	}
	if got := byLabel["main.com"].Speed.MaxThreads; got != 7 {
		t.Errorf("main.com froze threads=%d, want its last crawl's 7", got)
	}
	if want := config.Default().Speed.MaxThreads; byLabel["rival.com"].Speed.MaxThreads != want {
		t.Errorf("rival.com froze threads=%d, want app settings %d", byLabel["rival.com"].Speed.MaxThreads, want)
	}
}

// The crawl-all dialog's per-site plan: which setup each member will use.
func TestCrawlAllPlan(t *testing.T) {
	a := testApp(t)
	pa := NewProjectApp(a)

	s, err := project.Open(a.storeDir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject("", "https://main.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(p.ID, "rival.com", project.RoleCompetitor); err != nil {
		t.Fatal(err)
	}
	s.Close()

	id := mkStoredCrawl(t, a.storeDir, "https://main.com/", nil)

	plan, err := pa.CrawlAllPlan(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan has %d rows, want 2", len(plan))
	}
	rows := map[string]MemberSetup{}
	for _, m := range plan {
		rows[m.Domain] = m
	}
	if m := rows["main.com"]; !m.HasLast || m.CrawlID != id || m.Started == 0 {
		t.Errorf("main.com plan = %+v, want last crawl %s", m, id)
	}
	if m := rows["rival.com"]; m.HasLast || m.CrawlID != "" || m.Error != "" {
		t.Errorf("rival.com plan = %+v, want app-settings fallback", m)
	}
	if !strings.HasPrefix(rows["main.com"].Role, "main") {
		t.Errorf("main.com role = %q, want main", rows["main.com"].Role)
	}
}
