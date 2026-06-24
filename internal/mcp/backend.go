package mcp

import (
	"context"

	"github.com/agentberlin/bluesnake/internal/queue"
)

// StartRequest is the start_crawl tool's payload: seeds plus the same config
// surface the CLI exposes (profile + dotted-path overrides).
type StartRequest struct {
	Mode       string         `json:"mode,omitempty"` // spider (default) | list
	URL        string         `json:"url,omitempty"`
	URLs       []string       `json:"urls,omitempty"`
	SitemapURL string         `json:"sitemap_url,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Config     map[string]any `json:"config,omitempty"` // dotted path -> value
}

// Spec turns the tool payload into the neutral queue job spec the executor runs.
func (r StartRequest) Spec() queue.JobSpec {
	return queue.JobSpec{
		Mode: r.Mode, URL: r.URL, URLs: r.URLs,
		SitemapURL: r.SitemapURL, Profile: r.Profile, Config: r.Config,
	}
}

// Label is a short human label for the queue entry.
func (r StartRequest) Label() string {
	if r.URL != "" {
		return r.URL
	}
	if r.SitemapURL != "" {
		return r.SitemapURL
	}
	if len(r.URLs) > 0 {
		return r.URLs[0]
	}
	return "list crawl"
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
