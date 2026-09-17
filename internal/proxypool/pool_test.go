package proxypool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mustPool(t *testing.T, entries []Entry, s Strategy) *Pool {
	t.Helper()
	p, err := New(entries, s)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestNewValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entries  []Entry
		strategy Strategy
		wantErr  string
	}{
		{name: "empty pool is direct-only", strategy: RoundRobin},
		{name: "http proxy", entries: []Entry{{URL: "http://p:8080"}}, strategy: RoundRobin},
		{name: "https proxy", entries: []Entry{{URL: "https://p:8443"}}, strategy: RoundRobin},
		{name: "socks5 proxy", entries: []Entry{{URL: "socks5://p:1080"}}, strategy: RoundRobin},
		{name: "empty strategy defaults", entries: []Entry{{URL: "http://p:8080"}}},
		{
			name: "unknown strategy", entries: []Entry{{URL: "http://p:8080"}},
			strategy: "spiral", wantErr: "unknown strategy",
		},
		{
			name: "unsupported scheme", entries: []Entry{{URL: "ftp://p:21"}},
			strategy: RoundRobin, wantErr: "unsupported scheme",
		},
		{
			name: "no host", entries: []Entry{{URL: "http://"}},
			strategy: RoundRobin, wantErr: "no host",
		},
		{
			name: "unparseable", entries: []Entry{{URL: "://%zz"}},
			strategy: RoundRobin, wantErr: "proxies[0]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.entries, tc.strategy)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// An unconfigured pool must behave exactly as no proxy support at all: one
// direct egress, never nil, so no call site needs a nil check.
func TestEmptyPoolIsDirect(t *testing.T) {
	p := mustPool(t, nil, RoundRobin)
	if p.Len() != 1 {
		t.Fatalf("Len = %d, want 1", p.Len())
	}
	if p.Proxied() {
		t.Error("Proxied = true for an unconfigured pool")
	}
	got := p.Select("example.com")
	if got == nil {
		t.Fatal("Select returned nil")
	}
	if !got.Direct() || got.URL != nil {
		t.Errorf("egress = %+v, want direct with nil URL", got)
	}
	if got.Label() != DirectLabel {
		t.Errorf("Label = %q, want %q", got.Label(), DirectLabel)
	}
}

func TestRoundRobinCyclesInOrder(t *testing.T) {
	p := mustPool(t, []Entry{
		{URL: "http://a:1"}, {URL: "http://b:2"}, {URL: "http://c:3"},
	}, RoundRobin)
	var got []string
	for range 7 {
		got = append(got, p.Select("example.com").Label())
	}
	want := []string{
		"http://a:1", "http://b:2", "http://c:3",
		"http://a:1", "http://b:2", "http://c:3", "http://a:1",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selection %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// sticky_host is what keeps one identity behind one IP. The mapping must be
// stable per host and must still spread different hosts across the pool.
func TestStickyHostIsStableAndSpreads(t *testing.T) {
	p := mustPool(t, []Entry{
		{URL: "http://a:1"}, {URL: "http://b:2"}, {URL: "http://c:3"},
	}, StickyHost)

	first := p.Select("example.com").Label()
	for range 20 {
		if got := p.Select("example.com").Label(); got != first {
			t.Fatalf("sticky_host drifted: %q then %q", first, got)
		}
	}
	// Case must not change the mapping — the same host spelled differently is
	// the same host.
	if got := p.Select("EXAMPLE.COM").Label(); got != first {
		t.Errorf("case changed the mapping: %q vs %q", got, first)
	}

	seen := map[string]bool{}
	for _, h := range []string{"a.com", "b.com", "c.com", "d.com", "e.com", "f.com", "g.com", "h.com"} {
		seen[p.Select(h).Label()] = true
	}
	if len(seen) < 2 {
		t.Errorf("sticky_host pinned %d hosts to one egress; want a spread", len(seen))
	}
}

func TestRandomStaysInPool(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://a:1"}, {URL: "http://b:2"}}, Random)
	valid := map[string]bool{"http://a:1": true, "http://b:2": true}
	for range 50 {
		if got := p.Select("example.com").Label(); !valid[got] {
			t.Fatalf("Select returned %q, not in the pool", got)
		}
	}
}

// A retry must leave through a different egress: retrying a 5xx through the
// proxy that produced it burns the budget and can record a phantom origin error.
func TestSelectExcluding(t *testing.T) {
	p := mustPool(t, []Entry{
		{URL: "http://a:1"}, {URL: "http://b:2"}, {URL: "http://c:3"},
	}, RoundRobin)
	avoid := p.Select("example.com")
	for range 20 {
		if got := p.SelectExcluding("example.com", avoid); got == avoid {
			t.Fatalf("SelectExcluding returned the avoided egress %q", avoid.Label())
		}
	}
}

// With one egress there is nothing to swap to; the same one must come back
// rather than the call failing or returning nil.
func TestSelectExcludingSingleEgressReturnsSame(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://only:1"}}, RoundRobin)
	only := p.Select("example.com")
	if got := p.SelectExcluding("example.com", only); got != only {
		t.Fatalf("got %v, want the single egress back", got)
	}
}

func TestSelectExcludingNilAvoidIsPlainSelect(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://a:1"}, {URL: "http://b:2"}}, RoundRobin)
	if got := p.SelectExcluding("example.com", nil); got == nil {
		t.Fatal("SelectExcluding(nil) returned nil")
	}
}

// Credentials must never reach a log, an export or the UI. Label is the only
// printable form and it carries scheme://host:port and nothing else.
func TestLabelRedactsCredentials(t *testing.T) {
	p := mustPool(t, []Entry{
		{URL: "http://brd-customer-x-zone-y:sekrit@brd.superproxy.io:44445"},
	}, RoundRobin)
	got := p.Select("example.com")
	if strings.Contains(got.Label(), "sekrit") || strings.Contains(got.Label(), "brd-customer") {
		t.Fatalf("Label leaked credentials: %q", got.Label())
	}
	if got.Label() != "http://brd.superproxy.io:44445" {
		t.Errorf("Label = %q", got.Label())
	}
	for _, l := range p.Labels() {
		if strings.Contains(l, "sekrit") {
			t.Errorf("Labels leaked credentials: %q", l)
		}
	}
	// The credential must still be THERE — redaction is for display only.
	pw, _ := got.URL.User.Password()
	if pw != "sekrit" {
		t.Errorf("password lost from the dialable URL: %q", pw)
	}
}

func TestPasswordEnvOverridesURLPassword(t *testing.T) {
	p := mustPool(t, []Entry{
		{URL: "http://user:placeholder@p:8080", Password: "real"},
	}, RoundRobin)
	got := p.Select("example.com")
	pw, ok := got.URL.User.Password()
	if !ok || pw != "real" {
		t.Errorf("password = %q (set=%v), want %q", pw, ok, "real")
	}
	if got.URL.User.Username() != "user" {
		t.Errorf("username = %q, want preserved", got.URL.User.Username())
	}
}

func TestPasswordAppliedWhenURLHasNoUserinfo(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://p:8080", Password: "real"}}, RoundRobin)
	got := p.Select("example.com")
	if got.URL.User == nil {
		t.Fatal("no userinfo set")
	}
	if pw, _ := got.URL.User.Password(); pw != "real" {
		t.Errorf("password = %q", pw)
	}
}

func TestDialAddrFillsDefaultPort(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://p:8080", "p:8080"},
		{"http://p", "p:80"},
		{"https://p", "p:443"},
		{"socks5://p", "p:1080"},
		{"socks5h://p", "p:1080"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			p := mustPool(t, []Entry{{URL: tc.url}}, RoundRobin)
			if got := p.Select("h").DialAddr(); got != tc.want {
				t.Errorf("DialAddr = %q, want %q", got, tc.want)
			}
		})
	}
	direct := mustPool(t, nil, RoundRobin).Select("h")
	if got := direct.DialAddr(); got != "" {
		t.Errorf("direct DialAddr = %q, want empty", got)
	}
}

// The per-egress cap is a correctness feature: providers answer a breach of
// their own concurrency limit with errors indistinguishable from a ban.
func TestAcquireBoundsConcurrency(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://p:8080", MaxConcurrent: 2}}, RoundRobin)
	px := p.Select("example.com")
	ctx := context.Background()
	if !px.Acquire(ctx) || !px.Acquire(ctx) {
		t.Fatal("first two acquires should succeed")
	}

	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if px.Acquire(blocked) {
		t.Fatal("third acquire succeeded past the cap of 2")
	}

	px.Release()
	if !px.Acquire(ctx) {
		t.Fatal("acquire after release should succeed")
	}
	px.Release()
	px.Release()
}

func TestAcquireUncappedAndNilAreNoops(t *testing.T) {
	p := mustPool(t, []Entry{{URL: "http://p:8080"}}, RoundRobin)
	px := p.Select("example.com")
	for range 100 {
		if !px.Acquire(context.Background()) {
			t.Fatal("uncapped acquire blocked")
		}
	}
	px.Release() // must not panic or block

	var nilProxy *Proxy
	if !nilProxy.Acquire(context.Background()) {
		t.Error("nil proxy Acquire = false")
	}
	nilProxy.Release()
	if !nilProxy.Direct() {
		t.Error("nil proxy Direct = false")
	}
	if nilProxy.Label() != DirectLabel {
		t.Errorf("nil proxy Label = %q", nilProxy.Label())
	}
	if nilProxy.DialAddr() != "" {
		t.Error("nil proxy DialAddr not empty")
	}
}

func TestProxiedAndInspectors(t *testing.T) {
	mixed := mustPool(t, []Entry{{URL: "http://a:1"}, {}}, RoundRobin)
	if !mixed.Proxied() {
		t.Error("Proxied = false with a proxy in the pool")
	}
	if got := mixed.Labels(); len(got) != 2 || got[0] != "http://a:1" || got[1] != DirectLabel {
		t.Errorf("Labels = %v", got)
	}
	if mixed.Strategy() != RoundRobin {
		t.Errorf("Strategy = %q", mixed.Strategy())
	}
	if len(mixed.Proxies()) != 2 {
		t.Errorf("Proxies len = %d", len(mixed.Proxies()))
	}
	// A direct-only pool reports unproxied, which is what keeps the pre-proxy
	// behaviour (no Chrome forwarder, no proxy column) untouched.
	if mustPool(t, []Entry{{}}, RoundRobin).Proxied() {
		t.Error("direct-only pool reports Proxied")
	}
}

func TestForwardablePredicates(t *testing.T) {
	for _, tc := range []struct {
		url               string
		forwardable, need bool
	}{
		{"", false, false},                   // direct
		{"http://p:8080", true, false},       // no credentials: Chrome can take it
		{"http://u:p@p:8080", true, true},    // credentials: needs the shim
		{"https://u:p@p:8443", true, true},   // TLS upstream, still forwardable
		{"socks5://p:1080", false, false},    // no header to inject into
		{"socks5://u:p@p:1080", false, true}, // needs a shim we cannot provide
	} {
		t.Run(tc.url, func(t *testing.T) {
			p := mustPool(t, []Entry{{URL: tc.url}}, RoundRobin).Select("h")
			if got := Forwardable(p); got != tc.forwardable {
				t.Errorf("Forwardable = %v, want %v", got, tc.forwardable)
			}
			if got := NeedsForwarder(p); got != tc.need {
				t.Errorf("NeedsForwarder = %v, want %v", got, tc.need)
			}
		})
	}
}
