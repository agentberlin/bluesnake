package issues

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

func htmlPage(url string, facts *parse.Facts) *crawler.PageRecord {
	return &crawler.PageRecord{
		URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		Indexable: true, Facts: facts,
	}
}

func has(occs []Occurrence, url, id string) bool {
	for _, o := range occs {
		if o.URL == url && o.IssueID == id {
			return true
		}
	}
	return false
}

func eval(pages ...*crawler.PageRecord) []Occurrence {
	m := map[string]*crawler.PageRecord{}
	for _, p := range pages {
		m[p.URL] = p
	}
	return Evaluate(m, config.Default())
}

func TestCatalogueSanity(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Catalogue() {
		if seen[d.ID] {
			t.Errorf("duplicate issue id %s", d.ID)
		}
		seen[d.ID] = true
		if d.Severity != Issue && d.Severity != Warning && d.Severity != Opportunity {
			t.Errorf("%s: bad severity %s", d.ID, d.Severity)
		}
		if d.Priority != High && d.Priority != Medium && d.Priority != Low {
			t.Errorf("%s: bad priority %s", d.ID, d.Priority)
		}
		if d.Tab == "" || d.Name == "" {
			t.Errorf("%s: missing tab/name", d.ID)
		}
	}
	if len(seen) < 70 {
		t.Errorf("catalogue has %d issues, expected >= 70", len(seen))
	}
}

func TestResponseCodeIssues(t *testing.T) {
	occs := eval(
		&crawler.PageRecord{URL: "https://ex.com/404", Scope: "internal", State: crawler.StateCrawled, StatusCode: 404},
		&crawler.PageRecord{URL: "https://ex.com/500", Scope: "internal", State: crawler.StateCrawled, StatusCode: 500},
		&crawler.PageRecord{URL: "https://ex.com/301", Scope: "internal", State: crawler.StateCrawled, StatusCode: 301},
		&crawler.PageRecord{URL: "https://ex.com/err", Scope: "internal", State: crawler.StateError, FetchError: "timeout"},
		&crawler.PageRecord{URL: "https://ex.com/blocked", Scope: "internal", State: crawler.StateBlockedRobots, MatchedRobotsLine: 2},
		&crawler.PageRecord{URL: "https://other.com/404", Scope: "external", State: crawler.StateCrawled, StatusCode: 404},
	)
	for url, id := range map[string]string{
		"https://ex.com/404":     "internal_client_error",
		"https://ex.com/500":     "internal_server_error",
		"https://ex.com/301":     "internal_redirect",
		"https://ex.com/err":     "internal_no_response",
		"https://ex.com/blocked": "internal_blocked_robots",
		"https://other.com/404":  "external_client_error",
	} {
		if !has(occs, url, id) {
			t.Errorf("missing %s on %s", id, url)
		}
	}
}

func TestTitleAndHeadingIssues(t *testing.T) {
	occs := eval(
		htmlPage("https://ex.com/missing", &parse.Facts{H2s: []string{"sub"}}),
		htmlPage("https://ex.com/long", &parse.Facts{
			Titles: []string{strings.Repeat("long title ", 10)},
			H1s:    []string{"ok"}, HeadingLevels: []int{1}, H2s: []string{"x"},
		}),
		htmlPage("https://ex.com/same", &parse.Facts{
			Titles: []string{"Same Text"}, H1s: []string{"same text"},
			HeadingLevels: []int{1}, H2s: []string{"x"},
		}),
		htmlPage("https://ex.com/multi", &parse.Facts{
			Titles: []string{"one two three four five six seven eight", "second"},
			H1s:    []string{"a", "b"}, HeadingLevels: []int{2, 1}, H2s: []string{"x", "y"},
		}),
	)
	checks := map[string][]string{
		"https://ex.com/missing": {"title_missing", "h1_missing"},
		"https://ex.com/long":    {"title_over_chars"},
		"https://ex.com/same":    {"title_same_as_h1"},
		"https://ex.com/multi":   {"title_multiple", "h1_multiple", "h1_non_sequential", "h2_multiple"},
	}
	for url, ids := range checks {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}
	if has(occs, "https://ex.com/long", "title_missing") {
		t.Error("page with title flagged as missing")
	}
}

func TestDuplicates(t *testing.T) {
	mk := func(url, title, hash string) *crawler.PageRecord {
		return htmlPage(url, &parse.Facts{Titles: []string{title}, Hash: hash, H1s: []string{url}, H2s: []string{"x"}, HeadingLevels: []int{1}})
	}
	occs := eval(
		mk("https://ex.com/a", "Same Title", "hash1"),
		mk("https://ex.com/b", "Same Title", "hash1"),
		mk("https://ex.com/c", "Unique Title Here", "hash2"),
	)
	for _, url := range []string{"https://ex.com/a", "https://ex.com/b"} {
		if !has(occs, url, "title_duplicate") {
			t.Errorf("missing title_duplicate on %s", url)
		}
		if !has(occs, url, "content_exact_duplicate") {
			t.Errorf("missing content_exact_duplicate on %s", url)
		}
	}
	if has(occs, "https://ex.com/c", "title_duplicate") || has(occs, "https://ex.com/c", "content_exact_duplicate") {
		t.Error("unique page flagged as duplicate")
	}
}

func TestSecurityIssues(t *testing.T) {
	rec := htmlPage("https://ex.com/p", &parse.Facts{
		Titles: []string{"a reasonable length page title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		Links: []parse.Link{
			{Type: parse.Image, URL: "http://ex.com/i.png"},
			{Type: parse.CSS, URL: "https://cdn.x.com/s.css", PathType: "protocol_relative"},
			{Type: parse.Hyperlink, URL: "https://other.com/x", Target: "_blank", Rel: ""},
			{Type: parse.Hyperlink, URL: "https://safe.com/x", Target: "_blank", Rel: "noopener"},
			{Type: parse.Hyperlink, URL: "http://localhost:3000/dev", Anchor: "dev"},
		},
		Forms: []parse.Form{{Action: "http://ex.com/submit"}},
	})
	rec.Headers = map[string]string{"X-Content-Type-Options": "nosniff"}
	httpPage := htmlPage("http://ex.com/insecure", &parse.Facts{
		Titles: []string{"another reasonable length page title"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		Forms: []parse.Form{{Action: "http://ex.com/insecure"}},
	})
	occs := eval(rec, httpPage)

	for _, id := range []string{
		"security_mixed_content", "security_protocol_relative",
		"security_unsafe_cross_origin", "security_form_url_insecure",
		"security_missing_hsts", "security_missing_csp",
		"security_missing_x_frame_options", "security_missing_referrer_policy",
		"links_to_localhost",
	} {
		if !has(occs, "https://ex.com/p", id) {
			t.Errorf("missing %s", id)
		}
	}
	if has(occs, "https://ex.com/p", "security_missing_content_type_options") {
		t.Error("nosniff header present but flagged")
	}
	found := false
	for _, o := range occs {
		if o.URL == "https://ex.com/p" && o.IssueID == "security_unsafe_cross_origin" && o.Detail == "https://safe.com/x" {
			found = true
		}
	}
	if found {
		t.Error("noopener link flagged as unsafe")
	}
	if !has(occs, "http://ex.com/insecure", "security_http_url") {
		t.Error("missing security_http_url")
	}
	if !has(occs, "http://ex.com/insecure", "security_form_on_http") {
		t.Error("missing security_form_on_http")
	}
}

func TestURLIssues(t *testing.T) {
	mkURL := func(url string) *crawler.PageRecord {
		return htmlPage(url, &parse.Facts{Titles: []string{"a reasonable length page title here"},
			H1s: []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1}})
	}
	occs := eval(
		mkURL("https://ex.com/Upper/Case"),
		mkURL("https://ex.com/with_underscore"),
		mkURL("https://ex.com/p?utm_source=x"),
		mkURL("https://ex.com/a//b"),
		mkURL("https://ex.com/repeat/repeat/x"),
	)
	for url, ids := range map[string][]string{
		"https://ex.com/Upper/Case":      {"url_uppercase"},
		"https://ex.com/with_underscore": {"url_underscores"},
		"https://ex.com/p?utm_source=x":  {"url_parameters", "url_ga_params"},
		"https://ex.com/a//b":            {"url_multiple_slashes"},
		"https://ex.com/repeat/repeat/x": {"url_repetitive_path"},
	} {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}
}

func TestContentAndCanonicalIssues(t *testing.T) {
	canonTarget := htmlPage("https://ex.com/gone", &parse.Facts{})
	canonTarget.StatusCode = 404
	canonTarget.Indexable = false
	occs := eval(
		htmlPage("https://ex.com/thin", &parse.Facts{
			Titles: []string{"a reasonable length page title here"},
			H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
			WordCount: 50, ContentText: "lorem ipsum dolor", Flesch: 80,
		}),
		func() *crawler.PageRecord {
			r := htmlPage("https://ex.com/canon", &parse.Facts{
				Titles: []string{"a reasonable length page title here"},
				H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
				CanonicalHTML: []string{"https://ex.com/gone", "https://ex.com/other"},
				Links: []parse.Link{
					{Type: parse.Canonical, URL: "https://ex.com/gone", Raw: "/gone", PathType: "root_relative"},
				},
			})
			return r
		}(),
		canonTarget,
	)
	for url, ids := range map[string][]string{
		"https://ex.com/thin": {"content_low_word_count", "content_lorem_ipsum"},
		"https://ex.com/canon": {"canonical_multiple", "canonical_multiple_conflicting",
			"canonical_relative", "canonical_non_indexable_target"},
	} {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}
}

func TestIgnoreNonIndexable(t *testing.T) {
	page := htmlPage("https://ex.com/noindex", &parse.Facts{MetaRobots: []string{"noindex"}})
	page.Indexable = false
	page.IndexabilityStatus = "Noindex"

	cfg := config.Default()
	cfg.Advanced.IgnoreNonIndexableForIssues = true
	occs := Evaluate(map[string]*crawler.PageRecord{page.URL: page}, cfg)
	if has(occs, page.URL, "title_missing") {
		t.Error("non-indexable page must not get element issues when ignored")
	}
	if !has(occs, page.URL, "directive_noindex") {
		t.Error("directive issues still apply")
	}
}

func TestRemainingElementChecks(t *testing.T) {
	occs := eval(
		htmlPage("https://ex.com/d", &parse.Facts{
			Titles:        []string{"a reasonable length page title here"},
			Descriptions:  []string{strings.Repeat("very long description ", 10), "second desc"},
			Keywords:      []string{"a", "b"},
			H1s:           []string{strings.Repeat("long h1 ", 12)},
			H2s:           []string{strings.Repeat("long h2 ", 12)},
			HeadingLevels: []int{1, 2},
			WordCount:     500, Flesch: 20, ContentText: "complex words",
		}),
		htmlPage("https://ex.com/soft404", &parse.Facts{
			Titles: []string{"a reasonable length soft error title"},
			H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
			WordCount: 300, Flesch: 60, ContentText: "sorry, page not found",
		}),
	)
	for url, ids := range map[string][]string{
		"https://ex.com/d": {"description_over_chars", "description_multiple",
			"keywords_multiple", "h1_over_chars", "h2_over_chars",
			"content_readability_very_difficult"},
		"https://ex.com/soft404": {"content_soft_404"},
	} {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}
}

func TestImageAndDepthChecks(t *testing.T) {
	img := &crawler.PageRecord{URL: "https://ex.com/big.png", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 200, ContentType: "image/png",
		Size: 500 * 1024}
	page := htmlPage("https://ex.com/p", &parse.Facts{
		Titles: []string{"a reasonable length page title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		Links: []parse.Link{
			{Type: parse.Image, URL: "https://ex.com/big.png", Alt: ""},
			{Type: parse.Image, URL: "https://ex.com/ok.png", Alt: strings.Repeat("long alt ", 20)},
			{Type: parse.Hyperlink, URL: "https://ex.com/x", Anchor: ""},
		},
	})
	page.Depth = 9
	occs := eval(page, img)
	for _, id := range []string{"image_missing_alt", "image_alt_over_chars",
		"links_no_anchor_text", "links_high_crawl_depth"} {
		if !has(occs, "https://ex.com/p", id) {
			t.Errorf("missing %s", id)
		}
	}
	if !has(occs, "https://ex.com/big.png", "image_over_size") {
		t.Error("missing image_over_size on the image URL")
	}
}

func TestDirectiveChecks(t *testing.T) {
	page := htmlPage("https://ex.com/p", &parse.Facts{
		Titles: []string{"a reasonable length page title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		MetaRobots: []string{"noindex, nofollow"},
		XRobotsTag: []string{"none"},
	})
	occs := eval(page)
	for _, id := range []string{"directive_noindex", "directive_nofollow", "directive_none"} {
		if !has(occs, "https://ex.com/p", id) {
			t.Errorf("missing %s", id)
		}
	}
}

func TestValidationAndAMPChecks(t *testing.T) {
	broken := htmlPage("https://ex.com/broken", &parse.Facts{
		Titles: []string{"a reasonable length page title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		Head: parse.HeadValidity{MissingHead: true, MultipleBody: true,
			InvalidElementsInHead: []string{"div"}},
	})
	amp := htmlPage("https://ex.com/amp", &parse.Facts{
		Titles: []string{"an amp page with a fine title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		IsAMP: true, // no canonical, viewport, charset, or amp script
	})
	desktop := htmlPage("https://ex.com/desktop", &parse.Facts{
		Titles: []string{"the desktop variant with a title"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		AMPLinks: []string{"https://ex.com/amp"},
	})
	occs := eval(broken, amp, desktop)
	for url, ids := range map[string][]string{
		"https://ex.com/broken": {"validation_missing_head", "validation_multiple_body",
			"validation_invalid_head_elements"},
		"https://ex.com/amp": {"amp_missing_canonical", "amp_missing_viewport",
			"amp_missing_charset", "amp_missing_script"},
		"https://ex.com/desktop": {"amp_missing_return_link"},
	} {
		for _, id := range ids {
			if !has(occs, url, id) {
				t.Errorf("missing %s on %s", id, url)
			}
		}
	}

	// a correct AMP page raises none of the AMP issues
	good := htmlPage("https://ex.com/amp-good", &parse.Facts{
		Titles: []string{"a good amp page with a fine title"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		IsAMP: true, HasViewport: true, HasCharset: true, HasAMPScript: true,
		CanonicalHTML: []string{"https://ex.com/desktop"},
	})
	good.Indexable = false
	good.IndexabilityStatus = "Canonicalised"
	occs = eval(good)
	for _, id := range []string{"amp_missing_canonical", "amp_missing_viewport",
		"amp_missing_charset", "amp_missing_script", "amp_indexable"} {
		if has(occs, "https://ex.com/amp-good", id) {
			t.Errorf("good AMP page flagged with %s", id)
		}
	}
}
