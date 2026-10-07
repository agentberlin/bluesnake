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
	"time"
)

// ErrProxyAuth is a proxy refusing the configured credentials (407). It is a
// configuration error, never a ban: the crawl must stop, not carry on.
var ErrProxyAuth = errors.New("proxy rejected the configured credentials (407 Proxy Authentication Required)")

// Probe checks that an egress can open a tunnel to target ("host:port")
// without sending anything to the target itself: for an http(s) proxy it
// performs the CONNECT handshake with the configured credentials and closes the
// tunnel; for a SOCKS proxy it checks the proxy accepts a TCP connection. A
// direct egress always passes. Returns ErrProxyAuth (wrapped) on a 407.
//
// It exists for http.proxy_on_block, where the proxy carries nothing until the
// site blocks the crawl — without a probe, a wrong password surfaces only after
// the switch, as a wall of failed pages.
func Probe(ctx context.Context, p *Proxy, target string) error {
	if p.Direct() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d := &net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", p.dialAddr)
	if err != nil {
		return fmt.Errorf("proxy %s unreachable: %w", p.Label(), err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	switch p.URL.Scheme {
	case "socks5", "socks5h":
		return nil // reachable; SOCKS auth is only exercised by a real request
	case "https":
		tc := tls.Client(conn, &tls.Config{ServerName: p.URL.Hostname()})
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("proxy %s: TLS handshake: %w", p.Label(), err)
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
		return fmt.Errorf("proxy %s: %w", p.Label(), err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return fmt.Errorf("proxy %s: reading CONNECT answer: %w", p.Label(), err)
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return fmt.Errorf("proxy %s: %w", p.Label(), ErrProxyAuth)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("proxy %s refused a tunnel to %s: %s", p.Label(), target, resp.Status)
	}
	return nil
}
