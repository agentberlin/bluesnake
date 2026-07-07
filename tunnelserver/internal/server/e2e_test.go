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
	"sync"
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

// startClient wires a tunnel.Client to the server, forwarding to localAddr,
// blocks until it reports online, and returns the identity plus a stop func
// that simulates the user quitting the app.
func startClient(t *testing.T, serverAddr, localAddr string, reg map[string]string) (*tunnel.Identity, func()) {
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
	return id, cancel
}

func TestEndToEndProxy(t *testing.T) {
	addr, _ := startServer(t)

	// Fake local MCP server: asserts the client rewrote Host to localhost.
	// Guarded by a mutex — the handler runs on the httptest server's goroutine
	// while the assertions read on the test goroutine.
	var mu sync.Mutex
	var sawHost, sawPath string
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawHost, sawPath = r.Host, r.URL.Path
		mu.Unlock()
		io.WriteString(w, `{"jsonrpc":"2.0","result":"pong"}`)
	}))
	defer local.Close()
	localAddr := strings.TrimPrefix(local.URL, "http://")

	reg := register(t, addr)
	id, _ := startClient(t, addr, localAddr, reg)

	// Public request through the tunnel, retried past the registration window.
	resp, body := postThroughTunnel(t, clientTo(addr), id.MCPURL(), `{"jsonrpc":"2.0","method":"ping"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("public request status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "pong") {
		t.Errorf("unexpected body: %s", body)
	}
	mu.Lock()
	gotHost, gotPath := sawHost, sawPath
	mu.Unlock()
	if gotPath != "/mcp" {
		t.Errorf("local MCP saw path %q, want /mcp", gotPath)
	}
	if gotHost != localAddr {
		t.Errorf("local MCP saw Host %q, want %q (DNS-rebinding rewrite)", gotHost, localAddr)
	}
}

// postThroughTunnel POSTs to the public URL, retrying while the server still
// returns the transient "tunnel not connected" 502.
//
// The tunnel client reports StateOnline (so startClient returns) the instant it
// has read the auth response and stood up its yamux session — but the SERVER
// registers the tunnel as routable a beat later: the gateway replies to auth,
// then adds the session to its routing registry. A public request fired in that
// sub-millisecond window (as this test does, immediately after "online") reaches
// the server before the registry entry exists and gets a 502. The window is
// benign in production (a user pastes the URL seconds later) but races the test
// under load. Polling the real public path is what we actually want to assert:
// that once the tunnel is up, requests proxy through.
func postThroughTunnel(t *testing.T, cl *http.Client, url, body string) (*http.Response, []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := cl.Post(url, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusBadGateway && strings.Contains(string(b), "not connected") && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return resp, b
	}
}

// TestEndToEndOfflineStub drives the full offline-stub lifecycle over the
// real wire: register → app connects (probe snapshots its tools) → app quits
// → the same public URL keeps answering MCP, with tool calls returning
// isError results instead of transport failures.
func TestEndToEndOfflineStub(t *testing.T) {
	addr, st := startServer(t)

	// Minimal but faithful local MCP server: initialize/tools/list for the
	// probe, tools/call to prove live proxying.
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"bluesnake","version":"3.3.3"},"instructions":"live instructions"}`
		case "tools/list":
			result = `{"tools":[{"name":"start_crawl","description":"d","inputSchema":{"type":"object"}}]}`
		case "tools/call":
			result = `{"content":[{"type":"text","text":"live result"}],"isError":false}`
		default:
			result = `{}`
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":`+result+`}`)
	}))
	defer local.Close()

	reg := register(t, addr)
	id, stopApp := startClient(t, addr, strings.TrimPrefix(local.URL, "http://"), reg)
	cl := clientTo(addr)

	// Live: tools/call proxies to the local server.
	resp, body := postThroughTunnel(t, cl, id.MCPURL(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"start_crawl"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "live result") {
		t.Fatalf("live tools/call = %d %s", resp.StatusCode, body)
	}

	// Wait for the connect-time probe to persist its snapshot before the
	// app goes away.
	waitUntil(t, 8*time.Second, "snapshot persisted", func() bool {
		tn, err := st.GetByID(context.Background(), reg["tunnel_id"])
		return err == nil && len(tn.MCPSnapshot) > 0
	})

	// The user quits the app.
	stopApp()

	// The same URL keeps answering: poll until the gateway notices the drop
	// and the stub takes over (tools/call flips from proxied to isError).
	var stubBody string
	waitUntil(t, 8*time.Second, "stub takeover", func() bool {
		resp, err := cl.Post(id.MCPURL(), "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"start_crawl"}}`))
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		stubBody = string(b)
		return resp.StatusCode == 200 && strings.Contains(stubBody, `"isError":true`)
	})
	if !strings.Contains(stubBody, "not running") || !strings.Contains(stubBody, "start_crawl") {
		t.Errorf("offline tools/call message = %s", stubBody)
	}

	// initialize still succeeds and reflects the snapshotted identity.
	resp, body = postJSON(t, cl, id.MCPURL(), `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "3.3.3") || !strings.Contains(string(body), "OFFLINE") {
		t.Errorf("offline initialize = %d %s", resp.StatusCode, body)
	}

	// tools/list serves the snapshotted tools.
	resp, body = postJSON(t, cl, id.MCPURL(), `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "start_crawl") {
		t.Errorf("offline tools/list = %d %s", resp.StatusCode, body)
	}

	// Notifications are accepted silently; GET mirrors the real server's 405.
	resp, _ = postJSON(t, cl, id.MCPURL(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("offline notification status = %d, want 202", resp.StatusCode)
	}
	getResp, err := cl.Get(id.MCPURL())
	if err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("offline GET status = %d, want 405", getResp.StatusCode)
	}
}

// postJSON is a single POST without the tunnel-window retry loop.
func postJSON(t *testing.T, cl *http.Client, url, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := cl.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// waitUntil polls cond until it holds or the deadline passes.
func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
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
