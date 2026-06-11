package store

import (
	"context"
	"sync"
	"time"
)

// Mem is an in-memory Store for development and tests. It is the default when
// no database DSN is configured.
type Mem struct {
	mu      sync.Mutex
	tunnels map[string]*Tunnel
	conns   map[string]int // id -> connect count, for test assertions
	lastAt  map[string]time.Time
}

// NewMem returns an empty in-memory store.
func NewMem() *Mem {
	return &Mem{
		tunnels: map[string]*Tunnel{},
		conns:   map[string]int{},
		lastAt:  map[string]time.Time{},
	}
}

func (m *Mem) Create(_ context.Context, t *Tunnel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tunnels[t.ID]; ok {
		return ErrConflict
	}
	m.tunnels[t.ID] = cloneTunnel(t)
	return nil
}

func (m *Mem) GetByID(_ context.Context, id string) (*Tunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tunnels[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneTunnel(t), nil
}

// cloneTunnel deep-copies a tunnel (including its hash slice) so callers can't
// mutate stored state through a returned value.
func cloneTunnel(t *Tunnel) *Tunnel {
	return &Tunnel{
		ID:                t.ID,
		ConnectSecretHash: append([]byte(nil), t.ConnectSecretHash...),
		Revoked:           t.Revoked,
	}
}

func (m *Mem) MarkConnected(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tunnels[id]; !ok {
		return ErrNotFound
	}
	m.conns[id]++
	m.lastAt[id] = time.Now()
	return nil
}

func (m *Mem) Close() {}

// ConnectCount reports how many times a tunnel has connected (test helper).
func (m *Mem) ConnectCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns[id]
}
