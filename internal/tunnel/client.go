package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
	"github.com/hashicorp/yamux"
)

// State is the tunnel client's connection state, surfaced to the UI/CLI.
type State string

const (
	StateConnecting State = "connecting"
	StateOnline     State = "online"
	StateError      State = "error"
	StateStopped    State = "stopped"
)

// Status is a snapshot the embedding app renders. PublicURL/MCPURL are the
// display origin and the full capability URL respectively.
type Status struct {
	State     State
	PublicURL string
	MCPURL    string
	Err       string
}

// Config drives a Client. LocalAddr is the address of the already-running
// local MCP server (e.g. "127.0.0.1:8473"); the tunnel forwards public
// requests there.
type Config struct {
	Identity  *Identity
	LocalAddr string

	// OnStatus, if set, is called on every state transition. It must not
	// block.
	OnStatus func(Status)

	// InsecureSkipVerify and ServerName support development against a local
	// tunnel server with a self-signed cert. Unused in production.
	InsecureSkipVerify bool
	ServerName         string

	// Backoff bounds; zero means defaults.
	MinBackoff, MaxBackoff time.Duration
}

// Client maintains one outbound multiplexed tunnel and reconnects forever
// until its context is cancelled.
type Client struct {
	cfg Config

	mu     sync.Mutex
	status Status
}

// New constructs a Client. It does not dial; call Run.
func New(cfg Config) *Client {
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	return &Client{cfg: cfg, status: Status{
		State:     StateConnecting,
		PublicURL: cfg.Identity.PublicURL(),
		MCPURL:    cfg.Identity.MCPURL(),
	}}
}

// Status returns the latest snapshot.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) setState(s State, err error) {
	c.mu.Lock()
	c.status.State = s
	if err != nil {
		c.status.Err = err.Error()
	} else {
		c.status.Err = ""
	}
	c.status.PublicURL = c.cfg.Identity.PublicURL()
	c.status.MCPURL = c.cfg.Identity.MCPURL()
	snap := c.status
	cb := c.cfg.OnStatus
	c.mu.Unlock()
	if cb != nil {
		cb(snap)
	}
}

// Run connects and serves until ctx is cancelled, reconnecting with
// exponential backoff and jitter between attempts. It always returns
// ctx.Err() on cancellation.
func (c *Client) Run(ctx context.Context) error {
	backoff := c.cfg.MinBackoff
	attempt := 0
	for {
		if ctx.Err() != nil {
			c.setState(StateStopped, nil)
			return ctx.Err()
		}
		c.setState(StateConnecting, nil)
		err := c.dialAndServe(ctx)
		if ctx.Err() != nil {
			c.setState(StateStopped, nil)
			return ctx.Err()
		}
		c.setState(StateError, err)

		// Exponential backoff with decorrelated jitter, derived from the
		// attempt count (no global RNG so tests stay deterministic-ish).
		attempt++
		backoff = c.cfg.MinBackoff << min(attempt, 5)
		if backoff > c.cfg.MaxBackoff {
			backoff = c.cfg.MaxBackoff
		}
		jitter := time.Duration(int64(time.Now().UnixNano()) % int64(backoff/2+1))
		select {
		case <-ctx.Done():
			c.setState(StateStopped, nil)
			return ctx.Err()
		case <-time.After(backoff/2 + jitter):
		}
	}
}

// dialAndServe runs one connection lifetime: dial, authenticate, then serve
// proxied requests over the yamux session until it drops.
func (c *Client) dialAndServe(ctx context.Context) error {
	connectHost, _, _ := net.SplitHostPort(c.cfg.Identity.ConnectAddr)
	serverName := c.cfg.ServerName
	if serverName == "" {
		serverName = connectHost
	}
	// Only ever skip verification when connecting to a loopback dev server.
	// This makes the env/flag override inert against a real remote endpoint, so
	// the connect secret can't be MITM'd in a shipped build.
	insecure := c.cfg.InsecureSkipVerify && isLoopbackHost(connectHost)
	tlsCfg := &tls.Config{
		ServerName:         serverName,
		NextProtos:         []string{wire.ALPN},
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure, //nolint:gosec // only true for loopback dev servers
	}

	dialer := &tls.Dialer{Config: tlsCfg}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	conn, err := dialer.DialContext(dctx, "tcp", c.cfg.Identity.ConnectAddr)
	cancel()
	if err != nil {
		return fmt.Errorf("dial tunnel server: %w", err)
	}
	defer conn.Close()

	// Authenticate. Bound the handshake with a deadline so a dead server
	// can't wedge us.
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := wire.WriteFrame(conn, wire.AuthRequest{
		V:             wire.Version,
		TunnelID:      c.cfg.Identity.TunnelID,
		ConnectSecret: c.cfg.Identity.ConnectSecret,
	}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}
	var resp wire.AuthResponse
	if err := wire.ReadFrame(conn, &resp); err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("tunnel server rejected connection: %s", resp.Error)
	}
	_ = conn.SetDeadline(time.Time{}) // session is long-lived; clear deadline

	sess, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer sess.Close()

	c.setState(StateOnline, nil)

	// Serve every inbound stream as an HTTP request proxied to the local
	// MCP server. http.Server over a yamux session: Accept yields one
	// stream per public request.
	srv := &http.Server{Handler: c.proxyHandler()}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.Serve(sess) // returns when the session dies or ctx closes
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("tunnel session closed: %w", err)
}

// proxyHandler reverse-proxies public requests to the local MCP server. It
// rewrites Host to the local server's expected value (defeating its
// DNS-rebinding guard, which the localhost-only MCP server applies to the
// Host/Origin) and strips Origin entirely, and disables response buffering
// so MCP streaming responses flush immediately.
func (c *Client) proxyHandler() http.Handler {
	target := &url.URL{Scheme: "http", Host: c.cfg.LocalAddr}
	rp := &httputil.ReverseProxy{
		FlushInterval: -1, // never buffer; stream SSE/chunked bodies through
		Director: func(r *http.Request) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			// The local MCP server only trusts a localhost Host/Origin. Go
			// sends r.Host as the Host header, so setting it is enough; an
			// explicit "Host" header entry would be ignored anyway.
			r.Host = target.Host
			r.Header.Del("Origin")
			// Forwarded-* are set by the tunnel server; leave them.
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// The public edge maps tunnel-down to a friendly 502; this path
			// is "tunnel up but local MCP unreachable".
			http.Error(w, "local MCP server is not reachable", http.StatusBadGateway)
		},
	}
	return rp
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.LogOutput = logSink{}
	return cfg
}

// logSink discards yamux's internal logging; the embedding app reports state
// through OnStatus instead.
type logSink struct{}

func (logSink) Write(p []byte) (int, error) { return len(p), nil }

// isLoopbackHost reports whether host is a loopback name/address, used to gate
// TLS-verification skipping to local dev servers only.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
