package main

// In-process cobra tests for the CLI surface (#74 R4): cmd/bluesnake shipped
// with zero unit tests and sat outside the coverage gate — the structural
// reason a CLI-only resume-corruption bug (R1) stayed invisible. The
// acceptance suite execs the built binary as a subprocess, which in-process
// coverage instrumentation cannot see, so these tests are what count toward
// the gate — and they pin the command contracts (exit codes, output shape)
// directly.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/store"
)

// twoPageSite serves "/" -> /a with distinct titles.
func twoPageSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/a">a</a></body></html>`)
		default:
			fmt.Fprint(w, `<html><head><title>Leaf</title></head><body><p>leaf body</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// completedCrawl runs a real `bluesnake crawl` in-process and returns the
// store dir + crawl id.
func completedCrawl(t *testing.T) (string, string) {
	t.Helper()
	srv := twoPageSite(t)
	dir := t.TempDir()
	out, code := runCmd(t, "crawl", srv.URL+"/", "--store-dir", dir, "--quiet")
	if code != 0 {
		t.Fatalf("crawl: exit %d, output:\n%s", code, out)
	}
	infos, err := store.ListCrawls(dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("registry after crawl: %v %v", infos, err)
	}
	if infos[0].Status != store.StatusCompleted {
		t.Fatalf("crawl status = %q, want completed", infos[0].Status)
	}
	return dir, infos[0].ID
}

func TestCrawlCmd_FlagsAndSummary(t *testing.T) {
	srv := twoPageSite(t)
	dir := t.TempDir()
	out, code := runCmd(t, "crawl", srv.URL+"/", "--store-dir", dir,
		"--threads", "2", "--depth", "5", "--rate", "500", "--max-urls", "50",
		"--include", ".*", "--user-agent", "cli-test/1.0")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	for _, want := range []string{"Found", "Crawl ID:", "indexable:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestCrawlCmd_Errors(t *testing.T) {
	dir := t.TempDir()
	if out, code := runCmd(t, "crawl", "not-a-url", "--store-dir", dir); code != 1 {
		t.Errorf("bad url: exit %d, want 1, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "crawl", "https://e.com/", "--store-dir", dir, "--set", "nope=1"); code != 2 {
		t.Errorf("bad --set: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "crawl", "https://e.com/", "--store-dir", dir, "--config", filepath.Join(dir, "missing.yaml")); code != 2 {
		t.Errorf("missing --config: exit %d, want 2", code)
	}
}

func TestListCmd_FileHappyAndErrors(t *testing.T) {
	srv := twoPageSite(t)
	dir := t.TempDir()
	listFile := filepath.Join(dir, "urls.txt")
	if err := os.WriteFile(listFile, []byte(srv.URL+"/\n"+srv.URL+"/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCmd(t, "list", listFile, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	for _, want := range []string{"list mode: 2 URLs", "Crawl ID:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	infos, _ := store.ListCrawls(dir)
	if len(infos) != 1 || infos[0].Mode != "list" || infos[0].Status != store.StatusCompleted {
		t.Errorf("registry = %+v, want one completed list crawl", infos)
	}

	if _, code := runCmd(t, "list", "--store-dir", dir); code != 2 {
		t.Errorf("no input: exit %d, want 2", code)
	}
	empty := filepath.Join(dir, "empty.txt")
	os.WriteFile(empty, []byte("no urls here"), 0o644)
	if _, code := runCmd(t, "list", empty, "--store-dir", dir); code != 2 {
		t.Errorf("empty list: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "list", listFile, "--store-dir", dir, "--set", "bogus=1"); code != 2 {
		t.Errorf("bad --set: exit %d, want 2", code)
	}
}

func TestCompareCmd(t *testing.T) {
	srv := twoPageSite(t)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if out, code := runCmd(t, "crawl", srv.URL+"/", "--store-dir", dir, "--quiet"); code != 0 {
			t.Fatalf("crawl %d: exit %d, output:\n%s", i, code, out)
		}
	}
	infos, _ := store.ListCrawls(dir)
	if len(infos) != 2 {
		t.Fatalf("want 2 crawls, got %d", len(infos))
	}
	outPath := filepath.Join(dir, "cmp.json")
	out, code := runCmd(t, "compare", infos[0].ID, infos[1].ID, "--store-dir", dir,
		"--format", "json", "-o", outPath)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "Pages: 2 -> 2") {
		t.Errorf("comparison summary missing:\n%s", out)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("comparison dataset not written: %v", err)
	}
	if _, code := runCmd(t, "compare", "nope-a", "nope-b", "--store-dir", dir); code != 2 {
		t.Errorf("unknown ids: exit %d, want 2", code)
	}
}

func TestExportAndReportCmds(t *testing.T) {
	dir, id := completedCrawl(t)

	listOut, code := runCmd(t, "export", "--list")
	if code != 0 || !strings.Contains(listOut, "internal") {
		t.Fatalf("export --list: exit %d, output:\n%s", code, listOut)
	}
	out, code := runCmd(t, "export", id, "internal", "--store-dir", dir)
	if code != 0 {
		t.Fatalf("export internal: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "url") || len(strings.Split(strings.TrimSpace(out), "\n")) < 3 {
		t.Errorf("export csv looks wrong (want header + 2 rows):\n%s", out)
	}
	if out, code := runCmd(t, "export", id, "internal", "--store-dir", dir, "--format", "json"); code != 0 || !strings.Contains(out, "\"url\"") {
		t.Errorf("json export: exit %d, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "export", id, "internal", "--store-dir", dir, "--format", "xlsx"); code != 2 {
		t.Errorf("xlsx without --output: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "export", id, "--store-dir", dir); code != 2 {
		t.Errorf("missing tab arg: exit %d, want 2", code)
	}
	if _, code := runCmd(t, "export", "nope", "internal", "--store-dir", dir); code != 2 {
		t.Errorf("unknown crawl: exit %d, want 2", code)
	}

	repList, code := runCmd(t, "report", "--list")
	if code != 0 || strings.TrimSpace(repList) == "" {
		t.Fatalf("report --list: exit %d, output:\n%s", code, repList)
	}
	name := strings.Split(strings.TrimSpace(repList), "\n")[0]
	if out, code := runCmd(t, "report", id, name, "--store-dir", dir); code != 0 {
		t.Errorf("report %s: exit %d, output:\n%s", name, code, out)
	}
}

func TestSitemapCmd(t *testing.T) {
	dir, id := completedCrawl(t)
	outDir := filepath.Join(dir, "sm")
	out, code := runCmd(t, "sitemap", id, "--store-dir", dir, "-o", outDir)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		t.Errorf("no sitemap files written: %v %v", entries, err)
	}
}

func TestIssuesCmd(t *testing.T) {
	dir, id := completedCrawl(t)
	out, code := runCmd(t, "issues", id, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "SEVERITY") {
		t.Errorf("issues table missing header:\n%s", out)
	}
	// --urls with a real issue id from the table output
	m := regexp.MustCompile(`\s(\S+)\s*$`).FindStringSubmatch(strings.Split(strings.TrimSpace(out), "\n")[1])
	if m != nil {
		if _, code := runCmd(t, "issues", id, "--store-dir", dir, "--urls", m[1]); code != 0 {
			t.Errorf("issues --urls %s: exit %d", m[1], code)
		}
	}
	if _, code := runCmd(t, "issues", "nope", "--store-dir", dir); code != 2 {
		t.Errorf("unknown crawl: exit %d, want 2", code)
	}
}

func TestAnalyzeCmd(t *testing.T) {
	dir, id := completedCrawl(t)
	out, code := runCmd(t, "analyze", id, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "Analysis:") {
		t.Errorf("analysis summary missing:\n%s", out)
	}
	if _, code := runCmd(t, "analyze", "nope", "--store-dir", dir); code != 2 {
		t.Errorf("unknown crawl: exit %d, want 2", code)
	}
}

func TestQueueCmds(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "queue", "ls", "--store-dir", dir)
	if code != 0 || !strings.Contains(out, "queue is empty") {
		t.Fatalf("empty queue ls: exit %d, output:\n%s", code, out)
	}
	j, err := store.EnqueueJob(dir, store.Job{Source: "manual", Label: "q-test", Request: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	out, code = runCmd(t, "queue", "ls", "--store-dir", dir)
	if code != 0 || !strings.Contains(out, "q-test") {
		t.Fatalf("queue ls: exit %d, output:\n%s", code, out)
	}
	// queued -> cancel; then terminal -> remove
	if out, code := runCmd(t, "queue", "rm", j.ID, "--store-dir", dir); code != 0 || !strings.Contains(out, "canceled") {
		t.Errorf("rm queued: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "queue", "rm", j.ID, "--store-dir", dir); code != 0 || !strings.Contains(out, "removed") {
		t.Errorf("rm terminal: exit %d, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "queue", "rm", "job-nope", "--store-dir", dir); code != 1 {
		t.Errorf("rm unknown: exit %d, want 1", code)
	}
}

func TestCrawlsCmds(t *testing.T) {
	dir, id := completedCrawl(t)
	out, code := runCmd(t, "crawls", "ls", "--store-dir", dir)
	if code != 0 || !strings.Contains(out, id) || !strings.Contains(out, "completed") {
		t.Fatalf("crawls ls: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "crawls", "rm", id, "--store-dir", dir); code != 0 || !strings.Contains(out, "deleted") {
		t.Errorf("crawls rm: exit %d, output:\n%s", code, out)
	}
	out, _ = runCmd(t, "crawls", "ls", "--store-dir", dir)
	if strings.Contains(out, id) {
		t.Errorf("deleted crawl still listed:\n%s", out)
	}
}

func TestConfigCmds(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "config", "init", "--stdout")
	if code != 0 || !strings.Contains(out, "speed:") {
		t.Fatalf("config init --stdout: exit %d", code)
	}
	cfgPath := filepath.Join(dir, "bs.yaml")
	if out, code := runCmd(t, "config", "init", "-o", cfgPath); code != 0 || !strings.Contains(out, "wrote") {
		t.Fatalf("config init -o: exit %d, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "config", "init", "-o", cfgPath); code != 2 {
		t.Errorf("config init over existing file: exit %d, want 2", code)
	}
	if out, code := runCmd(t, "config", "validate", cfgPath); code != 0 || !strings.Contains(out, "ok") {
		t.Errorf("config validate: exit %d, output:\n%s", code, out)
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("nonsense_key: true\n"), 0o644)
	if _, code := runCmd(t, "config", "validate", bad); code != 2 {
		t.Errorf("config validate bad: exit %d, want 2", code)
	}
	if out, code := runCmd(t, "config", "show", "--set", "speed.max_threads=3"); code != 0 || !strings.Contains(out, "max_threads: 3") {
		t.Errorf("config show --set: exit %d, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "config", "show", "--set", "bogus=1"); code != 2 {
		t.Errorf("config show bad set: exit %d, want 2", code)
	}
}

func TestRobotsAndVersionCmds(t *testing.T) {
	dir := t.TempDir()
	robotsPath := filepath.Join(dir, "robots.txt")
	os.WriteFile(robotsPath, []byte("User-agent: *\nDisallow: /private\n"), 0o644)
	out, code := runCmd(t, "robots", "test", "https://e.com/ok", "https://e.com/private/x", "--robots-file", robotsPath)
	if code != 0 || !strings.Contains(out, "ALLOWED") || !strings.Contains(out, "BLOCKED") {
		t.Errorf("robots test: exit %d, output:\n%s", code, out)
	}
	if _, code := runCmd(t, "robots", "test", "https://e.com/"); code != 2 {
		t.Errorf("robots test without file: exit %d, want 2", code)
	}
	if out, code := runCmd(t, "version"); code != 0 || !strings.Contains(out, "bluesnake ") {
		t.Errorf("version: exit %d, output:\n%s", code, out)
	}
}

func TestProjectCmds(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "projects", "create", "example.com", "--store-dir", dir, "--name", "Example Study")
	if code != 0 {
		t.Fatalf("create: exit %d, output:\n%s", code, out)
	}
	m := regexp.MustCompile(`created project (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no project id:\n%s", out)
	}
	id := m[1]
	if out, code := runCmd(t, "projects", "ls", "--store-dir", dir); code != 0 || !strings.Contains(out, "example.com") {
		t.Errorf("ls: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "add", id, "rival.com", "--store-dir", dir); code != 0 {
		t.Errorf("add: exit %d, output:\n%s", code, out)
	}
	out, code = runCmd(t, "projects", "show", id, "--store-dir", dir)
	if code != 0 || !strings.Contains(out, "rival.com") || !strings.Contains(out, "no crawl yet") {
		t.Errorf("show: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "compare", id, "--store-dir", dir); code != 0 || !strings.Contains(out, "Example Study") {
		t.Errorf("compare: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "diff", id, "rival.com", "--store-dir", dir); code != 0 || !strings.Contains(out, "at least two comparable crawls") {
		t.Errorf("diff: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "remove", id, "rival.com", "--store-dir", dir); code != 0 {
		t.Errorf("remove: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "rm", id, "--store-dir", dir); code != 0 {
		t.Errorf("rm: exit %d, output:\n%s", code, out)
	}
}

// TestProjectCrawlAll_HappyPath drives the real crawl-all path over one member
// whose "domain" is an https-refusing local listener — the crawl fails fast,
// but the group lifecycle (enqueue → drain → per-member line → done) completes.
func TestProjectCrawlAll_HappyPath(t *testing.T) {
	dir := t.TempDir()
	main := hangListener(t) // TLS handshake fails/pauses; member outcome is an errored crawl
	out, code := runCmd(t, "projects", "create", main, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("create: exit %d", code)
	}
	m := regexp.MustCompile(`created project (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("no project id")
	}
	// An empty project reports and exits 0.
	out2, code := runCmd(t, "projects", "create", "empty.example", "--store-dir", dir)
	if code != 0 {
		t.Fatalf("create empty: exit %d", code)
	}
	m2 := regexp.MustCompile(`created project (\S+)`).FindStringSubmatch(out2)
	// remove the auto-added main member so the project is genuinely empty
	if out, code := runCmd(t, "projects", "remove", m2[1], "empty.example", "--store-dir", dir); code != 0 {
		t.Fatalf("remove main member: exit %d, output:\n%s", code, out)
	}
	if out, code := runCmd(t, "projects", "crawl-all", m2[1], "--store-dir", dir); code != 0 || !strings.Contains(out, "no member domains") {
		t.Errorf("empty crawl-all: exit %d, output:\n%s", code, out)
	}
}
