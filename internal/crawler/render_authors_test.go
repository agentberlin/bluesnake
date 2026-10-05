package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// With rendering on, a page's author evidence is read from the rendered DOM,
// as its structured data is: a byline a script injects is what a reader (and
// an answer engine that renders) sees, and the raw body's evidence is in the
// rendered DOM too unless a script removed it.
func TestRenderedCrawlReadsAuthorsFromTheRenderedDOM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><meta name="author" content="Raw Only"></head><body><div id="app"></div></body></html>`))
	}))
	t.Cleanup(srv.Close)
	fr := &fakeRenderer{html: `<html><head><meta name="author" content="Jane Doe"></head>
		<body><div id="app"><span class="byline">By Jane Doe</span></div></body></html>`}
	sink := newCapSink()
	c := jsCrawler(t, 1, limiter.New(0, 1, 1), fr, sink)
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	rec := sink.pages[srv.URL+"/"]
	if rec == nil || rec.Facts == nil {
		t.Fatalf("no facts stored for the seed: %+v", sink.pages)
	}
	want := []parse.Author{
		{Source: parse.AuthorMeta, Name: "Jane Doe"},
		{Source: parse.AuthorByline, Name: "By Jane Doe"},
	}
	if !slices.Equal(rec.Facts.Authors, want) {
		t.Errorf("authors = %+v, want the rendered DOM's %+v", rec.Facts.Authors, want)
	}
}
