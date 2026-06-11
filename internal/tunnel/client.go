package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
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

	// LogDir is the directory for the tunnel lifecycle and access logs
	// (tunnel.log + mcp-access.log, rotated). Empty disables file logging.
	LogDir string

	// Backoff bounds; zero means defaults.
	MinBackoff, MaxBackoff time.Duration
}

// Client maintains one outbound multiplexed tunnel and reconnects forever
// until its context is cancelled.
type Client struct {
	cfg Config
	lg  *Loggers

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
	return &Client{cfg: cfg, lg: openLoggers(cfg.LogDir), status: Status{
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
	defer c.lg.Close()
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
			c.lg.Tunnel().Info("stopped", "reason", "context-cancelled")
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
		wait := backoff/2 + jitter
		c.lg.Tunnel().Warn("reconnecting",
			"attempt", attempt,
			"backoff_ms", wait.Milliseconds(),
			"cause", errString(err))
		select {
		case <-ctx.Done():
			c.setState(StateStopped, nil)
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// errString renders an error for a log attribute, tolerating nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

	tlog := c.lg.Tunnel()
	tlog.Info("dialing",
		"connect_addr", c.cfg.Identity.ConnectAddr,
		"server_name", serverName,
		"tunnel_id", c.cfg.Identity.TunnelID)

	dialer := &tls.Dialer{Config: tlsCfg}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	conn, err := dialer.DialContext(dctx, "tcp", c.cfg.Identity.ConnectAddr)
	cancel()
	if err != nil {
		tlog.Error("dial failed", "connect_addr", c.cfg.Identity.ConnectAddr, "err", err.Error())
		return fmt.Errorf("dial tunnel server: %w", err)
	}
	defer conn.Close()

	// Record the negotiated ALPN so a protocol mismatch (e.g. landing on the
	// HTTPS path instead of the gateway connect path) is visible.
	var alpn string
	if tc, ok := conn.(*tls.Conn); ok {
		alpn = tc.ConnectionState().NegotiatedProtocol
	}
	tlog.Info("connected", "alpn", alpn)

	// Authenticate. Bound the handshake with a deadline so a dead server
	// can't wedge us.
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := wire.WriteFrame(conn, wire.AuthRequest{
		V:             wire.Version,
		TunnelID:      c.cfg.Identity.TunnelID,
		ConnectSecret: c.cfg.Identity.ConnectSecret,
	}); err != nil {
		tlog.Error("auth send failed", "err", err.Error())
		return fmt.Errorf("send auth: %w", err)
	}
	var resp wire.AuthResponse
	if err := wire.ReadFrame(conn, &resp); err != nil {
		tlog.Error("auth read failed", "err", err.Error())
		return fmt.Errorf("read auth response: %w", err)
	}
	if !resp.OK {
		// resp.Error is the server's reason; the connect secret is never logged.
		tlog.Warn("auth rejected", "reason", resp.Error)
		return fmt.Errorf("tunnel server rejected connection: %s", resp.Error)
	}
	tlog.Info("authenticated", "host", resp.Host)
	_ = conn.SetDeadline(time.Time{}) // session is long-lived; clear deadline

	sess, err := yamux.Server(conn, yamuxConfig(tlog))
	if err != nil {
		tlog.Error("session start failed", "err", err.Error())
		return fmt.Errorf("start session: %w", err)
	}
	defer sess.Close()

	c.setState(StateOnline, nil)
	tlog.Info("online", "public_host", c.cfg.Identity.PublicHost)

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
	tlog.Warn("disconnected", "cause", errString(err))
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
	// The access log wraps the proxy so every forwarded request — including
	// the unmatched-path 404s and any that hang — is recorded start to finish.
	return c.lg.accessHandler(rp)
}

func yamuxConfig(log *slog.Logger) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	// Route yamux's internal logging (keepalive/heartbeat failures, stream
	// errors) into the tunnel lifecycle log rather than discarding it.
	cfg.LogOutput = yamuxLogWriter{log: log}
	return cfg
}

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
