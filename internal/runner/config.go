package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
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

// BuildConfig assembles the effective config for a job spec:
// profile (or defaults) -> list-mode adjustments -> dotted-path overrides.
// Overrides win over everything, including the list-mode depth adjustment.
func BuildConfig(storeDir string, spec queue.JobSpec) (*config.Config, error) {
	cfg, err := LoadProfile(storeDir, spec.Profile)
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

// ValidateSpec checks a job spec without touching the network, so a surface can
// reject a bad request at enqueue time (unknown profile, bad config override,
// malformed seed) and still surface the precise error — the live sitemap fetch
// and final seed resolution stay deferred to run time (ResolveSeeds). A resume
// job is validated when it runs (the crawl must exist on disk).
func ValidateSpec(storeDir string, spec queue.JobSpec) error {
	if spec.ResumeID != "" {
		return nil
	}
	if spec.ConfigYAML != "" {
		cfg, err := config.Load([]byte(spec.ConfigYAML))
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
	} else if _, err := BuildConfig(storeDir, spec); err != nil {
		return err
	}
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
