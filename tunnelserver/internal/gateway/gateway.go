// Package gateway is the tunnel data plane. It has two responsibilities:
//
//   - HandleConn authenticates an inbound tunnel connection (one auth frame,
//     constant-time secret check against the store) and, on success, turns it
//     into a registered yamux session.
//   - PublicHandler routes an inbound public HTTPS request to the right
//     session by subdomain, enforces the URL access token, and reverse-proxies
//     it down the tunnel with response buffering disabled (so MCP streaming
//     works).
//
// The public path never reads the database: everything it needs is in the
// in-memory registry, populated at connect time.
package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/ratelimit"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/registry"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
	"github.com/hashicorp/yamux"
)

const (
	// handshakeTimeout bounds how long an unauthenticated peer may hold a
	// connection before it must complete the auth exchange.
	handshakeTimeout = 15 * time.Second
	// maxRequestBody caps a public request body, matching the local MCP
	// server's own 8MB limit.
	maxRequestBody = 8 << 20
	// connectPerIPPerMin / connectPerIPBurst throttle inbound tunnel-connect
	// attempts per source IP (each attempt costs a goroutine + a DB lookup).
	connectPerIPPerMin = 30
	connectPerIPBurst  = 30
	// maxConcurrentHandshakes caps unauthenticated connections being processed
	// at once, independent of how many live sessions exist.
	maxConcurrentHandshakes = 128
)

// Gateway ties the registry and store to the data-plane behavior.
type Gateway struct {
	reg          *registry.Registry
	st           store.Store
	baseDomain   string // e.g. "t.snake.blue"; sessions bind <id>.<baseDomain>
	log          *slog.Logger
	now          func() time.Time
	connLimiter  *ratelimit.Limiter
	handshakeSem chan struct{}
}

// New constructs a Gateway. baseDomain is the zone tunnels live under.
func New(reg *registry.Registry, st store.Store, baseDomain string, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	return &Gateway{
		reg:          reg,
		st:           st,
		baseDomain:   strings.ToLower(strings.Trim(baseDomain, ".")),
		log:          log,
		now:          time.Now,
		connLimiter:  ratelimit.New(float64(connectPerIPPerMin)/60, connectPerIPBurst),
		handshakeSem: make(chan struct{}, maxConcurrentHandshakes),
	}
}

// HandleConn runs one tunnel connection's lifetime: authenticate, register,
// then block until the session drops and deregister. It always closes conn.
func (g *Gateway) HandleConn(conn net.Conn) {
	defer conn.Close()

	// Throttle per source IP (IPv6 bucketed by /64) before spending a
	// goroutine-second and a DB query on an unauthenticated peer.
	ip := remoteIP(conn)
	if !g.connLimiter.Allow(ratelimit.IPKey(ip)) {
		g.log.Debug("tunnel connect rate-limited", "remote", ip)
		return
	}
	// Bound concurrent in-flight handshakes (released once this one resolves,
	// so live sessions don't hold a slot).
	select {
	case g.handshakeSem <- struct{}{}:
	default:
		g.log.Warn("tunnel connect rejected: too many concurrent handshakes", "remote", ip)
		return
	}
	hsReleased := false
	releaseHS := func() {
		if !hsReleased {
			hsReleased = true
			<-g.handshakeSem
		}
	}
	defer releaseHS()

	_ = conn.SetDeadline(g.now().Add(handshakeTimeout))
	var req wire.AuthRequest
	if err := wire.ReadFrame(conn, &req); err != nil {
		g.log.Debug("tunnel auth: read frame failed", "err", err, "remote", conn.RemoteAddr())
		return
	}

	tn, err := g.authenticate(req)
	if err != nil {
		_ = wire.WriteFrame(conn, wire.AuthResponse{OK: false, Error: err.Error()})
		if errors.Is(err, errUnavailable) {
			// A store outage must not read as a credential rejection: clients
			// (and users) could react by discarding tunnel.json, permanently
			// losing their subdomain.
			g.log.Error("tunnel auth: store unavailable", "tunnel_id", req.TunnelID, "remote", conn.RemoteAddr())
		} else {
			g.log.Info("tunnel auth rejected", "tunnel_id", req.TunnelID, "remote", conn.RemoteAddr())
		}
		return
	}

	host := tn.ID + "." + g.baseDomain
	if err := wire.WriteFrame(conn, wire.AuthResponse{OK: true, Host: host}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{}) // long-lived session: clear deadline

	ysess, err := yamux.Client(conn, serverYamuxConfig())
	if err != nil {
		g.log.Warn("tunnel session start failed", "tunnel_id", tn.ID, "err", err)
		return
	}
	defer ysess.Close()

	sess := registry.NewSession(tn.ID, host, ysess, g.now())
	sess.Handler = g.proxyFor(sess)
	g.reg.Add(sess)
	defer g.reg.Remove(sess)

	// Handshake is done; free the slot so the session's (long) lifetime doesn't
	// count against the concurrent-handshake cap.
	releaseHS()

	// Best-effort connect telemetry; never blocks the data path.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.st.MarkConnected(ctx, tn.ID)
	}()

	g.log.Info("tunnel connected", "tunnel_id", tn.ID, "host", host)
	<-ysess.CloseChan()
	g.log.Info("tunnel disconnected", "tunnel_id", tn.ID, "host", host)
}

// remoteIP extracts the source IP from a connection's remote address.
func remoteIP(conn net.Conn) string {
	if conn.RemoteAddr() == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	return host
}

// Auth failure classes, sent verbatim in the AuthResponse Error field.
// errUnauthorized is definitive (bad id/secret/version, or revoked);
// errUnavailable means the server couldn't check — the client should keep its
// credentials and retry.
var (
	errUnauthorized = errors.New("unauthorized")
	errUnavailable  = errors.New("temporarily unavailable, retry later")
)

// authenticate validates an auth frame in constant time with respect to
// whether the tunnel id exists, so timing can't be used to enumerate ids.
func (g *Gateway) authenticate(req wire.AuthRequest) (*store.Tunnel, error) {
	if req.V != wire.Version {
		return nil, errUnauthorized
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tn, err := g.st.GetByID(ctx, req.TunnelID)
	provided := store.Hash(req.ConnectSecret)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && tn == nil):
		// Compare against a fixed dummy so the unknown-id path costs the
		// same as the wrong-secret path.
		subtle.ConstantTimeCompare(provided, dummyHash)
		return nil, errUnauthorized
	case err != nil:
		// Store outage, not a verdict on the credentials.
		return nil, errUnavailable
	}
	if subtle.ConstantTimeCompare(provided, tn.ConnectSecretHash) != 1 {
		return nil, errUnauthorized
	}
	// Checked after the compare so revoked and wrong-secret are
	// indistinguishable from outside, in timing and in response.
	if tn.Revoked {
		return nil, errUnauthorized
	}
	return tn, nil
}

var dummyHash = make([]byte, 32)

// PublicHandler serves inbound public HTTPS requests for *.baseDomain.
func (g *Gateway) PublicHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label, ok := g.subdomainLabel(r.Host)
		if !ok {
			http.NotFound(w, r)
			return
		}
		sess := g.reg.Get(label)
		if sess == nil {
			// No live tunnel for this subdomain (offline, or never existed —
			// indistinguishable on purpose). Friendly, fixed message.
			writeOffline(w)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		sess.Handler.ServeHTTP(w, r)
	})
}

// proxyFor builds the reverse proxy that streams a public request down a
// specific session's yamux connection.
func (g *Gateway) proxyFor(sess *registry.Session) http.Handler {
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return sess.Open()
		},
		// One logical upstream per session; modest pooling of streams.
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	target := &url.URL{Scheme: "http", Host: sess.Host}
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1, // stream responses (SSE/chunked) without buffering
		Director: func(r *http.Request) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			// Strip ALL client-supplied forwarding headers so they can't be
			// spoofed. X-Forwarded-For in particular must be deleted or
			// ReverseProxy appends the real IP to the attacker's value, letting
			// a public caller forge the client IP seen downstream.
			r.Header.Del("X-Forwarded-For")
			r.Header.Del("X-Real-IP")
			r.Header.Del("X-Forwarded-Host")
			r.Header.Del("X-Forwarded-Proto")
			r.Header.Del("Forwarded")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Host", sess.Host)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			g.log.Debug("proxy error", "tunnel_id", sess.TunnelID, "err", err)
			writeOffline(w)
		},
	}
}

// subdomainLabel extracts the single tunnel-id label from a Host header,
// requiring exactly "<label>.<baseDomain>".
func (g *Gateway) subdomainLabel(host string) (string, bool) {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".") // tolerate fully-qualified trailing dot
	suffix := "." + g.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	label := host[:len(host)-len(suffix)]
	if label == "" || strings.Contains(label, ".") {
		return "", false // multi-level or empty labels are not tunnels
	}
	return label, true
}

func writeOffline(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte("bluesnake tunnel is not connected — start the local MCP server and enable its public URL.\n"))
}

func serverYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 20 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.LogOutput = discard{}
	return cfg
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
