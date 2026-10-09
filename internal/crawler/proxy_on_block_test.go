package crawler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/proxypool"
	"github.com/agentberlin/bluesnake/internal/render"
)

// --- fixtures ----------------------------------------------------------------

// onBlockProxy is a forward proxy for the proxy_on_block tests. It speaks both
// shapes a client uses — absolute-form requests for http:// (marked with an
// X-Via-Proxy header on the way to the origin) and CONNECT for https:// (whose
// upstream socket addresses it records, so a TLS origin can tell a tunnelled
// connection from a direct one). It can demand Basic credentials.
type onBlockProxy struct {
	srv      *httptest.Server
	wantAuth string
	hits     atomic.Int64 // forwarded requests, the start-up HEAD probe excluded
	probes   atomic.Int64 // forwarded HEAD requests (the start-up check of an http:// seed)
	connects atomic.Int64
	// connectAnswer, when set, is asked for every CONNECT (numbered from 1):
	// a non-empty raw status line ("407 Auth Failed") is answered instead of
	// opening the tunnel.
	connectAnswer func(n int64) string

	mu       sync.Mutex
	upstream map[string]bool // local addrs of the proxy's own dials to origins
}

func newOnBlockProxy(t *testing.T, user, pass string) *onBlockProxy {
	t.Helper()
	p := &onBlockProxy{upstream: map[string]bool{}}
	if user != "" {
		p.wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.wantAuth != "" && r.Header.Get("Proxy-Authorization") != p.wantAuth {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			n := p.connects.Add(1)
			if p.connectAnswer != nil {
				if status := p.connectAnswer(n); status != "" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						io.WriteString(conn, "HTTP/1.1 "+status+"\r\nContent-Length: 0\r\n\r\n") //nolint:errcheck
						conn.Close()
					}
					return
				}
			}
			dst, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			p.mu.Lock()
			p.upstream[dst.LocalAddr().String()] = true
			p.mu.Unlock()
			src, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				dst.Close()
				return
			}
			io.WriteString(src, "HTTP/1.1 200 Connection established\r\n\r\n")      //nolint:errcheck
			go func() { defer dst.Close(); defer src.Close(); io.Copy(dst, src) }() //nolint:errcheck
			go func() { defer dst.Close(); defer src.Close(); io.Copy(src, dst) }() //nolint:errcheck
			return
		}
		if r.Method == http.MethodHead {
			p.probes.Add(1)
		} else {
			p.hits.Add(1)
		}
		out, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		out.Header.Set("X-Via-Proxy", "1")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *onBlockProxy) url(user, pass string) string {
	if user == "" {
		return p.srv.URL
	}
	return strings.Replace(p.srv.URL, "http://", "http://"+user+":"+pass+"@", 1)
}

func (p *onBlockProxy) tunnelled(remoteAddr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upstream[remoteAddr]
}

// blockingSite serves pages and starts rate-limiting this machine's IP: after
// allowDirect direct page requests, every direct request gets a 429 (or the
// answer block() chooses). Requests through the proxy are always served. Each
// response says which way it came in (X-Seen-Via), so a test can check the
// route the crawler RECORDED against the route the request really took.
type blockingSite struct {
	srv         *httptest.Server
	pages       map[string]string
	allowDirect int
	// block, when set, answers a blocked direct request instead of a bare 429.
	block func(w http.ResponseWriter)
	// route, when set, overrides the answer for a path (both routes).
	route func(path string, proxied bool, w http.ResponseWriter) bool
	// viaProxy decides how a request came in; default: the X-Via-Proxy mark.
	viaProxy func(r *http.Request) bool

	mu            sync.Mutex
	directPages   int
	direct        map[string]int
	proxied       map[string]int
	firstProxied  time.Time
	directAfterSw int // direct requests that arrived after the first proxied one
	headProbes    int // start-up checks that reached the site through the proxy
}

func newBlockingSite(pages map[string]string, allowDirect int) *blockingSite {
	return &blockingSite{pages: pages, allowDirect: allowDirect, direct: map[string]int{}, proxied: map[string]int{}}
}

func (s *blockingSite) handler(w http.ResponseWriter, r *http.Request) {
	proxied := r.Header.Get("X-Via-Proxy") != ""
	if s.viaProxy != nil {
		proxied = s.viaProxy(r)
	}
	path := r.URL.Path
	if r.Method == http.MethodHead {
		// The start-up check of the fallback proxy (an http:// seed): it is
		// not a crawl request and must not mark the switch.
		s.mu.Lock()
		s.headProbes++
		s.mu.Unlock()
		w.Header().Set("X-Seen-Via", "proxy")
		return
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
			s.directAfterSw++
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
	w.Header().Set("X-Seen-Via", via)
	if s.route != nil && s.route(path, proxied, w) {
		return
	}
	if blocked {
		if s.block != nil {
			s.block(w)
		} else {
			w.WriteHeader(http.StatusTooManyRequests)
		}
		return
	}
	body, ok := s.pages[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, body)
}

func (s *blockingSite) start(t *testing.T) string {
	t.Helper()
	s.srv = httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(s.srv.Close)
	return s.srv.URL
}

func (s *blockingSite) counts(path string) (direct, proxied int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.direct[path], s.proxied[path]
}

func (s *blockingSite) directAfterSwitch() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.directAfterSw
}

// fanout is a root page linking to n leaves /p0../p{n-1}.
func fanout(n int) map[string]string {
	pages := map[string]string{}
	var links []string
	for i := range n {
		p := fmt.Sprintf("/p%d", i)
		links = append(links, link(p))
		pages[p] = fmt.Sprintf("<html><head><title>page %d</title></head><body><p>leaf %d</p></body></html>", i, i)
	}
	pages["/"] = "<html><head><title>home</title></head><body>" + strings.Join(links, "") + "</body></html>"
	return pages
}

// onBlockSink records every Page call (not just the last per URL), so a test
// can prove a blocked URL was recorded exactly once, and the persisted switch.
type onBlockSink struct {
	*capSink
	mu       sync.Mutex
	calls    map[string]int
	switches []EgressEvent
	stateAt  []string // egress mode when EgressSwitched was called
	c        *Crawler
}

func newOnBlockSink() *onBlockSink {
	return &onBlockSink{capSink: newCapSink(), calls: map[string]int{}}
}

func (s *onBlockSink) Page(rec *PageRecord) error {
	s.mu.Lock()
	s.calls[rec.URL]++
	s.mu.Unlock()
	return s.capSink.Page(rec)
}

func (s *onBlockSink) EgressSwitched(ev EgressEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switches = append(s.switches, ev)
	return nil
}

type onBlockRun struct {
	res    *crawlT
	sink   *onBlockSink
	status EgressStatus
	err    error
}

func runOnBlock(t *testing.T, seed string, mutate func(*config.Config), opts ...Option) onBlockRun {
	t.Helper()
	cfg := config.Default()
	cfg.HTTP.ProxyOnBlock = true
	cfg.SiteChecks.Enabled = "never"
	cfg.LlmsTxt.Check = false
	if mutate != nil {
		mutate(cfg)
	}
	sink := newOnBlockSink()
	c, err := New(cfg, append([]Option{WithSink(sink)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	sink.c = c
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := c.Run(ctx, seed)
	if err != nil {
		return onBlockRun{sink: sink, err: err}
	}
	if res.Interrupted {
		t.Fatal("the crawl did not finish on its own (hit the test timeout)")
	}
	ct := capFinalize(c, sink.snapshot(), res, seed)
	return onBlockRun{res: ct, sink: sink, status: c.EgressStatus()}
}

// checkRoutes asserts every recorded page's route matches the way its request
// really came in, and that no page was recorded twice or as a block.
func checkRoutes(t *testing.T, r onBlockRun, proxyLabel string) {
	t.Helper()
	for u, rec := range r.res.Pages {
		if n := r.sink.calls[u]; n != 1 {
			t.Errorf("%s recorded %d times, want exactly once", u, n)
		}
		seen := rec.Headers["X-Seen-Via"]
		switch rec.Proxy {
		case proxypool.DirectLabel:
			if seen != "direct" {
				t.Errorf("%s recorded as direct but came in via %q", u, seen)
			}
		case proxyLabel:
			if seen != "proxy" {
				t.Errorf("%s recorded as %s but came in via %q", u, proxyLabel, seen)
			}
		default:
			t.Errorf("%s recorded proxy %q, want direct or %s", u, rec.Proxy, proxyLabel)
		}
	}
}

// --- scenarios -----------------------------------------------------------------

// The core: a site that starts answering 429s. The crawl switches, each 429'd
// URL is recorded once with the proxy's 200, pages.proxy reads direct before
// and the proxy after, and no 429 row is stored.
func TestProxyOnBlockSwitchesAndRefetches(t *testing.T) {
	site := newBlockingSite(fanout(15), 4)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("", "")
		cfg.Speed.MaxThreads = 3
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := len(r.res.Pages); got != 16 {
		t.Fatalf("recorded %d pages, want 16", got)
	}
	direct, proxied := 0, 0
	for u, rec := range r.res.Pages {
		if rec.StatusCode != 200 {
			t.Errorf("%s recorded %d — a block must never become a page row", u, rec.StatusCode)
		}
		if rec.Proxy == proxypool.DirectLabel {
			direct++
		} else {
			proxied++
		}
	}
	checkRoutes(t, r, px.srv.URL)
	if direct == 0 || proxied == 0 {
		t.Errorf("direct=%d proxied=%d: want pages on both sides of the switch", direct, proxied)
	}
	if r.status.Mode != EgressProxy || r.status.Refetched == 0 {
		t.Errorf("status = %+v, want switched with re-fetches", r.status)
	}
	if len(r.sink.switches) != 1 || r.sink.switches[0].State != EgressProxy {
		t.Errorf("persisted switches = %+v, want exactly one", r.sink.switches)
	}
	if n := site.directAfterSwitch(); n != 0 {
		t.Errorf("%d direct requests reached the site after the switch", n)
	}
}

// Two member-only pages that answer 403 on every route never trip the switch:
// a 403 is a finding. Each is recorded as 403 after its one last direct try.
func TestProxyOnBlockIsolated403sNeverTrip(t *testing.T) {
	pages := fanout(6)
	pages["/"] += link("/m1") + link("/m2")
	pages["/m1"], pages["/m2"] = "", ""
	site := newBlockingSite(pages, 1000)
	site.route = func(path string, _ bool, w http.ResponseWriter) bool {
		if path == "/m1" || path == "/m2" {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	}
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") })
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressDirect {
		t.Fatalf("mode = %q, want direct: two 403s are not a block", r.status.Mode)
	}
	for _, p := range []string{"/m1", "/m2"} {
		rec := r.res.Pages[site.srv.URL+p]
		if rec == nil || rec.StatusCode != 403 {
			t.Fatalf("%s = %+v, want a 403 record", p, rec)
		}
		if d, _ := site.counts(p); d != 2 {
			t.Errorf("%s fetched %d times direct, want 2 (parked once, then its last try)", p, d)
		}
	}
	if px.hits.Load() != 0 || px.connects.Load() != 0 || px.probes.Load() != 1 {
		t.Errorf("proxy carried %d requests / %d tunnels / %d probes, want only the one start-up HEAD", px.hits.Load(), px.connects.Load(), px.probes.Load())
	}
	checkRoutes(t, r, px.srv.URL)
}

// A wall of blocks from an EXTERNAL host says nothing about the crawled site.
func TestProxyOnBlockExternalBlocksDoNotTrip(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(999) // LinkedIn's answer to crawlers
	}))
	t.Cleanup(ext.Close)
	pages := fanout(2)
	for i := range 10 {
		pages["/"] += fmt.Sprintf(`<a href="%s/x%d">ext</a>`, ext.URL, i)
	}
	site := newBlockingSite(pages, 1000)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("", "")
		cfg.Links.External.Crawl = true
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressDirect {
		t.Fatalf("mode = %q: external 999s must not trip the switch", r.status.Mode)
	}
	if rec := r.res.Pages[ext.URL+"/x0"]; rec == nil || rec.StatusCode != 999 {
		t.Fatalf("external page = %+v, want recorded as 999", rec)
	}
}

// Firewall challenge pages (403 + cf-mitigated) are hard signals: three in a
// row trip the switch.
func TestProxyOnBlockFirewallMarkerTrips(t *testing.T) {
	site := newBlockingSite(fanout(8), 1)
	site.block = func(w http.ResponseWriter) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
	}
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("", "")
		cfg.Speed.MaxThreads = 1
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy {
		t.Fatalf("mode = %q, want proxy", r.status.Mode)
	}
	for u, rec := range r.res.Pages {
		if rec.StatusCode != 200 {
			t.Errorf("%s = %d, want 200 through the proxy", u, rec.StatusCode)
		}
	}
	checkRoutes(t, r, px.srv.URL)
}

// Blocked attempts do not spend the MaxURLs budget.
func TestProxyOnBlockBlocksDoNotUseTheBudget(t *testing.T) {
	site := newBlockingSite(fanout(30), 3)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("", "")
		cfg.Limits.MaxURLs = 10
		cfg.Speed.MaxThreads = 2
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := len(r.res.Pages); got != 10 {
		t.Fatalf("recorded %d pages, want the full budget of 10", got)
	}
	for u, rec := range r.res.Pages {
		if rec.StatusCode != 200 {
			t.Errorf("%s = %d", u, rec.StatusCode)
		}
	}
}

// A robots.txt that came back 429 before the switch is cached allow-all only
// until the switch: after it the file is fetched through the proxy and its
// rules apply — including to URLs that were parked or already queued.
func TestProxyOnBlockRobotsRefetchedAfterSwitch(t *testing.T) {
	pages := fanout(6)
	pages["/"] += link("/private/x")
	pages["/private/x"] = "<p>secret</p>"
	site := newBlockingSite(pages, 0)
	site.route = func(path string, proxied bool, w http.ResponseWriter) bool {
		if path != "/robots.txt" {
			return false
		}
		if !proxied {
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		fmt.Fprint(w, "User-agent: *\nDisallow: /private/\n")
		return true
	}
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") })
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy {
		t.Fatalf("mode = %q, want proxy", r.status.Mode)
	}
	if _, p := site.counts("/robots.txt"); p == 0 {
		t.Fatal("robots.txt was never re-fetched through the proxy")
	}
	rec := r.res.Pages[site.srv.URL+"/private/x"]
	if rec == nil || rec.State != StateBlockedRobots {
		t.Fatalf("/private/x = %+v, want blocked by the re-fetched robots.txt", rec)
	}
	if _, p := site.counts("/private/x"); p != 0 {
		t.Errorf("/private/x fetched %d times through the proxy despite robots.txt", p)
	}
}

// Still blocked through the proxy: there is no further tier, so the blocks are
// recorded as real results, counted loudly, and the crawl completes.
func TestProxyOnBlockStillBlockedThroughProxy(t *testing.T) {
	site := newBlockingSite(fanout(8), 1)
	site.route = func(path string, proxied bool, w http.ResponseWriter) bool {
		if proxied && path != "/robots.txt" {
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") })
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy || r.status.StillBlocked == 0 {
		t.Fatalf("status = %+v, want switched and still blocked", r.status)
	}
	got429 := 0
	for _, rec := range r.res.Pages {
		if rec.StatusCode == 429 {
			got429++
			if rec.Proxy != px.srv.URL {
				t.Errorf("a 429 recorded via %q: only the proxy's answers may be recorded as blocks", rec.Proxy)
			}
		}
	}
	if got429 == 0 {
		t.Fatal("no 429 recorded — the proxy's blocks must be recorded as results")
	}
	checkRoutes(t, r, px.srv.URL)
}

// The fallback proxy is checked before anything is fetched: a wrong password
// fails the crawl at once, named as such.
func TestProxyOnBlockPreflightRejectsBadCredentials(t *testing.T) {
	site := newBlockingSite(fanout(2), 1000)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "u", "right")

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("u", "wrong") })
	if r.err == nil || !strings.Contains(r.err.Error(), "407") {
		t.Fatalf("err = %v, want a named 407 failure before the crawl starts", r.err)
	}
	if strings.Contains(r.err.Error(), "wrong") {
		t.Fatalf("error leaks the password: %v", r.err)
	}
	if d, _ := site.counts("/"); d != 0 {
		t.Errorf("the site was fetched %d times before the failed probe stopped the crawl", d)
	}
}

// A crawl that switched in an earlier session resumes on the proxy.
func TestProxyOnBlockResumeStartsOnProxy(t *testing.T) {
	site := newBlockingSite(fanout(4), 0)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") },
		WithResume(Resume{Escalated: true, EgressSwitchedAfter: 7}))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy || r.status.SwitchedAfter != 7 {
		t.Fatalf("status = %+v, want proxy from the start, switched after 7", r.status)
	}
	for u, rec := range r.res.Pages {
		if rec.Proxy != px.srv.URL || rec.StatusCode != 200 {
			t.Errorf("%s via %q status %d, want the proxy and 200", u, rec.Proxy, rec.StatusCode)
		}
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.direct) != 0 {
		t.Errorf("a resumed, switched crawl went direct: %v", site.direct)
	}
}

// The HTTP/2 regression: over TLS the client negotiates h2, and Go pools h2
// connections per host, not per proxy. The switch must still be real — every
// page recorded as proxied must have reached the site through the tunnel.
func TestProxyOnBlockSwitchIsRealOverHTTP2(t *testing.T) {
	px := newOnBlockProxy(t, "", "")
	site := newBlockingSite(fanout(12), 3)
	site.viaProxy = func(r *http.Request) bool { return px.tunnelled(r.RemoteAddr) }
	srv := httptest.NewUnstartedServer(http.HandlerFunc(site.handler))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	site.srv = srv

	r := runOnBlock(t, srv.URL+"/", func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("", "")
		cfg.Speed.MaxThreads = 3
	}, WithFetchOptions(fetch.WithInsecureTLS()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy {
		t.Fatalf("mode = %q, want proxy", r.status.Mode)
	}
	h2 := false
	for u, rec := range r.res.Pages {
		if rec.HTTPVersion == "HTTP/2.0" {
			h2 = true
		}
		if rec.StatusCode != 200 {
			t.Errorf("%s = %d", u, rec.StatusCode)
		}
	}
	if !h2 {
		t.Fatal("no page used HTTP/2 — the test is not exercising the h2 pool")
	}
	checkRoutes(t, r, px.srv.URL)
	if n := site.directAfterSwitch(); n != 0 {
		t.Errorf("%d requests rode a direct connection after the switch", n)
	}
}

// JavaScript rendering: Chrome changes route at the switch too. Renders after
// the switch reach the site through the proxy, so the rendered page is the
// page — not the 429 the direct IP now gets.
func TestProxyOnBlockRendersFollowTheSwitch(t *testing.T) {
	cfg0 := config.Default()
	if render.ChromePath(cfg0) == "" {
		t.Skip("no Chrome/Chromium available")
	}
	site := newBlockingSite(fanout(6), 3)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "u", "p")

	r := runOnBlock(t, seed, func(cfg *config.Config) {
		cfg.HTTP.Proxy = px.url("u", "p")
		cfg.Rendering.Mode = "javascript"
		cfg.Speed.MaxThreads = 2
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status.Mode != EgressProxy {
		t.Fatalf("mode = %q, want proxy", r.status.Mode)
	}
	for u, rec := range r.res.Pages {
		if rec.Proxy == proxypool.DirectLabel {
			continue
		}
		if rec.JSDiff == nil {
			t.Errorf("%s: no render recorded", u)
			continue
		}
		if rec.JSDiff.TitleChanged {
			t.Errorf("%s: rendered title %q differs from the raw page — Chrome got a different answer than the proxy", u, rec.JSDiff.RenderedTitle)
		}
	}
	if n := site.directAfterSwitch(); n != 0 {
		t.Errorf("%d requests (raw or Chrome) went direct after the switch", n)
	}
}

// The trip is persisted at the moment it happens — while the crawl is still
// draining — so a pause mid-drain resumes on the proxy.
func TestProxyOnBlockPersistsAtTrip(t *testing.T) {
	sink := newOnBlockSink()
	cfg := config.Default()
	cfg.HTTP.ProxyOnBlock = true
	cfg.HTTP.Proxy = "http://127.0.0.1:1"
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	e := c.egress
	e.requeue = func(frontier.Item) {}
	e.active = 1 // a fetch is still on the wire: the drain cannot finish
	for range proxypool.HardRun {
		e.observe(proxypool.Hard)
	}
	if got := e.status().Mode; got != EgressDraining {
		t.Fatalf("mode = %q, want draining while a fetch is in flight", got)
	}
	if len(sink.switches) != 1 {
		t.Fatalf("switch persisted %d times at the trip, want 1 — a pause now must resume on the proxy", len(sink.switches))
	}
	if c.esc.Escalated() {
		t.Fatal("the route must not change while a fetch is still in flight")
	}
	e.exit() // the last fetch lands
	if !c.esc.Escalated() || e.status().Mode != EgressProxy {
		t.Fatal("the last fetch out of the drain must flip the switch")
	}
}

// The start-up check takes the shape the crawl will. An http:// crawl never
// tunnels, so a Squid-style proxy that refuses CONNECT must not stop it.
func TestProxyOnBlockHTTPSeedRunsThroughAProxyThatRefusesCONNECT(t *testing.T) {
	site := newBlockingSite(fanout(8), 2)
	seed := site.start(t) + "/"
	px := newOnBlockProxy(t, "", "")
	px.connectAnswer = func(int64) string { return "403 Forbidden" }

	r := runOnBlock(t, seed, func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") })
	if r.err != nil {
		t.Fatalf("crawl refused: %v — the proxy forwards http:// requests fine", r.err)
	}
	if r.status.Mode != EgressProxy || r.status.Warning != "" {
		t.Fatalf("status = %+v, want switched with no warning", r.status)
	}
	checkRoutes(t, r, px.srv.URL)
}

// A check that is answered but inconclusive (a gateway 503 on the probe) is a
// warning, not a refusal: the crawl runs, and still switches when blocked.
func TestProxyOnBlockInconclusiveCheckOnlyWarns(t *testing.T) {
	px := newOnBlockProxy(t, "", "")
	px.connectAnswer = func(n int64) string {
		if n == 1 {
			return "503 Service Unavailable" // the probe hits a passing gateway error
		}
		return ""
	}
	site := newBlockingSite(fanout(10), 3)
	site.viaProxy = func(r *http.Request) bool { return px.tunnelled(r.RemoteAddr) }
	srv := httptest.NewUnstartedServer(http.HandlerFunc(site.handler))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	site.srv = srv

	r := runOnBlock(t, srv.URL+"/", func(cfg *config.Config) { cfg.HTTP.Proxy = px.url("", "") },
		WithFetchOptions(fetch.WithInsecureTLS()))
	if r.err != nil {
		t.Fatalf("crawl refused over an inconclusive check: %v", r.err)
	}
	if !strings.Contains(r.status.Warning, "inconclusive") {
		t.Errorf("warning = %q, want the inconclusive check reported", r.status.Warning)
	}
	if r.status.Mode != EgressProxy {
		t.Fatalf("mode = %q, want the switch to still happen", r.status.Mode)
	}
	checkRoutes(t, r, px.srv.URL)
}

// Credentials that stop working after the switch fail the crawl loudly, even
// when the proxy names its 407 its own way: Go reports a refused CONNECT with
// only the proxy's reason phrase, which text matching would miss.
func TestProxyOnBlock407AfterSwitchFailsWhateverItIsCalled(t *testing.T) {
	px := newOnBlockProxy(t, "", "")
	px.connectAnswer = func(n int64) string {
		if n == 1 {
			return "" // the start-up probe passes: the credentials worked then
		}
		return "407 Auth Failed" // ...and have expired by the switch
	}
	site := newBlockingSite(fanout(10), 3)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(site.handler))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	site.srv = srv

	cfg := config.Default()
	cfg.HTTP.ProxyOnBlock = true
	cfg.HTTP.Proxy = px.url("", "")
	cfg.SiteChecks.Enabled = "never"
	cfg.LlmsTxt.Check = false
	sink := newOnBlockSink()
	c, err := New(cfg, WithSink(sink), WithFetchOptions(fetch.WithInsecureTLS()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err = c.Run(ctx, srv.URL+"/")
	if !errors.Is(err, proxypool.ErrProxyAuth) {
		t.Fatalf("crawl err = %v, want ErrProxyAuth — a refused fallback must fail the crawl, not record errors", err)
	}
	for u, rec := range sink.snapshot() {
		if rec.State == StateError {
			t.Errorf("%s recorded as an error: a 407 must stop the crawl instead", u)
		}
	}
}
