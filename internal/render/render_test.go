package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
)

func requireChrome(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	if ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping rendering tests")
	}
	return cfg
}

const jsPage = `<html><head><title>Raw Title</title>
<script>
  document.title = "JS Title";
  window.addEventListener('DOMContentLoaded', function() {
    var a = document.createElement('a');
    a.href = '/js-only';
    a.textContent = 'js link';
    document.body.appendChild(a);
    var p = document.createElement('p');
    p.textContent = 'content injected by javascript here';
    document.body.appendChild(p);
  });
</script>
</head><body><h1>raw</h1><h2>s</h2><a href="/plain">plain</a></body></html>`

func TestRenderReturnsRenderedDOM(t *testing.T) {
	cfg := requireChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, jsPage)
	}))
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.HTML, "content injected by javascript") {
		t.Error("rendered DOM missing JS-injected content")
	}
	if !strings.Contains(res.HTML, "/js-only") {
		t.Error("rendered DOM missing JS-injected link")
	}
}

func TestRenderConsoleErrorsAndScreenshot(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.JSErrorReporting = true
	cfg.Rendering.Screenshots = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><script>console.error("boom from js");</script></head><body><p>x</p></body></html>`)
	}))
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ConsoleErrors) == 0 || !strings.Contains(res.ConsoleErrors[0], "boom") {
		t.Errorf("console errors = %v", res.ConsoleErrors)
	}
	if len(res.Screenshot) == 0 {
		t.Error("screenshot missing")
	}
}

func TestRenderUnreachableURL(t *testing.T) {
	cfg := requireChrome(t)
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Render(context.Background(), "http://127.0.0.1:1/nothing"); err == nil {
		t.Error("unreachable URL must error")
	}
}

func TestChromePathOverride(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.ChromePath = "/custom/chrome"
	if ChromePath(cfg) != "/custom/chrome" {
		t.Error("config chrome_path must win")
	}
}
