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

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/frontier"
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

	res := crawl(t, s, nil)
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

	t.Run("all resource types are status-checked", func(t *testing.T) {
		s := newSite(t, pages)
		crawl(t, s, nil)
		for _, path := range []string{"/s.css", "/a.js", "/i.png", "/f.html"} {
			if s.hitCount(path) != 1 {
				t.Errorf("%s fetched %d times, want 1", path, s.hitCount(path))
			}
		}
	})

	t.Run("disabled types are not fetched", func(t *testing.T) {
		s := newSite(t, pages)
		crawl(t, s, func(c *config.Config) {
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
