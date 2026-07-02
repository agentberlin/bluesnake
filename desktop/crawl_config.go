package main

import (
	"fmt"
	"os"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// CrawlConfigInfo is the frozen configuration a crawl ran with, surfaced to the
// read-only "Setup" tab. The config, seeds, and mode are frozen into the crawl's
// own database at creation (store.CreateCrawl) and never change afterwards — so
// this is the exact, permanent record of how the crawl was configured, available
// while it is still running as much as after it finishes.
type CrawlConfigInfo struct {
	Seeds        []string `json:"seeds"`
	Mode         string   `json:"mode"`         // spider | list
	ConfigJSON   string   `json:"configJson"`   // yaml-keyed JSON of the frozen config
	DefaultsJSON string   `json:"defaultsJson"` // yaml-keyed JSON of config.Default() — for the "changed from defaults" diff
	YAML         string   `json:"yaml"`         // the raw frozen config YAML (Copy / Save-as-profile)
}

// CrawlConfig returns the configuration a crawl was frozen with, plus the engine
// defaults to diff against. Both configs are encoded through configMapJSON so the
// Setup view indexes them by the same dotted yaml-tag paths the Settings tree uses.
func (a *App) CrawlConfig(id string) (CrawlConfigInfo, error) {
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	defer st.Close()

	cfgYAML, err := st.Meta("config")
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	// Round-trip through config.Load so the frozen config is normalised to exactly
	// the same shape as config.Default() (every key present) — the diff and the
	// full read-only tree then line up field-for-field.
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	configJSON, err := configMapJSON(cfg)
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	defaultsJSON, err := configMapJSON(config.Default())
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	seeds, err := st.Seeds()
	if err != nil {
		return CrawlConfigInfo{}, err
	}
	mode, _ := st.Meta("mode")

	return CrawlConfigInfo{
		Seeds:        seeds,
		Mode:         mode,
		ConfigJSON:   configJSON,
		DefaultsJSON: defaultsJSON,
		YAML:         cfgYAML,
	}, nil
}

// RerunCrawl starts a fresh crawl of the same site with the crawl's identical
// frozen config (same seeds, same every knob). It mirrors ResumeCrawl but, rather
// than continuing the old crawl, enqueues a brand-new one carrying the frozen
// config as a ConfigYAML spec — the executor's already-wired frozen-config path
// (see internal/runner/runner.go). Returns the queue job id.
func (a *App) RerunCrawl(id string) (string, error) {
	a.ensureQueue()

	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return "", err
	}
	cfgYAML, err := st.Meta("config")
	if err != nil {
		st.Close()
		return "", err
	}
	seeds, err := st.Seeds()
	if err != nil {
		st.Close()
		return "", err
	}
	mode, _ := st.Meta("mode")
	st.Close()

	if len(seeds) == 0 {
		return "", fmt.Errorf("crawl %q has no seeds to re-run", id)
	}
	spec := queue.JobSpec{Mode: mode, ConfigYAML: cfgYAML}
	if mode == "list" {
		spec.URLs = seeds
	} else {
		spec.URL = seeds[0]
	}
	if err := runner.ValidateSpec(a.storeDir, spec); err != nil {
		return "", err
	}
	j, err := a.disp.Enqueue(spec, "manual", "", "re-run "+seeds[0])
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

// SaveCrawlConfigAsProfile turns a crawl's frozen config into a reusable named
// profile. Once saved it appears in the profile picker everywhere, so the exact
// settings this crawl used can be pointed at any other site from New Crawl.
// Returns the saved profile name. Existing profiles are never overwritten.
func (a *App) SaveCrawlConfigAsProfile(id, name string) (string, error) {
	if profileSlug(name) == "" {
		return "", fmt.Errorf("profile name required")
	}
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return "", err
	}
	cfgYAML, err := st.Meta("config")
	st.Close()
	if err != nil {
		return "", err
	}
	if _, err := config.Load([]byte(cfgYAML)); err != nil {
		return "", err
	}
	path := a.profilePath(name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("a profile named %q already exists", name)
	}
	if err := os.MkdirAll(a.profilesDir(), 0o755); err != nil {
		return "", err
	}
	header := "# " + name + "\n"
	if err := os.WriteFile(path, append([]byte(header), cfgYAML...), 0o644); err != nil {
		return "", err
	}
	return name, nil
}
