//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/agentberlin/bluesnake/internal/selfupdate"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// applyParentDir stages the update next to the bundle (e.g. /Applications) so the
// final swap is an atomic rename on the same volume. An unwritable parent here
// is exactly the "can't update in place" case — the caller surfaces it and offers
// the release page instead.
func applyParentDir() (string, error) {
	bundle, err := macAppBundle()
	if err != nil {
		return "", err
	}
	return filepath.Dir(bundle), nil
}

// macAppBundle resolves the running .app bundle, rejecting a translocated copy
// (run from the DMG/quarantine), which can't self-update.
func macAppBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if rp, err := filepath.EvalSymlinks(exe); err == nil {
		exe = rp
	}
	// <name>.app/Contents/MacOS/<exe>
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(bundle, ".app") {
		return "", fmt.Errorf("not running from an .app bundle (%s) — open bluesnake from Applications to update", bundle)
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		return "", fmt.Errorf("bluesnake is running from a quarantined copy — move it to your Applications folder, reopen it, then update")
	}
	return bundle, nil
}

// installUpdate swaps the running bundle for the freshly-downloaded one and
// relaunches. zipPath is the verified universal app .zip; stageDir is the temp
// dir under the bundle's parent that selfupdate.Download wrote it into.
func installUpdate(m *updateManager, rel *selfupdate.Release, zipPath string) error {
	bundle, err := macAppBundle()
	if err != nil {
		return err
	}
	stageDir := filepath.Dir(zipPath)

	// 1. Extract with ditto — it round-trips the bundle's symlinks/metadata,
	//    which archive/zip would not.
	if out, err := exec.Command("/usr/bin/ditto", "-x", "-k", zipPath, stageDir).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn't unpack the update: %v: %s", err, strings.TrimSpace(string(out)))
	}
	newApp := filepath.Join(stageDir, filepath.Base(bundle))
	if _, err := os.Stat(newApp); err != nil {
		alt, ok := firstAppBundle(stageDir)
		if !ok {
			return fmt.Errorf("the update package didn't contain an .app bundle")
		}
		newApp = alt
	}

	// 2. Strip quarantine so the swapped bundle launches without a Gatekeeper
	//    prompt (Ventura 13.1+ re-adds it on unpack). No admin required.
	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", newApp).Run()

	// 3. Swap (same-volume renames). On failure, restore the original.
	old := bundle + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("couldn't replace the app (is it in a writable location?): %w", err)
	}
	if err := os.Rename(newApp, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return fmt.Errorf("couldn't install the update: %w", err)
	}

	// 4. Relaunch after we exit; clean up the old bundle + staging dir.
	if err := scheduleRelaunch(bundle, old, stageDir); err != nil {
		return fmt.Errorf("update installed but couldn't schedule a relaunch — quit and reopen bluesnake: %w", err)
	}
	wruntime.Quit(m.app.ctx)
	return nil
}

func firstAppBundle(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// scheduleRelaunch spawns a detached shell that waits for this process to exit,
// reopens the (already-swapped) bundle, then removes the old bundle, staging dir,
// and itself.
func scheduleRelaunch(bundle, old, stage string) error {
	f, err := os.CreateTemp("", "bluesnake-relaunch-*.sh")
	if err != nil {
		return err
	}
	script := f.Name()
	body := fmt.Sprintf(`#!/bin/sh
pid=%d
while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
sleep 0.3
/usr/bin/open %q
rm -rf %q %q
rm -f "$0"
`, os.Getpid(), bundle, old, stage)
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := os.Chmod(script, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from our session
	return cmd.Start()
}
