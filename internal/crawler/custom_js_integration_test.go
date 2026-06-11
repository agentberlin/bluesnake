package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/render"
)

func TestCrawlerCustomJSIntegration(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping custom JS integration test")
	}
	dir := t.TempDir()
	writeJS := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg.CustomJS = []config.CustomJS{
		{Name: "get-title", Type: "extraction", File: writeJS("get-title.js", `document.title`), ContentTypes: []string{"text/html"}},
		{Name: "pdf-only", Type: "extraction", File: writeJS("pdf-only.js", `"must not be stored for html"`), ContentTypes: []string{"application/pdf"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Custom JS Page</title></head><body><h1>t</h1><h2>s</h2><p>body</p></body></html>`)
	}))
	defer srv.Close()

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	rec := res.Pages[srv.URL+"/"]
	if rec == nil {
		t.Fatalf("root record missing: %+v", res.Pages)
	}
	var found bool
	for _, cr := range rec.CustomResults {
		switch cr.Name {
		case "get-title":
			found = true
			if cr.Kind != "js" {
				t.Errorf("get-title kind = %q, want %q", cr.Kind, "js")
			}
			if cr.Value != "Custom JS Page" {
				t.Errorf("get-title value = %q, want %q", cr.Value, "Custom JS Page")
			}
		case "pdf-only":
			// content_types ["application/pdf"] must not match a text/html page.
			t.Errorf("pdf-only stored for a text/html page: %+v", cr)
		}
	}
	if !found {
		t.Errorf("custom result get-title missing: %+v", rec.CustomResults)
	}
}

// custom JS results and custom search/extraction results must coexist: the
// crawler appends the search/extraction results rather than overwriting the
// JS ones already on the record.
func TestCrawlerCustomJSCoexistsWithSearch(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping custom JS coexistence test")
	}
	dir := t.TempDir()
	jsFile := filepath.Join(dir, "get-title.js")
	if err := os.WriteFile(jsFile, []byte(`document.title`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.CustomJS = []config.CustomJS{{Name: "get-title", Type: "extraction", File: jsFile}}
	cfg.CustomSearch = []config.CustomSearch{{Name: "has-body", Mode: "contains", Pattern: "body"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Coexist Page</title></head><body><h1>t</h1><h2>s</h2><p>body</p></body></html>`)
	}))
	defer srv.Close()

	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	rec := res.Pages[srv.URL+"/"]
	kinds := map[string]bool{}
	for _, cr := range rec.CustomResults {
		kinds[cr.Kind] = true
	}
	if !kinds["js"] {
		t.Errorf("custom JS result lost when custom_search is also configured: %+v", rec.CustomResults)
	}
	if !kinds["search"] {
		t.Errorf("custom search result missing: %+v", rec.CustomResults)
	}
}
