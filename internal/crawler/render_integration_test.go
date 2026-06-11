package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/parse"
	"github.com/hhsecond/acrawler/internal/render"
)

const jsPage = `<html><head><title>Raw Title</title>
<meta name="robots" content="noindex">
<script>
  document.title = "JS Title";
  document.querySelector('meta[name=robots]').remove();
  var link = document.createElement('link');
  link.rel = 'canonical';
  link.href = '/canonical-from-js';
  document.head.appendChild(link);
  window.addEventListener('DOMContentLoaded', function() {
    var a = document.createElement('a');
    a.href = '/js-only';
    a.textContent = 'js link';
    document.body.appendChild(a);
    var p = document.createElement('p');
    p.textContent = 'content injected by javascript here';
    document.body.appendChild(p);
  });
  fetch('/api/from-xhr');
  fetch('/api/posted', {method: 'POST'});
</script>
</head><body><h1>raw</h1><h2>s</h2><a href="/plain">plain</a></body></html>`

func TestCrawlerRenderingIntegration(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping rendering integration test")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, jsPage)
		default:
			fmt.Fprint(w, "<html><head><title>A target page with a title</title></head><body><h1>t</h1><h2>s</h2><p>target</p></body></html>")
		}
	})
	srv := httptest.NewServer(mux)
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

	root := res.Pages[srv.URL+"/"]
	if root == nil || root.JSDiff == nil {
		t.Fatalf("root record/JSDiff missing: %+v", root)
	}
	if !root.JSDiff.TitleChanged || root.JSDiff.RenderedTitle != "JS Title" {
		t.Errorf("title diff = %+v", root.JSDiff)
	}
	if root.JSDiff.JSLinks == 0 {
		t.Error("rendered-only link not counted")
	}
	if root.JSDiff.WordCountChange <= 0 {
		t.Errorf("word count change = %d, want > 0", root.JSDiff.WordCountChange)
	}
	if !root.JSDiff.NoindexOnlyRaw {
		t.Error("noindex removed by JS not detected")
	}
	if !root.JSDiff.CanonicalChanged {
		t.Error("JS-injected canonical not detected")
	}
	// the JS-injected link must be discovered and crawled
	if rec := res.Pages[srv.URL+"/js-only"]; rec == nil || rec.State != StateCrawled {
		t.Errorf("JS-only link not crawled: %+v", rec)
	}
	// link origin recorded
	var foundRendered bool
	for _, l := range root.Facts.Links {
		if l.URL == srv.URL+"/js-only" && l.Origin == "rendered" {
			foundRendered = true
		}
	}
	if !foundRendered {
		t.Error("rendered-only link missing origin=rendered")
	}
	// GET XHR/fetch requests made during rendering are discovered URLs
	// (Screaming Frog parity); POSTs are not crawl targets
	var xhrLink bool
	for _, l := range root.Facts.Links {
		if l.Type == parse.XHR && l.URL == srv.URL+"/api/from-xhr" {
			xhrLink = true
			if l.Origin != "xhr" {
				t.Errorf("xhr link origin = %q, want %q", l.Origin, "xhr")
			}
		}
	}
	if !xhrLink {
		t.Error("XHR request not recorded as a link")
	}
	if rec := res.Pages[srv.URL+"/api/from-xhr"]; rec == nil || rec.State != StateCrawled {
		t.Errorf("XHR-discovered URL not crawled: %+v", rec)
	}
	if res.Pages[srv.URL+"/api/posted"] != nil {
		t.Error("POST XHR must not be discovered")
	}
}
