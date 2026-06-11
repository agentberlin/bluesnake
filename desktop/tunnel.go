package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The desktop reverse tunnel: an opt-in public HTTPS URL for the local MCP
// server. It forwards to whatever address the MCP manager is bound to, so the
// public URL exposes exactly the same surface as the localhost endpoint. The
// tunnel requires the MCP server to be running; toggling MCP off (or changing
// its port) restarts or stops the tunnel to match.

// TunnelStatus is what the frontend renders in the MCP panel's public-URL
// section.
type TunnelStatus struct {
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"` // disabled | connecting | online | error | stopped
	PublicURL string `json:"publicUrl"`
	MCPURL    string `json:"mcpUrl"`
	Error     string `json:"error,omitempty"`
}

type tunnelManager struct {
	app *App

	mu       sync.Mutex
	enabled  bool
	client   *tunnel.Client
	cancel   context.CancelFunc
	identity *tunnel.Identity
	err      string
}

func newTunnelManager(a *App) *tunnelManager { return &tunnelManager{app: a} }

// initFromSettings restores the persisted toggle on launch. It must run after
// the MCP manager's init so the local address is known.
func (m *tunnelManager) initFromSettings() {
	s := m.app.loadSettings()
	m.mu.Lock()
	m.enabled = s.Tunnel.Enabled
	if m.enabled {
		m.startLocked()
	}
	st := m.statusLocked()
	m.mu.Unlock()
	m.emit(st)
}

// startLocked brings the tunnel client up, pointed at the live MCP listener.
// Failures land in m.err (surfaced in the UI) rather than aborting.
func (m *tunnelManager) startLocked() {
	m.err = ""
	localAddr := m.app.mcp.localAddr()
	if localAddr == "" {
		m.err = "enable the MCP server first"
		return
	}
	regCtx, cancelReg := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReg()
	id, err := tunnel.EnsureIdentity(regCtx, m.app.storeDir)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.identity = id

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.client = tunnel.New(tunnel.Config{
		Identity:           id,
		LocalAddr:          localAddr,
		LogDir:             filepath.Join(m.app.storeDir, "logs"),
		InsecureSkipVerify: os.Getenv("BLUESNAKE_TUNNEL_INSECURE") == "1",
		ServerName:         os.Getenv("BLUESNAKE_TUNNEL_SERVER_NAME"),
		OnStatus:           m.onStatus,
	})
	go func(c *tunnel.Client) { _ = c.Run(ctx) }(m.client)
}

func (m *tunnelManager) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.client = nil
}

// onStatus is invoked from the client's goroutine on each state transition.
func (m *tunnelManager) onStatus(_ tunnel.Status) {
	m.mu.Lock()
	st := m.statusLocked()
	m.mu.Unlock()
	m.emit(st)
}

func (m *tunnelManager) statusLocked() TunnelStatus {
	st := TunnelStatus{Enabled: m.enabled}
	if m.identity != nil {
		st.PublicURL = m.identity.PublicURL()
		st.MCPURL = m.identity.MCPURL()
	}
	switch {
	case !m.enabled:
		st.State = "disabled"
	case m.err != "":
		st.State = "error"
		st.Error = m.err
	case m.client != nil:
		cs := m.client.Status()
		st.State = string(cs.State)
		st.Error = cs.Err
	default:
		st.State = "stopped"
	}
	return st
}

func (m *tunnelManager) emit(st TunnelStatus) {
	if m.app.ctx != nil {
		runtime.EventsEmit(m.app.ctx, "tunnel:status", st)
	}
}

// onMCPChanged is called after the MCP server starts, stops, or moves ports so
// the tunnel can re-point or shut down.
func (m *tunnelManager) onMCPChanged() {
	m.mu.Lock()
	if !m.enabled {
		m.mu.Unlock()
		return
	}
	m.stopLocked()
	m.startLocked()
	st := m.statusLocked()
	m.mu.Unlock()
	m.emit(st)
}

func (m *tunnelManager) shutdown() {
	m.mu.Lock()
	m.stopLocked()
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Wails-bound methods

func (a *App) GetTunnelStatus() TunnelStatus {
	a.tunnel.mu.Lock()
	defer a.tunnel.mu.Unlock()
	return a.tunnel.statusLocked()
}

func (a *App) SetTunnelEnabled(enabled bool) TunnelStatus {
	m := a.tunnel
	m.mu.Lock()
	m.enabled = enabled
	m.stopLocked()
	if enabled {
		m.startLocked()
	} else {
		m.err = ""
	}
	st := m.statusLocked()
	m.mu.Unlock()

	a.persistTunnel(enabled)
	m.emit(st)
	return st
}

func (a *App) persistTunnel(enabled bool) {
	s := a.loadSettings()
	s.Tunnel.Enabled = enabled
	_ = a.saveSettings(s)
}
