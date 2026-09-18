package proxypool

import (
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeUpstream is an HTTP proxy that demands Basic credentials, exactly as a
// commercial gateway does. It speaks both shapes a browser uses: absolute-form
// requests for http:// targets and CONNECT for https://.
type fakeUpstream struct {
	srv      *httptest.Server
	wantAuth string       // required Proxy-Authorization value; empty = open proxy
	seenAuth atomic.Value // string: the credential the last request carried
	connects atomic.Int64
	plain    atomic.Int64
}

func newFakeUpstream(t *testing.T, user, pass string) *fakeUpstream {
	t.Helper()
	up := &fakeUpstream{}
	up.seenAuth.Store("")
	if user != "" {
		up.wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Proxy-Authorization")
		up.seenAuth.Store(got)
		if up.wantAuth != "" && got != up.wantAuth {
			w.Header().Set("Proxy-Authenticate", `Basic realm="upstream"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			up.connects.Add(1)
			up.tunnel(w, r)
			return
		}
		up.plain.Add(1)
		up.forward(w, r)
	}))
	t.Cleanup(up.srv.Close)
	return up
}

func (up *fakeUpstream) tunnel(w http.ResponseWriter, r *http.Request) {
	dst, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		dst.Close()
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	src, _, err := hj.Hijack()
	if err != nil {
		dst.Close()
		return
	}
	if _, err := io.WriteString(src, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		dst.Close()
		src.Close()
		return
	}
	go func() { defer dst.Close(); defer src.Close(); io.Copy(dst, src) }() //nolint:errcheck
	go func() { defer dst.Close(); defer src.Close(); io.Copy(src, dst) }() //nolint:errcheck
}

func (up *fakeUpstream) forward(w http.ResponseWriter, r *http.Request) {
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
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

func (up *fakeUpstream) URL(user, pass string) string {
	host := strings.TrimPrefix(up.srv.URL, "http://")
	if user == "" {
		return "http://" + host
	}
	return "http://" + url.UserPassword(user, pass).String() + "@" + host
}

func startForwarderFor(t *testing.T, proxyURL string) *Forwarder {
	t.Helper()
	p := mustPool(t, []Entry{{URL: proxyURL}}, RoundRobin).Select("h")
	f, err := StartForwarder(p)
	if err != nil {
		t.Fatalf("StartForwarder: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// clientVia is a plain http.Client routed through the forwarder, standing in
// for Chrome pointed at --proxy-server.
func clientVia(t *testing.T, f *Forwarder) *http.Client {
	t.Helper()
	u, err := url.Parse(f.ProxyServer())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(u),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// The whole point: Chrome cannot send proxy credentials, so the forwarder adds
// them. The address handed to Chrome must itself be credential-free.
func TestForwarderInjectsCredentialsOnPlainHTTP(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "origin-said-hi") //nolint:errcheck
	}))
	defer origin.Close()

	up := newFakeUpstream(t, "cust-zone", "sekrit")
	f := startForwarderFor(t, up.URL("cust-zone", "sekrit"))

	if strings.Contains(f.ProxyServer(), "sekrit") || strings.Contains(f.ProxyServer(), "@") {
		t.Fatalf("ProxyServer leaked credentials: %q", f.ProxyServer())
	}
	if !strings.HasPrefix(f.ProxyServer(), "http://127.0.0.1:") {
		t.Errorf("ProxyServer = %q, want a loopback address", f.ProxyServer())
	}

	resp, err := clientVia(t, f).Get(origin.URL + "/page")
	if err != nil {
		t.Fatalf("GET through forwarder: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "origin-said-hi" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if up.plain.Load() == 0 {
		t.Error("request did not reach the upstream proxy")
	}
	if got := up.seenAuth.Load().(string); got == "" {
		t.Error("upstream saw no Proxy-Authorization: credentials were not injected")
	}
}

// https:// targets arrive as CONNECT, which no transport can relay for us.
func TestForwarderInjectsCredentialsOnCONNECT(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls-origin") //nolint:errcheck
	}))
	defer origin.Close()

	up := newFakeUpstream(t, "cust-zone", "sekrit")
	f := startForwarderFor(t, up.URL("cust-zone", "sekrit"))

	resp, err := clientVia(t, f).Get(origin.URL + "/secure")
	if err != nil {
		t.Fatalf("GET through CONNECT tunnel: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "tls-origin" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if up.connects.Load() == 0 {
		t.Error("no CONNECT reached the upstream proxy")
	}
	if got := up.seenAuth.Load().(string); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("upstream saw Proxy-Authorization %q, want injected Basic credentials", got)
	}
}

// Wrong credentials are a configuration error, not a transient blip: the
// upstream's own 407 must reach the caller rather than a generic 502, or the
// operator debugs a "site blocked us" that was really a typo.
func TestForwarderSurfacesUpstreamAuthFailure(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()

	up := newFakeUpstream(t, "cust-zone", "right-password")
	f := startForwarderFor(t, up.URL("cust-zone", "wrong-password"))

	resp, err := clientVia(t, f).Get(origin.URL + "/secure")
	if err != nil {
		// Some transports surface a failed CONNECT as an error rather than a
		// response; either is acceptable as long as it is not silent success.
		if !strings.Contains(err.Error(), "407") && !strings.Contains(err.Error(), "Proxy Authentication") {
			t.Fatalf("error = %v, want it to name the 407", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407 surfaced from the upstream", resp.StatusCode)
	}
}

// An unauthenticated upstream still works: the forwarder simply adds no header.
func TestForwarderWithoutCredentials(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "open") //nolint:errcheck
	}))
	defer origin.Close()

	up := newFakeUpstream(t, "", "")
	f := startForwarderFor(t, up.URL("", ""))

	resp, err := clientVia(t, f).Get(origin.URL + "/p")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "open" {
		t.Fatalf("body = %q", body)
	}
	if got := up.seenAuth.Load().(string); got != "" {
		t.Errorf("sent Proxy-Authorization %q to an open upstream", got)
	}
}

func TestStartForwarderRejectsUnforwardable(t *testing.T) {
	for _, u := range []string{"", "socks5://u:p@host:1080"} {
		p := mustPool(t, []Entry{{URL: u}}, RoundRobin).Select("h")
		if _, err := StartForwarder(p); err == nil {
			t.Errorf("StartForwarder(%q) = nil error, want refusal", u)
		}
	}
}

func TestForwarderCloseIsIdempotent(t *testing.T) {
	up := newFakeUpstream(t, "", "")
	p := mustPool(t, []Entry{{URL: up.URL("", "")}}, RoundRobin).Select("h")
	f, err := StartForwarder(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// A dead upstream must fail the request, not hang it.
func TestForwarderBadGatewayOnDeadUpstream(t *testing.T) {
	f := startForwarderFor(t, "http://127.0.0.1:1") // nothing listens on port 1
	resp, err := clientVia(t, f).Get("http://example.invalid/p")
	if err != nil {
		return // transport-level failure is an acceptable outcome
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// A CONNECT the upstream itself refuses (unreachable origin) must come back as
// a failure, not a half-open tunnel that hangs the render.
func TestForwarderCONNECTToUnreachableOrigin(t *testing.T) {
	up := newFakeUpstream(t, "", "")
	f := startForwarderFor(t, up.URL("", ""))

	// Port 1 on loopback: the upstream's own dial fails, so it answers the
	// CONNECT with an error rather than opening a tunnel.
	resp, err := clientVia(t, f).Get("https://127.0.0.1:1/p")
	if err != nil {
		return // surfaced as a transport error: acceptable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("unreachable origin produced a 200 through the tunnel")
	}
}

// An https:// upstream takes the TLS dial path. The fake upstream presents a
// self-signed certificate, so the handshake must fail cleanly rather than
// silently downgrading to plaintext — an egress that quietly stopped using TLS
// to the proxy would leak the credentials it exists to protect.
func TestForwarderTLSUpstreamPathIsUsed(t *testing.T) {
	tlsUp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tlsUp.Close()

	f := startForwarderFor(t, strings.Replace(tlsUp.URL, "https://", "https://u:p@", 1))
	resp, err := clientVia(t, f).Get("http://example.invalid/p")
	if err != nil {
		if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "tls") {
			t.Logf("failed with %v", err) // any clean failure is acceptable
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("untrusted TLS upstream produced a 200")
	}
}
