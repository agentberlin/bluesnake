package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

// StartRequest is the start_crawl tool's payload: seeds plus the same config
// surface the CLI exposes (profile + dotted-path overrides).
type StartRequest struct {
	Mode       string         `json:"mode,omitempty"` // spider (default) | list
	URL        string         `json:"url,omitempty"`
	URLs       []string       `json:"urls,omitempty"`
	SitemapURL string         `json:"sitemap_url,omitempty"`
	Project    string         `json:"project,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Config     map[string]any `json:"config,omitempty"` // dotted path -> value
}

// Progress is a live-crawl snapshot for the crawl_status tool.
type Progress struct {
	CrawlID    string  `json:"crawl_id"`
	Seed       string  `json:"seed"`
	State      string  `json:"state"` // running
	Total      int     `json:"total"` // URLs processed so far (fetched + robots-blocked + errored)
	Discovered int     `json:"discovered"`
	Queue      int     `json:"queue"`
	S2xx       int     `json:"status_2xx"`
	S3xx       int     `json:"status_3xx"`
	S4xx       int     `json:"status_4xx"`
	S5xx       int     `json:"status_5xx"`
	Blocked    int     `json:"blocked_by_robots"`
	NoResponse int     `json:"no_response"`
	Indexable  int     `json:"indexable"`
	RatePerSec float64 `json:"urls_per_sec"`
	ElapsedSec int     `json:"elapsed_sec"`
}

// Backend is the crawl-control surface the tools run against. The CLI uses
// Runner; the desktop app adapts its Wails session manager so MCP-started
// crawls appear live in the UI. At most one crawl runs per backend.
type Backend interface {
	StartCrawl(ctx context.Context, req StartRequest) (string, error)
	ResumeCrawl(id string) (string, error)
	PauseCrawl() error
	StopCrawl() error
	Progress() *Progress // nil when no crawl is live
	StoreDir() string
}

// ---------------------------------------------------------------------------
// shared crawl-request semantics (used by Runner and the desktop adapter)

// profileSlug mirrors the desktop app's profile file naming
// (<store-dir>/profiles/<slug>.yaml).
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

const defaultProfileName = "Default audit"

// loadProfile resolves a named profile to a config. An empty name means the
// default profile when it exists, otherwise built-in defaults; a named
// profile that doesn't exist is an error (the caller asked for something
// specific).
func loadProfile(storeDir, name string) (*config.Config, error) {
	dir := filepath.Join(storeDir, "profiles")
	if name == "" {
		path := filepath.Join(dir, profileSlug(defaultProfileName)+".yaml")
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

// ListProfileNames returns the display names of saved profiles ("# Name"
// header comment, falling back to the slug), default profile first.
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
		if names[i] == defaultProfileName {
			return true
		}
		if names[j] == defaultProfileName {
			return false
		}
		return names[i] < names[j]
	})
	return names
}

// BuildConfig assembles the effective config for a start_crawl request:
// profile (or defaults) -> list-mode adjustments -> dotted-path overrides.
// Overrides win over everything, including the list-mode depth adjustment.
func BuildConfig(storeDir string, req StartRequest) (*config.Config, error) {
	cfg, err := loadProfile(storeDir, req.Profile)
	if err != nil {
		return nil, err
	}
	if req.Mode == "list" {
		cfg.Mode = "list"
		cfg.Limits.MaxDepth = cfg.ListMode.CrawlDepth
		if !cfg.ListMode.RespectRobots {
			cfg.Robots.Mode = "ignore"
		}
	}
	for key, value := range req.Config {
		// JSON is valid YAML, so encode each value as JSON and reuse the
		// config schema's typed Set (same path as the CLI's --set).
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

// ResolveSeeds turns a StartRequest into seed URLs and the store mode,
// fetching the sitemap when list mode asks for one.
func ResolveSeeds(ctx context.Context, cfg *config.Config, req StartRequest) (seeds []string, mode string, err error) {
	switch req.Mode {
	case "", "spider":
		if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
			return nil, "", fmt.Errorf("url must be a full URL including http:// or https:// (got %q)", req.URL)
		}
		return []string{req.URL}, "spider", nil
	case "list":
		if req.SitemapURL != "" {
			seeds, err = crawler.FetchSitemapURLs(ctx, cfg, req.SitemapURL)
			if err != nil {
				return nil, "", fmt.Errorf("sitemap fetch: %w", err)
			}
		} else {
			seeds = req.URLs
		}
		if len(seeds) == 0 {
			return nil, "", fmt.Errorf("list mode needs urls or sitemap_url")
		}
		return seeds, "list", nil
	default:
		return nil, "", fmt.Errorf("mode must be spider or list (got %q)", req.Mode)
	}
}

// DefaultProject derives a project name from the first seed's hostname.
func DefaultProject(project string, seeds []string) string {
	if project != "" {
		return project
	}
	if u, err := url.Parse(seeds[0]); err == nil {
		return strings.TrimPrefix(u.Hostname(), "www.")
	}
	return ""
}
