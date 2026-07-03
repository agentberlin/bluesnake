// Package sitecheck implements bluesnake's site-level checks — the engine
// behind both the standalone Tools testers (CLI `bluesnake tools`, MCP
// run_tool, the desktop Tools page) and the crawl-integrated site-check pass
// (DESIGN.md §5.10). Each check fetches what it needs over the shared
// fetch client, returns a JSON-serializable report, and derives findings
// (issue-catalogue occurrences) from that report — one derivation, so the
// interactive tools and the crawl can never disagree. Standalone runs are
// throwaway: nothing here persists anything; the crawl pass stores reports
// via its sink and the analyze phase re-derives findings with DecodeFindings.
//
// The package deliberately imports neither internal/issues nor
// internal/crawler (issues imports crawler; crawler imports sitecheck):
// findings carry plain string issue IDs, and internal/analyze — which imports
// both sides — maps them to catalogue occurrences.
package sitecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
)

// Check kinds — the site_checks storage rows and DecodeFindings dispatch.
const (
	KindRobots     = "robots"
	KindSitemap    = "sitemap"
	KindAIBots     = "ai_bots"
	KindRenderDiff = "render_diff"
)

// Check thresholds. Code constants, not config (DESIGN.md §8 precedent):
// each is a published protocol limit, not a preference.
const (
	maxRobotsBytes  = 500 << 10 // Google processes only the first 500 KiB of robots.txt
	maxSitemapURLs  = 50000     // sitemaps.org: at most 50,000 <url> entries per file
	maxSitemapBytes = 50 << 20  // sitemaps.org: at most 52,428,800 bytes uncompressed
)

// Operational bounds for the checks themselves.
const (
	robotsRedirectHops = 5        // Google REP: follow at least five hops, then treat as 404
	sitemapIndexDepth  = 2        // index recursion depth, matching the crawler's walker
	maxSitemapFiles    = 100      // per-report expansion bound; overflow is recorded, never silent
	maxExamples        = 5        // per-counter example URLs carried in a report
	gunzipCap          = 64 << 20 // decompression bound; reading past 50MB already proves the finding
)

// Finding is one derived issue occurrence: a catalogue ID (plain string — see
// the package comment for why issues isn't imported), the artifact URL it
// attaches to, and an optional detail.
type Finding struct {
	IssueID string `json:"issue_id"`
	URL     string `json:"url"`
	Detail  string `json:"detail,omitempty"`
}

// Reporter is implemented by every check report.
type Reporter interface {
	Findings() []Finding
}

// Fetcher is the checks' HTTP dependency. Standalone tools hand in the plain
// *fetch.Client; the crawl pass hands in the crawler's capped fetcher so
// every out-of-band check fetch takes a global fetch slot like a worker
// fetch (GL-08 — only robots.txt keeps its documented serialized bypass).
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) *fetch.Result
	FetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result
}

// Checker runs site-level checks over a shared fetcher.
type Checker struct {
	cfg        *config.Config
	client     Fetcher
	renderGate func(ctx context.Context) (release func(), ok bool)
}

// Option configures a Checker.
type Option func(*Checker)

// WithRenderGate brackets RenderDiff's headless-Chrome render with the
// caller's slot acquire/release — the crawl pass passes the process-wide
// render cap (REN-01). It gates only the render: the page fetch before it is
// capped separately through the Fetcher, because a fetch slot and a render
// slot must never be held together (the limiter's lock-order rule). ok=false
// means the wait was cancelled; the check degrades to a render error.
func WithRenderGate(gate func(ctx context.Context) (release func(), ok bool)) Option {
	return func(c *Checker) { c.renderGate = gate }
}

func New(cfg *config.Config, client Fetcher, opts ...Option) *Checker {
	c := &Checker{cfg: cfg, client: client}
	for _, o := range opts {
		o(c)
	}
	return c
}

// DecodeFindings re-derives the findings from a stored report of the given
// kind. Unknown kinds and undecodable reports yield nil: a report stored by a
// different version must degrade, never fail analysis.
func DecodeFindings(kind string, report []byte) []Finding {
	switch kind {
	case KindRobots:
		var r RobotsReport
		if json.Unmarshal(report, &r) == nil {
			return r.Findings()
		}
	case KindSitemap:
		var r SitemapReport
		if json.Unmarshal(report, &r) == nil {
			return r.Findings()
		}
	case KindAIBots:
		var r AIBotsReport
		if json.Unmarshal(report, &r) == nil {
			return r.Findings()
		}
	case KindRenderDiff:
		var r RenderDiffReport
		if json.Unmarshal(report, &r) == nil {
			return r.Findings()
		}
	}
	return nil
}

// siteRoot normalizes a site argument (bare host, root URL, or any page URL)
// to its scheme://host[:port] root. A missing scheme defaults to https.
func siteRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("sitecheck: empty target")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("sitecheck: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("sitecheck: no host in %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("sitecheck: unsupported scheme %q", u.Scheme)
	}
	return u.Scheme + "://" + u.Host, nil
}

// normalizePageURL defaults a bare host/path argument to https — page-level
// checks take the URL as given otherwise (no root folding).
func normalizePageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return raw
}

func addExample(list *[]string, v string) {
	if len(*list) < maxExamples {
		*list = append(*list, v)
	}
}
