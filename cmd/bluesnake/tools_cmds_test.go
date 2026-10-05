package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// In-process surface tests for the `tools` group: the same paths the BDD
// features exercise through the built binary (invisible to the coverage
// gate), pinned here through runCmd so the human/JSON renderers count.

func toolsSite(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".xml") {
			w.Header().Set("Content-Type", "application/xml")
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestToolsListCmd(t *testing.T) {
	out, code := runCmd(t, "tools", "list")
	if code != 0 || !strings.Contains(out, "robots") || !strings.Contains(out, "serp") {
		t.Errorf("tools list: exit %d, output:\n%s", code, out)
	}
	out, code = runCmd(t, "tools", "list", "--json")
	if code != 0 || !strings.Contains(out, `"name":"robots"`) {
		t.Errorf("tools list --json: exit %d, output:\n%s", code, out)
	}
}

func TestToolsRobotsCmd(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/robots.txt": "User-agent: *\nDisallow: /private/\nSitemap: /s.xml\n",
	})
	out, code := runCmd(t, "tools", "robots", "--site", srv.URL, srv.URL+"/private/x", srv.URL+"/ok")
	if code != 0 || !strings.Contains(out, "BLOCKED") || !strings.Contains(out, "ALLOWED") {
		t.Errorf("live robots: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "sitemap") || !strings.Contains(out, "findings: none") {
		t.Errorf("health line missing:\n%s", out)
	}

	// local file + findings rendering (no Sitemap directive → a finding)
	dir := t.TempDir()
	path := filepath.Join(dir, "robots.txt")
	os.WriteFile(path, []byte("User-agent: *\nDisallow: /x\n"), 0o644)
	out, code = runCmd(t, "tools", "robots", "--robots-file", path, "https://e.com/x")
	if code != 0 || !strings.Contains(out, "BLOCKED") || !strings.Contains(out, "Missing Sitemap Directive") {
		t.Errorf("file robots: exit %d, output:\n%s", code, out)
	}

	// a 404 file renders the status branch
	missing := toolsSite(t, map[string]string{})
	if out, code = runCmd(t, "tools", "robots", "--site", missing.URL); code != 0 || !strings.Contains(out, "status 404") {
		t.Errorf("missing robots: exit %d, output:\n%s", code, out)
	}
	// unreachable host renders the fetch-error branch
	if out, code = runCmd(t, "tools", "robots", "--site", "http://127.0.0.1:1"); code != 0 || !strings.Contains(out, "unreachable") {
		t.Errorf("unreachable robots: exit %d, output:\n%s", code, out)
	}
	// no input at all is a usage error
	if _, code = runCmd(t, "tools", "robots"); code != 2 {
		t.Errorf("no input: exit %d, want 2", code)
	}
	// --json emits the {report, findings} envelope
	if out, code = runCmd(t, "tools", "robots", "--robots-file", path, "--json"); code != 0 || !strings.Contains(out, `"findings"`) {
		t.Errorf("json robots: exit %d, output:\n%s", code, out)
	}
}

func TestToolsSitemapCmd(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/sitemap.xml": `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>URL/</loc></url></urlset>`,
	})
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>`+srv.URL+`/</loc></url></urlset>`)
			return
		}
		if r.URL.Path == "/" {
			fmt.Fprint(w, "<html></html>")
			return
		}
		w.WriteHeader(404)
	})
	out, code := runCmd(t, "tools", "sitemap", srv.URL+"/sitemap.xml", "--check-entries", "1")
	if code != 0 || !strings.Contains(out, "OK") || !strings.Contains(out, "entry") {
		t.Errorf("sitemap: exit %d, output:\n%s", code, out)
	}
	empty := toolsSite(t, map[string]string{"/": "<html></html>"})
	if out, code = runCmd(t, "tools", "sitemap", empty.URL); code != 0 || !strings.Contains(out, "no sitemap found") {
		t.Errorf("missing sitemap: exit %d, output:\n%s", code, out)
	}
}

func TestToolsAIBotsCmd(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/":           "<html></html>",
		"/robots.txt": "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\nSitemap: /s.xml\n",
	})
	out, code := runCmd(t, "tools", "aibots", srv.URL)
	if code != 0 || !strings.Contains(out, "GPTBot") || !strings.Contains(out, "BLOCKED (line 2") {
		t.Errorf("aibots live: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "control token (never fetches)") || !strings.Contains(out, "control fetch") {
		t.Errorf("aibots columns missing:\n%s", out)
	}
	out, code = runCmd(t, "tools", "aibots", srv.URL, "--live=false", "--skip", "GPTBot")
	if code != 0 || strings.Contains(out, "control fetch") || strings.Contains(out, "GPTBot") {
		t.Errorf("aibots no-live/skip: exit %d, output:\n%s", code, out)
	}
}

// aibotsPagesSite serves a robots.txt with a GPTBot-only and a Googlebot-only
// rule, edge-blocks ClaudeBot, and records every page request's User-Agent.
func aibotsPagesSite(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var pageUAs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: GPTBot\nDisallow: /pricing\n\nUser-agent: Googlebot\nDisallow: /hidden\n")
			return
		}
		mu.Lock()
		pageUAs = append(pageUAs, ua)
		mu.Unlock()
		if strings.Contains(ua, "ClaudeBot") {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "<html></html>")
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(pageUAs)
	}
}

func writeURLs(t *testing.T, urls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(path, []byte(strings.Join(urls, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestToolsAIBotsURLsFile(t *testing.T) {
	srv, pageUAs := aibotsPagesSite(t)
	file := writeURLs(t, srv.URL+"/pricing", srv.URL+"/hidden")

	out, code := runCmd(t, "tools", "aibots", srv.URL, "--urls-file", file, "--json",
		"--set", "http.user_agent=PriorityProbe/1.0", "--set", "speed.max_threads=1")
	if code != 0 {
		t.Fatalf("aibots --urls-file --json: exit %d, output:\n%s", code, out)
	}
	var env struct {
		Report   sitecheck.AIBotsURLsReport `json:"report"`
		Findings []sitecheck.Finding        `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	rep := env.Report
	if len(rep.Bots) != len(sitecheck.DefaultBots()) || rep.Caveat != sitecheck.AIBotCaveat ||
		!rep.Robots.Found || !strings.Contains(rep.Robots.Body, "Disallow: /pricing") || len(rep.URLs) != 2 {
		t.Fatalf("report = bots %d, robots %+v, urls %d", len(rep.Bots), rep.Robots, len(rep.URLs))
	}
	verdict := func(page int, name string) sitecheck.NamedBotVerdict {
		for _, v := range rep.URLs[page].Bots {
			if v.Name == name {
				return v
			}
		}
		t.Fatalf("no %s verdict on %s", name, rep.URLs[page].URL)
		return sitecheck.NamedBotVerdict{}
	}
	if v := verdict(0, "GPTBot"); v.RobotsAllowed || v.RobotsRule != "Disallow: /pricing" {
		t.Errorf("GPTBot on /pricing = %+v", v)
	}
	if v := verdict(0, "ClaudeBot"); !v.RobotsAllowed || !v.BlockedLive || v.LiveStatus != 403 {
		t.Errorf("ClaudeBot on /pricing = %+v", v)
	}
	if v := verdict(1, "Applebot"); v.RobotsAllowed || v.RobotsVia != "Googlebot" {
		t.Errorf("Applebot on /hidden = %+v, want blocked via Googlebot's group", v)
	}
	if rep.URLs[0].ControlStatus != 200 || len(env.Findings) == 0 {
		t.Errorf("control = %d, findings = %v", rep.URLs[0].ControlStatus, env.Findings)
	}
	// --set http.user_agent reaches the control fetch: one per page.
	controls := 0
	for _, ua := range pageUAs() {
		if ua == "PriorityProbe/1.0" {
			controls++
		}
	}
	if controls != 2 {
		t.Errorf("control fetches with the --set user agent = %d, want 2 (one per page)", controls)
	}

	// The human renderer lists each page and the bots it turns away.
	out, code = runCmd(t, "tools", "aibots", srv.URL, "--urls-file", file, "--live=false")
	if code != 0 || !strings.Contains(out, srv.URL+"/pricing  (") ||
		!strings.Contains(out, "BLOCKED (line 2: Disallow: /pricing)") ||
		!strings.Contains(out, "via Googlebot") || strings.Contains(out, "control:") {
		t.Errorf("aibots --urls-file text: exit %d, output:\n%s", code, out)
	}
}

func TestToolsAIBotsURLsFileErrors(t *testing.T) {
	srv, _ := aibotsPagesSite(t)
	for name, args := range map[string][]string{
		"URL on another host": {"--urls-file", writeURLs(t, srv.URL+"/a", "https://elsewhere.example/b")},
		"no URLs in the file": {"--urls-file", writeURLs(t, "not a url")},
		"missing file":        {"--urls-file", filepath.Join(t.TempDir(), "absent.txt")},
		"unknown --set key":   {"--set", "speed.no_such_knob=1"},
		"invalid --set value": {"--set", "speed.max_threads=0"},
	} {
		if out, code := runCmd(t, append([]string{"tools", "aibots", srv.URL, "--live=false"}, args...)...); code != 2 {
			t.Errorf("%s: exit %d, want 2; output:\n%s", name, code, out)
		}
	}
}

// Without --urls-file, aibots emits exactly the site-root report it always
// has: the AIBots report through the shared envelope, with today's keys.
func TestToolsAIBotsWithoutURLsFileUnchanged(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/":           "<html></html>",
		"/robots.txt": "User-agent: GPTBot\nDisallow: /\n",
	})
	out, code := runCmd(t, "tools", "aibots", srv.URL, "--json")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := sitecheck.New(cfg, client).AIBots(context.Background(), srv.URL, sitecheck.AIBotOptions{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := emitToolJSON(&want, rep); err != nil {
		t.Fatal(err)
	}
	if out != want.String() {
		t.Errorf("CLI report differs from the AIBots report:\n got %s\nwant %s", out, want.String())
	}
	var env struct {
		Report map[string]json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(env.Report))
	if got := strings.Join(keys, ","); got != "bots,caveat,control_status,live,robots_found,site,url" {
		t.Errorf("report keys = %s", got)
	}
}

func TestToolsLlmsCmd(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/llms.txt": "# Site\n\n> Summary.\n\n## Docs\n- [G](/g): guide\n",
	})
	out, code := runCmd(t, "tools", "llms", srv.URL)
	if code != 0 || !strings.Contains(out, "OK") || !strings.Contains(out, "link") {
		t.Errorf("llms: exit %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "MISSING") { // llms-full.txt is absent
		t.Errorf("missing full file not rendered:\n%s", out)
	}
}

func TestToolsStructuredCmd(t *testing.T) {
	srv := toolsSite(t, map[string]string{
		"/p": `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product","name":"W","offers":{"@type":"Offer","price":"1","priceCurrency":"USD"}}
</script></head><body></body></html>`,
		"/plain": "<html><body>text</body></html>",
	})
	out, code := runCmd(t, "tools", "structured", srv.URL+"/p")
	if code != 0 || !strings.Contains(out, "Product") || !strings.Contains(out, "Rich Result Validation Errors") {
		t.Errorf("structured: exit %d, output:\n%s", code, out)
	}
	if out, code = runCmd(t, "tools", "structured", srv.URL+"/plain"); code != 0 || !strings.Contains(out, "no structured data found") {
		t.Errorf("plain page: exit %d, output:\n%s", code, out)
	}
	if out, code = runCmd(t, "tools", "structured", srv.URL+"/gone"); code != 0 || !strings.Contains(out, "nothing to validate") {
		t.Errorf("404 page: exit %d, output:\n%s", code, out)
	}
}

func TestToolsSerpCmd(t *testing.T) {
	long := strings.Repeat("WideWidgetWarehouse", 3)
	out, code := runCmd(t, "tools", "serp", "--title", long, "--description", "Too short.")
	if code != 0 || !strings.Contains(out, "displays as") || !strings.Contains(out, "Below X Pixels") {
		t.Errorf("serp pure: exit %d, output:\n%s", code, out)
	}
	srv := toolsSite(t, map[string]string{
		"/": `<html><head><title>Live</title><meta name="description" content="Live description of the page under test."></head><body></body></html>`,
	})
	if out, code = runCmd(t, "tools", "serp", "--url", srv.URL); code != 0 || !strings.Contains(out, "Live") {
		t.Errorf("serp url: exit %d, output:\n%s", code, out)
	}
	if out, code = runCmd(t, "tools", "serp", "--url", "http://127.0.0.1:1"); code != 0 || !strings.Contains(out, "fetch failed") {
		t.Errorf("serp fetch-fail: exit %d, output:\n%s", code, out)
	}
	if _, code = runCmd(t, "tools", "serp"); code != 2 {
		t.Errorf("serp no input: exit %d, want 2", code)
	}
	if out, code = runCmd(t, "tools", "serp", "--title", "t", "--json"); code != 0 || !strings.Contains(out, `"report"`) {
		t.Errorf("serp json: exit %d, output:\n%s", code, out)
	}
}

// The render tool's Chrome path is covered by the sitecheck e2e; the CLI's
// non-Chrome branches (fetch failure, non-2xx) render without a browser.
func TestToolsRenderCmdNoChrome(t *testing.T) {
	if out, code := runCmd(t, "tools", "render", "http://127.0.0.1:1"); code != 0 || !strings.Contains(out, "fetch failed") {
		t.Errorf("render unreachable: exit %d, output:\n%s", code, out)
	}
	srv := toolsSite(t, map[string]string{})
	out, code := runCmd(t, "tools", "render", srv.URL+"/gone")
	if code != 0 || !strings.Contains(out, "nothing to diff") {
		t.Errorf("render 404: exit %d, output:\n%s", code, out)
	}
}
