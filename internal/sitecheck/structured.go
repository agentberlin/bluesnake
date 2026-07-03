package sitecheck

import (
	"context"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/structured"
)

// StructuredReport is the rich-results audit for one URL: schema.org entities
// found (JSON-LD, Microdata, RDFa) and the Google rich-result validation
// messages the crawler's structured-data engine produces per page. Tool-only:
// crawls with extraction.structured_data enabled already run this per page,
// so there is no crawl-integrated half and nothing is persisted.
type StructuredReport struct {
	URL         string   `json:"url"`
	FetchStatus int      `json:"fetch_status"`
	FetchError  string   `json:"fetch_error,omitempty"`
	Formats     []string `json:"formats,omitempty"` // jsonld | microdata | rdfa
	Types       []string `json:"types,omitempty"`
	ParseErrors []string `json:"parse_errors,omitempty"`
	Recovered   []string `json:"recovered,omitempty"`
	Errors      []string `json:"errors,omitempty"`   // missing required properties
	Warnings    []string `json:"warnings,omitempty"` // missing recommended properties
}

// Structured fetches one URL and validates its structured data. All three
// formats are extracted regardless of the extraction.structured_data config —
// those keys budget per-page crawl cost; running this tool is consent.
func (c *Checker) Structured(ctx context.Context, pageURL string) (*StructuredReport, error) {
	rep := &StructuredReport{URL: normalizePageURL(pageURL)}
	res := c.fetch(ctx, rep.URL)
	rep.FetchStatus, rep.FetchError = res.StatusCode, res.FetchError
	if res.FetchError != "" || res.StatusCode < 200 || res.StatusCode >= 300 {
		return rep, nil
	}
	cfg := *c.cfg
	cfg.Extraction.StructuredData = config.StructuredDataConfig{JSONLD: true, Microdata: true, RDFa: true}
	if data := structured.Extract(res.Body, &cfg); data != nil {
		rep.Formats, rep.Types = data.Formats, data.Types
		rep.ParseErrors, rep.Recovered = data.ParseErrors, data.Recovered
		rep.Errors, rep.Warnings = data.Errors, data.Warnings
	}
	return rep, nil
}

// Findings mirrors the per-page emission in internal/issues (structuredData):
// the tool and a structured-data-enabled crawl report the same IDs.
func (r *StructuredReport) Findings() []Finding {
	if r.FetchError != "" || r.FetchStatus < 200 || r.FetchStatus >= 300 {
		return nil
	}
	var out []Finding
	add := func(id, detail string) {
		out = append(out, Finding{IssueID: id, URL: r.URL, Detail: detail})
	}
	if len(r.Formats) == 0 {
		add("structured_missing", "")
	}
	for _, p := range r.ParseErrors {
		add("structured_parse_error", p)
	}
	for _, p := range r.Recovered {
		add("structured_invalid_recovered", p)
	}
	for _, p := range r.Errors {
		add("structured_validation_error", p)
	}
	for _, p := range r.Warnings {
		add("structured_validation_warning", p)
	}
	return out
}
