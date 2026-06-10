package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// A page that settles immediately must release the tab long before the AJAX
// timeout — the timeout is a cap, not a fixed wait.
func TestRenderReturnsEarlyWhenPageSettles(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.AjaxTimeoutSec = 10
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
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed >= 10*time.Second {
		t.Errorf("render took %s — waited out the full AJAX timeout instead of settling early", elapsed)
	}
	if !strings.Contains(res.HTML, "content injected by javascript") {
		t.Error("early-settled DOM missing JS-injected content")
	}
}

// Mirror of the real-world pathology that motivated settle detection: a
// widget iframe whose response never completes and a fetch() stream that
// stays open forever. Neither must hold the snapshot hostage — the load
// event never fires here, and the network never goes idle.
func TestRenderSettlesDespiteStuckStreams(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.AjaxTimeoutSec = 10

	mux := http.NewServeMux()
	hang := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		fmt.Fprint(w, "<html><body>widget")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never finishes until the tab closes
	}
	mux.HandleFunc("/widget", hang)
	mux.HandleFunc("/stream", hang)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><script>
			window.addEventListener('DOMContentLoaded', function() {
				var p = document.createElement('p');
				p.textContent = 'settled content';
				document.body.appendChild(p);
				fetch('/stream'); // long-lived request, never completes
			});
		</script></head><body><iframe src="/widget"></iframe></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 6*time.Second {
		t.Errorf("render took %s with permanently-open requests — should settle in ~2s, cap is 10s", elapsed)
	}
	if !strings.Contains(res.HTML, "settled content") {
		t.Error("settled DOM missing JS-injected content")
	}
}

// Analytics-style chatter: a ping every 300ms keeps the wire from ever being
// quiet, but the DOM is static — the DOM-stability probe must settle the page.
func TestRenderSettlesDespiteNetworkChatter(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.AjaxTimeoutSec = 10

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><script>
			window.addEventListener('DOMContentLoaded', function() {
				var p = document.createElement('p');
				p.textContent = 'settled content';
				document.body.appendChild(p);
				setInterval(function(){ fetch('/ping'); }, 300);
			});
		</script></head><body><h1>x</h1></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 6*time.Second {
		t.Errorf("render took %s under constant beacon chatter — DOM stability should settle it in ~2s", elapsed)
	}
	if !strings.Contains(res.HTML, "settled content") {
		t.Error("settled DOM missing JS-injected content")
	}
}
