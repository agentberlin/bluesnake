package mcp

import (
	"strings"
	"testing"
)

// Spec() owns the setup→ConfigSource mapping, including the #88 default: a
// spider start that names neither a setup nor a profile reuses the site's
// last-crawl setup. List mode never defaults to "last" (a list audit has no
// single site), and explicit choices map through verbatim.
func TestStartRequestSetupMapping(t *testing.T) {
	cases := []struct {
		name string
		req  StartRequest
		want string
	}{
		{"spider default", StartRequest{URL: "https://e.com/"}, "last"},
		{"explicit last", StartRequest{URL: "https://e.com/", Setup: "last"}, "last"},
		{"app settings", StartRequest{URL: "https://e.com/", Setup: "app_settings"}, ""},
		{"profile", StartRequest{URL: "https://e.com/", Profile: "Slow"}, ""},
		{"list default", StartRequest{Mode: "list", URLs: []string{"https://e.com/a"}}, ""},
		{"explicit last stays for list (rejected downstream)", StartRequest{Mode: "list", Setup: "last"}, "last"},
	}
	for _, tt := range cases {
		if got := tt.req.Spec().ConfigSource; got != tt.want {
			t.Errorf("%s: ConfigSource = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestStartCrawlSetupParamValidation(t *testing.T) {
	s, _ := projectServer(t)

	if text, isErr := callTool(t, s, "start_crawl",
		map[string]any{"url": "https://e.com/", "setup": "bogus"}); !isErr {
		t.Errorf("unknown setup accepted: %s", text)
	}
	if text, isErr := callTool(t, s, "start_crawl",
		map[string]any{"url": "https://e.com/", "setup": "last", "profile": "Slow"}); !isErr {
		t.Errorf("setup+profile accepted: %s", text)
	}
}

// The start response says which base config the crawl resolved, so an agent
// knows whether the site's remembered setup or the app settings ran.
func TestStartCrawlReportsBaseConfig(t *testing.T) {
	s, _ := projectServer(t)

	text, isErr := callTool(t, s, "start_crawl", map[string]any{"url": "https://e.com/"})
	if isErr {
		t.Fatalf("start_crawl errored: %s", text)
	}
	if !strings.Contains(text, "app settings") {
		t.Errorf("response must name the resolved base for a never-crawled site, got: %s", text)
	}
}
