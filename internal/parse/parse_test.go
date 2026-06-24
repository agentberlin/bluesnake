package parse

import (
	"net/http"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func parseHTML(t *testing.T, pageURL, body string, header http.Header, mutate func(*config.Config)) *Facts {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	return Parse(pageURL, []byte(body), header, cfg)
}

func findLink(facts *Facts, typ LinkType, url string) *Link {
	for i := range facts.Links {
		if facts.Links[i].Type == typ && facts.Links[i].URL == url {
			return &facts.Links[i]
		}
	}
	return nil
}

func TestElementExtraction(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html lang="en"><head>
			<title> First   title </title>
			<title>Second</title>
			<meta name="Description" content="desc one">
			<meta name="keywords" content="k1, k2">
			<meta name="robots" content="noindex">
			<link rel="canonical" href="/canon">
		</head><body>
			<h2>Jumped</h2><h1>Main <span>heading</span></h1><h3>Deep</h3>
		</body></html>`, nil, nil)

	if len(f.Titles) != 2 || f.Titles[0] != "First title" {
		t.Errorf("titles = %v", f.Titles)
	}
	if len(f.Descriptions) != 1 || f.Descriptions[0] != "desc one" {
		t.Errorf("descriptions = %v (meta name matching must be case-insensitive)", f.Descriptions)
	}
	if len(f.Keywords) != 1 {
		t.Errorf("keywords = %v", f.Keywords)
	}
	if len(f.MetaRobots) != 1 || f.MetaRobots[0] != "noindex" {
		t.Errorf("meta robots = %v", f.MetaRobots)
	}
	if len(f.CanonicalHTML) != 1 || f.CanonicalHTML[0] != "https://ex.com/canon" {
		t.Errorf("canonical = %v", f.CanonicalHTML)
	}
	if f.Lang != "en" {
		t.Errorf("lang = %q", f.Lang)
	}
	if len(f.H1s) != 1 || f.H1s[0] != "Main heading" {
		t.Errorf("h1s = %v", f.H1s)
	}
	if want := []int{2, 1, 3}; len(f.HeadingLevels) != 3 || f.HeadingLevels[0] != want[0] || f.HeadingLevels[1] != want[1] || f.HeadingLevels[2] != want[2] {
		t.Errorf("heading levels = %v, want %v", f.HeadingLevels, want)
	}
	if f.TitlesOutsideHead != 0 {
		t.Errorf("titles outside head = %d", f.TitlesOutsideHead)
	}
}

func TestOutsideHeadDetection(t *testing.T) {
	// an invalid element ends the head for Google; the html parser moves the
	// rest to body, so the late title is detected as outside the head
	f := parseHTML(t, "https://ex.com/p", `
		<html><head><div>x</div><title>Late</title></head><body></body></html>`, nil, nil)
	if f.TitlesOutsideHead != 1 {
		t.Errorf("titles outside head = %d, want 1", f.TitlesOutsideHead)
	}
	if len(f.Head.InvalidElementsInHead) == 0 {
		t.Error("invalid elements in head must be reported")
	}

	// a <header> element in body must not be confused with <head>
	f = parseHTML(t, "https://ex.com/p", `
		<html><head><title>ok</title></head><body><header><h1>x</h1></header></body></html>`, nil, nil)
	if f.TitlesOutsideHead != 0 {
		t.Errorf("title inside real head flagged outside, count = %d", f.TitlesOutsideHead)
	}
}

func TestHeadValidity(t *testing.T) {
	tests := []struct {
		name  string
		html  string
		check func(HeadValidity) bool
	}{
		{"missing head", "<html><body></body></html>", func(h HeadValidity) bool { return h.MissingHead }},
		{"multiple heads", "<html><head></head><head></head><body></body></html>", func(h HeadValidity) bool { return h.MultipleHead }},
		{"missing body", "<html><head></head></html>", func(h HeadValidity) bool { return h.MissingBody }},
		{"multiple bodies", "<html><head></head><body></body><body></body></html>", func(h HeadValidity) bool { return h.MultipleBody }},
		{"body before html", "<body></body><html></html>", func(h HeadValidity) bool { return h.BodyBeforeHTML }},
		{"head not first", "<html><div></div><head></head><body></body></html>", func(h HeadValidity) bool { return h.HeadNotFirst }},
		{"clean page ok", "<html><head><title>t</title></head><body></body></html>", func(h HeadValidity) bool {
			return !h.MissingHead && !h.MultipleHead && !h.MissingBody && !h.MultipleBody &&
				!h.BodyBeforeHTML && !h.HeadNotFirst && len(h.InvalidElementsInHead) == 0
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hv := headChecks([]byte(tt.html))
			if !tt.check(hv) {
				t.Errorf("check failed: %+v", hv)
			}
		})
	}
}

func TestLinkHeaderParsing(t *testing.T) {
	h := http.Header{}
	h.Add("Link", `<https://ex.com/canon>; rel="canonical", </de>; rel="alternate"; hreflang="de"`)
	h.Add("Link", `<?page=3>; rel="next"`)
	h.Add("X-Robots-Tag", "noarchive")
	h.Add("X-Robots-Tag", "nosnippet")

	f := parseHTML(t, "https://ex.com/list?page=2", "<html><body></body></html>", h, nil)

	if len(f.CanonicalHTTP) != 1 || f.CanonicalHTTP[0] != "https://ex.com/canon" {
		t.Errorf("http canonical = %v", f.CanonicalHTTP)
	}
	if len(f.HreflangHTTP) != 1 || f.HreflangHTTP[0].Lang != "de" || f.HreflangHTTP[0].URL != "https://ex.com/de" {
		t.Errorf("http hreflang = %v", f.HreflangHTTP)
	}
	if len(f.NextHTTP) != 1 || f.NextHTTP[0] != "https://ex.com/list?page=3" {
		t.Errorf("http next = %v", f.NextHTTP)
	}
	if len(f.XRobotsTag) != 2 {
		t.Errorf("x-robots = %v", f.XRobotsTag)
	}
}

func TestMetaRefresh(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{`0;url=/new`, "https://ex.com/new"},
		{`5; URL='/quoted'`, "https://ex.com/quoted"},
		{`5`, "https://ex.com/old"}, // bare delay refreshes self
	}
	for _, tt := range tests {
		f := parseHTML(t, "https://ex.com/old",
			`<html><head><meta http-equiv="refresh" content="`+tt.content+`"></head><body></body></html>`, nil, nil)
		if f.MetaRefreshURL != tt.want {
			t.Errorf("content %q: target = %q, want %q", tt.content, f.MetaRefreshURL, tt.want)
		}
	}
}

func TestBaseHref(t *testing.T) {
	f := parseHTML(t, "https://ex.com/deep/dir/p", `
		<html><head><base href="/other/"></head>
		<body><a href="page">x</a></body></html>`, nil, nil)
	if findLink(f, Hyperlink, "https://ex.com/other/page") == nil {
		t.Errorf("base href not applied, links: %+v", f.Links)
	}
}

// Screaming-Frog-style link paths: //body-rooted, [@id] when present, 1-based
// positional [k] among same-tag siblings (only when >1), bare for singletons.
func TestElemPathSFStyle(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html><body>
			<header><nav><a id="home" href="/a">a</a></nav></header>
			<main id="mn">
				<div><a href="/d1">d1</a></div>
				<div><a href="/d2">d2</a></div>
			</main>
		</body></html>`, nil, nil)
	check := func(dst, want string) {
		t.Helper()
		l := findLink(f, Hyperlink, dst)
		if l == nil {
			t.Fatalf("missing link %s", dst)
		}
		if l.ElemPath != want {
			t.Errorf("%s elem path = %q, want %q", dst, l.ElemPath, want)
		}
	}
	check("https://ex.com/a", "//body/header/nav/a")   // singletons -> bare (no @id qualifier)
	check("https://ex.com/d1", "//body/main/div[1]/a") // 1st of 2 sibling divs
	check("https://ex.com/d2", "//body/main/div[2]/a") // 2nd of 2 sibling divs
}

// TestSFElemPathHead pins that head links (canonical/stylesheet/hreflang/amp)
// are //head-rooted by the same positional scheme as body links — the walk
// stops at <head> and excludes <html>, mirroring the //body rooting.
func TestSFElemPathHead(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html><head>
			<link rel="stylesheet" href="/a.css">
			<link rel="canonical" href="/canon">
		</head><body></body></html>`, nil, nil)
	l := findLink(f, Canonical, "https://ex.com/canon")
	if l == nil {
		t.Fatal("missing canonical link")
	}
	// two <link> siblings in <head> -> 1-based same-tag index; canonical is 2nd
	if l.ElemPath != "//head/link[2]" {
		t.Errorf("canonical elem path = %q, want //head/link[2]", l.ElemPath)
	}
}

// Element-text EXTRACTION joins same-tag-adjacent inline elements with NO
// synthetic space: SF extracts `<span>Run</span><span>Execute</span>` as
// "RunExecute" (probe + infisical.com's three zero-whitespace-adjacent <span>s
// in its <h1> → 129 chars, no spaces). This is the opposite of word COUNTING,
// which breaks at same-tag adjacency (content_parity_test.go).
func TestHeadingSameTagAdjacentJoin(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><h1><span>Run</span><span>Execute</span></h1>`+
			`<h2><span>AaA</span><span>BbB</span><span>CcC</span></h2></body></html>`, nil, nil)
	if len(f.H1s) != 1 || f.H1s[0] != "RunExecute" {
		t.Errorf("H1s = %q, want [RunExecute]", f.H1s)
	}
	if len(f.H2s) != 1 || f.H2s[0] != "AaABbBCcC" {
		t.Errorf("H2s = %q, want [AaABbBCcC]", f.H2s)
	}
}

func TestAnchorEdgeData(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html><body>
			<nav><a href="/about">About</a></nav>
			<a href="/nf" rel="ugc">User link</a>
			<a href="/img"><img src="/i.jpg" alt="pic alt"></a>
		</body></html>`, nil, nil)

	about := findLink(f, Hyperlink, "https://ex.com/about")
	if about == nil || about.Position != "nav" || about.Anchor != "About" {
		t.Errorf("about link = %+v", about)
	}
	if !strings.HasSuffix(about.ElemPath, "/nav/a") {
		t.Errorf("elem path = %q", about.ElemPath)
	}
	nf := findLink(f, Hyperlink, "https://ex.com/nf")
	if nf == nil || !nf.Nofollow {
		t.Error("rel=ugc must be nofollow")
	}
	img := findLink(f, Hyperlink, "https://ex.com/img")
	if img == nil || img.Alt != "pic alt" {
		t.Errorf("hyperlinked image alt = %+v", img)
	}
}

func TestSrcset(t *testing.T) {
	html := `<html><body><img src="/a.jpg" srcset="/a-2x.jpg 2x, /a-3x.jpg 3x"></body></html>`
	f := parseHTML(t, "https://ex.com/p", html, nil, nil)
	count := 0
	for _, l := range f.Links {
		if l.Type == Image {
			count++
		}
	}
	if count != 1 {
		t.Errorf("srcset off: %d image links, want 1", count)
	}
	f = parseHTML(t, "https://ex.com/p", html, nil, func(c *config.Config) { c.Advanced.ExtractSrcset = true })
	count = 0
	for _, l := range f.Links {
		if l.Type == Image {
			count++
		}
	}
	if count != 3 {
		t.Errorf("srcset on: %d image links, want 3", count)
	}
}

func TestContentArea(t *testing.T) {
	html := `<html><body>
		<nav>nav words here</nav>
		<div class="ads">buy this now</div>
		<div id="main">real content words</div>
		<footer>footer words</footer>
	</body></html>`

	t.Run("default excludes nav and footer", func(t *testing.T) {
		f := parseHTML(t, "https://ex.com/p", html, nil, nil)
		if f.WordCount != 6 { // "buy this now real content words"
			t.Errorf("word count = %d (%q)", f.WordCount, f.ContentText)
		}
	})

	t.Run("exclude by class", func(t *testing.T) {
		f := parseHTML(t, "https://ex.com/p", html, nil, func(c *config.Config) {
			c.Content.Area.ExcludeClasses = []string{"ads"}
		})
		if f.WordCount != 3 {
			t.Errorf("word count = %d (%q)", f.WordCount, f.ContentText)
		}
	})

	t.Run("include by id", func(t *testing.T) {
		f := parseHTML(t, "https://ex.com/p", html, nil, func(c *config.Config) {
			c.Content.Area.IncludeIDs = []string{"main"}
		})
		if f.WordCount != 3 || f.ContentText != "real content words" {
			t.Errorf("word count = %d (%q)", f.WordCount, f.ContentText)
		}
	})

	t.Run("script and style never count", func(t *testing.T) {
		f := parseHTML(t, "https://ex.com/p",
			`<html><body><p>two words</p><script>var x = 1;</script><style>.a{}</style></body></html>`, nil, nil)
		if f.WordCount != 2 {
			t.Errorf("word count = %d", f.WordCount)
		}
	})
}

func TestReadabilityMetrics(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><p>The cat sat on the mat. The dog ran fast.</p></body></html>`, nil, nil)
	if f.AvgWordsPerSentence != 5 {
		t.Errorf("avg words/sentence = %v, want 5", f.AvgWordsPerSentence)
	}
	if f.Flesch < 90 { // trivially easy text scores high
		t.Errorf("flesch = %v, want > 90", f.Flesch)
	}
}

func TestHashStability(t *testing.T) {
	a := parseHTML(t, "https://ex.com/a", "<html><body>same</body></html>", nil, nil)
	b := parseHTML(t, "https://ex.com/b", "<html><body>same</body></html>", nil, nil)
	c := parseHTML(t, "https://ex.com/c", "<html><body>diff</body></html>", nil, nil)
	if a.Hash != b.Hash {
		t.Error("identical bodies must hash identically")
	}
	if a.Hash == c.Hash {
		t.Error("different bodies must hash differently")
	}
}

func TestForms(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html><body>
			<form action="http://insecure.ex.com/submit"></form>
			<form></form>
		</body></html>`, nil, nil)
	if len(f.Forms) != 2 {
		t.Fatalf("forms = %+v", f.Forms)
	}
	if f.Forms[0].Action != "http://insecure.ex.com/submit" {
		t.Errorf("form action = %q", f.Forms[0].Action)
	}
	if f.Forms[1].Action != "https://ex.com/p" {
		t.Errorf("empty form action must resolve to the page, got %q", f.Forms[1].Action)
	}
}

func TestAMPDetection(t *testing.T) {
	f := parseHTML(t, "https://ex.com/amp", `<html amp><head></head><body></body></html>`, nil, nil)
	if !f.IsAMP {
		t.Error("html amp attribute must be detected")
	}
}

func TestUncrawlableLinks(t *testing.T) {
	html := `<html><body><span href="/s">x</span><a href="javascript:f()">y</a></body></html>`
	f := parseHTML(t, "https://ex.com/p", html, nil, nil)
	if len(f.Links) != 0 {
		t.Errorf("uncrawlable storage off: links = %+v", f.Links)
	}
	f = parseHTML(t, "https://ex.com/p", html, nil, func(c *config.Config) { c.Links.Uncrawlable.Store = true })
	count := 0
	for _, l := range f.Links {
		if l.Type == Uncrawlable {
			count++
		}
	}
	if count != 2 {
		t.Errorf("uncrawlable count = %d, want 2", count)
	}
}

// Zero-width characters injected by heading-anchor generators (Mintlify et
// al) are stripped from extracted text — Screaming Frog's H2s never carry
// them (measured on greptile.com/docs, 2026-06-12).
func TestZeroWidthCharsStripped(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p",
		"<html><body><h2>\u200b Filters</h2><h1>A\ufeffB</h1></body></html>", nil, nil)
	if len(f.H2s) != 1 || f.H2s[0] != "Filters" {
		t.Errorf("H2s = %q, want [Filters]", f.H2s)
	}
	if len(f.H1s) != 1 || f.H1s[0] != "AB" {
		t.Errorf("H1s = %q, want [AB]", f.H1s)
	}
}

// Link-position rules are Screaming Frog's default search terms; the head
// rule is "/head/" so links inside <header> never match it (yonedalabs.com
// classified header links as "head" with the old "/head" term).
func TestLinkPositionHeaderNotHead(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `
		<html><body>
			<header><a href="/from-header">x</a></header>
			<aside><a href="/from-aside">z</a></aside>
			<footer><a href="/from-footer">y</a></footer>
		</body></html>`, nil, nil)
	if l := findLink(f, Hyperlink, "https://ex.com/from-header"); l == nil || l.Position != "header" {
		t.Errorf("header link position = %+v, want header", l)
	}
	if l := findLink(f, Hyperlink, "https://ex.com/from-footer"); l == nil || l.Position != "footer" {
		t.Errorf("footer link position = %+v, want footer", l)
	}
	// SF labels the <aside> region "Aside" (its decoded default config names
	// position #4 "Aside"); bluesnake must emit "aside", not "sidebar", so
	// parity diffs of Link Position match. braintrust.dev: 10100 such links.
	if l := findLink(f, Hyperlink, "https://ex.com/from-aside"); l == nil || l.Position != "aside" {
		t.Errorf("aside link position = %+v, want aside", l)
	}
}

// Link position is also classified by a region's class/id, not just its tag
// (R2). Screaming Frog derives Link Position from a substring search of its
// link path, and that path carries an element's id or single-token class as an
// XPath qualifier (div[@class='site-footer'], div[@id='footer']) — so "footer"
// matches a <div class="site-footer"> exactly as it matches a <footer> tag.
// bluesnake mirrors SF's matching: SF's term set, CASE-SENSITIVE substring,
// and only a SINGLE-token class participates (a multi-class "col footer-col"
// is not a footer in SF). It deliberately does NOT copy one SF behaviour: SF
// only emits the class/id qualifier to disambiguate same-tag siblings, so SF
// classifies the SAME <div class="footer"> as Footer when it has a sibling div
// but Content when it is an only child — a proven artifact of SF's path
// disambiguation, not semantics. bluesnake reads the id/class consistently
// (sibling-independent), so a sole-child <div class="footer"> is Footer.
//
// Every "want" below except the one marked "(consistency)" was confirmed
// against Screaming Frog v24.1 (STANDARD config) on controlled probe pages.
func TestLinkPositionClassId(t *testing.T) {
	cases := []struct {
		name string
		frag string // body fragment; the asserted link is href="/<name>"
		want string
	}{
		// single-token class regions SF classifies by class
		{"class-site-footer", `<div class="site-footer"><a href="/class-site-footer">x</a></div>`, "footer"},
		{"class-footer-wrapper", `<div class="footer-wrapper"><a href="/class-footer-wrapper">x</a></div>`, "footer"},
		{"class-navbar", `<div class="navbar"><a href="/class-navbar">x</a></div>`, "nav"},
		{"class-main-navigation", `<div class="main-navigation"><a href="/class-main-navigation">x</a></div>`, "nav"},
		{"class-header", `<div class="header"><a href="/class-header">x</a></div>`, "header"},
		{"class-page-header", `<div class="page-header"><a href="/class-page-header">x</a></div>`, "header"},
		{"class-aside", `<div class="aside"><a href="/class-aside">x</a></div>`, "aside"},
		// greedy substring: "nav" matches inside "navy" (SF does this too)
		{"class-navy-greedy", `<div class="navy"><a href="/class-navy-greedy">x</a></div>`, "nav"},
		// id-based regions
		{"id-footer", `<div id="footer"><a href="/id-footer">x</a></div>`, "footer"},
		{"id-main-footer", `<div id="main-footer"><a href="/id-main-footer">x</a></div>`, "footer"},
		// not matched by SF -> bluesnake matches SF (Content)
		{"class-sidebar-no-aside", `<div class="sidebar"><a href="/class-sidebar-no-aside">x</a></div>`, "content"},
		{"role-navigation-ignored", `<div role="navigation"><a href="/role-navigation-ignored">x</a></div>`, "content"},
		{"role-contentinfo-ignored", `<div role="contentinfo"><a href="/role-contentinfo-ignored">x</a></div>`, "content"},
		{"data-attr-ignored", `<div data-section="footer"><a href="/data-attr-ignored">x</a></div>`, "content"},
		// case-sensitive: a capitalised class does not match the lowercase term
		{"class-MainFooter-case", `<div class="MainFooter"><a href="/class-MainFooter-case">x</a></div>`, "content"},
		{"class-NAVBAR-case", `<div class="NAVBAR"><a href="/class-NAVBAR-case">x</a></div>`, "content"},
		// multi-token class is dropped (matches SF)
		{"multiclass-col-footer-col", `<div class="col footer-col"><a href="/multiclass-col-footer-col">x</a></div>`, "content"},
		{"multiclass-extra-footer", `<div class="extra footer"><a href="/multiclass-extra-footer">x</a></div>`, "content"},
		{"multiclass-nav-bar-utility", `<div class="nav-bar utility"><a href="/multiclass-nav-bar-utility">x</a></div>`, "content"},
		// a single hyphenated token is kept
		{"single-footer-x", `<div class="footer-x"><a href="/single-footer-x">x</a></div>`, "footer"},
		// the anchor's OWN single-token class is read
		{"anchor-own-class", `<a class="footer-link" href="/anchor-own-class">x</a>`, "footer"},
		// rule precedence: first matching term in config order (header before footer)
		{"header-class-over-footer-tag", `<div class="header"><footer><a href="/header-class-over-footer-tag">x</a></footer></div>`, "header"},
		{"nav-over-footer-class", `<nav><div class="footer"><a href="/nav-over-footer-class">x</a></div></nav>`, "nav"},
		// a semantic tag still wins regardless of a multi-class on the tag itself
		{"footer-tag-multiclass", `<footer class="site mod"><a href="/footer-tag-multiclass">x</a></footer>`, "footer"},
		// (consistency) sole-child class="footer": SF says Content (it omits the
		// class qualifier when there is no same-tag sibling to disambiguate);
		// bluesnake reads the class regardless -> Footer (more correct).
		{"sole-child-footer", `<div class="footer"><a href="/sole-child-footer">x</a></div>`, "footer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := parseHTML(t, "https://ex.com/p", "<html><body>"+tc.frag+"</body></html>", nil, nil)
			l := findLink(f, Hyperlink, "https://ex.com/"+tc.name)
			if l == nil {
				t.Fatalf("link /%s not found", tc.name)
			}
			if l.Position != tc.want {
				t.Errorf("position(%s) = %q, want %q", tc.name, l.Position, tc.want)
			}
		})
	}
}

// Element-text EXTRACTION (anchors, titles, headings) re-introduces a word
// boundary across a block-level element but joins across inline ones, matching
// Screaming Frog's (jsoup's) block/inline classification of extracted text.
// This is distinct from word COUNTING (content.go) — they only agree on the
// genuinely-block elements; the few edge cases below were pinned with probe
// pages crawled by SF v24.1 (R10 in the crawl-comparison decision log):
//
//   - <svg>/<canvas>/<button> are BLOCK in extracted text -> a childless one
//     between two text runs gets a space ("Next Title"), even though content.go
//     lists them as inline for word counting. <svg> in particular drives the
//     real-world gap: blog "Next/Previous" cards <a><span>Next<svg/></span>
//     <span>Title</span></a> read "Next Title" in SF (173 anchors on
//     braintrust.dev), not "NextTitle".
//   - <img>/<iframe>/<map>/<dialog>/<summary>/<legend>/<area>/<datalist>/<track>
//     are INLINE -> no space ("NextTitle"). (The original R10 hypothesis that
//     <img> also separates is DISPROVEN here: only <svg> does.)
//   - Same-tag-adjacent inline siblings still join with no space (R3:
//     "RunExecute") — a replaced element between them is what re-adds the space.
func TestAnchorTextBlockInlineBoundaries(t *testing.T) {
	cases := []struct {
		name string
		path string
		frag string
		want string
	}{
		// block-level inline elements -> SF inserts a word boundary
		{"svg-spans", "/svg-spans", `<span>Next<svg width="1" height="1"></svg></span><span>Title</span>`, "Next Title"},
		{"svg-bare", "/svg-bare", `Next<svg width="1" height="1"></svg>Title`, "Next Title"},
		{"canvas", "/canvas", `Next<canvas></canvas>Title`, "Next Title"},
		{"button", "/button", `Next<button></button>Title`, "Next Title"},
		{"video", "/video", `Next<video></video>Title`, "Next Title"},
		{"br", "/br", `Next<br>Title`, "Next Title"},
		// inline elements -> SF joins with no space
		{"img", "/img", `Next<img src="/i.png" alt="">Title`, "NextTitle"},
		{"iframe", "/iframe", `Next<iframe></iframe>Title`, "NextTitle"},
		{"map", "/map", `Next<map></map>Title`, "NextTitle"},
		{"dialog", "/dialog", `Next<dialog></dialog>Title`, "NextTitle"},
		{"summary", "/summary", `Next<summary></summary>Title`, "NextTitle"},
		{"legend", "/legend", `Next<legend></legend>Title`, "NextTitle"},
		{"area", "/area", `Next<area>Title`, "NextTitle"},
		{"datalist", "/datalist", `Next<datalist></datalist>Title`, "NextTitle"},
		{"track", "/track", `Next<track>Title`, "NextTitle"},
		// R3 regression guard: same-tag-adjacent siblings join with no space
		{"r3", "/r3", `<span>Run</span><span>Execute</span>`, "RunExecute"},
	}
	var body strings.Builder
	body.WriteString("<html><body>")
	for _, c := range cases {
		body.WriteString(`<a href="` + c.path + `">` + c.frag + `</a>`)
	}
	body.WriteString("</body></html>")
	f := parseHTML(t, "https://ex.com/p", body.String(), nil, nil)
	for _, c := range cases {
		l := findLink(f, Hyperlink, "https://ex.com"+c.path)
		if l == nil {
			t.Errorf("%s: link missing", c.name)
			continue
		}
		if l.Anchor != c.want {
			t.Errorf("%s: anchor = %q, want %q", c.name, l.Anchor, c.want)
		}
	}

	// Headings extract through the same path: an inline <svg> icon separates the
	// two label runs just like in anchors.
	h := parseHTML(t, "https://ex.com/h",
		`<html><body><h1><span>Icon<svg width="1" height="1"></svg></span><span>Heading</span></h1></body></html>`, nil, nil)
	if len(h.H1s) != 1 || h.H1s[0] != "Icon Heading" {
		t.Errorf("H1s = %q, want [Icon Heading]", h.H1s)
	}
}
