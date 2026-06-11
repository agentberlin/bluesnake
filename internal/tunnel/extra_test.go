package tunnel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestEnsureIdentityRegisters(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tunnel_id":      "freshtunnel1",
			"connect_secret": "cs",
			"public_host":    "freshtunnel1.t.snake.blue",
			"connect_addr":   "connect.t.snake.blue:443",
		})
	}))
	defer srv.Close()
	t.Setenv(envAPIBase, srv.URL)

	id, err := EnsureIdentity(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if id.TunnelID != "freshtunnel1" {
		t.Errorf("tunnel id = %q", id.TunnelID)
	}
	// Persisted: a second call returns the same identity without registering.
	if _, err := os.Stat(IdentityPath(dir)); err != nil {
		t.Errorf("identity not persisted: %v", err)
	}
	again, err := EnsureIdentity(context.Background(), dir)
	if err != nil || again.TunnelID != id.TunnelID {
		t.Errorf("second EnsureIdentity = (%v, %v)", again, err)
	}
}

func TestAPIBaseDefault(t *testing.T) {
	t.Setenv(envAPIBase, "")
	if got := APIBase(); got != DefaultAPIBase {
		t.Errorf("APIBase = %q, want default %q", got, DefaultAPIBase)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true, "127.5.5.5": true,
		"": false, "example.com": false, "8.8.8.8": false, "192.0.2.1": false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// TestRunReconnectsOnError points the client at a TCP listener that accepts then
// immediately closes, so each dial fails the TLS/auth step and Run loops through
// its error→backoff→retry path until the context is cancelled.
func TestRunReconnectsOnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close() // drop immediately → client dial/handshake fails
		}
	}()

	id := &Identity{
		TunnelID: "abcabcabcabc", ConnectSecret: "s",
		PublicHost: "abcabcabcabc.t.snake.blue", ConnectAddr: ln.Addr().String(),
	}
	var mu sync.Mutex
	sawError := false
	c := New(Config{
		Identity:           id,
		LocalAddr:          "127.0.0.1:1",
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         10 * time.Millisecond,
		OnStatus: func(s Status) {
			if s.State == StateError {
				mu.Lock()
				sawError = true
				mu.Unlock()
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if !sawError {
		t.Error("expected at least one StateError during reconnect loop")
	}
	if c.Status().State != StateStopped {
		t.Errorf("final state = %q, want stopped after ctx cancel", c.Status().State)
	}
}
