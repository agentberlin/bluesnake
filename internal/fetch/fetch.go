// Package fetch is bluesnake's HTTP client. Network behaviour is data, not
// transport: redirects are never followed (they are recorded with their
// resolved target and re-enter discovery at the crawler level), errors and
// timeouts become no-response results, HSTS is emulated client-side as
// synthetic 307s (DESIGN.md §5.2), and 5xx responses are retried per config.
package fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
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
}

// Option customizes a Client (test hooks).
type Option func(*Client)

// WithInsecureTLS skips certificate verification (tests against httptest TLS
// servers; later also the trusted-certificates feature's escape hatch).
func WithInsecureTLS() Option {
	return func(c *Client) { c.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} }
}

type Client struct {
	cfg       *config.Config
	hc        *http.Client
	transport *http.Transport
	maxBody   int64
	timeout   time.Duration
	hsts      *hstsStore
}

func New(cfg *config.Config, opts ...Option) (*Client, error) {
	transport := &http.Transport{}
	if cfg.HTTP.Proxy != "" {
		pu, err := url.Parse(cfg.HTTP.Proxy)
		if err != nil {
			return nil, fmt.Errorf("http.proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(pu)
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
	c := &Client{
		cfg:       cfg,
		transport: transport,
		maxBody:   int64(cfg.Limits.MaxPageSizeKB) * 1024,
		timeout:   time.Duration(cfg.Advanced.ResponseTimeoutSec) * time.Second,
		hsts:      newHSTSStore(),
	}
	c.hc = &http.Client{
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
		c.hc.Jar = jar
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Fetch performs one request. The context bounds the whole call in addition
// to the configured response timeout.
func (c *Client) Fetch(ctx context.Context, rawURL string) *Result {
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
	for range attempts {
		c.doOnce(ctx, u, res)
		if res.FetchError != "" || res.StatusCode < 500 {
			break
		}
	}
	return res
}

func (c *Client) doOnce(ctx context.Context, u *url.URL, res *Result) {
	*res = Result{URL: res.URL} // reset between retries

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.FetchError = err.Error()
		return
	}
	req.Header.Set("User-Agent", c.cfg.HTTP.UserAgent)
	if c.cfg.HTTP.BrowserHeaders {
		// Screaming Frog's measured default request profile (v24.1).
		req.Header.Set("Accept", browserAccept)
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	}
	// Configured headers win over the browser defaults above.
	for name, value := range c.cfg.HTTP.Headers {
		req.Header.Set(name, value)
	}
	c.applyAuth(req)

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		res.FetchError = err.Error()
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	res.ResponseTimeMs = time.Since(start).Milliseconds()
	if err != nil {
		res.FetchError = err.Error()
		return
	}
	if int64(len(body)) > c.maxBody {
		body = body[:c.maxBody]
		res.Truncated = true
	}

	res.StatusCode = resp.StatusCode
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
