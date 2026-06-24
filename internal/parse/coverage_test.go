package parse

import (
	"net/http"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// countLinks returns the number of links of a given type.
func countLinks(f *Facts, typ LinkType) int {
	n := 0
	for _, l := range f.Links {
		if l.Type == typ {
			n++
		}
	}
	return n
}

// TestIsPaginated pins the rel="prev" paginated-page signal: a page is
// "paginated" only once it declares a rel="prev" link (in HTML or an HTTP
// Link header) — page 1 of a sequence carries only rel="next" and is not.
func TestIsPaginated(t *testing.T) {
	// nil receiver is safe and false
	var nilFacts *Facts
	if nilFacts.IsPaginated() {
		t.Error("nil Facts must not be paginated")
	}

	// page 1: only rel=next -> not paginated
	p1 := parseHTML(t, "https://ex.com/p1",
		`<html><head><link rel="next" href="/p2"></head><body></body></html>`, nil, nil)
	if p1.IsPaginated() {
		t.Error("a page with only rel=next must not be paginated")
	}

	// page 2: declares rel=prev in HTML -> paginated
	p2 := parseHTML(t, "https://ex.com/p2",
		`<html><head><link rel="prev" href="/p1"><link rel="next" href="/p3"></head><body></body></html>`, nil, nil)
	if !p2.IsPaginated() {
		t.Error("a page declaring rel=prev in HTML must be paginated")
	}

	// rel=prev via the HTTP Link header also counts
	h := http.Header{}
	h.Add("Link", `</p1>; rel="prev"`)
	p2h := parseHTML(t, "https://ex.com/p2", "<html><body></body></html>", h, nil)
	if !p2h.IsPaginated() {
		t.Error("a page declaring rel=prev in the Link header must be paginated")
	}

	// rel="previous" alias also feeds PrevHTML
	prevAlias := parseHTML(t, "https://ex.com/p2",
		`<html><head><link rel="previous" href="/p1"></head><body></body></html>`, nil, nil)
	if !prevAlias.IsPaginated() || len(prevAlias.PrevHTML) != 1 {
		t.Errorf("rel=previous alias must register as prev: %+v", prevAlias.PrevHTML)
	}
}

// TestLinkElementRels exercises every rel branch of handleLinkElement:
// canonical, stylesheet, next, prev, amphtml, hreflang alternate and the
// media-only mobile alternate; plus the multi-rel and empty-href edges.
func TestLinkElementRels(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><head>
		<link rel="stylesheet" href="/main.css">
		<link rel="next" href="/p2">
		<link rel="prev" href="/p0">
		<link rel="amphtml" href="/amp">
		<link rel="alternate" hreflang="fr" href="/fr">
		<link rel="alternate" media="only screen and (max-width: 640px)" href="https://m.ex.com/p">
		<link rel="alternate">
		<link rel="canonical" href="">
	</head><body></body></html>`, nil, nil)

	if countLinks(f, CSS) != 1 {
		t.Errorf("stylesheet link = %d, want 1", countLinks(f, CSS))
	}
	if len(f.NextHTML) != 1 || f.NextHTML[0] != "https://ex.com/p2" {
		t.Errorf("next = %v", f.NextHTML)
	}
	if len(f.PrevHTML) != 1 || f.PrevHTML[0] != "https://ex.com/p0" {
		t.Errorf("prev = %v", f.PrevHTML)
	}
	if len(f.AMPLinks) != 1 || f.AMPLinks[0] != "https://ex.com/amp" {
		t.Errorf("amphtml = %v", f.AMPLinks)
	}
	if len(f.HreflangHTML) != 1 || f.HreflangHTML[0].Lang != "fr" || f.HreflangHTML[0].URL != "https://ex.com/fr" {
		t.Errorf("hreflang = %v", f.HreflangHTML)
	}
	if len(f.MobileAlternates) != 1 || f.MobileAlternates[0] != "https://m.ex.com/p" {
		t.Errorf("mobile alternate = %v", f.MobileAlternates)
	}
	// rel=alternate with neither hreflang nor media contributes nothing
	if findLink(f, MobileAlternate, "") != nil {
		t.Error("bare rel=alternate must not create a mobile alternate")
	}
	// the empty-href canonical link is dropped entirely
	if len(f.CanonicalHTML) != 0 {
		t.Errorf("empty-href canonical must be ignored, got %v", f.CanonicalHTML)
	}
}

// TestLinkElementMultiRel pins that a single <link> carrying several rel tokens
// is processed once per token.
func TestLinkElementMultiRel(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><head>
		<link rel="next stylesheet" href="/combo">
	</head><body></body></html>`, nil, nil)
	if len(f.NextHTML) != 1 {
		t.Errorf("multi-rel next not registered: %v", f.NextHTML)
	}
	if countLinks(f, CSS) != 1 {
		t.Errorf("multi-rel stylesheet not registered: %d", countLinks(f, CSS))
	}
}

// TestResourceElements exercises the script/iframe/media/source/embed/object
// branches of handleElement (each emits a typed link edge), plus the AMP
// script detection.
func TestResourceElements(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<script src="https://cdn.ampproject.org/v0.js"></script>
		<script src="/app.js"></script>
		<script>var inline = 1;</script>
		<iframe src="/frame.html"></iframe>
		<video src="/movie.mp4"></video>
		<audio src="/sound.mp3"></audio>
		<track src="/subs.vtt">
		<picture><source src="/pic.webp"></picture>
		<source src="/clip.mp4">
		<embed src="/thing.swf">
		<object data="/legacy.swf"></object>
	</body></html>`, nil, nil)

	if !f.HasAMPScript {
		t.Error("ampproject.org script must set HasAMPScript")
	}
	if countLinks(f, JS) != 2 { // ampproject + app.js; inline has no src
		t.Errorf("JS links = %d, want 2", countLinks(f, JS))
	}
	if countLinks(f, IFrame) != 1 {
		t.Errorf("iframe links = %d, want 1", countLinks(f, IFrame))
	}
	// video, audio, track, and the bare <source> (not in <picture>) are Media
	if countLinks(f, Media) != 4 {
		t.Errorf("media links = %d, want 4 (video/audio/track/source)", countLinks(f, Media))
	}
	// the <source> inside <picture> is classified as an Image
	if findLink(f, Image, "https://ex.com/pic.webp") == nil {
		t.Errorf("picture source must be an Image link: %+v", f.Links)
	}
	if countLinks(f, SWF) != 2 { // embed src + object data
		t.Errorf("SWF links = %d, want 2 (embed + object)", countLinks(f, SWF))
	}
}

// TestUncrawlableHrefOnNonCarrier covers the default branch of handleElement:
// an href on an element that is not a hyperlink carrier is stored as an
// Uncrawlable edge when the option is on, and <base href> is exempt.
func TestUncrawlableHrefOnNonCarrier(t *testing.T) {
	body := `<html><head><base href="/root/"></head><body>
		<span href="/spanlink">x</span>
		<div href="/divlink">y</div>
	</body></html>`
	// off by default: nothing stored
	off := parseHTML(t, "https://ex.com/p", body, nil, nil)
	if countLinks(off, Uncrawlable) != 0 {
		t.Errorf("uncrawlable storage off: %d edges", countLinks(off, Uncrawlable))
	}
	// on: the span and div hrefs are stored, base is exempt
	on := parseHTML(t, "https://ex.com/p", body, nil, func(c *config.Config) {
		c.Links.Uncrawlable.Store = true
	})
	if countLinks(on, Uncrawlable) != 2 {
		t.Errorf("uncrawlable on: %d edges, want 2 (base exempt)", countLinks(on, Uncrawlable))
	}
}

// TestNonCrawlableAnchorSchemes pins that mailto/tel/data/ftp anchors are
// dropped (no link, not even uncrawlable), while javascript: anchors are stored
// as uncrawlable when the option is on.
func TestNonCrawlableAnchorSchemes(t *testing.T) {
	body := `<html><body>
		<a href="mailto:a@b.com">mail</a>
		<a href="tel:+1234">call</a>
		<a href="data:text/plain,hi">data</a>
		<a href="ftp://files.ex.com/x">ftp</a>
		<a href="javascript:doThing()">js</a>
		<a href="  ">blank</a>
	</body></html>`
	f := parseHTML(t, "https://ex.com/p", body, nil, func(c *config.Config) {
		c.Links.Uncrawlable.Store = true
	})
	if countLinks(f, Hyperlink) != 0 {
		t.Errorf("scheme anchors must not become hyperlinks: %+v", f.Links)
	}
	// only the javascript: anchor is uncrawlable; mailto/tel/data/ftp and the
	// whitespace-only href are silently dropped
	if countLinks(f, Uncrawlable) != 1 {
		t.Errorf("uncrawlable count = %d, want 1 (javascript only)", countLinks(f, Uncrawlable))
	}
}

// TestMetaViewportAndCharset covers the viewport, charset and http-equiv
// content-type branches of handleMeta.
func TestMetaViewportAndCharset(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><head>
		<meta name="viewport" content="width=device-width, initial-scale=1">
		<meta charset="utf-8">
	</head><body></body></html>`, nil, nil)
	if !f.HasViewport {
		t.Error("viewport meta not detected")
	}
	if !f.HasCharset {
		t.Error("charset attribute not detected")
	}

	// charset can also come from an http-equiv content-type meta
	f = parseHTML(t, "https://ex.com/p", `<html><head>
		<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">
	</head><body></body></html>`, nil, nil)
	if !f.HasCharset {
		t.Error("http-equiv content-type must set HasCharset")
	}

	// no charset declared at all
	f = parseHTML(t, "https://ex.com/p", `<html><head><title>x</title></head><body></body></html>`, nil, nil)
	if f.HasCharset {
		t.Error("HasCharset must be false when no charset is declared")
	}
}

// TestMetaRefreshLink pins that a meta refresh with a url= target also emits a
// MetaRefreshLink edge, and that only the first refresh meta wins.
func TestMetaRefreshLink(t *testing.T) {
	f := parseHTML(t, "https://ex.com/old", `<html><head>
		<meta http-equiv="refresh" content="0; url=/new">
		<meta http-equiv="refresh" content="0; url=/second">
	</head><body></body></html>`, nil, nil)
	if f.MetaRefresh != "0; url=/new" {
		t.Errorf("first refresh must win: MetaRefresh = %q", f.MetaRefresh)
	}
	if f.MetaRefreshURL != "https://ex.com/new" {
		t.Errorf("MetaRefreshURL = %q", f.MetaRefreshURL)
	}
	l := findLink(f, MetaRefreshLink, "https://ex.com/new")
	if l == nil {
		t.Errorf("meta refresh link edge missing: %+v", f.Links)
	}
}

// TestTitleInsideSVGIgnored covers the svg-ancestor guard in handleElement:
// an SVG <title> is decorative, not the page title.
func TestTitleInsideSVGIgnored(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><head><title>Real Title</title></head>
		<body><svg><title>icon label</title></svg></body></html>`, nil, nil)
	if len(f.Titles) != 1 || f.Titles[0] != "Real Title" {
		t.Errorf("Titles = %v, want only the real <title>", f.Titles)
	}
}

// TestAMPLightningBolt covers the ⚡ attribute branch of the html element.
func TestAMPLightningBolt(t *testing.T) {
	f := parseHTML(t, "https://ex.com/amp", `<html ⚡><head></head><body></body></html>`, nil, nil)
	if !f.IsAMP {
		t.Error("the ⚡ attribute must mark a page as AMP")
	}
}

// TestPreformattedTextLineBreaks covers writeText's <pre> branch: a newline
// inside preformatted text is a line/sentence break, while a newline in normal
// text only collapses to a word break.
func TestPreformattedTextLineBreaks(t *testing.T) {
	// three lines inside <pre> -> three blocks -> three sentences
	f := parseHTML(t, "https://ex.com/p",
		"<html><body><pre>line one here\nline two here\nline three here</pre></body></html>", nil, nil)
	if f.WordCount != 9 {
		t.Errorf("pre word count = %d, want 9", f.WordCount)
	}
	// each pre line is its own block (sentence)
	gotSentences := int(float64(f.WordCount)/f.AvgWordsPerSentence + 0.5)
	if gotSentences != 3 {
		t.Errorf("pre sentences = %d (awps=%.3f), want 3", gotSentences, f.AvgWordsPerSentence)
	}

	// a leading newline inside pre produces an empty first line (i==0 path then
	// flush on the next) — still counts the words that follow
	f = parseHTML(t, "https://ex.com/p",
		"<html><body><pre>\nalpha beta</pre></body></html>", nil, nil)
	if f.WordCount != 2 {
		t.Errorf("pre with leading newline word count = %d, want 2", f.WordCount)
	}
}

// TestListMarkersOrdered covers listMarker's <ol> ordinal branch including the
// start attribute and the non-list / definition-list cases that return "".
func TestListMarkersOrdered(t *testing.T) {
	// an <ol start="5"> numbers its items 5, 6, 7 — each marker is a number
	// word AND a sentence terminator
	f := parseHTML(t, "https://ex.com/p",
		`<html><body><ol start="5"><li>aa bb</li><li>cc dd</li><li>ee ff</li></ol></body></html>`, nil, nil)
	// 3 markers + 6 words = 9 words
	if f.WordCount != 9 {
		t.Errorf("ol start word count = %d, want 9", f.WordCount)
	}
	if !strings.Contains(f.ContentText, "5.") || !strings.Contains(f.ContentText, "7.") {
		t.Errorf("ol start markers absent from content: %q", f.ContentText)
	}

	// a bare <ul> bullet
	f = parseHTML(t, "https://ex.com/p",
		`<html><body><ul><li>only item</li></ul></body></html>`, nil, nil)
	if !strings.Contains(f.ContentText, "•") {
		t.Errorf("ul bullet absent: %q", f.ContentText)
	}

	// an <li> whose parent is not a list element gets no marker
	f = parseHTML(t, "https://ex.com/p",
		`<html><body><div><li>orphan item</li></div></body></html>`, nil, nil)
	if strings.Contains(f.ContentText, "•") {
		t.Errorf("orphan li must not get a bullet: %q", f.ContentText)
	}

	// a definition list has no markers
	f = parseHTML(t, "https://ex.com/p",
		`<html><body><dl><dt>term one</dt><dd>def one</dd></dl></body></html>`, nil, nil)
	if strings.Contains(f.ContentText, "•") {
		t.Errorf("definition list must have no markers: %q", f.ContentText)
	}

	// an empty <ol> item drops its unconsumed marker (no stray number)
	f = parseHTML(t, "https://ex.com/p",
		`<html><body><ol><li></li><li>real text</li></ol></body></html>`, nil, nil)
	if !strings.Contains(f.ContentText, "real text") {
		t.Errorf("ol with an empty item lost its real item: %q", f.ContentText)
	}
}

// TestLinkHeaderEdgeCases exercises parseLinkEntry and parseHeaderFacts edge
// cases: a malformed entry without <...>, an unresolvable target, multiple
// rels in one entry, and an alternate without hreflang.
func TestLinkHeaderEdgeCases(t *testing.T) {
	h := http.Header{}
	// malformed entry (no angle brackets) is skipped; valid canonical kept
	h.Add("Link", `not-a-link; rel="canonical", <https://ex.com/c>; rel="canonical"`)
	// one entry, two rels (next + an alternate without hreflang)
	h.Add("Link", `</p3>; rel="next"; hreflang=""`)
	// alternate WITHOUT hreflang must not create an hreflang fact
	h.Add("Link", `</plain>; rel="alternate"`)

	f := parseHTML(t, "https://ex.com/list", "<html><body></body></html>", h, nil)
	if len(f.CanonicalHTTP) != 1 || f.CanonicalHTTP[0] != "https://ex.com/c" {
		t.Errorf("canonical HTTP = %v (malformed entry must be skipped)", f.CanonicalHTTP)
	}
	if len(f.NextHTTP) != 1 || f.NextHTTP[0] != "https://ex.com/p3" {
		t.Errorf("next HTTP = %v", f.NextHTTP)
	}
	if len(f.HreflangHTTP) != 0 {
		t.Errorf("alternate without hreflang must add no hreflang fact: %v", f.HreflangHTTP)
	}
}

// TestLinkHeaderParamWithoutValue covers the parseLinkEntry branch where a
// parameter has no '=' (it is skipped, not treated as a key).
func TestLinkHeaderParamWithoutValue(t *testing.T) {
	h := http.Header{}
	h.Add("Link", `<https://ex.com/c>; canonical; rel="canonical"`)
	f := parseHTML(t, "https://ex.com/p", "<html><body></body></html>", h, nil)
	if len(f.CanonicalHTTP) != 1 {
		t.Errorf("valueless param must be skipped, canonical = %v", f.CanonicalHTTP)
	}
}

// TestNoHeaderFacts covers the nil-header early return in parseHeaderFacts.
func TestNoHeaderFacts(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", "<html><body></body></html>", nil, nil)
	if len(f.XRobotsTag) != 0 || len(f.CanonicalHTTP) != 0 {
		t.Error("nil header must produce no header-derived facts")
	}
}

// TestBaseHrefResolvesRelativeBase covers resolve() when the base href is
// itself relative: the page URL anchors the base, then links resolve against
// the resolved base.
func TestBaseHrefResolvesRelativeBase(t *testing.T) {
	f := parseHTML(t, "https://ex.com/section/page", `<html><head>
		<base href="../assets/">
	</head><body>
		<a href="logo.png">x</a>
	</body></html>`, nil, nil)
	if f.BaseHref != "../assets/" {
		t.Errorf("BaseHref = %q", f.BaseHref)
	}
	if findLink(f, Hyperlink, "https://ex.com/assets/logo.png") == nil {
		t.Errorf("relative base not resolved correctly: %+v", f.Links)
	}
}

// TestPositionDisabled covers the StoreLinkPaths-off branch of position():
// with link-path storage disabled, no link carries a Position.
func TestPositionDisabled(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<footer><a href="/x">x</a></footer>
	</body></html>`, nil, func(c *config.Config) {
		c.StoreLinkPaths = false
	})
	l := findLink(f, Hyperlink, "https://ex.com/x")
	if l == nil {
		t.Fatal("link missing")
	}
	if l.Position != "" {
		t.Errorf("position = %q, want empty when StoreLinkPaths is off", l.Position)
	}
}

// TestMalformedBodyStillParses pins Parse's never-fail contract on garbage
// input: it returns Facts with a stable hash rather than panicking.
func TestMalformedBodyStillParses(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<<>not really>html<<<`, nil, nil)
	if f == nil || f.Hash == "" {
		t.Error("Parse must always return Facts with a hash")
	}
}
