package crawler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// newCrawlerWithScope builds a crawler and primes the scope/startFolder fields
// that Run would normally set, so the per-URL predicate helpers (classify,
// typeFlags, outsideStartFolder, followsForDepth) can be exercised in
// isolation without a live crawl.
func newCrawlerWithScope(t *testing.T, mutate func(*config.Config), seed string) *Crawler {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := urlutil.NewScope(seed, cfg.Scope.CrawlAllSubdomains, cfg.Scope.CDNs)
	if err != nil {
		t.Fatal(err)
	}
	c.scope = scope
	return c
}

// TestWithFetchOptionsAppliesToClient drives the WithFetchOptions plumbing
// end-to-end: a TLS httptest server has a self-signed cert, so a default
// (verifying) client cannot fetch it. Passing fetch.WithInsecureTLS through
// WithFetchOptions must make the crawl succeed.
func TestWithFetchOptionsAppliesToClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<a href="/child">x</a>`)
	}))
	defer srv.Close()

	// Without the option, the self-signed cert is rejected: the seed fetch
	// errors and nothing is crawled.
	plain, err := New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	resPlain, err := plain.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if resPlain.Crawled != 0 {
		t.Fatalf("without WithInsecureTLS the TLS server should be unreachable, crawled = %d", resPlain.Crawled)
	}

	// With the option threaded through WithFetchOptions, the cert is accepted
	// and the crawl proceeds.
	sink := newCapSink()
	c, err := New(config.Default(), WithFetchOptions(fetch.WithInsecureTLS()), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res := runCap(t, c, sink, srv.URL+"/")
	if res.Crawled < 2 {
		t.Errorf("with WithInsecureTLS the TLS server must be crawled, crawled = %d, want >= 2", res.Crawled)
	}
	if rec := res.Pages[srv.URL+"/"]; rec == nil || rec.State != StateCrawled {
		t.Errorf("seed not crawled over TLS: %+v", rec)
	}
}

// Inlink counting + its gate (a nofollow hyperlink or an image must not count as
// an inlink) is now derived from the gated `edges` the crawl records, not an
// in-RAM RecomputeInlinks replay. It is pinned non-circularly over a real
// crawl+finalize by TestDepthAndInlinkGateDivergenceOracle (finalize package) and
// by the capFinalize-backed inlink assertions in this package's crawl() tests.

// TestStartFolderOf covers the seed start-folder extraction for the root, a
// file at the root, a subfolder, a file inside a subfolder, and a path that
// carries a query string.
func TestStartFolderOf(t *testing.T) {
	cases := []struct {
		seed string
		want string
	}{
		{"https://ex.com", ""},
		{"https://ex.com/", ""},
		{"http://ex.com/page.html", ""},
		{"https://ex.com/blog/", "/blog/"},
		{"https://ex.com/blog/post", "/blog/"},
		{"https://ex.com/a/b/c", "/a/b/"},
		{"https://ex.com/blog/?ref=x", "/blog/"},
		{"https://ex.com/docs/page#frag", "/docs/"},
	}
	for _, tc := range cases {
		if got := startFolderOf(tc.seed); got != tc.want {
			t.Errorf("startFolderOf(%q) = %q, want %q", tc.seed, got, tc.want)
		}
	}
}

// TestOutsideStartFolder exercises outsideStartFolder: no start folder (always
// inside), an external URL (always inside), an in-folder URL, and an
// out-of-folder URL.
func TestOutsideStartFolder(t *testing.T) {
	const base = "http://site.test"
	c := newCrawlerWithScope(t, nil, base+"/blog/")

	// No start folder configured: nothing is outside.
	if c.outsideStartFolder(base + "/anything") {
		t.Error("with empty startFolder, no URL is outside")
	}

	c.startFolder = "/blog/"
	if c.outsideStartFolder(base + "/blog/post") {
		t.Error("/blog/post is inside /blog/")
	}
	if !c.outsideStartFolder(base + "/about") {
		t.Error("/about is outside /blog/")
	}
	// An external URL is never "outside the start folder" (the folder concept
	// is internal-only).
	if c.outsideStartFolder("http://other.test/blog/post") {
		t.Error("external URLs are never outside-start-folder")
	}
}

// TestClassifyListMode pins the list-mode override in classify: every uploaded
// seed authority is internal even if it differs from the scope host.
func TestClassifyListMode(t *testing.T) {
	const seedHost = "http://main.test"
	c := newCrawlerWithScope(t, func(cfg *config.Config) { cfg.Mode = "list" }, seedHost+"/")
	// Simulate Run's list-mode seedAuth setup with a second distinct host.
	c.seedAuth = map[string]bool{
		urlutil.Authority(seedHost + "/"):           true,
		urlutil.Authority("http://other.test/page"): true,
	}

	if got := c.classify("http://other.test/page"); got != urlutil.Internal {
		t.Errorf("classify(other host in list seeds) = %v, want Internal", got)
	}
	if got := c.classify("http://main.test/x"); got != urlutil.Internal {
		t.Errorf("classify(seed host) = %v, want Internal", got)
	}
	if got := c.classify("http://stranger.test/x"); got == urlutil.Internal {
		t.Error("classify(host not in list seeds) must not be Internal")
	}
}

// TestTypeFlags covers the link-type -> store/crawl mapping for a representative
// internal set, the external gate (external types require external crawling),
// and the uncrawlable default.
func TestTypeFlags(t *testing.T) {
	c := newCrawlerWithScope(t, func(cfg *config.Config) {
		// enable a spread of resource/link types so the mapped flags are non-trivial
		cfg.Resources.Images = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Resources.CSS = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Resources.JavaScript = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Resources.Media = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Resources.SWF = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.Canonicals = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.Hreflang = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.Pagination = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.AMP = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.MobileAlternate = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.IFrames = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Links.MetaRefresh = config.StoreCrawl{Store: true, Crawl: true}
	}, "http://site.test/")

	internalTypes := []parse.LinkType{
		parse.Image, parse.CSS, parse.JS, parse.Media, parse.SWF, parse.IFrame,
		parse.Canonical, parse.HreflangLink, parse.Next, parse.Prev, parse.AMP,
		parse.MetaRefreshLink, parse.MobileAlternate,
	}
	for _, lt := range internalTypes {
		store, crawl := c.typeFlags(lt, urlutil.Internal)
		if !store || !crawl {
			t.Errorf("typeFlags(%s, internal) = store %v crawl %v, want both true", lt, store, crawl)
		}
	}

	// Internal hyperlink uses the internal links policy (on by default).
	if store, crawl := c.typeFlags(parse.Hyperlink, urlutil.Internal); !store || !crawl {
		t.Errorf("typeFlags(hyperlink, internal) = %v/%v, want true/true", store, crawl)
	}

	// External: with external crawling off (default), even an enabled resource
	// type is gated to crawl=false.
	if _, crawl := c.typeFlags(parse.Image, urlutil.External); crawl {
		t.Error("external image must not be crawled when external links are off")
	}
	if _, crawl := c.typeFlags(parse.Hyperlink, urlutil.External); crawl {
		t.Error("external hyperlink must not be crawled when external links are off")
	}

	// Uncrawlable / form-action types are never fetched.
	for _, lt := range []parse.LinkType{parse.FormAction, parse.Uncrawlable} {
		if store, crawl := c.typeFlags(lt, urlutil.Internal); store || crawl {
			t.Errorf("typeFlags(%s) = %v/%v, want false/false", lt, store, crawl)
		}
	}

	// XHR maps to the JavaScript resource policy.
	if _, crawl := c.typeFlags(parse.XHR, urlutil.Internal); !crawl {
		t.Error("XHR should follow the JavaScript resource policy (enabled here)")
	}
}

// TestTypeFlagsExternalGate verifies a TYPE that is on AND external crawling on
// still crawls externally, isolating the external AND-gate branch.
func TestTypeFlagsExternalGate(t *testing.T) {
	c := newCrawlerWithScope(t, func(cfg *config.Config) {
		cfg.Links.External = config.StoreCrawl{Store: true, Crawl: true}
	}, "http://site.test/")
	if store, crawl := c.typeFlags(parse.Hyperlink, urlutil.External); !store || !crawl {
		t.Errorf("external hyperlink with external crawl on = %v/%v, want true/true", store, crawl)
	}
}

// TestFollowsForDepthRow exercises followsForDepthRow (the depth CSR's follow
// gate over a stored link row): a plain internal hyperlink is followed, a
// non-crawled type is not, an internal nofollow link is not (unless the config
// follows it), and an external nofollow link obeys the external rule.
func TestFollowsForDepthRow(t *testing.T) {
	const base = "http://site.test"
	hl, img := string(parse.Hyperlink), string(parse.Image)

	c := newCrawlerWithScope(t, nil, base+"/")
	if !c.followsForDepthRow(hl, base+"/x", false) {
		t.Error("plain internal hyperlink must be a followed edge")
	}
	if c.followsForDepthRow(img, base+"/i.png", false) {
		t.Error("image (not crawled by default) must not be a followed edge")
	}
	if c.followsForDepthRow(hl, base+"/x", true) {
		t.Error("internal nofollow link must not be followed by default")
	}

	// follow_internal_nofollow on: now the nofollow internal link is followed.
	cFollow := newCrawlerWithScope(t, func(cfg *config.Config) {
		cfg.Scope.FollowInternalNofollow = true
	}, base+"/")
	if !cFollow.followsForDepthRow(hl, base+"/x", true) {
		t.Error("follow_internal_nofollow must make a nofollow internal link a followed edge")
	}

	// External nofollow link: external links + follow_external_nofollow both on.
	cExt := newCrawlerWithScope(t, func(cfg *config.Config) {
		cfg.Links.External = config.StoreCrawl{Store: true, Crawl: true}
		cfg.Scope.FollowExternalNofollow = true
	}, base+"/")
	if !cExt.followsForDepthRow(hl, "http://other.test/x", true) {
		t.Error("external nofollow link must be followed when external crawl + follow_external_nofollow are on")
	}
	// External nofollow with external crawl on but follow_external_nofollow off.
	cExtNo := newCrawlerWithScope(t, func(cfg *config.Config) {
		cfg.Links.External = config.StoreCrawl{Store: true, Crawl: true}
	}, base+"/")
	if cExtNo.followsForDepthRow(hl, "http://other.test/x", true) {
		t.Error("external nofollow must not be followed when follow_external_nofollow is off")
	}
}

// TestFetchSitemapURLs drives FetchSitemapURLs (the list-mode "download XML
// sitemap" input) against a sitemap index that fans out to a child sitemap,
// asserting the listed page URLs are returned normalized and de-duplicated.
func TestFetchSitemapURLs(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap-index.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<sitemapindex><sitemap><loc>%s/child.xml</loc></sitemap></sitemapindex>`, srvURL)
	})
	mux.HandleFunc("/child.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/a</loc></url><url><loc>%s/b</loc></url></urlset>`, srvURL, srvURL)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	urls, err := FetchSitemapURLs(context.Background(), config.Default(), srv.URL+"/sitemap-index.xml")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range urls {
		got[u] = true
	}
	if !got[srv.URL+"/a"] || !got[srv.URL+"/b"] {
		t.Errorf("FetchSitemapURLs = %v, want both /a and /b", urls)
	}
	if len(urls) != 2 {
		t.Errorf("FetchSitemapURLs returned %d urls, want 2", len(urls))
	}
}

// TestFetchSitemapURLsErrors covers the error paths: a non-200 status and a
// body that is not valid sitemap XML.
func TestFetchSitemapURLsErrors(t *testing.T) {
	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer srv.Close()
		if _, err := FetchSitemapURLs(context.Background(), config.Default(), srv.URL+"/sitemap.xml"); err == nil {
			t.Error("expected an error for a 500 sitemap response")
		}
	})

	t.Run("malformed xml", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "<<<not xml>>>")
		}))
		defer srv.Close()
		if _, err := FetchSitemapURLs(context.Background(), config.Default(), srv.URL+"/sitemap.xml"); err == nil {
			t.Error("expected a parse error for malformed sitemap XML")
		}
	})
}

// TestCrawlLlmsTxtAdmitsCuratedLinks exercises crawlLlmsTxt end-to-end: a
// /llms.txt with a relative curated link is fetched, the link is resolved and
// admitted (crawl_linked on), and an /llms-full.txt is fetched too (fetch_full
// on) but contributes no curated links. The recorded sink rows are asserted.
func TestCrawlLlmsTxtAdmitsCuratedLinks(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":     "<p>root</p>",
		"/docs": "<p>docs</p>",
	})
	s.pages["/llms.txt"] = "# Site\n\n> A short summary.\n\n## Docs\n\n- [Docs](/docs): the docs\n"
	s.pages["/llms-full.txt"] = "# Site full\n\nlong prose"

	sink := &llmsRecordingSink{}
	cfg := config.Default()
	cfg.LlmsTxt.Check = true
	cfg.LlmsTxt.FetchFull = true
	cfg.LlmsTxt.CrawlLinked = true
	// Disable sitemap auto-discovery noise so the assertions stay focused.
	cfg.Sitemaps.CrawlLinked = false
	cfg.Sitemaps.AutoDiscoverViaRobots = false
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res := runCap(t, c, sink, s.server.URL+"/")

	if rec := s.page(res, "/docs"); rec == nil || rec.State != StateCrawled {
		t.Errorf("curated llms.txt link /docs must be crawled: %+v", rec)
	}
	if s.hitCount("/llms.txt") != 1 {
		t.Errorf("/llms.txt fetched %d times, want 1", s.hitCount("/llms.txt"))
	}
	if s.hitCount("/llms-full.txt") != 1 {
		t.Errorf("/llms-full.txt fetched %d times, want 1 (fetch_full on)", s.hitCount("/llms-full.txt"))
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	// Both files recorded; the primary llms.txt was found.
	if len(sink.files) != 2 {
		t.Errorf("recorded %d llms files, want 2", len(sink.files))
	}
	primaryFound := false
	for _, f := range sink.files {
		if f.Kind == "llms_txt" {
			primaryFound = f.Found
		}
	}
	if !primaryFound {
		t.Error("primary /llms.txt must be recorded as Found")
	}
	if len(sink.links) != 1 || sink.links[0].url != s.server.URL+"/docs" {
		t.Errorf("recorded curated links = %+v, want exactly /docs", sink.links)
	}
}

// TestCrawlLlmsTxtCrawlLinkedOff verifies that with crawl_linked off the curated
// links are still recorded on the sink but never admitted to the frontier.
func TestCrawlLlmsTxtCrawlLinkedOff(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<p>root</p>"})
	s.pages["/llms.txt"] = "# Site\n\n> Summary.\n\n## Docs\n\n- [Docs](/docs): docs\n"

	sink := &llmsRecordingSink{}
	cfg := config.Default()
	cfg.LlmsTxt.Check = true
	cfg.LlmsTxt.FetchFull = false
	cfg.LlmsTxt.CrawlLinked = false
	cfg.Sitemaps.CrawlLinked = false
	cfg.Sitemaps.AutoDiscoverViaRobots = false
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}

	if s.hitCount("/docs") != 0 {
		t.Error("with crawl_linked off, curated links must not be fetched")
	}
	if s.hitCount("/llms-full.txt") != 0 {
		t.Error("with fetch_full off, /llms-full.txt must not be fetched")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.links) != 1 {
		t.Errorf("curated link rows = %d, want 1 (recorded even when not crawled)", len(sink.links))
	}
}

// TestCrawlLlmsTxtMissing covers the not-found path: a host with no /llms.txt
// records the file as not Found and admits nothing.
func TestCrawlLlmsTxtMissing(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<p>root</p>"})
	sink := &llmsRecordingSink{}
	cfg := config.Default()
	cfg.LlmsTxt.Check = true
	cfg.LlmsTxt.FetchFull = false
	cfg.LlmsTxt.CrawlLinked = true
	cfg.Sitemaps.CrawlLinked = false
	cfg.Sitemaps.AutoDiscoverViaRobots = false
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.files) != 1 || sink.files[0].Found {
		t.Errorf("missing /llms.txt: files = %+v, want one record with Found=false", sink.files)
	}
	if len(sink.links) != 0 {
		t.Errorf("missing /llms.txt must yield no curated links, got %+v", sink.links)
	}
}

// TestRobotsSitemapsForIgnoreMode pins that sitemap discovery returns nothing in
// ignore mode (robots.txt is never downloaded) and that an unparseable URL also
// yields nil.
func TestRobotsSitemapsForIgnoreMode(t *testing.T) {
	cfg := config.Default()
	cfg.Robots.Mode = "ignore"
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newRobotsMgr(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.sitemapsFor(context.Background(), "http://example.com/"); got != nil {
		t.Errorf("sitemapsFor in ignore mode = %v, want nil", got)
	}

	cfg2 := config.Default()
	client2, _ := fetch.New(cfg2)
	m2, _ := newRobotsMgr(cfg2, client2)
	if got := m2.sitemapsFor(context.Background(), "://bad-url"); got != nil {
		t.Errorf("sitemapsFor on an unparseable URL = %v, want nil", got)
	}
}

// TestRobotsCheckUnparseableURL covers the check() guard for a URL that
// url.Parse rejects: the verdict must default to allowed.
func TestRobotsCheckUnparseableURL(t *testing.T) {
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newRobotsMgr(cfg, client)
	if err != nil {
		t.Fatal(err)
	}
	v := m.check(context.Background(), "http://[::1]:namedport/x")
	if !v.Allowed {
		t.Error("an unparseable URL must default to allowed")
	}
}

// TestRobotsCustomFileMissing pins newRobotsMgr's error path: a custom robots
// file that does not exist must surface an error from New.
func TestRobotsCustomFileMissing(t *testing.T) {
	cfg := config.Default()
	cfg.Robots.Custom = []config.CustomRobots{{Host: "example.com", File: "/no/such/robots.txt"}}
	if _, err := New(cfg); err == nil {
		t.Error("expected New to fail when a custom robots file is missing")
	}
}

// TestSinkErrorPropagated pins noteSinkErr: the first sink error is captured
// and returned from Run, and only the first is kept (sinkErrOnce).
func TestSinkErrorPropagated(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a"),
		"/a": "<p>x</p>",
	})
	sentinel := errors.New("sink boom")
	sink := &errSink{err: sentinel}
	cfg := config.Default()
	cfg.Sitemaps.CrawlLinked = false
	cfg.Sitemaps.AutoDiscoverViaRobots = false
	cfg.LlmsTxt.Check = false
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := c.Run(context.Background(), s.server.URL+"/")
	if !errors.Is(runErr, sentinel) {
		t.Errorf("Run error = %v, want the sink error %v", runErr, sentinel)
	}
}

// TestRegexReplaceRewriteRule exercises mustCompile (run from New when a
// url_rewriting.regex_replace rule is configured) and verifies the compiled
// rule rewrites discovered URLs: two links that differ only by a tracking
// query collapse to the same target, so the target is fetched exactly once.
func TestRegexReplaceRewriteRule(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":     `<a href="/page?utm_source=a">a</a><a href="/page?utm_source=b">b</a>`,
		"/page": "<p>page</p>",
	})
	res := crawl(t, s, func(c *config.Config) {
		// strip any ?utm_... query so both variants normalise to /page
		c.URLRewriting.RegexReplace = []config.RegexReplace{
			{Pattern: `\?utm_[^#]*`, Replace: ""},
		}
	})
	if rec := s.page(res, "/page"); rec == nil || rec.State != StateCrawled {
		t.Fatalf("/page = %+v, want crawled after the tracking query is stripped", rec)
	}
	if got := s.hitCount("/page"); got != 1 {
		t.Errorf("/page fetched %d times, want 1 (both utm variants rewritten to one URL)", got)
	}
}

// errSink fails every Page call, to drive noteSinkErr / sinkErr propagation.
type errSink struct{ err error }

func (s *errSink) Page(*PageRecord) error          { return s.err }
func (s *errSink) FrontierAdd(frontier.Item) error { return nil }
func (s *errSink) FrontierDone(string) error       { return nil }

// llmsRecordingSink captures llms.txt file and link rows in addition to the
// base Sink methods.
type llmsRecordingSink struct {
	mu    sync.Mutex
	pages map[string]*PageRecord
	files []LlmsTxtRecord
	links []llmsLinkRow
}

type llmsLinkRow struct {
	src, url, section, anchor string
}

func (s *llmsRecordingSink) Page(rec *PageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pages == nil {
		s.pages = map[string]*PageRecord{}
	}
	cp := *rec
	s.pages[rec.URL] = &cp
	return nil
}
func (s *llmsRecordingSink) snapshot() map[string]*PageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*PageRecord, len(s.pages))
	for k, v := range s.pages {
		out[k] = v
	}
	return out
}
func (s *llmsRecordingSink) FrontierAdd(frontier.Item) error { return nil }
func (s *llmsRecordingSink) FrontierDone(string) error       { return nil }
func (s *llmsRecordingSink) LlmsTxtFile(rec LlmsTxtRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = append(s.files, rec)
	return nil
}
func (s *llmsRecordingSink) LlmsTxtLink(src, url, section, anchor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links = append(s.links, llmsLinkRow{src, url, section, anchor})
	return nil
}
