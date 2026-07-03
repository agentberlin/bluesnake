package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"gopkg.in/yaml.v3"
)

// Profile resolution and config/seed building, shared by every surface (it used
// to live in internal/mcp; it moved here so the queue executor — the single
// crawl path — owns it and the surfaces don't each rebuild a crawl request).

// profileSlug maps a display name to its <store-dir>/profiles/<slug>.yaml file.
func profileSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

// DefaultProfileName is the profile applied when a job names no profile (the
// saved default if present, otherwise built-in defaults).
const DefaultProfileName = "Default audit"

// LoadProfile resolves a named profile to a config. An empty name means the
// default profile when it exists, otherwise built-in defaults; a named profile
// that doesn't exist is an error (the caller asked for something specific).
func LoadProfile(storeDir, name string) (*config.Config, error) {
	dir := filepath.Join(storeDir, "profiles")
	if name == "" {
		path := filepath.Join(dir, profileSlug(DefaultProfileName)+".yaml")
		if _, err := os.Stat(path); err != nil {
			return config.Default(), nil
		}
		return config.LoadFile(path)
	}
	path := filepath.Join(dir, profileSlug(name)+".yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("profile %q not found (list_profiles shows what exists)", name)
	}
	return config.LoadFile(path)
}

// ListProfileNames returns the display names of saved profiles ("# Name" header
// comment, falling back to the slug), default profile first.
func ListProfileNames(storeDir string) []string {
	dir := filepath.Join(storeDir, "profiles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.ReplaceAll(strings.TrimSuffix(e.Name(), ".yaml"), "-", " ")
		if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			first, _, _ := strings.Cut(string(data), "\n")
			if strings.HasPrefix(first, "# ") {
				name = strings.TrimSpace(strings.TrimPrefix(first, "# "))
			}
		}
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		if names[i] == DefaultProfileName {
			return true
		}
		if names[j] == DefaultProfileName {
			return false
		}
		return names[i] < names[j]
	})
	return names
}

// BaseSource says where a resolved base config came from, so surfaces can
// show the truth of what will run (the CLI's provenance line, the desktop's
// setup preview, the MCP start_crawl response).
type BaseSource struct {
	Kind    string // "last" | "app" | "profile"
	CrawlID string // Kind "last": the crawl whose frozen setup was reused
	Started time.Time
}

// ResolveBase resolves a spec's base config — the layer under the dotted-path
// overrides — and reports its provenance. This is the single resolution path
// (#88): FreezeSpec freezes through it at enqueue, and the preview surfaces
// call it directly, so what a user is shown and what a job runs can never
// disagree.
//
//	ConfigSource ""     -> the named Profile, or the app settings when none
//	ConfigSource "last" -> the seed site's most recent spider crawl's frozen
//	                       config (FindLastSetup); app settings when the site
//	                       has never been crawled. Spider-only, and mutually
//	                       exclusive with Profile.
func ResolveBase(storeDir string, spec queue.JobSpec) (*config.Config, BaseSource, error) {
	switch spec.ConfigSource {
	case "":
		kind := "app"
		if spec.Profile != "" {
			kind = "profile"
		}
		cfg, err := LoadProfile(storeDir, spec.Profile)
		return cfg, BaseSource{Kind: kind}, err
	case "last":
		if spec.Profile != "" {
			return nil, BaseSource{}, fmt.Errorf("config_source \"last\" and a profile are mutually exclusive — the profile IS the setup source")
		}
		if spec.Mode == "list" {
			return nil, BaseSource{}, fmt.Errorf("config_source \"last\" applies to spider crawls only — a list audit has no single site whose setup could be reused")
		}
		ls, err := FindLastSetup(storeDir, spec.URL)
		if err != nil {
			return nil, BaseSource{}, err
		}
		if ls == nil {
			cfg, err := LoadProfile(storeDir, "")
			return cfg, BaseSource{Kind: "app"}, err
		}
		cfg, err := config.Load([]byte(ls.ConfigYAML))
		if err != nil {
			return nil, BaseSource{}, fmt.Errorf("crawl %s's frozen config: %w — pick another setup source", ls.CrawlID, err)
		}
		return cfg, BaseSource{Kind: "last", CrawlID: ls.CrawlID, Started: ls.Started}, nil
	default:
		return nil, BaseSource{}, fmt.Errorf("unknown config_source %q (\"last\" or empty)", spec.ConfigSource)
	}
}

// BuildConfig assembles the effective config for a job spec:
// base (ResolveBase) -> list-mode adjustments -> dotted-path overrides.
// Overrides win over everything, including the list-mode depth adjustment.
func BuildConfig(storeDir string, spec queue.JobSpec) (*config.Config, error) {
	cfg, _, err := ResolveBase(storeDir, spec)
	if err != nil {
		return nil, err
	}
	if spec.Mode == "list" {
		cfg.Mode = "list"
		cfg.Limits.MaxDepth = cfg.ListMode.CrawlDepth
		if !cfg.ListMode.RespectRobots {
			cfg.Robots.Mode = "ignore"
		}
	}
	for key, value := range spec.Config {
		// JSON is valid YAML, so encode each value as JSON and reuse the config
		// schema's typed Set (same path as the CLI's --set).
		enc, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("config[%s]: %w", key, err)
		}
		if err := cfg.Set(key + "=" + string(enc)); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// FreezeSpec validates a job spec for enqueue and freezes its effective config
// into ConfigYAML: the base (ResolveBase — last-crawl setup, profile, or app
// settings) + dotted-path overrides are resolved NOW, so a queued job runs
// with exactly the config the user saw when they enqueued it — editing a
// profile (or finishing another crawl of the same site) afterwards changes
// future enqueues, never jobs already sitting in the queue. A resume job
// passes through untouched (it runs its crawl's own frozen config), and an
// already-frozen spec (CLI file/flags, desktop re-run) is validated as-is.
// The profile name and config source stay on the spec as provenance; the
// executor ignores them once ConfigYAML is set. The live sitemap fetch and
// final seed resolution stay deferred to run time (ResolveSeeds), so a
// list-mode job still reads a fresh sitemap when it runs.
func FreezeSpec(storeDir string, spec queue.JobSpec) (queue.JobSpec, error) {
	if spec.ResumeID != "" {
		return spec, nil
	}
	if err := validateSeedShape(spec); err != nil {
		return queue.JobSpec{}, err
	}
	if spec.ConfigYAML != "" {
		cfg, err := config.Load([]byte(spec.ConfigYAML))
		if err != nil {
			return queue.JobSpec{}, err
		}
		if err := cfg.Validate(); err != nil {
			return queue.JobSpec{}, err
		}
		return spec, nil
	}
	cfg, err := BuildConfig(storeDir, spec)
	if err != nil {
		return queue.JobSpec{}, err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return queue.JobSpec{}, err
	}
	spec.ConfigYAML = string(data)
	spec.Config = nil // folded into ConfigYAML
	return spec, nil
}

// validateSeedShape checks the spec's seed surface (mode + url/urls) without
// touching config or the network.
func validateSeedShape(spec queue.JobSpec) error {
	switch spec.Mode {
	case "", "spider":
		if !strings.HasPrefix(spec.URL, "http://") && !strings.HasPrefix(spec.URL, "https://") {
			return fmt.Errorf("url must be a full URL including http:// or https:// (got %q)", spec.URL)
		}
	case "list":
		if spec.SitemapURL == "" && len(spec.URLs) == 0 {
			return fmt.Errorf("list mode needs urls or sitemap_url")
		}
	default:
		return fmt.Errorf("mode must be spider or list (got %q)", spec.Mode)
	}
	return nil
}

// ResolveSeeds turns a job spec into seed URLs and the store mode, fetching the
// sitemap when list mode asks for one (done at run time so the seed set is fresh).
func ResolveSeeds(ctx context.Context, cfg *config.Config, spec queue.JobSpec) (seeds []string, mode string, err error) {
	switch spec.Mode {
	case "", "spider":
		if !strings.HasPrefix(spec.URL, "http://") && !strings.HasPrefix(spec.URL, "https://") {
			return nil, "", fmt.Errorf("url must be a full URL including http:// or https:// (got %q)", spec.URL)
		}
		return []string{spec.URL}, "spider", nil
	case "list":
		if spec.SitemapURL != "" {
			seeds, err = crawler.FetchSitemapURLs(ctx, cfg, spec.SitemapURL)
			if err != nil {
				return nil, "", fmt.Errorf("sitemap fetch: %w", err)
			}
		} else {
			seeds = spec.URLs
		}
		if len(seeds) == 0 {
			return nil, "", fmt.Errorf("list mode needs urls or sitemap_url")
		}
		return seeds, "list", nil
	default:
		return nil, "", fmt.Errorf("mode must be spider or list (got %q)", spec.Mode)
	}
}
