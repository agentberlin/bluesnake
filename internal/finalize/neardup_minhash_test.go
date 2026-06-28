package finalize

// FIN-NDSIG (MEMORY-SCALING.md §5.5, EC19): the near-dup minhash column. A
// near-dup-enabled crawl precomputes each page's signature at crawl time and
// persists it (pages.minhash), so finalize runs near-dup over the ContentText-
// free lite map and never reloads the page bodies. These tests pin: (1) the
// signatures round-trip through the store; (2) near-dup over the column path is
// IDENTICAL to the ContentText fallback path; (3) finalize fires near-dup off
// the lite map; (4) a near-dup-OFF crawl leaves the column empty.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// nearDupSite serves two ~near-identical long pages (/a, /b differ by a couple
// of words) plus an unrelated page /c — so near-dup fires on {/a,/b} only.
func nearDupSite(t *testing.T) *httptest.Server {
	t.Helper()
	words := func(prefix string, n int) []string {
		w := make([]string, n)
		for i := range w {
			w[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return w
	}
	base := words("alpha", 200)
	bb := append([]string(nil), base...)
	bb[100], bb[101] = "swappedx", "swappedy"
	body := func(ws []string) string {
		return "<html><head><title>t</title></head><body><p>" + strings.Join(ws, " ") + "</p></body></html>"
	}
	bodies := map[string]string{
		"/":  `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`,
		"/a": body(base),
		"/b": body(bb),
		"/c": body(words("zeta", 200)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawlNearDup crawls nearDupSite with near-dup enabled/disabled and returns the
// store (finalized).
func crawlNearDup(t *testing.T, enabled bool) (*store.Crawl, string) {
	t.Helper()
	srv := nearDupSite(t)
	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Analysis.NearDuplicates = enabled
	cfg.Content.NearDuplicates.Enabled = enabled
	cfg.Content.NearDuplicates.Threshold = 90
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
	return st, seed
}

// TestNearDupMinhashColumnPersisted: a near-dup-enabled crawl stores a signature
// per content page, and the lite (ContentText-free) map carries it.
func TestNearDupMinhashColumnPersisted(t *testing.T) {
	st, seed := crawlNearDup(t, true)
	base := strings.TrimSuffix(seed, "/")

	lite, err := st.LoadPagesLite()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/a", "/b", "/c"} {
		rec := lite[base+p]
		if rec == nil {
			t.Fatalf("%s missing", p)
		}
		if rec.Facts.ContentText != "" {
			t.Errorf("%s lite ContentText not stripped", p)
		}
		if len(rec.Minhash) == 0 {
			t.Errorf("%s has no persisted minhash signature", p)
		}
	}
}

// TestNearDupColumnEqualsContentTextPath proves near-dup over the precomputed
// column (the lite map) is byte-identical to the ContentText fallback path (the
// same map with the column cleared) — the cutover-safety parity gate.
func TestNearDupColumnEqualsContentTextPath(t *testing.T) {
	st, _ := crawlNearDup(t, true)
	cfg := config.Default()
	cfg.Analysis.NearDuplicates = true
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 90

	// Column path: lite map (no ContentText, has signatures).
	lite, err := st.LoadPagesLite()
	if err != nil {
		t.Fatal(err)
	}
	links, _ := st.LinkRows()
	colRes := analyze.Run(lite, nil, nil, cfg, analyze.WithLinks(links))

	// Fallback path: full map (has ContentText) with the signatures cleared, so
	// analyze must recompute from the bodies.
	full, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range full {
		rec.Minhash = nil
	}
	txtRes := analyze.Run(full, nil, nil, cfg, analyze.WithLinks(links))

	col := nearDupSet(colRes)
	txt := nearDupSet(txtRes)
	if len(col) == 0 {
		t.Fatal("near-dup produced no pairs — parity check is vacuous")
	}
	if strings.Join(col, "\n") != strings.Join(txt, "\n") {
		t.Errorf("near-dup column path != ContentText path\n column: %v\n text:   %v", col, txt)
	}
}

func nearDupSet(r *analyze.Results) []string {
	var out []string
	for url, nd := range r.NearDups {
		out = append(out, fmt.Sprintf("%s|%d|%.2f|%s", url, nd.Count, nd.ClosestSimilarity, nd.ClosestMatch))
	}
	sort.Strings(out)
	return out
}

// TestNearDupFinalizeFiresOffLiteMap: the full finalize path (which, with the
// column present, analyzes the lite map) still persists content_near_duplicate
// on the two similar pages and not the unrelated one.
func TestNearDupFinalizeFiresOffLiteMap(t *testing.T) {
	st, seed := crawlNearDup(t, true)
	base := strings.TrimSuffix(seed, "/")
	urls, err := st.IssueURLs("content_near_duplicate")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range urls {
		got[u] = true
	}
	for _, p := range []string{"/a", "/b"} {
		if !got[base+p] {
			t.Errorf("content_near_duplicate did not fire on %s (got %v)", p, urls)
		}
	}
	if got[base+"/c"] {
		t.Errorf("content_near_duplicate fired on the unrelated page /c: %v", urls)
	}
}

// TestNearDupDisabledLeavesColumnEmpty: a crawl with near-dup OFF persists no
// signatures (the column stays NULL), so finalize falls back to the full map.
func TestNearDupDisabledLeavesColumnEmpty(t *testing.T) {
	st, seed := crawlNearDup(t, false)
	base := strings.TrimSuffix(seed, "/")
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/a", "/b", "/c"} {
		if rec := pages[base+p]; rec != nil && len(rec.Minhash) != 0 {
			t.Errorf("%s has a minhash signature though near-dup was off", p)
		}
	}
}
