package main

import (
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
)

// SetupPreview is the quick-knob slice of a resolved setup source, plus its
// provenance — what the New Crawl card shows for the selected source. The
// card initializes its knobs from this and only emits overrides for knobs the
// user then touches, so the preview IS the crawl unless deliberately changed.
type SetupPreview struct {
	Source     string  `json:"source"` // what actually resolved: "last" | "app" | "profile"
	CrawlID    string  `json:"crawlId,omitempty"`
	Started    int64   `json:"started,omitempty"` // unix seconds; source "last" only
	Depth      int     `json:"depth"`             // -1 = unlimited
	Threads    int     `json:"threads"`
	Rate       float64 `json:"rate"` // 0 = unlimited
	Rendering  string  `json:"rendering"`
	SiteChecks string  `json:"siteChecks"` // nearest card value: auto | all | off
}

// SetupPreview resolves a setup source exactly the way enqueue will
// (runner.ResolveBase — the single resolution path), so the card can never
// show a setup a crawl wouldn't run. source "last" needs the form's URL to
// derive the site; a URL that resolves to no prior crawl reports Source "app"
// so the form can say the fallback out loud.
func (a *App) SetupPreview(source, profile, rawURL string) (SetupPreview, error) {
	cfg, src, err := runner.ResolveBase(a.storeDir,
		queue.JobSpec{Mode: "spider", URL: rawURL, Profile: profile, ConfigSource: source})
	if err != nil {
		return SetupPreview{}, err
	}
	return SetupPreview{
		Source:     src.Kind,
		CrawlID:    src.CrawlID,
		Started:    unixOrZero(src),
		Depth:      cfg.Limits.MaxDepth,
		Threads:    cfg.Speed.MaxThreads,
		Rate:       cfg.Speed.MaxURLsPerSec,
		Rendering:  cfg.Rendering.Mode,
		SiteChecks: siteChecksKnob(cfg),
	}, nil
}

func unixOrZero(src runner.BaseSource) int64 {
	if src.Started.IsZero() {
		return 0
	}
	return src.Started.Unix()
}

// siteChecksKnob maps the site_checks config family to the card's three-way
// selector. Display-only and deliberately lossy (an "always" config shows as
// "All" whether or not its render diff is on): untouched knobs emit no
// override, so the underlying config always runs exactly as frozen.
func siteChecksKnob(cfg *config.Config) string {
	switch cfg.SiteChecks.Enabled {
	case "never":
		return "off"
	case "always":
		return "all"
	default:
		return "auto"
	}
}
