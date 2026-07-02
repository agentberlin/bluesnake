package finalize

// Stream-and-drop parity tests (MEMORY-SCALING.md §5.4, §13.6). These pin the
// behaviour that must survive freeing per-page records from RAM: the store stays
// the authority and every aggregate a fresh crawl used to compute in the live
// result map must still land on disk.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

const contentMarker = "uniquecontentmarker alpha bravo charlie delta echo foxtrot golf hotel"

// crawlOnePage runs a single-page crawl whose body carries distinctive content
// text into a fresh store, returning the store, the live result, and the URL.
func crawlOnePage(t *testing.T) (*store.Crawl, *crawler.Result, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><h1>Heading</h1><p>%s</p></body></html>`, contentMarker)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg := config.Default()
	st, err := store.CreateCrawl(dir, []string{srv.URL + "/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return st, res, srv.URL + "/"
}

// TestContentTextRoundTripsThroughStore (SD-05/06/07) is the durable guard for
// the Phase-0 quick win: the crawler frees the in-RAM ContentText once it has
// been persisted (the bulk of a PageRecord's footprint — the records themselves
// are no longer retained at all post stream-and-drop, pinned by the crawler's
// TestNoPageRecordRetainedAfterRun), but the store must round-trip it intact so
// near-dup / lorem / soft-404 / compare keep reading it off LoadPages.
func TestContentTextRoundTripsThroughStore(t *testing.T) {
	st, _, url := crawlOnePage(t)

	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	rec := pages[url]
	if rec == nil || rec.Facts == nil {
		t.Fatalf("page %s missing or has no facts after LoadPages", url)
	}
	if !strings.Contains(rec.Facts.ContentText, "uniquecontentmarker") {
		t.Errorf("LoadPages ContentText = %q, want it to contain the marker", rec.Facts.ContentText)
	}
}

// TestStreamedContentIssuesMatchWholeMapEvaluate pins the Phase-2 lite-map +
// streamed-ContentText finalize path: the two ContentText-dependent issue checks
// (lorem/soft-404) it persists must be byte-identical to running issues.Evaluate
// over a full ContentText-bearing map — proving the peak-cutting split preserves
// the issue set exactly.
func TestStreamedContentIssuesMatchWholeMapEvaluate(t *testing.T) {
	bodies := map[string]string{
		"/":        `<a href="/lorem">x</a><a href="/missing">y</a><a href="/clean">z</a>`,
		"/lorem":   `<html><body><p>Lorem ipsum dolor sit amet, plenty of additional words here so the page is not flagged as thin content at all.</p></body></html>`,
		"/missing": `<html><body><p>Sorry, this page not found anywhere on our website, please try another link from the homepage instead today.</p></body></html>`,
		"/clean":   `<html><body><p>A perfectly ordinary readable page of prose with nothing alarming present and a healthy amount of words to read.</p></body></html>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
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

	// Oracle: the whole-map issues.Evaluate over the full ContentText-bearing map.
	full, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	wantLorem, wantSoft := []string{}, []string{}
	for _, o := range issues.Evaluate(full, cfg) {
		switch o.IssueID {
		case "content_lorem_ipsum":
			wantLorem = append(wantLorem, o.URL)
		case "content_soft_404":
			wantSoft = append(wantSoft, o.URL)
		}
	}

	// What the streamed finalize path actually persisted.
	gotLorem, err := st.IssueURLs("content_lorem_ipsum")
	if err != nil {
		t.Fatal(err)
	}
	gotSoft, err := st.IssueURLs("content_soft_404")
	if err != nil {
		t.Fatal(err)
	}

	sameSet := func(name string, got, want []string) {
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: streamed = %v, whole-map oracle = %v", name, got, want)
		}
	}
	sameSet("content_lorem_ipsum", gotLorem, wantLorem)
	sameSet("content_soft_404", gotSoft, wantSoft)

	// Sanity: the checks actually fired on the intended pages (not a vacuous pass).
	base := strings.TrimSuffix(seed, "/")
	if len(gotLorem) != 1 || gotLorem[0] != base+"/lorem" {
		t.Errorf("lorem fired on %v, want only /lorem", gotLorem)
	}
	if len(gotSoft) != 1 || gotSoft[0] != base+"/missing" {
		t.Errorf("soft_404 fired on %v, want only /missing", gotSoft)
	}
}

// graphSite serves a small link graph with known inlinks/depth/discovered-from:
//
//	/  -> /a, /b      (seed)
//	/a -> /b, /c
//	/b -> /
//	/c                (leaf)
//
// depths: /=0 /a=1 /b=1 /c=2; inlinks: /a=1 /b=2 /c=1 /=1; discovered_from:
// /a=/ /b=/ /c=/a /="" (seed, lock).
func graphSite(t *testing.T) *httptest.Server {
	t.Helper()
	page := func(links ...string) string {
		var b strings.Builder
		b.WriteString("<html><body>")
		for _, l := range links {
			fmt.Fprintf(&b, `<a href="%s">x</a>`, l)
		}
		b.WriteString("</body></html>")
		return b.String()
	}
	bodies := map[string]string{
		"/":  page("/a", "/b"),
		"/a": page("/b", "/c"),
		"/b": page("/"),
		"/c": "<html><body><p>leaf</p></body></html>",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawlGraph runs a fresh crawl of graphSite through the full finalize path and
// returns the store + the seed URL, so callers can LoadPages and assert the
// persisted aggregates.
func crawlGraph(t *testing.T) (*store.Crawl, *crawler.Crawler, string) {
	t.Helper()
	srv := graphSite(t)
	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
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
	// Finalize exactly as the CLI/queue do for a fresh completed crawl: Seeds
	// supplied, Resumed=false.
	if _, err := Crawl(c, st, res, Params{
		StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return st, c, seed
}

// TestFreshCrawlPersistsInlinksDiscoveredFromDepth (SD-01/02/03) is the load-
// bearing guard for stream-and-drop: once the live Result no longer carries the
// page map, a fresh crawl must still persist exact inlinks, first-wins +
// seed-locked discovered_from, and shortest-path depth — all derived through the
// store-backed finalize path rather than the dropped in-RAM map.
func TestFreshCrawlPersistsInlinksDiscoveredFromDepth(t *testing.T) {
	st, _, seed := crawlGraph(t)
	base := strings.TrimSuffix(seed, "/")

	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for path := range map[string]bool{"/": true, "/a": true, "/b": true, "/c": true} {
		if pages[base+path] == nil {
			t.Fatalf("page %s missing after crawl", path)
		}
	}

	wantDepth := map[string]int{"/": 0, "/a": 1, "/b": 1, "/c": 2}
	for path, d := range wantDepth {
		if got := pages[base+path].Depth; got != d {
			t.Errorf("depth %s = %d, want %d", path, got, d)
		}
	}
	wantInlinks := map[string]int{"/": 1, "/a": 1, "/b": 2, "/c": 1}
	for path, n := range wantInlinks {
		if got := pages[base+path].Inlinks; got != n {
			t.Errorf("inlinks %s = %d, want %d", path, got, n)
		}
	}
	wantFrom := map[string]string{"/": "", "/a": base + "/", "/b": base + "/", "/c": base + "/a"}
	for path, src := range wantFrom {
		if got := pages[base+path].DiscoveredFrom; got != src {
			t.Errorf("discovered_from %s = %q, want %q", path, got, src)
		}
	}
}
