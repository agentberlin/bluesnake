package server_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/server"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

const (
	baseDomain = "t.snake.blue"
	apiHost    = "api.snake.blue"
)

// clientTo builds an HTTPS client that ignores DNS and always dials addr, so a
// request to https://anything.t.snake.blue reaches the test server while still
// carrying the right Host/SNI.
func clientTo(addr string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
			},
		},
	}
}

// startServer brings up a dev-TLS tunnel server on a random port and returns
// its address, store, and a cancel func.
func startServer(t *testing.T) (addr string, st *store.Mem) {
	t.Helper()
	tlsCfg, err := server.DevTLSConfig("*."+baseDomain, baseDomain, apiHost)
	if err != nil {
		t.Fatal(err)
	}
	st = store.NewMem()
	srv, err := server.New(server.Config{
		Store:       st,
		BaseDomain:  baseDomain,
		APIHost:     apiHost,
		ConnectAddr: "placeholder:443", // overridden per-test in the identity
		TLSConfig:   tlsCfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	rawLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, tls.NewListener(rawLn, tlsCfg)) }()
	return rawLn.Addr().String(), st
}

// register hits the control plane and returns the registration response.
func register(t *testing.T, addr string) map[string]string {
	t.Helper()
	resp, err := clientTo(addr).Post("https://"+apiHost+"/v1/register", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("register status %d", resp.StatusCode)
	}
	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// startClient wires a tunnel.Client to the server, forwarding to localAddr, and
// blocks until it reports online.
func startClient(t *testing.T, serverAddr, localAddr string, reg map[string]string) *tunnel.Identity {
	t.Helper()
	id := &tunnel.Identity{
		TunnelID:      reg["tunnel_id"],
		ConnectSecret: reg["connect_secret"],
		PublicHost:    reg["public_host"],
		ConnectAddr:   serverAddr, // dial the test server directly
		APIBase:       "https://" + apiHost,
	}
	online := make(chan struct{})
	var closedOnce bool
	c := tunnel.New(tunnel.Config{
		Identity:           id,
		LocalAddr:          localAddr,
		InsecureSkipVerify: true,
		ServerName:         "connect." + baseDomain,
		OnStatus: func(s tunnel.Status) {
			if s.State == tunnel.StateOnline && !closedOnce {
				closedOnce = true
				close(online)
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()

	select {
	case <-online:
	case <-time.After(8 * time.Second):
		t.Fatal("tunnel client did not come online")
	}
	return id
}

func TestEndToEndProxy(t *testing.T) {
	addr, _ := startServer(t)

	// Fake local MCP server: asserts the client rewrote Host to localhost.
	var sawHost, sawPath string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost = r.Host
		sawPath = r.URL.Path
		io.WriteString(w, `{"jsonrpc":"2.0","result":"pong"}`)
	}))
	defer local.Close()
	localAddr := strings.TrimPrefix(local.URL, "http://")

	reg := register(t, addr)
	id := startClient(t, addr, localAddr, reg)

	// Public request through the tunnel.
	resp, err := clientTo(addr).Post(id.MCPURL(), "application/json", strings.NewReader(`{"jsonrpc":"2.0","method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("public request status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "pong") {
		t.Errorf("unexpected body: %s", body)
	}
	if sawPath != "/mcp" {
		t.Errorf("local MCP saw path %q, want /mcp", sawPath)
	}
	if sawHost != localAddr {
		t.Errorf("local MCP saw Host %q, want %q (DNS-rebinding rewrite)", sawHost, localAddr)
	}
}

func TestEndToEndOffline(t *testing.T) {
	addr, _ := startServer(t)

	// Unknown subdomain (no live tunnel) → friendly 502.
	resp, err := clientTo(addr).Get("https://neverexisted0.t.snake.blue/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("offline status = %d, want 502", resp.StatusCode)
	}
}
