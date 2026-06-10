package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/urlutil"
)

func (w *world) registerCrawlSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a site page "([^"]*)" linking to "([^"]*)"$`, w.sitePageLinking)
	sc.Step(`^a site page "([^"]*)" with body:$`, w.sitePageBody)
	sc.Step(`^a site page "([^"]*)" with body "([^"]*)"$`, w.sitePageBodyInline)
	sc.Step(`^a site page "([^"]*)" with (\d+) generated links$`, w.sitePageGenerated)
	sc.Step(`^a site page "([^"]*)" with (\d+) generated links under "([^"]*)" and (\d+) under "([^"]*)"$`, w.sitePageGeneratedSplit)
	sc.Step(`^a site page "([^"]*)" linking to a path of (\d+) characters$`, w.sitePageLongLink)
	sc.Step(`^a second test server page "([^"]*)" linking onward to "([^"]*)"$`, w.secondServerPage)
	sc.Step(`^a test server redirect chain from "(/[^"]*)" of length (\d+)$`, w.redirectChain)
	sc.Step(`^the crawl config override "([^"]*)"(?: is set)?$`, w.addCrawlOverride)
	sc.Step(`^a path limit of (\d+) for "([^"]*)"$`, w.addPathLimit)
	sc.Step(`^a custom robots\.txt for the test server:$`, w.customRobots)
	sc.Step(`^the test server serves the background robots\.txt$`, w.serveBackgroundRobots)
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
	c, err := crawler.New(cfg)
	if err != nil {
		return err
	}
	w.crawlResult, err = c.Run(context.Background(), srv.URL+path)
	return err
}

// --- assertions ---

func (w *world) crawlPage(path string) *crawler.PageRecord {
	return w.crawlResult.Pages[w.server.URL+path]
}

func (w *world) countState(state string) int {
	n := 0
	for _, p := range w.crawlResult.Pages {
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

func (w *world) pageNotRequested(path string) error {
	return w.serverHits(0, path)
}

func (w *world) pageRequestedTimes(path string, times int) error {
	return w.serverHits(times, path)
}

func (w *world) externalPageStatus(path string, status int) error {
	rec := w.crawlResult.Pages[w.extServer.URL+path]
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
	rec := w.crawlResult.Pages[w.extServer.URL+path]
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

// requestedCount counts distinct requested paths, excluding robots.txt.
func (w *world) requestedCount() int {
	w.hitsMu.Lock()
	defer w.hitsMu.Unlock()
	n := 0
	for path, hits := range w.hits {
		if hits > 0 && path != "/robots.txt" {
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
		if hits > 0 && strings.HasPrefix(path, prefix) && path != "/robots.txt" {
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
	for url, p := range w.crawlResult.Pages {
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
