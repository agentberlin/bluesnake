package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/registry"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
	"github.com/hashicorp/yamux"
)

func newGateway(t *testing.T) (*Gateway, *store.Mem) {
	t.Helper()
	st := store.NewMem()
	return New(registry.New(), st, "t.snake.blue", nil), st
}

func TestSubdomainLabel(t *testing.T) {
	g, _ := newGateway(t)
	cases := []struct {
		host      string
		wantLabel string
		wantOK    bool
	}{
		{"abc123.t.snake.blue", "abc123", true},
		{"abc123.t.snake.blue:443", "abc123", true},
		{"ABC123.T.SNAKE.BLUE", "abc123", true},
		{"t.snake.blue", "", false},     // bare base domain
		{"a.b.t.snake.blue", "", false}, // multi-level label
		{"abc123.evil.com", "", false},  // wrong zone
		{"abc123.t.snake.blue.evil.com", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := g.subdomainLabel(c.host)
		if got != c.wantLabel || ok != c.wantOK {
			t.Errorf("subdomainLabel(%q) = (%q,%v), want (%q,%v)", c.host, got, ok, c.wantLabel, c.wantOK)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	g, st := newGateway(t)
	ctx := context.Background()
	_ = st.Create(ctx, &store.Tunnel{ID: "good", ConnectSecretHash: store.Hash("secret")})
	_ = st.Create(ctx, &store.Tunnel{ID: "dead", ConnectSecretHash: store.Hash("secret"), Revoked: true})

	cases := []struct {
		name    string
		req     wire.AuthRequest
		wantErr error
	}{
		{"valid", wire.AuthRequest{V: wire.Version, TunnelID: "good", ConnectSecret: "secret"}, nil},
		{"wrong secret", wire.AuthRequest{V: wire.Version, TunnelID: "good", ConnectSecret: "nope"}, errUnauthorized},
		{"unknown id", wire.AuthRequest{V: wire.Version, TunnelID: "ghost", ConnectSecret: "secret"}, errUnauthorized},
		{"revoked", wire.AuthRequest{V: wire.Version, TunnelID: "dead", ConnectSecret: "secret"}, errUnauthorized},
		{"bad version", wire.AuthRequest{V: 99, TunnelID: "good", ConnectSecret: "secret"}, errUnauthorized},
	}
	for _, c := range cases {
		_, err := g.authenticate(c.req)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: authenticate err = %v, want %v", c.name, err, c.wantErr)
		}
	}
}

// failingStore simulates a store outage: every lookup errors.
type failingStore struct{ store.Store }

func (failingStore) GetByID(context.Context, string) (*store.Tunnel, error) {
	return nil, errors.New("db down")
}

// TestAuthenticateStoreOutage: a store outage must surface as "temporarily
// unavailable", never as a credential rejection — clients (and users) react to
// "unauthorized" by discarding tunnel.json, permanently losing the subdomain.
func TestAuthenticateStoreOutage(t *testing.T) {
	g := New(registry.New(), failingStore{}, "t.snake.blue", nil)
	_, err := g.authenticate(wire.AuthRequest{V: wire.Version, TunnelID: "good", ConnectSecret: "secret"})
	if !errors.Is(err, errUnavailable) {
		t.Errorf("store outage err = %v, want errUnavailable", err)
	}
}

// fakeApp wires a client-side yamux session that serves backend over the
// tunnel, and returns the gateway-side session pointing at it.
func fakeApp(t *testing.T, backend http.Handler) *yamux.Session {
	t.Helper()
	gwConn, appConn := net.Pipe()
	gwSess, err := yamux.Client(gwConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	appSess, err := yamux.Server(appConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: backend}
	go func() { _ = srv.Serve(appSess) }()
	t.Cleanup(func() { srv.Close(); gwSess.Close(); appSess.Close() })
	return gwSess
}

func TestPublicHandlerProxies(t *testing.T) {
	g, _ := newGateway(t)

	var gotPath, gotProto, gotFwdHost, gotXFFExtra string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotProto = r.Header.Get("X-Forwarded-Proto")
		gotFwdHost = r.Header.Get("X-Forwarded-Host")
		gotXFFExtra = r.Header.Get("X-Spoofed")
		io.WriteString(w, "hello from app")
	})
	gwSess := fakeApp(t, backend)

	sess := registry.NewSession("abc123", "abc123.t.snake.blue", gwSess, time.Now())
	sess.Handler = g.proxyFor(sess)
	g.reg.Add(sess)

	req := httptest.NewRequest(http.MethodPost, "http://abc123.t.snake.blue/mcp", strings.NewReader("{}"))
	req.Host = "abc123.t.snake.blue"
	req.Header.Set("X-Forwarded-Proto", "http") // attempt to spoof
	req.Header.Set("X-Spoofed", "1")
	rec := httptest.NewRecorder()

	g.PublicHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello from app" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if gotPath != "/mcp" {
		t.Errorf("backend saw path %q, want /mcp", gotPath)
	}
	if gotProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https (spoof overwritten)", gotProto)
	}
	if gotFwdHost != "abc123.t.snake.blue" {
		t.Errorf("X-Forwarded-Host = %q", gotFwdHost)
	}
	if gotXFFExtra != "1" {
		t.Errorf("non-forwarding custom header should pass through, got %q", gotXFFExtra)
	}
}

func TestPublicHandlerOffline(t *testing.T) {
	g, _ := newGateway(t)
	req := httptest.NewRequest(http.MethodGet, "http://nobody.t.snake.blue/mcp", nil)
	req.Host = "nobody.t.snake.blue"
	rec := httptest.NewRecorder()
	g.PublicHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("offline status = %d, want 502", rec.Code)
	}
}

func TestPublicHandlerBadHost(t *testing.T) {
	g, _ := newGateway(t)
	req := httptest.NewRequest(http.MethodGet, "http://evil.com/x/mcp", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	g.PublicHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("bad host status = %d, want 404", rec.Code)
	}
}
