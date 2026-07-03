package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
)

// writeProfile writes a named profile file the way the desktop app does
// ("# Name" header + YAML body).
func writeProfile(t *testing.T, dir, name, body string) {
	t.Helper()
	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, profileSlug(name)+".yaml"),
		[]byte("# "+name+"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFreezeSpecSnapshotsConfigAtEnqueue pins the freeze-at-enqueue contract:
// the effective config (profile + overrides) is resolved into ConfigYAML when
// the job is enqueued, so editing the profile afterwards changes future
// enqueues but never a job already frozen — exactly like the per-crawl config
// frozen at CreateCrawl, one step earlier.
func TestFreezeSpecSnapshotsConfigAtEnqueue(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "Slow", "speed:\n  max_threads: 3\n")

	spec := queue.JobSpec{
		URL:     "https://e.com/",
		Profile: "Slow",
		Config:  map[string]any{"limits.max_depth": 2},
	}
	frozen, err := FreezeSpec(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ConfigYAML == "" {
		t.Fatal("FreezeSpec left ConfigYAML empty")
	}
	if frozen.Config != nil {
		t.Errorf("overrides must be folded into ConfigYAML, got Config=%v", frozen.Config)
	}
	if frozen.Profile != "Slow" {
		t.Errorf("profile name must stay as provenance, got %q", frozen.Profile)
	}

	cfg, err := config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 3 || cfg.Limits.MaxDepth != 2 {
		t.Fatalf("frozen config: max_threads=%d max_depth=%d, want 3/2",
			cfg.Speed.MaxThreads, cfg.Limits.MaxDepth)
	}

	// Edit the profile while the (conceptual) job waits in the queue.
	writeProfile(t, dir, "Slow", "speed:\n  max_threads: 9\n")

	// The frozen job is immune — this is what the executor runs.
	cfg, err = config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 3 {
		t.Errorf("queued job saw the profile edit: max_threads=%d, want 3", cfg.Speed.MaxThreads)
	}

	// A fresh enqueue of the same spec picks up the edit.
	refrozen, err := FreezeSpec(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load([]byte(refrozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != 9 {
		t.Errorf("new enqueue after the edit: max_threads=%d, want 9", cfg.Speed.MaxThreads)
	}
}

// TestFreezeSpecPassThrough pins the two no-freeze arms: a resume job (runs
// its crawl's own frozen config) and an already-frozen spec (CLI/re-run),
// which must come back byte-identical.
func TestFreezeSpecPassThrough(t *testing.T) {
	dir := t.TempDir()

	resume := queue.JobSpec{ResumeID: "abc"}
	if got, err := FreezeSpec(dir, resume); err != nil || got.ResumeID != "abc" || got.ConfigYAML != "" {
		t.Errorf("resume spec must pass through untouched: got %+v, err=%v", got, err)
	}

	yamlSpec := queue.JobSpec{URL: "https://e.com/", ConfigYAML: "speed:\n  max_threads: 4\n"}
	got, err := FreezeSpec(dir, yamlSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigYAML != yamlSpec.ConfigYAML {
		t.Errorf("already-frozen spec must keep its YAML verbatim")
	}
}

// TestFreezeSpecUnknownProfile pins the enqueue-time rejection of a profile
// that doesn't exist (the caller asked for something specific).
func TestFreezeSpecUnknownProfile(t *testing.T) {
	_, err := FreezeSpec(t.TempDir(), queue.JobSpec{URL: "https://e.com/", Profile: "Nope"})
	if err == nil {
		t.Fatal("unknown profile accepted at enqueue")
	}
}

// TestFreezeSpecListMode pins that list-mode adjustments are baked into the
// frozen YAML (the executor loads it verbatim, without re-running BuildConfig).
func TestFreezeSpecListMode(t *testing.T) {
	frozen, err := FreezeSpec(t.TempDir(), queue.JobSpec{
		Mode: "list",
		URLs: []string{"https://e.com/a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(frozen.ConfigYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "list" {
		t.Errorf("frozen mode = %q, want list", cfg.Mode)
	}
	if cfg.Robots.Mode != "ignore" {
		t.Errorf("frozen robots.mode = %q, want ignore (list_mode.respect_robots defaults off)", cfg.Robots.Mode)
	}
}
