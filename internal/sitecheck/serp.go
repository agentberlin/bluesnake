package sitecheck

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/serpwidth"
)

// SerpOptions selects what to preview. Title/Description are measured as
// given; URL fetches a live page and previews its actual title and meta
// description (explicit Title/Description override the fetched values, the
// live-editing loop). At least one of the three must be set.
type SerpOptions struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// SerpField is one measured snippet field: rendered pixel width at Google's
// desktop SERP font size against the configured thresholds.* limits, plus the
// truncation Google would apply when the text overflows max_px.
type SerpField struct {
	Text     string `json:"text"`
	Chars    int    `json:"chars"`
	Pixels   int    `json:"pixels"`
	MinChars int    `json:"min_chars,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
	MinPx    int    `json:"min_px,omitempty"`
	MaxPx    int    `json:"max_px,omitempty"`
	// Truncated is the longest prefix (plus ellipsis) that fits within MaxPx —
	// what the SERP would display. Set only when the text overflows.
	Truncated string `json:"truncated,omitempty"`
}

// SerpReport is the snippet preview: nothing is fetched unless a URL is given,
// nothing is ever persisted. Findings reuse the per-page pixel/char catalogue
// IDs, so the preview and a crawl measure identically.
type SerpReport struct {
	URL         string     `json:"url,omitempty"`
	Fetched     bool       `json:"fetched,omitempty"`
	FetchStatus int        `json:"fetch_status,omitempty"`
	FetchError  string     `json:"fetch_error,omitempty"`
	Title       *SerpField `json:"title,omitempty"`
	Description *SerpField `json:"description,omitempty"`
}

// Serp measures how a title/description pair renders on Google's desktop
// results page. Pure unless opts.URL is set.
func (c *Checker) Serp(ctx context.Context, opts SerpOptions) (*SerpReport, error) {
	if opts.URL == "" && opts.Title == "" && opts.Description == "" {
		return nil, errors.New("sitecheck: serp needs a title, a description, or a url")
	}
	rep := &SerpReport{}
	title, desc := opts.Title, opts.Description
	if opts.URL != "" {
		rep.URL = normalizePageURL(opts.URL)
		res := c.fetch(ctx, rep.URL)
		rep.FetchStatus, rep.FetchError = res.StatusCode, res.FetchError
		if res.FetchError != "" || res.StatusCode < 200 || res.StatusCode >= 300 {
			return rep, nil
		}
		rep.Fetched = true
		facts := parse.Parse(rep.URL, res.Body, res.Headers, c.cfg)
		if title == "" && len(facts.Titles) > 0 {
			title = facts.Titles[0]
		}
		if desc == "" && len(facts.Descriptions) > 0 {
			desc = facts.Descriptions[0]
		}
	}
	t := &c.cfg.Thresholds
	if title != "" || rep.Fetched {
		rep.Title = measureSerpField(title, serpwidth.TitleFontPx, t.Title)
	}
	if desc != "" || rep.Fetched {
		rep.Description = measureSerpField(desc, serpwidth.DescriptionFontPx, t.Description)
	}
	return rep, nil
}

func measureSerpField(text string, fontPx float64, th config.WidthThreshold) *SerpField {
	f := &SerpField{
		Text:     text,
		Chars:    len([]rune(text)),
		Pixels:   serpwidth.Width(text, fontPx),
		MinChars: th.MinChars, MaxChars: th.MaxChars,
		MinPx: th.MinPx, MaxPx: th.MaxPx,
	}
	if th.MaxPx > 0 && f.Pixels > th.MaxPx {
		f.Truncated = truncateToWidth(text, fontPx, th.MaxPx)
	}
	return f
}

// truncateToWidth returns the longest prefix of s that, with a trailing
// ellipsis, fits within maxPx at the given font size.
func truncateToWidth(s string, fontPx float64, maxPx int) string {
	runes := []rune(s)
	for i := len(runes); i > 0; i-- {
		if cut := strings.TrimRight(string(runes[:i]), " "); serpwidth.Width(cut+"…", fontPx) <= maxPx {
			return cut + "…"
		}
	}
	return "…"
}

// Findings mirrors the per-page title/description emission in internal/issues
// (chars and pixels each checked over-else-below; pixel checks gated on the
// thresholds being set). *_missing is only meaningful when a live page was
// fetched — a pure preview of one field says nothing about the other.
func (r *SerpReport) Findings() []Finding {
	var out []Finding
	add := func(id, detail string) {
		out = append(out, Finding{IssueID: id, URL: r.URL, Detail: detail})
	}
	field := func(f *SerpField, name, charDetail string) {
		if f == nil {
			return
		}
		if strings.TrimSpace(f.Text) == "" {
			if r.Fetched {
				add(name+"_missing", "")
			}
			return
		}
		if f.MaxChars > 0 && f.Chars > f.MaxChars {
			add(name+"_over_chars", charDetail)
		} else if f.MinChars > 0 && f.Chars < f.MinChars {
			add(name+"_below_chars", charDetail)
		}
		if f.MaxPx > 0 && f.Pixels > f.MaxPx {
			add(name+"_over_pixels", fmt.Sprintf("%dpx", f.Pixels))
		} else if f.MinPx > 0 && f.Pixels < f.MinPx {
			add(name+"_below_pixels", fmt.Sprintf("%dpx", f.Pixels))
		}
	}
	// Details mirror internal/issues: the title checks carry the title text,
	// the description char checks carry nothing.
	if r.Title != nil {
		field(r.Title, "title", r.Title.Text)
	}
	field(r.Description, "description", "")
	return out
}
