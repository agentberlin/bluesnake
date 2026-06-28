package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/extract"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/urlutil"
	"github.com/cucumber/godog"
)

func (w *world) registerCrawlSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a site page "([^"]*)" linking to "([^"]*)"$`, w.sitePageLinking)
	sc.Step(`^a site page "([^"]*)" with body:$`, w.sitePageBody)
	sc.Step(`^a site page "([^"]*)" with body "([^"]*)"$`, w.sitePageBodyInline)
	sc.Step(`^a site page "([^"]*)" with a script that injects a link to "([^"]*)"$`, w.sitePageInjectedLink)
	sc.Step(`^a site page "([^"]*)" with a script that injects an image "([^"]*)"$`, w.sitePageInjectedImage)
	sc.Step(`^a site page "([^"]*)" with a script that fetches "([^"]*)"$`, w.sitePageFetchScript)
	sc.Step(`^a site page "([^"]*)" with (\d+) generated links$`, w.sitePageGenerated)
	sc.Step(`^a site page "([^"]*)" with (\d+) generated links under "([^"]*)" and (\d+) under "([^"]*)"$`, w.sitePageGeneratedSplit)
	sc.Step(`^a site page "([^"]*)" linking to a path of (\d+) characters$`, w.sitePageLongLink)
	sc.Step(`^a second test server page "([^"]*)" linking onward to "([^"]*)"$`, w.secondServerPage)
	sc.Step(`^a site page "([^"]*)" redirecting to the external page "([^"]*)"$`, w.sitePageRedirectExternal)
	sc.Step(`^a test server redirect chain from "(/[^"]*)" of length (\d+)$`, w.redirectChain)
	sc.Step(`^the crawl config override "([^"]*)"(?: is set)?$`, w.addCrawlOverride)
	sc.Step(`^a path limit of (\d+) for "([^"]*)"$`, w.addPathLimit)
	sc.Step(`^a custom robots\.txt for the test server:$`, w.customRobots)
	sc.Step(`^the test server serves the background robots\.txt$`, w.serveBackgroundRobots)
	sc.Step(`^the test server serves the background robots\.txt behind a redirect$`, w.serveRedirectedRobots)
	sc.Step(`^I crawl the site$`, w.crawlSite)
	sc.Step(`^I crawl the site starting at "([^"]*)"$`, w.crawlSiteAt)

	sc.Step(`^(\d+) pages have crawl state "([^"]*)"$`, w.pagesHaveState)
	sc.Step(`^at most (\d+) pages have crawl state "([^"]*)"$`, w.atMostPagesHaveState)
	sc.Step(`^the crawl page "([^"]*)" has crawl state "([^"]*)"$`, w.crawlPageState)
	sc.Step(`^the crawl page "([^"]*)" has depth (\d+)$`, w.crawlPageDepth)
	sc.Step(`^the crawl page "([^"]*)" has status code (\d+)$`, w.crawlPageStatus)
	sc.Step(`^the crawl page "([^"]*)" has redirect type "([^"]*)"$`, w.crawlPageRedirectType)
	sc.Step(`^the crawl page "([^"]*)" is non-indexable in the crawl$`, w.crawlPageNonIndexable)
	sc.Step(`^the crawl page "([^"]*)" has matched robots line (\d+)$`, w.crawlPageRobotsLine)
	sc.Step(`^the crawl page "([^"]*)" is a duplicate of "([^"]*)"$`, w.crawlPageDuplicateOf)
	sc.Step(`^the crawl page "([^"]*)" is not a content duplicate$`, w.crawlPageNotDuplicate)
	sc.Step(`^the crawl page "([^"]*)" has no depth$`, w.crawlPageNoDepth)
	sc.Step(`^the crawl page "([^"]*)" has (\d+) inlinks?$`, w.crawlPageInlinks)
	sc.Step(`^the crawl page "([^"]*)" was discovered from "([^"]*)"$`, w.crawlPageDiscoveredFrom)
	sc.Step(`^the crawl has no page record for "([^"]*)"$`, w.crawlNoPageRecord)
	sc.Step(`^the page "([^"]*)" was not requested$`, w.pageNotRequested)
	sc.Step(`^the page "([^"]*)" was requested exactly (\d+) times$`, w.pageRequestedTimes)
	sc.Step(`^the external page "([^"]*)" has status code (\d+)$`, w.externalPageStatus)
	sc.Step(`^the external page "([^"]*)" is not parsed$`, w.externalPageNotParsed)
	sc.Step(`^the second server page "([^"]*)" was not requested$`, w.secondServerNotRequested)
	sc.Step(`^only (\d+) pages were requested$`, w.exactPagesRequested)
	sc.Step(`^at most (\d+) pages were requested$`, w.atMostPagesRequested)
	sc.Step(`^exactly (\d+) chain URLs under "([^"]*)" were requested$`, w.chainURLsRequested)
	sc.Step(`^at most (\d+) pages under "([^"]*)" were crawled$`, w.atMostPagesUnderCrawled)
	sc.Step(`^(\d+) pages under "([^"]*)" were crawled$`, w.pagesUnderCrawled)
}

// --- fixture building ---

func (w *world) sitePageLinking(path, targets string) error {
	var body strings.Builder
	body.WriteString("<html><body>")
	if targets != "" {
		for target := range strings.SplitSeq(targets, ",") {
			target = strings.TrimSpace(target)
			if w.extServer != nil {
				target = strings.ReplaceAll(target, "<external>", w.extServer.URL)
			}
			fmt.Fprintf(&body, `<a href="%s">link</a> `, target)
		}
	}
	body.WriteString("</body></html>")
	r := w.route(path)
	r.status, r.body = 200, body.String()
	return nil
}

func (w *world) sitePageBody(path string, doc *godog.DocString) error {
	return w.sitePageBodyInline(path, doc.Content)
}

func (w *world) sitePageBodyInline(path, body string) error {
	r := w.route(path)
	r.status, r.body = 200, body
	return nil
}

// sitePageInjectedLink serves a page whose only link to the target is
// created by JavaScript on DOMContentLoaded (@chrome rendering scenarios).
func (w *world) sitePageInjectedLink(path, target string) error {
	return w.sitePageBodyInline(path, fmt.Sprintf(`<html><head><title>js page</title></head>
<body><h1>js</h1><script>
document.addEventListener('DOMContentLoaded', function () {
  var a = document.createElement('a');
  a.href = %q; a.textContent = 'js link';
  document.body.appendChild(a);
});
</script></body></html>`, target))
}

// sitePageInjectedImage serves a page whose script injects an <img> (not a
// hyperlink) on load — used to prove js_contains_links counts rendered-only
// hyperlinks, not injected resources.
func (w *world) sitePageInjectedImage(path, target string) error {
	return w.sitePageBodyInline(path, fmt.Sprintf(`<html><head><title>js image page</title></head>
<body><h1>js</h1><script>
document.addEventListener('DOMContentLoaded', function () {
  var img = document.createElement('img');
  img.src = %q; document.body.appendChild(img);
});
</script></body></html>`, target))
}

// sitePageFetchScript serves a page that issues a fetch() to target on load —
// used to prove XHR/fetch endpoints observed during rendering are governed by
// resources.javascript and not enqueued as pages.
func (w *world) sitePageFetchScript(path, target string) error {
	return w.sitePageBodyInline(path, fmt.Sprintf(`<html><head><title>fetch page</title></head>
<body><h1>fetch</h1><script>
document.addEventListener('DOMContentLoaded', function () {
  fetch(%q);
});
</script></body></html>`, target))
}

func (w *world) sitePageGenerated(path string, n int) error {
	var body strings.Builder
	for i := range n {
		target := fmt.Sprintf("/gen/%d", i)
		fmt.Fprintf(&body, `<a href="%s">g</a> `, target)
		t := w.route(target)
		t.status, t.body = 200, "<p>x</p>"
	}
	r := w.route(path)
	r.status, r.body = 200, body.String()
	return nil
}

func (w *world) sitePageGeneratedSplit(path string, n1 int, prefix1 string, n2 int, prefix2 string) error {
	var body strings.Builder
	gen := func(n int, prefix string) {
		for i := range n {
			target := fmt.Sprintf("%sp%d", prefix, i)
			fmt.Fprintf(&body, `<a href="%s">g</a> `, target)
			t := w.route(target)
			t.status, t.body = 200, "<p>x</p>"
		}
	}
	gen(n1, prefix1)
	gen(n2, prefix2)
	r := w.route(path)
	r.status, r.body = 200, body.String()
	return nil
}

func (w *world) sitePageLongLink(path string, length int) error {
	target := "/" + strings.Repeat("x", length-1)
	t := w.route(target)
	t.status, t.body = 200, "<p>x</p>"
	r := w.route(path)
	r.status, r.body = 200, fmt.Sprintf(`<html><body><a href="%s">long</a></body></html>`, target)
	return nil
}

func (w *world) secondServerPage(path, onward string) error {
	w.extHits = map[string]int{}
	pages := map[string]string{
		path:   fmt.Sprintf(`<html><body><a href="%s">on</a></body></html>`, onward),
		onward: "<p>x</p>",
	}
	w.extServer = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.extMu.Lock()
		w.extHits[r.URL.Path]++
		w.extMu.Unlock()
		body, ok := pages[r.URL.Path]
		if !ok {
			rw.WriteHeader(404)
			return
		}
		rw.Header().Set("Content-Type", "text/html")
		fmt.Fprint(rw, body)
	}))
	return nil
}

// sitePageRedirectExternal stands up an external server serving extPath and
// makes the internal page redirect (301) to it, so the redirect TARGET is
// external. Used to prove external redirect targets obey links.external.crawl.
func (w *world) sitePageRedirectExternal(path, extPath string) error {
	w.extHits = map[string]int{}
	w.extServer = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.extMu.Lock()
		w.extHits[r.URL.Path]++
		w.extMu.Unlock()
		if r.URL.Path == extPath {
			rw.Header().Set("Content-Type", "text/html")
			fmt.Fprint(rw, "<html><body><p>external target</p></body></html>")
			return
		}
		rw.WriteHeader(404)
	}))
	r := w.route(path)
	r.status, r.redirectTo = 301, w.extServer.URL+extPath
	return nil
}

func (w *world) redirectChain(prefix string, length int) error {
	for i := range length {
		next := fmt.Sprintf("%s%d", prefix, i+1)
		if i == length-1 {
			next = "/end"
		}
		r := w.route(fmt.Sprintf("%s%d", prefix, i))
		r.status, r.redirectTo = 301, next
	}
	end := w.route("/end")
	end.status, end.body = 200, "<p>end</p>"
	return nil
}

func (w *world) addCrawlOverride(o string) error {
	w.crawlOverride = append(w.crawlOverride, o)
	return nil
}

func (w *world) addPathLimit(max int, pattern string) error {
	w.pathLimits = append(w.pathLimits, config.PathLimit{Pattern: pattern, Max: max})
	return nil
}

func (w *world) customRobots(doc *godog.DocString) error {
	w.customRobotsPath = filepath.Join(w.tmpDir, "custom-robots.txt")
	return os.WriteFile(w.customRobotsPath, []byte(doc.Content), 0o644)
}

func (w *world) serveBackgroundRobots() error {
	r := w.route("/robots.txt")
	r.status, r.body = 200, w.robotsContent
	return nil
}

// serveRedirectedRobots serves /robots.txt as a 308 to /real-robots.txt, which
// then carries the rules. Pins that robots fetching follows redirects (Google
// REP): without it the 308 would read as allow-all and skip rule enforcement.
func (w *world) serveRedirectedRobots() error {
	r := w.route("/robots.txt")
	r.status, r.redirectTo = 308, "/real-robots.txt"
	real := w.route("/real-robots.txt")
	real.status, real.body = 200, w.robotsContent
	return nil
}

// --- crawling ---

func (w *world) crawlSite() error { return w.crawlSiteAt("/") }

func (w *world) crawlSiteAt(path string) error {
	srv := w.ensureServer()
	cfg := config.Default()
	for _, o := range w.crawlOverride {
		if err := cfg.Set(o); err != nil {
			return err
		}
	}
	cfg.Limits.ByPath = append(cfg.Limits.ByPath, w.pathLimits...)
	if w.customRobotsPath != "" {
		cfg.Robots.Custom = []config.CustomRobots{
			{Host: urlutil.Host(srv.URL), File: w.customRobotsPath},
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	st, err := store.CreateCrawl(w.storeDirPath(), []string{srv.URL + path}, cfg.Mode, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	w.storedCrawlID = st.ID
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		return err
	}
	seed := srv.URL + path
	w.crawlResult, err = c.Run(context.Background(), seed)
	if err != nil {
		return err
	}
	return w.finalizeCrawlPages(c, st, w.crawlResult, seed)
}

// finalizeCrawlPages reproduces the store-backed finalize aggregate pass (the
// production path now that records stream to the store and are dropped from the
// live Result): persist seed-locked discovered_from + inlinks, recompute
// shortest-path depth and full-graph inlinks over the stored graph, and reload
// the finalized records into w.crawlPages for assertions.
func (w *world) finalizeCrawlPages(c *crawler.Crawler, st *store.Crawl, res *crawler.Result, seeds ...string) error {
	if err := st.SaveInlinkSources(res.Inlinks); err != nil {
		return err
	}
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	c.RecomputeDepths(pages, seeds...)
	c.RecomputeInlinks(pages)
	if err := st.SaveDepths(pages); err != nil {
		return err
	}
	if err := st.SaveInlinks(pages); err != nil {
		return err
	}
	if w.crawlPages, err = st.LoadPages(); err != nil {
		return err
	}
	// Custom search/extraction hits live in their own table — LoadPages only
	// rebuilds the pages row, so attach them the way production consumers
	// (export, desktop) read them: straight from custom_results.
	rows, err := st.DB().Query(`SELECT url, kind, name, value FROM custom_results`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var url string
		var cr extract.Result
		if err := rows.Scan(&url, &cr.Kind, &cr.Name, &cr.Value); err != nil {
			return err
		}
		if rec, ok := w.crawlPages[url]; ok {
			rec.CustomResults = append(rec.CustomResults, cr)
		}
	}
	return rows.Err()
}

// --- assertions ---

func (w *world) crawlPage(path string) *crawler.PageRecord {
	return w.crawlPages[w.server.URL+path]
}

func (w *world) countState(state string) int {
	n := 0
	for _, p := range w.crawlPages {
		if p.State == state {
			n++
		}
	}
	return n
}

func (w *world) pagesHaveState(count int, state string) error {
	if got := w.countState(state); got != count {
		return fmt.Errorf("%d pages with state %s, want %d", got, state, count)
	}
	return nil
}

func (w *world) atMostPagesHaveState(count int, state string) error {
	if got := w.countState(state); got > count {
		return fmt.Errorf("%d pages with state %s, want <= %d", got, state, count)
	}
	return nil
}

func (w *world) crawlPageState(path, state string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.State != state {
		return fmt.Errorf("%s state = %s, want %s", path, rec.State, state)
	}
	return nil
}

func (w *world) crawlPageDepth(path string, depth int) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.Depth != depth {
		return fmt.Errorf("%s depth = %d, want %d", path, rec.Depth, depth)
	}
	return nil
}

func (w *world) crawlPageStatus(path string, status int) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.StatusCode != status {
		return fmt.Errorf("%s status = %d, want %d", path, rec.StatusCode, status)
	}
	return nil
}

func (w *world) crawlPageRedirectType(path, typ string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.RedirectType != typ {
		return fmt.Errorf("%s redirect type = %q, want %q", path, rec.RedirectType, typ)
	}
	return nil
}

func (w *world) crawlPageNonIndexable(path string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.Indexable {
		return fmt.Errorf("%s is indexable, want non-indexable", path)
	}
	return nil
}

func (w *world) crawlPageRobotsLine(path string, line int) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.MatchedRobotsLine != line {
		return fmt.Errorf("%s matched line = %d, want %d", path, rec.MatchedRobotsLine, line)
	}
	return nil
}

// crawlPageDuplicateOf asserts the identical-content short-circuit recorded
// this page as a byte-for-byte duplicate of the given canonical page (which is
// the first page crawled with that raw-body hash).
func (w *world) crawlPageDuplicateOf(path, canonical string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	want := w.server.URL + canonical
	if rec.DuplicateOf != want {
		return fmt.Errorf("%s duplicate_of = %q, want %q", path, rec.DuplicateOf, want)
	}
	return nil
}

func (w *world) crawlPageNotDuplicate(path string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.DuplicateOf != "" {
		return fmt.Errorf("%s is a duplicate of %q, want none", path, rec.DuplicateOf)
	}
	return nil
}

// crawlPageNoDepth asserts the page has no followed-link path from a seed
// (NoDepth sentinel, exported blank) — e.g. a sitemap-only discovery.
func (w *world) crawlPageNoDepth(path string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.Depth != crawler.NoDepth {
		return fmt.Errorf("%s depth = %d, want no depth (%d)", path, rec.Depth, crawler.NoDepth)
	}
	return nil
}

func (w *world) crawlNoPageRecord(path string) error {
	if rec := w.crawlPage(path); rec != nil {
		return fmt.Errorf("unexpected crawl page record for %s (state %q)", path, rec.State)
	}
	return nil
}

func (w *world) crawlPageInlinks(path string, count int) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	if rec.Inlinks != count {
		return fmt.Errorf("%s inlinks = %d, want %d", path, rec.Inlinks, count)
	}
	return nil
}

func (w *world) crawlPageDiscoveredFrom(path, from string) error {
	rec := w.crawlPage(path)
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	want := w.server.URL + from
	if rec.DiscoveredFrom != want {
		return fmt.Errorf("%s discovered_from = %q, want %q", path, rec.DiscoveredFrom, want)
	}
	return nil
}

func (w *world) pageNotRequested(path string) error {
	return w.serverHits(0, path)
}

func (w *world) pageRequestedTimes(path string, times int) error {
	return w.serverHits(times, path)
}

func (w *world) externalPageStatus(path string, status int) error {
	rec := w.crawlPages[w.extServer.URL+path]
	if rec == nil {
		return fmt.Errorf("no record for external %s", path)
	}
	if rec.Scope != "external" {
		return fmt.Errorf("scope = %s, want external", rec.Scope)
	}
	if rec.StatusCode != status {
		return fmt.Errorf("external %s status = %d, want %d", path, rec.StatusCode, status)
	}
	return nil
}

func (w *world) externalPageNotParsed(path string) error {
	rec := w.crawlPages[w.extServer.URL+path]
	if rec == nil {
		return fmt.Errorf("no record for external %s", path)
	}
	if rec.Facts != nil {
		return fmt.Errorf("external page was parsed")
	}
	return nil
}

func (w *world) secondServerNotRequested(path string) error {
	w.extMu.Lock()
	defer w.extMu.Unlock()
	if w.extHits[path] != 0 {
		return fmt.Errorf("second server %s was requested %d times", path, w.extHits[path])
	}
	return nil
}

// wellKnownSiteFile reports whether a path is a site-level well-known file
// fetched out-of-band for every crawl (robots.txt, llms.txt) — never a crawled
// "page", so it's excluded from page-request counts.
func wellKnownSiteFile(path string) bool {
	switch path {
	case "/robots.txt", "/llms.txt", "/llms-full.txt":
		return true
	}
	return false
}

// requestedCount counts distinct requested paths, excluding well-known files.
func (w *world) requestedCount() int {
	w.hitsMu.Lock()
	defer w.hitsMu.Unlock()
	n := 0
	for path, hits := range w.hits {
		if hits > 0 && !wellKnownSiteFile(path) {
			n++
		}
	}
	return n
}

func (w *world) exactPagesRequested(count int) error {
	if got := w.requestedCount(); got != count {
		return fmt.Errorf("%d pages requested, want %d", got, count)
	}
	return nil
}

func (w *world) atMostPagesRequested(count int) error {
	if got := w.requestedCount(); got > count {
		return fmt.Errorf("%d pages requested, want <= %d", got, count)
	}
	return nil
}

func (w *world) chainURLsRequested(count int, prefix string) error {
	w.hitsMu.Lock()
	defer w.hitsMu.Unlock()
	got := 0
	for path, hits := range w.hits {
		if hits > 0 && strings.HasPrefix(path, prefix) && !wellKnownSiteFile(path) {
			got++
		}
	}
	if got != count {
		return fmt.Errorf("%d chain URLs requested under %s, want %d", got, prefix, count)
	}
	return nil
}

func (w *world) countCrawledUnder(prefix string) int {
	n := 0
	for url, p := range w.crawlPages {
		if p.State == crawler.StateCrawled && strings.HasPrefix(url, w.server.URL+prefix) {
			n++
		}
	}
	return n
}

func (w *world) atMostPagesUnderCrawled(count int, prefix string) error {
	if got := w.countCrawledUnder(prefix); got > count {
		return fmt.Errorf("%d pages crawled under %s, want <= %d", got, prefix, count)
	}
	return nil
}

func (w *world) pagesUnderCrawled(count int, prefix string) error {
	if got := w.countCrawledUnder(prefix); got != count {
		return fmt.Errorf("%d pages crawled under %s, want %d", got, prefix, count)
	}
	return nil
}
