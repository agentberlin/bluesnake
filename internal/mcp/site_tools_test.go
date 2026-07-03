package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListToolsRegistry(t *testing.T) {
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "list_tools", map[string]any{})
	if isErr {
		t.Fatalf("list_tools errored: %s", out)
	}
	for _, name := range []string{"robots", "sitemap", "aibots", "render", "llms", "structured", "serp"} {
		if !strings.Contains(out, `"`+name+`"`) {
			t.Errorf("list_tools output missing %s:\n%s", name, out)
		}
	}
}

func TestRunToolRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /private/\nSitemap: /s.xml\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{
		"tool": "robots", "target": srv.URL,
		"args": map[string]any{"urls": []string{srv.URL + "/private/x"}, "user_agent": "somebot"},
	})
	if isErr {
		t.Fatalf("run_tool errored: %s", out)
	}
	var res struct {
		Report struct {
			Found    bool `json:"found"`
			Verdicts []struct {
				Allowed bool `json:"allowed"`
				Line    int  `json:"line"`
			} `json:"verdicts"`
		} `json:"report"`
		Findings []struct {
			IssueID string `json:"issue_id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	if !res.Report.Found || len(res.Report.Verdicts) != 1 || res.Report.Verdicts[0].Allowed {
		t.Fatalf("report = %+v", res.Report)
	}
	if res.Findings == nil {
		t.Error("findings must be present (empty array, not null)")
	}
}

func TestRunToolInlineRobotsBody(t *testing.T) {
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{
		"tool": "robots", "target": "",
		"args": map[string]any{
			"robots_txt": "User-agent: *\nDisallow: /x\n",
			"urls":       []string{"https://ex.com/x"},
		},
	})
	if isErr {
		t.Fatalf("run_tool errored: %s", out)
	}
	if !strings.Contains(out, `"allowed": false`) {
		t.Errorf("inline body verdict missing:\n%s", out)
	}
}

func TestRunToolErrors(t *testing.T) {
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{"tool": "nope", "target": "x"})
	if !isErr || !strings.Contains(out, "available:") {
		t.Errorf("unknown tool result = %q (isErr=%v), want the available list", out, isErr)
	}
	out, isErr = callTool(t, s, "run_tool", map[string]any{
		"tool": "sitemap", "target": "https://ex.com",
		"args": map[string]any{"check_entires": 3}, // typo must be caught
	})
	if !isErr || !strings.Contains(out, "list_tools") {
		t.Errorf("unknown arg result = %q (isErr=%v), want a self-correction hint", out, isErr)
	}
}

func TestRunToolStructured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product","name":"W","offers":{"@type":"Offer","price":"1","priceCurrency":"USD"}}
</script></head><body></body></html>`)
	}))
	defer srv.Close()
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{"tool": "structured", "target": srv.URL})
	if isErr {
		t.Fatalf("run_tool errored: %s", out)
	}
	if !strings.Contains(out, "structured_validation_error") {
		t.Errorf("validation finding missing:\n%s", out)
	}
}

func TestRunToolSerp(t *testing.T) {
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{
		"tool": "serp", "target": "",
		"args": map[string]any{"title": strings.Repeat("Wide Widget Warehouse ", 5)},
	})
	if isErr {
		t.Fatalf("run_tool errored: %s", out)
	}
	if !strings.Contains(out, "title_over_pixels") || !strings.Contains(out, `"truncated"`) {
		t.Errorf("serp output missing verdicts/truncation:\n%s", out)
	}
}

func TestRunToolLlms(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	s := NewServer(&fakeBackend{}, "test")
	out, isErr := callTool(t, s, "run_tool", map[string]any{"tool": "llms", "target": srv.URL})
	if isErr {
		t.Fatalf("run_tool errored: %s", out)
	}
	if !strings.Contains(out, "llms_txt_missing") {
		t.Errorf("llms findings missing:\n%s", out)
	}
}
