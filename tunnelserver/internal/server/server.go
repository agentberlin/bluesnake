// Package server assembles the tunnel server: one TLS listener on :443 that
// carries both public HTTPS traffic and outbound tunnel connections,
// distinguished by ALPN.
//
//   - A tunnel client negotiates ALPN "bluesnake-tunnel/1"; that connection is
//     handed to the gateway as a raw conn (handshake + yamux).
//   - Everything else is ordinary HTTPS, routed by Host: the control-plane API
//     at the api host, and the data-plane proxy for *.<baseDomain>.
//
// TLS certificates (a wildcard for the tunnel zone plus the api host) are
// obtained and renewed in-process by certmagic via the ACME DNS-01 challenge,
// so no inbound port 80 and no per-tunnel certificate work is required.
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/netutil"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/api"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/gateway"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/registry"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

// Config is the assembled server configuration.
type Config struct {
	Store store.Store

	// BaseDomain is the zone tunnels live under, e.g. "t.snake.blue".
	BaseDomain string
	// APIHost is the control-plane hostname, e.g. "api.snake.blue".
	APIHost string
	// ConnectAddr is what clients dial for the tunnel, e.g.
	// "connect.t.snake.blue:443" — returned to clients at registration.
	ConnectAddr string

	// TLSConfig terminates TLS for the public listener. It MUST advertise the
	// tunnel ALPN and "http/1.1" in NextProtos. In production this comes from
	// certmagic; in dev it's a self-signed config.
	TLSConfig *tls.Config

	TrustProxyHeader  bool
	TrustedProxyCIDRs []string // proxy ranges allowed to set CF-Connecting-IP; empty → Cloudflare

	// Rate-limit overrides (per minute / burst); zero → package defaults.
	RegisterPerIPPerMin, RegisterPerIPBurst   float64
	RegisterGlobalPerMin, RegisterGlobalBurst float64

	Log *slog.Logger
}

// Server is a constructed, runnable tunnel server.
type Server struct {
	cfg     Config
	log     *slog.Logger
	gw      *gateway.Gateway
	httpSrv *http.Server
	root    http.Handler
}

// New assembles a Server from cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("server: Store is required")
	}
	if cfg.BaseDomain == "" || cfg.APIHost == "" {
		return nil, fmt.Errorf("server: BaseDomain and APIHost are required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	reg := registry.New()
	gw := gateway.New(reg, cfg.Store, cfg.BaseDomain, log)
	apiSrv := api.New(api.Config{
		Store:             cfg.Store,
		BaseDomain:        cfg.BaseDomain,
		ConnectAddr:       cfg.ConnectAddr,
		TrustProxyHeader:  cfg.TrustProxyHeader,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		PerIPPerMin:       cfg.RegisterPerIPPerMin,
		PerIPBurst:        cfg.RegisterPerIPBurst,
		GlobalPerMin:      cfg.RegisterGlobalPerMin,
		GlobalBurst:       cfg.RegisterGlobalBurst,
		Log:               log,
	})

	root := routeByHost(cfg.APIHost, cfg.BaseDomain, apiSrv.Handler(), gw.PublicHandler())

	s := &Server{cfg: cfg, log: log, gw: gw, root: root}
	s.httpSrv = &http.Server{
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,  // slowloris guard; no WriteTimeout (SSE)
		IdleTimeout:       120 * time.Second, // reap idle keep-alive conns (does not affect in-flight SSE)
		// Hand tunnel-ALPN connections to the gateway instead of treating them
		// as HTTP. This is how the same :443 listener serves both.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){
			wire.ALPN: func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
				s.gw.HandleConn(conn)
			},
		},
	}
	return s, nil
}

// routeByHost dispatches HTTPS requests: the api host to the control plane,
// anything under the tunnel zone to the data-plane proxy.
func routeByHost(apiHost, baseDomain string, apiHandler, publicHandler http.Handler) http.Handler {
	apiHost = strings.ToLower(apiHost)
	zoneSuffix := "." + strings.ToLower(strings.Trim(baseDomain, "."))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(host, ".") // tolerate fully-qualified trailing dot
		switch {
		case host == apiHost:
			apiHandler.ServeHTTP(w, r)
		case strings.HasSuffix(host, zoneSuffix):
			publicHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// Serve runs the server on the given TLS listener until ctx is cancelled. The
// listener must already apply s.cfg.TLSConfig (use ServeTLSListener for the
// common path).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	err := s.httpSrv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// ListenAndServe binds addr, wraps it in the configured TLS, and serves.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if s.cfg.TLSConfig == nil {
		return fmt.Errorf("server: TLSConfig is required to serve")
	}
	rawLn, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	// Cap total simultaneous connections so the box can't be exhausted by a
	// flood of half-open/idle sockets.
	limited := netutil.LimitListener(rawLn, maxConnections)
	s.log.Info("tunnel server listening", "addr", addr, "api_host", s.cfg.APIHost, "base_domain", s.cfg.BaseDomain, "max_conns", maxConnections)
	return s.Serve(ctx, tls.NewListener(limited, s.cfg.TLSConfig))
}

// maxConnections bounds total accepted connections on the public listener.
const maxConnections = 8192

// Handler exposes the host-routing handler (tests).
func (s *Server) Handler() http.Handler { return s.root }
