// Package version is the build-time version of bluesnake, embedded from the
// adjacent VERSION file. Both Go binaries (the CLI and the desktop app) embed it
// here, and the desktop frontend build reads the same file (see
// desktop/frontend/vite.config.js) — so stamping that one file sets the version
// everywhere.
//
// For released builds, CI derives the version from the pushed git tag
// (vX.Y.Z -> X.Y.Z) and overwrites VERSION before building, so the tag is
// authoritative. The committed value is therefore only a development
// placeholder ("0.0.0-dev"); local/source builds report that, released binaries
// report the tag. See .github/workflows/release.yml.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Version is the trimmed semantic version, e.g. "0.2.0" (or "0.0.0-dev" for an
// unstamped local build).
var Version = strings.TrimSpace(raw)
