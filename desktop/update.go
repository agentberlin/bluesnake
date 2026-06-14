package main

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/selfupdate"
	"github.com/agentberlin/bluesnake/internal/version"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Self-update for the desktop app. The OS-agnostic check/download lives in
// internal/selfupdate; the actual install (swap the macOS .app, run the Windows
// installer) is platform-specific and lives in update_{darwin,windows,other}.go
// behind installUpdate / applyParentDir. Binaries are unsigned (docs/PACKAGING.md),
// so downloads are checksum-verified and the first run still needs the usual
// Gatekeeper/SmartScreen approval — but an in-place update of an already-trusted
// app launches without re-prompting.

// UpdateStatus is the snapshot the frontend renders (title-bar pill + Settings).
type UpdateStatus struct {
	Current           string `json:"current"`
	Latest            string `json:"latest"`
	Available         bool   `json:"available"`         // a newer build is installable on this platform (pill also checks Skipped)
	PlatformSupported bool   `json:"platformSupported"` // a desktop artifact exists for this OS
	IsDev             bool   `json:"isDev"`             // unstamped local build — updates disabled
	Notes             string `json:"notes"`
	URL               string `json:"url"`     // release page (manual-download fallback)
	Skipped           bool   `json:"skipped"` // latest == the ×-dismissed version
	Checked           string `json:"checked"` // RFC3339 of this check (empty if none)
	Error             string `json:"error,omitempty"`
}

// checkCacheTTL reuses the last network result across same-session callers (the
// launch check and the Settings panel mount) so we hit GitHub once per launch.
// The manual "Check for updates" button forces a fresh check.
const checkCacheTTL = 30 * time.Minute

type updateManager struct {
	app *App

	mu         sync.Mutex
	last       *selfupdate.Release // newest successful check (nil on dev/error)
	lastStatus UpdateStatus
	checkedAt  time.Time
	busy       bool
}

func newUpdateManager(a *App) *updateManager { return &updateManager{app: a} }

// ---------------------------------------------------------------------------
// Wails-bound methods

// AppVersion is the running build's version (single source: internal/version).
func (a *App) AppVersion() string { return version.Version }

// CheckForUpdate returns the cached result when checked recently this session,
// otherwise hits GitHub. Used by the launch check and the Settings panel mount.
func (a *App) CheckForUpdate() UpdateStatus { return a.upd.check(false) }

// RefreshUpdate forces a fresh network check (the manual "Check" button).
func (a *App) RefreshUpdate() UpdateStatus { return a.upd.check(true) }

// ApplyUpdate downloads, verifies, installs the latest release, and relaunches.
// On success the process quits during install, so the returned status is only
// meaningful when it carries an Error.
func (a *App) ApplyUpdate() UpdateStatus { return a.upd.apply() }

// SkipUpdate hides the title-bar pill for a specific version (until a newer one).
func (a *App) SkipUpdate(v string) {
	s := a.loadSettings()
	s.Updates.SkippedVersion = v
	_ = a.saveSettings(s)
}

// UpdatePrefs is the Settings panel's view of the persisted preferences.
type UpdatePrefs struct {
	AutoCheck      bool   `json:"autoCheck"`
	LastCheck      string `json:"lastCheck"`
	SkippedVersion string `json:"skippedVersion"`
}

func (a *App) GetUpdatePrefs() UpdatePrefs {
	s := a.loadSettings()
	return UpdatePrefs{
		AutoCheck:      s.Updates.AutoCheck,
		LastCheck:      s.Updates.LastCheck,
		SkippedVersion: s.Updates.SkippedVersion,
	}
}

func (a *App) SetUpdateAutoCheck(enabled bool) {
	s := a.loadSettings()
	s.Updates.AutoCheck = enabled
	_ = a.saveSettings(s)
}

// ---------------------------------------------------------------------------
// check

func (m *updateManager) check(force bool) UpdateStatus {
	m.mu.Lock()
	if !force && !m.checkedAt.IsZero() && time.Since(m.checkedAt) < checkCacheTTL {
		st := m.lastStatus
		m.mu.Unlock()
		return st
	}
	m.mu.Unlock()

	cur := version.Version
	st := UpdateStatus{Current: cur, Checked: time.Now().UTC().Format(time.RFC3339)}

	if selfupdate.IsDevBuild(cur) {
		st.IsDev = true
		m.store(nil, st)
		return st
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rel, isNewer, err := selfupdate.Check(ctx, cur)
	if err != nil {
		st.Error = err.Error()
		m.store(nil, st)
		return st
	}

	st.Latest = rel.Version
	st.Notes = rel.Notes
	st.URL = rel.HTMLURL
	st.PlatformSupported = rel.PlatformSupported
	skipped := m.app.loadSettings().Updates.SkippedVersion
	st.Skipped = isNewer && skipped == rel.Version
	// Available = "there's a newer build you can install here". The title-bar
	// pill additionally hides Skipped versions; the Settings panel does not (it's
	// where you act after dismissing the pill).
	st.Available = isNewer && rel.PlatformSupported
	m.store(rel, st)

	// remember when we last reached GitHub (shown in Settings)
	s := m.app.loadSettings()
	s.Updates.LastCheck = st.Checked
	_ = m.app.saveSettings(s)
	return st
}

func (m *updateManager) store(rel *selfupdate.Release, st UpdateStatus) {
	m.mu.Lock()
	m.last = rel
	m.lastStatus = st
	m.checkedAt = time.Now()
	m.mu.Unlock()
}

// ---------------------------------------------------------------------------
// apply

func (m *updateManager) apply() UpdateStatus {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return UpdateStatus{Error: "an update is already in progress"}
	}
	m.busy = true
	rel := m.last
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock() }()

	st := UpdateStatus{Current: version.Version}

	// No cached release (e.g. apply invoked before a check) — do one now.
	if rel == nil {
		fresh := m.check(true)
		m.mu.Lock()
		rel = m.last
		m.mu.Unlock()
		if rel == nil {
			st.Error = orDefault(fresh.Error, "no update is available")
			return st
		}
	}
	st.Latest = rel.Version
	st.URL = rel.HTMLURL
	if !rel.PlatformSupported || rel.Asset.URL == "" {
		st.Error = "in-app updates aren't supported on this platform — download from the release page"
		m.emit("error", 0, 0, st.Error)
		return st
	}

	parent, err := applyParentDir()
	if err != nil {
		st.Error = err.Error()
		m.emit("error", 0, 0, st.Error)
		return st
	}
	tmp, err := os.MkdirTemp(parent, ".bluesnake-update-")
	if err != nil {
		st.Error = "couldn't prepare the update (no writable location): " + err.Error()
		m.emit("error", 0, 0, st.Error)
		return st
	}

	m.emit("downloading", 0, 0, "")
	ctx := context.Background()
	path, err := selfupdate.Download(ctx, rel, tmp, func(done, total int64) {
		m.emit("downloading", done, total, "")
	})
	if err != nil {
		os.RemoveAll(tmp)
		st.Error = err.Error()
		m.emit("error", 0, 0, st.Error)
		return st
	}

	m.emit("applying", 0, 0, "")
	if err := installUpdate(m, rel, path); err != nil {
		os.RemoveAll(tmp)
		st.Error = err.Error()
		m.emit("error", 0, 0, st.Error)
		return st
	}
	// installUpdate spawns the relaunch helper and quits the app; reaching here
	// means it returned without quitting (shouldn't happen on success).
	return st
}

// emit streams update progress to the frontend. phase is one of
// "downloading" | "applying" | "error".
func (m *updateManager) emit(phase string, done, total int64, msg string) {
	wruntime.EventsEmit(m.app.ctx, "update:progress", map[string]any{
		"phase": phase, "done": done, "total": total, "message": msg,
	})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
