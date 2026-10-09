package acceptance

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/cucumber/godog"
)

// onBlockWorld is the per-scenario state of the http.proxy_on_block feature:
// a site that starts refusing this machine's IP, a fallback proxy that can open
// CONNECT tunnels (the start-up probe uses one), and the crawl's outcome as
// recorded in a real store.
type onBlockWorld struct {
	site      *onBlockSite
	ext       *httptest.Server
	proxy     *onBlockProxy
	proxyURL  string
	overrides []string

	crawlErr error
	status   crawler.EgressStatus
	pages    map[string]*crawler.PageRecord // last record per URL
	calls    map[string]int                 // Page calls per URL
	egress   *crawler.EgressEvent
}

func (w *world) ob() *onBlockWorld {
	if w.onBlock == nil {
		w.onBlock = &onBlockWorld{}
	}
	return w.onBlock
}

func (w *world) closeOnBlock() {
	if w.onBlock == nil {
		return
	}
	if w.onBlock.site != nil {
		w.onBlock.site.srv.Close()
	}
	if w.onBlock.ext != nil {
		w.onBlock.ext.Close()
	}
	if w.onBlock.proxy != nil {
		w.onBlock.proxy.srv.Close()
	}
}

func (w *world) registerProxyOnBlockSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a blocking site whose home page links (\d+) pages and answers direct requests with 429 after (\d+) pages$`, w.obSite429)
	sc.Step(`^a blocking site whose home page links (\d+) pages and answers direct requests with a Cloudflare challenge after (\d+) pages$`, w.obSiteChallenge)
	sc.Step(`^the blocking site's page "([^"]*)" answers 403 on every route$`, w.obPage403)
	sc.Step(`^the blocking site links "([^"]*)" from its home page$`, w.obLinkFromHome)
	sc.Step(`^the blocking site's robots\.txt answers 429 directly and disallows "([^"]*)" through the proxy$`, w.obRobots)
	sc.Step(`^the blocking site also answers proxied page requests with 429$`, w.obProxied429)
	sc.Step(`^an external site answering (\d+), linked (\d+) times from the blocking site's home page$`, w.obExternal)
	sc.Step(`^a fallback proxy$`, func() error { return w.obProxy("", "") })
	sc.Step(`^a fallback proxy requiring username "([^"]*)" and password "([^"]*)"$`, w.obProxy)
	sc.Step(`^proxy_on_block is configured with the fallback proxy$`, func() error { return w.obConfigure("", "") })
	sc.Step(`^proxy_on_block is configured with the fallback proxy using username "([^"]*)" and password "([^"]*)"$`, w.obConfigure)
	sc.Step(`^the blocking crawl config "([^"]*)"$`, func(o string) error { w.ob().overrides = append(w.ob().overrides, o); return nil })
	sc.Step(`^I crawl the blocking site$`, func() error { return w.obCrawl(false) })
	sc.Step(`^I crawl the blocking site as a resume of a crawl that already switched$`, func() error { return w.obCrawl(true) })

	sc.Step(`^the crawl switched to the proxy$`, w.obSwitched)
	sc.Step(`^the crawl did not switch to the proxy$`, w.obNotSwitched)
	sc.Step(`^(\d+) pages were recorded, each exactly once$`, w.obRecordedOnce)
	sc.Step(`^every recorded page has status 200$`, w.obAll200)
	sc.Step(`^every page's recorded route is the route it really took$`, w.obRoutesTrue)
	sc.Step(`^pages were recorded both before and after the switch$`, w.obBothSides)
	sc.Step(`^no request reached the site directly after the switch$`, w.obNoDirectAfter)
	sc.Step(`^the switch is stored with the crawl for resume$`, w.obStored)
	sc.Step(`^the blocking site's page "([^"]*)" is recorded with status (\d+)$`, w.obPageStatus)
	sc.Step(`^the blocking site's page "([^"]*)" was fetched directly (\d+) times$`, w.obFetchedDirect)
	sc.Step(`^the fallback proxy carried no page requests$`, w.obProxyIdle)
	sc.Step(`^the blocking site's page "([^"]*)" is blocked by robots\.txt$`, w.obBlockedByRobots)
	sc.Step(`^the blocking site's page "([^"]*)" was never fetched through the proxy$`, w.obNeverProxied)
	sc.Step(`^the crawl counted responses still blocked through the proxy$`, w.obStillBlocked)
	sc.Step(`^the blocking crawl fails with "([^"]*)"$`, w.obFails)
	sc.Step(`^the blocking site received no requests$`, w.obNoRequests)
	sc.Step(`^the blocking site received no direct requests$`, w.obNoDirect)
	sc.Step(`^every page recorded through the proxy was rendered from the same page$`, w.obRendered)
}

// --- the site -------------------------------------------------------------------

type onBlockSite struct {
	srv         *httptest.Server
	pages       map[string]string
	allowDirect int
	challenge   bool
	proxied429  bool
	forbidden   map[string]bool
	robotsRules string // non-empty: robots.txt is 429 direct, these rules proxied

	mu           sync.Mutex
	directPages  int
	direct       map[string]int
	proxied      map[string]int
	firstProxied time.Time
	directAfter  int
}

func (w *world) obNewSite(n, allow int, challenge bool) error {
	s := &onBlockSite{allowDirect: allow, challenge: challenge, pages: map[string]string{},
		forbidden: map[string]bool{}, direct: map[string]int{}, proxied: map[string]int{}}
	var links []string
	for i := range n - 1 {
		p := fmt.Sprintf("/p%d", i)
		links = append(links, fmt.Sprintf(`<a href="%s">x</a>`, p))
		s.pages[p] = fmt.Sprintf("<html><head><title>page %d</title></head><body>leaf %d</body></html>", i, i)
	}
	s.pages["/"] = "<html><head><title>home</title></head><body>" + strings.Join(links, "") + "</body></html>"
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	w.ob().site = s
	return nil
}

// The site's page count includes its home page.
func (w *world) obSite429(n, allow int) error       { return w.obNewSite(n+1, allow, false) }
func (w *world) obSiteChallenge(n, allow int) error { return w.obNewSite(n+1, allow, true) }

func (w *world) obPage403(path string) error {
	s := w.ob().site
	s.forbidden[path] = true
	s.pages[path] = ""
	return w.obLinkFromHome(path)
}

func (w *world) obLinkFromHome(path string) error {
	s := w.ob().site
	s.pages["/"] = strings.Replace(s.pages["/"], "</body>", fmt.Sprintf(`<a href="%s">x</a></body>`, path), 1)
	if _, ok := s.pages[path]; !ok {
		s.pages[path] = "<p>" + path + "</p>"
	}
	return nil
}

func (w *world) obRobots(rule string) error {
	w.ob().site.robotsRules = "User-agent: *\nDisallow: " + rule + "\n"
	return nil
}

func (w *world) obProxied429() error { w.ob().site.proxied429 = true; return nil }

func (w *world) obExternal(status, n int) error {
	ob := w.ob()
	ob.ext = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(status) }))
	for i := range n {
		ob.site.pages["/"] = strings.Replace(ob.site.pages["/"], "</body>",
			fmt.Sprintf(`<a href="%s/x%d">ext</a></body>`, ob.ext.URL, i), 1)
	}
	return nil
}

func (s *onBlockSite) serve(rw http.ResponseWriter, r *http.Request) {
	proxied := r.Header.Get("X-Via-Proxy") != ""
	path := r.URL.Path
	if r.Method == http.MethodHead {
		return // the start-up check of the fallback proxy, not a crawl request
	}
	s.mu.Lock()
	if proxied {
		s.proxied[path]++
		if s.firstProxied.IsZero() {
			s.firstProxied = time.Now()
		}
	} else {
		s.direct[path]++
		if !s.firstProxied.IsZero() {
			s.directAfter++
		}
	}
	_, isPage := s.pages[path]
	blocked := false
	if !proxied && isPage {
		s.directPages++
		blocked = s.directPages > s.allowDirect
	}
	s.mu.Unlock()

	via := "direct"
	if proxied {
		via = "proxy"
	}
	rw.Header().Set("X-Seen-Via", via)
	switch {
	case path == "/robots.txt" && s.robotsRules != "":
		if !proxied {
			rw.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(rw, s.robotsRules)
		return
	case s.forbidden[path]:
		rw.WriteHeader(http.StatusForbidden)
		return
	case proxied && s.proxied429 && isPage:
		rw.WriteHeader(http.StatusTooManyRequests)
		return
	case blocked && s.challenge:
		rw.Header().Set("Cf-Mitigated", "challenge")
		rw.WriteHeader(http.StatusForbidden)
		return
	case blocked:
		rw.WriteHeader(http.StatusTooManyRequests)
		return
	}
	body, ok := s.pages[path]
	if !ok {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", "text/html")
	fmt.Fprint(rw, body)
}

// --- the proxy ------------------------------------------------------------------

type onBlockProxy struct {
	srv      *httptest.Server
	wantAuth string
	pages    atomic.Int64 // forwarded plain-HTTP requests
}

func (w *world) obProxy(user, pass string) error {
	p := &onBlockProxy{}
	if user != "" {
		p.wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	p.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if p.wantAuth != "" && r.Header.Get("Proxy-Authorization") != p.wantAuth {
			rw.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			dst, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadGateway)
				return
			}
			src, _, err := rw.(http.Hijacker).Hijack()
			if err != nil {
				dst.Close()
				return
			}
			io.WriteString(src, "HTTP/1.1 200 Connection established\r\n\r\n")      //nolint:errcheck
			go func() { defer dst.Close(); defer src.Close(); io.Copy(dst, src) }() //nolint:errcheck
			go func() { defer dst.Close(); defer src.Close(); io.Copy(src, dst) }() //nolint:errcheck
			return
		}
		if r.Method != http.MethodHead { // the start-up check is not a page request
			p.pages.Add(1)
		}
		out, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		out.Header.Set("X-Via-Proxy", "1")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				rw.Header().Add(k, v)
			}
		}
		rw.WriteHeader(resp.StatusCode)
		io.Copy(rw, resp.Body) //nolint:errcheck
	}))
	w.ob().proxy = p
	return nil
}

func (w *world) obConfigure(user, pass string) error {
	ob := w.ob()
	ob.proxyURL = ob.proxy.srv.URL
	if user != "" {
		ob.proxyURL = strings.Replace(ob.proxyURL, "http://", "http://"+user+":"+pass+"@", 1)
	}
	return nil
}

// --- crawling -------------------------------------------------------------------

// countingStore is the production store sink with every Page call counted, so
// a scenario can prove a blocked URL was recorded exactly once. It embeds
// *store.Crawl, so every capability the engine sniffs (dedup, queue, egress…)
// still reaches the store.
type countingStore struct {
	*store.Crawl
	mu    sync.Mutex
	calls map[string]int
}

func (s *countingStore) Page(rec *crawler.PageRecord) error {
	s.mu.Lock()
	s.calls[rec.URL]++
	s.mu.Unlock()
	return s.Crawl.Page(rec)
}

func (w *world) obCrawl(resumeSwitched bool) error {
	ob := w.ob()
	cfg := config.Default()
	cfg.HTTP.ProxyOnBlock = true
	cfg.HTTP.Proxy = ob.proxyURL
	cfg.SiteChecks.Enabled = "never"
	cfg.LlmsTxt.Check = false
	for _, o := range ob.overrides {
		if err := cfg.Set(o); err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	seed := ob.site.srv.URL + "/"
	st, err := store.CreateCrawl(w.storeDirPath(), []string{seed}, cfg.Mode, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	sink := &countingStore{Crawl: st, calls: map[string]int{}}
	opts := []crawler.Option{crawler.WithSink(sink)}
	if resumeSwitched {
		opts = append(opts, crawler.WithResume(crawler.Resume{Escalated: true, EgressSwitchedAfter: 1}))
	}
	c, err := crawler.New(cfg, opts...)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := c.Run(ctx, seed)
	ob.crawlErr = err
	if err != nil {
		return nil // asserted by "the blocking crawl fails with"
	}
	if res.Interrupted {
		return fmt.Errorf("the crawl did not finish on its own")
	}
	ob.status = c.EgressStatus()
	ob.calls = sink.calls
	if ob.pages, err = st.LoadPages(); err != nil {
		return err
	}
	ob.egress, err = st.Egress()
	return err
}

// --- assertions -----------------------------------------------------------------

func (w *world) obSwitched() error {
	if w.ob().crawlErr != nil {
		return w.ob().crawlErr
	}
	if m := w.ob().status.Mode; m != crawler.EgressProxy {
		return fmt.Errorf("egress mode = %q, want %q", m, crawler.EgressProxy)
	}
	return nil
}

func (w *world) obNotSwitched() error {
	if w.ob().crawlErr != nil {
		return w.ob().crawlErr
	}
	if m := w.ob().status.Mode; m != crawler.EgressDirect {
		return fmt.Errorf("egress mode = %q, want %q", m, crawler.EgressDirect)
	}
	return nil
}

func (w *world) obRecordedOnce(n int) error {
	ob := w.ob()
	if len(ob.pages) != n {
		return fmt.Errorf("recorded %d pages, want %d", len(ob.pages), n)
	}
	for u, c := range ob.calls {
		if c != 1 {
			return fmt.Errorf("%s recorded %d times", u, c)
		}
	}
	return nil
}

func (w *world) obAll200() error {
	for u, r := range w.ob().pages {
		if r.StatusCode != 200 {
			return fmt.Errorf("%s recorded %d", u, r.StatusCode)
		}
	}
	return nil
}

func (w *world) obRoutesTrue() error {
	ob := w.ob()
	for u, r := range ob.pages {
		seen := r.Headers["X-Seen-Via"]
		switch {
		case r.Proxy == "direct" && seen != "direct",
			r.Proxy == ob.proxy.srv.URL && seen != "proxy":
			return fmt.Errorf("%s recorded via %q but came in %q", u, r.Proxy, seen)
		case r.Proxy != "direct" && r.Proxy != ob.proxy.srv.URL:
			return fmt.Errorf("%s recorded via unexpected route %q", u, r.Proxy)
		}
	}
	for u, c := range ob.calls {
		if c != 1 {
			return fmt.Errorf("%s recorded %d times", u, c)
		}
	}
	return nil
}

func (w *world) obBothSides() error {
	direct, proxied := 0, 0
	for _, r := range w.ob().pages {
		if r.Proxy == "direct" {
			direct++
		} else {
			proxied++
		}
	}
	if direct == 0 || proxied == 0 {
		return fmt.Errorf("direct=%d proxied=%d, want both", direct, proxied)
	}
	return nil
}

func (w *world) obNoDirectAfter() error {
	s := w.ob().site
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.directAfter != 0 {
		return fmt.Errorf("%d direct requests after the switch", s.directAfter)
	}
	return nil
}

func (w *world) obStored() error {
	ev := w.ob().egress
	if ev == nil || ev.State != crawler.EgressProxy {
		return fmt.Errorf("stored egress = %+v, want the switch recorded", ev)
	}
	return nil
}

func (w *world) obPage(path string) (*crawler.PageRecord, error) {
	r := w.ob().pages[w.ob().site.srv.URL+path]
	if r == nil {
		return nil, fmt.Errorf("%s not recorded", path)
	}
	return r, nil
}

func (w *world) obPageStatus(path string, code int) error {
	r, err := w.obPage(path)
	if err != nil {
		return err
	}
	if r.StatusCode != code {
		return fmt.Errorf("%s = %d, want %d", path, r.StatusCode, code)
	}
	return nil
}

func (w *world) obFetchedDirect(path string, n int) error {
	s := w.ob().site
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.direct[path] != n {
		return fmt.Errorf("%s fetched directly %d times, want %d", path, s.direct[path], n)
	}
	return nil
}

func (w *world) obProxyIdle() error {
	if n := w.ob().proxy.pages.Load(); n != 0 {
		return fmt.Errorf("the proxy carried %d page requests", n)
	}
	return nil
}

func (w *world) obBlockedByRobots(path string) error {
	r, err := w.obPage(path)
	if err != nil {
		return err
	}
	if r.State != crawler.StateBlockedRobots {
		return fmt.Errorf("%s state = %q, want %q", path, r.State, crawler.StateBlockedRobots)
	}
	return nil
}

func (w *world) obNeverProxied(path string) error {
	s := w.ob().site
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proxied[path] != 0 {
		return fmt.Errorf("%s fetched %d times through the proxy", path, s.proxied[path])
	}
	return nil
}

func (w *world) obStillBlocked() error {
	if n := w.ob().status.StillBlocked; n == 0 {
		return fmt.Errorf("no responses counted as still blocked through the proxy")
	}
	return nil
}

func (w *world) obFails(want string) error {
	err := w.ob().crawlErr
	if err == nil || !strings.Contains(err.Error(), want) {
		return fmt.Errorf("crawl error = %v, want one containing %q", err, want)
	}
	return nil
}

func (w *world) obNoRequests() error {
	s := w.ob().site
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.direct)+len(s.proxied) != 0 {
		return fmt.Errorf("the site received requests: direct %v, proxied %v", s.direct, s.proxied)
	}
	return nil
}

func (w *world) obNoDirect() error {
	s := w.ob().site
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.direct) != 0 {
		return fmt.Errorf("the site received direct requests: %v", s.direct)
	}
	return nil
}

func (w *world) obRendered() error {
	for u, r := range w.ob().pages {
		if r.Proxy == "direct" {
			continue
		}
		if r.JSDiff == nil {
			return fmt.Errorf("%s: not rendered", u)
		}
		if r.JSDiff.TitleChanged {
			return fmt.Errorf("%s: rendered title %q differs — Chrome got a different answer", u, r.JSDiff.RenderedTitle)
		}
	}
	return nil
}
