// Package fetch is bluesnake's HTTP client. Network behaviour is data, not
// transport: redirects are never followed (they are recorded with their
// resolved target and re-enter discovery at the crawler level), errors and
// timeouts become no-response results, HSTS is emulated client-side as
// synthetic 307s (DESIGN.md §5.2), and 5xx responses are retried per config.
package fetch

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/proxypool"
)

// browserAccept is the navigational Accept header bluesnake sends when
// http.browser_headers is on. It is byte-for-byte the value Screaming Frog
// v24.1 sends by default (measured), and it is the header that matters to
// bot-protection layers: Clerk middleware on Vercel returns 403 to a request
// whose Accept is missing or "*/*", and the normal 307 auth redirect once it
// contains "text/html" — independent of the User-Agent and the HTTP version
// (verified live against scale.jobs). Go's net/http sends no Accept by default.
//
// Parity notes for the rest of SF's measured default request profile: SF also
// sends Cache-Control/Pragma "no-cache" (set below), so we mirror that; SF
// sends no Accept-Language, so neither do we (add one via http.headers if you
// want it). Accept-Encoding is deliberately left unset — the transport then
// sends "gzip" itself (exactly as SF does) and transparently decompresses;
// setting it by hand would hand us an undecoded body to parse.
const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// Result is everything the pipeline needs to know about one request. A nil
// Result is never returned; network failures set FetchError with status 0.
type Result struct {
	URL            string
	StatusCode     int
	Status         string // reason phrase, or "HSTS Policy" for synthetic turnarounds
	Headers        http.Header
	Body           []byte
	Truncated      bool // body exceeded limits.max_page_size_kb
	ContentType    string
	HTTPVersion    string
	ResponseTimeMs int64
	RedirectURL    string // resolved Location target for 3xx
	RedirectType   string // "http" | "hsts"
	FetchError     string // non-empty = no response (timeout, refused, malformed)
	// Proxy is the redacted label of the egress that served this request
	// ("direct" when unproxied). Never carries credentials.
	Proxy string
	// ProxyAuthFailed is set when a proxy refused the configured credentials
	// (407) — a configuration error, never a property of the page.
	ProxyAuthFailed bool
}

// Option customizes a Client (test hooks).
type Option func(*Client)

// WithInsecureTLS skips certificate verification (tests against httptest TLS
// servers). It amends the existing TLS config rather than replacing it, so a
// test that also configures trusted roots keeps them.
func WithInsecureTLS() Option {
	return func(c *Client) {
		for _, t := range c.tiers {
			if t.transport.TLSClientConfig == nil {
				t.transport.TLSClientConfig = &tls.Config{}
			}
			t.transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
}

// WithEscalation shares a crawl's direct→proxy switch with the client
// (http.proxy_on_block): the crawler flips it, the client routes by it. Without
// it, a proxy_on_block client builds a private switch that never flips.
func WithEscalation(e *proxypool.Escalation) Option {
	return func(c *Client) { c.esc = e }
}

type Client struct {
	cfg     *config.Config
	tiers   []*tier // [0] = the route used before any escalation
	esc     *proxypool.Escalation
	maxBody int64
	timeout time.Duration
	hsts    *hstsStore
	meter   *meter
}

// tier is one route a client can take: an egress pool with its OWN transport.
// Separate transports are what make an escalation real. Go pools HTTP/2
// connections per host, not per proxy, so with one shared transport a request
// stamped for the proxy would ride a direct h2 connection opened earlier — and
// be recorded as proxied. A tier's connections can only ever carry that tier's
// requests.
type tier struct {
	pool      *proxypool.Pool
	transport *http.Transport
	hc        *http.Client
}

// ctxProxyKey carries the egress chosen for one request. The transport's Proxy
// hook reads it back out, which is what makes selection per-request without
// mutating shared state: the caller picks, so the caller also KNOWS which
// egress served the response and can record it. A transport field would be
// racy across workers and would tell us nothing after the fact.
type ctxProxyKey struct{}

func New(cfg *config.Config, opts ...Option) (*Client, error) {
	pool, err := BuildPool(cfg)
	if err != nil {
		return nil, err
	}
	m := newMeter(pool)
	c := &Client{
		cfg:     cfg,
		maxBody: int64(cfg.Limits.MaxPageSizeKB) * 1024,
		timeout: time.Duration(cfg.Advanced.ResponseTimeoutSec) * time.Second,
		hsts:    newHSTSStore(),
		meter:   m,
	}
	pools := []*proxypool.Pool{pool}
	if cfg.HTTP.ProxyOnBlock {
		// Fallback mode: tier 0 is direct, tier 1 is the configured pool.
		direct, err := proxypool.New(nil, "")
		if err != nil {
			return nil, err
		}
		pools = []*proxypool.Pool{direct, pool}
	}
	for _, p := range pools {
		t, err := newTier(cfg, p, m)
		if err != nil {
			return nil, err
		}
		c.tiers = append(c.tiers, t)
	}
	for _, opt := range opts {
		opt(c)
	}
	if cfg.HTTP.ProxyOnBlock && c.esc == nil {
		c.esc = proxypool.NewEscalation(false)
	}
	if len(c.tiers) > 1 {
		// Retire the direct route's pooled connections at the switch. The
		// tier split already guarantees no post-switch request can use them;
		// this just stops them pinning sockets for the idle timeout.
		direct := c.tiers[0]
		c.esc.OnEscalate(direct.transport.CloseIdleConnections)
	}
	return c, nil
}

func newTier(cfg *config.Config, pool *proxypool.Pool, m *meter) (*tier, error) {
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if p, ok := req.Context().Value(ctxProxyKey{}).(*proxypool.Proxy); ok {
				return p.URL, nil
			}
			// No egress stamped (an internal caller bypassing FetchWith): go
			// direct rather than guessing, so a missing stamp can never be
			// mistaken for a deliberate proxy choice.
			return nil, nil
		},
		// Keep-alive sized to the worker count. The stdlib default is 2 per
		// host, so every thread past the second re-dialled and re-handshook for
		// each page: latency we paid for nothing, and — through a proxy — a
		// fresh TLS ClientHello per page for any fingerprinting defender to
		// sample. The cap is per (proxy, host), so a pool multiplies the
		// connections this permits, which is the intended behaviour.
		MaxIdleConnsPerHost: max(cfg.Speed.MaxThreads, 2),
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &countingConn{Conn: c, total: &m.total, own: m.forAddr(addr)}, nil
		},
	}
	if roots, err := rootPool(cfg.HTTP.TrustedCertDirs); err != nil {
		return nil, err
	} else if roots != nil {
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	switch cfg.HTTP.Version {
	case "1.1":
		// Force HTTP/1.1: a non-nil (empty) TLSNextProto map stops crypto/tls
		// from offering h2 in ALPN, so the connection stays HTTP/1.1 even when a
		// custom TLSClientConfig is set (trusted certs, the insecure test hook).
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = map[string]func(authority string, c *tls.Conn) http.RoundTripper{}
	default:
		// "" or "2": prefer HTTP/2. ForceAttemptHTTP2 keeps h2 negotiation on
		// even when a custom TLSClientConfig would otherwise downgrade the
		// transport to HTTP/1.1, matching what a browser negotiates.
		transport.ForceAttemptHTTP2 = true
	}
	hc := &http.Client{
		Transport: transport,
		// redirects are data: always return the 3xx itself
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if cfg.Advanced.CookieStorage == "persistent" {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		hc.Jar = jar
	}
	return &tier{pool: pool, transport: transport, hc: hc}, nil
}

// route is the tier requests take right now: the proxy tier once the crawl's
// switch has flipped, the first tier otherwise.
func (c *Client) route() *tier {
	if len(c.tiers) > 1 && c.esc.Escalated() {
		return c.tiers[1]
	}
	return c.tiers[0]
}

// Escalation is the client's direct→proxy switch (nil unless proxy_on_block).
func (c *Client) Escalation() *proxypool.Escalation { return c.esc }

// FallbackProxies lists the egresses the client switches to on escalation —
// the ones a proxy_on_block crawl must probe before it starts. Empty when the
// client has no fallback tier.
func (c *Client) FallbackProxies() []*proxypool.Proxy {
	if len(c.tiers) < 2 {
		return nil
	}
	return c.tiers[1].pool.Proxies()
}

// CloseIdleConnections drops the client's pooled keep-alive connections,
// releasing their read/write-loop goroutines and sockets. IdleConnTimeout
// reaps them eventually, but "eventually" is 90s of pinned sockets per finished
// crawl in a long-lived multi-crawl process, so owners still close explicitly.
// Call it when the client's owner is done fetching (a finished crawl, a
// completed tool run); the client stays usable — a later request dials fresh.
func (c *Client) CloseIdleConnections() {
	for _, t := range c.tiers {
		t.transport.CloseIdleConnections()
	}
}

// Override customizes a single request without touching the configured
// profile — the AI-bot live probes fetch as each bot's User-Agent
// (DESIGN.md §5.10). Zero value = the configured behaviour.
type Override struct {
	UserAgent string            // replaces http.user_agent when non-empty
	Headers   map[string]string // applied last, over browser and configured headers
}

// Fetch performs one request. The context bounds the whole call in addition
// to the configured response timeout.
func (c *Client) Fetch(ctx context.Context, rawURL string) *Result {
	return c.FetchWith(ctx, rawURL, Override{})
}

// FetchWith is Fetch with a per-request override.
func (c *Client) FetchWith(ctx context.Context, rawURL string, o Override) *Result {
	res := &Result{URL: rawURL}

	u, err := url.Parse(rawURL)
	if err != nil {
		res.FetchError = err.Error()
		return res
	}

	// HSTS emulation: a known-HSTS host turns http:// around locally.
	if c.cfg.Advanced.RespectHSTS && u.Scheme == "http" && c.hsts.match(strings.ToLower(u.Hostname())) {
		upgraded := *u
		upgraded.Scheme = "https"
		res.StatusCode = http.StatusTemporaryRedirect
		res.Status = "HSTS Policy"
		res.RedirectURL = upgraded.String()
		res.RedirectType = "hsts"
		return res
	}

	attempts := 1 + c.cfg.Advanced.Retry5xx
	// last is the egress that served the previous attempt. A retry deliberately
	// avoids it: a 5xx is as likely to have come from a proxy being throttled or
	// blocked as from the origin, and retrying through the same egress both
	// burns the retry budget and, when the proxy was the cause, records a
	// phantom origin error in the audit.
	var last *proxypool.Proxy
	for range attempts {
		last = c.doOnce(ctx, u, res, o, last)
		if res.FetchError != "" || res.StatusCode < 500 {
			break
		}
		// Before a proxy_on_block switch, a 5xx that is a block signal (a
		// firewall 503, a 503 with Retry-After) is not retried: hammering it
		// from the same IP only deepens the block, and the crawler wants the
		// signal itself — it parks the URL and may switch routes.
		if len(c.tiers) > 1 && !c.esc.Escalated() &&
			proxypool.Classify(res.StatusCode, res.Headers, res.FetchError) != proxypool.NotBlock {
			break
		}
	}
	return res
}

// doOnce performs one attempt and returns the egress it used, so the caller can
// route a retry elsewhere. avoid is the previous attempt's egress, or nil.
func (c *Client) doOnce(ctx context.Context, u *url.URL, res *Result, o Override, avoid *proxypool.Proxy) *proxypool.Proxy {
	*res = Result{URL: res.URL} // reset between retries

	// The tier is read once per attempt, so a request never straddles a switch.
	t := c.route()
	p := t.pool.SelectExcluding(u.Hostname(), avoid)
	// The per-egress concurrency cap is held only around the request itself.
	// Providers enforce their own limits and answer a breach with errors
	// indistinguishable from a ban, so exceeding one corrupts the crawl's
	// results, not just its manners.
	if !p.Acquire(ctx) {
		res.FetchError = ctx.Err().Error()
		return p
	}
	defer p.Release()
	res.Proxy = p.Label()

	// Stamp the egress where the transport's Proxy hook will find it.
	ctx = context.WithValue(ctx, ctxProxyKey{}, p)
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.FetchError = err.Error()
		return p
	}
	ua := c.cfg.HTTP.UserAgent
	if o.UserAgent != "" {
		ua = o.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	if c.cfg.HTTP.BrowserHeaders {
		// Screaming Frog's measured default request profile (v24.1).
		req.Header.Set("Accept", browserAccept)
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	}
	// Configured headers win over the browser defaults above; per-request
	// override headers win over everything.
	for name, value := range c.cfg.HTTP.Headers {
		req.Header.Set(name, value)
	}
	for name, value := range o.Headers {
		req.Header.Set(name, value)
	}
	c.applyAuth(req)

	start := time.Now()
	resp, err := t.hc.Do(req)
	if err != nil {
		res.FetchError = err.Error()
		// Go reports a CONNECT refused with 407 as an error carrying the
		// status text; a plain-HTTP proxied request gets the 407 as a response.
		res.ProxyAuthFailed = !p.Direct() && strings.Contains(err.Error(), "Proxy Authentication Required")
		return p
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	res.ResponseTimeMs = time.Since(start).Milliseconds()
	if err != nil {
		res.FetchError = err.Error()
		return p
	}
	if int64(len(body)) > c.maxBody {
		body = body[:c.maxBody]
		res.Truncated = true
	}

	res.StatusCode = resp.StatusCode
	res.ProxyAuthFailed = !p.Direct() && resp.StatusCode == http.StatusProxyAuthRequired
	res.Status = reasonPhrase(resp.Status, resp.StatusCode)
	res.Headers = resp.Header
	res.Body = body
	res.ContentType = resp.Header.Get("Content-Type")
	res.HTTPVersion = resp.Proto

	if resp.TLS != nil {
		if sts := resp.Header.Get("Strict-Transport-Security"); sts != "" {
			c.hsts.record(strings.ToLower(u.Hostname()), sts)
		}
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := resp.Header.Get("Location"); loc != "" {
			if target, err := u.Parse(loc); err == nil {
				res.RedirectURL = target.String()
				res.RedirectType = "http"
			}
		}
	}
	return p
}

// applyAuth adds basic credentials for the longest matching configured URL
// prefix, and any configured auth cookies whose domain matches.
func (c *Client) applyAuth(req *http.Request) {
	var best *config.BasicAuth
	for i := range c.cfg.HTTP.Auth.Basic {
		rule := &c.cfg.HTTP.Auth.Basic[i]
		if strings.HasPrefix(req.URL.String(), rule.URLPrefix) &&
			(best == nil || len(rule.URLPrefix) > len(best.URLPrefix)) {
			best = rule
		}
	}
	if best != nil {
		password := best.Password
		if best.PasswordEnv != "" {
			password = os.Getenv(best.PasswordEnv)
		}
		req.SetBasicAuth(best.Username, password)
	}
	host := strings.ToLower(req.URL.Hostname())
	for _, ck := range c.cfg.HTTP.Auth.Cookies {
		if ck.Domain == "" || host == ck.Domain || strings.HasSuffix(host, "."+ck.Domain) {
			req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		}
	}
}

func reasonPhrase(status string, code int) string {
	return strings.TrimSpace(strings.TrimPrefix(status, strconv.Itoa(code)))
}

// hstsStore tracks hosts that sent a valid Strict-Transport-Security header
// (RFC 6797), with includeSubDomains support.
type hstsStore struct {
	mu    sync.RWMutex
	hosts map[string]bool // host -> includeSubDomains
}

func newHSTSStore() *hstsStore {
	return &hstsStore{hosts: make(map[string]bool)}
}

func (s *hstsStore) record(host, header string) {
	maxAge := -1
	includeSub := false
	for part := range strings.SplitSeq(header, ";") {
		part = strings.TrimSpace(strings.ToLower(part))
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				maxAge = n
			}
		}
		if part == "includesubdomains" {
			includeSub = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case maxAge == 0:
		delete(s.hosts, host)
	case maxAge > 0:
		s.hosts[host] = includeSub
	}
}

func (s *hstsStore) match(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.hosts[host]; ok {
		return true
	}
	for {
		i := strings.Index(host, ".")
		if i < 0 {
			return false
		}
		host = host[i+1:]
		if includeSub, ok := s.hosts[host]; ok && includeSub {
			return true
		}
	}
}
