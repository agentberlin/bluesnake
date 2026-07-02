package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDefaultProfile drops a default-profile YAML into storeDir the way the
// desktop's settings view persists it.
func writeDefaultProfile(t *testing.T, storeDir, yaml string) {
	t.Helper()
	dir := filepath.Join(storeDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, profileSlug(DefaultProfileName)+".yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProcessWiring pins the knob-to-wiring resolution the desktop and the
// standalone MCP server share: max_concurrent_crawls from the default profile
// drives the dispatcher width, and a shared limiter exists exactly when W>1.
func TestProcessWiring(t *testing.T) {
	t.Run("no profile defaults to single-crawl", func(t *testing.T) {
		w, lim, err := ProcessWiring(t.TempDir())
		if err != nil || w != 1 || lim != nil {
			t.Fatalf("ProcessWiring(empty) = (%d, %v, %v), want (1, nil, nil)", w, lim, err)
		}
	})
	t.Run("knob unset keeps single-crawl", func(t *testing.T) {
		dir := t.TempDir()
		writeDefaultProfile(t, dir, "speed:\n  max_threads: 3\n")
		w, lim, err := ProcessWiring(dir)
		if err != nil || w != 1 || lim != nil {
			t.Fatalf("ProcessWiring(knob unset) = (%d, %v, %v), want (1, nil, nil)", w, lim, err)
		}
	})
	t.Run("knob drives width and shared limiter", func(t *testing.T) {
		dir := t.TempDir()
		writeDefaultProfile(t, dir, "speed:\n  max_concurrent_crawls: 3\n  max_global_threads: 7\n")
		w, lim, err := ProcessWiring(dir)
		if err != nil {
			t.Fatal(err)
		}
		if w != 3 {
			t.Errorf("concurrency = %d, want 3 (speed.max_concurrent_crawls)", w)
		}
		if lim == nil {
			t.Fatal("no shared limiter with W>1 — M parallel crawls would each cap themselves (H1/P17)")
		}
	})
	t.Run("unreadable profile fails safe to single-crawl", func(t *testing.T) {
		dir := t.TempDir()
		writeDefaultProfile(t, dir, "speed: [not a mapping\n")
		w, lim, err := ProcessWiring(dir)
		if err == nil {
			t.Error("corrupt default profile returned no error")
		}
		if w != 1 || lim != nil {
			t.Errorf("fail-safe wiring = (%d, %v), want (1, nil)", w, lim)
		}
	})
}
