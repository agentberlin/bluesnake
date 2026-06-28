package crawler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/render"
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

	sink := newCapSink()
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res := runCap(t, c, sink, srv.URL+"/")

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
	// GET XHR/fetch requests made during rendering are recorded as xhr
	// links but never crawled as pages while JS resource crawling is off —
	// SF buckets them as JavaScript resources, and treating them as page
	// links makes Next.js ?_rsc prefetches (fresh token per render) explode
	// the frontier. POSTs are not recorded at all.
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
	if rec := res.Pages[srv.URL+"/api/from-xhr"]; rec != nil {
		t.Errorf("XHR URL must not be crawled with resources.javascript.crawl off: %+v", rec)
	}
	if res.Pages[srv.URL+"/api/posted"] != nil {
		t.Error("POST XHR must not be discovered")
	}
}

// R16: in JS-rendering mode structured data must be extracted from the rendered
// DOM, not the raw body. A JSON-LD block injected by JavaScript (the common
// Webflow/CMS FAQ pattern — zenskar.com lost FAQPage/Question/Answer on 67 blog
// pages this way) is absent from the raw HTML and was previously missed. Types
// present in the raw HTML must still survive.
const jsStructuredPage = `<html><head><title>SD</title>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Organization","name":"Acme","logo":"/l.png","url":"https://acme.example/"}</script>
<script>
  window.addEventListener('DOMContentLoaded', function() {
    var s = document.createElement('script');
    s.type = 'application/ld+json';
    s.textContent = JSON.stringify({"@context":"https://schema.org","@type":"FAQPage","mainEntity":[{"@type":"Question","name":"Q?","acceptedAnswer":{"@type":"Answer","text":"A."}}]});
    document.head.appendChild(s);
  });
</script>
</head><body><h1>h</h1><p>hello world</p></body></html>`

func TestRenderedStructuredDataFromDOM(t *testing.T) {
	cfg := config.Default()
	cfg.Rendering.Mode = "javascript"
	cfg.Rendering.AjaxTimeoutSec = 1
	cfg.Extraction.StructuredData.JSONLD = true // structured-data extraction must be on
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found; skipping rendering integration test")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, jsStructuredPage)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sink := newCapSink()
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res := runCap(t, c, sink, srv.URL+"/")
	root := res.Pages[srv.URL+"/"]
	if root == nil || root.StructuredData == nil {
		t.Fatalf("root/StructuredData missing: %+v", root)
	}
	have := map[string]bool{}
	for _, ty := range root.StructuredData.Types {
		have[ty] = true
	}
	// JS-injected FAQ types — the regression this fix prevents
	for _, want := range []string{"FAQPage", "Question", "Answer"} {
		if !have[want] {
			t.Errorf("rendered structured data missing JS-injected %q; got %v", want, root.StructuredData.Types)
		}
	}
	// raw-HTML type must still be present
	if !have["Organization"] {
		t.Errorf("rendered structured data dropped raw-HTML Organization; got %v", root.StructuredData.Types)
	}
	// R18: the JS-injected types are flagged as render-only; the raw type is not
	if root.JSDiff == nil {
		t.Fatal("JSDiff missing")
	}
	jsOnly := map[string]bool{}
	for _, ty := range root.JSDiff.StructuredJSOnly {
		jsOnly[ty] = true
	}
	for _, want := range []string{"FAQPage", "Question", "Answer"} {
		if !jsOnly[want] {
			t.Errorf("StructuredJSOnly missing JS-injected %q; got %v", want, root.JSDiff.StructuredJSOnly)
		}
	}
	if jsOnly["Organization"] {
		t.Errorf("raw-HTML Organization wrongly flagged as render-only; got %v", root.JSDiff.StructuredJSOnly)
	}
}
