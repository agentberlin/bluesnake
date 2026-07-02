package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
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
// Runner; the desktop app adapts its crawl queue so MCP-started crawls appear
// live in the UI. Up to speed.max_concurrent_crawls crawls run per backend
// (from the default profile, read at construction; default 1); a start beyond
// that capacity is rejected rather than silently queued. Control is addressed
// by crawl id — the tool layer resolves an omitted id against Running() and
// errors when it is ambiguous.
type Backend interface {
	StartCrawl(ctx context.Context, req StartRequest) (string, error)
	ResumeCrawl(id string) (string, error)
	PauseCrawl(crawlID string) error
	StopCrawl(crawlID string) error
	Running() []Progress // live snapshots of every in-flight crawl, oldest first; empty when idle
	StoreDir() string
}

// StartViaQueue enforces the MCP start contract over a crawl-queue dispatcher,
// identically for the standalone Runner and the desktop backend: reject the
// start when every crawl slot is taken (the historical "a crawl is already
// running" contract, generalised to W slots — never silently queue an agent's
// crawl behind other work), else enqueue and block until the crawl exists.
// mu serializes the capacity check against racing starts on the same backend.
func StartViaQueue(ctx context.Context, d *queue.Dispatcher, maxCrawls int, mu *sync.Mutex, spec queue.JobSpec, label string) (string, error) {
	mu.Lock()
	if cur := d.CurrentAll(); len(cur) >= maxCrawls {
		mu.Unlock()
		return "", capacityError(cur, maxCrawls)
	}
	j, err := d.Enqueue(spec, "manual", "", label)
	mu.Unlock()
	if err != nil {
		return "", err
	}
	return d.AwaitCrawl(ctx, j.ID)
}

// capacityError names every running crawl so the model can address one with
// pause_crawl/stop_crawl (or wait) instead of guessing.
func capacityError(cur []queue.Job, maxCrawls int) error {
	ids := make([]string, len(cur))
	for i, j := range cur {
		if j.CrawlID != "" {
			ids[i] = j.CrawlID
		} else {
			ids[i] = "(starting)"
		}
	}
	if maxCrawls == 1 {
		return fmt.Errorf("a crawl is already running (crawl %s) — pause_crawl or stop_crawl first", ids[0])
	}
	return fmt.Errorf("all %d crawl slots are busy (running: %s) — pause_crawl or stop_crawl one first, or raise speed.max_concurrent_crawls in the default profile (applies after a restart)",
		maxCrawls, strings.Join(ids, ", "))
}

// ProgressFromSnapshot maps the executor's live reading to the tool payload.
func ProgressFromSnapshot(s runner.Snapshot) Progress {
	return Progress{
		CrawlID: s.CrawlID, Seed: s.Seed, State: "running",
		Total: s.Total, Discovered: s.Discovered, Queue: s.Queue,
		S2xx: s.S2xx, S3xx: s.S3xx, S4xx: s.S4xx, S5xx: s.S5xx,
		Blocked: s.Blocked, NoResponse: s.NoResponse, Indexable: s.Indexable,
		RatePerSec: s.RatePerSec, ElapsedSec: s.ElapsedSec,
	}
}
