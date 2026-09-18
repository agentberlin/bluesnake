// Package proxypool chooses which egress a request leaves through.
//
// The pool is deliberately dumb: it holds an ordered set of egresses (each a
// proxy URL, or the nil URL meaning "connect directly"), and answers one
// question — given a target host, which egress should this request use. It does
// no I/O, opens no sockets, and knows nothing about HTTP; internal/fetch owns
// the transport and hands the chosen egress back via the request context.
//
// That split is what makes N proxies cost almost nothing over one: a single
// configured proxy is a pool of size one, and the difference between them is
// which value Select returns. Go's http.Transport evaluates its Proxy hook per
// request and includes the proxy URL in its connection-pool key, so one
// transport multiplexes the whole pool with correct per-(proxy, host)
// keep-alive — the rotation costs us no connection reuse.
//
// Credentials live in the *url.Userinfo of each Proxy.URL and must never leave
// this package in printable form: Label() is the only string a caller should
// log, store, export or render. Nothing here formats a URL with its userinfo.
package proxypool

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync/atomic"
)

// Strategy decides which egress a request takes.
type Strategy string

const (
	// RoundRobin walks the egresses in order, one step per request. Spreads
	// load evenly and is the right default when every request is independent.
	RoundRobin Strategy = "round_robin"
	// StickyHost maps each target host to one egress for the life of the pool.
	// Required whenever the crawl carries a shared identity (persistent cookie
	// jar, configured auth cookies): one session emerging from many source IPs
	// is a stronger bot signal than the traffic concentration rotation avoids,
	// and on an authenticated crawl it reads as session hijacking.
	StickyHost Strategy = "sticky_host"
	// Random picks uniformly at random.
	Random Strategy = "random"
)

// Proxy is one egress. A nil URL means a direct connection.
type Proxy struct {
	// URL is the proxy to dial, including any credentials. nil = direct.
	URL *url.URL
	// label is the redacted, loggable identity of this egress.
	label string
	// slots bounds concurrent in-flight requests through this egress. nil =
	// unbounded. Providers enforce their own concurrency limits and answer
	// breaches with errors that look exactly like bans, so the cap is a
	// correctness feature, not just politeness.
	slots chan struct{}
	// dialAddr is the host:port the transport dials for this egress, used to
	// attribute wire bytes back to it. Empty for direct.
	dialAddr string
}

// Label is the only printable form of an egress: scheme://host:port with any
// credentials removed, or "direct". Safe to log, store, export and display.
func (p *Proxy) Label() string {
	if p == nil {
		return DirectLabel
	}
	return p.label
}

// DirectLabel marks a request that used no proxy.
const DirectLabel = "direct"

// Direct reports whether this egress connects without a proxy.
func (p *Proxy) Direct() bool { return p == nil || p.URL == nil }

// DialAddr is the host:port the transport dials to reach this egress, or "" for
// direct. Used to attribute connection bytes to the egress that carried them.
func (p *Proxy) DialAddr() string {
	if p == nil {
		return ""
	}
	return p.dialAddr
}

// Acquire takes one of this egress's concurrency slots, blocking until one is
// free or ctx is done. It returns true once a slot is held — the caller MUST
// then Release, ideally via defer so a panic mid-request cannot leak it — or
// false if ctx was cancelled while waiting (no slot held; do not Release).
// A nil proxy or an uncapped egress returns true immediately.
func (p *Proxy) Acquire(ctx context.Context) bool {
	if p == nil || p.slots == nil {
		return true
	}
	select {
	case p.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// Release returns a concurrency slot. No-op for a nil or uncapped egress.
func (p *Proxy) Release() {
	if p == nil || p.slots == nil {
		return
	}
	<-p.slots
}

// Entry is one configured egress, as it arrives from config.
type Entry struct {
	// URL is the proxy URL. Empty means a direct (unproxied) egress, which is
	// how http.proxy_include_direct puts "no proxy" into the rotation.
	URL string
	// Password overrides any password in URL's userinfo (resolved from
	// password_env by the caller, so this package never reads the environment).
	Password string
	// MaxConcurrent bounds in-flight requests through this egress. 0 =
	// unbounded.
	MaxConcurrent int
}

// Pool selects an egress per request.
type Pool struct {
	proxies  []*Proxy
	strategy Strategy
	next     atomic.Uint64
}

// New builds a pool. An empty entries slice yields a pool whose single egress
// is direct, so callers never need to nil-check: an unconfigured crawl behaves
// exactly as it did before proxies existed.
func New(entries []Entry, strategy Strategy) (*Pool, error) {
	if strategy == "" {
		strategy = RoundRobin
	}
	switch strategy {
	case RoundRobin, StickyHost, Random:
	default:
		return nil, fmt.Errorf("proxypool: unknown strategy %q", strategy)
	}
	if len(entries) == 0 {
		entries = []Entry{{}} // direct-only
	}
	p := &Pool{strategy: strategy}
	for i, e := range entries {
		px, err := newProxy(e)
		if err != nil {
			return nil, fmt.Errorf("proxies[%d]: %w", i, err)
		}
		p.proxies = append(p.proxies, px)
	}
	return p, nil
}

func newProxy(e Entry) (*Proxy, error) {
	px := &Proxy{label: DirectLabel}
	if raw := strings.TrimSpace(e.URL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		if u.Host == "" {
			return nil, fmt.Errorf("%q has no host", raw)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("%q: unsupported scheme %q (want http, https, socks5 or socks5h)", raw, u.Scheme)
		}
		if e.Password != "" {
			user := ""
			if u.User != nil {
				user = u.User.Username()
			}
			u.User = url.UserPassword(user, e.Password)
		}
		px.URL = u
		px.label = u.Scheme + "://" + u.Host
		px.dialAddr = dialAddr(u)
	}
	if e.MaxConcurrent > 0 {
		px.slots = make(chan struct{}, e.MaxConcurrent)
	}
	return px, nil
}

// dialAddr is the host:port the transport connects to for this proxy, with the
// scheme's default port filled in when the URL omits it.
func dialAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	switch u.Scheme {
	case "https":
		return u.Hostname() + ":443"
	case "socks5", "socks5h":
		return u.Hostname() + ":1080"
	default:
		return u.Hostname() + ":80"
	}
}

// Select returns the egress for a request to host. It never returns nil: a
// direct-only pool returns its direct egress, whose URL is nil.
//
// StickyHost hashes the host, so the mapping is stable for the life of the
// process and identical across workers without any shared state — two requests
// to the same host always leave through the same IP.
func (p *Pool) Select(host string) *Proxy {
	if len(p.proxies) == 1 {
		return p.proxies[0]
	}
	switch p.strategy {
	case StickyHost:
		h := fnv.New32a()
		_, _ = h.Write([]byte(strings.ToLower(host)))
		return p.proxies[int(h.Sum32()%uint32(len(p.proxies)))]
	case Random:
		return p.proxies[rand.IntN(len(p.proxies))]
	default: // RoundRobin
		n := p.next.Add(1) - 1
		return p.proxies[int(n%uint64(len(p.proxies)))]
	}
}

// SelectExcluding returns an egress for host, avoiding avoid when the pool has
// another to offer. It is what makes a retry a *different* egress: retrying a
// 5xx through the proxy that just produced it burns the retry budget and, when
// the 5xx came from the proxy rather than the origin, writes a phantom error
// into the crawl. With a single egress there is nothing to swap to and the same
// one comes back.
//
// StickyHost is the deliberate exception: it is in force precisely because the
// crawl carries one identity, and moving that identity to a second IP partway
// through is the signal stickiness exists to avoid. There, a retry is a retry
// of the request, not of the egress.
func (p *Pool) SelectExcluding(host string, avoid *Proxy) *Proxy {
	if avoid == nil || len(p.proxies) == 1 || p.strategy == StickyHost {
		return p.Select(host)
	}
	cand := p.Select(host)
	if cand != avoid {
		return cand
	}
	// The draw landed back on the egress we are trying to leave. Step to its
	// neighbour: with two or more egresses a neighbour is always a different
	// entry, so one step resolves it without a retry loop that a random
	// strategy could spin through.
	for i, c := range p.proxies {
		if c == avoid {
			return p.proxies[(i+1)%len(p.proxies)]
		}
	}
	return cand // avoid belongs to another pool; the draw stands
}

// Len is the number of configured egresses.
func (p *Pool) Len() int { return len(p.proxies) }

// Strategy reports the strategy in force.
func (p *Pool) Strategy() Strategy { return p.strategy }

// Proxies returns the configured egresses in order.
func (p *Pool) Proxies() []*Proxy { return p.proxies }

// Labels lists the redacted egress labels, in order.
func (p *Pool) Labels() []string {
	out := make([]string, 0, len(p.proxies))
	for _, px := range p.proxies {
		out = append(out, px.Label())
	}
	return out
}

// Proxied reports whether any egress uses a proxy. A direct-only pool is the
// unconfigured case, and call sites use this to keep their pre-proxy behaviour
// (no forwarder for Chrome, no proxy column worth showing) untouched.
func (p *Pool) Proxied() bool {
	for _, px := range p.proxies {
		if !px.Direct() {
			return true
		}
	}
	return false
}
