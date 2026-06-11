package issues

import (
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/parse"
)

// expansionFacts returns Facts complete enough that the pre-existing element
// and mobile/validation checks stay quiet, so each test below isolates the
// expansion check under scrutiny.
func expansionFacts() *parse.Facts {
	return &parse.Facts{
		Titles: []string{"a reasonable length page title here"},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
		HasViewport: true, HasCharset: true, Lang: "en",
	}
}

func detailsOf(occs []Occurrence, url, id string) []string {
	var ds []string
	for _, o := range occs {
		if o.URL == url && o.IssueID == id {
			ds = append(ds, o.Detail)
		}
	}
	return ds
}

func TestExpansionCatalogue(t *testing.T) {
	want := []struct {
		id, tab, name string
		sev           Severity
		pri           Priority
	}{
		{"viewport_missing", "mobile", "Viewport Not Set", Issue, High},
		{"charset_missing", "validation", "Missing Charset", Warning, Low},
		{"html_lang_missing", "validation", "Missing <html lang> Attribute", Warning, Low},
		{"image_missing_size_attributes", "images", "Missing Size Attributes", Opportunity, Low},
		{"links_outlinks_to_redirect", "links", "Internal Outlinks To Redirect Pages", Opportunity, Low},
		{"links_outlinks_to_broken", "links", "Internal Outlinks To Broken Pages", Issue, Medium},
		{"redirect_broken", "response_codes", "Redirect To Broken Page", Issue, High},
		{"canonical_to_redirect", "canonicals", "Canonical Is A Redirect", Issue, Medium},
		{"hreflang_outside_head", "hreflang", "Outside <head>", Issue, Medium},
		{"security_insecure_cookie", "security", "Cookie Without Secure Attribute", Warning, Low},
		{"links_nofollow_inlinks_only", "links", "Nofollow Inlinks Only", Warning, Medium},
		{"links_only_non_indexable_inlinks", "links", "Inlinks Only From Non-Indexable Pages", Warning, Low},
		{"canonical_unlinked", "canonicals", "Unlinked Canonical", Warning, Low},
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

func TestViewportMissing(t *testing.T) {
	missing := expansionFacts()
	missing.HasViewport = false
	occs := eval(
		htmlPage("https://ex.com/no-viewport", missing),
		htmlPage("https://ex.com/has-viewport", expansionFacts()),
	)
	if !has(occs, "https://ex.com/no-viewport", "viewport_missing") {
		t.Error("missing viewport_missing on page without viewport meta")
	}
	if has(occs, "https://ex.com/has-viewport", "viewport_missing") {
		t.Error("page with viewport meta flagged")
	}

	// indexable-gated: honours advanced.ignore_non_indexable_for_issues
	gatedFacts := expansionFacts()
	gatedFacts.HasViewport = false
	gated := htmlPage("https://ex.com/gated", gatedFacts)
	gated.Indexable = false
	cfg := config.Default()
	cfg.Advanced.IgnoreNonIndexableForIssues = true
	occs = Evaluate(map[string]*crawler.PageRecord{gated.URL: gated}, cfg)
	if has(occs, gated.URL, "viewport_missing") {
		t.Error("non-indexable page flagged although non-indexable issues are ignored")
	}
}

func TestCharsetMissing(t *testing.T) {
	bareFacts := expansionFacts()
	bareFacts.HasCharset = false
	bare := htmlPage("https://ex.com/bare", bareFacts)
	bare.Headers = map[string]string{"Content-Type": "text/html"}

	headerFacts := expansionFacts()
	headerFacts.HasCharset = false
	viaHeader := htmlPage("https://ex.com/via-header", headerFacts)
	viaHeader.ContentType = "text/html; charset=utf-8"
	viaHeader.Headers = map[string]string{"Content-Type": "text/html; charset=utf-8"}

	viaMeta := htmlPage("https://ex.com/via-meta", expansionFacts())
	viaMeta.Headers = map[string]string{"Content-Type": "text/html"}

	occs := eval(bare, viaHeader, viaMeta)
	if !has(occs, "https://ex.com/bare", "charset_missing") {
		t.Error("missing charset_missing when neither meta nor header declare one")
	}
	if has(occs, "https://ex.com/via-header", "charset_missing") {
		t.Error("charset declared in the Content-Type header but flagged")
	}
	if has(occs, "https://ex.com/via-meta", "charset_missing") {
		t.Error("charset declared via meta tag but flagged")
	}
}

func TestHTMLLangMissing(t *testing.T) {
	noLang := expansionFacts()
	noLang.Lang = ""
	occs := eval(
		htmlPage("https://ex.com/no-lang", noLang),
		htmlPage("https://ex.com/with-lang", expansionFacts()),
	)
	if !has(occs, "https://ex.com/no-lang", "html_lang_missing") {
		t.Error("missing html_lang_missing on page without lang attribute")
	}
	if has(occs, "https://ex.com/with-lang", "html_lang_missing") {
		t.Error("page with html lang attribute flagged")
	}
}

func TestImageMissingSizeAttributes(t *testing.T) {
	f := expansionFacts()
	f.Links = []parse.Link{
		{Type: parse.Image, URL: "https://ex.com/unsized.png", Alt: "a"},
		{Type: parse.Image, URL: "https://ex.com/unsized.png", Alt: "a"}, // second reference: dedupe
		{Type: parse.Image, URL: "https://ex.com/no-height.png", Alt: "b", Width: "100"},
		{Type: parse.Image, URL: "https://ex.com/sized.png", Alt: "c", Width: "100", Height: "80"},
	}
	occs := eval(htmlPage("https://ex.com/p", f))
	counts := map[string]int{}
	for _, d := range detailsOf(occs, "https://ex.com/p", "image_missing_size_attributes") {
		counts[d]++
	}
	if counts["https://ex.com/unsized.png"] != 1 {
		t.Errorf("unsized.png flagged %d times, want exactly 1 (dedupe per image URL)",
			counts["https://ex.com/unsized.png"])
	}
	if counts["https://ex.com/no-height.png"] != 1 {
		t.Errorf("no-height.png flagged %d times, want 1 (missing height alone qualifies)",
			counts["https://ex.com/no-height.png"])
	}
	if counts["https://ex.com/sized.png"] != 0 {
		t.Error("image with both width and height flagged")
	}
}

func TestOutlinksToRedirectAndBroken(t *testing.T) {
	moved := &crawler.PageRecord{URL: "https://ex.com/moved", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 301, RedirectURL: "https://ex.com/ok"}
	gone := &crawler.PageRecord{URL: "https://ex.com/gone", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 404}
	ok := htmlPage("https://ex.com/ok", expansionFacts())

	srcFacts := expansionFacts()
	srcFacts.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://ex.com/moved", Anchor: "moved page"},
		{Type: parse.Hyperlink, URL: "https://ex.com/gone", Anchor: "gone page"},
		{Type: parse.Hyperlink, URL: "https://ex.com/ok", Anchor: "fine page"},
	}
	src := htmlPage("https://ex.com/src", srcFacts)

	cleanFacts := expansionFacts()
	cleanFacts.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://ex.com/ok", Anchor: "fine page"},
	}
	clean := htmlPage("https://ex.com/clean", cleanFacts)

	occs := eval(moved, gone, ok, src, clean)

	if got := detailsOf(occs, "https://ex.com/src", "links_outlinks_to_redirect"); len(got) != 1 || got[0] != "https://ex.com/moved" {
		t.Errorf("links_outlinks_to_redirect details = %v, want exactly [https://ex.com/moved]", got)
	}
	if got := detailsOf(occs, "https://ex.com/src", "links_outlinks_to_broken"); len(got) != 1 || got[0] != "https://ex.com/gone" {
		t.Errorf("links_outlinks_to_broken details = %v, want exactly [https://ex.com/gone]", got)
	}
	for _, id := range []string{"links_outlinks_to_redirect", "links_outlinks_to_broken"} {
		if has(occs, "https://ex.com/clean", id) {
			t.Errorf("page linking only to a 200 page flagged with %s", id)
		}
	}
}

func TestRedirectBroken(t *testing.T) {
	dead := &crawler.PageRecord{URL: "https://ex.com/dead", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 301, RedirectURL: "https://ex.com/gone"}
	gone := &crawler.PageRecord{URL: "https://ex.com/gone", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 404}
	moved := &crawler.PageRecord{URL: "https://ex.com/moved", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 301, RedirectURL: "https://ex.com/ok"}
	ok := htmlPage("https://ex.com/ok", expansionFacts())

	occs := eval(dead, gone, moved, ok)
	if !has(occs, "https://ex.com/dead", "redirect_broken") {
		t.Error("missing redirect_broken on redirect to a 404 page")
	}
	if got := detailsOf(occs, "https://ex.com/dead", "redirect_broken"); len(got) != 1 || got[0] != "https://ex.com/gone" {
		t.Errorf("redirect_broken details = %v, want exactly [https://ex.com/gone]", got)
	}
	if has(occs, "https://ex.com/moved", "redirect_broken") {
		t.Error("redirect to a healthy page flagged")
	}
}

func TestCanonicalToRedirect(t *testing.T) {
	moved := &crawler.PageRecord{URL: "https://ex.com/moved", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 301, RedirectURL: "https://ex.com/ok"}
	ok := htmlPage("https://ex.com/ok", expansionFacts())

	toRedirect := expansionFacts()
	toRedirect.CanonicalHTML = []string{"https://ex.com/moved"}
	toOK := expansionFacts()
	toOK.CanonicalHTML = []string{"https://ex.com/ok"}

	occs := eval(moved, ok,
		htmlPage("https://ex.com/canon-redirect", toRedirect),
		htmlPage("https://ex.com/canon-ok", toOK),
	)
	if !has(occs, "https://ex.com/canon-redirect", "canonical_to_redirect") {
		t.Error("missing canonical_to_redirect when the canonical target is a 301")
	}
	if has(occs, "https://ex.com/canon-ok", "canonical_to_redirect") {
		t.Error("canonical to a 200 page flagged")
	}
}

func TestHreflangOutsideHead(t *testing.T) {
	outside := expansionFacts()
	outside.HreflangOutsideHead = 2
	occs := eval(
		htmlPage("https://ex.com/stray-hreflang", outside),
		htmlPage("https://ex.com/clean-hreflang", expansionFacts()),
	)
	if !has(occs, "https://ex.com/stray-hreflang", "hreflang_outside_head") {
		t.Error("missing hreflang_outside_head")
	}
	if has(occs, "https://ex.com/clean-hreflang", "hreflang_outside_head") {
		t.Error("page without stray hreflang flagged")
	}
}

func TestInsecureCookie(t *testing.T) {
	mk := func(url, cookie string) *crawler.PageRecord {
		p := htmlPage(url, expansionFacts())
		if cookie != "" {
			p.Headers = map[string]string{"Set-Cookie": cookie}
		}
		return p
	}
	occs := eval(
		mk("https://ex.com/plain-cookie", "session=abc; HttpOnly; Path=/"),
		// the cookie *name* contains "secure" but there is no Secure attribute
		mk("https://ex.com/named-secure", "securepref=1; HttpOnly"),
		mk("https://ex.com/secure-cookie", "session=abc; Secure; HttpOnly"),
		mk("https://ex.com/lower-secure", "session=abc; secure"),
		mk("https://ex.com/no-cookie", ""),
		mk("http://ex.com/http-cookie", "session=abc; HttpOnly"),
	)
	for _, url := range []string{"https://ex.com/plain-cookie", "https://ex.com/named-secure"} {
		if !has(occs, url, "security_insecure_cookie") {
			t.Errorf("missing security_insecure_cookie on %s", url)
		}
	}
	for _, url := range []string{"https://ex.com/secure-cookie", "https://ex.com/lower-secure",
		"https://ex.com/no-cookie", "http://ex.com/http-cookie"} {
		if has(occs, url, "security_insecure_cookie") {
			t.Errorf("unexpected security_insecure_cookie on %s", url)
		}
	}
}

// The crawler newline-joins repeated Set-Cookie headers; an insecure cookie
// anywhere in the set must be flagged even when the first one is Secure.
func TestInsecureCookieMultiValue(t *testing.T) {
	mk := func(url, joined string) *crawler.PageRecord {
		p := htmlPage(url, expansionFacts())
		p.Headers = map[string]string{"Set-Cookie": joined}
		return p
	}
	occs := eval(
		mk("https://ex.com/secure-then-insecure", "a=1; Secure; HttpOnly\nb=2; HttpOnly"),
		mk("https://ex.com/all-secure", "a=1; Secure\nb=2; Secure; HttpOnly"),
	)
	if !has(occs, "https://ex.com/secure-then-insecure", "security_insecure_cookie") {
		t.Error("an insecure cookie after a secure one must still be flagged")
	}
	if has(occs, "https://ex.com/all-secure", "security_insecure_cookie") {
		t.Error("all-secure multi-cookie response must not be flagged")
	}
}

func TestNofollowInlinksOnly(t *testing.T) {
	linkTo := func(url string, nofollow bool) parse.Link {
		return parse.Link{Type: parse.Hyperlink, URL: url, Anchor: "target page", Nofollow: nofollow}
	}
	aF := expansionFacts()
	aF.Links = []parse.Link{linkTo("https://ex.com/t", true), linkTo("https://ex.com/t2", true)}
	bF := expansionFacts()
	bF.Links = []parse.Link{linkTo("https://ex.com/t", true)}
	cF := expansionFacts()
	cF.Links = []parse.Link{linkTo("https://ex.com/t2", false)}
	// a followed self-link must not count as an inlink
	tF := expansionFacts()
	tF.Links = []parse.Link{linkTo("https://ex.com/t", false)}

	occs := eval(
		htmlPage("https://ex.com/a", aF),
		htmlPage("https://ex.com/b", bF),
		htmlPage("https://ex.com/c", cF),
		htmlPage("https://ex.com/t", tF),
		htmlPage("https://ex.com/t2", expansionFacts()),
		htmlPage("https://ex.com/no-inlinks", expansionFacts()),
	)
	if !has(occs, "https://ex.com/t", "links_nofollow_inlinks_only") {
		t.Error("missing links_nofollow_inlinks_only when every inlink is nofollow")
	}
	if has(occs, "https://ex.com/t2", "links_nofollow_inlinks_only") {
		t.Error("page with one followed inlink flagged")
	}
	if has(occs, "https://ex.com/no-inlinks", "links_nofollow_inlinks_only") {
		t.Error("page with zero inlinks flagged")
	}
}

func TestOnlyNonIndexableInlinks(t *testing.T) {
	linkTo := func(urls ...string) []parse.Link {
		var ls []parse.Link
		for _, u := range urls {
			ls = append(ls, parse.Link{Type: parse.Hyperlink, URL: u, Anchor: "target page"})
		}
		return ls
	}
	nF := expansionFacts()
	nF.MetaRobots = []string{"noindex"}
	nF.Links = linkTo("https://ex.com/t", "https://ex.com/t2", "https://ex.com/t3")
	n := htmlPage("https://ex.com/noindex-linker", nF)
	n.Indexable = false
	n.IndexabilityStatus = "Noindex"

	aF := expansionFacts()
	aF.Links = linkTo("https://ex.com/t2")
	a := htmlPage("https://ex.com/indexable-linker", aF)

	// an indexable self-link must be excluded from the inlink set
	tF := expansionFacts()
	tF.Links = linkTo("https://ex.com/t")
	tPage := htmlPage("https://ex.com/t", tF)

	t3 := htmlPage("https://ex.com/t3", expansionFacts())
	t3.Indexable = false
	t3.IndexabilityStatus = "Noindex"

	occs := eval(n, a, tPage,
		htmlPage("https://ex.com/t2", expansionFacts()),
		t3,
		htmlPage("https://ex.com/no-inlinks", expansionFacts()),
	)
	if !has(occs, "https://ex.com/t", "links_only_non_indexable_inlinks") {
		t.Error("missing links_only_non_indexable_inlinks when all inlinks come from noindex pages")
	}
	if has(occs, "https://ex.com/t2", "links_only_non_indexable_inlinks") {
		t.Error("page with an indexable inlinker flagged")
	}
	if has(occs, "https://ex.com/t3", "links_only_non_indexable_inlinks") {
		t.Error("non-indexable target flagged; the check applies to indexable pages only")
	}
	if has(occs, "https://ex.com/no-inlinks", "links_only_non_indexable_inlinks") {
		t.Error("page with zero inlinks flagged")
	}
}

func TestCanonicalUnlinked(t *testing.T) {
	refF := expansionFacts()
	refF.CanonicalHTML = []string{"https://ex.com/canon-target"}
	ref := htmlPage("https://ex.com/ref", refF)

	targetF := expansionFacts()
	targetF.CanonicalHTML = []string{"https://ex.com/canon-target"} // self-canonical
	canonTarget := htmlPage("https://ex.com/canon-target", targetF)

	ref2F := expansionFacts()
	ref2F.CanonicalHTML = []string{"https://ex.com/linked-canon"}
	ref2 := htmlPage("https://ex.com/ref2", ref2F)

	linkerF := expansionFacts()
	linkerF.Links = []parse.Link{
		{Type: parse.Hyperlink, URL: "https://ex.com/linked-canon", Anchor: "linked canonical"},
	}
	linker := htmlPage("https://ex.com/linker", linkerF)
	linkedCanon := htmlPage("https://ex.com/linked-canon", expansionFacts())

	// self-canonical with no other referrer is not "another page's canonical"
	selfF := expansionFacts()
	selfF.CanonicalHTML = []string{"https://ex.com/self-only"}
	selfOnly := htmlPage("https://ex.com/self-only", selfF)

	occs := eval(ref, canonTarget, ref2, linker, linkedCanon, selfOnly)
	if !has(occs, "https://ex.com/canon-target", "canonical_unlinked") {
		t.Error("missing canonical_unlinked on a canonical target with no hyperlink inlinks")
	}
	if got := detailsOf(occs, "https://ex.com/canon-target", "canonical_unlinked"); len(got) != 1 || got[0] != "https://ex.com/ref" {
		t.Errorf("canonical_unlinked details = %v, want exactly [https://ex.com/ref]", got)
	}
	if has(occs, "https://ex.com/linked-canon", "canonical_unlinked") {
		t.Error("canonical target with a hyperlink inlink flagged")
	}
	if has(occs, "https://ex.com/self-only", "canonical_unlinked") {
		t.Error("self-canonical page without referrers flagged")
	}
}
