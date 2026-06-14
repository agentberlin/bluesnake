package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/agentberlin/bluesnake/internal/mcp"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The desktop MCP server: the same tool surface as `bluesnake mcp`, served
// over streamable HTTP from inside the app. Crawl control is adapted onto
// the App's session manager, so a crawl started by an LLM streams into the
// UI exactly like one started by hand — and the UI's pause/stop buttons work
// on it.

const defaultMCPPort = 8473

// appVersion is the single canonical version (see internal/version), reported
// in the MCP server handshake.
var appVersion = version.Version

// MCPStatus is what the frontend renders (titlebar pill + settings panel).
type MCPStatus struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Port     int    `json:"port"`
	Endpoint string `json:"endpoint"`
	Error    string `json:"error,omitempty"`
}

type mcpManager struct {
	app *App

	mu      sync.Mutex
	enabled bool
	port    int
	ln      net.Listener
	srv     *http.Server
	err     string
}

func newMCPManager(a *App) *mcpManager {
	return &mcpManager{app: a, port: defaultMCPPort}
}

// ---------------------------------------------------------------------------
// app-level settings (~/.bluesnake/desktop.json) — distinct from crawl
// profiles: the MCP server belongs to the app, not to any crawl's config.

type appSettings struct {
	MCP struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
	} `json:"mcp"`
	Tunnel struct {
		Enabled bool `json:"enabled"`
	} `json:"tunnel"`
	CLI struct {
		// Prompted records that the first-launch "install the CLI?" prompt has
		// been shown and dismissed once, so it never reappears.
		Prompted bool `json:"prompted"`
	} `json:"cli"`
	Updates struct {
		AutoCheck      bool   `json:"autoCheck"`      // check GitHub on launch (default true)
		SkippedVersion string `json:"skippedVersion"` // pill ×-dismissed for this version
		LastCheck      string `json:"lastCheck"`      // RFC3339 of the last successful network check
	} `json:"updates"`
}

func (a *App) settingsPath() string { return filepath.Join(a.storeDir, "desktop.json") }

func (a *App) loadSettings() appSettings {
	var s appSettings
	s.MCP.Port = defaultMCPPort
	s.Updates.AutoCheck = true // default on; an explicit false in the file overrides
	if data, err := os.ReadFile(a.settingsPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.MCP.Port <= 0 {
		s.MCP.Port = defaultMCPPort
	}
	return s
}

func (a *App) saveSettings(s appSettings) error {
	if err := os.MkdirAll(a.storeDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.settingsPath(), data, 0o644)
}

// ---------------------------------------------------------------------------
// lifecycle

// initFromSettings restores the persisted state on app launch (auto-start
// when the toggle was left on).
func (m *mcpManager) initFromSettings() {
	s := m.app.loadSettings()
	m.mu.Lock()
	m.port = s.MCP.Port
	m.enabled = s.MCP.Enabled
	if m.enabled {
		m.startLocked()
	}
	m.mu.Unlock()
}

// startLocked starts the HTTP listener; failures land in m.err (shown in the
// UI) rather than aborting the app.
func (m *mcpManager) startLocked() {
	m.err = ""
	srv := mcp.NewServer(&desktopBackend{app: m.app}, appVersion)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.port))
	if err != nil {
		m.err = err.Error()
		return
	}
	m.ln = ln
	m.srv = &http.Server{Handler: srv.HTTPHandler()}
	go func(hs *http.Server, ln net.Listener) {
		_ = hs.Serve(ln) // returns on Close
	}(m.srv, ln)
}

func (m *mcpManager) stopLocked() {
	if m.srv != nil {
		_ = m.srv.Close()
		m.srv, m.ln = nil, nil
	}
}

// localAddr returns the bound MCP listener address while running (for the
// tunnel to forward to), or "" when stopped.
func (m *mcpManager) localAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ln != nil {
		return m.ln.Addr().String()
	}
	return ""
}

func (m *mcpManager) statusLocked() MCPStatus {
	st := MCPStatus{Enabled: m.enabled, Running: m.ln != nil, Port: m.port, Error: m.err}
	if st.Running {
		st.Endpoint = fmt.Sprintf("http://%s/mcp", m.ln.Addr())
	} else {
		st.Endpoint = fmt.Sprintf("http://127.0.0.1:%d/mcp", m.port)
	}
	return st
}

func (m *mcpManager) shutdown() {
	m.mu.Lock()
	m.stopLocked()
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Wails-bound methods

func (a *App) GetMCPStatus() MCPStatus {
	a.mcp.mu.Lock()
	defer a.mcp.mu.Unlock()
	return a.mcp.statusLocked()
}

func (a *App) SetMCPEnabled(enabled bool) MCPStatus {
	m := a.mcp
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

	a.persistMCP(st)
	runtime.EventsEmit(a.ctx, "mcp:status", st)
	// The public tunnel forwards to the MCP listener; follow it up or down.
	a.tunnel.onMCPChanged()
	return st
}

func (a *App) SetMCPPort(port int) MCPStatus {
	m := a.mcp
	m.mu.Lock()
	if port < 1024 || port > 65535 {
		m.err = fmt.Sprintf("port must be between 1024 and 65535 (got %d)", port)
		st := m.statusLocked()
		m.mu.Unlock()
		return st
	}
	m.port = port
	if m.enabled {
		m.stopLocked()
		m.startLocked()
	}
	st := m.statusLocked()
	m.mu.Unlock()

	a.persistMCP(st)
	runtime.EventsEmit(a.ctx, "mcp:status", st)
	a.tunnel.onMCPChanged()
	return st
}

func (a *App) persistMCP(st MCPStatus) {
	s := a.loadSettings()
	s.MCP.Enabled = st.Enabled
	s.MCP.Port = st.Port
	_ = a.saveSettings(s)
}

// ---------------------------------------------------------------------------
// desktopBackend adapts the App's session manager to mcp.Backend.

type desktopBackend struct{ app *App }

func (b *desktopBackend) StoreDir() string { return b.app.storeDir }

func (b *desktopBackend) StartCrawl(ctx context.Context, req mcp.StartRequest) (string, error) {
	a := b.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil && !a.session.finished() {
		return "", fmt.Errorf("a crawl is already running (crawl %s) — pause_crawl or stop_crawl first", a.session.st.ID)
	}
	cfg, err := mcp.BuildConfig(a.storeDir, req)
	if err != nil {
		return "", err
	}
	seeds, mode, err := mcp.ResolveSeeds(ctx, cfg, req)
	if err != nil {
		return "", err
	}
	st, err := store.CreateCrawl(a.storeDir, mcp.DefaultProject(req.Project, seeds), seeds[0], mode, cfg)
	if err != nil {
		return "", err
	}
	s, err := newCrawlSession(a, st, cfg, seeds, nil, nil)
	if err != nil {
		st.Close()
		return "", err
	}
	a.session = s
	go s.run()
	// the UI jumps to the live progress view, same as a hand-started crawl
	runtime.EventsEmit(a.ctx, "crawl:started", st.ID)
	return st.ID, nil
}

func (b *desktopBackend) ResumeCrawl(id string) (string, error) {
	id, err := b.app.ResumeCrawl(id)
	if err == nil {
		runtime.EventsEmit(b.app.ctx, "crawl:started", id)
	}
	return id, err
}

func (b *desktopBackend) PauseCrawl() error {
	if b.app.ActiveProgress() == nil {
		return fmt.Errorf("no crawl is running")
	}
	b.app.PauseCrawl()
	return nil
}

func (b *desktopBackend) StopCrawl() error {
	if b.app.ActiveProgress() == nil {
		return fmt.Errorf("no crawl is running")
	}
	b.app.StopCrawl()
	return nil
}

func (b *desktopBackend) Progress() *mcp.Progress {
	p := b.app.ActiveProgress()
	if p == nil {
		return nil
	}
	return &mcp.Progress{
		CrawlID: p.CrawlID, Seed: p.Seed, State: p.State,
		Total: p.Total, Discovered: p.Discovered, Queue: p.Queue,
		S2xx: p.S2xx, S3xx: p.S3xx, S4xx: p.S4xx, S5xx: p.S5xx,
		Blocked: p.Blocked, NoResponse: p.NoResp, Indexable: p.Indexable,
		RatePerSec: p.Rate, ElapsedSec: p.ElapsedSec,
	}
}
