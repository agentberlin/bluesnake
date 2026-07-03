package sitecheck

import (
	"encoding/json"
	"fmt"
)

// HealthEntry is the per-kind rollup behind an overview "site health" strip:
// one entry per stored check report, carrying the report's positive story (a
// one-line summary of what IS there) alongside the derived findings. Rolling
// up by kind keeps the strip fixed-size — kinds grow one per check family,
// while issue IDs keep multiplying.
type HealthEntry struct {
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings,omitempty"`
}

// Health rolls a stored site-check report into its strip entry. ok=false for
// unknown kinds or undecodable reports — a report written by a different
// version degrades to no entry, never an error (the DecodeFindings contract).
func Health(kind string, report []byte) (HealthEntry, bool) {
	e := HealthEntry{Kind: kind, Findings: DecodeFindings(kind, report)}
	switch kind {
	case KindRobots:
		var r RobotsReport
		if json.Unmarshal(report, &r) != nil {
			return e, false
		}
		e.Label, e.Summary = "robots.txt", robotsSummary(&r)
	case KindSitemap:
		var r SitemapReport
		if json.Unmarshal(report, &r) != nil {
			return e, false
		}
		e.Label, e.Summary = "Sitemaps", sitemapSummary(&r)
	case KindAIBots:
		var r AIBotsReport
		if json.Unmarshal(report, &r) != nil {
			return e, false
		}
		e.Label, e.Summary = "AI bots", aiBotsSummary(&r)
	case KindRenderDiff:
		var r RenderDiffReport
		if json.Unmarshal(report, &r) != nil {
			return e, false
		}
		e.Label, e.Summary = "JS render", renderSummary(&r)
	default:
		return e, false
	}
	return e, true
}

// LlmsHealth is the llms.txt strip entry. The llms.txt audit predates the
// site-check pass and lives in its own tables, so the caller hands the
// rebuilt report in rather than raw storage bytes.
func LlmsHealth(rep *LlmsReport) HealthEntry {
	main, full := false, false
	for _, f := range rep.Files {
		if f.Found {
			switch f.Kind {
			case "llms_txt":
				main = true
			case "llms_full_txt":
				full = true
			}
		}
	}
	summary := "not present"
	switch {
	case main && full:
		summary = "llms.txt and llms-full.txt present"
	case main:
		summary = "llms.txt present"
	case full:
		summary = "only llms-full.txt present"
	}
	return HealthEntry{Kind: "llms_txt", Label: "llms.txt", Summary: summary, Findings: rep.Findings()}
}

func robotsSummary(r *RobotsReport) string {
	switch {
	case r.FetchError != "":
		return "unreachable"
	case !r.Found && r.Status >= 500:
		return fmt.Sprintf("server error (HTTP %d)", r.Status)
	case !r.Found:
		return fmt.Sprintf("missing (HTTP %d)", r.Status)
	}
	s := fmt.Sprintf("HTTP %d · %s · %d rules", r.Status, humanBytes(r.SizeBytes), r.Rules)
	if n := len(r.Sitemaps); n > 0 {
		s += fmt.Sprintf(" · %d sitemap directive%s", n, plural(n))
	}
	return s
}

func sitemapSummary(r *SitemapReport) string {
	if r.Missing {
		return "none found"
	}
	files, entries := 0, 0
	for _, f := range r.Files {
		if f.FetchError == "" && f.Status >= 200 && f.Status < 300 && f.XMLError == "" {
			files++
			entries += f.Entries
		}
	}
	if files == 0 {
		return fmt.Sprintf("%d declared, none usable", len(r.Files))
	}
	return fmt.Sprintf("%d sitemap%s · %d URLs", files, plural(files), entries)
}

func aiBotsSummary(r *AIBotsReport) string {
	allowed := 0
	for _, b := range r.Bots {
		if b.RobotsAllowed && !b.BlockedLive {
			allowed++
		}
	}
	s := fmt.Sprintf("%d/%d crawlers allowed", allowed, len(r.Bots))
	if r.Live {
		s += " · live-probed"
	}
	return s
}

func renderSummary(r *RenderDiffReport) string {
	switch {
	case r.FetchError != "" || !r.Rendered:
		return "not rendered"
	case r.RenderedWordCount > 0 && r.RawWordCount*jsContentMinRatio < r.RenderedWordCount:
		return fmt.Sprintf("content needs JavaScript (%d → %d words)", r.RawWordCount, r.RenderedWordCount)
	}
	return "content visible without JavaScript"
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
