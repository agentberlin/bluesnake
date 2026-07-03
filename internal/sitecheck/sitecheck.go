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
	"github.com/agentberlin/bluesnake/internal/limiter"
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

// Fetcher is the checks' HTTP dependency — every surface hands in its plain
// fetch client (the crawl pass reuses the crawler's). Slot discipline is not
// the client's job: the Checker brackets its own fetches and renders with the
// limiter injected via WithLimiter, so every surface gets the process-wide
// caps from the one implementation.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) *fetch.Result
	FetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result
}

// Checker runs site-level checks over a shared fetcher.
type Checker struct {
	cfg    *config.Config
	client Fetcher
	lim    *limiter.Limiter // nil ⇒ no process-wide caps (one-shot CLI runs)
}

// Option configures a Checker.
type Option func(*Checker)

// WithLimiter runs every check fetch and the render diff's Chrome render under
// the process-wide concurrency caps: each fetch takes a global fetch slot
// exactly like a crawl worker's page fetch (GL-08), and the render takes a
// render slot (REN-01) — never both at once, the limiter's lock-order rule
// (the raw fetch before a render completes and releases its slot first). The
// crawl pass injects the crawler's limiter; dispatcher-owning surfaces (the
// desktop Tools hub, MCP run_tool) inject their runner.ProcessWiring limiter,
// so interactive tool runs share the same ceilings as the crawls they run
// beside. One-shot processes (CLI `bluesnake tools`) inject nothing: no crawl
// runs beside them, mirroring the executor's single-crawl P17 fallback.
func WithLimiter(l *limiter.Limiter) Option {
	return func(c *Checker) { c.lim = l }
}

func New(cfg *config.Config, client Fetcher, opts ...Option) *Checker {
	c := &Checker{cfg: cfg, client: client}
	for _, o := range opts {
		o(c)
	}
	return c
}

// fetch runs one check fetch under the process-wide fetch cap. A cancel while
// waiting degrades to an error result — a check must report, never fail.
func (c *Checker) fetch(ctx context.Context, rawURL string) *fetch.Result {
	if !c.lim.AcquireFetch(ctx) {
		return &fetch.Result{URL: rawURL, FetchError: "cancelled while waiting for a fetch slot"}
	}
	defer c.lim.ReleaseFetch()
	return c.client.Fetch(ctx, rawURL)
}

// fetchWith is fetch with a per-request override (the AI-bot probes' UA swap).
func (c *Checker) fetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result {
	if !c.lim.AcquireFetch(ctx) {
		return &fetch.Result{URL: rawURL, FetchError: "cancelled while waiting for a fetch slot"}
	}
	defer c.lim.ReleaseFetch()
	return c.client.FetchWith(ctx, rawURL, o)
}

// capped is the Checker's slot-taking view of its client — handed to shared
// fetch helpers (FetchRobots) so their fetches take slots like every direct
// check fetch does.
type capped struct{ c *Checker }

func (v capped) Fetch(ctx context.Context, rawURL string) *fetch.Result {
	return v.c.fetch(ctx, rawURL)
}

func (v capped) FetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result {
	return v.c.fetchWith(ctx, rawURL, o)
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
