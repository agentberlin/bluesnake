package bundle

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/structured"
)

// The fixture site carries everything a bundle has to: body text, a JSON-LD
// block, a class-only footer region, a stylesheet (an asset link that is not a
// graph edge) and an outbound link. The canonical is self-referential so the
// page stays indexable, and the outbound link points at a real second server so
// the external page is a crawled row rather than a DNS failure.
const homeBody = `<html><head><title>Home page title</title>
<meta name="description" content="Home description">
<meta name="robots" content="index,follow">
<link rel="stylesheet" href="/style.css">
<link rel="canonical" href="/">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Organization","name":"Ex","logo":"l.png","url":"https://ex.test"}</script>
</head><body>
<h1>First heading</h1><h1>Second heading</h1>
<p>uniquecontentmarker alpha bravo charlie delta echo</p>
<div class="site-footer"><a href="/about">About us</a></div>
<a href="%s/elsewhere">Outbound</a>
</body></html>`

const aboutBody = `<html><head><title>About page title</title></head>
<body><h1>About</h1><p>about body text</p></body></html>`

// crawledFixture runs a real crawl of the two-page site into a fresh store and
// returns the store plus its registry row — the same pair the CLI hands Write.
func crawledFixture(t *testing.T, mutate func(*config.Config)) (*store.Crawl, store.Info) {
	t.Helper()
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>external</body></html>")
	}))
	t.Cleanup(ext.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, homeBody, ext.URL)
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "body{}")
		default:
			fmt.Fprint(w, aboutBody)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Extraction.StructuredData.JSONLD = true
	cfg.Links.External.Store = true
	cfg.Links.External.Crawl = true
	if mutate != nil {
		mutate(cfg)
	}
	st, err := store.CreateCrawl(dir, []string{srv.URL + "/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(dir, st.ID, store.StatusCompleted, res.Crawled, res.Total); err != nil {
		t.Fatal(err)
	}
	info, err := store.CrawlInfo(dir, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	return st, info
}

// bundleOf renders a bundle and splits it into the header and page records.
func bundleOf(t *testing.T, st *store.Crawl, info store.Info, opts Options) (Header, []Page, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(st, info, opts, &buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw := buf.Bytes()
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	var h Header
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		t.Fatalf("header line is not JSON: %v\n%s", err, lines[0])
	}
	var pages []Page
	for _, line := range lines[1:] {
		var p Page
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("page line is not JSON: %v\n%s", err, line)
		}
		pages = append(pages, p)
	}
	return h, pages, raw
}

func pageByPath(pages []Page, suffix string) *Page {
	for i := range pages {
		if strings.HasSuffix(pages[i].URL, suffix) {
			return &pages[i]
		}
	}
	return nil
}

// The header describes the stream and is the consumer's two safety checks:
// `format` (refuse what you do not understand) and `pages` (tell a truncated
// transfer from a small crawl).
func TestHeaderDescribesTheCrawlAndCountsTheLines(t *testing.T) {
	st, info := crawledFixture(t, nil)
	h, pages, _ := bundleOf(t, st, info, Options{})

	if h.Format != Format {
		t.Errorf("format = %q, want %q", h.Format, Format)
	}
	if h.BluesnakeVersion == "" || h.CrawlID != info.ID {
		t.Errorf("header = %+v, want the version and crawl id filled", h)
	}
	if h.Mode != "spider" || len(h.Seeds) != 1 {
		t.Errorf("mode = %q seeds = %v, want a spider crawl with one seed", h.Mode, h.Seeds)
	}
	if h.Status != store.StatusCompleted || h.StartedAt == "" {
		t.Errorf("status = %q started_at = %q, want the registry row's values", h.Status, h.StartedAt)
	}
	if h.Crawled != info.Crawled || h.Total != info.Total {
		t.Errorf("crawled/total = %d/%d, want the registry's %d/%d", h.Crawled, h.Total, info.Crawled, info.Total)
	}
	if h.Scope != ScopeInternal || len(h.LinkTypes) != 1 || h.LinkTypes[0] != "hyperlink" {
		t.Errorf("scope = %q link_types = %v, want the defaults echoed", h.Scope, h.LinkTypes)
	}
	if !strings.HasPrefix(h.ConfigDigest, "sha256:") {
		t.Errorf("config_digest = %q, want a sha256 digest", h.ConfigDigest)
	}
	if h.Pages != len(pages) {
		t.Errorf("header pages = %d, but %d page lines follow — the truncation check is broken",
			h.Pages, len(pages))
	}
	if h.Pages == 0 {
		t.Fatal("no pages bundled")
	}
}

// The single highest-value field in the format: the page body text the whole
// downstream index is built on, and the one no tab export carries.
func TestPageCarriesContentTextAndFacts(t *testing.T) {
	st, info := crawledFixture(t, nil)
	_, pages, _ := bundleOf(t, st, info, Options{})

	home := pageByPath(pages, "/")
	if home == nil {
		t.Fatalf("home page missing from %d records", len(pages))
	}
	if !strings.Contains(home.ContentText, "uniquecontentmarker") {
		t.Errorf("content_text = %q, want the body text", home.ContentText)
	}
	if home.Title != "Home page title" || home.MetaDescription != "Home description" {
		t.Errorf("title/description = %q / %q", home.Title, home.MetaDescription)
	}
	// Arrays stay arrays: both h1s, not just the first.
	if len(home.H1) != 2 || home.H1[0] != "First heading" || home.H1[1] != "Second heading" {
		t.Errorf("h1 = %v, want both headings in document order", home.H1)
	}
	if len(home.MetaRobots) != 1 || home.MetaRobots[0] != "index,follow" {
		t.Errorf("meta_robots = %v", home.MetaRobots)
	}
	if home.Canonical != home.URL {
		t.Errorf("canonical = %q, want the self-referential %q", home.Canonical, home.URL)
	}
	if home.WordCount == 0 {
		t.Error("word_count = 0")
	}
	if home.LastModified != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Errorf("last_modified = %q, want the response header", home.LastModified)
	}
	if home.Depth == nil || *home.Depth != 0 {
		t.Errorf("depth = %v, want 0 for the seed", home.Depth)
	}
	if !home.Indexable {
		t.Error("indexable = false; it must be a bool, not the tab exports' string")
	}
}

// The verbatim JSON-LD block, and the position path behind a link's label —
// the two values retained specifically so an export could carry them.
func TestPageCarriesJSONLDAndPositionPath(t *testing.T) {
	st, info := crawledFixture(t, nil)
	_, pages, _ := bundleOf(t, st, info, Options{})

	home := pageByPath(pages, "/")
	if home == nil || home.Structured == nil {
		t.Fatalf("home page = %+v, want structured data", home)
	}
	if len(home.Structured.JSONLD) != 1 {
		t.Fatalf("structured.jsonld = %v, want the one block", home.Structured.JSONLD)
	}
	if !strings.Contains(home.Structured.JSONLD[0], `"@type":"Organization"`) {
		t.Errorf("structured.jsonld[0] = %q, want the block verbatim", home.Structured.JSONLD[0])
	}
	if len(home.Structured.Types) == 0 {
		t.Errorf("structured.types = %v, want the verdict alongside the evidence", home.Structured.Types)
	}

	var footer *Link
	for i := range home.Links {
		if strings.HasSuffix(home.Links[i].URL, "/about") {
			footer = &home.Links[i]
		}
	}
	if footer == nil {
		t.Fatalf("the /about link is missing from %+v", home.Links)
	}
	if !strings.Contains(footer.PositionPath, "site-footer") {
		t.Errorf("position_path = %q, want the site-footer class", footer.PositionPath)
	}
	if footer.Position != "footer" {
		t.Errorf("position = %q, want footer", footer.Position)
	}
	if strings.Contains(footer.ElemPath, "site-footer") {
		t.Errorf("elem_path = %q, want the pure-positional path", footer.ElemPath)
	}
	if footer.Anchor != "About us" {
		t.Errorf("anchor = %q", footer.Anchor)
	}
}

// Scope: internal by default (a site's own corpus), the whole crawl with all.
func TestScopeDefaultsToInternal(t *testing.T) {
	st, info := crawledFixture(t, nil)

	h, pages, _ := bundleOf(t, st, info, Options{})
	for _, p := range pages {
		if p.Scope != ScopeInternal {
			t.Errorf("default bundle carries a %s page (%s)", p.Scope, p.URL)
		}
	}
	internalCount := len(pages)

	hAll, all, _ := bundleOf(t, st, info, Options{Scope: ScopeAll})
	if hAll.Scope != ScopeAll {
		t.Errorf("header scope = %q, want all", hAll.Scope)
	}
	if hAll.Pages != len(all) {
		t.Errorf("--scope all: header pages = %d, %d lines", hAll.Pages, len(all))
	}
	var external int
	for _, p := range all {
		if p.Scope == ScopeExternal {
			external++
		}
	}
	if external == 0 {
		t.Fatal("--scope all emitted no external page — the fixture cannot prove the default filters")
	}
	if len(all) <= internalCount {
		t.Errorf("--scope all = %d pages, default = %d; all must be a superset", len(all), internalCount)
	}
	if h.Pages != internalCount {
		t.Errorf("default header pages = %d, want %d", h.Pages, internalCount)
	}

	// external-only is the complement.
	_, ext, _ := bundleOf(t, st, info, Options{Scope: ScopeExternal})
	if len(ext) != external {
		t.Errorf("--scope external = %d pages, want the %d external pages", len(ext), external)
	}
}

// Link types: the graph by default (hyperlinks), everything on request. The
// stylesheet is the canary — an asset reference, not a graph edge.
func TestLinkTypesDefaultToHyperlinksOnly(t *testing.T) {
	st, info := crawledFixture(t, nil)

	_, pages, _ := bundleOf(t, st, info, Options{})
	home := pageByPath(pages, "/")
	if home == nil {
		t.Fatal("home page missing")
	}
	for _, l := range home.Links {
		if l.Type != "hyperlink" {
			t.Errorf("default bundle carries a %q link (%s)", l.Type, l.URL)
		}
	}
	if len(home.Links) == 0 {
		t.Fatal("no hyperlinks bundled")
	}

	h, allPages, _ := bundleOf(t, st, info, Options{LinkTypes: []string{LinkTypeAll}})
	if len(h.LinkTypes) != 1 || h.LinkTypes[0] != LinkTypeAll {
		t.Errorf("header link_types = %v, want [all]", h.LinkTypes)
	}
	allHome := pageByPath(allPages, "/")
	var css bool
	for _, l := range allHome.Links {
		if l.Type == "css" {
			css = true
		}
	}
	if !css {
		t.Errorf("--link-types all carries no stylesheet: %+v", allHome.Links)
	}

	// A named subset is honoured, and only it.
	_, cssOnly, _ := bundleOf(t, st, info, Options{LinkTypes: []string{"css"}})
	cssHome := pageByPath(cssOnly, "/")
	if len(cssHome.Links) == 0 {
		t.Fatal("--link-types css produced no links")
	}
	for _, l := range cssHome.Links {
		if l.Type != "css" {
			t.Errorf("--link-types css carries a %q link", l.Type)
		}
	}
}

// Two bundles of one unchanged crawl are byte-identical — what lets a consumer
// diff two bundles and a conversion be compared against a committed fixture.
func TestBundleIsByteReproducible(t *testing.T) {
	st, info := crawledFixture(t, nil)
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"plain", Options{Scope: ScopeAll, LinkTypes: []string{LinkTypeAll}}},
		{"gzip", Options{Scope: ScopeAll, LinkTypes: []string{LinkTypeAll}, GZIP: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a, b bytes.Buffer
			if err := Write(st, info, tc.opts, &a); err != nil {
				t.Fatal(err)
			}
			if err := Write(st, info, tc.opts, &b); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Errorf("two bundles of one crawl differ (%d vs %d bytes)", a.Len(), b.Len())
			}
			if a.Len() == 0 {
				t.Fatal("empty bundle")
			}
		})
	}
}

// Pages are ordered by URL, and each page's links keep Facts.Links order
// (document order) rather than being re-sorted.
func TestOrderingIsURLThenDocumentOrder(t *testing.T) {
	st, info := crawledFixture(t, nil)
	_, pages, _ := bundleOf(t, st, info, Options{Scope: ScopeAll, LinkTypes: []string{LinkTypeAll}})
	for i := 1; i < len(pages); i++ {
		if pages[i-1].URL > pages[i].URL {
			t.Fatalf("pages out of URL order: %q before %q", pages[i-1].URL, pages[i].URL)
		}
	}
	home := pageByPath(pages, "/")
	// The home page's head carries the stylesheet and canonical before the body's
	// hyperlinks; document order must survive.
	var seenCSS, seenHyperlink bool
	for _, l := range home.Links {
		if l.Type == "css" {
			if seenHyperlink {
				t.Errorf("links re-sorted: the head stylesheet follows a body hyperlink")
			}
			seenCSS = true
		}
		if l.Type == "hyperlink" {
			seenHyperlink = true
		}
	}
	if !seenCSS || !seenHyperlink {
		t.Fatalf("fixture did not produce both link kinds: %+v", home.Links)
	}
}

// A gzipped bundle is a valid gzip stream carrying the same bytes.
func TestGzipStreamDecompressesToThePlainBundle(t *testing.T) {
	st, info := crawledFixture(t, nil)
	var plain, zipped bytes.Buffer
	if err := Write(st, info, Options{}, &plain); err != nil {
		t.Fatal(err)
	}
	if err := Write(st, info, Options{GZIP: true}, &zipped); err != nil {
		t.Fatal(err)
	}
	gr, err := gzip.NewReader(&zipped)
	if err != nil {
		t.Fatalf("not a gzip stream: %v", err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if err := gr.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain.Bytes()) {
		t.Error("the gzipped bundle decompresses to something other than the plain bundle")
	}
}

// Pages without Facts (non-HTML, and every external page) are DATA, not noise:
// a link to a crawled PDF is a link to something the crawl found. They are
// emitted with the Facts-derived fields empty, never filtered out — and every
// array stays an array so the record shape does not vary.
func TestFactlessPagesAreEmittedWithEmptyFacts(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rec := &crawler.PageRecord{
		URL: "https://ex.test/paper.pdf", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "application/pdf", Size: 1234,
		Depth: crawler.NoDepth,
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	_, pages, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	if len(pages) != 1 {
		t.Fatalf("pages = %d, want the PDF row emitted", len(pages))
	}
	p := pages[0]
	if p.ContentType != "application/pdf" || p.Size != 1234 {
		t.Errorf("scalars lost: %+v", p)
	}
	if p.Depth != nil {
		t.Errorf("depth = %v, want null for a page no followed path reaches", *p.Depth)
	}
	if p.ContentText != "" || p.Title != "" {
		t.Errorf("Facts-derived fields must be empty: %+v", p)
	}
	// Arrays, not nulls.
	for _, want := range []string{`"h1":[]`, `"meta_robots":[]`, `"x_robots_tag":[]`, `"links":[]`, `"depth":null`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("record does not contain %s:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"structured":`) {
		t.Errorf("a page with no structured data must omit the key, not emit null:\n%s", raw)
	}
}

// The canonical follows the `canonicals` tab's rule: HTML first, then the HTTP
// Link header — not the `internal` tab's HTML-only one.
func TestCanonicalFallsBackToTheHTTPHeader(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Facts: &parse.Facts{CanonicalHTTP: []string{"https://ex.test/from-header"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, _ := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	if len(pages) != 1 || pages[0].Canonical != "https://ex.test/from-header" {
		t.Errorf("canonical = %q, want the HTTP Link header's value", pages[0].Canonical)
	}
}

// A page with JSON-LD but no parsed Facts still carries its structured data:
// the two columns are independent.
func TestStructuredSurvivesWithoutFacts(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	block := `{"@type":"WebPage","name":"x"}`
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		StructuredData: &structured.PageData{Formats: []string{"jsonld"}, JSONLD: []string{block}},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, _ := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	if len(pages) != 1 || pages[0].Structured == nil || len(pages[0].Structured.JSONLD) != 1 {
		t.Fatalf("structured = %+v", pages[0].Structured)
	}
	if pages[0].Structured.JSONLD[0] != block {
		t.Errorf("jsonld = %q, want %q", pages[0].Structured.JSONLD[0], block)
	}
}

// A bad scope or link type is rejected before anything is written, so a typo
// produces an error rather than a silently empty links array on every page.
func TestValidateRejectsUnknownScopeAndLinkType(t *testing.T) {
	if err := (Options{Scope: "iternal"}).Validate(); err == nil {
		t.Error("a misspelled scope must be rejected")
	}
	if err := (Options{LinkTypes: []string{"hyperlinks"}}).Validate(); err == nil {
		t.Error("a misspelled link type must be rejected")
	}
	if err := (Options{}).Validate(); err != nil {
		t.Errorf("the defaults must validate: %v", err)
	}
	for _, s := range []string{ScopeInternal, ScopeExternal, ScopeAll} {
		if err := (Options{Scope: s}).Validate(); err != nil {
			t.Errorf("scope %q: %v", s, err)
		}
	}
	for _, lt := range parse.LinkTypes() {
		if err := (Options{LinkTypes: []string{string(lt)}}).Validate(); err != nil {
			t.Errorf("link type %q: %v", lt, err)
		}
	}
	// A whitespace-only list falls back to the default rather than emitting
	// nothing.
	if err := (Options{LinkTypes: []string{" ", ""}}).Validate(); err != nil {
		t.Errorf("an empty list must fall back to the default: %v", err)
	}
	var buf bytes.Buffer
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Write validates again: a library caller cannot skip the check.
	if err := Write(c, store.Info{ID: c.ID}, Options{Scope: "nope"}, &buf); err == nil {
		t.Error("Write accepted an invalid scope")
	}
	if buf.Len() != 0 {
		t.Errorf("a rejected bundle wrote %d bytes", buf.Len())
	}
}

// A bundle of an interrupted crawl is legitimate: the header says so rather
// than the export refusing it.
func TestInterruptedCrawlBundlesAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	c, err := store.CreateCrawl(dir, []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(dir, c.ID, store.StatusInterrupted, 1, 4); err != nil {
		t.Fatal(err)
	}
	info, err := store.CrawlInfo(dir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	h, pages, _ := bundleOf(t, c, info, Options{})
	if h.Status != store.StatusInterrupted {
		t.Errorf("status = %q, want interrupted", h.Status)
	}
	if h.Crawled != 1 || h.Total != 4 {
		t.Errorf("crawled/total = %d/%d, want the registry's 1/4", h.Crawled, h.Total)
	}
	if h.Pages != len(pages) || h.Pages != 1 {
		t.Errorf("pages = %d (%d lines), want the one stored row", h.Pages, len(pages))
	}
}

// The digest changes when the frozen config does, so a consumer can notice a
// corpus built under two different link-position rule sets.
func TestConfigDigestTracksTheFrozenConfig(t *testing.T) {
	base, baseInfo := crawledFixture(t, nil)
	other, otherInfo := crawledFixture(t, func(c *config.Config) {
		c.LinkPositions = append([]config.LinkPosition{{Name: "masthead", Match: "masthead"}}, c.LinkPositions...)
	})
	same, sameInfo := crawledFixture(t, nil)

	bh, _, _ := bundleOf(t, base, baseInfo, Options{})
	oh, _, _ := bundleOf(t, other, otherInfo, Options{})
	sh, _, _ := bundleOf(t, same, sameInfo, Options{})

	if bh.ConfigDigest == oh.ConfigDigest {
		t.Error("changed link-position rules produced the same digest")
	}
	if bh.ConfigDigest != sh.ConfigDigest {
		t.Errorf("identical configs produced different digests: %s vs %s", bh.ConfigDigest, sh.ConfigDigest)
	}
}

// Response-header keys are net/http-canonical today, but a rendered response or
// a future transport need not agree, so last_modified is looked up
// case-insensitively.
func TestLastModifiedLookupIsCaseInsensitive(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Headers: map[string]string{"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT"},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, _ := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	if len(pages) != 1 || pages[0].LastModified != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Errorf("last_modified = %q, want the lowercase header found", pages[0].LastModified)
	}

	// A page with no Last-Modified at all reports an empty string, not a miss.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/b", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Headers: map[string]string{"Content-Type": "text/html"},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, _ = bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	for _, p := range pages {
		if strings.HasSuffix(p.URL, "/b") && p.LastModified != "" {
			t.Errorf("last_modified = %q, want empty", p.LastModified)
		}
	}
}

// A crawl that has not finished carries an empty finished_at rather than the
// Unix epoch the registry stores a zero as.
func TestUnfinishedCrawlHasEmptyFinishedAt(t *testing.T) {
	dir := t.TempDir()
	c, err := store.CreateCrawl(dir, []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	info, err := store.CrawlInfo(dir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	h, _, _ := bundleOf(t, c, info, Options{})
	if h.Status != store.StatusRunning {
		t.Errorf("status = %q, want running", h.Status)
	}
	if h.FinishedAt != "" {
		t.Errorf("finished_at = %q, want empty for an unfinished crawl", h.FinishedAt)
	}
	if h.StartedAt == "" {
		t.Error("started_at is empty")
	}
}
