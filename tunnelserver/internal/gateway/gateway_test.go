package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// replyGateConn is the gateway's side of a tunnel connection that parks its
// first write — the auth reply — before it reaches the wire, until the test
// resumes it. Any other write arriving while the reply is parked means yamux
// bytes would precede the reply, and is flagged on early.
type replyGateConn struct {
	net.Conn
	writes   atomic.Int32
	replying chan struct{}
	resume   chan struct{}
	early    chan struct{}
}

func (c *replyGateConn) Write(p []byte) (int, error) {
	if c.writes.Add(1) == 1 {
		close(c.replying)
		<-c.resume
	} else {
		select {
		case <-c.resume:
		default:
			select {
			case c.early <- struct{}{}:
			default:
			}
		}
	}
	return c.Conn.Write(p)
}

// TestConnectRoutableBeforeAuthReply pins the connect ordering. The client
// reports online the moment it reads a successful AuthResponse, so the
// session must be routable before that reply is written; otherwise the first
// public requests after a connect reach the offline stub, and MCP clients are
// told the app is not running while it is. The gateway is frozen just before
// its reply, so the check is deterministic rather than a scheduling race. A
// public request landing in that window must queue behind the reply (yamux
// bytes ahead of it would corrupt the client's auth read) and then proxy to
// the live app.
func TestConnectRoutableBeforeAuthReply(t *testing.T) {
	g, st := newGateway(t)
	_ = st.Create(context.Background(), &store.Tunnel{ID: tunnelID, ConnectSecretHash: store.Hash("s")})

	gwConn, appConn := net.Pipe()
	rc := &replyGateConn{Conn: gwConn, replying: make(chan struct{}), resume: make(chan struct{}), early: make(chan struct{}, 1)}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(rc.resume) }) }
	handled := make(chan struct{})
	go func() { g.HandleConn(rc); close(handled) }()
	t.Cleanup(func() {
		resume()
		_ = appConn.Close()
		<-handled
	})

	if err := wire.WriteFrame(appConn, wire.AuthRequest{V: wire.Version, TunnelID: tunnelID, ConnectSecret: "s"}); err != nil {
		t.Fatal(err)
	}
	<-rc.replying
	if g.reg.Get(tunnelID) == nil {
		t.Fatal("auth reply is being written before the session is routable: public requests in that window get the offline stub")
	}

	// A public request arrives while the reply is still parked.
	inWindow := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		inWindow <- postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"start_crawl"}}`)
	}()
	select {
	case <-rc.early:
		t.Fatal("yamux bytes reached the wire ahead of the auth reply")
	case <-time.After(50 * time.Millisecond):
	}
	resume()

	// The app reads its reply and comes online, as tunnel.Client does.
	var resp wire.AuthResponse
	if err := wire.ReadFrame(appConn, &resp); err != nil || !resp.OK {
		t.Fatalf("auth reply = %+v, %v", resp, err)
	}
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		result := `{}`
		if req.Method == "tools/call" {
			result = `{"content":[{"type":"text","text":"live result"}],"isError":false}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":`+result+`}`)
	})
	appSess, err := yamux.Server(appConn, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: backend}
	go func() { _ = srv.Serve(appSess) }()
	t.Cleanup(func() { _ = srv.Close(); _ = appSess.Close() })

	select {
	case rec := <-inWindow:
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "live result") {
			t.Errorf("request during the auth reply = %d %s, want the live app's result", rec.Code, rec.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("request during the auth reply never completed")
	}
}

// TestGatedConnCloseReleasesHeldWrite: when the auth reply never goes out,
// closing the session must fail a write held at the gate rather than strand
// yamux's send loop — yamux's Close waits for that loop, so a stuck write
// would hang HandleConn.
func TestGatedConnCloseReleasesHeldWrite(t *testing.T) {
	gwConn, appConn := net.Pipe()
	gc := newGatedConn(gwConn)
	done := make(chan error, 1)
	go func() { _, err := gc.Write([]byte("yamux")); done <- err }()

	_ = gc.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("held write after Close = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left a held write blocked")
	}
	if b, err := io.ReadAll(appConn); len(b) != 0 || err != nil {
		t.Errorf("peer read %q, %v; want nothing written", b, err)
	}
}
