// Package issues evaluates the audit catalogue over crawl results: per-page
// rules plus cross-page rules (duplicates), each mapped to a stable issue ID
// with Screaming Frog's severity (issue/warning/opportunity) and priority
// (high/medium/low). See docs/research/02 for the source inventory; checks
// that need post-crawl graph analysis live in the analyze package.
package issues

import (
	"fmt"
	"strings"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/parse"
	"github.com/hhsecond/acrawler/internal/urlutil"
)

type Severity string

const (
	Issue       Severity = "issue"
	Warning     Severity = "warning"
	Opportunity Severity = "opportunity"
)

type Priority string

const (
	High   Priority = "high"
	Medium Priority = "medium"
	Low    Priority = "low"
)

// Def is one catalogue entry.
type Def struct {
	ID       string
	Tab      string
	Name     string
	Severity Severity
	Priority Priority
}

// Occurrence is one issue on one URL.
type Occurrence struct {
	URL     string
	IssueID string
	Detail  string
}

// catalogue is the single source of truth (severity/priority from the
// official issues library, docs/research/02 §3).
var catalogue = []Def{
	// Response codes
	{"internal_no_response", "response_codes", "Internal No Response", Issue, High},
	{"internal_client_error", "response_codes", "Internal Client Error (4XX)", Issue, High},
	{"internal_server_error", "response_codes", "Internal Server Error (5XX)", Issue, High},
	{"internal_blocked_robots", "response_codes", "Internal Blocked by Robots.txt", Warning, High},
	{"internal_redirect", "response_codes", "Internal Redirection (3XX)", Warning, Low},
	{"internal_redirect_meta_refresh", "response_codes", "Internal Redirection (Meta Refresh)", Warning, Low},
	{"external_no_response", "response_codes", "External No Response", Warning, Low},
	{"external_client_error", "response_codes", "External Client Error (4XX)", Warning, Low},
	{"external_server_error", "response_codes", "External Server Error (5XX)", Warning, Low},
	// Security
	{"security_http_url", "security", "HTTP URLs", Issue, High},
	{"security_mixed_content", "security", "Mixed Content", Issue, High},
	{"security_form_url_insecure", "security", "Form URL Insecure", Issue, High},
	{"security_form_on_http", "security", "Form On HTTP URL", Issue, High},
	{"security_unsafe_cross_origin", "security", "Unsafe Cross-Origin Links", Warning, Low},
	{"security_protocol_relative", "security", "Protocol-Relative Resource Links", Warning, Low},
	{"security_missing_hsts", "security", "Missing HSTS Header", Warning, Low},
	{"security_missing_csp", "security", "Missing Content-Security-Policy Header", Warning, Low},
	{"security_missing_content_type_options", "security", "Missing X-Content-Type-Options Header", Warning, Low},
	{"security_missing_x_frame_options", "security", "Missing X-Frame-Options Header", Warning, Low},
	{"security_missing_referrer_policy", "security", "Missing Secure Referrer-Policy Header", Warning, Low},
	// URL
	{"url_uppercase", "url", "Uppercase", Warning, Low},
	{"url_underscores", "url", "Underscores", Opportunity, Low},
	{"url_non_ascii", "url", "Non ASCII Characters", Warning, Low},
	{"url_parameters", "url", "Parameters", Warning, Low},
	{"url_multiple_slashes", "url", "Multiple Slashes", Issue, Low},
	{"url_contains_space", "url", "Contains A Space", Issue, Low},
	{"url_repetitive_path", "url", "Repetitive Path", Warning, Low},
	{"url_ga_params", "url", "GA Tracking Parameters", Warning, Low},
	{"url_over_length", "url", "Over X Characters", Opportunity, Low},
	// Page titles
	{"title_missing", "page_titles", "Missing", Issue, High},
	{"title_duplicate", "page_titles", "Duplicate", Opportunity, Medium},
	{"title_over_chars", "page_titles", "Over X Characters", Opportunity, Medium},
	{"title_below_chars", "page_titles", "Below X Characters", Opportunity, Medium},
	{"title_same_as_h1", "page_titles", "Same as H1", Opportunity, Low},
	{"title_multiple", "page_titles", "Multiple", Issue, High},
	{"title_outside_head", "page_titles", "Outside <head>", Issue, High},
	// Meta description
	{"description_missing", "meta_description", "Missing", Opportunity, Low},
	{"description_duplicate", "meta_description", "Duplicate", Opportunity, Low},
	{"description_over_chars", "meta_description", "Over X Characters", Opportunity, Low},
	{"description_below_chars", "meta_description", "Below X Characters", Opportunity, Low},
	{"description_multiple", "meta_description", "Multiple", Issue, Medium},
	{"description_outside_head", "meta_description", "Outside <head>", Issue, Medium},
	// Meta keywords
	{"keywords_multiple", "meta_keywords", "Multiple", Warning, Low},
	// H1 / H2
	{"h1_missing", "h1", "Missing", Issue, Medium},
	{"h1_duplicate", "h1", "Duplicate", Opportunity, Low},
	{"h1_over_chars", "h1", "Over X Characters", Opportunity, Low},
	{"h1_multiple", "h1", "Multiple", Warning, Medium},
	{"h1_non_sequential", "h1", "Non-sequential", Warning, Low},
	{"h2_missing", "h2", "Missing", Warning, Low},
	{"h2_over_chars", "h2", "Over X Characters", Opportunity, Low},
	{"h2_multiple", "h2", "Multiple", Warning, Low},
	// Content
	{"content_low_word_count", "content", "Low Content Pages", Opportunity, Medium},
	{"content_exact_duplicate", "content", "Exact Duplicates", Issue, High},
	{"content_lorem_ipsum", "content", "Lorem Ipsum Placeholder", Warning, High},
	{"content_soft_404", "content", "Soft 404 Pages", Warning, High},
	{"content_readability_difficult", "content", "Readability Difficult", Opportunity, Low},
	{"content_readability_very_difficult", "content", "Readability Very Difficult", Opportunity, Low},
	// Images
	{"image_missing_alt", "images", "Missing Alt Text", Issue, Low},
	{"image_alt_over_chars", "images", "Alt Text Over X Characters", Opportunity, Low},
	{"image_over_size", "images", "Over X KB", Opportunity, Medium},
	// Canonicals
	{"canonical_missing", "canonicals", "Missing", Warning, Medium},
	{"canonical_canonicalised", "canonicals", "Canonicalised", Warning, High},
	{"canonical_multiple", "canonicals", "Multiple", Warning, Low},
	{"canonical_multiple_conflicting", "canonicals", "Multiple Conflicting", Issue, High},
	{"canonical_relative", "canonicals", "Canonical Is Relative", Warning, Low},
	{"canonical_outside_head", "canonicals", "Outside <head>", Issue, High},
	{"canonical_non_indexable_target", "canonicals", "Non-Indexable Canonical", Issue, High},
	// Directives
	{"directive_noindex", "directives", "Noindex", Warning, High},
	{"directive_nofollow", "directives", "Nofollow", Warning, High},
	{"directive_none", "directives", "None", Warning, High},
	// Links
	{"links_high_internal_outlinks", "links", "Pages With High Internal Outlinks", Warning, Low},
	{"links_high_external_outlinks", "links", "Pages With High External Outlinks", Warning, Low},
	{"links_no_internal_outlinks", "links", "Pages Without Internal Outlinks", Warning, High},
	{"links_internal_nofollow_outlinks", "links", "Internal Nofollow Outlinks", Warning, Low},
	{"links_non_descriptive_anchor", "links", "Non-Descriptive Anchor Text In Internal Outlinks", Opportunity, Low},
	{"links_no_anchor_text", "links", "Internal Outlinks With No Anchor Text", Opportunity, Low},
	{"links_to_localhost", "links", "Outlinks To Localhost", Issue, High},
	{"links_high_crawl_depth", "links", "Pages With High Crawl Depth", Opportunity, Medium},
	// Analysis phase (computed by the analyze package)
	{"redirect_chain", "response_codes", "Internal Redirect Chain", Warning, Medium},
	{"redirect_loop", "response_codes", "Internal Redirect Loop", Issue, High},
	{"canonical_chain", "canonicals", "Canonical Chain", Warning, Medium},
	{"content_near_duplicate", "content", "Near Duplicates", Warning, Medium},
	{"hreflang_non_200", "hreflang", "Non-200 Hreflang URLs", Issue, High},
	{"hreflang_missing_return", "hreflang", "Missing Return Links", Issue, High},
	{"hreflang_invalid_code", "hreflang", "Incorrect Language & Region Codes", Issue, High},
	{"hreflang_missing_self_reference", "hreflang", "Missing Self Reference", Warning, Low},
	{"hreflang_missing_x_default", "hreflang", "Missing X-Default", Warning, Low},
	{"pagination_non_200", "pagination", "Non-200 Pagination URLs", Issue, High},
	{"pagination_sequence_error", "pagination", "Sequence Error", Issue, Low},
	{"sitemap_orphan", "sitemaps", "Orphan URLs", Issue, Medium},
	{"sitemap_non_indexable", "sitemaps", "Non-Indexable URLs In Sitemap", Issue, Medium},
	{"sitemap_in_multiple", "sitemaps", "URLs In Multiple Sitemaps", Warning, Low},
	{"sitemap_not_in_sitemap", "sitemaps", "URLs Not In Sitemap", Issue, Medium},
	// Structured data
	{"structured_parse_error", "structured_data", "Parse Errors", Issue, High},
	{"structured_validation_error", "structured_data", "Validation Errors", Issue, High},
	{"structured_validation_warning", "structured_data", "Validation Warnings", Opportunity, Low},
	// JavaScript rendering
	{"js_noindex_only_raw", "javascript", "Noindex Only in Original HTML", Issue, High},
	{"js_canonical_mismatch", "javascript", "Canonical Mismatch", Issue, High},
	{"js_title_updated", "javascript", "Page Title Updated by JavaScript", Warning, Medium},
	{"js_h1_updated", "javascript", "H1 Updated by JavaScript", Warning, Medium},
	{"js_contains_links", "javascript", "Contains JavaScript Links", Warning, Medium},
	{"js_console_errors", "javascript", "Pages With JavaScript Errors", Warning, Low},
	// Validation (HTML parseability for search bots)
	{"validation_missing_head", "validation", "Missing <head> Tag", Issue, High},
	{"validation_multiple_head", "validation", "Multiple <head> Tags", Issue, High},
	{"validation_missing_body", "validation", "Missing <body> Tag", Issue, High},
	{"validation_multiple_body", "validation", "Multiple <body> Tags", Issue, High},
	{"validation_body_before_html", "validation", "<body> Element Preceding <html>", Warning, High},
	{"validation_head_not_first", "validation", "<head> Not First In <html> Element", Warning, High},
	{"validation_invalid_head_elements", "validation", "Invalid HTML Elements In <head>", Warning, High},
	{"validation_document_over_2mb", "validation", "HTML Document Over 2MB", Issue, High},
	// AMP (static checks)
	{"amp_missing_canonical", "amp", "Missing Canonical", Issue, High},
	{"amp_missing_viewport", "amp", "Missing/Invalid Meta Viewport Tag", Issue, High},
	{"amp_missing_charset", "amp", "Missing/Invalid Meta Charset Tag", Issue, High},
	{"amp_missing_script", "amp", "Missing/Invalid AMP Script", Issue, High},
	{"amp_missing_return_link", "amp", "Missing Non-AMP Return Link", Issue, High},
	{"amp_indexable", "amp", "Indexable AMP Page", Warning, High},
}

var defByID = func() map[string]Def {
	m := make(map[string]Def, len(catalogue))
	for _, d := range catalogue {
		m[d.ID] = d
	}
	return m
}()

// Catalogue returns all issue definitions.
func Catalogue() []Def { return catalogue }

// Lookup returns a definition by ID.
func Lookup(id string) (Def, bool) {
	d, ok := defByID[id]
	return d, ok
}

type evaluator struct {
	cfg   *config.Config
	pages map[string]*crawler.PageRecord
	occs  []Occurrence
}

// Evaluate runs the whole catalogue over a crawl's pages.
func Evaluate(pages map[string]*crawler.PageRecord, cfg *config.Config) []Occurrence {
	e := &evaluator{cfg: cfg, pages: pages}
	for _, rec := range pages {
		e.responseCodes(rec)
		if rec.State != crawler.StateCrawled || rec.Scope != "internal" {
			continue
		}
		e.urlChecks(rec)
		if rec.Facts == nil {
			continue
		}
		e.security(rec)
		if !e.skipForIndexability(rec) {
			e.elements(rec)
			e.content(rec)
		}
		e.canonicals(rec)
		e.structuredData(rec)
		e.javascript(rec)
		e.validation(rec)
		e.amp(rec)
		e.directives(rec)
		e.links(rec)
		e.images(rec)
	}
	e.duplicates()
	return e.occs
}

func (e *evaluator) add(url, id string, detail ...string) {
	d := ""
	if len(detail) > 0 {
		d = detail[0]
	}
	e.occs = append(e.occs, Occurrence{URL: url, IssueID: id, Detail: d})
}

// skipForIndexability honours advanced.ignore_non_indexable_for_issues.
func (e *evaluator) skipForIndexability(rec *crawler.PageRecord) bool {
	return e.cfg.Advanced.IgnoreNonIndexableForIssues && !rec.Indexable
}

func (e *evaluator) responseCodes(rec *crawler.PageRecord) {
	internal := rec.Scope == "internal"
	prefix := map[bool]string{true: "internal", false: "external"}[internal]
	switch {
	case rec.State == crawler.StateBlockedRobots:
		if internal {
			e.add(rec.URL, "internal_blocked_robots", fmt.Sprintf("matched robots.txt line %d", rec.MatchedRobotsLine))
		}
	case rec.State == crawler.StateError:
		e.add(rec.URL, prefix+"_no_response", rec.FetchError)
	case rec.StatusCode >= 500:
		e.add(rec.URL, prefix+"_server_error", rec.Status)
	case rec.StatusCode >= 400:
		e.add(rec.URL, prefix+"_client_error", rec.Status)
	case rec.StatusCode >= 300:
		if internal {
			e.add(rec.URL, "internal_redirect", rec.RedirectURL)
		}
	case rec.RedirectType == "meta_refresh":
		if internal {
			e.add(rec.URL, "internal_redirect_meta_refresh", rec.RedirectURL)
		}
	}
}

func (e *evaluator) urlChecks(rec *crawler.PageRecord) {
	u := rec.URL
	rest := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	path := ""
	if i := strings.Index(rest, "/"); i >= 0 {
		path = rest[i:]
	}
	if path == "" || path == "/" {
		return
	}
	if strings.ToLower(path) != path {
		e.add(u, "url_uppercase")
	}
	if strings.Contains(path, "_") {
		e.add(u, "url_underscores")
	}
	for _, r := range path {
		if r > 127 {
			e.add(u, "url_non_ascii")
			break
		}
	}
	if strings.Contains(path, "?") {
		e.add(u, "url_parameters")
	}
	noQuery, _, _ := strings.Cut(path, "?")
	if strings.Contains(noQuery, "//") {
		e.add(u, "url_multiple_slashes")
	}
	if strings.Contains(path, "%20") || strings.Contains(path, " ") {
		e.add(u, "url_contains_space")
	}
	if hasRepeatedSegment(noQuery) {
		e.add(u, "url_repetitive_path")
	}
	for _, p := range []string{"utm_", "_ga=", "_gl="} {
		if strings.Contains(path, p) {
			e.add(u, "url_ga_params")
			break
		}
	}
	if len(u) > e.cfg.Thresholds.URLMaxChars {
		e.add(u, "url_over_length", fmt.Sprintf("%d chars", len(u)))
	}
}

func hasRepeatedSegment(path string) bool {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := 1; i < len(segs); i++ {
		if segs[i] != "" && segs[i] == segs[i-1] {
			return true
		}
	}
	return false
}

func (e *evaluator) security(rec *crawler.PageRecord) {
	f := rec.Facts
	isHTTPS := strings.HasPrefix(rec.URL, "https://")
	if !isHTTPS && isHTMLPage(rec) {
		e.add(rec.URL, "security_http_url")
	}
	for _, l := range f.Links {
		switch l.Type {
		case parse.Image, parse.CSS, parse.JS, parse.Media:
			if isHTTPS && strings.HasPrefix(l.URL, "http://") {
				e.add(rec.URL, "security_mixed_content", l.URL)
			}
			if l.PathType == "protocol_relative" {
				e.add(rec.URL, "security_protocol_relative", l.URL)
			}
		case parse.Hyperlink:
			if l.Target == "_blank" && e.isExternal(rec.URL, l.URL) &&
				!strings.Contains(l.Rel, "noopener") && !strings.Contains(l.Rel, "noreferrer") {
				e.add(rec.URL, "security_unsafe_cross_origin", l.URL)
			}
		}
	}
	for _, form := range f.Forms {
		if strings.HasPrefix(form.Action, "http://") {
			e.add(rec.URL, "security_form_url_insecure", form.Action)
		}
		if !isHTTPS {
			e.add(rec.URL, "security_form_on_http")
		}
	}
	if isHTTPS && isHTMLPage(rec) && rec.StatusCode == 200 {
		header := func(name string) string { return rec.Headers[name] }
		if header("Strict-Transport-Security") == "" {
			e.add(rec.URL, "security_missing_hsts")
		}
		if header("Content-Security-Policy") == "" {
			e.add(rec.URL, "security_missing_csp")
		}
		if !strings.EqualFold(header("X-Content-Type-Options"), "nosniff") {
			e.add(rec.URL, "security_missing_content_type_options")
		}
		if v := strings.ToUpper(header("X-Frame-Options")); v != "DENY" && v != "SAMEORIGIN" {
			e.add(rec.URL, "security_missing_x_frame_options")
		}
		if !secureReferrerPolicy(header("Referrer-Policy")) {
			e.add(rec.URL, "security_missing_referrer_policy")
		}
	}
}

func secureReferrerPolicy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no-referrer-when-downgrade", "strict-origin-when-cross-origin", "no-referrer", "strict-origin":
		return true
	}
	return false
}

// isExternal classifies a link target: by its crawl record when crawled,
// else by comparing hosts with the linking page.
func (e *evaluator) isExternal(fromURL, linkURL string) bool {
	if rec, ok := e.pages[linkURL]; ok {
		return rec.Scope == "external"
	}
	return urlutil.Host(linkURL) != urlutil.Host(fromURL)
}

func (e *evaluator) elements(rec *crawler.PageRecord) {
	f, t := rec.Facts, &e.cfg.Thresholds
	u := rec.URL

	switch {
	case len(f.Titles) == 0 || strings.TrimSpace(f.Titles[0]) == "":
		e.add(u, "title_missing")
	default:
		title := f.Titles[0]
		if len(f.Titles) > 1 {
			e.add(u, "title_multiple", fmt.Sprintf("%d titles", len(f.Titles)))
		}
		if n := len([]rune(title)); n > t.Title.MaxChars {
			e.add(u, "title_over_chars", title)
		} else if n < t.Title.MinChars {
			e.add(u, "title_below_chars", title)
		}
		if len(f.H1s) > 0 && strings.EqualFold(title, f.H1s[0]) {
			e.add(u, "title_same_as_h1")
		}
	}
	if f.TitlesOutsideHead > 0 {
		e.add(u, "title_outside_head")
	}

	switch {
	case len(f.Descriptions) == 0 || strings.TrimSpace(f.Descriptions[0]) == "":
		e.add(u, "description_missing")
	default:
		if len(f.Descriptions) > 1 {
			e.add(u, "description_multiple")
		}
		if n := len([]rune(f.Descriptions[0])); n > t.Description.MaxChars {
			e.add(u, "description_over_chars")
		} else if n < t.Description.MinChars {
			e.add(u, "description_below_chars")
		}
	}
	if f.DescriptionsOutsideHead > 0 {
		e.add(u, "description_outside_head")
	}
	if len(f.Keywords) > 1 {
		e.add(u, "keywords_multiple")
	}

	switch {
	case len(f.H1s) == 0 || strings.TrimSpace(f.H1s[0]) == "":
		e.add(u, "h1_missing")
	default:
		if len(f.H1s) > 1 {
			e.add(u, "h1_multiple")
		}
		if len([]rune(f.H1s[0])) > t.H1MaxChars {
			e.add(u, "h1_over_chars")
		}
		if len(f.HeadingLevels) > 0 && f.HeadingLevels[0] != 1 {
			e.add(u, "h1_non_sequential", fmt.Sprintf("first heading is h%d", f.HeadingLevels[0]))
		}
	}
	switch {
	case len(f.H2s) == 0:
		e.add(u, "h2_missing")
	default:
		if len(f.H2s) > 1 {
			e.add(u, "h2_multiple")
		}
		if len([]rune(f.H2s[0])) > t.H2MaxChars {
			e.add(u, "h2_over_chars")
		}
	}
}

func (e *evaluator) content(rec *crawler.PageRecord) {
	f, t := rec.Facts, &e.cfg.Thresholds
	if f.WordCount > 0 && f.WordCount < t.LowContentWords {
		e.add(rec.URL, "content_low_word_count", fmt.Sprintf("%d words", f.WordCount))
	}
	lower := strings.ToLower(f.ContentText)
	if strings.Contains(lower, "lorem ipsum") {
		e.add(rec.URL, "content_lorem_ipsum")
	}
	if rec.StatusCode == 200 {
		for _, pat := range t.Soft404Patterns {
			if strings.Contains(lower, strings.ToLower(pat)) {
				e.add(rec.URL, "content_soft_404", pat)
				break
			}
		}
	}
	if f.WordCount > 0 {
		if f.Flesch < 30 {
			e.add(rec.URL, "content_readability_very_difficult")
		} else if f.Flesch < 50 {
			e.add(rec.URL, "content_readability_difficult")
		}
	}
}

func (e *evaluator) canonicals(rec *crawler.PageRecord) {
	f := rec.Facts
	u := rec.URL
	all := append(append([]string{}, f.CanonicalHTML...), f.CanonicalHTTP...)
	if len(all) == 0 {
		if rec.Indexable && isHTMLPage(rec) {
			e.add(u, "canonical_missing")
		}
		return
	}
	if rec.IndexabilityStatus == "Canonicalised" {
		e.add(u, "canonical_canonicalised", all[0])
	}
	if len(all) > 1 {
		e.add(u, "canonical_multiple")
		distinct := map[string]bool{}
		for _, c := range all {
			distinct[c] = true
		}
		if len(distinct) > 1 {
			e.add(u, "canonical_multiple_conflicting")
		}
	}
	if f.CanonicalOutsideHead > 0 {
		e.add(u, "canonical_outside_head")
	}
	for _, l := range f.Links {
		if l.Type == parse.Canonical && l.PathType != "absolute" {
			e.add(u, "canonical_relative", l.Raw)
			break
		}
	}
	if target, ok := e.pages[all[0]]; ok && all[0] != u && !target.Indexable {
		e.add(u, "canonical_non_indexable_target", all[0])
	}
}

func (e *evaluator) structuredData(rec *crawler.PageRecord) {
	sd := rec.StructuredData
	if sd == nil {
		return
	}
	for _, p := range sd.ParseErrors {
		e.add(rec.URL, "structured_parse_error", p)
	}
	for _, p := range sd.Errors {
		e.add(rec.URL, "structured_validation_error", p)
	}
	for _, p := range sd.Warnings {
		e.add(rec.URL, "structured_validation_warning", p)
	}
}

func (e *evaluator) javascript(rec *crawler.PageRecord) {
	d := rec.JSDiff
	if d == nil {
		return
	}
	if d.NoindexOnlyRaw {
		e.add(rec.URL, "js_noindex_only_raw")
	}
	if d.CanonicalChanged {
		e.add(rec.URL, "js_canonical_mismatch", d.RenderedCanonical)
	}
	if d.TitleChanged {
		e.add(rec.URL, "js_title_updated", d.RenderedTitle)
	}
	if d.H1Changed {
		e.add(rec.URL, "js_h1_updated")
	}
	if d.JSLinks > 0 {
		e.add(rec.URL, "js_contains_links", fmt.Sprintf("%d rendered-only links", d.JSLinks))
	}
	if len(d.ConsoleErrors) > 0 {
		e.add(rec.URL, "js_console_errors", strings.Join(d.ConsoleErrors, "; "))
	}
}

func (e *evaluator) validation(rec *crawler.PageRecord) {
	if !isHTMLPage(rec) {
		return
	}
	hv := rec.Facts.Head
	u := rec.URL
	if hv.MissingHead {
		e.add(u, "validation_missing_head")
	}
	if hv.MultipleHead {
		e.add(u, "validation_multiple_head")
	}
	if hv.MissingBody {
		e.add(u, "validation_missing_body")
	}
	if hv.MultipleBody {
		e.add(u, "validation_multiple_body")
	}
	if hv.BodyBeforeHTML {
		e.add(u, "validation_body_before_html")
	}
	if hv.HeadNotFirst {
		e.add(u, "validation_head_not_first")
	}
	if len(hv.InvalidElementsInHead) > 0 {
		e.add(u, "validation_invalid_head_elements", strings.Join(hv.InvalidElementsInHead, ", "))
	}
	if rec.Size > 2*1024*1024 {
		e.add(u, "validation_document_over_2mb", fmt.Sprintf("%d bytes", rec.Size))
	}
}

// amp runs the static AMP checks on AMP pages and the AMP/non-AMP linking
// reciprocity (the official AMP validator's full rule set is out of scope;
// these are the structural requirements).
func (e *evaluator) amp(rec *crawler.PageRecord) {
	f := rec.Facts
	u := rec.URL
	if f.IsAMP {
		if len(f.CanonicalHTML)+len(f.CanonicalHTTP) == 0 {
			e.add(u, "amp_missing_canonical")
		}
		if !f.HasViewport {
			e.add(u, "amp_missing_viewport")
		}
		if !f.HasCharset {
			e.add(u, "amp_missing_charset")
		}
		if !f.HasAMPScript {
			e.add(u, "amp_missing_script")
		}
		if rec.Indexable && len(f.CanonicalHTML) > 0 && f.CanonicalHTML[0] != u {
			e.add(u, "amp_indexable")
		}
	}
	// non-AMP page linking an AMP variant: the AMP page must canonical back
	for _, ampURL := range f.AMPLinks {
		target, ok := e.pages[ampURL]
		if !ok || target.Facts == nil {
			continue
		}
		returns := false
		for _, c := range target.Facts.CanonicalHTML {
			if c == u {
				returns = true
				break
			}
		}
		if !returns {
			e.add(u, "amp_missing_return_link", ampURL)
		}
	}
}

func (e *evaluator) directives(rec *crawler.PageRecord) {
	for _, v := range append(append([]string{}, rec.Facts.MetaRobots...), rec.Facts.XRobotsTag...) {
		for directive := range strings.SplitSeq(v, ",") {
			switch strings.ToLower(strings.TrimSpace(directive)) {
			case "noindex":
				e.add(rec.URL, "directive_noindex")
			case "nofollow":
				e.add(rec.URL, "directive_nofollow")
			case "none":
				e.add(rec.URL, "directive_none")
			}
		}
	}
}

func (e *evaluator) links(rec *crawler.PageRecord) {
	f, t := rec.Facts, &e.cfg.Thresholds
	u := rec.URL
	internalOut, externalOut := 0, 0
	for _, l := range f.Links {
		if l.Type != parse.Hyperlink {
			continue
		}
		host := urlutil.Host(l.URL)
		if host == "localhost" || host == "127.0.0.1" {
			e.add(u, "links_to_localhost", l.URL)
		}
		external := e.isExternal(u, l.URL)
		if external {
			externalOut++
			continue
		}
		internalOut++
		if l.Nofollow {
			e.add(u, "links_internal_nofollow_outlinks", l.URL)
		}
		anchor := strings.ToLower(strings.TrimSpace(l.Anchor))
		if anchor == "" && l.Alt == "" {
			e.add(u, "links_no_anchor_text", l.URL)
		} else {
			for _, bad := range t.NonDescriptiveAnchors {
				if anchor == bad {
					e.add(u, "links_non_descriptive_anchor", l.Anchor)
					break
				}
			}
		}
	}
	if isHTMLPage(rec) {
		if internalOut == 0 {
			e.add(u, "links_no_internal_outlinks")
		}
		if internalOut > t.HighInternalOutlinks {
			e.add(u, "links_high_internal_outlinks", fmt.Sprintf("%d", internalOut))
		}
		if externalOut > t.HighExternalOutlinks {
			e.add(u, "links_high_external_outlinks", fmt.Sprintf("%d", externalOut))
		}
		if rec.Depth > t.HighCrawlDepth {
			e.add(u, "links_high_crawl_depth", fmt.Sprintf("depth %d", rec.Depth))
		}
	}
}

// images flags issues on image *references* (per page) and oversized image
// files (per crawled image URL).
func (e *evaluator) images(rec *crawler.PageRecord) {
	t := &e.cfg.Thresholds
	for _, l := range rec.Facts.Links {
		if l.Type != parse.Image {
			continue
		}
		if strings.TrimSpace(l.Alt) == "" {
			e.add(rec.URL, "image_missing_alt", l.URL)
		} else if len([]rune(l.Alt)) > t.ImageAltMaxChars {
			e.add(rec.URL, "image_alt_over_chars", l.URL)
		}
		if img, ok := e.pages[l.URL]; ok && img.Size > t.ImageMaxKB*1024 {
			e.add(l.URL, "image_over_size", fmt.Sprintf("%d KB", img.Size/1024))
		}
	}
}

// duplicates runs the cross-page checks: exact duplicate content and
// duplicate titles/descriptions/h1 across indexable internal HTML pages.
func (e *evaluator) duplicates() {
	type group struct{ urls []string }
	byHash := map[string]*group{}
	byTitle := map[string]*group{}
	byDesc := map[string]*group{}
	byH1 := map[string]*group{}

	collect := func(m map[string]*group, key, url string) {
		if key == "" {
			return
		}
		if m[key] == nil {
			m[key] = &group{}
		}
		m[key].urls = append(m[key].urls, url)
	}

	for url, rec := range e.pages {
		if rec.State != crawler.StateCrawled || rec.Scope != "internal" ||
			rec.Facts == nil || !isHTMLPage(rec) || e.skipForIndexability(rec) {
			continue
		}
		f := rec.Facts
		collect(byHash, f.Hash, url)
		if len(f.Titles) > 0 {
			collect(byTitle, f.Titles[0], url)
		}
		if len(f.Descriptions) > 0 {
			collect(byDesc, f.Descriptions[0], url)
		}
		if len(f.H1s) > 0 {
			collect(byH1, f.H1s[0], url)
		}
	}

	flag := func(m map[string]*group, issueID string) {
		for key, g := range m {
			if len(g.urls) < 2 {
				continue
			}
			for _, url := range g.urls {
				e.add(url, issueID, key)
			}
		}
	}
	flag(byHash, "content_exact_duplicate")
	flag(byTitle, "title_duplicate")
	flag(byDesc, "description_duplicate")
	flag(byH1, "h1_duplicate")
}

func isHTMLPage(rec *crawler.PageRecord) bool {
	return strings.Contains(rec.ContentType, "text/html") ||
		strings.Contains(rec.ContentType, "application/xhtml")
}
