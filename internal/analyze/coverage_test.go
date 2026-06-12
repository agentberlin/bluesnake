package analyze

// The catalogue coverage meta-test enforces DESIGN.md §6: every issue in the
// catalogue must have at least one fixture that triggers it, and a healthy
// fixture that triggers nothing. It lives in this package (not internal/
// issues) because analysis-phase issues need analyze.Run and analyze already
// imports issues. Adding a catalogue entry without a fixture here fails the
// build's test gate with a "no fixture triggers <id>" error.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
)

// covPage is a minimal internal crawled HTML page.
func covPage(url string) *crawler.PageRecord {
	return &crawler.PageRecord{
		URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		Indexable: true, IndexabilityStatus: "Indexable",
		Facts: &parse.Facts{},
	}
}

func covRedirect(url, target string) *crawler.PageRecord {
	return &crawler.PageRecord{
		URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 301, Status: "Moved Permanently", RedirectURL: target, RedirectType: "http",
	}
}

// kitchenSink builds one page set (plus a sitemap index) that collectively
// trips every issue in the catalogue.
func kitchenSink() (map[string]*crawler.PageRecord, SitemapIndex) {
	const ks = "https://ks.ex"
	pages := map[string]*crawler.PageRecord{}
	add := func(p *crawler.PageRecord) *crawler.PageRecord {
		pages[p.URL] = p
		return p
	}
	hyper := func(url, anchor string) parse.Link {
		return parse.Link{Type: parse.Hyperlink, URL: url, Anchor: anchor}
	}

	// --- response codes ---
	add(&crawler.PageRecord{URL: ks + "/err", Scope: "internal", State: crawler.StateError, FetchError: "timeout"})
	notFound := add(covPage(ks + "/404"))
	notFound.StatusCode, notFound.Status, notFound.Indexable, notFound.Facts = 404, "Not Found", false, nil
	srvErr := add(covPage(ks + "/500"))
	srvErr.StatusCode, srvErr.Status, srvErr.Indexable, srvErr.Facts = 500, "Internal Server Error", false, nil
	add(&crawler.PageRecord{URL: ks + "/blocked", Scope: "internal", State: crawler.StateBlockedRobots, MatchedRobotsLine: 2})
	add(covRedirect(ks+"/301", ks+"/404")) // internal_redirect + redirect_broken
	meta := add(covPage(ks + "/metarefresh"))
	meta.RedirectType, meta.RedirectURL = "meta_refresh", ks+"/ok"
	add(&crawler.PageRecord{URL: "https://ext.ex/404", Scope: "external", State: crawler.StateCrawled, StatusCode: 404})
	add(&crawler.PageRecord{URL: "https://ext.ex/500", Scope: "external", State: crawler.StateCrawled, StatusCode: 500})
	add(&crawler.PageRecord{URL: "https://ext.ex/err", Scope: "external", State: crawler.StateError, FetchError: "refused"})
	add(covRedirect(ks+"/r1", ks+"/r2")) // 2-hop chain
	add(covRedirect(ks+"/r2", ks+"/r3"))
	add(covPage(ks + "/r3"))
	add(covRedirect(ks+"/l1", ks+"/l2")) // loop
	add(covRedirect(ks+"/l2", ks+"/l1"))
	add(covPage(ks + "/ok"))
	add(covPage(ks + "/ok2"))

	// --- security ---
	sec := add(covPage(ks + "/sec"))
	sec.Headers = map[string]string{"Set-Cookie": "session=abc; HttpOnly"}
	sec.Facts = &parse.Facts{
		Links: []parse.Link{
			{Type: parse.Image, URL: "http://ks.ex/mixed.png", Alt: "mixed", Width: "10", Height: "10"},
			{Type: parse.CSS, URL: "https://cdn.ex/s.css", PathType: "protocol_relative"},
			{Type: parse.Hyperlink, URL: "https://other.ex/x", Anchor: "elsewhere", Target: "_blank"},
			hyper("http://localhost:3000/dev", "dev link"),
		},
		Forms: []parse.Form{{Action: "http://ks.ex/submit"}},
	}
	httpForm := add(covPage("http://ks.ex/httpform"))
	httpForm.Facts = &parse.Facts{Forms: []parse.Form{{Action: "https://ks.ex/x"}}}

	// --- url checks (facts-free records exercise the URL rules alone) ---
	for _, u := range []string{
		ks + "/Upper", ks + "/under_score", ks + "/café", ks + "/pp?utm_source=x",
		ks + "/a//b", ks + "/sp%20ace", ks + "/rep/rep/x",
		ks + "/" + strings.Repeat("long/", 30),
	} {
		p := add(covPage(u))
		p.Facts = nil
	}

	// --- titles / descriptions / headings ---
	add(covPage(ks + "/t-missing")) // empty facts: every "missing" check fires
	for i, u := range []string{ks + "/t-dup1", ks + "/t-dup2"} {
		p := add(covPage(u))
		p.Facts = &parse.Facts{
			Titles:       []string{"Duplicate Title Coverage Page Here"},
			Descriptions: []string{"A duplicated meta description that is comfortably over the seventy character minimum threshold."},
			H1s:          []string{"dup heading"}, H2s: []string{"dup subheading"},
			HeadingLevels: []int{1, 2},
			Hash:          "dup-hash",
		}
		_ = i
	}
	long := add(covPage(ks + "/t-long"))
	long.Facts = &parse.Facts{
		Titles:        []string{strings.Repeat("long title ", 7)}, // 77 chars
		Descriptions:  []string{strings.Repeat("long desc ", 17)}, // 170 chars
		H1s:           []string{strings.Repeat("long h1 ", 10)},   // 80 chars
		H2s:           []string{strings.Repeat("long h2 ", 10)},   // 80 chars
		HeadingLevels: []int{1, 2},
	}
	short := add(covPage(ks + "/t-short"))
	short.Facts = &parse.Facts{Titles: []string{"Tiny"}, Descriptions: []string{"short"}}
	pxWide := add(covPage(ks + "/t-px-wide"))
	pxWide.Facts = &parse.Facts{
		Titles:       []string{strings.Repeat("W", 55)},
		Descriptions: []string{strings.Repeat("W", 100)},
	}
	pxNarrow := add(covPage(ks + "/t-px-narrow"))
	pxNarrow.Facts = &parse.Facts{
		Titles:       []string{strings.Repeat("l", 40)},
		Descriptions: []string{strings.Repeat("l", 130)},
	}
	multi := add(covPage(ks + "/t-multi"))
	multi.Facts = &parse.Facts{
		Titles: []string{"A first title of a sensible size", "second title"}, TitlesOutsideHead: 1,
		Descriptions: []string{
			"The first description, long enough to clear the seventy character minimum threshold for sure.",
			"second description",
		},
		DescriptionsOutsideHead: 1,
		Keywords:                []string{"a", "b"},
		H1s:                     []string{"one", "two"}, HeadingLevels: []int{2, 1},
		H2s: []string{"x", "y"},
	}
	same := add(covPage(ks + "/t-same"))
	same.Facts = &parse.Facts{Titles: []string{"Same Text Here On Title And H1"}, H1s: []string{"same text here on title and h1"}, HeadingLevels: []int{1}}
	skipped := add(covPage(ks + "/t-skipped")) // h1 > h3 > h2: first h2 follows a deeper heading
	skipped.Facts = &parse.Facts{
		Titles:       []string{"A Page With Non-Sequential Headings"},
		Descriptions: []string{"This page exists to exercise the non-sequential h2 check with a heading order of h1, h3, h2."},
		H1s:          []string{"top heading"}, H2s: []string{"late subheading"},
		HeadingLevels: []int{1, 3, 2},
	}

	// --- content ---
	thin := add(covPage(ks + "/c-thin"))
	thin.Facts = &parse.Facts{WordCount: 50, ContentText: "lorem ipsum sadly page not found", Flesch: 10}
	diff := add(covPage(ks + "/c-diff"))
	diff.Facts = &parse.Facts{WordCount: 300, Flesch: 40, ContentText: "complicated prose"}

	// --- images ---
	imgs := add(covPage(ks + "/imgs"))
	imgs.Facts = &parse.Facts{Links: []parse.Link{
		{Type: parse.Image, URL: ks + "/noalt.png"},
		{Type: parse.Image, URL: ks + "/longalt.png", Alt: strings.Repeat("alt ", 30), Width: "10", Height: "10"},
		{Type: parse.Image, URL: ks + "/big.png", Alt: "big image", Width: "10", Height: "10"},
	}}
	big := add(covPage(ks + "/big.png"))
	big.ContentType, big.Size, big.Facts = "image/png", 200*1024, nil

	// --- canonicals ---
	cnMulti := add(covPage(ks + "/cn-multi"))
	cnMulti.Facts = &parse.Facts{
		CanonicalHTML: []string{ks + "/x", ks + "/y"}, CanonicalOutsideHead: 1,
		Links: []parse.Link{{Type: parse.Canonical, Raw: "/x", URL: ks + "/x", PathType: "root_relative"}},
	}
	cnIsed := add(covPage(ks + "/cn-ised"))
	cnIsed.Indexable, cnIsed.IndexabilityStatus = false, "Canonicalised"
	cnIsed.Facts = &parse.Facts{CanonicalHTML: []string{ks + "/ok"}}
	cnToRedirect := add(covPage(ks + "/cn-to-redirect"))
	cnToRedirect.Facts = &parse.Facts{CanonicalHTML: []string{ks + "/301"}}
	add(func() *crawler.PageRecord { // canonical chain c1 -> c2 -> c3
		p := covPage(ks + "/cc1")
		p.Facts = &parse.Facts{CanonicalHTML: []string{ks + "/cc2"}}
		return p
	}())
	cc2 := add(covPage(ks + "/cc2"))
	cc2.Facts = &parse.Facts{CanonicalHTML: []string{ks + "/cc3"}}
	add(covPage(ks + "/cc3"))
	cuRef := add(covPage(ks + "/cu-ref"))
	cuRef.Facts = &parse.Facts{CanonicalHTML: []string{ks + "/cu-target"}}
	add(covPage(ks + "/cu-target")) // nothing hyperlinks it -> canonical_unlinked

	// --- directives ---
	dir := add(covPage(ks + "/dir"))
	dir.Facts = &parse.Facts{MetaRobots: []string{"noindex, nofollow"}, XRobotsTag: []string{"none"},
		MetaRobotsOutsideHead: 1}

	// --- links ---
	manyInt := add(covPage(ks + "/lk-many-int"))
	for i := 0; i < 1001; i++ {
		manyInt.Facts.Links = append(manyInt.Facts.Links, hyper(fmt.Sprintf("%s/gen/%d", ks, i), "generated"))
	}
	manyExt := add(covPage(ks + "/lk-many-ext"))
	for i := 0; i < 101; i++ {
		manyExt.Facts.Links = append(manyExt.Facts.Links, hyper(fmt.Sprintf("https://o%d.ex/", i), "outbound"))
	}
	assorted := add(covPage(ks + "/lk-assorted"))
	assorted.Depth = 9
	assorted.Facts = &parse.Facts{Links: []parse.Link{
		{Type: parse.Hyperlink, URL: ks + "/ok", Anchor: "fine anchor", Nofollow: true},
		hyper(ks+"/ok2", "click here"),
		{Type: parse.Hyperlink, URL: ks + "/ok"},
	}}
	toBad := add(covPage(ks + "/lk-to-bad"))
	toBad.Facts = &parse.Facts{Links: []parse.Link{
		hyper(ks+"/301", "moved page"), hyper(ks+"/404", "gone page"),
	}}
	nfSrc := add(covPage(ks + "/lk-nf-src"))
	nfSrc.Facts = &parse.Facts{Links: []parse.Link{
		{Type: parse.Hyperlink, URL: ks + "/lk-nf-target", Anchor: "target page", Nofollow: true},
	}}
	add(covPage(ks + "/lk-nf-target"))
	niSrc := add(covPage(ks + "/lk-ni-src"))
	niSrc.Indexable, niSrc.IndexabilityStatus = false, "Noindex"
	niSrc.Facts = &parse.Facts{Links: []parse.Link{hyper(ks+"/lk-ni-target", "target page")}}
	add(covPage(ks + "/lk-ni-target"))

	// --- hreflang ---
	hlEn := add(covPage(ks + "/hl-en"))
	hlEn.Facts = &parse.Facts{HreflangHTML: []parse.Hreflang{
		{Lang: "en", URL: ks + "/hl-en"},
		{Lang: "de", URL: ks + "/hl-de"},
		{Lang: "fr", URL: ks + "/hl-fr"},
		{Lang: "x-default", URL: ks + "/hl-en"},
		{Lang: "zz", URL: ks + "/hl-en"},
		{Lang: "es", URL: ks + "/hl-es404"},
	}}
	hlDe := add(covPage(ks + "/hl-de"))
	hlDe.Facts = &parse.Facts{HreflangHTML: []parse.Hreflang{
		{Lang: "de", URL: ks + "/hl-de"},
		{Lang: "en", URL: ks + "/hl-en"},
	}}
	// fr annotates only de (not itself, not en): en's fr-link gets no return
	// link, and fr itself lacks a self reference and an x-default
	hlFr := add(covPage(ks + "/hl-fr"))
	hlFr.Facts = &parse.Facts{HreflangHTML: []parse.Hreflang{
		{Lang: "de", URL: ks + "/hl-de"},
	}}
	es404 := add(covPage(ks + "/hl-es404"))
	es404.StatusCode, es404.Indexable, es404.Facts = 404, false, nil
	stray := add(covPage(ks + "/hl-stray"))
	stray.Facts = &parse.Facts{HreflangOutsideHead: 1}

	// --- pagination ---
	pg1 := add(covPage(ks + "/pg1"))
	pg1.Facts = &parse.Facts{NextHTML: []string{ks + "/pg2", ks + "/pg404"}}
	add(covPage(ks + "/pg2")) // no rel=prev back -> sequence error
	pg404 := add(covPage(ks + "/pg404"))
	pg404.StatusCode, pg404.Indexable, pg404.Facts = 404, false, nil

	// --- sitemaps ---
	add(covPage(ks + "/sm-orphan")) // depth 0, no inlinks, listed below
	smNoindex := add(covPage(ks + "/sm-noindex"))
	smNoindex.Indexable, smNoindex.IndexabilityStatus = false, "Noindex"
	add(covPage(ks + "/sm-multi"))
	sitemaps := SitemapIndex{
		ks + "/sm-orphan":  {ks + "/sitemap.xml"},
		ks + "/sm-noindex": {ks + "/sitemap.xml"},
		ks + "/sm-multi":   {ks + "/sitemap.xml", ks + "/sitemap2.xml"},
	}

	// --- structured data ---
	sd := add(covPage(ks + "/sd"))
	sd.StructuredData = &structured.PageData{
		ParseErrors: []string{"bad json-ld"},
		Errors:      []string{"Product: missing name"},
		Warnings:    []string{"Product: missing image"},
	}

	// --- javascript rendering diff ---
	js := add(covPage(ks + "/js"))
	js.JSDiff = &crawler.JSDiff{
		NoindexOnlyRaw: true, CanonicalChanged: true, RenderedCanonical: ks + "/x",
		TitleChanged: true, RenderedTitle: "rendered", H1Changed: true,
		JSLinks: 2, ConsoleErrors: []string{"boom"},
	}

	// --- validation ---
	vd := add(covPage(ks + "/vd"))
	vd.Size = 3 * 1024 * 1024
	vd.Facts = &parse.Facts{Head: parse.HeadValidity{
		MissingHead: true, MultipleHead: true, MissingBody: true, MultipleBody: true,
		BodyBeforeHTML: true, HeadNotFirst: true, InvalidElementsInHead: []string{"div"},
	}}

	// --- AMP ---
	ampBad := add(covPage(ks + "/amp-bad"))
	ampBad.Facts = &parse.Facts{IsAMP: true}
	ampIdx := add(covPage(ks + "/amp-idx"))
	ampIdx.Facts = &parse.Facts{
		IsAMP: true, HasViewport: true, HasCharset: true, HasAMPScript: true,
		CanonicalHTML: []string{ks + "/desk"},
	}
	desk := add(covPage(ks + "/desk"))
	desk.Facts = &parse.Facts{AMPLinks: []string{ks + "/amp-bad"}}

	// --- near duplicates (one changed word in 400 keeps the minhash
	// estimate comfortably above the 90% threshold) ---
	words := make([]string, 400)
	for i := range words {
		words[i] = fmt.Sprintf("word%d", i)
	}
	ndText := strings.Join(words, " ")
	nd1 := add(covPage(ks + "/nd1"))
	nd1.Facts = &parse.Facts{ContentText: ndText, WordCount: 400, Hash: "nd-1", Flesch: 60}
	words[200] = "changed"
	nd2 := add(covPage(ks + "/nd2"))
	nd2.Facts = &parse.Facts{ContentText: strings.Join(words, " "), WordCount: 400, Hash: "nd-2", Flesch: 60}

	return pages, sitemaps
}

// healthyPages is the negative fixture: a fully healthy two-page site that
// must trigger NOTHING (the "one fixture that doesn't" half of DESIGN.md §6).
func healthyPages() map[string]*crawler.PageRecord {
	const (
		one = "https://healthy.ex/"
		two = "https://healthy.ex/second"
	)
	headers := func() map[string]string {
		return map[string]string{
			"Content-Type":              "text/html; charset=utf-8",
			"Strict-Transport-Security": "max-age=63072000",
			"Content-Security-Policy":   "default-src 'self'",
			"X-Content-Type-Options":    "nosniff",
			"X-Frame-Options":           "DENY",
			"Referrer-Policy":           "strict-origin-when-cross-origin",
			"Set-Cookie":                "session=abc; Secure; HttpOnly",
		}
	}
	mk := func(url, title, desc, h1, hash, other, anchor string, depth int) *crawler.PageRecord {
		p := covPage(url)
		p.ContentType = "text/html; charset=utf-8"
		p.Depth = depth
		p.Headers = headers()
		p.Facts = &parse.Facts{
			Titles:        []string{title},
			Descriptions:  []string{desc},
			H1s:           []string{h1},
			H2s:           []string{h1 + " subheading"},
			HeadingLevels: []int{1, 2},
			HasViewport:   true, HasCharset: true, Lang: "en",
			CanonicalHTML: []string{url},
			WordCount:     500, Flesch: 65,
			ContentText: "plenty of perfectly readable healthy text that mentions nothing alarming at all",
			Hash:        hash,
			Links: []parse.Link{
				{Type: parse.Canonical, Raw: url, URL: url, PathType: "absolute"},
				{Type: parse.Hyperlink, URL: other, Anchor: anchor},
				{Type: parse.Image, URL: "https://healthy.ex/img.png", Alt: "a healthy image", Width: "100", Height: "80"},
			},
		}
		return p
	}
	a := mk(one, "A Perfectly Healthy Example Page", // 32 chars, ~280px
		"This page demonstrates a fully healthy document so the coverage meta test can pin a zero issue baseline for the catalogue.",
		"Healthy page heading", "healthy-hash-1", two, "the second healthy page guide", 0)
	b := mk(two, "The Second Healthy Example Page Title", // 37 chars
		"The second healthy document keeps every threshold satisfied so the negative fixture requirement of the design holds true.",
		"Second healthy heading", "healthy-hash-2", one, "back to the healthy home page", 1)
	return toMap(a, b)
}

func TestCatalogueFixtureCoverage(t *testing.T) {
	pages, sitemaps := kitchenSink()
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Resources.Images.Store = true           // image checks are storage-gated
	cfg.Links.External.Store = true             // high-external-outlinks is storage-gated
	cfg.Extraction.StructuredData.JSONLD = true // structured_missing needs extraction on

	triggered := map[string]bool{}
	for _, o := range issues.Evaluate(pages, cfg) {
		triggered[o.IssueID] = true
	}
	for _, o := range Run(pages, sitemaps, cfg).Occurrences {
		triggered[o.IssueID] = true
	}

	for _, def := range issues.Catalogue() {
		if !triggered[def.ID] {
			t.Errorf("no fixture triggers %s (%s/%s) — extend kitchenSink alongside the catalogue", def.ID, def.Tab, def.Name)
		}
	}
	for id := range triggered {
		if _, ok := issues.Lookup(id); !ok {
			t.Errorf("fixtures trigger %s which is not in the catalogue", id)
		}
	}
}

func TestHealthySiteTriggersNothing(t *testing.T) {
	pages := healthyPages()
	cfg := config.Default()

	for _, o := range issues.Evaluate(pages, cfg) {
		t.Errorf("healthy page %s unexpectedly has %s (%s)", o.URL, o.IssueID, o.Detail)
	}
	for _, o := range Run(pages, nil, cfg).Occurrences {
		t.Errorf("healthy page %s unexpectedly has analysis issue %s (%s)", o.URL, o.IssueID, o.Detail)
	}
}
