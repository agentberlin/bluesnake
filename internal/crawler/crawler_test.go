package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// site is a tiny declarative fixture server.
type site struct {
	mu     sync.Mutex
	hits   map[string]int
	pages  map[string]string // path -> html body
	server *httptest.Server
}

func newSite(t *testing.T, pages map[string]string) *site {
	t.Helper()
	s := &site{hits: map[string]int{}, pages: pages}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		body, ok := s.pages[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *site) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func link(path string) string { return fmt.Sprintf(`<a href="%s">x</a>`, path) }

func crawl(t *testing.T, s *site, mutate func(*config.Config)) *Result {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (s *site) page(res *Result, path string) *PageRecord {
	return res.Pages[s.server.URL+path]
}

func TestBasicCrawl(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a") + link("/b"),
		"/a": link("/b") + link("/c"),
		"/b": link("/"),
		"/c": "<p>leaf</p>",
	})
	res := crawl(t, s, nil)

	if res.Crawled != 4 {
		t.Errorf("crawled = %d, want 4", res.Crawled)
	}
	for path, depth := range map[string]int{"/": 0, "/a": 1, "/b": 1, "/c": 2} {
		rec := s.page(res, path)
		if rec == nil || rec.State != StateCrawled {
			t.Errorf("%s not crawled: %+v", path, rec)
			continue
		}
		if rec.Depth != depth {
			t.Errorf("%s depth = %d, want %d", path, rec.Depth, depth)
		}
	}
	if s.hitCount("/b") != 1 {
		t.Errorf("/b fetched %d times, want 1 (dedup)", s.hitCount("/b"))
	}
	if rec := s.page(res, "/c"); rec == nil || !rec.Indexable {
		t.Error("/c must be indexable")
	}
	if rec := s.page(res, "/a"); rec.Inlinks != 1 || rec.DiscoveredFrom != s.server.URL+"/" {
		t.Errorf("/a inlinks=%d from=%s", rec.Inlinks, rec.DiscoveredFrom)
	}
}

func TestRobotsRespected(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":           link("/private/x") + link("/ok"),
		"/private/x":  "<p>secret</p>",
		"/ok":         "<p>ok</p>",
		"/robots.txt": "User-agent: *\nDisallow: /private/\n",
	})
	res := crawl(t, s, nil)

	if rec := s.page(res, "/private/x"); rec == nil || rec.State != StateBlockedRobots {
		t.Errorf("blocked page record = %+v", rec)
	} else if rec.MatchedRobotsLine != 2 {
		t.Errorf("matched line = %d, want 2", rec.MatchedRobotsLine)
	}
	if s.hitCount("/private/x") != 0 {
		t.Error("blocked URL must never be fetched")
	}
	if rec := s.page(res, "/ok"); rec == nil || rec.State != StateCrawled {
		t.Error("/ok must be crawled")
	}
}

func TestRobotsIgnoreMode(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":           link("/private/x"),
		"/private/x":  "<p>secret</p>",
		"/robots.txt": "User-agent: *\nDisallow: /private/\n",
	})
	res := crawl(t, s, func(c *config.Config) { c.Robots.Mode = "ignore" })

	if s.hitCount("/robots.txt") != 0 {
		t.Error("ignore mode must not fetch robots.txt")
	}
	if rec := s.page(res, "/private/x"); rec == nil || rec.StatusCode != 200 {
		t.Errorf("private page = %+v", rec)
	}
}

func TestRedirectDiscovery(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":    link("/old"),
		"/new": "<p>target</p>",
	})
	s.pages["/old"] = "" // replaced by handler below
	redirSrv := s.server
	_ = redirSrv
	// inject a redirect by wrapping: easier to use a dedicated handler page
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		body, ok := s.pages[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	})

	res := crawl(t, s, nil)
	old := s.page(res, "/old")
	if old == nil || old.StatusCode != 301 || old.RedirectType != "http" {
		t.Fatalf("/old = %+v", old)
	}
	if old.Indexable {
		t.Error("redirect must be non-indexable")
	}
	if rec := s.page(res, "/new"); rec == nil || rec.State != StateCrawled {
		t.Errorf("/new = %+v", rec)
	}
}

func TestMaxURLs(t *testing.T) {
	pages := map[string]string{}
	var links strings.Builder
	for i := range 30 {
		path := fmt.Sprintf("/p%d", i)
		links.WriteString(link(path))
		pages[path] = "<p>x</p>"
	}
	pages["/"] = links.String()
	s := newSite(t, pages)

	res := crawl(t, s, func(c *config.Config) { c.Limits.MaxURLs = 10 })
	if res.Crawled > 10 {
		t.Errorf("crawled %d, want <= 10", res.Crawled)
	}
}

func TestNofollowNotFollowed(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":       `<a href="/hidden" rel="nofollow">x</a>`,
		"/hidden": "<p>x</p>",
	})
	crawl(t, s, nil)
	if s.hitCount("/hidden") != 0 {
		t.Error("nofollow target must not be fetched by default")
	}

	s2 := newSite(t, map[string]string{
		"/":       `<a href="/hidden" rel="nofollow">x</a>`,
		"/hidden": "<p>x</p>",
	})
	res := crawl(t, s2, func(c *config.Config) { c.Scope.FollowInternalNofollow = true })
	if rec := s2.page(res, "/hidden"); rec == nil || rec.State != StateCrawled {
		t.Error("follow_internal_nofollow must enable crawling")
	}
}

func TestExternalStatusCheckedNotFollowed(t *testing.T) {
	ext := newSite(t, map[string]string{
		"/page":   link("/onward"),
		"/onward": "<p>x</p>",
	})
	s := newSite(t, map[string]string{
		"/": fmt.Sprintf(`<a href="%s/page">ext</a>`, ext.server.URL),
	})

	res := crawl(t, s, func(c *config.Config) {
		c.Links.External = config.StoreCrawl{Store: true, Crawl: true}
	})
	extRec := res.Pages[ext.server.URL+"/page"]
	if extRec == nil || extRec.Scope != "external" || extRec.StatusCode != 200 {
		t.Fatalf("external record = %+v", extRec)
	}
	if extRec.Facts != nil {
		t.Error("external pages must not be parsed")
	}
	if ext.hitCount("/onward") != 0 {
		t.Error("external outlinks must not be crawled")
	}
}

func TestStartFolderScoping(t *testing.T) {
	pages := map[string]string{
		"/blog/":     link("/blog/post") + link("/about"),
		"/blog/post": "<p>post</p>",
		"/about":     link("/other"),
		"/other":     "<p>x</p>",
	}

	t.Run("default: outside checked but not followed", func(t *testing.T) {
		s := newSite(t, pages)
		cfg := config.Default()
		c, _ := New(cfg)
		res, err := c.Run(context.Background(), s.server.URL+"/blog/")
		if err != nil {
			t.Fatal(err)
		}
		if rec := res.Pages[s.server.URL+"/about"]; rec == nil || !rec.OutsideStartFolder {
			t.Errorf("/about = %+v", rec)
		}
		if s.hitCount("/other") != 0 {
			t.Error("links on outside-folder pages must not be followed")
		}
	})

	t.Run("crawl outside enabled follows onward", func(t *testing.T) {
		s := newSite(t, pages)
		cfg := config.Default()
		cfg.Scope.CrawlOutsideStartFolder = true
		c, _ := New(cfg)
		if _, err := c.Run(context.Background(), s.server.URL+"/blog/"); err != nil {
			t.Fatal(err)
		}
		if s.hitCount("/other") != 1 {
			t.Error("crawl_outside_start_folder must follow onward links")
		}
	})

	t.Run("check disabled never leaves the folder", func(t *testing.T) {
		s := newSite(t, pages)
		cfg := config.Default()
		cfg.Scope.CheckLinksOutsideStartFolder = false
		c, _ := New(cfg)
		if _, err := c.Run(context.Background(), s.server.URL+"/blog/"); err != nil {
			t.Fatal(err)
		}
		if s.hitCount("/about") != 0 {
			t.Error("/about must not be fetched at all")
		}
	})
}

func TestRedirectChainLimit(t *testing.T) {
	pages := map[string]string{"/end": "<p>end</p>"}
	s := newSite(t, pages)
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		var i int
		if n, _ := fmt.Sscanf(r.URL.Path, "/r%d", &i); n == 1 {
			next := fmt.Sprintf("/r%d", i+1)
			if i == 7 {
				next = "/end"
			}
			http.Redirect(w, r, next, http.StatusMovedPermanently)
			return
		}
		if r.URL.Path == "/end" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, pages["/end"])
			return
		}
		w.WriteHeader(404)
	})

	cfg := config.Default()
	cfg.Limits.MaxRedirects = 5
	c, _ := New(cfg)
	if _, err := c.Run(context.Background(), s.server.URL+"/r0"); err != nil {
		t.Fatal(err)
	}
	requested := 0
	for i := range 8 {
		if s.hitCount(fmt.Sprintf("/r%d", i)) > 0 {
			requested++
		}
	}
	if requested != 6 {
		t.Errorf("chain URLs requested = %d, want 6 (head + 5 redirects)", requested)
	}
}

func TestExcludePattern(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":          link("/keep") + link("/skip/this"),
		"/keep":      "<p>x</p>",
		"/skip/this": "<p>x</p>",
	})
	res := crawl(t, s, func(c *config.Config) { c.Scope.Exclude = []string{"/skip/"} })
	if s.hitCount("/skip/this") != 0 {
		t.Error("excluded URL must not be fetched")
	}
	if rec := s.page(res, "/keep"); rec == nil {
		t.Error("/keep must be crawled")
	}
}

func TestInterrupt(t *testing.T) {
	pages := map[string]string{}
	var links strings.Builder
	for i := range 50 {
		path := fmt.Sprintf("/p%d", i)
		links.WriteString(link(path))
		pages[path] = "<p>x</p>"
	}
	pages["/"] = links.String()
	s := newSite(t, pages)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before start: only in-flight work may complete
	cfg := config.Default()
	c, _ := New(cfg)
	res, err := c.Run(ctx, s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted {
		t.Error("result must be flagged interrupted")
	}
}

func TestResourceLinks(t *testing.T) {
	pages := map[string]string{
		"/": `<html><head><link rel="stylesheet" href="/s.css"><script src="/a.js"></script></head>` +
			`<body><img src="/i.png"><iframe src="/f.html"></iframe></body></html>`,
		"/s.css":  "body{}",
		"/a.js":   "var x;",
		"/i.png":  "PNG",
		"/f.html": "<p>frame</p>",
	}

	enableAll := func(c *config.Config) {
		c.Resources.Images = config.StoreCrawl{Store: true, Crawl: true}
		c.Resources.CSS = config.StoreCrawl{Store: true, Crawl: true}
		c.Resources.JavaScript = config.StoreCrawl{Store: true, Crawl: true}
	}

	t.Run("all resource types are status-checked", func(t *testing.T) {
		s := newSite(t, pages)
		crawl(t, s, enableAll)
		for _, path := range []string{"/s.css", "/a.js", "/i.png", "/f.html"} {
			if s.hitCount(path) != 1 {
				t.Errorf("%s fetched %d times, want 1", path, s.hitCount(path))
			}
		}
	})

	t.Run("disabled types are not fetched", func(t *testing.T) {
		s := newSite(t, pages)
		crawl(t, s, func(c *config.Config) {
			enableAll(c)
			c.Resources.Images.Crawl = false
			c.Resources.CSS.Crawl = false
		})
		if s.hitCount("/i.png") != 0 || s.hitCount("/s.css") != 0 {
			t.Error("disabled resource types must not be fetched")
		}
		if s.hitCount("/a.js") != 1 {
			t.Error("javascript still enabled, must be fetched")
		}
	})
}

func TestRateLimiter(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a") + link("/b"),
		"/a": "<p>x</p>",
		"/b": "<p>x</p>",
	})
	res := crawl(t, s, func(c *config.Config) { c.Speed.MaxURLsPerSec = 100 })
	if res.Crawled != 3 {
		t.Errorf("crawled = %d, want 3", res.Crawled)
	}
}

func TestCustomRobotsOverride(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/robots.txt"
	if err := os.WriteFile(file, []byte("User-agent: *\nDisallow: /shop/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSite(t, map[string]string{
		"/":           link("/shop/item") + link("/ok"),
		"/shop/item":  "<p>x</p>",
		"/ok":         "<p>x</p>",
		"/robots.txt": "User-agent: *\nDisallow: /ok\n", // live file says the opposite
	})
	res := crawl(t, s, func(c *config.Config) {
		c.Robots.Custom = []config.CustomRobots{{Host: "127.0.0.1", File: file}}
	})
	if rec := s.page(res, "/shop/item"); rec == nil || rec.State != StateBlockedRobots {
		t.Errorf("/shop/item = %+v, want blocked by custom robots", rec)
	}
	if rec := s.page(res, "/ok"); rec == nil || rec.State != StateCrawled {
		t.Errorf("/ok = %+v, want crawled (live robots replaced)", rec)
	}
	if s.hitCount("/robots.txt") != 0 {
		t.Error("live robots.txt must not be fetched when a custom file is configured")
	}
}

func TestIgnoreReportMode(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":           link("/private/x"),
		"/private/x":  "<p>x</p>",
		"/robots.txt": "User-agent: *\nDisallow: /private/\n",
	})
	res := crawl(t, s, func(c *config.Config) { c.Robots.Mode = "ignore-report" })
	if s.hitCount("/robots.txt") != 1 {
		t.Errorf("robots.txt fetched %d times, want 1", s.hitCount("/robots.txt"))
	}
	if rec := s.page(res, "/private/x"); rec == nil || rec.StatusCode != 200 {
		t.Errorf("private page = %+v, want crawled despite disallow", rec)
	}
}

func TestMetaRefreshDiscovery(t *testing.T) {
	s := newSite(t, map[string]string{
		"/": link("/m"),
		"/m": `<html><head><meta http-equiv="refresh" content="0;url=/target"></head>` +
			`<body></body></html>`,
		"/target": "<p>x</p>",
	})
	res := crawl(t, s, nil)
	if rec := s.page(res, "/m"); rec == nil || rec.RedirectType != "meta_refresh" {
		t.Errorf("/m = %+v, want meta_refresh redirect type", rec)
	}
	if rec := s.page(res, "/target"); rec == nil || rec.State != StateCrawled {
		t.Errorf("/target = %+v, want crawled", rec)
	}
}

func TestMaxLinksPerPage(t *testing.T) {
	pages := map[string]string{}
	var links strings.Builder
	for i := range 10 {
		path := fmt.Sprintf("/p%d", i)
		links.WriteString(link(path))
		pages[path] = "<p>x</p>"
	}
	pages["/"] = links.String()
	s := newSite(t, pages)
	res := crawl(t, s, func(c *config.Config) { c.Limits.MaxLinksPerPage = 4 })
	if res.Crawled != 5 { // root + first 4 links
		t.Errorf("crawled = %d, want 5", res.Crawled)
	}
}

func TestIncludePattern(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":       link("/blog/a") + link("/shop/b"),
		"/blog/a": "<p>x</p>",
		"/shop/b": "<p>x</p>",
	})
	crawl(t, s, func(c *config.Config) { c.Scope.Include = []string{"/blog/"} })
	if s.hitCount("/shop/b") != 0 {
		t.Error("non-included URL must not be fetched")
	}
	if s.hitCount("/blog/a") != 1 {
		t.Error("included URL must be fetched")
	}
}

func TestInvalidLinksSkipped(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":   `<a href="hppts://broken/">bad</a>` + link("/ok"),
		"/ok": "<p>x</p>",
	})
	res := crawl(t, s, nil)
	for url := range res.Pages {
		if strings.Contains(url, "broken") {
			t.Errorf("invalid URL recorded: %s", url)
		}
	}
}

// recordingSink captures sink calls including sitemap entries.
type recordingSink struct {
	mu       sync.Mutex
	pages    []string
	sitemaps map[string][]string
}

func (s *recordingSink) Page(rec *PageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages = append(s.pages, rec.URL)
	return nil
}
func (s *recordingSink) FrontierAdd(frontier.Item) error { return nil }
func (s *recordingSink) FrontierDone(string) error       { return nil }
func (s *recordingSink) SitemapEntry(sitemap, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sitemaps == nil {
		s.sitemaps = map[string][]string{}
	}
	s.sitemaps[url] = append(s.sitemaps[url], sitemap)
	return nil
}

func TestSitemapCrawling(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":       link("/linked"),
		"/linked": "<p>x</p>",
		"/orphan": "<p>orphan</p>",
	})
	// sitemap index -> child sitemap -> urls
	s.pages["/sitemap-index.xml"] = `<sitemapindex><sitemap><loc>` + s.server.URL + `/sitemap.xml</loc></sitemap></sitemapindex>`
	s.pages["/sitemap.xml"] = `<urlset><url><loc>` + s.server.URL + `/linked</loc></url><url><loc>` + s.server.URL + `/orphan</loc></url></urlset>`

	sink := &recordingSink{}
	cfg := config.Default()
	cfg.Sitemaps.CrawlLinked = true
	cfg.Sitemaps.URLs = []string{s.server.URL + "/sitemap-index.xml"}
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rec := s.page(res, "/orphan"); rec == nil || rec.State != StateCrawled {
		t.Errorf("sitemap-only page must be crawled: %+v", rec)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if got := sink.sitemaps[s.server.URL+"/orphan"]; len(got) != 1 {
		t.Errorf("sitemap entries for /orphan = %v", got)
	}
}

func TestSitemapAutoDiscovery(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":            "<p>root</p>",
		"/from-robots": "<p>x</p>",
	})
	s.pages["/robots.txt"] = "User-agent: *\nAllow: /\nSitemap: " + s.server.URL + "/found.xml\n"
	s.pages["/found.xml"] = `<urlset><url><loc>` + s.server.URL + `/from-robots</loc></url></urlset>`

	cfg := config.Default()
	cfg.Sitemaps.CrawlLinked = true
	cfg.Sitemaps.AutoDiscoverViaRobots = true
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rec := s.page(res, "/from-robots"); rec == nil || rec.State != StateCrawled {
		t.Errorf("robots-discovered sitemap URL must be crawled: %+v", rec)
	}
}

// R17: sitemap auto-discovery must run for EACH in-scope host the crawl enters,
// not just the seed host. A second in-scope host (a subdomain in the wild, e.g.
// docs.zenskar.com; modelled here as an in-scope CDN host so two httptest servers
// stay distinct) advertises its sitemap only in its own robots.txt. A page on it
// is reachable by link, but a sitemap-only page on it is reachable ONLY via that
// host's sitemap — which the seed host's robots.txt never references. Before this
// fix the second host's sitemap was never read and its sitemap-only pages were
// missed (zenskar text pass: 163 docs.zenskar.com/reference/* pages).
func TestSitemapAutoDiscoveryPerHost(t *testing.T) {
	// second in-scope host: a linked page + a robots-advertised sitemap + a
	// sitemap-only orphan that is NOT linked from anywhere.
	b := newSite(t, map[string]string{
		"/linked":       "<p>b linked</p>",
		"/sitemap-only": "<p>b orphan</p>",
	})
	b.pages["/robots.txt"] = "User-agent: *\nAllow: /\nSitemap: " + b.server.URL + "/found.xml\n"
	b.pages["/found.xml"] = `<urlset><url><loc>` + b.server.URL + `/sitemap-only</loc></url></urlset>`

	// seed host links only to the second host's /linked page, so the crawl
	// enters the second host but never sees its /sitemap-only orphan via a link.
	s := newSite(t, map[string]string{"/": ""})
	s.pages["/"] = link(b.server.URL + "/linked")

	cfg := config.Default()
	cfg.Sitemaps.CrawlLinked = true
	cfg.Sitemaps.AutoDiscoverViaRobots = true
	cfg.Scope.CDNs = []string{urlutil.Authority(b.server.URL)} // 2nd host is in-scope
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rec := res.Pages[b.server.URL+"/linked"]; rec == nil || rec.State != StateCrawled {
		t.Fatalf("second-host linked page must be crawled (host must be entered): %+v", rec)
	}
	if rec := res.Pages[b.server.URL+"/sitemap-only"]; rec == nil || rec.State != StateCrawled {
		t.Errorf("second-host sitemap-only page must be crawled via that host's own sitemap: %+v", rec)
	}
}

// Robots.txt served behind a redirect (the common apex→www 308) must be
// followed — Google's REP allows five hops. A dead-end here used to read as
// allow-all with no Sitemap directives, silently disabling both rule
// enforcement and sitemap auto-discovery (measured against Screaming Frog
// on happyrobot.ai / greptile.com / artisan.co, 2026-06-12).
func TestRobotsRedirectFollowed(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/real-robots.txt", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/real-robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "User-agent: *\nDisallow: /private/\nSitemap: %s/map.xml\n", srvURL)
	})
	mux.HandleFunc("/map.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/orphan</loc></url></urlset>`, srvURL)
	})
	for _, p := range []string{"/", "/orphan", "/private/x"} {
		body := `<p>x</p>`
		if p == "/" {
			body = `<a href="/private/x">x</a>`
		}
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, body)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	cfg := config.Default()
	cfg.Sitemaps.CrawlLinked = true
	cfg.Sitemaps.AutoDiscoverViaRobots = true
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rec := res.Pages[srv.URL+"/orphan"]; rec == nil || rec.State != StateCrawled {
		t.Errorf("sitemap URL behind redirected robots.txt not crawled: %+v", rec)
	}
	if rec := res.Pages[srv.URL+"/private/x"]; rec == nil || rec.State != StateBlockedRobots {
		t.Errorf("/private/x = %+v, want blocked by the redirect-target robots rules", rec)
	}
}

// An internal page redirecting off-site must not cause a fetch of the
// external target while external link crawling is off (greptile.com's oauth
// chain pulled gitlab.com pages into the crawl, 2026-06-12).
func TestExternalRedirectTargetNotFetched(t *testing.T) {
	extHits := 0
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extHits++
		fmt.Fprint(w, "<p>x</p>")
	}))
	defer ext.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<a href="/away">x</a>`)
	})
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ext.URL+"/landing", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Default() // external links: store/crawl off
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rec := res.Pages[srv.URL+"/away"]; rec == nil || rec.RedirectURL == "" {
		t.Fatalf("redirect source = %+v, want recorded redirect", rec)
	}
	if extHits != 0 {
		t.Errorf("external redirect target fetched %d times, want 0", extHits)
	}
	if rec := res.Pages[ext.URL+"/landing"]; rec != nil {
		t.Errorf("external redirect target admitted: %+v", rec)
	}
}

// Inlinks counts hyperlink edges only (SF's "Inlinks" column): a redirect
// source still sets discovered-from but does not count (yonedalabs.com was
// reported one high on every redirect target, 2026-06-12).
func TestInlinksCountHyperlinksOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<a href="/moved">x</a>`)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<p>x</p>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	moved, final := res.Pages[srv.URL+"/moved"], res.Pages[srv.URL+"/final"]
	if moved == nil || moved.Inlinks != 1 {
		t.Errorf("/moved inlinks = %+v, want 1 (the hyperlink)", moved)
	}
	if final == nil || final.Inlinks != 0 {
		t.Errorf("/final inlinks = %+v, want 0 (redirect source doesn't count)", final)
	}
	if final != nil && final.DiscoveredFrom != srv.URL+"/moved" {
		t.Errorf("/final discovered-from = %q, want the redirect source", final.DiscoveredFrom)
	}
}

// TestIdenticalContentShortCircuit pins the identical-content crawl
// short-circuit (R8 / sweetgreen order.* SPA-shell balloon). Every path the
// maze serves returns BYTE-IDENTICAL HTML whose two links are RELATIVE
// ("a/", "b/"), so each URL resolves them to a different child pair — the
// renderer-free analogue of a client-routed shell that injects a different
// per-URL link set from one identical body. Without the short-circuit this
// fans out as an unbounded binary tree of identical pages; with it, only the
// first identical body is ever expanded.
func TestIdenticalContentShortCircuit(t *testing.T) {
	const body = `<html><body><a href="a/">A</a><a href="b/">B</a></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	run := func(skip bool) *Result {
		t.Helper()
		cfg := config.Default()
		cfg.Advanced.SkipIdenticalContentLinks = skip
		cfg.Limits.MaxDepth = 3 // bounds the control crawl; the fix needs no bound
		c, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		res, err := c.Run(context.Background(), srv.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	on := run(true)
	if on.Crawled != 3 {
		t.Fatalf("with short-circuit: crawled = %d, want 3 (seed + 2 identical children, no further expansion)", on.Crawled)
	}
	if rec := on.Pages[srv.URL+"/"]; rec == nil || rec.DuplicateOf != "" {
		t.Errorf("seed must be the canonical page (DuplicateOf empty), got %+v", rec)
	}
	for _, child := range []string{"/a/", "/b/"} {
		rec := on.Pages[srv.URL+child]
		if rec == nil {
			t.Fatalf("%s not recorded", child)
		}
		if rec.DuplicateOf != srv.URL+"/" {
			t.Errorf("%s DuplicateOf = %q, want the seed URL", child, rec.DuplicateOf)
		}
	}
	for _, grandchild := range []string{"/a/a/", "/a/b/", "/b/a/", "/b/b/"} {
		if on.Pages[srv.URL+grandchild] != nil {
			t.Errorf("%s was crawled — a byte-identical shell was expanded", grandchild)
		}
	}

	off := run(false)
	if off.Crawled < 15 {
		t.Fatalf("control (short-circuit off, depth<=3): crawled = %d, want the identical-body maze to balloon (>=15)", off.Crawled)
	}
}

// TestFirstWithContentClaimGating pins that a page only claims a content hash
// as its canonical when it will actually expand (claim=true). A non-expanding
// page (outside the start folder, no link discovery) must NOT become canonical
// for a hash, otherwise it could shadow a later in-folder byte-identical twin
// and suppress that twin's in-scope outlinks. This is deterministic where the
// end-to-end crawl order is not.
func TestFirstWithContentClaimGating(t *testing.T) {
	c := &Crawler{seenContent: map[string]string{}}
	const h = "deadbeef"

	// An outside-folder page sees the hash first but does NOT claim it.
	if canon, first := c.firstWithContent(h, "http://x/outside", false); !first || canon != "http://x/outside" {
		t.Fatalf("non-expanding first call: canon=%q first=%v, want itself + true", canon, first)
	}
	// Because the outside page never claimed it, an in-folder page with the
	// same body is still treated as first and claims the hash now.
	if canon, first := c.firstWithContent(h, "http://x/in-folder", true); !first || canon != "http://x/in-folder" {
		t.Fatalf("in-folder claim: canon=%q first=%v, want itself + true (outside page must not have claimed)", canon, first)
	}
	// A subsequent identical page is now a duplicate of the in-folder canonical.
	if canon, first := c.firstWithContent(h, "http://x/twin", true); first || canon != "http://x/in-folder" {
		t.Fatalf("later twin: canon=%q first=%v, want canonical=in-folder + false", canon, first)
	}
}

// TestDistinctContentNotDeduped guards against false dedup: pages that share a
// layout/nav shell but differ in content are NOT byte-identical, so every one
// must be crawled AND expanded (only full byte identity short-circuits, never
// a near-duplicate).
func TestDistinctContentNotDeduped(t *testing.T) {
	shell := func(name, extra string) string {
		return `<html><body><nav><a href="/">home</a></nav><h1>` + name + `</h1>` + extra + `</body></html>`
	}
	s := newSite(t, map[string]string{
		"/":      shell("Home", link("/p1")+link("/p2")),
		"/p1":    shell("Page One", link("/leaf1")),
		"/p2":    shell("Page Two", link("/leaf2")),
		"/leaf1": shell("Leaf One", ""),
		"/leaf2": shell("Leaf Two", ""),
	})
	res := crawl(t, s, nil) // default config: short-circuit ON

	for _, p := range []string{"/", "/p1", "/p2", "/leaf1", "/leaf2"} {
		rec := s.page(res, p)
		if rec == nil || rec.State != StateCrawled {
			t.Errorf("%s not crawled (false dedup of distinct content?): %+v", p, rec)
			continue
		}
		if rec.DuplicateOf != "" {
			t.Errorf("%s wrongly marked duplicate of %s", p, rec.DuplicateOf)
		}
	}
	if res.Crawled != 5 {
		t.Errorf("crawled = %d, want 5 (leaves reachable only by expanding distinct pages)", res.Crawled)
	}
}

// TestRecomputeDepthsReconstructsFreshDepths verifies the exported recompute
// (used by the resume finalize) reproduces a fresh crawl's shortest-path depths
// when run over the full graph — even when the records arrive with stale depths,
// as they do when reloaded from the store on resume. This is the SF-parity
// invariant for resume: resume depth == fresh depth == Screaming Frog depth.
func TestRecomputeDepthsReconstructsFreshDepths(t *testing.T) {
	// /e is reachable by a short path (/->/c->/e = 2) and a long one
	// (/->/a->/b->/d->/e = 4); the shortest must win regardless of how the
	// records were discovered.
	s := newSite(t, map[string]string{
		"/":  link("/a") + link("/c"),
		"/a": link("/b"),
		"/b": link("/d"),
		"/c": link("/e"),
		"/d": link("/e"),
		"/e": "<p>leaf</p>",
	})
	cfg := config.Default()
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	seed := s.server.URL + "/"
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: a fresh crawl already computes shortest-path depths.
	for path, d := range map[string]int{"/": 0, "/a": 1, "/c": 1, "/b": 2, "/e": 2, "/d": 3} {
		if rec := s.page(res, path); rec == nil || rec.Depth != d {
			t.Fatalf("fresh %s depth = %v, want %d", path, rec, d)
		}
	}
	want := map[string]int{}
	for url, rec := range res.Pages {
		want[url] = rec.Depth
	}

	// Simulate the resume state: the full two-session graph reloaded from the
	// store, but with stale/admit-time depths (here scrambled to a wrong value).
	// This is exactly what the resume finalize feeds RecomputeDepths.
	merged := map[string]*PageRecord{}
	for url, rec := range res.Pages {
		cp := *rec
		cp.Depth = 999
		merged[url] = &cp
	}
	// An orphan with no followed-link path must end up NoDepth, not 999.
	orphan := s.server.URL + "/orphan"
	merged[orphan] = &PageRecord{URL: orphan, Depth: 999}

	c.RecomputeDepths(merged, seed)

	for url, w := range want {
		if got := merged[url].Depth; got != w {
			t.Errorf("recomputed %s depth = %d, want %d", url, got, w)
		}
	}
	if got := merged[orphan].Depth; got != NoDepth {
		t.Errorf("orphan depth = %d, want NoDepth (%d)", got, NoDepth)
	}
}

// TestResumePreservesDiscoveredFrom verifies a page first linked before an
// interrupt keeps its original discoverer on resume: the crawler seeds the
// first-discoverer from the stored frontier Source, since the page that linked
// it is not re-processed this session.
func TestResumePreservesDiscoveredFrom(t *testing.T) {
	s := newSite(t, map[string]string{
		"/pending": "<p>leaf</p>",
	})
	seed := s.server.URL + "/"
	cfg := config.Default()
	c, err := New(cfg, WithResume(
		[]string{seed}, // already processed: the seed is not re-crawled
		[]frontier.Item{{URL: s.server.URL + "/pending", Depth: 1, Source: seed}},
	))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	rec := s.page(res, "/pending")
	if rec == nil {
		t.Fatal("/pending was not crawled on resume")
	}
	if rec.DiscoveredFrom != seed {
		t.Errorf("DiscoveredFrom = %q, want the stored frontier source %q", rec.DiscoveredFrom, seed)
	}
}
