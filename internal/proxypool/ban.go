package proxypool

import (
	"net/http"
	"strings"
	"sync"
)

// Strength is how strongly one response says "this source IP is being rate
// limited or blocked". The ban policy is three-valued on purpose (PROXY.md §8.3):
// a genuine 403 on /admin is an audit finding, not a block, so a single soft
// signal never trips anything on its own.
type Strength int

const (
	// NotBlock is an ordinary answer — including ordinary 4xx/5xx findings.
	NotBlock Strength = iota
	// Soft counts only as part of a burst (Window): a bare 403, a 503 that
	// asks the client to come back later, a dropped connection.
	Soft
	// Hard is unambiguous: a 429, or a 403/503 carrying a firewall marker.
	Hard
)

func (s Strength) String() string {
	switch s {
	case Soft:
		return "soft"
	case Hard:
		return "hard"
	}
	return "none"
}

// firewallMarkers are response headers that identify a challenge or block page
// served by a WAF/CDN rather than the site's own answer. The list grows as
// crawls find new ones; a marker only upgrades a 403/503, never a 200.
var firewallMarkers = []string{
	"Cf-Mitigated",      // Cloudflare: "challenge" on managed-challenge / block pages
	"X-Amzn-Waf-Action", // AWS WAF: captcha / challenge / block
	"X-Datadome",        // DataDome bot protection
	"X-Sucuri-Block",    // Sucuri firewall
	"X-Px-Block",        // PerimeterX / HUMAN
}

// Classify is the ban policy: one response in, one Strength out. Pure — no I/O,
// no state — so every case is table-testable. fetchErr is the transport error
// for a response that never arrived ("" when one did).
//
// Timeouts are deliberately NotBlock: a slow site times out, and that says
// nothing about whether this IP is welcome. A plain 503 is NotBlock too —
// maintenance windows and outages use it, and advanced.retry_5xx retries it.
func Classify(status int, h http.Header, fetchErr string) Strength {
	if fetchErr != "" {
		e := strings.ToLower(fetchErr)
		if strings.Contains(e, "connection reset") || strings.Contains(e, "connection refused") {
			return Soft
		}
		return NotBlock
	}
	switch status {
	case http.StatusTooManyRequests:
		return Hard
	case http.StatusForbidden:
		if hasFirewallMarker(h) {
			return Hard
		}
		return Soft
	case http.StatusServiceUnavailable:
		if hasFirewallMarker(h) {
			return Hard
		}
		if h.Get("Retry-After") != "" {
			return Soft
		}
		return NotBlock
	case 999: // LinkedIn and a few others answer scrapers with a non-standard 999
		return Soft
	}
	return NotBlock
}

func hasFirewallMarker(h http.Header) bool {
	for _, k := range firewallMarkers {
		if h.Get(k) != "" {
			return true
		}
	}
	return false
}

// Trip rule constants (v1: internal, not config). A rolling window of the last
// WindowSize in-scope responses trips when WindowBlocks of them are blocks of
// either strength, or on HardRun hard signals in a row.
const (
	WindowSize   = 20
	WindowBlocks = 5
	HardRun      = 3
)

// Window is the rolling trip detector. Safe for concurrent use.
type Window struct {
	mu      sync.Mutex
	ring    [WindowSize]Strength
	n       int    // filled slots (≤ WindowSize)
	next    int    // ring write index
	blocks  int    // non-NotBlock entries currently in the ring
	hardRun int    // consecutive Hard observations
	seen    uint64 // observations ever made — the clock parked URLs age against
	tripped bool
}

// Observe records one response and reports whether this observation tripped
// the window. It trips at most once; later calls return false.
func (w *Window) Observe(s Strength) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n == WindowSize && w.ring[w.next] != NotBlock {
		w.blocks-- // the slot about to be overwritten ages out
	}
	w.ring[w.next] = s
	w.next = (w.next + 1) % WindowSize
	if w.n < WindowSize {
		w.n++
	}
	if s != NotBlock {
		w.blocks++
	}
	if s == Hard {
		w.hardRun++
	} else {
		w.hardRun = 0
	}
	w.seen++
	if !w.tripped && (w.blocks >= WindowBlocks || w.hardRun >= HardRun) {
		w.tripped = true
		return true
	}
	return false
}

// AllBlocked reports whether every observation in the window is a block and at
// least one was hard: the whole site, as far as the crawl could see it, is
// refusing this IP. It decides the idle case — a crawl with nothing left to
// fetch but its parked URLs (a seed blocked from the first request discovers
// nothing else to count).
func (w *Window) AllBlocked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n == 0 || w.blocks != w.n {
		return false
	}
	for i := 0; i < w.n; i++ {
		if w.ring[i] == Hard {
			return true
		}
	}
	return false
}

// Trip forces the window tripped (the idle rule); false if it already was.
func (w *Window) Trip() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tripped {
		return false
	}
	w.tripped = true
	return true
}

// Seen is the number of observations made so far.
func (w *Window) Seen() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen
}
