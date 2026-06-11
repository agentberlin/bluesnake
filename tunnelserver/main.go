// Command tunnelserver is the bluesnake reverse-tunnel server. It gives each
// running local MCP server a stable public HTTPS URL by accepting an outbound,
// multiplexed connection from the embedded client and reverse-proxying public
// requests back down it.
//
// It is intentionally self-contained under tunnelserver/ (its own internal
// packages) so it can be lifted into a private repository later; the only
// shared code with the open-source app is the wire protocol package.
//
// Configuration is entirely via environment variables — see docs/TUNNEL.md.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/tunnelserver/internal/server"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := loadConfig()
	if cfg.pepper != "" {
		store.SetPepper([]byte(cfg.pepper))
	} else {
		log.Warn("no SECRET_PEPPER set — credential hashes are unpeppered (dev only)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := buildStore(ctx, cfg, log)
	if err != nil {
		log.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	tlsConfig, err := buildTLS(ctx, cfg, log)
	if err != nil {
		log.Error("TLS init failed", "err", err)
		os.Exit(1)
	}

	srv, err := server.New(server.Config{
		Store:             st,
		BaseDomain:        cfg.baseDomain,
		APIHost:           cfg.apiHost,
		ConnectAddr:       cfg.connectAddr,
		TLSConfig:         tlsConfig,
		TrustProxyHeader:  cfg.trustProxy,
		TrustedProxyCIDRs: cfg.trustedProxies,
		Log:               log,
	})
	if err != nil {
		log.Error("server init failed", "err", err)
		os.Exit(1)
	}

	log.Info("starting tunnel server",
		"listen", cfg.listenAddr, "base_domain", cfg.baseDomain,
		"api_host", cfg.apiHost, "connect_addr", cfg.connectAddr,
		"store", cfg.storeKind(), "tls", cfg.tlsKind())

	if err := srv.ListenAndServe(ctx, cfg.listenAddr); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
	log.Info("tunnel server stopped")
}

type config struct {
	listenAddr     string
	baseDomain     string
	apiHost        string
	connectAddr    string
	trustProxy     bool
	trustedProxies []string

	dbDSN  string // empty → in-memory store (dev)
	pepper string // SECRET_PEPPER: mixed into stored credential hashes

	// TLS
	devTLS     bool
	acmeEmail  string
	acmeCA     string // optional staging override
	cfAPIToken string
	certDir    string
}

func (c config) storeKind() string {
	if c.dbDSN == "" {
		return "memory"
	}
	return "postgres"
}

func (c config) tlsKind() string {
	if c.devTLS {
		return "dev-self-signed"
	}
	return "certmagic-acme"
}

func loadConfig() config {
	c := config{
		// Contract env vars shared with the infra/ops deploy (see docs/TUNNEL.md).
		baseDomain: env("BASE_DOMAIN", "t.snake.blue"),
		apiHost:    env("API_DOMAIN", "api.snake.blue"),
		dbDSN:      os.Getenv("DATABASE_URL"),
		acmeEmail:  os.Getenv("ACME_EMAIL"),
		cfAPIToken: os.Getenv("CF_API_TOKEN"),
		certDir:    env("CERT_DIR", ""),
		pepper:     os.Getenv("SECRET_PEPPER"),

		// Operational knobs not in the infra contract (our defaults; override
		// only if needed).
		listenAddr:     env("BLUESNAKE_TUNNEL_LISTEN", ":443"),
		connectAddr:    env("BLUESNAKE_TUNNEL_CONNECT_ADDR", ""),
		trustProxy:     envBool("BLUESNAKE_TUNNEL_TRUST_PROXY", false),
		trustedProxies: envList("BLUESNAKE_TUNNEL_TRUSTED_PROXIES"),
		devTLS:         envBool("BLUESNAKE_TUNNEL_DEV_TLS", false),
		acmeCA:         os.Getenv("BLUESNAKE_TUNNEL_ACME_CA"),
	}
	if c.connectAddr == "" {
		// Default: clients dial connect.<baseDomain>:443.
		c.connectAddr = "connect." + c.baseDomain + ":443"
	}
	return c
}

func buildStore(ctx context.Context, cfg config, log *slog.Logger) (store.Store, error) {
	if cfg.dbDSN == "" {
		log.Warn("no BLUESNAKE_TUNNEL_DB_DSN set — using in-memory store (state is lost on restart; dev only)")
		return store.NewMem(), nil
	}
	return store.NewGorm(ctx, cfg.dbDSN)
}

func buildTLS(ctx context.Context, cfg config, log *slog.Logger) (*tls.Config, error) {
	if cfg.devTLS {
		log.Warn("BLUESNAKE_TUNNEL_DEV_TLS=1 — serving a self-signed certificate (dev only)")
		return server.DevTLSConfig("*."+cfg.baseDomain, cfg.baseDomain, cfg.apiHost)
	}

	if cfg.cfAPIToken == "" {
		return nil, fmt.Errorf("CF_API_TOKEN is required for ACME DNS-01 (or set BLUESNAKE_TUNNEL_DEV_TLS=1 for dev)")
	}
	if cfg.acmeEmail == "" {
		return nil, fmt.Errorf("ACME_EMAIL is required for ACME registration")
	}

	if cfg.certDir != "" {
		certmagic.Default.Storage = &certmagic.FileStorage{Path: cfg.certDir}
	}
	magic := certmagic.NewDefault()
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:     acmeCA(cfg.acmeCA),
		Email:  cfg.acmeEmail,
		Agreed: true,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &cloudflare.Provider{APIToken: cfg.cfAPIToken},
			},
		},
	})
	magic.Issuers = []certmagic.Issuer{issuer}

	// One wildcard for the whole tunnel zone (covers every <id>.<base> and the
	// connect host) plus the api host.
	names := []string{"*." + cfg.baseDomain, cfg.apiHost}
	mctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := magic.ManageSync(mctx, names); err != nil {
		return nil, fmt.Errorf("obtaining certificates for %s: %w", strings.Join(names, ", "), err)
	}

	tlsConfig := magic.TLSConfig()
	server.ApplyALPN(tlsConfig)
	return tlsConfig, nil
}

func acmeCA(override string) string {
	switch strings.ToLower(override) {
	case "", "production", "prod":
		return certmagic.LetsEncryptProductionCA
	case "staging", "test":
		return certmagic.LetsEncryptStagingCA
	default:
		return override // a full directory URL
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envList(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
