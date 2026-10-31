package finalize

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestDepthThroughRewrittenLinks is the end-to-end guard for url_rewriting ×
// depth: the home page links to `/x?utm_source=a`, which a remove_params rule
// turns into `/x` before it is fetched and stored, and `/x` links on to `/y`.
// The raw `links` row keeps the tracking href, so the finalize depth walk must
// look the hop up under the crawl's rewritten key or `/x` and `/y` end with no
// depth at all. Asserted on the persisted column against a hand-derived oracle.
func TestDepthThroughRewrittenLinks(t *testing.T) {
	bodies := map[string]string{
		"/":  `<a href="/x?utm_source=a">x</a>`,
		"/x": `<a href="/y">y</a>`,
		"/y": `<p>leaf</p>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><head><title>t</title></head><body>%s</body></html>", body)
	}))
	t.Cleanup(srv.Close)

	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
	cfg.URLRewriting.RemoveParams = []string{"utm_source"}

	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true}); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	// Vacuity guard: the stored link row must really carry the RAW tracking href
	// (the crawl rewrote the fetch key, not the link), otherwise the walk never
	// has to translate anything and the test proves nothing.
	links, err := st.LinkRows()
	if err != nil {
		t.Fatal(err)
	}
	sawRaw := false
	for _, l := range links {
		if l.Dst == seed+"x?utm_source=a" {
			sawRaw = true
		}
	}
	if !sawRaw {
		t.Fatalf("fixture did not store the raw tracking href in links — test is vacuous; rows: %+v", links)
	}

	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pages[seed+"x?utm_source=a"]; ok {
		t.Fatalf("page map holds the un-rewritten key %q — the crawl did not apply the rewrite rule", seed+"x?utm_source=a")
	}
	wantDepth := map[string]int{seed: 0, seed + "x": 1, seed + "y": 2}
	for u, d := range wantDepth {
		rec, ok := pages[u]
		if !ok {
			t.Errorf("page %s missing from the store", u)
			continue
		}
		if rec.Depth != d {
			t.Errorf("persisted depth(%s) = %d, want %d (hop through the rewritten link must count)", u, rec.Depth, d)
		}
	}
}
