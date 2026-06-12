// Package api is the tunnel control plane: a small HTTP API for registering a
// tunnel identity. It is deliberately account-less for phase 1; abuse is
// contained with per-IP and global rate limits (and, in production, Cloudflare
// in front).
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/agentberlin/bluesnake/tunnelserver/internal/ratelimit"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

// Config configures the API surface.
type Config struct {
	Store       store.Store
	BaseDomain  string // tunnels bind <id>.<BaseDomain>
	ConnectAddr string // host:port clients dial for the tunnel, e.g. connect.t.snake.blue:443

	// TrustProxyHeader, when true, allows deriving the client IP from
	// CF-Connecting-IP — but only for connections whose peer is in
	// TrustedProxyCIDRs (defaulting to Cloudflare's ranges). Leave false when
	// the API is exposed directly.
	TrustProxyHeader bool
	// TrustedProxyCIDRs lists the proxy source ranges allowed to set
	// CF-Connecting-IP. Empty + TrustProxyHeader true → Cloudflare's published
	// ranges.
	TrustedProxyCIDRs []string

	// Rate limits. Zero values fall back to conservative defaults.
	PerIPPerMin  float64
	PerIPBurst   float64
	GlobalPerMin float64
	GlobalBurst  float64

	Log *slog.Logger
}

// API holds the constructed handlers.
type API struct {
	cfg            Config
	log            *slog.Logger
	perIP          *ratelimit.Limiter
	global         *ratelimit.Limiter
	trustedProxies []*net.IPNet
}

// New constructs the API.
func New(cfg Config) *API {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	def := func(v, d float64) float64 {
		if v <= 0 {
			return d
		}
		return v
	}
	perIPRate := def(cfg.PerIPPerMin, 5) / 60
	globalRate := def(cfg.GlobalPerMin, 120) / 60

	var trusted []*net.IPNet
	if cfg.TrustProxyHeader {
		src := cfg.TrustedProxyCIDRs
		if len(src) == 0 {
			src = cloudflareCIDRs
		}
		trusted = parseCIDRs(src)
	}
	return &API{
		cfg:            cfg,
		log:            cfg.Log,
		perIP:          ratelimit.New(perIPRate, def(cfg.PerIPBurst, 5)),
		global:         ratelimit.New(globalRate, def(cfg.GlobalBurst, 60)),
		trustedProxies: trusted,
	}
}

// Handler returns the API mux (mount at the api host).
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/register", a.handleRegister)
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

type registerResponse struct {
	TunnelID      string `json:"tunnel_id"`
	ConnectSecret string `json:"connect_secret"`
	PublicHost    string `json:"public_host"`
	ConnectAddr   string `json:"connect_addr"`
}

func (a *API) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r) {
		return
	}

	secret, err := store.NewSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate credentials")
		return
	}

	var id string
	for attempt := 0; attempt < 6; attempt++ {
		candidate, err := store.NewID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate id")
			return
		}
		err = a.cfg.Store.Create(r.Context(), &store.Tunnel{
			ID:                candidate,
			ConnectSecretHash: store.Hash(secret),
		})
		if err == nil {
			id = candidate
			break
		}
		if !errors.Is(err, store.ErrConflict) {
			a.log.Error("register: store create failed", "err", err)
			writeError(w, http.StatusInternalServerError, "registration failed")
			return
		}
	}
	if id == "" {
		writeError(w, http.StatusInternalServerError, "could not allocate a tunnel id")
		return
	}

	a.log.Info("tunnel registered", "tunnel_id", id, "ip", a.clientIP(r))
	writeJSON(w, registerResponse{
		TunnelID:      id,
		ConnectSecret: secret,
		PublicHost:    id + "." + a.cfg.BaseDomain,
		ConnectAddr:   a.cfg.ConnectAddr,
	})
}

// allow enforces the per-IP limit first, then the global limit, writing 429 on
// rejection. Per-IP is checked first on purpose: a single abuser past its own
// limit must not keep draining the shared global bucket (which would let one IP
// 429 everyone). Only requests that clear per-IP consume a global token.
func (a *API) allow(w http.ResponseWriter, r *http.Request) bool {
	if !a.perIP.Allow(ratelimit.IPKey(a.clientIP(r))) {
		writeError(w, http.StatusTooManyRequests, "too many requests, slow down")
		return false
	}
	if !a.global.Allow("*") {
		writeError(w, http.StatusTooManyRequests, "service is busy, try again shortly")
		return false
	}
	return true
}

func (a *API) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Only trust CF-Connecting-IP when the request actually arrived from a
	// trusted proxy IP; otherwise a direct-to-origin caller could spoof it and
	// mint a fresh rate-limit bucket per request.
	if a.cfg.TrustProxyHeader && ipInNets(host, a.trustedProxies) {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}
	return host
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{msg})
}
