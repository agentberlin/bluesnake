package proxypool

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// probeTargets returns an https:// and an http:// seed. The probe never needs
// the https origin to answer (it only opens the tunnel); the http origin
// counts the absolute-form requests that reach it through the proxy.
func probeTargets(t *testing.T) (tls, plain *url.URL, plainHits *atomic.Int64) {
	t.Helper()
	tlsOrigin := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(tlsOrigin.Close)
	plainHits = &atomic.Int64{}
	plainOrigin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plainHits.Add(1) }))
	t.Cleanup(plainOrigin.Close)
	tls, _ = url.Parse(tlsOrigin.URL + "/")
	plain, _ = url.Parse(plainOrigin.URL + "/")
	return tls, plain, plainHits
}

// gateway is a proxy whose CONNECT answer is scripted, for the cases a real
// gateway produces that fakeUpstream does not: a refused port, a passing 502,
// a 407 with a non-standard reason phrase. Absolute-form requests are
// forwarded, or answered with connectStatus too when refusePlain is set.
func gateway(t *testing.T, connectStatus string, refusePlain bool) *Proxy {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && !refusePlain {
			out, _ := http.NewRequest(r.Method, r.RequestURI, nil)
			resp, err := http.DefaultTransport.RoundTrip(out)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			return
		}
		// Write the raw status line so the reason phrase is exactly ours.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		io.WriteString(conn, "HTTP/1.1 "+connectStatus+"\r\nContent-Length: 0\r\n\r\n") //nolint:errcheck
		conn.Close()
	}))
	t.Cleanup(srv.Close)
	return mustPool(t, []Entry{{URL: srv.URL}}, RoundRobin).Select("h")
}

func TestProbeAcceptsGoodCredentials(t *testing.T) {
	tlsT, plainT, hits := probeTargets(t)
	up := newFakeUpstream(t, "u", "right")
	p := mustPool(t, []Entry{{URL: up.URL("u", "right")}}, RoundRobin).Select("h")
	if err := Probe(context.Background(), p, tlsT); err != nil {
		t.Fatalf("https probe: %v", err)
	}
	if up.connects.Load() != 1 {
		t.Fatalf("https probe opened %d tunnels, want 1", up.connects.Load())
	}
	if err := Probe(context.Background(), p, plainT); err != nil {
		t.Fatalf("http probe: %v", err)
	}
	if up.connects.Load() != 1 || up.plain.Load() != 1 || hits.Load() != 1 {
		t.Fatalf("http probe: tunnels=%d forwarded=%d origin hits=%d, want 1/1/1 — an http crawl never tunnels, so neither may its probe",
			up.connects.Load(), up.plain.Load(), hits.Load())
	}
}

// A wrong password must be named as such, never mistaken for a block — on
// both probe shapes.
func TestProbeReportsBadCredentials(t *testing.T) {
	tlsT, plainT, hits := probeTargets(t)
	up := newFakeUpstream(t, "u", "right")
	p := mustPool(t, []Entry{{URL: up.URL("u", "wrong")}}, RoundRobin).Select("h")
	for _, target := range []*url.URL{tlsT, plainT} {
		err := Probe(context.Background(), p, target)
		if !errors.Is(err, ErrProxyAuth) {
			t.Fatalf("%s: Probe err = %v, want ErrProxyAuth", target.Scheme, err)
		}
		if strings.Contains(err.Error(), "wrong") {
			t.Fatalf("error leaks the password: %v", err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused probe reached the site %d times", hits.Load())
	}
}

// A 407 is recognised by its code, whatever the proxy calls it.
func TestProbeRecognises407WithAnyReasonPhrase(t *testing.T) {
	tlsT, _, _ := probeTargets(t)
	if err := Probe(context.Background(), gateway(t, "407 Auth Failed", false), tlsT); !errors.Is(err, ErrProxyAuth) {
		t.Fatalf("Probe err = %v, want ErrProxyAuth", err)
	}
}

// Squid-style proxies refuse CONNECT to port 80, but an http:// crawl never
// tunnels: the probe must not refuse a crawl that would work.
func TestProbeHTTPSeedDoesNotNeedCONNECT(t *testing.T) {
	_, plainT, hits := probeTargets(t)
	p := gateway(t, "403 Forbidden", false) // refuses every CONNECT, forwards GET/HEAD
	if err := Probe(context.Background(), p, plainT); err != nil {
		t.Fatalf("Probe err = %v, want nil: the crawl would run fine through this proxy", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("origin saw %d forwarded probes, want 1", hits.Load())
	}
}

// Answers that prove neither a working nor a broken fallback are warnings,
// not failures: a refused port, a passing gateway error.
func TestProbeInconclusiveAnswersOnlyWarn(t *testing.T) {
	tlsT, plainT, _ := probeTargets(t)
	cases := []struct {
		name   string
		p      *Proxy
		target *url.URL
	}{
		{"CONNECT refused", gateway(t, "403 Forbidden", false), tlsT},
		{"gateway 502 on CONNECT", gateway(t, "502 Bad Gateway", false), tlsT},
		{"gateway 503 on CONNECT", gateway(t, "503 Service Unavailable", false), tlsT},
	}
	for _, c := range cases {
		err := Probe(context.Background(), c.p, c.target)
		if !errors.Is(err, ErrProbeInconclusive) {
			t.Errorf("%s: err = %v, want ErrProbeInconclusive", c.name, err)
		}
		if errors.Is(err, ErrProxyAuth) {
			t.Errorf("%s: must not be reported as bad credentials", c.name)
		}
	}
	// A plain-HTTP gateway error is the proxy relaying or failing a request,
	// not a credential refusal: it passes (or at worst warns), never fails.
	if err := Probe(context.Background(), gateway(t, "502 Bad Gateway", true), plainT); err != nil && !errors.Is(err, ErrProbeInconclusive) {
		t.Errorf("http 502: err = %v, want nil or inconclusive", err)
	}
}

// Only an unreachable proxy — or rejected credentials — fails the crawl.
func TestProbeReportsDeadProxy(t *testing.T) {
	tlsT, plainT, _ := probeTargets(t)
	up := newFakeUpstream(t, "", "")
	addr := up.URL("", "")
	up.srv.Close()
	p := mustPool(t, []Entry{{URL: addr}}, RoundRobin).Select("h")
	for _, target := range []*url.URL{tlsT, plainT} {
		err := Probe(context.Background(), p, target)
		if err == nil || errors.Is(err, ErrProbeInconclusive) {
			t.Fatalf("%s: err = %v, want a hard failure for a dead proxy", target.Scheme, err)
		}
	}
}

func TestProbeDirectAlwaysPasses(t *testing.T) {
	p := mustPool(t, nil, RoundRobin).Select("h")
	if err := Probe(context.Background(), p, &url.URL{Scheme: "https", Host: "unused:1"}); err != nil {
		t.Fatal(err)
	}
}

// The switchable forwarder carries Chrome direct until Escalate, then through
// the upstream — and the tunnel opened before the switch must not survive it.
func TestSwitchableForwarderMovesToUpstream(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	t.Cleanup(origin.Close)
	up := newFakeUpstream(t, "u", "p")
	p := mustPool(t, []Entry{{URL: up.URL("u", "p")}}, RoundRobin).Select("h")
	f, err := StartSwitchableForwarder(p, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	client := clientVia(t, f)

	get := func() {
		t.Helper()
		resp, err := client.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
	get()
	if up.connects.Load() != 0 {
		t.Fatalf("before the switch the upstream saw %d tunnels, want 0", up.connects.Load())
	}
	f.Escalate()
	get() // the pre-switch tunnel was cut, so the client must dial a new one
	if up.connects.Load() != 1 {
		t.Fatalf("after the switch the upstream saw %d tunnels, want 1", up.connects.Load())
	}
	if got := up.seenAuth.Load().(string); got != up.wantAuth {
		t.Fatalf("upstream credential = %q, want the configured one", got)
	}
}

// A forwarder started already escalated (a resumed crawl) never goes direct.
func TestSwitchableForwarderStartsEscalated(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(origin.Close)
	up := newFakeUpstream(t, "", "")
	p := mustPool(t, []Entry{{URL: up.URL("", "")}}, RoundRobin).Select("h")
	f, err := StartSwitchableForwarder(p, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	resp, err := clientVia(t, f).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if up.plain.Load() != 1 {
		t.Fatalf("upstream carried %d plain requests, want 1", up.plain.Load())
	}
}
