package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// The command-line tool ships embedded inside the macOS .app bundle at
// Contents/Resources/bin/bluesnake (see the release workflow). This file lets
// the app install it the way VS Code's "Install 'code' command" does — symlink
// the embedded binary onto the user's PATH — instead of shipping a loose
// "Install CLI.command" script in the DMG. The logic mirrors the old
// packaging/macos/install-cli.command. It's a no-op on builds with no embedded
// CLI (Windows, Linux, `wails dev`): CLIInfo reports Available=false and the UI
// hides the feature.

// CLIStatus is what the frontend renders (first-launch prompt + settings panel).
type CLIStatus struct {
	Available bool   `json:"available"` // an embedded CLI exists that we can install
	Installed bool   `json:"installed"` // a `bluesnake` command is already on PATH
	Source    string `json:"source"`    // path of the embedded CLI binary
	Target    string `json:"target"`    // where it's linked, when installed
	Error     string `json:"error,omitempty"`
}

// CLIInfo reports whether the CLI can be installed and whether it already is.
func (a *App) CLIInfo() CLIStatus {
	src := cliSourcePath()
	st := CLIStatus{Available: src != "", Source: src}
	if target, ok := cliInstalledAt(); ok {
		st.Installed = true
		st.Target = target
	}
	return st
}

// InstallCLI symlinks the embedded CLI onto the user's PATH and returns the
// resulting status (with Error set, not returned, so the UI can show it inline).
func (a *App) InstallCLI() CLIStatus {
	src := cliSourcePath()
	if src == "" {
		return CLIStatus{Available: false, Error: "the command-line tool isn't bundled with this build"}
	}
	dir, err := cliTargetDir()
	if err != nil {
		return CLIStatus{Available: true, Source: src, Error: "no writable bin directory found: " + err.Error()}
	}
	target := filepath.Join(dir, "bluesnake")
	_ = os.Remove(target) // `ln -sf` semantics: replace whatever's already there
	if err := os.Symlink(src, target); err != nil {
		return CLIStatus{Available: true, Source: src, Error: err.Error()}
	}
	return CLIStatus{Available: true, Installed: true, Source: src, Target: target}
}

// CLIPromptSeen reports whether the first-launch install prompt has been shown.
func (a *App) CLIPromptSeen() bool { return a.loadSettings().CLI.Prompted }

// MarkCLIPromptSeen records that the prompt was shown and dismissed, so it won't
// reappear on the next launch.
func (a *App) MarkCLIPromptSeen() {
	s := a.loadSettings()
	s.CLI.Prompted = true
	_ = a.saveSettings(s)
}

// cliSourcePath returns the embedded CLI binary to link, or "" if none exists.
func cliSourcePath() string {
	// Prefer the canonical /Applications install so the symlink survives ejecting
	// the DMG or moving the copy the user launched.
	if p := "/Applications/bluesnake.app/Contents/Resources/bin/bluesnake"; isExecutableFile(p) {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if rp, err := filepath.EvalSymlinks(exe); err == nil {
		exe = rp
	}
	// <bundle>/Contents/MacOS/bluesnake -> <bundle>/Contents/Resources/bin/bluesnake
	p := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources", "bin", "bluesnake")
	if isExecutableFile(p) {
		return p
	}
	return ""
}

// cliInstalledAt finds an existing `bluesnake` on PATH in a standard bin dir.
func cliInstalledAt() (string, bool) {
	for _, d := range candidateBinDirs() {
		link := filepath.Join(d, "bluesnake")
		if fi, err := os.Lstat(link); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 || isExecutableFile(link) {
				return link, true
			}
		}
	}
	return "", false
}

// cliTargetDir picks where to write the symlink: a writable system bin dir if
// there is one, otherwise ~/.local/bin (created on demand).
func cliTargetDir() (string, error) {
	for _, d := range []string{"/usr/local/bin", "/opt/homebrew/bin"} {
		if dirWritable(d) {
			return d, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", d, err)
	}
	return d, nil
}

func candidateBinDirs() []string {
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

// dirWritable reports whether dir exists and we can create files in it.
func dirWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".bluesnake-wtest-")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}
