// Package registry tracks the live tunnel sessions keyed by subdomain. The
// public request path touches only this in-memory map — never the database —
// so the data plane stays fast and independent of control-plane availability.
package registry

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// maxStreamsPerSession caps concurrent in-flight public requests proxied down a
// single tunnel, bounding the memory one client (or one attacker hammering a
// leaked URL) can make the server hold open. Matches yamux's default accept
// backlog.
const maxStreamsPerSession = 256

// ErrTooManyStreams is returned by Open when a session is at its concurrent
// stream cap; the gateway turns it into a 502.
var ErrTooManyStreams = errors.New("tunnel session at concurrent-request limit")

// Session is one live tunnel: the multiplexed connection back to a client and
// the HTTP handler that proxies public requests down it.
type Session struct {
	TunnelID string
	Host     string
	Created  time.Time

	// Handler proxies a public request over the yamux session. It is set by
	// the gateway after construction (the registry stays agnostic of HTTP
	// proxying details).
	Handler http.Handler

	ysess     *yamux.Session
	streamSem chan struct{} // bounds concurrent in-flight streams
}

// NewSession builds a session for a freshly authenticated tunnel.
func NewSession(tunnelID, host string, ysess *yamux.Session, now time.Time) *Session {
	return &Session{
		TunnelID:  tunnelID,
		Host:      host,
		Created:   now,
		ysess:     ysess,
		streamSem: make(chan struct{}, maxStreamsPerSession),
	}
}

// Open opens a new multiplexed stream to the client (one per public request),
// subject to the per-session concurrency cap. The returned conn releases its
// slot when closed.
func (s *Session) Open() (net.Conn, error) {
	select {
	case s.streamSem <- struct{}{}:
	default:
		return nil, ErrTooManyStreams
	}
	conn, err := s.ysess.Open()
	if err != nil {
		<-s.streamSem
		return nil, err
	}
	return &countedConn{Conn: conn, release: func() { <-s.streamSem }}, nil
}

// countedConn releases a session stream slot exactly once, when closed.
type countedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *countedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// Close tears down the underlying connection.
func (s *Session) Close() error { return s.ysess.Close() }

// CloseChan fires when the underlying session dies (client gone, network
// drop). The gateway blocks on it to deregister.
func (s *Session) CloseChan() <-chan struct{} { return s.ysess.CloseChan() }

// Registry is the set of live sessions, keyed by tunnel id (== subdomain
// label).
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{sessions: map[string]*Session{}}
}

// Add registers a session, evicting any prior session for the same tunnel id
// (latest-connection-wins, so a reconnect after a network switch reclaims the
// subdomain and the stale connection is closed). The evicted session, if any,
// is returned so the caller can finish closing it.
func (r *Registry) Add(s *Session) (evicted *Session) {
	r.mu.Lock()
	old := r.sessions[s.TunnelID]
	r.sessions[s.TunnelID] = s
	r.mu.Unlock()
	if old != nil && old != s {
		_ = old.Close()
	}
	return old
}

// Remove deregisters a session, but only if it is still the current one for
// its id (a newer reconnect must not be clobbered by an older session's
// cleanup).
func (r *Registry) Remove(s *Session) {
	r.mu.Lock()
	if r.sessions[s.TunnelID] == s {
		delete(r.sessions, s.TunnelID)
	}
	r.mu.Unlock()
}

// Get returns the live session for a tunnel id, or nil if none is connected.
func (r *Registry) Get(tunnelID string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[tunnelID]
}

// Count reports the number of live sessions (metrics/tests).
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}
