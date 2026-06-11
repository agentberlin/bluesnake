package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A page with no scripts and no subresources: it settles the instant it loads,
// so the adaptive strategy snapshots almost immediately while the fixed
// strategy must still sleep out the full AJAX timeout.
const staticPage = `<html><head><title>Static Title</title></head><body><p>static body content</p></body></html>`

func staticServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, staticPage)
	}))
}

// With wait_strategy=fixed the AJAX timeout is a fixed wait, not a cap:
// after the load event the renderer sleeps the full rendering.ajax_timeout_sec
// before snapshotting, so compare workflows get deterministic snapshots.
func TestRenderFixedWaitStrategySleepsFullTimeout(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "fixed"
	cfg.Rendering.AjaxTimeoutSec = 1
	srv := staticServer()
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
	if elapsed < 1*time.Second {
		t.Errorf("render took %s — fixed strategy must sleep the full 1s AJAX timeout before snapshotting", elapsed)
	}
	if !strings.Contains(res.HTML, "static body content") {
		t.Error("fixed-strategy snapshot missing page content")
	}
}

// Contrast guard for the test above: the same instantly-settled page under
// wait_strategy=adaptive must release the tab well before the AJAX timeout
// (mirrors the bounds of TestRenderReturnsEarlyWhenPageSettles).
func TestRenderAdaptiveWaitStrategyStillSettlesEarly(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "adaptive"
	cfg.Rendering.AjaxTimeoutSec = 10
	srv := staticServer()
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
		t.Errorf("render took %s — adaptive strategy must settle a static page well before the 10s cap", elapsed)
	}
	if !strings.Contains(res.HTML, "static body content") {
		t.Error("adaptive-strategy snapshot missing page content")
	}
}

// The fixed strategy waits for the load event first, so DOMContentLoaded
// scripts have run by snapshot time: JS-injected DOM must still be captured.
func TestRenderFixedWaitStrategyCapturesJSInjectedDOM(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "fixed"
	cfg.Rendering.AjaxTimeoutSec = 1
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
	if !strings.Contains(res.HTML, "JS Title") {
		t.Error("fixed-strategy snapshot missing JS-rewritten title")
	}
	if !strings.Contains(res.HTML, "content injected by javascript") {
		t.Error("fixed-strategy snapshot missing JS-injected content")
	}
}
