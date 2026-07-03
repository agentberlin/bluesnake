package sitecheck

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/render"
)

// jsContentMinRatio: raw text below half the rendered text = the page's
// content depends on JavaScript. Code constant, not config (DESIGN.md §8).
const jsContentMinRatio = 2

// RenderDiffReport is the raw-vs-rendered audit for one URL: what a
// non-rendering consumer (text crawlers, most AI bots) sees versus what a
// browser builds. Findings reuse the JavaScript-tab catalogue IDs that
// rendered crawls emit per page, so the two paths never disagree.
type RenderDiffReport struct {
	URL         string `json:"url"`
	FetchStatus int    `json:"fetch_status"`
	FetchError  string `json:"fetch_error,omitempty"`
	Rendered    bool   `json:"rendered"`
	RenderError string `json:"render_error,omitempty"`

	RawTitle           string   `json:"raw_title,omitempty"`
	RenderedTitle      string   `json:"rendered_title,omitempty"`
	TitleChanged       bool     `json:"title_changed,omitempty"`
	DescriptionChanged bool     `json:"description_changed,omitempty"`
	H1Changed          bool     `json:"h1_changed,omitempty"`
	RawCanonical       string   `json:"raw_canonical,omitempty"`
	RenderedCanonical  string   `json:"rendered_canonical,omitempty"`
	CanonicalChanged   bool     `json:"canonical_changed,omitempty"`
	NoindexOnlyRaw     bool     `json:"noindex_only_raw,omitempty"`
	RawWordCount       int      `json:"raw_word_count"`
	RenderedWordCount  int      `json:"rendered_word_count"`
	RenderedOnlyLinks  int      `json:"rendered_only_links,omitempty"`
	RenderedOnlyLinkEx []string `json:"rendered_only_link_examples,omitempty"`
	ConsoleErrors      []string `json:"console_errors,omitempty"`
}

// RenderDiff fetches a URL raw, renders it in headless Chrome, and diffs the
// two parses. Chrome-gated: without Chrome the error carries the same hint
// rendered crawling gives.
func (c *Checker) RenderDiff(ctx context.Context, pageURL string) (*RenderDiffReport, error) {
	pageURL = normalizePageURL(pageURL)
	r, err := render.New(c.cfg)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	rep := &RenderDiffReport{URL: pageURL}
	res := c.client.Fetch(ctx, pageURL)
	rep.FetchStatus, rep.FetchError = res.StatusCode, res.FetchError
	if res.FetchError != "" || res.StatusCode < 200 || res.StatusCode >= 300 {
		return rep, nil
	}
	rawFacts := parse.Parse(pageURL, res.Body, res.Headers, c.cfg)

	rendered, err := r.Render(ctx, pageURL)
	if err != nil {
		rep.RenderError = err.Error()
		return rep, nil
	}
	rep.Rendered = true
	renderedFacts := parse.Parse(pageURL, []byte(rendered.HTML), res.Headers, c.cfg)
	diffFacts(rep, rawFacts, renderedFacts, rendered.ConsoleErrors)
	return rep, nil
}

// diffFacts fills the report from the two parses — pure, so the diff
// semantics are testable without Chrome. Comparisons mirror the crawler's
// rendered-mode JSDiff (first-instance elements; rendered-only hyperlinks).
func diffFacts(rep *RenderDiffReport, raw, rendered *parse.Facts, consoleErrors []string) {
	first := func(v []string) string {
		if len(v) > 0 {
			return v[0]
		}
		return ""
	}
	rep.RawTitle, rep.RenderedTitle = first(raw.Titles), first(rendered.Titles)
	rep.TitleChanged = rep.RawTitle != rep.RenderedTitle
	rep.DescriptionChanged = first(raw.Descriptions) != first(rendered.Descriptions)
	rep.H1Changed = first(raw.H1s) != first(rendered.H1s)
	rep.RawCanonical, rep.RenderedCanonical = first(raw.CanonicalHTML), first(rendered.CanonicalHTML)
	rep.CanonicalChanged = rep.RawCanonical != rep.RenderedCanonical
	rep.NoindexOnlyRaw = hasNoindex(raw.MetaRobots) && !hasNoindex(rendered.MetaRobots)
	rep.RawWordCount, rep.RenderedWordCount = raw.WordCount, rendered.WordCount
	rep.ConsoleErrors = consoleErrors

	seen := map[string]bool{}
	for _, l := range raw.Links {
		if l.Type == parse.Hyperlink {
			seen[l.URL] = true
		}
	}
	for _, l := range rendered.Links {
		if l.Type == parse.Hyperlink && l.URL != "" && !seen[l.URL] {
			seen[l.URL] = true
			rep.RenderedOnlyLinks++
			addExample(&rep.RenderedOnlyLinkEx, l.URL)
		}
	}
}

func hasNoindex(directives []string) bool {
	for _, d := range directives {
		lower := strings.ToLower(d)
		if strings.Contains(lower, "noindex") || strings.Contains(lower, "none") {
			return true
		}
	}
	return false
}

// Findings derives the issue occurrences: the JavaScript-tab IDs rendered
// crawls already emit, plus js_dependent_content — the headline "this page's
// content is invisible without JavaScript" signal.
func (r *RenderDiffReport) Findings() []Finding {
	if !r.Rendered {
		return nil
	}
	var out []Finding
	add := func(id, detail string) {
		out = append(out, Finding{IssueID: id, URL: r.URL, Detail: detail})
	}
	if r.RenderedWordCount > 0 && r.RawWordCount*jsContentMinRatio < r.RenderedWordCount {
		add("js_dependent_content", fmt.Sprintf("%d words in the raw HTML vs %d rendered", r.RawWordCount, r.RenderedWordCount))
	}
	if r.RenderedOnlyLinks > 0 {
		add("js_contains_links", fmt.Sprintf("%d hyperlinks only in the rendered DOM (e.g. %s)",
			r.RenderedOnlyLinks, strings.Join(r.RenderedOnlyLinkEx, ", ")))
	}
	if r.TitleChanged {
		add("js_title_updated", fmt.Sprintf("%q -> %q", r.RawTitle, r.RenderedTitle))
	}
	if r.DescriptionChanged {
		add("js_description_updated", "")
	}
	if r.H1Changed {
		add("js_h1_updated", "")
	}
	if r.CanonicalChanged {
		add("js_canonical_mismatch", fmt.Sprintf("%q -> %q", r.RawCanonical, r.RenderedCanonical))
	}
	if r.NoindexOnlyRaw {
		add("js_noindex_only_raw", "")
	}
	if len(r.ConsoleErrors) > 0 {
		add("js_console_errors", strings.Join(r.ConsoleErrors, "; "))
	}
	return out
}
