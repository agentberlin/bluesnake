package proxypool

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Forwarder is a credential-injecting shim between Chrome and an upstream
// proxy, and it exists because Chrome will not accept proxy credentials.
//
// Chrome takes its egress from --proxy-server, and that flag has no place for a
// username or password: the user:pass@host:port form is rejected outright ("no
// supported proxies"). The protocol-level alternative — enabling the CDP Fetch
// domain with handleAuthRequests and answering Fetch.authRequired — pauses every
// request in the tab awaiting an explicit continue, which is a large behavioural
// change to the render path for a small problem, and is reported not to fire
// reliably. Neither is acceptable for a provider like Bright Data, where the
// credentials ARE the configuration: the customer id, zone and session all ride
// in the proxy username.
//
// So: bind an unauthenticated listener on loopback, hand Chrome that address,
// and add the Proxy-Authorization header on the way out. Chrome never sees a
// credential, nothing is intercepted, and no CDP domain is enabled.
//
// A Forwarder serves exactly one upstream egress, so rotating renders across N
// proxies means N forwarders. Only http:// and https:// upstreams are supported
// (SOCKS carries no HTTP header to inject into); callers check Forwardable
// first.
type Forwarder struct {
	ln       net.Listener
	srv      *http.Server
	upstream *url.URL
	auth     string // pre-encoded "Basic …", empty when the upstream needs none
	tr       *http.Transport
	once     sync.Once

	// Switchable mode (http.proxy_on_block): while direct is set, the
	// forwarder connects to targets itself instead of through the upstream,
	// so Chrome starts from this machine's IP. Escalate clears it and cuts
	// every open tunnel, so Chrome re-dials — through the upstream.
	direct   atomic.Bool
	directTr *http.Transport
	tmu      sync.Mutex
	tunnels  map[net.Conn]struct{}
}

// Forwardable reports whether a forwarder can carry this egress. A direct
// egress needs none, and SOCKS proxies have no header to inject credentials
// into — Chrome must be pointed at those directly, which works only when they
// are unauthenticated.
func Forwardable(p *Proxy) bool {
	if p.Direct() {
		return false
	}
	return p.URL.Scheme == "http" || p.URL.Scheme == "https"
}

// NeedsForwarder reports whether Chrome cannot use this egress directly: it
// carries credentials, which --proxy-server cannot express.
func NeedsForwarder(p *Proxy) bool {
	return !p.Direct() && p.URL.User != nil
}

// StartForwarder binds a loopback listener that forwards to p, injecting p's
// credentials. The caller owns the returned Forwarder and must Close it.
func StartForwarder(p *Proxy) (*Forwarder, error) {
	if !Forwardable(p) {
		return nil, fmt.Errorf("proxypool: cannot forward to %s (only http and https upstreams carry credentials)", p.Label())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &Forwarder{ln: ln, upstream: p.URL, tunnels: map[net.Conn]struct{}{}}
	if u := p.URL.User; u != nil {
		pw, _ := u.Password()
		f.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pw))
	}
	// Plain-HTTP requests ride a transport pointed at the upstream, which adds
	// Proxy-Authorization from the URL's userinfo itself. CONNECT is handled by
	// hand below, because a transport cannot relay an opaque tunnel.
	f.tr = &http.Transport{
		Proxy:               http.ProxyURL(p.URL),
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	f.srv = &http.Server{
		Handler:           f,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

// StartSwitchableForwarder is StartForwarder for http.proxy_on_block: Chrome
// is always pointed at the loopback listener, which starts out connecting
// direct (unless startDirect is false — a resumed crawl that already switched)
// and moves to p on Escalate. p may be an unauthenticated http(s) proxy too.
func StartSwitchableForwarder(p *Proxy, startDirect bool) (*Forwarder, error) {
	f, err := StartForwarder(p)
	if err != nil {
		return nil, err
	}
	f.directTr = &http.Transport{
		Proxy:               nil,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	}
	f.direct.Store(startDirect)
	return f, nil
}

// Escalate moves a switchable forwarder from direct to the upstream proxy and
// closes every live tunnel and idle direct connection, so no request made after
// the switch can ride a connection opened before it. Idempotent; a no-op on a
// forwarder that was never direct.
func (f *Forwarder) Escalate() {
	if !f.direct.Swap(false) {
		return
	}
	f.tmu.Lock()
	for c := range f.tunnels {
		c.Close()
	}
	f.tunnels = map[net.Conn]struct{}{}
	f.tmu.Unlock()
	if f.directTr != nil {
		f.directTr.CloseIdleConnections()
	}
}

func (f *Forwarder) track(conns ...net.Conn) {
	f.tmu.Lock()
	for _, c := range conns {
		f.tunnels[c] = struct{}{}
	}
	f.tmu.Unlock()
}

func (f *Forwarder) untrack(conns ...net.Conn) {
	f.tmu.Lock()
	for _, c := range conns {
		delete(f.tunnels, c)
	}
	f.tmu.Unlock()
}

// ProxyServer is the value to hand Chrome's --proxy-server: a loopback address
// with no credentials in it.
func (f *Forwarder) ProxyServer() string {
	return "http://" + f.ln.Addr().String()
}

// Close stops the listener and releases the upstream connections.
func (f *Forwarder) Close() error {
	var err error
	f.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = f.srv.Shutdown(ctx)
		f.tr.CloseIdleConnections()
		if f.directTr != nil {
			f.directTr.CloseIdleConnections()
		}
	})
	return err
}

func (f *Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		f.connect(w, r)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = "" // required: a client request must not carry RequestURI
	tr := f.tr
	if f.direct.Load() {
		tr = f.directTr
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// connect relays an opaque CONNECT tunnel: dial the upstream proxy, ask it to
// open the tunnel with our credentials attached, then splice the two sockets.
func (f *Forwarder) connect(w http.ResponseWriter, r *http.Request) {
	if f.direct.Load() {
		f.connectDirect(w, r)
		return
	}
	up, err := f.dialUpstream(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req := "CONNECT " + r.Host + " HTTP/1.1\r\nHost: " + r.Host + "\r\n"
	if f.auth != "" {
		req += "Proxy-Authorization: " + f.auth + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(up, req); err != nil {
		up.Close()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	br := bufio.NewReader(up)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		up.Close()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		up.Close()
		// Surface the upstream's verdict rather than a generic failure: a 407
		// here means the configured proxy credentials are wrong, which is a
		// config error the operator needs to see, not a transient blip.
		http.Error(w, "upstream proxy: "+resp.Status, resp.StatusCode)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "connect unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		up.Close()
		client.Close()
		return
	}
	// Read through br, not up: the CONNECT response parse may have buffered
	// the first bytes of the tunnel, and reading the socket directly would
	// silently drop them.
	f.splice(client, up, br)
}

// connectDirect opens the tunnel to the target itself — the switchable
// forwarder's pre-switch mode.
func (f *Forwarder) connectDirect(w http.ResponseWriter, r *http.Request) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	up, err := d.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "connect unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		up.Close()
		client.Close()
		return
	}
	f.splice(client, up, up)
}

// splice relays bytes both ways until either side closes, tracking both
// sockets so Escalate can cut the tunnel.
func (f *Forwarder) splice(client, up net.Conn, fromUp io.Reader) {
	f.track(client, up)
	closeBoth := func() {
		up.Close()
		client.Close()
		f.untrack(client, up)
	}
	go func() {
		defer closeBoth()
		_, _ = io.Copy(up, client)
	}()
	go func() {
		defer closeBoth()
		_, _ = io.Copy(client, fromUp)
	}()
}

func (f *Forwarder) dialUpstream(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	addr := dialAddr(f.upstream)
	if f.upstream.Scheme == "https" {
		return tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: f.upstream.Hostname()})
	}
	return d.DialContext(ctx, "tcp", addr)
}
