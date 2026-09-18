package fetch

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// testProxy is an HTTP forward proxy that records what passed through it. The
// crawl's targets in these tests are plain-HTTP httptest servers, which a
// browser-shaped client reaches with absolute-form requests rather than CONNECT.
type testProxy struct {
	srv  *httptest.Server
	name string

	mu   sync.Mutex
	seen []string // request URLs, in order

	// status, when non-zero, is answered instead of forwarding — this is how a
	// proxy that has started being throttled or blocked behaves.
	status int
}

func newTestProxy(t *testing.T, name string) *testProxy {
	t.Helper()
	p := &testProxy{name: name}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.RequestURI)
		status := p.status
		p.mu.Unlock()

		if status != 0 {
			w.WriteHeader(status)
			return
		}
		out, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		// Mark the response so a test can tell which egress carried it.
		w.Header().Set("X-Test-Proxy", p.name)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *testProxy) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.seen)
}

func (p *testProxy) fail(status int) {
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
}

// label is the redacted form the client should report for this proxy.
func (p *testProxy) label() string { return p.srv.URL }

func proxyCfg(t *testing.T, proxies []*testProxy, mutate func(*config.Config)) *config.Config {
	t.Helper()
	cfg := config.Default()
	for _, p := range proxies {
		cfg.HTTP.Proxies = append(cfg.HTTP.Proxies, config.ProxyEntry{URL: p.srv.URL})
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	return cfg
}

// An unconfigured crawl must behave exactly as before proxies existed, and must
// still SAY so — "direct" is a fact worth recording, not an empty field.
func TestFetchWithoutProxyReportsDirect(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hi")
	}))
	defer origin.Close()

	res := newClient(t, nil).Fetch(context.Background(), origin.URL+"/p")
	if res.FetchError != "" {
		t.Fatalf("fetch error: %s", res.FetchError)
	}
	if res.Proxy != "direct" {
		t.Errorf("Proxy = %q, want \"direct\"", res.Proxy)
	}
}

func TestFetchThroughSingleProxyIsAttributed(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "through-proxy")
	}))
	defer origin.Close()
	px := newTestProxy(t, "A")

	c, err := New(proxyCfg(t, []*testProxy{px}, nil))
	if err != nil {
		t.Fatal(err)
	}
	res := c.Fetch(context.Background(), origin.URL+"/p")
	if res.FetchError != "" {
		t.Fatalf("fetch error: %s", res.FetchError)
	}
	if string(res.Body) != "through-proxy" {
		t.Errorf("body = %q", res.Body)
	}
	if px.count() != 1 {
		t.Errorf("proxy saw %d requests, want 1", px.count())
	}
	if res.Headers.Get("X-Test-Proxy") != "A" {
		t.Error("response did not come back through the proxy")
	}
	if res.Proxy != px.label() {
		t.Errorf("Proxy = %q, want %q", res.Proxy, px.label())
	}
}

// The shorthand must be exactly equivalent to a one-entry list.
func TestSingleProxyShorthandEquivalentToList(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	px := newTestProxy(t, "A")

	cfg := config.Default()
	cfg.HTTP.Proxy = px.srv.URL
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := c.Fetch(context.Background(), origin.URL+"/p")
	if res.Proxy != px.label() || px.count() != 1 {
		t.Errorf("Proxy = %q, proxy saw %d requests", res.Proxy, px.count())
	}
}

// Round-robin must actually spread requests, and each result must name the
// egress that really carried it.
func TestRoundRobinSpreadsAcrossProxies(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	a, b := newTestProxy(t, "A"), newTestProxy(t, "B")

	c, err := New(proxyCfg(t, []*testProxy{a, b}, nil))
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]int{}
	for i := range 6 {
		res := c.Fetch(context.Background(), fmt.Sprintf("%s/p%d", origin.URL, i))
		if res.FetchError != "" {
			t.Fatalf("fetch %d: %s", i, res.FetchError)
		}
		labels[res.Proxy]++
		// The recorded egress must match the one that actually served it.
		if got, want := res.Headers.Get("X-Test-Proxy"), proxyName(res.Proxy, a, b); got != want {
			t.Errorf("fetch %d: served by %q but recorded as %q", i, got, res.Proxy)
		}
	}
	if a.count() != 3 || b.count() != 3 {
		t.Errorf("split = A:%d B:%d, want 3/3", a.count(), b.count())
	}
	if len(labels) != 2 {
		t.Errorf("results named %d distinct egresses, want 2: %v", len(labels), labels)
	}
}

func proxyName(label string, ps ...*testProxy) string {
	for _, p := range ps {
		if p.label() == label {
			return p.name
		}
	}
	return ""
}

// sticky_host pins a host to one egress, which is what keeps a shared session
// behind a single source IP.
func TestStickyHostKeepsOneEgressPerHost(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	a, b := newTestProxy(t, "A"), newTestProxy(t, "B")

	c, err := New(proxyCfg(t, []*testProxy{a, b}, func(cfg *config.Config) {
		cfg.HTTP.ProxyStrategy = "sticky_host"
	}))
	if err != nil {
		t.Fatal(err)
	}
	first := c.Fetch(context.Background(), origin.URL+"/p0").Proxy
	for i := 1; i < 6; i++ {
		if got := c.Fetch(context.Background(), fmt.Sprintf("%s/p%d", origin.URL, i)).Proxy; got != first {
			t.Fatalf("fetch %d used %q, want %q throughout", i, got, first)
		}
	}
	if a.count()+b.count() != 6 || (a.count() != 0 && b.count() != 0) {
		t.Errorf("traffic split A:%d B:%d, want all on one egress", a.count(), b.count())
	}
}

// The reason retries re-select: a 5xx from a throttled proxy retried through
// that same proxy burns the budget and records a phantom origin error.
func TestRetryLeavesThroughADifferentProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "recovered")
	}))
	defer origin.Close()
	bad, good := newTestProxy(t, "BAD"), newTestProxy(t, "GOOD")
	bad.fail(http.StatusServiceUnavailable)

	c, err := New(proxyCfg(t, []*testProxy{bad, good}, func(cfg *config.Config) {
		cfg.Advanced.Retry5xx = 1
	}))
	if err != nil {
		t.Fatal(err)
	}
	res := c.Fetch(context.Background(), origin.URL+"/p")
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want the retry to succeed through the healthy egress", res.StatusCode)
	}
	if string(res.Body) != "recovered" {
		t.Errorf("body = %q", res.Body)
	}
	if res.Proxy != good.label() {
		t.Errorf("Proxy = %q, want the healthy egress %q", res.Proxy, good.label())
	}
	if bad.count() != 1 {
		t.Errorf("failing proxy saw %d requests, want exactly 1 (the retry must not reuse it)", bad.count())
	}
	if good.count() != 1 {
		t.Errorf("healthy proxy saw %d requests, want 1", good.count())
	}
}

// Credentials belong in the dialed URL and nowhere else.
func TestResultProxyNeverCarriesCredentials(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	px := newTestProxy(t, "A")

	withCreds := strings.Replace(px.srv.URL, "http://", "http://user:sekrit@", 1)
	cfg := config.Default()
	cfg.HTTP.Proxy = withCreds
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := c.Fetch(context.Background(), origin.URL+"/p")
	if strings.Contains(res.Proxy, "sekrit") || strings.Contains(res.Proxy, "user") {
		t.Fatalf("Result.Proxy leaked credentials: %q", res.Proxy)
	}
}

func TestPasswordEnvResolvedAndMissingIsLoud(t *testing.T) {
	px := newTestProxy(t, "A")
	u, _ := url.Parse(px.srv.URL)

	cfg := config.Default()
	cfg.HTTP.Proxies = []config.ProxyEntry{{
		URL:         "http://user@" + u.Host,
		PasswordEnv: "BLUESNAKE_TEST_PROXY_PW",
	}}
	if _, err := New(cfg); err == nil {
		t.Fatal("unset password_env should fail loudly, not dial without the password")
	} else if !strings.Contains(err.Error(), "BLUESNAKE_TEST_PROXY_PW") {
		t.Errorf("error = %v, want it to name the variable", err)
	}

	t.Setenv("BLUESNAKE_TEST_PROXY_PW", "sekrit")
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("with the variable set: %v", err)
	}
	pw, ok := c.Pool().Select("h").URL.User.Password()
	if !ok || pw != "sekrit" {
		t.Errorf("password = %q (set=%v)", pw, ok)
	}
}

// Wire bytes are what a per-GB provider bills for, and they must be attributable
// to the egress that carried them.
func TestTrafficMeteredPerEgress(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 4096))
	}))
	defer origin.Close()
	px := newTestProxy(t, "A")

	c, err := New(proxyCfg(t, []*testProxy{px}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Traffic().Total(); got != 0 {
		t.Errorf("traffic before any fetch = %d, want 0", got)
	}
	c.Fetch(context.Background(), origin.URL+"/p")

	total := c.Traffic()
	if total.In < 4096 {
		t.Errorf("In = %d, want at least the 4096-byte body", total.In)
	}
	if total.Out == 0 {
		t.Error("Out = 0, want the request bytes counted")
	}
	if total.Total() != total.In+total.Out {
		t.Error("Total is not In+Out")
	}

	per := c.TrafficByEgress()
	if len(per) != 1 {
		t.Fatalf("byEgress = %v, want exactly the one proxy that carried traffic", per)
	}
	if per[0].Proxy != px.label() {
		t.Errorf("attributed to %q, want %q", per[0].Proxy, px.label())
	}
	if per[0].Total() != total.Total() {
		t.Errorf("per-egress total %d != client total %d", per[0].Total(), total.Total())
	}
}

func TestTrafficMeteredWithoutProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "body")
	}))
	defer origin.Close()

	c := newClient(t, nil)
	c.Fetch(context.Background(), origin.URL+"/p")
	if c.Traffic().Total() == 0 {
		t.Error("no traffic metered on the direct path")
	}
	per := c.TrafficByEgress()
	if len(per) != 1 || per[0].Proxy != "direct" {
		t.Errorf("byEgress = %v, want a single direct entry", per)
	}
}

// http.trusted_cert_dirs is what makes a TLS-terminating proxy usable without
// turning verification off — which an auditor that reports on certificate
// validity must never do.
func TestTrustedCertDirsVerifiesAPrivateCA(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "private-ca-ok")
	}))
	defer origin.Close()

	dir := t.TempDir()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: origin.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "root.pem"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Without the directory the handshake must fail — proving the next case
	// passes because of the configured root, not because verification is off.
	bare := newClient(t, nil)
	if res := bare.Fetch(context.Background(), origin.URL+"/p"); res.FetchError == "" {
		t.Fatal("untrusted TLS server verified without the configured root")
	}

	cfg := config.Default()
	cfg.HTTP.TrustedCertDirs = []string{dir}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New with trusted_cert_dirs: %v", err)
	}
	res := c.Fetch(context.Background(), origin.URL+"/p")
	if res.FetchError != "" {
		t.Fatalf("fetch with the configured root: %s", res.FetchError)
	}
	if string(res.Body) != "private-ca-ok" {
		t.Errorf("body = %q", res.Body)
	}
}

// Every trusted-cert failure is a config error at construction. A silently
// ignored directory surfaces later as an x509 failure on every URL in the crawl.
func TestTrustedCertDirsFailLoudly(t *testing.T) {
	empty := t.TempDir()
	junkDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(junkDir, "not-a-cert.pem"), []byte("nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, dir, want string }{
		{"missing directory", filepath.Join(empty, "nope"), "trusted_cert_dirs"},
		{"no certificates", empty, "holds no"},
		{"unparseable PEM", junkDir, "holds no PEM certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.HTTP.TrustedCertDirs = []string{tc.dir}
			_, err := New(cfg)
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The keep-alive cap must track the worker count, or every thread past the
// second re-dials and re-handshakes for each page.
func TestIdleConnsTrackThreadCount(t *testing.T) {
	for _, threads := range []int{1, 5, 40} {
		cfg := config.Default()
		cfg.Speed.MaxThreads = threads
		c, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := c.transport.MaxIdleConnsPerHost, max(threads, 2); got != want {
			t.Errorf("threads=%d: MaxIdleConnsPerHost = %d, want %d", threads, got, want)
		}
		if c.transport.IdleConnTimeout == 0 {
			t.Errorf("threads=%d: IdleConnTimeout unset — idle sockets stay pinned until the peer closes", threads)
		}
	}
}

// Rotation under a shared identity is refused rather than silently overridden:
// one session from many IPs is a stronger signal than the concentration it avoids.
func TestSharedIdentityForcesStickyHost(t *testing.T) {
	a, b := newTestProxy(t, "A"), newTestProxy(t, "B")

	persistent := proxyCfg(t, []*testProxy{a, b}, func(cfg *config.Config) {
		cfg.Advanced.CookieStorage = "persistent"
	})
	if got := persistent.ResolvedProxyStrategy(); got != "sticky_host" {
		t.Errorf("persistent cookies resolved to %q, want sticky_host", got)
	}

	cookies := config.Default()
	cookies.HTTP.Auth.Cookies = []config.AuthCookie{{Name: "session", Value: "abc"}}
	if got := cookies.ResolvedProxyStrategy(); got != "sticky_host" {
		t.Errorf("auth cookies resolved to %q, want sticky_host", got)
	}

	plain := config.Default()
	if got := plain.ResolvedProxyStrategy(); got != "round_robin" {
		t.Errorf("default resolved to %q, want round_robin", got)
	}
}
