package crawler

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/robots"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// SiteCheckRecord is one stored site-level check report (DESIGN.md §5.10):
// the check kind, the artifact URL it audited (robots.txt URL, site root),
// and the full JSON report. Findings are re-derived from the report in the
// analyze phase via sitecheck.DecodeFindings, never stored here.
type SiteCheckRecord struct {
	Kind    string
	Subject string
	Report  []byte
}

// SiteCheckSink is the optional sink extension for site-level check reports.
type SiteCheckSink interface {
	SiteCheck(rec SiteCheckRecord) error
}

// siteChecksApply implements site_checks.enabled: "always"/"never" are
// unconditional; "auto" runs the checks only for a full-domain audit — a
// root-seeded spider crawl that is not scope-narrowed (no include patterns).
// A crawl of a path or a filtered slice audits a part of the site, where
// site-level findings would be noise; limits (max_urls etc.) deliberately do
// not gate: the checks are site-scoped and fixed-cost regardless of how many
// pages the crawl fetches.
func (c *Crawler) siteChecksApply(seed string) bool {
	if !c.cfg.SiteChecks.Robots && !c.cfg.SiteChecks.Sitemap &&
		!c.cfg.SiteChecks.AIBots.Check && !c.cfg.SiteChecks.RenderDiff {
		return false
	}
	switch c.cfg.SiteChecks.Enabled {
	case "never":
		return false
	case "always":
		return true
	}
	if c.cfg.Mode == "list" || len(c.cfg.Scope.Include) > 0 {
		return false
	}
	u, err := url.Parse(seed)
	if err != nil {
		return false
	}
	return (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == ""
}

// cappedFetcher routes the site-check pass's out-of-band fetches through the
// process-wide fetch cap (GL-08): each check fetch takes a global slot exactly
// like a worker page fetch, so M parallel crawls' site-check traffic counts
// against the same ceiling. A fetch cancelled while waiting for a slot
// degrades to an error result — a check must report, never panic.
type cappedFetcher struct{ c *Crawler }

func (f cappedFetcher) Fetch(ctx context.Context, rawURL string) *fetch.Result {
	if res := f.c.fetchCapped(ctx, rawURL); res != nil {
		return res
	}
	return &fetch.Result{URL: rawURL, FetchError: "crawl cancelled"}
}

func (f cappedFetcher) FetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result {
	if !f.c.limiter.AcquireFetch(ctx) {
		return &fetch.Result{URL: rawURL, FetchError: "crawl cancelled"}
	}
	defer f.c.limiter.ReleaseFetch()
	return f.c.client.FetchWith(ctx, rawURL, o)
}

// runSiteChecks executes the site-level checks for the seed's host. It runs
// in one background goroutine concurrent with the crawl — fetches within it
// are sequential, so the out-of-band burst against the origin is bounded to
// one in-flight request, and each fetch holds a global fetch slot (the
// cappedFetcher above; the robots.txt reuse below keeps that fetch's
// documented bypass). Each report is stored via the sink (INSERT OR REPLACE
// keyed on kind+subject, so a resume re-running the pass is idempotent) and
// retained on the Result. A check that errors is skipped — the pass can
// degrade but never fail the crawl.
func (c *Crawler) runSiteChecks(ctx context.Context, seed string) {
	u, err := url.Parse(seed)
	if err != nil || u.Host == "" {
		return
	}
	root := u.Scheme + "://" + u.Host
	chk := sitecheck.New(c.cfg, cappedFetcher{c}, sitecheck.WithRenderGate(
		func(ctx context.Context) (func(), bool) {
			if !c.limiter.AcquireRender(ctx) {
				return nil, false
			}
			return c.limiter.ReleaseRender, true
		}))

	record := func(kind, subject string, report any) {
		data, err := json.Marshal(report)
		if err != nil {
			return
		}
		rec := SiteCheckRecord{Kind: kind, Subject: subject, Report: data}
		c.siteCheckMu.Lock()
		c.siteChecks = append(c.siteChecks, rec)
		c.siteCheckFindings += len(sitecheck.DecodeFindings(kind, data))
		c.siteCheckMu.Unlock()
		if sink, ok := c.sink.(SiteCheckSink); ok && c.sink != nil {
			c.noteSinkErr(sink.SiteCheck(rec))
		}
	}

	var fromRobots []string
	var robotsFile *robots.File // the AI-bot verdicts read this, whatever its source
	robotsFound := false
	if custom := c.robots.customFor(u.Hostname()); custom != nil {
		// A custom robots.txt override (the tester workflow) means the live
		// file is never consulted — so there is nothing live to audit; sitemap
		// discovery and the AI-bot verdicts read the override, exactly as the
		// crawl itself does.
		robotsFile, robotsFound = custom, true
		fromRobots = resolveAgainst(root+"/robots.txt", custom.Sitemaps)
	} else {
		// One robots.txt fetch per crawl: reuse the robots manager's record
		// (or make the single fetch here when the policy never downloads —
		// ignore mode still gets its file audited).
		rf := c.robots.fetchRecordFor(ctx, root)
		rep := chk.EvaluateRobots(root, rf, sitecheck.RobotsOptions{})
		fromRobots = resolveAgainst(rep.FinalURL, rep.Sitemaps)
		robotsFound = rf.Found()
		if robotsFound {
			robotsFile = robots.Parse(rf.Body)
		} else {
			robotsFile = robots.Parse(nil)
		}
		if c.cfg.SiteChecks.Robots {
			record(sitecheck.KindRobots, rep.URL, rep)
		}
	}
	if c.cfg.SiteChecks.Sitemap {
		opts := sitecheck.SitemapOptions{
			FromRobots:    fromRobots,
			Declared:      c.cfg.Sitemaps.URLs,
			RobotsChecked: true, // the robots answer above is authoritative either way
		}
		if rep, err := chk.Sitemaps(ctx, root, opts); err == nil {
			record(sitecheck.KindSitemap, root, rep)
		}
	}
	if c.cfg.SiteChecks.AIBots.Check {
		opts := sitecheck.AIBotOptionsFromConfig(c.cfg.SiteChecks.AIBots)
		rep := chk.EvaluateAIBots(ctx, root, robotsFile, robotsFound, opts)
		record(sitecheck.KindAIBots, rep.URL, rep)
	}
	// Seed-page render diff: text-mode crawls only (a rendered crawl diffs
	// every page), opt-in, and degrades silently without Chrome — exactly
	// like rendered crawling, Chrome is a runtime dependency.
	if c.cfg.SiteChecks.RenderDiff && c.cfg.Rendering.Mode == "text" {
		if rep, err := chk.RenderDiff(ctx, seed); err == nil {
			record(sitecheck.KindRenderDiff, rep.URL, rep)
		}
	}
}

// resolveAgainst resolves possibly-relative Sitemap: directive values against
// the robots.txt URL they were read from.
func resolveAgainst(robotsURL string, sitemaps []string) []string {
	base, err := url.Parse(robotsURL)
	if err != nil {
		return nil
	}
	var out []string
	for _, sm := range sitemaps {
		if resolved, err := base.Parse(sm); err == nil {
			out = append(out, resolved.String())
		}
	}
	return out
}
