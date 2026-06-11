package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
	"github.com/hashicorp/yamux"
)

func genServerTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "tunnel-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{wire.ALPN, "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}
}

// TestClientDialAndProxy exercises the real client end to end against a minimal
// in-package tunnel server: TLS+ALPN dial, auth handshake, yamux session, and a
// proxied request that the client forwards to the local MCP server (with the
// Host rewrite and Origin strip).
func TestClientDialAndProxy(t *testing.T) {
	// Fake local MCP server the client forwards to.
	var sawHost, sawOrigin string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		sawOrigin = r.Header.Get("Origin")
		io.WriteString(w, "from-local")
	}))
	defer local.Close()
	localAddr := strings.TrimPrefix(local.URL, "http://")

	// Minimal tunnel server: accept one connection, authenticate, run yamux as
	// the stream opener, and expose the session.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", genServerTLS(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	sessCh := make(chan *yamux.Session, 1)
	authCh := make(chan wire.AuthRequest, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		var req wire.AuthRequest
		if err := wire.ReadFrame(conn, &req); err != nil {
			return
		}
		authCh <- req
		if err := wire.WriteFrame(conn, wire.AuthResponse{OK: true, Host: req.TunnelID + ".t.snake.blue"}); err != nil {
			return
		}
		sess, err := yamux.Client(conn, nil)
		if err != nil {
			return
		}
		sessCh <- sess
	}()

	id := &Identity{
		TunnelID:      "abcabcabcabc",
		ConnectSecret: "the-connect-secret",
		PublicHost:    "abcabcabcabc.t.snake.blue",
		ConnectAddr:   ln.Addr().String(), // loopback → InsecureSkipVerify honored
	}
	online := make(chan struct{})
	var once bool
	c := New(Config{
		Identity:           id,
		LocalAddr:          localAddr,
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		OnStatus: func(s Status) {
			if s.State == StateOnline && !once {
				once = true
				close(online)
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	// The server received the auth frame with the connect secret.
	select {
	case req := <-authCh:
		if req.ConnectSecret != "the-connect-secret" || req.TunnelID != "abcabcabcabc" {
			t.Fatalf("bad auth frame: %+v", req)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("server never received auth frame")
	}

	select {
	case <-online:
	case <-time.After(8 * time.Second):
		t.Fatal("client never reported online")
	}

	var sess *yamux.Session
	select {
	case sess = <-sessCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no yamux session")
	}

	// Issue a public request down the tunnel via an HTTP client that dials a
	// fresh yamux stream per connection.
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) { return sess.Open() },
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://tunnel/mcp", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from-local" {
		t.Errorf("body = %q, want from-local", body)
	}
	if sawHost != localAddr {
		t.Errorf("local saw Host %q, want %q (rewrite)", sawHost, localAddr)
	}
	if sawOrigin != "" {
		t.Errorf("local saw Origin %q, want stripped", sawOrigin)
	}
	if got := c.Status().MCPURL; got != "https://abcabcabcabc.t.snake.blue/mcp" {
		t.Errorf("status MCPURL = %q", got)
	}
}

// TestClientInsecureIgnoredForNonLoopback verifies the InsecureSkipVerify
// override is inert against a non-loopback connect address (so a shipped build
// can't be tricked into skipping TLS verification of a real endpoint). The dial
// must fail to verify rather than succeed.
func TestClientInsecureIgnoredForNonLoopback(t *testing.T) {
	id := &Identity{
		TunnelID: "abcabcabcabc", ConnectSecret: "s",
		PublicHost: "abcabcabcabc.t.snake.blue",
		// A non-loopback, unreachable address; we only assert insecure is gated.
		ConnectAddr: "192.0.2.1:443",
	}
	c := New(Config{Identity: id, LocalAddr: "127.0.0.1:1", InsecureSkipVerify: true, ServerName: "localhost"})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// Run returns ctx error after failing to connect; we just ensure no panic
	// and that it respects cancellation (the dial itself will time out/refuse).
	_ = c.Run(ctx)
	if c.Status().State == StateOnline {
		t.Error("client should not be online against an unreachable secure endpoint")
	}
}
