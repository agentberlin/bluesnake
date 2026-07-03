package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestProfile writes a named profile file the way the desktop app does
// ("# Name" header + YAML body) into <dir>/profiles.
func writeTestProfile(t *testing.T, dir, slug, name, body string) {
	t.Helper()
	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, slug+".yaml"),
		[]byte("# "+name+"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

// `config profiles` is the CLI twin of MCP's list_profiles: read-only, default
// (the app settings) first, friendly message when nothing is saved yet.
func TestConfigProfilesList(t *testing.T) {
	dir := t.TempDir()

	out, _, err := runCLI(t, "config", "profiles", "--store-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no profiles saved") {
		t.Errorf("empty store: %q", out)
	}

	writeTestProfile(t, dir, "slow", "Slow", "speed:\n  max_threads: 3\n")
	writeTestProfile(t, dir, "default-audit", "Default audit", "speed:\n  max_threads: 5\n")

	out, _, err = runCLI(t, "config", "profiles", "--store-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Default audit") || lines[1] != "Slow" {
		t.Errorf("profiles listing = %q, want default first then Slow", out)
	}
}

// `config show --profile` prints a profile's effective config (the CLI twin of
// get_profile_config), composable with --set on top.
func TestConfigShowProfile(t *testing.T) {
	dir := t.TempDir()
	writeTestProfile(t, dir, "slow", "Slow", "speed:\n  max_threads: 3\n")

	out, _, err := runCLI(t, "config", "show", "--profile", "Slow", "--store-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "max_threads: 3") {
		t.Errorf("profile config not printed:\n%s", out)
	}

	// --set overrides apply on top of the profile
	out, _, err = runCLI(t, "config", "show", "--profile", "Slow", "--set", "speed.max_threads=7", "--store-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "max_threads: 7") {
		t.Errorf("--set must win over the profile:\n%s", out)
	}

	// an unknown profile is a clean config error
	if _, _, err := runCLI(t, "config", "show", "--profile", "Nope", "--store-dir", dir); err == nil {
		t.Error("unknown profile accepted")
	}

	// --profile and --config are mutually exclusive
	if _, _, err := runCLI(t, "config", "show", "--profile", "Slow", "--config", "x.yaml", "--store-dir", dir); err == nil {
		t.Error("--profile combined with --config accepted")
	}

	// --crawl stays a standalone source
	if _, _, err := runCLI(t, "config", "show", "--crawl", "abc", "--profile", "Slow", "--store-dir", dir); err == nil {
		t.Error("--crawl combined with --profile accepted")
	}
}

// crawl/list/projects crawl-all accept --profile with the same guard rails;
// the bad flag combination must fail fast, before anything is enqueued.
func TestCrawlProfileFlagGuards(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := runCLI(t, "crawl", "https://example.com/", "--profile", "Slow", "--config", "x.yaml", "--store-dir", dir); err == nil {
		t.Error("crawl: --profile combined with --config accepted")
	}
	if _, _, err := runCLI(t, "crawl", "https://example.com/", "--profile", "Nope", "--store-dir", dir); err == nil {
		t.Error("crawl: unknown profile accepted")
	}
	if _, _, err := runCLI(t, "list", "--sitemap", "https://example.com/s.xml", "--profile", "Nope", "--store-dir", dir); err == nil {
		t.Error("list: unknown profile accepted")
	}
	if _, _, err := runCLI(t, "projects", "crawl-all", "some-project", "--profile", "Nope", "--store-dir", dir); err == nil {
		t.Error("projects crawl-all: unknown profile accepted")
	}
}
