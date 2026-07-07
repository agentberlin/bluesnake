package gateway

// The offline data path. A tunnel whose app is closed used to answer every
// public request with a bare 502, which MCP clients read as "connector
// broken" — forcing users through a pointless reconnect. Instead, requests
// for a *registered* tunnel with no live session are answered by the
// mcpstub package: the MCP lifecycle succeeds and tool calls return
// isError results telling the model the app is not running.
//
// This is the one place the public path consults the database — the "is this
// a registered tunnel?" verdict has to come from somewhere when no session
// exists. Three guards keep that cheap and unabusable: labels that cannot be
// tunnel ids are rejected without a lookup, verdicts (and snapshots) are
// cached with a TTL, and cache misses are per-IP rate-limited. On a store
// outage or when rate-limited, the path degrades to the old 502.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/tunnelserver/internal/mcpstub"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/ratelimit"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/registry"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

const (
	// offlineCacheTTL bounds the staleness of a cached verdict: after a
	// revoke (or a fresh registration), the offline path converges within
	// one TTL.
	offlineCacheTTL = 60 * time.Second
	// offlineCacheMax caps cache entries so spraying well-formed random
	// subdomains cannot grow memory without bound.
	offlineCacheMax = 16384
	// offlinePerIPPerMin / offlinePerIPBurst throttle store lookups (cache
	// misses only) per source IP.
	offlinePerIPPerMin = 60
	offlinePerIPBurst  = 30
	// probeTimeout bounds the connect-time snapshot probe.
	probeTimeout = 15 * time.Second
)

// gateEntry is one cached offline verdict for a subdomain label.
type gateEntry struct {
	allowed bool // registered && !revoked → serve the stub
	snap    *mcpstub.Snapshot
	expires time.Time
}

// offlineGate caches offline verdicts keyed by tunnel id.
type offlineGate struct {
	mu      sync.Mutex
	entries map[string]gateEntry
	now     func() time.Time
}

func newOfflineGate(now func() time.Time) *offlineGate {
	return &offlineGate{entries: map[string]gateEntry{}, now: now}
}

func (o *offlineGate) get(id string) (gateEntry, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.entries[id]
	if !ok || o.now().After(e.expires) {
		return gateEntry{}, false
	}
	return e, true
}

func (o *offlineGate) put(id string, e gateEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e.expires = o.now().Add(offlineCacheTTL)
	if _, exists := o.entries[id]; !exists && len(o.entries) >= offlineCacheMax {
		o.evictLocked()
	}
	o.entries[id] = e
}

// evictLocked frees one slot: preferably an expired entry, otherwise an
// arbitrary one (cache misses just cost one extra store lookup).
func (o *offlineGate) evictLocked() {
	now := o.now()
	for k, e := range o.entries {
		if now.After(e.expires) {
			delete(o.entries, k)
			return
		}
	}
	for k := range o.entries {
		delete(o.entries, k)
		return
	}
}

// serveOffline handles a public request whose subdomain has no live session.
func (g *Gateway) serveOffline(w http.ResponseWriter, r *http.Request, label string) {
	// The stub impersonates /mcp only — the sole path the tunnel exposes.
	// Anything else keeps the old fixed 502, as do labels that cannot be
	// tunnel ids (no store lookup for those).
	if r.URL.Path != "/mcp" || !store.ValidID(label) {
		writeOffline(w)
		return
	}

	entry, ok := g.offline.get(label)
	if !ok {
		if !g.offlineLimiter.Allow(ratelimit.IPKey(requestIP(r))) {
			g.log.Debug("offline lookup rate-limited", "label", label)
			writeOffline(w)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		tn, err := g.st.GetByID(ctx, label)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && tn == nil):
			entry = gateEntry{allowed: false}
		case err != nil:
			// Store outage: degrade to the old behavior, and don't cache a
			// verdict we never got.
			g.log.Error("offline lookup failed", "label", label, "err", err)
			writeOffline(w)
			return
		default:
			entry = gateEntry{allowed: !tn.Revoked, snap: mcpstub.Decode(tn.MCPSnapshot)}
		}
		g.offline.put(label, entry)
	}

	if !entry.allowed {
		writeOffline(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	mcpstub.Serve(w, r, entry.snap)
}

// snapshotSession probes a freshly connected tunnel for its initialize and
// tools/list results, primes the offline cache, and persists the snapshot so
// it survives tunnel-server restarts. Best-effort: on failure the previous
// snapshot (cache or DB) keeps serving.
func (g *Gateway) snapshotSession(sess *registry.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	raw, err := mcpstub.Probe(ctx, sess.Open, sess.Host)
	if err != nil {
		g.log.Warn("mcp snapshot probe failed", "tunnel_id", sess.TunnelID, "err", err)
		return
	}
	g.offline.put(sess.TunnelID, gateEntry{allowed: true, snap: mcpstub.Decode(raw)})

	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if err := g.st.SaveMCPSnapshot(sctx, sess.TunnelID, raw); err != nil {
		g.log.Warn("mcp snapshot persist failed", "tunnel_id", sess.TunnelID, "err", err)
	}
}

// requestIP extracts the source IP of a public request. The data plane is
// never behind a proxy (tunnel subdomains are DNS-only), so RemoteAddr is
// authoritative.
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
