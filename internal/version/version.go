// Package version is the single source of truth for the bluesnake version.
//
// The version lives in the adjacent VERSION file so every consumer derives
// from one place: both Go binaries (the CLI and the desktop app) embed it
// here, and the desktop frontend build reads the same file (see
// desktop/frontend/vite.config.js). Bump the version by editing VERSION only.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Version is the trimmed semantic version, e.g. "0.1.0".
var Version = strings.TrimSpace(raw)
