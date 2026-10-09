package proxypool

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ErrProxyAuth is a proxy refusing the configured credentials (407). It is a
// configuration error, never a ban: the crawl must stop, not carry on.
var ErrProxyAuth = errors.New("proxy rejected the configured credentials (407 Proxy Authentication Required)")

// ErrProbeInconclusive marks a probe answer that proves neither a working nor
// a broken fallback — a CONNECT refused for a port, a passing 502 from the
// gateway, a timeout after the proxy accepted the connection. The caller warns
// and carries on: the fallback may never be needed, and refusing the crawl
// over a transient answer would block crawls that would have worked.
var ErrProbeInconclusive = errors.New("fallback proxy check inconclusive")

// Probe checks, before a proxy_on_block crawl starts, that egress p can
// carry the crawl's requests to target (the seed URL). It fails hard only on
// what certainly breaks the fallback: an unreachable proxy (a plain error) or
// rejected credentials (wraps ErrProxyAuth). Any other answer wraps
// ErrProbeInconclusive. A direct egress always passes.
//
// The probe takes the same shape the crawl will: for an https:// target the
// CONNECT handshake (closed once answered — nothing is sent to the target);
// for an http:// target, which the client never tunnels, one absolute-form
// HEAD of the target through the proxy, the request a forward proxy checks
// credentials on. A SOCKS proxy is checked for reachability only.
//
// It exists for http.proxy_on_block, where the proxy carries nothing until the
// site blocks the crawl — without a probe, a wrong password surfaces only after
// the switch, as a wall of failed pages.
func Probe(ctx context.Context, p *Proxy, target *url.URL) error {
	if p.Direct() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", p.dialAddr)
	if err != nil {
		return fmt.Errorf("proxy %s unreachable: %w", p.Label(), err)
	}
	switch {
	case p.URL.Scheme == "socks5" || p.URL.Scheme == "socks5h":
		conn.Close()
		return nil // reachable; SOCKS auth is only exercised by a real request
	case target.Scheme == "http":
		conn.Close()
		return probeForward(ctx, p, target)
	}
	defer conn.Close()
	return probeConnect(ctx, p, conn, hostPort(target))
}

// probeConnect performs the CONNECT handshake an https crawl will.
func probeConnect(ctx context.Context, p *Proxy, conn net.Conn, target string) error {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if p.URL.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: p.URL.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("%w: proxy %s: TLS handshake: %v", ErrProbeInconclusive, p.Label(), err)
		}
		conn = tc
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u := p.URL.User; u != nil {
		pw, _ := u.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pw)) + "\r\n"
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return fmt.Errorf("%w: proxy %s: %v", ErrProbeInconclusive, p.Label(), err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fmt.Errorf("%w: proxy %s: reading CONNECT answer: %v", ErrProbeInconclusive, p.Label(), err)
	}
	resp.Body.Close()
	return verdict(p, resp.StatusCode, resp.Status, "a tunnel to "+target)
}

// probeForward sends the one absolute-form request an http crawl would.
func probeForward(ctx context.Context, p *Proxy, target *url.URL) error {
	tr := &http.Transport{Proxy: http.ProxyURL(p.URL), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return err
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("%w: proxy %s: %v", ErrProbeInconclusive, p.Label(), err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return fmt.Errorf("proxy %s: %w", p.Label(), ErrProxyAuth)
	}
	// Any other status is the site's answer relayed by the proxy (or the
	// proxy's own error page): the credentials were accepted.
	return nil
}

func verdict(p *Proxy, code int, status, what string) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusProxyAuthRequired:
		return fmt.Errorf("proxy %s: %w", p.Label(), ErrProxyAuth)
	}
	return fmt.Errorf("%w: proxy %s refused %s: %s", ErrProbeInconclusive, p.Label(), what, status)
}

// hostPort is the CONNECT authority for u, with the scheme's default port.
func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "443"
	if u.Scheme == "http" {
		port = "80"
	}
	return net.JoinHostPort(u.Hostname(), port)
}
