package runner

import (
	"os"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// mkCrawl freezes a crawl of seed into the store (registry row + crawl DB with
// the config snapshot), the way every real crawl starts, and returns its id.
func mkCrawl(t *testing.T, dir, seed, mode string, mutate func(*config.Config)) string {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	st, err := store.CreateCrawl(dir, []string{seed}, mode, cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

// FindLastSetup keys on the exact lowercased host:port of the seed — the same
// site identity the project layer uses (§5.9): scheme and path never matter,
// www./ports are distinct sites.
func TestFindLastSetupMatchesSiteExactly(t *testing.T) {
	dir := t.TempDir()
	a := mkCrawl(t, dir, "https://a.com/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 3 })
	mkCrawl(t, dir, "https://b.com/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 5 })
	mkCrawl(t, dir, "https://www.a.com/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 7 })
	mkCrawl(t, dir, "https://a.com:8080/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 9 })

	for _, url := range []string{"https://a.com/", "http://a.com/deep/path?q=1", "https://A.COM/"} {
		ls, err := FindLastSetup(dir, url)
		if err != nil {
			t.Fatal(err)
		}
		if ls == nil || ls.CrawlID != a {
			t.Fatalf("FindLastSetup(%q) = %+v, want crawl %s", url, ls, a)
		}
		cfg, err := config.Load([]byte(ls.ConfigYAML))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Limits.MaxDepth != 3 {
			t.Errorf("FindLastSetup(%q) depth = %d, want 3 (www./port/other hosts must not fold in)", url, cfg.Limits.MaxDepth)
		}
	}
}

func TestFindLastSetupNewestWins(t *testing.T) {
	dir := t.TempDir()
	mkCrawl(t, dir, "https://a.com/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 1 })
	second := mkCrawl(t, dir, "https://a.com/blog/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 2 })

	ls, err := FindLastSetup(dir, "https://a.com/")
	if err != nil {
		t.Fatal(err)
	}
	if ls == nil || ls.CrawlID != second {
		t.Fatalf("want the most recent crawl %s, got %+v", second, ls)
	}
}

// A list-mode audit is a different activity with mode-specific config baked in
// (depth 0, robots ignored) — it never becomes a domain's remembered setup.
func TestFindLastSetupSkipsListMode(t *testing.T) {
	dir := t.TempDir()
	spider := mkCrawl(t, dir, "https://a.com/", "spider", func(c *config.Config) { c.Limits.MaxDepth = 4 })
	mkCrawl(t, dir, "https://a.com/x", "list", nil) // newer, but list mode

	ls, err := FindLastSetup(dir, "https://a.com/")
	if err != nil {
		t.Fatal(err)
	}
	if ls == nil || ls.CrawlID != spider {
		t.Fatalf("want the spider crawl %s, got %+v", spider, ls)
	}
}

func TestFindLastSetupNeverCrawled(t *testing.T) {
	ls, err := FindLastSetup(t.TempDir(), "https://never.example/")
	if err != nil || ls != nil {
		t.Fatalf("never-crawled site: want (nil, nil), got (%+v, %v)", ls, err)
	}
}

// A registry row whose crawl DB is gone (deleted out-of-band) must fail loudly
// — silently sliding to an older setup would be spooky.
func TestFindLastSetupUnreadableCrawl(t *testing.T) {
	dir := t.TempDir()
	id := mkCrawl(t, dir, "https://a.com/", "spider", nil)
	path, err := store.CrawlDBPath(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = FindLastSetup(dir, "https://a.com/")
	if err == nil || !strings.Contains(err.Error(), id) {
		t.Fatalf("want an error naming crawl %s, got %v", id, err)
	}
}

// The default source: FreezeSpec with ConfigSource "last" freezes the site's
// previous setup (not the default profile), with dotted overrides on top.
func TestFreezeSpecLastSetup(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, DefaultProfileName, "speed:\n  max_threads: 4\n")
	mkCrawl(t, dir, "https://a.com/", "spider", func(c *config.Config) {
		c.Speed.MaxThreads = 7
		c.Rendering.Mode = "javascript"
	})

	frozen, err := FreezeSpec(dir, queue.JobSpec{
		URL:          "https://a.com/",
		ConfigSource: "last",
		Config:       map[string]any{"limits.max_depth": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 7 || cfg.Rendering.Mode != "javascript" {
		t.Errorf("frozen base: threads=%d rendering=%q, want the last crawl's 7/javascript",
			cfg.Speed.MaxThreads, cfg.Rendering.Mode)
	}
	if cfg.Limits.MaxDepth != 2 {
		t.Errorf("override lost: max_depth=%d, want 2", cfg.Limits.MaxDepth)
	}
	if frozen.ConfigSource != "last" {
		t.Errorf("config source must stay as provenance, got %q", frozen.ConfigSource)
	}
}

// "last" on a never-crawled site falls back to the app settings (the saved
// default profile when present, built-in defaults otherwise).
func TestFreezeSpecLastFallsBackToAppSettings(t *testing.T) {
	dir := t.TempDir()

	frozen, err := FreezeSpec(dir, queue.JobSpec{URL: "https://new.example/", ConfigSource: "last"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if want := config.Default().Speed.MaxThreads; cfg.Speed.MaxThreads != want {
		t.Errorf("no profile saved: threads=%d, want built-in default %d", cfg.Speed.MaxThreads, want)
	}

	writeProfile(t, dir, DefaultProfileName, "speed:\n  max_threads: 4\n")
	frozen, err = FreezeSpec(dir, queue.JobSpec{URL: "https://new.example/", ConfigSource: "last"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 4 {
		t.Errorf("default profile saved: threads=%d, want 4 (app settings)", cfg.Speed.MaxThreads)
	}
}

func TestFreezeSpecConfigSourceValidation(t *testing.T) {
	dir := t.TempDir()
	for name, spec := range map[string]queue.JobSpec{
		"last+profile":   {URL: "https://a.com/", ConfigSource: "last", Profile: "Slow"},
		"last+list mode": {Mode: "list", URLs: []string{"https://a.com/x"}, ConfigSource: "last"},
		"unknown source": {URL: "https://a.com/", ConfigSource: "previous"},
	} {
		if _, err := FreezeSpec(dir, spec); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// ResolveBase is the one resolution path every surface shares (FreezeSpec, the
// desktop's setup preview, the CLI's provenance line) — pin its provenance.
func TestResolveBaseProvenance(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "Slow", "speed:\n  max_threads: 3\n")
	id := mkCrawl(t, dir, "https://a.com/", "spider", func(c *config.Config) { c.Speed.MaxThreads = 7 })

	cfg, src, err := ResolveBase(dir, queue.JobSpec{URL: "https://a.com/", ConfigSource: "last"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != "last" || src.CrawlID != id || cfg.Speed.MaxThreads != 7 {
		t.Errorf("last: got kind=%q crawl=%q threads=%d, want last/%s/7", src.Kind, src.CrawlID, cfg.Speed.MaxThreads, id)
	}

	cfg, src, err = ResolveBase(dir, queue.JobSpec{URL: "https://never.example/", ConfigSource: "last"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Kind != "app" || src.CrawlID != "" {
		t.Errorf("fallback: got kind=%q crawl=%q, want app/(none)", src.Kind, src.CrawlID)
	}

	_, src, err = ResolveBase(dir, queue.JobSpec{URL: "https://a.com/", Profile: "Slow"})
	if err != nil || src.Kind != "profile" {
		t.Errorf("profile: got kind=%q err=%v, want profile/nil", src.Kind, err)
	}

	_, src, err = ResolveBase(dir, queue.JobSpec{URL: "https://a.com/"})
	if err != nil || src.Kind != "app" {
		t.Errorf("no source, no profile: got kind=%q err=%v, want app/nil", src.Kind, err)
	}
}
