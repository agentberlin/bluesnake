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
	"os"
	"path/filepath"
	"slices"
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

// outcomeFixture crawls a site with one page of every outcome the header's
// status_counts separates: 2xx, 3xx, 4xx and 5xx responses, a URL robots.txt
// disallows, and one whose connection drops before a response (a fetch error).
// The outbound link reaches a 404 on a second server, so the external scope
// has a count of its own.
func outcomeFixture(t *testing.T) (*store.Crawl, store.Info) {
	t.Helper()
	ext := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ext.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body><a href="/ok">ok</a> <a href="/old">old</a>
<a href="/missing">missing</a> <a href="/broken">broken</a> <a href="/private/x">private</a>
<a href="/reset">reset</a> <a href="%s/gone">gone</a></body></html>`, ext.URL)
		case "/ok":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><body>ok</body></html>")
		case "/old":
			http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
		case "/broken":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "/reset":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close() // no response at all
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Links.External.Store = true
	cfg.Links.External.Crawl = true
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

// tally classifies page records the way the progress feed classifies the page
// stream: state first, then status class, and no status class is no response.
func tally(pages []Page) StatusCounts {
	var c StatusCounts
	for _, p := range pages {
		switch {
		case p.State == crawler.StateBlockedRobots:
			c.BlockedByRobots++
		case p.State == crawler.StateError:
			c.NoResponse++
		case p.StatusCode >= 500:
			c.Status5xx++
		case p.StatusCode >= 400:
			c.Status4xx++
		case p.StatusCode >= 300:
			c.Status3xx++
		case p.StatusCode >= 200:
			c.Status2xx++
		default:
			c.NoResponse++
		}
	}
	return c
}

func (c StatusCounts) sum() int {
	return c.Status2xx + c.Status3xx + c.Status4xx + c.Status5xx + c.BlockedByRobots + c.NoResponse
}

// The header's status_counts describe exactly the page lines that follow —
// the same scope, the same classification as the progress feed — and add up
// to `pages`, so a consumer can chart a crawl's outcomes from line 1 alone.
func TestHeaderStatusCountsMatchTheRecords(t *testing.T) {
	st, info := outcomeFixture(t)

	h, pages, _ := bundleOf(t, st, info, Options{})
	want := StatusCounts{Status2xx: 2, Status3xx: 1, Status4xx: 1, Status5xx: 1, BlockedByRobots: 1, NoResponse: 1}
	if got := tally(pages); got != want {
		t.Fatalf("fixture records classify as %+v, want one of each outcome (\"/\" and /ok are the 2xx): %+v", got, want)
	}
	if h.StatusCounts != want {
		t.Errorf("header status_counts = %+v, want the records' %+v", h.StatusCounts, want)
	}
	if h.StatusCounts.sum() != h.Pages || h.Pages != len(pages) {
		t.Errorf("status_counts sum to %d, header pages = %d, %d lines follow", h.StatusCounts.sum(), h.Pages, len(pages))
	}

	// The counts follow the scope filter with `pages`: the external 404 is
	// counted only by the bundles that emit it.
	for _, scope := range []string{ScopeExternal, ScopeAll} {
		h, pages, _ := bundleOf(t, st, info, Options{Scope: scope})
		if got := tally(pages); h.StatusCounts != got {
			t.Errorf("--scope %s: header status_counts = %+v, records classify as %+v", scope, h.StatusCounts, got)
		}
		if h.StatusCounts.sum() != h.Pages || h.Pages != len(pages) {
			t.Errorf("--scope %s: status_counts sum to %d, header pages = %d, %d lines follow",
				scope, h.StatusCounts.sum(), h.Pages, len(pages))
		}
	}
	if h, _, _ := bundleOf(t, st, info, Options{Scope: ScopeAll}); h.StatusCounts.Status4xx != 2 {
		t.Errorf("--scope all: status_4xx = %d, want the internal and the external 404", h.StatusCounts.Status4xx)
	}
}

// All six keys are always on the wire, zeros included, so a consumer never has
// to guess whether a missing key means zero — an absent status_counts object
// means only an older bundle. A recorded response with no status class counts
// as no_response, so the six still sum to `pages`.
func TestHeaderStatusCountsCarryEveryKey(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	header := func() (Header, string) {
		h, _, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
		return h, strings.SplitN(string(raw), "\n", 2)[0]
	}
	if _, line := header(); !strings.Contains(line,
		`"status_counts":{"status_2xx":0,"status_3xx":0,"status_4xx":0,"status_5xx":0,"blocked_by_robots":0,"no_response":0}`) {
		t.Errorf("an empty crawl's header must carry all six keys at zero:\n%s", line)
	}

	for _, p := range []*crawler.PageRecord{
		{URL: "https://ex.test/", State: crawler.StateCrawled, StatusCode: 200},
		{URL: "https://ex.test/switch", State: crawler.StateCrawled, StatusCode: 101},
	} {
		p.Scope = "internal"
		if err := c.Page(p); err != nil {
			t.Fatal(err)
		}
	}
	h, line := header()
	if !strings.Contains(line,
		`"status_counts":{"status_2xx":1,"status_3xx":0,"status_4xx":0,"status_5xx":0,"blocked_by_robots":0,"no_response":1}`) {
		t.Errorf("header status_counts, want the 101 as no_response and every zero kept:\n%s", line)
	}
	if h.StatusCounts.sum() != h.Pages {
		t.Errorf("status_counts sum to %d, pages = %d", h.StatusCounts.sum(), h.Pages)
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
	// The whole header map, not just the one value the first bundles pulled out.
	if ct := home.Headers["Content-Type"]; !strings.Contains(ct, "text/html") {
		t.Errorf("headers = %v, want the response's Content-Type", home.Headers)
	}
	if home.ContentHash == "" {
		t.Error("content_hash is empty; the raw-body MD5 is stored on every parsed page")
	}
	if home.WordCount == 0 || home.TextRatio == 0 {
		t.Errorf("word_count = %d text_ratio = %v, want the readability metrics", home.WordCount, home.TextRatio)
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
	// Arrays, not nulls — and the headers map and custom results likewise.
	for _, want := range []string{`"h1":[]`, `"h2":[]`, `"heading_levels":[]`, `"meta_keywords":[]`,
		`"meta_robots":[]`, `"x_robots_tag":[]`, `"hreflang":[]`, `"amp_links":[]`, `"mobile_alternates":[]`,
		`"head":{"invalid_elements":[],"missing":false,"multiple":false}`,
		`"headers":{}`, `"custom_results":[]`, `"links":[]`, `"depth":null`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("record does not contain %s:\n%s", want, raw)
		}
	}
	// The three optional keys are absent, not null: structured where the page
	// has none, jsdiff where the crawl did not render, html where it did not
	// store sources.
	line := strings.SplitN(string(raw), "\n", 2)[1]
	for _, absent := range []string{`"structured":`, `"jsdiff":`, `"html":`, `"rendered_html":`} {
		if strings.Contains(line, absent) {
			t.Errorf("record must omit %s rather than emit null or empty:\n%s", absent, line)
		}
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

// pageLines splits a raw bundle into its page lines (everything after the
// header), for assertions about what a record does NOT carry.
func pageLines(raw []byte) []string {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	return lines[1:]
}

// A Full bundle of a crawl that kept its sources (extraction.store_html)
// carries every page's raw HTML, and the header says both what the crawl kept
// and that this stream has it.
func TestFullCarriesStoredHTML(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) { c.Extraction.StoreHTML = true })
	h, pages, _ := bundleOf(t, st, info, Options{Scope: ScopeAll, Full: true})
	if !h.Stored.HTML || h.Stored.RenderedHTML || !h.Full {
		t.Errorf("header stored = %+v full = %v, want html stored and full", h.Stored, h.Full)
	}
	home := pageByPath(pages, "/")
	if home == nil || home.HTML == nil {
		t.Fatal("home page carries no html field")
	}
	if !strings.Contains(*home.HTML, "<h1>First heading</h1>") {
		t.Errorf("html = %q, want the raw source", *home.HTML)
	}
	// Every line carries the key — "" for a page with no stored source — so a
	// consumer never has to guess whether a missing key means "not kept for
	// this page" or "never requested". The external page is that case: fetched
	// for its status, never parsed, so nothing was stored for it.
	var external *Page
	for i := range pages {
		if pages[i].HTML == nil {
			t.Errorf("%s: html key missing although the crawl stored HTML", pages[i].URL)
		}
		if pages[i].Scope == ScopeExternal {
			external = &pages[i]
		}
	}
	if external == nil || external.HTML == nil || *external.HTML != "" {
		t.Errorf("external page = %+v, want html present and empty", external)
	}
}

// The sources are opt-in: without Full a crawl that kept them still bundles
// without them, and the header says so — stored.html true, full false — so a
// consumer knows a re-bundle, not a re-crawl, is what gets them the HTML.
func TestStoredHTMLStaysOutWithoutFull(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) { c.Extraction.StoreHTML = true })
	h, _, raw := bundleOf(t, st, info, Options{Scope: ScopeAll})
	if !h.Stored.HTML || h.Full {
		t.Errorf("header stored = %+v full = %v, want html stored and not full", h.Stored, h.Full)
	}
	for _, line := range pageLines(raw) {
		if strings.Contains(line, `"html":`) || strings.Contains(line, `"rendered_html":`) {
			t.Fatalf("a bundle without Full must not emit the source keys:\n%s", line)
		}
	}
}

// Full on a crawl that stored nothing carries nothing, and the key is absent
// rather than "" on every page: "" could not say whether the source was never
// stored or stored empty.
func TestFullOnACrawlThatStoredNoHTMLCarriesNone(t *testing.T) {
	st, info := crawledFixture(t, nil)
	h, _, raw := bundleOf(t, st, info, Options{Full: true})
	if h.Stored.HTML || h.Stored.RenderedHTML || !h.Full {
		t.Errorf("header stored = %+v full = %v, want neither stored and full", h.Stored, h.Full)
	}
	for _, line := range pageLines(raw) {
		if strings.Contains(line, `"html":`) || strings.Contains(line, `"rendered_html":`) {
			t.Fatalf("a crawl that stored no HTML must not emit the keys:\n%s", line)
		}
	}
}

// A recorded blob whose file is gone is corruption, not an empty page: the
// bundle fails naming the URL rather than quietly emitting "" for it.
func TestMissingStoredHTMLFileIsAnError(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) { c.Extraction.StoreHTML = true })
	_, pages, _ := bundleOf(t, st, info, Options{})
	home := pageByPath(pages, "/")
	path, err := st.BlobPath(home.URL, "html")
	if err != nil || path == "" {
		t.Fatalf("blob path = %q, %v", path, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = Write(st, info, Options{Full: true}, &buf)
	if err == nil || !strings.Contains(err.Error(), home.URL) {
		t.Errorf("Write = %v, want an error naming %s", err, home.URL)
	}
	// Without Full the file is never read, so the default bundle is unaffected.
	buf.Reset()
	if err := Write(st, info, Options{}, &buf); err != nil {
		t.Errorf("a bundle without Full must not touch the stored files: %v", err)
	}
}

// The blobs table records each file's path as it was at crawl time. A store
// that has moved since still bundles: the file is found under the store's own
// assets dir by name.
func TestStoredHTMLSurvivesAMovedStore(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) { c.Extraction.StoreHTML = true })
	dir := filepath.Dir(filepath.Dir(st.AssetsDir())) // <dir>/crawls/<id>.assets
	st.Close()
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	re, err := store.OpenCrawl(moved, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	_, pages, _ := bundleOf(t, re, info, Options{Full: true})
	home := pageByPath(pages, "/")
	if home == nil || home.HTML == nil || !strings.Contains(*home.HTML, "<h1>First heading</h1>") {
		t.Errorf("home page after the move = %+v, want its stored HTML", home)
	}
}

// rendered_html rides along only on a JavaScript-rendering crawl that asked
// for it: the flag alone writes nothing, so the header must not promise it.
func TestRenderedHTMLNeedsARenderingCrawl(t *testing.T) {
	textMode := config.Default()
	textMode.Extraction.StoreRenderedHTML = true
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", textMode)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if h, _, _ := bundleOf(t, c, store.Info{ID: c.ID}, Options{Full: true}); h.Stored.RenderedHTML {
		t.Error("header promises rendered_html on a text-mode crawl, which never renders")
	}

	// The stored DOM is written through the same BlobSink the crawler uses.
	jsMode := config.Default()
	jsMode.Rendering.Mode = "javascript"
	jsMode.Extraction.StoreRenderedHTML = true
	r, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", jsMode)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	const dom = "<html><body><h1>JS Title</h1></body></html>"
	if err := r.Blob("https://ex.test/", "rendered_html", []byte(dom)); err != nil {
		t.Fatal(err)
	}
	h, pages, _ := bundleOf(t, r, store.Info{ID: r.ID}, Options{Full: true})
	if !h.Stored.RenderedHTML || h.Stored.HTML {
		t.Errorf("header stored = %+v, want rendered_html only", h.Stored)
	}
	if len(pages) != 1 || pages[0].RenderedHTML == nil || *pages[0].RenderedHTML != dom {
		t.Fatalf("rendered_html = %v, want the stored DOM", pages[0].RenderedHTML)
	}
	if pages[0].HTML != nil {
		t.Error("html key present although store_html is off")
	}
}

// The columns finalize and the analyses write — the link graph, duplicates,
// egress attribution — are carried as stored, and jsdiff like structured:
// present only where a rendering crawl recorded one.
func TestPageCarriesGraphDuplicateAndRenderFields(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/a", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		DiscoveredFrom: "https://ex.test/", OutsideStartFolder: true,
		DuplicateOf: "https://ex.test/", Proxy: "direct", MatchedRobotsLine: 7,
		Headers: map[string]string{"Content-Type": "text/html", "X-Cache": "HIT"},
		JSDiff:  &crawler.JSDiff{RenderedWordCount: 120, WordCountChange: 20, TitleChanged: true, RenderedTitle: "JS title"},
	}); err != nil {
		t.Fatal(err)
	}
	// Inlinks and the analysis metrics are written by finalize, after the
	// crawl, with UPDATEs like these — not by Page.
	if _, err := c.DB().Exec(`UPDATE pages SET inlinks = 3, link_score = 0.25, unique_inlinks = 2, unique_outlinks = 5,
		closest_similarity = 0.9, near_dup_count = 1 WHERE url = ?`, "https://ex.test/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/b", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	a := pageByPath(pages, "/a")
	if a == nil {
		t.Fatal("/a missing")
	}
	if a.Inlinks != 3 || a.UniqueInlinks != 2 || a.UniqueOutlinks != 5 || a.LinkScore != 0.25 {
		t.Errorf("graph = inlinks %d unique %d/%d score %v", a.Inlinks, a.UniqueInlinks, a.UniqueOutlinks, a.LinkScore)
	}
	if a.DiscoveredFrom != "https://ex.test/" || !a.OutsideStartFolder {
		t.Errorf("discovered_from = %q outside_start_folder = %v", a.DiscoveredFrom, a.OutsideStartFolder)
	}
	if a.DuplicateOf != "https://ex.test/" || a.ClosestSimilarity != 0.9 || a.NearDupCount != 1 {
		t.Errorf("duplicates = %q %v %d", a.DuplicateOf, a.ClosestSimilarity, a.NearDupCount)
	}
	if a.Proxy != "direct" || a.MatchedRobotsLine != 7 {
		t.Errorf("proxy = %q matched_robots_line = %d", a.Proxy, a.MatchedRobotsLine)
	}
	if a.Headers["X-Cache"] != "HIT" || len(a.Headers) != 2 {
		t.Errorf("headers = %v, want both stored headers", a.Headers)
	}
	if a.JSDiff == nil || a.JSDiff.RenderedTitle != "JS title" || !a.JSDiff.TitleChanged {
		t.Errorf("jsdiff = %+v, want the stored comparison", a.JSDiff)
	}
	for _, line := range pageLines(raw) {
		if strings.Contains(line, `"url":"https://ex.test/b"`) && strings.Contains(line, `"jsdiff":`) {
			t.Errorf("a page the crawl did not render must omit jsdiff:\n%s", line)
		}
	}
}

// The remaining parsed facts — the ones the tab exports flatten or drop — and
// the link attributes the links tab carries but the first bundles did not.
func TestPageCarriesTheRestOfTheFacts(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Facts: &parse.Facts{
			Keywords: []string{"alpha, bravo"}, H2s: []string{"Sub one", "Sub two"}, HeadingLevels: []int{1, 2, 2},
			NextHTTP: []string{"https://ex.test/?page=2"}, PrevHTML: []string{"https://ex.test/?page=0"},
			MetaRefresh: "5; url=/x", MetaRefreshURL: "https://ex.test/x",
			HreflangHTML: []parse.Hreflang{{Lang: "en", URL: "https://ex.test/"}},
			HreflangHTTP: []parse.Hreflang{{Lang: "de", URL: "https://ex.test/de"}},
			AMPLinks:     []string{"https://ex.test/amp"}, MobileAlternates: []string{"https://m.ex.test/"},
			Lang: "en", IsAMP: true,
			TextRatio: 12.5, AvgWordsPerSentence: 9.5, Flesch: 70.25, Hash: "abc123",
			Head: parse.HeadValidity{InvalidElementsInHead: []string{"img"}, MultipleHead: true},
			Links: []parse.Link{
				{Type: parse.Image, URL: "https://ex.test/i.png", Raw: "/i.png", Alt: "An image", Width: "10", Height: "20", PathType: "root-relative"},
				{Type: parse.Hyperlink, URL: "https://ex.test/x", Raw: "/x", Target: "_blank", PathType: "root-relative"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{LinkTypes: []string{LinkTypeAll}})
	p := pages[0]
	if !slices.Equal(p.MetaKeywords, []string{"alpha, bravo"}) || !slices.Equal(p.H2, []string{"Sub one", "Sub two"}) {
		t.Errorf("meta_keywords = %v h2 = %v", p.MetaKeywords, p.H2)
	}
	if !slices.Equal(p.HeadingLevels, []int{1, 2, 2}) {
		t.Errorf("heading_levels = %v", p.HeadingLevels)
	}
	// rel next/prev follow the canonical rule: HTML, else the HTTP Link header.
	if p.RelNext != "https://ex.test/?page=2" || p.RelPrev != "https://ex.test/?page=0" {
		t.Errorf("rel_next = %q rel_prev = %q", p.RelNext, p.RelPrev)
	}
	if p.MetaRefresh != "5; url=/x" || p.MetaRefreshURL != "https://ex.test/x" {
		t.Errorf("meta_refresh = %q -> %q", p.MetaRefresh, p.MetaRefreshURL)
	}
	wantHreflang := []Hreflang{{Lang: "en", URL: "https://ex.test/", Source: "html"}, {Lang: "de", URL: "https://ex.test/de", Source: "http"}}
	if !slices.Equal(p.Hreflang, wantHreflang) {
		t.Errorf("hreflang = %+v, want both sources kept apart: %+v", p.Hreflang, wantHreflang)
	}
	if !slices.Equal(p.AMPLinks, []string{"https://ex.test/amp"}) || !slices.Equal(p.MobileAlternates, []string{"https://m.ex.test/"}) {
		t.Errorf("amp_links = %v mobile_alternates = %v", p.AMPLinks, p.MobileAlternates)
	}
	if p.Lang != "en" || !p.IsAMP {
		t.Errorf("lang = %q is_amp = %v", p.Lang, p.IsAMP)
	}
	if p.TextRatio != 12.5 || p.AvgWordsPerSentence != 9.5 || p.Flesch != 70.25 || p.ContentHash != "abc123" {
		t.Errorf("readability = %v/%v/%v hash = %q", p.TextRatio, p.AvgWordsPerSentence, p.Flesch, p.ContentHash)
	}
	if !slices.Equal(p.Head.InvalidElements, []string{"img"}) || p.Head.Missing || !p.Head.Multiple {
		t.Errorf("head = %+v", p.Head)
	}

	if len(p.Links) != 2 {
		t.Fatalf("links = %+v, want the image and the hyperlink", p.Links)
	}
	img, a := p.Links[0], p.Links[1]
	if img.Raw != "/i.png" || img.Alt != "An image" || img.Width != "10" || img.Height != "20" || img.PathType != "root-relative" {
		t.Errorf("image link = %+v", img)
	}
	if a.Raw != "/x" || a.Target != "_blank" || a.Alt != "" {
		t.Errorf("hyperlink = %+v", a)
	}
	// Image-only attributes are omitted where the type cannot carry them: one
	// alt/width/height on the line, the image's.
	line := pageLines(raw)[0]
	for _, key := range []string{`"alt":`, `"width":`, `"height":`} {
		if n := strings.Count(line, key); n != 1 {
			t.Errorf("%s appears %d times, want once (the image's):\n%s", key, n, line)
		}
	}
	for _, key := range []string{`"raw":`, `"target":`, `"path_type":`} {
		if n := strings.Count(line, key); n != 2 {
			t.Errorf("%s appears %d times, want on both links:\n%s", key, n, line)
		}
	}
}

// Custom search and extraction values ride on their page, sorted by (kind,
// name) so the stream stays reproducible, and a page where nothing matched
// still carries the search's answer rather than nothing.
func TestPageCarriesCustomResults(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) {
		c.CustomSearch = []config.CustomSearch{{Name: "marker", Mode: "contains", Pattern: "uniquecontentmarker"}}
		c.CustomExtraction = []config.CustomExtraction{{Name: "heading", Type: "css", Expression: "h1"}}
	})
	_, pages, _ := bundleOf(t, st, info, Options{Scope: ScopeAll})
	home := pageByPath(pages, "/")
	want := []CustomResult{
		{Kind: "extraction", Name: "heading", Value: "First heading | Second heading"},
		{Kind: "search", Name: "marker", Value: "1"},
	}
	if home == nil || !slices.Equal(home.CustomResults, want) {
		t.Errorf("home custom_results = %+v, want %+v", home.CustomResults, want)
	}
	about := pageByPath(pages, "/about")
	wantAbout := []CustomResult{
		{Kind: "extraction", Name: "heading", Value: "About"},
		{Kind: "search", Name: "marker", Value: "0"},
	}
	if about == nil || !slices.Equal(about.CustomResults, wantAbout) {
		t.Errorf("about custom_results = %+v, want %+v", about.CustomResults, wantAbout)
	}
	// The external page is never parsed; nothing ran, and [] says so.
	for _, p := range pages {
		if p.Scope == ScopeExternal && (p.CustomResults == nil || len(p.CustomResults) != 0) {
			t.Errorf("external page custom_results = %+v, want an empty array", p.CustomResults)
		}
	}
}
