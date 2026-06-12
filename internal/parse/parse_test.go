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
			<footer><a href="/from-footer">y</a></footer>
		</body></html>`, nil, nil)
	if l := findLink(f, Hyperlink, "https://ex.com/from-header"); l == nil || l.Position != "header" {
		t.Errorf("header link position = %+v, want header", l)
	}
	if l := findLink(f, Hyperlink, "https://ex.com/from-footer"); l == nil || l.Position != "footer" {
		t.Errorf("footer link position = %+v, want footer", l)
	}
}
