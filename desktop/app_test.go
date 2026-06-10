package main

import (
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.storeDir = t.TempDir()
	return a
}

func TestTestRobotsVerdicts(t *testing.T) {
	a := testApp(t)
	robots := strings.Join([]string{
		"User-agent: *",
		"Disallow: /admin",
		"Allow: /admin/public",
	}, "\n")
	got := a.TestRobots(robots, "acrawler", []string{"/", "/admin", "/admin/public", ""})
	if len(got) != 3 {
		t.Fatalf("verdicts = %d, want 3 (blank lines skipped)", len(got))
	}
	cases := map[string]bool{"/": true, "/admin": false, "/admin/public": true}
	for _, v := range got {
		if want := cases[v.URL]; v.Allowed != want {
			t.Errorf("%s allowed = %v, want %v (rule %q line %d)", v.URL, v.Allowed, want, v.Rule, v.Line)
		}
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

func TestStartCrawlValidation(t *testing.T) {
	a := testApp(t)
	if _, err := a.StartCrawl(StartRequest{Mode: "spider", URL: "not-a-url"}); err == nil {
		t.Error("invalid URL accepted")
	}
	if _, err := a.StartCrawl(StartRequest{Mode: "list"}); err == nil {
		t.Error("empty list accepted")
	}
}
