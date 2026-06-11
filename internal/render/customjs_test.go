package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

const customJSPage = `<html><head><title>Raw Title</title></head><body><h1>x</h1><p>body</p></body></html>`

func customJSServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, customJSPage)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeSnippet(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func findJSResult(results []JSResult, name string) (JSResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return JSResult{}, false
}

func TestRenderCustomJSActionThenExtraction(t *testing.T) {
	cfg := requireChrome(t)
	dir := t.TempDir()
	cfg.CustomJS = []config.CustomJS{
		{Name: "set-title", Type: "action", File: writeSnippet(t, dir, "set-title.js", `document.title = "changed by action"`)},
		{Name: "get-title", Type: "extraction", File: writeSnippet(t, dir, "get-title.js", `document.title`)},
	}
	srv := customJSServer(t)

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := findJSResult(res.JSResults, "get-title")
	if !ok {
		t.Fatalf("JSResults missing get-title: %+v", res.JSResults)
	}
	if got.Value != "changed by action" {
		t.Errorf("get-title = %q, want %q (actions must run before extractions)", got.Value, "changed by action")
	}
	if _, ok := findJSResult(res.JSResults, "set-title"); ok {
		t.Error("action snippet result must be discarded, not appended to JSResults")
	}
}

func TestRenderCustomJSValueEncoding(t *testing.T) {
	cfg := requireChrome(t)
	dir := t.TempDir()
	cases := []struct {
		name, src, want string
	}{
		{"string-result", `document.title`, "Raw Title"},
		{"number-result", `6*7`, "42"},
		{"bool-result", `1 === 1`, "true"},
		{"object-result", `({a:1})`, `{"a":1}`},
	}
	for _, c := range cases {
		cfg.CustomJS = append(cfg.CustomJS, config.CustomJS{
			Name: c.name, Type: "extraction", File: writeSnippet(t, dir, c.name+".js", c.src),
		})
	}
	srv := customJSServer(t)

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got, ok := findJSResult(res.JSResults, c.name)
		if !ok {
			t.Errorf("JSResults missing %s: %+v", c.name, res.JSResults)
			continue
		}
		if got.Value != c.want {
			t.Errorf("%s = %q, want %q", c.name, got.Value, c.want)
		}
	}
}

func TestRenderCustomJSThrowingSnippet(t *testing.T) {
	cfg := requireChrome(t)
	dir := t.TempDir()
	cfg.CustomJS = []config.CustomJS{
		{Name: "exploder", Type: "extraction", File: writeSnippet(t, dir, "exploder.js", `(() => { throw new Error("snippet exploded") })()`)},
		{Name: "after-exploder", Type: "extraction", File: writeSnippet(t, dir, "after-exploder.js", `"still ran"`)},
	}
	srv := customJSServer(t)

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := findJSResult(res.JSResults, "exploder")
	if !ok {
		t.Fatalf("JSResults missing exploder: %+v", res.JSResults)
	}
	if !strings.HasPrefix(got.Value, "error:") {
		t.Errorf("throwing snippet value = %q, want prefix %q", got.Value, "error:")
	}
	// One bad snippet must not abort the rest of the list.
	after, ok := findJSResult(res.JSResults, "after-exploder")
	if !ok {
		t.Fatalf("JSResults missing after-exploder: %+v", res.JSResults)
	}
	if after.Value != "still ran" {
		t.Errorf("after-exploder = %q, want %q", after.Value, "still ran")
	}
}

// New reads every snippet file at construction and must fail fast on a
// missing one. ChromePath is overridden with a path that is never launched
// (the allocator starts no browser during New), so this case runs even on
// machines without Chrome.
func TestNewCustomJSMissingFile(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.ChromePath = "/custom/chrome"
	cfg.CustomJS = []config.CustomJS{
		{Name: "ghost-snippet", Type: "extraction", File: filepath.Join(t.TempDir(), "missing.js")},
	}
	r, err := New(cfg)
	if err == nil {
		r.Close()
		t.Fatal("New with a missing snippet file must error")
	}
	if !strings.Contains(err.Error(), "ghost-snippet") {
		t.Errorf("error %q does not mention the snippet name ghost-snippet", err)
	}
}
