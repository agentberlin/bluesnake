//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/agentberlin/bluesnake/internal/selfupdate"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// On Windows the update artifact is the NSIS installer, which we just stage in a
// scratch dir — no same-volume rename needed (the installer does the swap).
func applyParentDir() (string, error) { return os.TempDir(), nil }

// installUpdate runs the downloaded NSIS installer after this process exits. A
// running .exe can't be overwritten in place, so a detached helper batch waits
// for our PID to go away, then launches the installer (which handles file
// replacement, UAC elevation, and relaunch).
func installUpdate(m *updateManager, rel *selfupdate.Release, installer string) error {
	pid := os.Getpid()
	bat := filepath.Join(filepath.Dir(installer), "bluesnake-update.bat")
	body := fmt.Sprintf("@echo off\r\n"+
		":wait\r\n"+
		"tasklist /FI \"PID eq %d\" 2>NUL | find \"%d\" >NUL\r\n"+
		"if not errorlevel 1 (\r\n"+
		"  timeout /t 1 /nobreak >NUL\r\n"+
		"  goto wait\r\n"+
		")\r\n"+
		"start \"\" \"%s\"\r\n"+
		"del \"%%~f0\"\r\n",
		pid, pid, installer)
	if err := os.WriteFile(bat, []byte(body), 0o644); err != nil {
		return err
	}
	// `start` detaches the batch so it survives our exit.
	if err := exec.Command("cmd", "/c", "start", "", "/min", bat).Start(); err != nil {
		return err
	}
	wruntime.Quit(m.app.ctx)
	return nil
}
