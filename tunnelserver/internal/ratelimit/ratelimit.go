// Package ratelimit is a small, dependency-free token-bucket limiter used to
// blunt registration floods on the control plane without requiring user
// accounts (phase-1 abuse control). It supports both per-key (per-IP) and
// global limiting.
package ratelimit

import (
	"net/netip"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a refilling token bucket keyed by an arbitrary string (e.g. a
// client IP). Safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens added per second
	burst   float64 // bucket capacity
	now     func() time.Time
	lastGC  time.Time
}

// New returns a limiter that allows `burst` requests immediately and refills
// at `ratePerSec` tokens per second.
func New(ratePerSec, burst float64) *Limiter {
	return &Limiter{
		buckets: map[string]*bucket{},
		rate:    ratePerSec,
		burst:   burst,
		now:     time.Now,
	}
}

// Allow reports whether one request for key may proceed, consuming a token if
// so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.gcLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	// Refill proportional to elapsed time, capped at burst.
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// gcLocked drops idle buckets occasionally so the map can't grow without
// bound under a spray of distinct keys (e.g. spoofed/rotated source IPs).
func (l *Limiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	// A bucket idle long enough to have fully refilled carries no state.
	idleFull := time.Duration(l.burst/l.rate*float64(time.Second)) + time.Minute
	for k, b := range l.buckets {
		if now.Sub(b.last) > idleFull {
			delete(l.buckets, k)
		}
	}
}

// IPKey canonicalizes an IP address into a per-IP bucket key. IPv6 addresses
// collapse to their /64 prefix — a single end site routinely holds a whole
// /64, so keying on the full address would hand an attacker 2^64 fresh
// buckets. IPv4 (including 4-in-6-mapped) keys on the address itself.
// Non-IP strings pass through unchanged.
func IPKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if addr.Is4() || addr.Is4In6() {
		return addr.Unmap().String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}

// Len reports the number of tracked buckets (test/metrics helper).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
