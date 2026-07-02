package main

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// freezeCrawl creates a stored crawl whose frozen config differs from the engine
// defaults, returning its id — the fixture the Setup-view backend reads back.
func freezeCrawl(t *testing.T, a *App, seeds []string, mode string, tweak func(*config.Config)) string {
	t.Helper()
	cfg := config.Default()
	if tweak != nil {
		tweak(cfg)
	}
	st, err := store.CreateCrawl(a.storeDir, seeds, mode, cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()
	return id
}

func TestCrawlConfigReturnsFrozenSettings(t *testing.T) {
	a := testApp(t)
	id := freezeCrawl(t, a, []string{"https://example.com/"}, "spider", func(c *config.Config) {
		c.Speed.MaxThreads = 9
		c.Limits.MaxDepth = 2
	})

	info, err := a.CrawlConfig(id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != "spider" {
		t.Errorf("mode = %q, want spider", info.Mode)
	}
	if len(info.Seeds) != 1 || info.Seeds[0] != "https://example.com/" {
		t.Errorf("seeds = %v, want [https://example.com/]", info.Seeds)
	}
	// The frozen non-default values round-trip into the yaml-keyed JSON view.
	if !strings.Contains(info.ConfigJSON, `"max_threads":9`) {
		t.Errorf("configJson missing frozen max_threads: %s", info.ConfigJSON)
	}
	// Defaults carry the baseline (5), so the "changed from defaults" diff has
	// something to surface.
	if !strings.Contains(info.DefaultsJSON, `"max_threads":5`) {
		t.Errorf("defaultsJson should carry the default threads (5): %s", info.DefaultsJSON)
	}
	if !strings.Contains(info.YAML, "max_threads: 9") {
		t.Errorf("yaml missing frozen config: %s", info.YAML)
	}

	if _, err := a.CrawlConfig("does-not-exist"); err == nil {
		t.Error("expected error for unknown crawl id")
	}
}

func TestSaveCrawlConfigAsProfile(t *testing.T) {
	a := testApp(t)
	id := freezeCrawl(t, a, []string{"https://ex.com/"}, "spider", func(c *config.Config) {
		c.Speed.MaxThreads = 7
	})

	name, err := a.SaveCrawlConfigAsProfile(id, "From crawl")
	if err != nil {
		t.Fatal(err)
	}
	if name != "From crawl" {
		t.Errorf("saved name = %q, want %q", name, "From crawl")
	}

	names, err := a.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range names {
		if n == "From crawl" {
			found = true
		}
	}
	if !found {
		t.Errorf("saved profile not listed: %v", names)
	}
	// The profile carries the frozen value, so it reproduces these settings.
	js, err := a.GetProfileConfig("From crawl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"max_threads":7`) {
		t.Errorf("profile missing frozen value: %s", js)
	}

	// Never clobber an existing profile, and require a usable name.
	if _, err := a.SaveCrawlConfigAsProfile(id, "From crawl"); err == nil {
		t.Error("expected duplicate profile name to be rejected")
	}
	if _, err := a.SaveCrawlConfigAsProfile(id, "   "); err == nil {
		t.Error("expected empty profile name to be rejected")
	}
}

func TestRerunCrawlEnqueuesFrozenConfig(t *testing.T) {
	a := testApp(t)
	id := freezeCrawl(t, a, []string{"https://ex.com/"}, "spider", func(c *config.Config) {
		c.Limits.MaxDepth = 3
	})

	jobID, err := a.RerunCrawl(id)
	if err != nil {
		t.Fatal(err)
	}
	if jobID == "" {
		t.Fatal("RerunCrawl returned an empty job id")
	}

	jobs, err := a.disp.List()
	if err != nil {
		t.Fatal(err)
	}
	var job *queue.Job
	for i := range jobs {
		if jobs[i].ID == jobID {
			job = &jobs[i]
		}
	}
	if job == nil {
		t.Fatalf("re-run job %s not found in queue", jobID)
	}
	// The re-run travels as a fully-frozen ConfigYAML spec of the same site — not
	// a profile lookup — so it reproduces the original crawl exactly.
	if job.Spec.ConfigYAML == "" {
		t.Error("re-run spec is missing the frozen ConfigYAML")
	}
	if job.Spec.URL != "https://ex.com/" {
		t.Errorf("re-run URL = %q, want the original seed", job.Spec.URL)
	}
	if !strings.Contains(job.Spec.ConfigYAML, "max_depth: 3") {
		t.Errorf("re-run did not carry the frozen config: %s", job.Spec.ConfigYAML)
	}
}
