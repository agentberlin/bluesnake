package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.storeDir = t.TempDir()
	return a
}

// The Tools binding drives the whole hub through the sitecheck dispatcher —
// the robots tester's inline-body path replaced the old App.TestRobots.
func TestToolsAppRunRobots(t *testing.T) {
	ta := NewToolsApp()
	if len(ta.ListTools()) < 7 {
		t.Fatalf("registry = %v", ta.ListTools())
	}
	robots := "User-agent: *\nDisallow: /admin\nAllow: /admin/public\n"
	args := `{"robots_txt":` + strconv.Quote(robots) + `,"urls":["/","/admin","/admin/public"]}`
	out, err := ta.RunTool("robots", "", args)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Report struct {
			Verdicts []struct {
				URL     string `json:"url"`
				Allowed bool   `json:"allowed"`
			} `json:"verdicts"`
		} `json:"report"`
		Findings []struct {
			IssueID  string `json:"issue_id"`
			Severity string `json:"severity"`
			Name     string `json:"name"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	cases := map[string]bool{"/": true, "/admin": false, "/admin/public": true}
	if len(res.Report.Verdicts) != 3 {
		t.Fatalf("verdicts = %+v", res.Report.Verdicts)
	}
	for _, v := range res.Report.Verdicts {
		if want := cases[v.URL]; v.Allowed != want {
			t.Errorf("%s allowed = %v, want %v", v.URL, v.Allowed, want)
		}
	}
	// no Sitemap: directive → the findings strip must carry the catalogue name
	found := false
	for _, f := range res.Findings {
		if f.IssueID == "robots_txt_no_sitemap" {
			found = true
			if f.Severity == "" || f.Name == "" {
				t.Errorf("finding not decorated: %+v", f)
			}
		}
	}
	if !found {
		t.Errorf("findings = %+v, want robots_txt_no_sitemap", res.Findings)
	}

	if _, err := ta.RunTool("nope", "", ""); err == nil {
		t.Error("unknown tool accepted")
	}
}

func TestProfilesRoundTrip(t *testing.T) {
	a := testApp(t)
	names, err := a.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 || names[0] != defaultProfile {
		t.Fatalf("ListProfiles = %v, want %q first", names, defaultProfile)
	}

	// dotted-path set persists and round-trips through the JSON view
	if err := a.SetConfigValues(defaultProfile, map[string]string{
		"speed.max_threads":                  "9",
		"thresholds.non_descriptive_anchors": `["click here","more info"]`,
	}); err != nil {
		t.Fatal(err)
	}
	vals, err := a.GetConfigValues(defaultProfile, []string{"speed.max_threads"})
	if err != nil {
		t.Fatal(err)
	}
	if vals["speed.max_threads"] != "9" {
		t.Errorf("speed.max_threads = %q, want 9", vals["speed.max_threads"])
	}
	js, err := a.GetProfileConfig(defaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"more info"`) {
		t.Errorf("profile JSON missing list value: %s", js)
	}

	// invalid values are rejected by Validate
	if err := a.SetConfigValues(defaultProfile, map[string]string{"robots.mode": `"nonsense"`}); err == nil {
		t.Error("invalid enum accepted")
	}

	if err := a.DuplicateProfile(defaultProfile, "JS rendering"); err != nil {
		t.Fatal(err)
	}
	names, _ = a.ListProfiles()
	if len(names) != 2 {
		t.Fatalf("after duplicate, profiles = %v", names)
	}
	if err := a.DeleteProfile(defaultProfile); err == nil {
		t.Error("default profile deletion should be refused")
	}
}

func TestListCrawlsEmpty(t *testing.T) {
	a := testApp(t)
	crawls, err := a.ListCrawls()
	if err != nil {
		t.Fatal(err)
	}
	if len(crawls) != 0 {
		t.Fatalf("crawls = %v, want empty", crawls)
	}
}

// The overview's site-health strip: per-kind chips rolled up from the stored
// site-check reports (+ the llms.txt audit), fixed order, decorated with the
// worst catalogue severity.
func TestOverviewSiteHealth(t *testing.T) {
	a := testApp(t)
	st, err := store.CreateCrawl(a.storeDir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	aiRep, _ := json.Marshal(map[string]any{
		"site": "https://ex.com", "url": "https://ex.com/", "robots_found": true, "live": false,
		"bots":   []map[string]any{{"name": "GPTBot", "robots_allowed": true}, {"name": "ClaudeBot", "robots_allowed": false, "robots_line": 2, "robots_rule": "Disallow: /"}},
		"caveat": "c",
	})
	robotsRep, _ := json.Marshal(map[string]any{
		"url": "https://ex.com/robots.txt", "status": 200, "found": true, "size_bytes": 120, "groups": 1, "rules": 2,
	})
	for _, rec := range []crawler.SiteCheckRecord{
		{Kind: "ai_bots", Subject: "https://ex.com/", Report: aiRep},
		{Kind: "robots", Subject: "https://ex.com/robots.txt", Report: robotsRep},
	} {
		if err := st.SiteCheck(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.LlmsTxtFile(crawler.LlmsTxtRecord{
		URL: "https://ex.com/llms.txt", Kind: "llms_txt", Status: 200, Found: true, Title: "Ex", Summary: "sum",
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	o, err := a.Overview(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.SiteHealth) != 3 {
		t.Fatalf("siteHealth = %+v, want robots + ai_bots + llms", o.SiteHealth)
	}
	// fixed display order regardless of storage order
	if o.SiteHealth[0].Kind != "robots" || o.SiteHealth[1].Kind != "ai_bots" || o.SiteHealth[2].Kind != "llms_txt" {
		t.Errorf("order = %s, %s, %s", o.SiteHealth[0].Kind, o.SiteHealth[1].Kind, o.SiteHealth[2].Kind)
	}
	ai := o.SiteHealth[1]
	if !strings.Contains(ai.Summary, "1/2 crawlers allowed") || ai.Findings == 0 || ai.Severity != "warning" {
		t.Errorf("ai chip = %+v", ai)
	}
	robots := o.SiteHealth[0]
	// healthy file, but no Sitemap: directive → one opportunity finding
	if !strings.Contains(robots.Summary, "HTTP 200") || robots.Severity != "opportunity" {
		t.Errorf("robots chip = %+v", robots)
	}
}

// The New Crawl form's site-checks selector is absolute like every other
// quick-config field: Auto/All/Off each freeze an explicit override into the
// crawl; only non-form callers (empty value) defer to the profile. "All"
// forces the whole family on, render diff included.
func TestStartRequestSiteChecks(t *testing.T) {
	cases := []struct {
		in         string
		enabled    any
		renderDiff any
	}{
		{"auto", "auto", nil},
		{"all", "always", true},
		{"off", "never", nil},
		{"", nil, nil},
	}
	for _, tt := range cases {
		spec := StartRequest{Mode: "spider", URL: "https://ex.com", SiteChecks: tt.in}.toSpec()
		if got := spec.Config["site_checks.enabled"]; got != tt.enabled {
			t.Errorf("%q: enabled override = %v, want %v", tt.in, got, tt.enabled)
		}
		if got := spec.Config["site_checks.render_diff"]; got != tt.renderDiff {
			t.Errorf("%q: render_diff override = %v, want %v", tt.in, got, tt.renderDiff)
		}
	}
	// a mistyped value flows into config validation and is rejected at enqueue
	a := testApp(t)
	if _, err := a.StartCrawl(StartRequest{Mode: "spider", URL: "https://ex.com", SiteChecks: "nonsense"}); err == nil {
		t.Error("invalid site-checks value accepted")
	}
}

func TestStartCrawlValidation(t *testing.T) {
	a := testApp(t)
	if _, err := a.StartCrawl(StartRequest{Mode: "spider", URL: "not-a-url"}); err == nil {
		t.Error("invalid URL accepted")
	}
	if _, err := a.StartCrawl(StartRequest{Mode: "list"}); err == nil {
		t.Error("empty list accepted")
	}
}
