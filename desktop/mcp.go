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
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
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
// desktopBackend adapts the App's crawl queue to mcp.Backend, so an LLM-started
// crawl streams into the UI exactly like a hand-started one. It shares the
// standalone MCP Runner's start contract (mcp.StartViaQueue): up to the app's
// queue concurrency crawls run at once, a start beyond that capacity is
// rejected (never silently queued behind hand-started work), and the crawl id
// is returned once the dispatcher has begun the crawl. Control is addressed by
// crawl id, so an agent can pause one of several parallel crawls.

type desktopBackend struct {
	app     *App
	startMu sync.Mutex // serializes the capacity check against racing starts
}

func (b *desktopBackend) StoreDir() string { return b.app.storeDir }

func (b *desktopBackend) StartCrawl(ctx context.Context, req mcp.StartRequest) (string, error) {
	a := b.app
	a.ensureQueue()
	spec := req.Spec()
	if err := runner.ValidateSpec(a.storeDir, spec); err != nil {
		return "", err
	}
	// the observer emits crawl:started when the dispatcher begins the crawl, so
	// the UI jumps to the live view just like a hand-started crawl
	return mcp.StartViaQueue(ctx, a.disp, a.queueW, &b.startMu, spec, req.Label())
}

func (b *desktopBackend) ResumeCrawl(id string) (string, error) {
	a := b.app
	a.ensureQueue()
	a.invalidate(id)
	return mcp.StartViaQueue(context.Background(), a.disp, a.queueW, &b.startMu,
		queue.JobSpec{ResumeID: id}, "resume "+id)
}

func (b *desktopBackend) PauseCrawl(crawlID string) error {
	a := b.app
	a.ensureQueue()
	if _, ok := a.exec.SnapshotCrawl(crawlID); !ok {
		return fmt.Errorf("crawl %s is not running", crawlID)
	}
	a.disp.PauseCrawl(crawlID)
	return nil
}

func (b *desktopBackend) StopCrawl(crawlID string) error {
	a := b.app
	a.ensureQueue()
	if _, ok := a.exec.SnapshotCrawl(crawlID); !ok {
		return fmt.Errorf("crawl %s is not running", crawlID)
	}
	a.disp.StopCrawl(crawlID)
	return nil
}

func (b *desktopBackend) Running() []mcp.Progress {
	a := b.app
	a.ensureQueue()
	snaps := a.exec.Snapshots()
	out := make([]mcp.Progress, len(snaps))
	for i, s := range snaps {
		out[i] = mcp.ProgressFromSnapshot(s)
	}
	return out
}
