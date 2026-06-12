package issues

import (
	"fmt"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// Second catalogue tranche (2026-06): SF-parity checks over existing crawl
// data completing the directives, pagination, hreflang, links, URL,
// canonicals, validation, javascript, images and h1 tabs. Analysis-phase
// rules (loops, unlinked, return-link family, sitemap size) are tested in
// internal/analyze; their catalogue entries are asserted here.
func TestExpansion2Catalogue(t *testing.T) {
	want := []struct {
		id, tab, name string
		sev           Severity
		pri           Priority
	}{
		{"directive_noimageindex", "directives", "NoImageIndex", Issue, Low},
		{"directive_unavailable_after", "directives", "Unavailable_After", Warning, Medium},
		{"directive_nosnippet", "directives", "NoSnippet", Warning, Low},
		{"directive_notranslate", "directives", "NoTranslate", Warning, Low},
		{"directive_noodp", "directives", "NoODP", Warning, Low},
		{"directive_noydir", "directives", "NoYDIR", Warning, Low},
		{"pagination_not_in_anchor", "pagination", "Pagination URL Not In Anchor Tag", Issue, High},
		{"pagination_non_indexable", "pagination", "Non-Indexable", Warning, High},
		{"pagination_multiple", "pagination", "Multiple Pagination URLs", Issue, Low},
		{"pagination_loop", "pagination", "Pagination Loop", Issue, Low},
		{"pagination_unlinked", "pagination", "Unlinked Pagination URLs", Issue, Medium},
		{"hreflang_multiple_entries", "hreflang", "Multiple Entries", Issue, High},
		{"hreflang_not_using_canonical", "hreflang", "Not Using Canonical", Issue, High},
		{"hreflang_inconsistent_return", "hreflang", "Inconsistent Language & Region Return Links", Issue, High},
		{"hreflang_non_canonical_return", "hreflang", "Non-Canonical Return Links", Issue, High},
		{"hreflang_noindex_return", "hreflang", "Noindex Return Links", Issue, High},
		{"hreflang_unlinked", "hreflang", "Unlinked Hreflang URLs", Issue, Medium},
		{"links_uncrawlable_outlinks", "links", "Pages With Uncrawlable Internal Outlinks", Warning, High},
		{"links_follow_nofollow_inlinks", "links", "Follow & Nofollow Internal Inlinks To Page", Warning, Low},
		{"url_internal_search", "url", "Internal Search", Warning, Low},
		{"canonical_contains_fragment", "canonicals", "Contains Fragment URL", Issue, High},
		{"canonical_invalid_attribute", "canonicals", "Invalid Attribute In Annotation", Issue, High},
		{"validation_resource_over_2mb", "validation", "Resource Over 2MB", Issue, High},
		{"js_description_updated", "javascript", "Meta Description Updated by JavaScript", Warning, Medium},
		{"image_missing_alt_attribute", "images", "Missing Alt Attribute", Issue, Low},
		{"h1_alt_text", "h1", "Alt Text in h1", Warning, Low},
		{"sitemap_over_50k", "sitemaps", "XML Sitemap With Over 50k URLs", Issue, High},
	}
	for _, w := range want {
		d, ok := Lookup(w.id)
		if !ok {
			t.Errorf("%s missing from catalogue", w.id)
			continue
		}
		if d.Tab != w.tab || d.Name != w.name || d.Severity != w.sev || d.Priority != w.pri {
			t.Errorf("%s = %+v, want tab=%s name=%q severity=%s priority=%s",
				w.id, d, w.tab, w.name, w.sev, w.pri)
		}
	}
}

func TestDirectiveValues(t *testing.T) {
	mk := func(url string, meta, xrobots []string) *crawler.PageRecord {
		f := expansionFacts()
		f.MetaRobots = meta
		f.XRobotsTag = xrobots
		return htmlPage(url, f)
	}
	occs := eval(
		mk("https://ex.com/imgidx", []string{"noimageindex"}, nil),
		mk("https://ex.com/snippets", nil, []string{"nosnippet, notranslate"}),
		mk("https://ex.com/expiry", []string{"unavailable_after: 25-Aug-2026 15:00:00 PST"}, nil),
		mk("https://ex.com/dirs", []string{"noodp"}, []string{"noydir"}),
		// max-snippet carries a colon-value and must not match nosnippet
		mk("https://ex.com/maxsnippet", []string{"max-snippet:50"}, nil),
		mk("https://ex.com/plain", []string{"index, follow"}, nil),
	)
	expect := map[string][]string{
		"https://ex.com/imgidx":   {"directive_noimageindex"},
		"https://ex.com/snippets": {"directive_nosnippet", "directive_notranslate"},
		"https://ex.com/expiry":   {"directive_unavailable_after"},
		"https://ex.com/dirs":     {"directive_noodp", "directive_noydir"},
	}
	for url, ids := range expect {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}
	for _, id := range []string{"directive_nosnippet", "directive_noimageindex", "directive_noodp",
		"directive_noydir", "directive_notranslate", "directive_unavailable_after"} {
		if has(occs, "https://ex.com/maxsnippet", id) || has(occs, "https://ex.com/plain", id) {
			t.Errorf("unexpected %s on a page without that directive", id)
		}
	}
	if got := detailsOf(occs, "https://ex.com/expiry", "directive_unavailable_after"); len(got) != 1 || got[0] != "25-Aug-2026 15:00:00 PST" {
		t.Errorf("unavailable_after detail = %v, want the expiry date", got)
	}
}

func TestPaginationNotInAnchor(t *testing.T) {
	bare := expansionFacts()
	bare.NextHTML = []string{"https://ex.com/p2"}
	linked := expansionFacts()
	linked.NextHTML = []string{"https://ex.com/p2"}
	linked.Links = []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.com/p2", Anchor: "next page"}}

	occs := eval(
		htmlPage("https://ex.com/bare", bare),
		htmlPage("https://ex.com/linked", linked),
		htmlPage("https://ex.com/p2", expansionFacts()),
	)
	if !has(occs, "https://ex.com/bare", "pagination_not_in_anchor") {
		t.Error("missing pagination_not_in_anchor when rel=next URL is not hyperlinked")
	}
	if has(occs, "https://ex.com/linked", "pagination_not_in_anchor") {
		t.Error("page hyperlinking its rel=next target flagged")
	}
}

func TestPaginationNonIndexable(t *testing.T) {
	src := expansionFacts()
	src.NextHTML = []string{"https://ex.com/noidx", "https://ex.com/ok", "https://ex.com/404"}
	src.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://ex.com/noidx", Anchor: "next"},
		{Type: parse.Hyperlink, URL: "https://ex.com/ok", Anchor: "next"},
		{Type: parse.Hyperlink, URL: "https://ex.com/404", Anchor: "next"},
	}
	noidx := htmlPage("https://ex.com/noidx", expansionFacts())
	noidx.Indexable, noidx.IndexabilityStatus = false, "Noindex"
	gone := &crawler.PageRecord{URL: "https://ex.com/404", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 404}

	occs := eval(htmlPage("https://ex.com/src", src), noidx,
		htmlPage("https://ex.com/ok", expansionFacts()), gone)
	if got := detailsOf(occs, "https://ex.com/src", "pagination_non_indexable"); len(got) != 1 || got[0] != "https://ex.com/noidx" {
		t.Errorf("pagination_non_indexable details = %v, want exactly [https://ex.com/noidx] (non-200s belong to pagination_non_200)", got)
	}
}

func TestPaginationMultiple(t *testing.T) {
	multi := expansionFacts()
	multi.NextHTML = []string{"https://ex.com/p2", "https://ex.com/p3"}
	normal := expansionFacts()
	normal.NextHTML = []string{"https://ex.com/p3"}
	normal.PrevHTML = []string{"https://ex.com/p1"}

	occs := eval(
		htmlPage("https://ex.com/multi", multi),
		htmlPage("https://ex.com/normal", normal),
	)
	if !has(occs, "https://ex.com/multi", "pagination_multiple") {
		t.Error("missing pagination_multiple on two rel=next annotations")
	}
	if has(occs, "https://ex.com/normal", "pagination_multiple") {
		t.Error("a middle page (one next + one prev) flagged as multiple")
	}
}

func TestHreflangMultipleEntries(t *testing.T) {
	conflict := expansionFacts()
	conflict.HreflangHTML = []parse.Hreflang{
		{Lang: "en", URL: "https://ex.com/a"},
		{Lang: "EN", URL: "https://ex.com/b"}, // codes are case-insensitive
		{Lang: "de", URL: "https://ex.com/de"},
	}
	repeated := expansionFacts()
	repeated.HreflangHTML = []parse.Hreflang{
		{Lang: "en", URL: "https://ex.com/a"},
		{Lang: "en", URL: "https://ex.com/a"}, // same URL twice is not a conflict
	}
	occs := eval(
		htmlPage("https://ex.com/conflict", conflict),
		htmlPage("https://ex.com/repeated", repeated),
	)
	if got := detailsOf(occs, "https://ex.com/conflict", "hreflang_multiple_entries"); len(got) != 1 || got[0] != "en" {
		t.Errorf("hreflang_multiple_entries details = %v, want exactly [en]", got)
	}
	if has(occs, "https://ex.com/repeated", "hreflang_multiple_entries") {
		t.Error("repeated identical annotation flagged as multiple entries")
	}
}

func TestHreflangNotUsingCanonical(t *testing.T) {
	canonicalised := expansionFacts()
	canonicalised.CanonicalHTML = []string{"https://ex.com/canonical"}
	canonicalised.HreflangHTML = []parse.Hreflang{{Lang: "en", URL: "https://ex.com/self"}}

	selfCanonical := expansionFacts()
	selfCanonical.CanonicalHTML = []string{"https://ex.com/clean"}
	selfCanonical.HreflangHTML = []parse.Hreflang{{Lang: "en", URL: "https://ex.com/clean"}}

	occs := eval(
		htmlPage("https://ex.com/self", canonicalised),
		htmlPage("https://ex.com/clean", selfCanonical),
		htmlPage("https://ex.com/canonical", expansionFacts()),
	)
	if !has(occs, "https://ex.com/self", "hreflang_not_using_canonical") {
		t.Error("missing hreflang_not_using_canonical: self-annotation on a canonicalised page")
	}
	if has(occs, "https://ex.com/clean", "hreflang_not_using_canonical") {
		t.Error("self-canonical page flagged")
	}
}

func TestLinksUncrawlableOutlinks(t *testing.T) {
	f := expansionFacts()
	f.Links = []parse.Link{
		{Type: parse.Uncrawlable, Raw: "javascript:void(0)", Anchor: "menu"},
		{Type: parse.Uncrawlable, Raw: "/x", ElemPath: "/html/body/div"},
		{Type: parse.Hyperlink, URL: "https://ex.com/fine", Anchor: "fine"},
	}
	occs := eval(
		htmlPage("https://ex.com/unc", f),
		htmlPage("https://ex.com/fine", expansionFacts()),
	)
	if got := detailsOf(occs, "https://ex.com/unc", "links_uncrawlable_outlinks"); len(got) != 1 || got[0] != "2 uncrawlable outlinks" {
		t.Errorf("links_uncrawlable_outlinks details = %v, want one occurrence counting 2", got)
	}
	if has(occs, "https://ex.com/fine", "links_uncrawlable_outlinks") {
		t.Error("page without uncrawlable links flagged")
	}
}

func TestFollowNofollowInlinks(t *testing.T) {
	linkTo := func(url string, nofollow bool) parse.Link {
		return parse.Link{Type: parse.Hyperlink, URL: url, Anchor: "target page", Nofollow: nofollow}
	}
	aF := expansionFacts()
	aF.Links = []parse.Link{linkTo("https://ex.com/mixed", false), linkTo("https://ex.com/all-nf", true)}
	bF := expansionFacts()
	bF.Links = []parse.Link{linkTo("https://ex.com/mixed", true), linkTo("https://ex.com/all-f", false)}

	occs := eval(
		htmlPage("https://ex.com/a", aF),
		htmlPage("https://ex.com/b", bF),
		htmlPage("https://ex.com/mixed", expansionFacts()),
		htmlPage("https://ex.com/all-nf", expansionFacts()),
		htmlPage("https://ex.com/all-f", expansionFacts()),
	)
	if !has(occs, "https://ex.com/mixed", "links_follow_nofollow_inlinks") {
		t.Error("missing links_follow_nofollow_inlinks on a page with mixed inlinks")
	}
	for _, url := range []string{"https://ex.com/all-nf", "https://ex.com/all-f"} {
		if has(occs, url, "links_follow_nofollow_inlinks") {
			t.Errorf("uniform-inlink page %s flagged as mixed", url)
		}
	}
}

func TestURLInternalSearch(t *testing.T) {
	mk := func(url string) *crawler.PageRecord { return htmlPage(url, nil) }
	occs := eval(
		mk("https://ex.com/search?q=shoes"),
		mk("https://ex.com/find?query=boots"),
		mk("https://ex.com/blog?s="),       // empty value: not a search result
		mk("https://ex.com/list?sort=asc"), // not a search parameter
	)
	for _, url := range []string{"https://ex.com/search?q=shoes", "https://ex.com/find?query=boots"} {
		if !has(occs, url, "url_internal_search") {
			t.Errorf("missing url_internal_search on %s", url)
		}
	}
	for _, url := range []string{"https://ex.com/blog?s=", "https://ex.com/list?sort=asc"} {
		if has(occs, url, "url_internal_search") {
			t.Errorf("unexpected url_internal_search on %s", url)
		}
	}
}

func TestCanonicalContainsFragment(t *testing.T) {
	frag := expansionFacts()
	frag.CanonicalHTML = []string{"https://ex.com/canon"}
	frag.Links = []parse.Link{{Type: parse.Canonical, Raw: "/canon#top", URL: "https://ex.com/canon", PathType: "root_relative"}}
	clean := expansionFacts()
	clean.CanonicalHTML = []string{"https://ex.com/clean"}
	clean.Links = []parse.Link{{Type: parse.Canonical, Raw: "https://ex.com/clean", URL: "https://ex.com/clean", PathType: "absolute"}}

	occs := eval(
		htmlPage("https://ex.com/frag", frag),
		htmlPage("https://ex.com/clean", clean),
		htmlPage("https://ex.com/canon", expansionFacts()),
	)
	if got := detailsOf(occs, "https://ex.com/frag", "canonical_contains_fragment"); len(got) != 1 || got[0] != "/canon#top" {
		t.Errorf("canonical_contains_fragment details = %v, want the raw href", got)
	}
	if has(occs, "https://ex.com/clean", "canonical_contains_fragment") {
		t.Error("fragment-free canonical flagged")
	}
}

func TestCanonicalInvalidAttribute(t *testing.T) {
	bad := expansionFacts()
	bad.CanonicalHTML = []string{"https://ex.com/canon"}
	bad.CanonicalInvalidAttrs = []string{"hreflang", "media"}
	good := expansionFacts()
	good.CanonicalHTML = []string{"https://ex.com/canon"}

	occs := eval(
		htmlPage("https://ex.com/bad", bad),
		htmlPage("https://ex.com/good", good),
		htmlPage("https://ex.com/canon", expansionFacts()),
	)
	if got := detailsOf(occs, "https://ex.com/bad", "canonical_invalid_attribute"); len(got) != 1 || got[0] != "hreflang, media" {
		t.Errorf("canonical_invalid_attribute details = %v, want the offending attributes", got)
	}
	if has(occs, "https://ex.com/good", "canonical_invalid_attribute") {
		t.Error("attribute-clean canonical flagged")
	}
}

func TestValidationResourceOver2MB(t *testing.T) {
	mk := func(url, ct string, size int) *crawler.PageRecord {
		return &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, ContentType: ct, Size: size}
	}
	bigHTMLFacts := expansionFacts()
	bigHTML := htmlPage("https://ex.com/big.html", bigHTMLFacts)
	bigHTML.Size = 3 * 1024 * 1024

	occs := eval(
		mk("https://ex.com/big.css", "text/css", 3*1024*1024),
		mk("https://ex.com/big.js", "application/javascript", 3*1024*1024),
		mk("https://ex.com/small.css", "text/css", 1024*1024),
		mk("https://ex.com/big.png", "image/png", 3*1024*1024),
		bigHTML,
	)
	for _, url := range []string{"https://ex.com/big.css", "https://ex.com/big.js"} {
		if !has(occs, url, "validation_resource_over_2mb") {
			t.Errorf("missing validation_resource_over_2mb on %s", url)
		}
	}
	for _, url := range []string{"https://ex.com/small.css", "https://ex.com/big.png"} {
		if has(occs, url, "validation_resource_over_2mb") {
			t.Errorf("unexpected validation_resource_over_2mb on %s", url)
		}
	}
	// HTML documents have their own 2MB check
	if has(occs, "https://ex.com/big.html", "validation_resource_over_2mb") {
		t.Error("HTML page flagged as an oversized resource")
	}
	if !has(occs, "https://ex.com/big.html", "validation_document_over_2mb") {
		t.Error("missing validation_document_over_2mb on the oversized HTML page")
	}
}

func TestJSDescriptionUpdated(t *testing.T) {
	changed := htmlPage("https://ex.com/changed", expansionFacts())
	changed.JSDiff = &crawler.JSDiff{DescriptionChanged: true}
	same := htmlPage("https://ex.com/same", expansionFacts())
	same.JSDiff = &crawler.JSDiff{}

	occs := eval(changed, same)
	if !has(occs, "https://ex.com/changed", "js_description_updated") {
		t.Error("missing js_description_updated when the rendered description differs")
	}
	if has(occs, "https://ex.com/same", "js_description_updated") {
		t.Error("unchanged description flagged")
	}
}

func TestImageMissingAltAttribute(t *testing.T) {
	f := expansionFacts()
	f.Links = []parse.Link{
		{Type: parse.Image, URL: "https://ex.com/no-attr.png", NoAltAttr: true, Width: "10", Height: "10"},
		{Type: parse.Image, URL: "https://ex.com/empty-alt.png", Alt: "", Width: "10", Height: "10"},
		{Type: parse.Image, URL: "https://ex.com/fine.png", Alt: "described", Width: "10", Height: "10"},
	}
	cfg := config.Default()
	cfg.Resources.Images.Store = true // image checks are storage-gated
	page := htmlPage("https://ex.com/p", f)
	occs := Evaluate(map[string]*crawler.PageRecord{page.URL: page}, cfg)

	if got := detailsOf(occs, "https://ex.com/p", "image_missing_alt_attribute"); len(got) != 1 || got[0] != "https://ex.com/no-attr.png" {
		t.Errorf("image_missing_alt_attribute details = %v, want exactly the attribute-less image", got)
	}
	if got := detailsOf(occs, "https://ex.com/p", "image_missing_alt"); len(got) != 1 || got[0] != "https://ex.com/empty-alt.png" {
		t.Errorf("image_missing_alt details = %v, want exactly the empty-alt image (missing attribute is its own check)", got)
	}
}

func TestH1AltText(t *testing.T) {
	alt := expansionFacts()
	alt.H1s = []string{"Company Logo"}
	alt.H1AltText = true
	occs := eval(
		htmlPage("https://ex.com/alt-h1", alt),
		htmlPage("https://ex.com/text-h1", expansionFacts()),
	)
	if !has(occs, "https://ex.com/alt-h1", "h1_alt_text") {
		t.Error("missing h1_alt_text on a page whose h1 text came from an image alt")
	}
	if has(occs, "https://ex.com/alt-h1", "h1_missing") {
		t.Error("alt-text h1 also reported missing — the alt is the h1 text")
	}
	if has(occs, "https://ex.com/text-h1", "h1_alt_text") {
		t.Error("page with a real-text h1 flagged")
	}
}

// guard against substring matches: 100 hyperlinks must not trip the
// 1000-internal-outlinks threshold via the uncrawlable counter
func TestUncrawlableDoesNotCountAsInternalOutlinks(t *testing.T) {
	f := expansionFacts()
	for i := 0; i < 30; i++ {
		f.Links = append(f.Links, parse.Link{Type: parse.Uncrawlable, Raw: fmt.Sprintf("javascript:nav(%d)", i)})
	}
	f.Links = append(f.Links, parse.Link{Type: parse.Hyperlink, URL: "https://ex.com/t", Anchor: "target page"})
	occs := eval(htmlPage("https://ex.com/p", f), htmlPage("https://ex.com/t", expansionFacts()))
	if has(occs, "https://ex.com/p", "links_no_internal_outlinks") {
		t.Error("page with a crawlable hyperlink reported as having no internal outlinks")
	}
	if !has(occs, "https://ex.com/p", "links_uncrawlable_outlinks") {
		t.Error("missing links_uncrawlable_outlinks")
	}
}
