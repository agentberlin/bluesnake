//go:build !darwin && !windows

package main

import (
	"errors"
	"os"

	"github.com/agentberlin/bluesnake/internal/selfupdate"
)

// No desktop artifact ships for these platforms (Linux is CLI-only), so the
// updater is unreachable in practice — selfupdate.Check reports the platform as
// unsupported and apply() stops before installUpdate. These stubs exist only so
// the package builds everywhere.

func applyParentDir() (string, error) { return os.TempDir(), nil }

func installUpdate(m *updateManager, rel *selfupdate.Release, path string) error {
	return errors.New("in-app updates aren't supported on this platform")
}
