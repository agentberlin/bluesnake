package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

func fixtureSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>t</title></head><body>hello</body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The CLI's #88 default: a bare `crawl` reuses the setup the site last ran
// with (here: a --set override from the first run), names its source out
// loud, and `--setup defaults` opts back out to the pinned built-ins.
func TestCrawlReusesLastSetupByDefault(t *testing.T) {
	dir := t.TempDir()
	srv := fixtureSite(t)

	if _, errOut, err := runCLI(t, "crawl", srv.URL, "--store-dir", dir, "-q",
		"--set", "speed.max_threads=2"); err != nil {
		t.Fatalf("first crawl: %v (%s)", err, errOut)
	}

	out, errOut, err := runCLI(t, "crawl", srv.URL, "--store-dir", dir)
	if err != nil {
		t.Fatalf("second crawl: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "last crawl") {
		t.Errorf("second crawl must name its setup source, got: %q", out)
	}

	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("have %d crawls, want 2", len(infos))
	}
	assertFrozenThreads(t, dir, infos[1].ID, 2) // inherited from the first run

	// --setup defaults ignores the remembered setup
	if _, errOut, err = runCLI(t, "crawl", srv.URL, "--store-dir", dir, "-q", "--setup", "defaults"); err != nil {
		t.Fatalf("defaults crawl: %v (%s)", err, errOut)
	}
	infos, _ = store.ListCrawls(dir)
	assertFrozenThreads(t, dir, infos[2].ID, config.Default().Speed.MaxThreads)
}

func assertFrozenThreads(t *testing.T, dir, id string, want int) {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfgYAML, err := st.Meta("config")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speed.MaxThreads != want {
		t.Errorf("crawl %s frozen max_threads = %d, want %d", id, cfg.Speed.MaxThreads, want)
	}
}

func TestCrawlSetupFlagValidation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCLI(t, "crawl", "https://e.com/", "--store-dir", dir,
		"--setup", "app", "--profile", "Slow"); err == nil {
		t.Error("--setup with --profile accepted")
	}
	if _, _, err := runCLI(t, "crawl", "https://e.com/", "--store-dir", dir,
		"--setup", "bogus"); err == nil {
		t.Error("unknown --setup value accepted")
	}
}

// Crawl-all's default mode: each member crawls with its own site's last
// setup; a member never crawled before falls back to the app settings. The
// per-member resolution is printed before anything runs.
func TestProjectsCrawlAllPerMemberSetups(t *testing.T) {
	dir := t.TempDir()
	srv := fixtureSite(t)
	host := strings.TrimPrefix(srv.URL, "http://")

	// the member site remembers a custom setup from an earlier crawl
	if _, errOut, err := runCLI(t, "crawl", srv.URL, "--store-dir", dir, "-q",
		"--set", "speed.max_threads=2"); err != nil {
		t.Fatalf("seed crawl: %v (%s)", err, errOut)
	}

	out, _, err := runCLI(t, "projects", "create", host, "--store-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(out) // "created project <id> (<name>)"
	id := fields[2]
	if _, _, err := runCLI(t, "projects", "add", id, "never.example", "--store-dir", dir); err != nil {
		t.Fatal(err)
	}

	out, _, err = runCLI(t, "projects", "crawl-all", id, "--store-dir", dir)
	if err != nil {
		t.Fatalf("crawl-all: %v", err)
	}
	if !strings.Contains(out, host+" — last crawl setup") {
		t.Errorf("member with history must announce its last-crawl setup, got:\n%s", out)
	}
	if !strings.Contains(out, "never.example — app settings") {
		t.Errorf("never-crawled member must announce the app-settings fallback, got:\n%s", out)
	}

	// the member's new crawl froze its own remembered setup (the enqueue URL is
	// https://<host>, which is the same site key as the http seed crawl)
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	var memberCrawls []string
	for _, in := range infos[1:] { // [0] is the seed crawl
		if strings.Contains(in.Seed, host) {
			memberCrawls = append(memberCrawls, in.ID)
		}
	}
	if len(memberCrawls) != 1 {
		t.Fatalf("member crawls = %v, want exactly 1", memberCrawls)
	}
	assertFrozenThreads(t, dir, memberCrawls[0], 2)
}

func TestProjectsCrawlAllSetupFlagValidation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCLI(t, "projects", "crawl-all", "p1", "--store-dir", dir,
		"--setup", "app", "--profile", "Slow"); err == nil {
		t.Error("--setup with --profile accepted")
	}
	if _, _, err := runCLI(t, "projects", "crawl-all", "p1", "--store-dir", dir,
		"--setup", "bogus"); err == nil {
		t.Error("unknown --setup value accepted")
	}
}

// `--setup app` is the app settings — the saved default profile, the same
// base the desktop and MCP mean by it.
func TestCrawlSetupApp(t *testing.T) {
	dir := t.TempDir()
	srv := fixtureSite(t)
	writeTestProfile(t, dir, "default-audit", "Default audit", "speed:\n  max_threads: 4\n")

	if _, errOut, err := runCLI(t, "crawl", srv.URL, "--store-dir", dir, "-q", "--setup", "app"); err != nil {
		t.Fatalf("crawl: %v (%s)", err, errOut)
	}
	infos, err := store.ListCrawls(dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("crawls=%d err=%v", len(infos), err)
	}
	assertFrozenThreads(t, dir, infos[0].ID, 4)
}
