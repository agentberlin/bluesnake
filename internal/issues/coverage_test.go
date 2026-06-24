package issues

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
)

// TestStructuredDataChecks covers the full structuredData evaluator: the
// "missing" path (extraction on, nothing extracted), and each of the parse /
// recovered / validation-error / validation-warning paths fed from a populated
// StructuredData record. structured-data extraction is off in the default
// config, so the "missing" check must be enabled explicitly.
func TestStructuredDataChecks(t *testing.T) {
	cfg := config.Default()
	cfg.Extraction.StructuredData.JSONLD = true

	// a page with no structured data at all -> structured_missing
	missing := htmlPage("https://ex.com/missing", expansionFacts())

	// a page whose extraction produced formats but several diagnostics
	withData := htmlPage("https://ex.com/with-data", expansionFacts())
	withData.StructuredData = &structured.PageData{
		Formats:     []string{"jsonld"},
		Types:       []string{"Product"},
		ParseErrors: []string{"unexpected end of JSON input"},
		Recovered:   []string{"escaped a raw control character"},
		Errors:      []string{"Product missing required property: name"},
		Warnings:    []string{"Product missing recommended property: brand"},
	}

	// a page with valid extracted data raises none of the structured issues
	clean := htmlPage("https://ex.com/clean", expansionFacts())
	clean.StructuredData = &structured.PageData{
		Formats: []string{"jsonld"}, Types: []string{"Organization"},
	}

	occs := Evaluate(map[string]*crawler.PageRecord{
		missing.URL: missing, withData.URL: withData, clean.URL: clean,
	}, cfg)

	if !has(occs, "https://ex.com/missing", "structured_missing") {
		t.Error("missing structured_missing on a page with no structured data")
	}
	for id, want := range map[string]string{
		"structured_parse_error":       "unexpected end of JSON input",
		"structured_invalid_recovered": "escaped a raw control character",
		"structured_validation_error":  "Product missing required property: name",
	} {
		got := detailsOf(occs, "https://ex.com/with-data", id)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s details = %v, want [%q]", id, got, want)
		}
	}
	if !has(occs, "https://ex.com/with-data", "structured_validation_warning") {
		t.Error("missing structured_validation_warning")
	}
	// a page that produced data is not "missing"
	if has(occs, "https://ex.com/with-data", "structured_missing") {
		t.Error("a page with extracted formats flagged structured_missing")
	}
	// the clean page raises nothing
	for _, id := range []string{"structured_missing", "structured_parse_error",
		"structured_validation_error", "structured_validation_warning"} {
		if has(occs, "https://ex.com/clean", id) {
			t.Errorf("clean structured page flagged %s", id)
		}
	}
}

// TestStructuredMissingDisabled pins that with structured-data extraction off
// (the default), the structured_missing check never fires.
func TestStructuredMissingDisabled(t *testing.T) {
	page := htmlPage("https://ex.com/p", expansionFacts())
	occs := eval(page) // default config: extraction off
	if has(occs, "https://ex.com/p", "structured_missing") {
		t.Error("structured_missing fired although structured-data extraction is disabled")
	}
}

// TestJavaScriptDiffChecks covers every branch of the javascript evaluator that
// reads a populated JSDiff. (DescriptionChanged and StructuredJSOnly already
// have dedicated tests; this completes the rest.)
func TestJavaScriptDiffChecks(t *testing.T) {
	full := htmlPage("https://ex.com/full", expansionFacts())
	full.JSDiff = &crawler.JSDiff{
		NoindexOnlyRaw:    true,
		CanonicalChanged:  true,
		RenderedCanonical: "https://ex.com/rendered-canon",
		TitleChanged:      true,
		RenderedTitle:     "Rendered Title",
		H1Changed:         true,
		JSLinks:           7,
		ConsoleErrors:     []string{"TypeError: x is undefined", "ReferenceError: y"},
	}
	// a page with a nil JSDiff (no rendering) produces no JS issues
	noRender := htmlPage("https://ex.com/no-render", expansionFacts())

	occs := eval(full, noRender)
	for id, wantDetail := range map[string]string{
		"js_noindex_only_raw":   "",
		"js_canonical_mismatch": "https://ex.com/rendered-canon",
		"js_title_updated":      "Rendered Title",
		"js_h1_updated":         "",
		"js_contains_links":     "7 rendered-only links",
		"js_console_errors":     "TypeError: x is undefined; ReferenceError: y",
	} {
		if !has(occs, "https://ex.com/full", id) {
			t.Errorf("missing %s", id)
			continue
		}
		if wantDetail != "" {
			if got := occDetail(occs, "https://ex.com/full", id); got != wantDetail {
				t.Errorf("%s detail = %q, want %q", id, got, wantDetail)
			}
		}
	}
	// nil JSDiff: no JS issues at all
	for _, id := range []string{"js_noindex_only_raw", "js_title_updated", "js_h1_updated"} {
		if has(occs, "https://ex.com/no-render", id) {
			t.Errorf("page without a JSDiff flagged %s", id)
		}
	}
}

// TestSecureReferrerPolicy pins which Referrer-Policy values count as secure
// (and which trip the missing-referrer-policy header check). It exercises the
// header check directly via Evaluate over internal 2xx pages.
func TestSecureReferrerPolicy(t *testing.T) {
	secure := []string{
		"no-referrer", "strict-origin", "strict-origin-when-cross-origin",
		"no-referrer-when-downgrade", "  No-Referrer  ", // case + whitespace insensitive
	}
	insecure := []string{
		"unsafe-url", "origin", "same-origin", "origin-when-cross-origin", "",
	}
	var pages []*crawler.PageRecord
	mk := func(url, policy string) *crawler.PageRecord {
		p := htmlPage(url, expansionFacts())
		p.Headers = map[string]string{
			"Strict-Transport-Security": "max-age=31536000",
			"Content-Security-Policy":   "default-src 'self'",
			"X-Content-Type-Options":    "nosniff",
			"X-Frame-Options":           "DENY",
			"Referrer-Policy":           policy,
		}
		return p
	}
	for i, v := range secure {
		pages = append(pages, mk("https://ex.com/sec"+string(rune('a'+i)), v))
	}
	for i, v := range insecure {
		pages = append(pages, mk("https://ex.com/ins"+string(rune('a'+i)), v))
	}
	occs := eval(pages...)
	for i := range secure {
		url := "https://ex.com/sec" + string(rune('a'+i))
		if has(occs, url, "security_missing_referrer_policy") {
			t.Errorf("secure policy %q flagged as missing", secure[i])
		}
	}
	for i := range insecure {
		url := "https://ex.com/ins" + string(rune('a'+i))
		if !has(occs, url, "security_missing_referrer_policy") {
			t.Errorf("insecure policy %q not flagged", insecure[i])
		}
	}
}

// TestSecurityHeadersGating pins securityHeaders' guards: only https + 2xx
// internal responses are checked. A non-2xx https page and an http page are
// both exempt from the header checks.
func TestSecurityHeadersGating(t *testing.T) {
	notModified := htmlPage("https://ex.com/304", expansionFacts())
	notModified.StatusCode = 304
	httpPage := htmlPage("http://ex.com/insecure", expansionFacts())

	occs := eval(notModified, httpPage)
	for _, id := range []string{"security_missing_hsts", "security_missing_csp",
		"security_missing_referrer_policy"} {
		if has(occs, "https://ex.com/304", id) {
			t.Errorf("non-2xx https page flagged %s", id)
		}
		if has(occs, "http://ex.com/insecure", id) {
			t.Errorf("http page flagged %s (header checks are https-only)", id)
		}
	}
}

// TestSecurityHeadersAllowed pins the positive paths: a page with every secure
// header present raises none of the header checks (covers the EqualFold and
// SAMEORIGIN branches).
func TestSecurityHeadersAllowed(t *testing.T) {
	p := htmlPage("https://ex.com/secure", expansionFacts())
	p.Headers = map[string]string{
		"Strict-Transport-Security": "max-age=63072000",
		"Content-Security-Policy":   "default-src 'self'",
		"X-Content-Type-Options":    "NOSNIFF",    // case-insensitive
		"X-Frame-Options":           "sameorigin", // case-insensitive, both values
		"Referrer-Policy":           "no-referrer",
	}
	occs := eval(p)
	for _, id := range []string{
		"security_missing_hsts", "security_missing_csp",
		"security_missing_content_type_options",
		"security_missing_x_frame_options", "security_missing_referrer_policy",
	} {
		if has(occs, p.URL, id) {
			t.Errorf("fully-secured page flagged %s", id)
		}
	}
}

// TestValidationFullSet covers the validation evaluator branches not already
// exercised: missing body, body-before-html, head-not-first, the 2MB document
// check, and hreflang-outside-head. (charset/lang have their own tests.)
func TestValidationFullSet(t *testing.T) {
	f := expansionFacts()
	f.Head = parse.HeadValidity{
		MissingBody:    true,
		BodyBeforeHTML: true,
		HeadNotFirst:   true,
		MultipleHead:   true,
	}
	f.HreflangOutsideHead = 1
	page := htmlPage("https://ex.com/bad", f)
	page.Size = 3 * 1024 * 1024 // over 2MB

	occs := eval(page)
	for _, id := range []string{
		"validation_missing_body", "validation_body_before_html",
		"validation_head_not_first", "validation_multiple_head",
		"validation_document_over_2mb", "hreflang_outside_head",
	} {
		if !has(occs, "https://ex.com/bad", id) {
			t.Errorf("missing %s", id)
		}
	}
	if got := occDetail(occs, "https://ex.com/bad", "validation_document_over_2mb"); !strings.Contains(got, "bytes") {
		t.Errorf("validation_document_over_2mb detail = %q, want a byte count", got)
	}
}

// TestValidationNonHTMLSkipped pins validation's isHTMLPage guard: a non-HTML
// resource is not subject to the <head>/<body> checks.
func TestValidationNonHTMLSkipped(t *testing.T) {
	css := &crawler.PageRecord{
		URL: "https://ex.com/app.css", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/css", Indexable: true,
	}
	occs := eval(css)
	for _, id := range []string{"validation_missing_head", "charset_missing", "html_lang_missing"} {
		if has(occs, css.URL, id) {
			t.Errorf("non-HTML resource flagged %s", id)
		}
	}
}

// TestAMPReturnLinkPaths covers amp's non-AMP-page branch: a desktop page
// linking an AMP variant that DOES canonical back must not be flagged, and a
// missing AMP target (not crawled) is skipped.
func TestAMPReturnLinkPaths(t *testing.T) {
	// amp page that correctly canonicals back to the desktop page
	ampGoodFacts := expansionFacts()
	ampGoodFacts.IsAMP = true
	ampGoodFacts.HasAMPScript = true
	ampGoodFacts.CanonicalHTML = []string{"https://ex.com/desktop-ok"}
	ampGood := htmlPage("https://ex.com/amp-good", ampGoodFacts)

	desktopOKFacts := expansionFacts()
	desktopOKFacts.AMPLinks = []string{"https://ex.com/amp-good"}
	desktopOK := htmlPage("https://ex.com/desktop-ok", desktopOKFacts)

	// desktop page whose AMP target was never crawled (not in the map)
	desktopMissingFacts := expansionFacts()
	desktopMissingFacts.AMPLinks = []string{"https://ex.com/amp-uncrawled"}
	desktopMissing := htmlPage("https://ex.com/desktop-missing", desktopMissingFacts)

	occs := eval(ampGood, desktopOK, desktopMissing)
	if has(occs, "https://ex.com/desktop-ok", "amp_missing_return_link") {
		t.Error("desktop page whose AMP variant canonicals back was flagged")
	}
	if has(occs, "https://ex.com/desktop-missing", "amp_missing_return_link") {
		t.Error("uncrawled AMP target must be skipped, not flagged")
	}
}

// TestAMPIndexable covers the amp_indexable branch: an AMP page that canonicals
// to its desktop variant but is itself still indexable (rather than
// Canonicalised) is flagged — an AMP page pointing at a different canonical
// should not be indexed in its own right.
func TestAMPIndexable(t *testing.T) {
	f := expansionFacts()
	f.IsAMP = true
	f.HasViewport = true
	f.HasCharset = true
	f.HasAMPScript = true
	f.CanonicalHTML = []string{"https://ex.com/desktop"} // points at the desktop variant
	page := htmlPage("https://ex.com/amp", f)            // Indexable=true via htmlPage

	occs := eval(page)
	if !has(occs, "https://ex.com/amp", "amp_indexable") {
		t.Error("an indexable AMP page canonicalised to a different URL must be flagged amp_indexable")
	}
	// it is otherwise valid, so the structural AMP issues stay quiet
	for _, id := range []string{"amp_missing_canonical", "amp_missing_viewport",
		"amp_missing_charset", "amp_missing_script"} {
		if has(occs, "https://ex.com/amp", id) {
			t.Errorf("valid AMP page flagged %s", id)
		}
	}
}

// TestResponseCodeExternalAndMetaRefresh covers responseCodes branches not in
// the base test: external server error, external no-response, and an internal
// meta-refresh redirect.
func TestResponseCodeExternalAndMetaRefresh(t *testing.T) {
	occs := eval(
		&crawler.PageRecord{URL: "https://other.com/500", Scope: "external",
			State: crawler.StateCrawled, StatusCode: 503, Status: "503 Service Unavailable"},
		&crawler.PageRecord{URL: "https://other.com/dead", Scope: "external",
			State: crawler.StateError, FetchError: "connection refused"},
		&crawler.PageRecord{URL: "https://ex.com/refresh", Scope: "internal",
			State: crawler.StateCrawled, StatusCode: 200, RedirectType: "meta_refresh",
			RedirectURL: "https://ex.com/target"},
	)
	if !has(occs, "https://other.com/500", "external_server_error") {
		t.Error("missing external_server_error")
	}
	if !has(occs, "https://other.com/dead", "external_no_response") {
		t.Error("missing external_no_response")
	}
	if got := occDetail(occs, "https://ex.com/refresh", "internal_redirect_meta_refresh"); got != "https://ex.com/target" {
		t.Errorf("internal_redirect_meta_refresh detail = %q, want the target", got)
	}
}

// TestURLChecksRemaining covers urlChecks branches not in the base test: the
// non-ASCII path, an explicit %20 space, and the over-length threshold; plus
// the root-path early return.
func TestURLChecksRemaining(t *testing.T) {
	mk := func(url string) *crawler.PageRecord {
		return htmlPage(url, expansionFacts())
	}
	cfg := config.Default()
	cfg.Thresholds.URLMaxChars = 60
	pages := []*crawler.PageRecord{
		mk("https://ex.com/café/page"),                  // non-ASCII
		mk("https://ex.com/with%20space"),               // encoded space
		mk("https://ex.com/has space"),                  // literal space
		mk("https://ex.com/"),                           // root path: nothing flagged
		mk("https://ex.com/" + strings.Repeat("a", 80)), // over-length
	}
	m := map[string]*crawler.PageRecord{}
	for _, p := range pages {
		m[p.URL] = p
	}
	occs := Evaluate(m, cfg)

	if !has(occs, "https://ex.com/café/page", "url_non_ascii") {
		t.Error("missing url_non_ascii")
	}
	if !has(occs, "https://ex.com/with%20space", "url_contains_space") {
		t.Error("missing url_contains_space for %20")
	}
	if !has(occs, "https://ex.com/has space", "url_contains_space") {
		t.Error("missing url_contains_space for a literal space")
	}
	if !has(occs, "https://ex.com/"+strings.Repeat("a", 80), "url_over_length") {
		t.Error("missing url_over_length")
	}
	// the root path "/" must trigger no URL checks
	for _, o := range occs {
		if o.URL == "https://ex.com/" && strings.HasPrefix(o.IssueID, "url_") {
			t.Errorf("root path flagged %s", o.IssueID)
		}
	}
}

// TestLinksHighOutlinks covers the high-internal-outlinks and (storage-gated)
// high-external-outlinks thresholds, plus links_no_internal_outlinks on a page
// with only external links.
func TestLinksHighOutlinks(t *testing.T) {
	cfg := config.Default()
	cfg.Thresholds.HighInternalOutlinks = 3
	cfg.Thresholds.HighExternalOutlinks = 2
	cfg.Links.External.Store = true

	hi := expansionFacts()
	for i := 0; i < 5; i++ {
		hi.Links = append(hi.Links, parse.Link{
			Type: parse.Hyperlink, URL: "https://ex.com/i" + string(rune('a'+i)), Anchor: "internal link"})
	}
	for i := 0; i < 4; i++ {
		hi.Links = append(hi.Links, parse.Link{
			Type: parse.Hyperlink, URL: "https://other.com/e" + string(rune('a'+i)), Anchor: "external link"})
	}
	page := htmlPage("https://ex.com/hub", hi)

	// a page whose only hyperlinks are external -> no internal outlinks
	extOnly := expansionFacts()
	extOnly.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://other.com/x", Anchor: "out"},
	}
	extPage := htmlPage("https://ex.com/ext-only", extOnly)

	m := map[string]*crawler.PageRecord{page.URL: page, extPage.URL: extPage}
	occs := Evaluate(m, cfg)

	if !has(occs, "https://ex.com/hub", "links_high_internal_outlinks") {
		t.Error("missing links_high_internal_outlinks")
	}
	if !has(occs, "https://ex.com/hub", "links_high_external_outlinks") {
		t.Error("missing links_high_external_outlinks")
	}
	if !has(occs, "https://ex.com/ext-only", "links_no_internal_outlinks") {
		t.Error("missing links_no_internal_outlinks on an external-only page")
	}
}

// TestNonDescriptiveAnchor covers the non-descriptive-anchor branch of links:
// an anchor matching a configured non-descriptive term is flagged, while a
// descriptive one is not.
func TestNonDescriptiveAnchor(t *testing.T) {
	f := expansionFacts()
	f.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://ex.com/a", Anchor: "click here"},
		{Type: parse.Hyperlink, URL: "https://ex.com/b", Anchor: "Read our annual report"},
	}
	page := htmlPage("https://ex.com/p", f)
	targetA := htmlPage("https://ex.com/a", expansionFacts())
	targetB := htmlPage("https://ex.com/b", expansionFacts())

	cfg := config.Default()
	cfg.Thresholds.NonDescriptiveAnchors = []string{"click here", "read more"}
	occs := Evaluate(map[string]*crawler.PageRecord{
		page.URL: page, targetA.URL: targetA, targetB.URL: targetB}, cfg)

	if got := detailsOf(occs, "https://ex.com/p", "links_non_descriptive_anchor"); len(got) != 1 || got[0] != "click here" {
		t.Errorf("links_non_descriptive_anchor details = %v, want exactly [click here]", got)
	}
}

// TestContentReadabilityDifficult covers the moderate-difficulty readability
// branch (30 <= Flesch < 50) that the base tests skip (they hit < 30).
func TestContentReadabilityDifficult(t *testing.T) {
	f := expansionFacts()
	f.WordCount = 400
	f.Flesch = 40
	f.ContentText = "moderately difficult prose"
	page := htmlPage("https://ex.com/hard", f)

	occs := eval(page)
	if !has(occs, "https://ex.com/hard", "content_readability_difficult") {
		t.Error("missing content_readability_difficult for Flesch 40")
	}
	if has(occs, "https://ex.com/hard", "content_readability_very_difficult") {
		t.Error("Flesch 40 must not be 'very difficult' (that is < 30)")
	}
}

// TestCanonicalMissing pins canonical_missing: an indexable HTML page with no
// canonical at all is flagged, while a page that declares one is not.
func TestCanonicalMissing(t *testing.T) {
	noCanon := htmlPage("https://ex.com/no-canon", expansionFacts())
	withCanonFacts := expansionFacts()
	withCanonFacts.CanonicalHTML = []string{"https://ex.com/with-canon"}
	withCanon := htmlPage("https://ex.com/with-canon", withCanonFacts)

	occs := eval(noCanon, withCanon)
	if !has(occs, "https://ex.com/no-canon", "canonical_missing") {
		t.Error("missing canonical_missing on an indexable page with no canonical")
	}
	if has(occs, "https://ex.com/with-canon", "canonical_missing") {
		t.Error("page with a canonical flagged as missing")
	}
}
