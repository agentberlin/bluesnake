package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// `config show --crawl <id>` prints the exact config frozen into a stored crawl,
// so a crawl's settings are inspectable headless (parity with the desktop Setup
// tab). This is the keystone that also makes re-running a crawl's config
// composable at the CLI: `config show --crawl X > profile.yaml`.
func TestConfigShowCrawlFrozen(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Speed.MaxThreads = 11
	st, err := store.CreateCrawl(dir, []string{"https://example.com/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()

	var out, errBuf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"config", "show", "--crawl", id, "--store-dir", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, errBuf.String())
	}
	if !strings.Contains(out.String(), "max_threads: 11") {
		t.Errorf("frozen config not printed:\n%s", out.String())
	}

	// --crawl is a standalone source: combining it with --set/--config is rejected.
	root2 := newRootCmd()
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	root2.SetArgs([]string{"config", "show", "--crawl", id, "--store-dir", dir, "--set", "speed.max_threads=3"})
	if err := root2.Execute(); err == nil {
		t.Error("expected an error when --crawl is combined with --set")
	}

	// an unknown crawl id is a clean config error, not a panic.
	root3 := newRootCmd()
	root3.SetOut(&bytes.Buffer{})
	root3.SetErr(&bytes.Buffer{})
	root3.SetArgs([]string{"config", "show", "--crawl", "nope", "--store-dir", dir})
	if err := root3.Execute(); err == nil {
		t.Error("expected an error for an unknown crawl id")
	}
}
