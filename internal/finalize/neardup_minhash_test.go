package finalize

// FIN-NDSIG (MEMORY-SCALING.md §5.5, EC19): the near-dup minhash column. A
// near-dup-enabled crawl precomputes each page's signature at crawl time and
// persists it (pages.minhash), and the near-copy step picks its candidates from
// the stored signatures. These tests pin: (1) the signatures round-trip through
// the store; (2) the step over the column is IDENTICAL to the step computing
// signatures from the stored text; (3) finalize raises the issue from it; (4) a
// near-dup-OFF crawl leaves the column empty.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestNearDupColumnEqualsContentTextPath proves the near-copy step over the
// precomputed signature column is byte-identical to the same step computing
// each signature from the stored text — the path an older crawl, or one that
// ran with the check off, takes when the check is turned on for re-analysis.
func TestNearDupColumnEqualsContentTextPath(t *testing.T) {
	st, _ := crawlNearDup(t, true)
	cfg := config.Default()
	cfg.Analysis.NearDuplicates = true
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 90

	if _, err := Analyze(st, cfg); err != nil {
		t.Fatal(err)
	}
	col := nearCopyRows(t, st)
	if _, err := st.DB().Exec(`UPDATE pages SET minhash = NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(st, cfg); err != nil {
		t.Fatal(err)
	}
	txt := nearCopyRows(t, st)
	if !strings.Contains(strings.Join(col, "\n"), `"share"`) {
		t.Fatalf("near copies found no pairs — parity check is vacuous: %v", col)
	}
	if strings.Join(col, "\n") != strings.Join(txt, "\n") {
		t.Errorf("near copies over the column != over the text\n column: %v\n text:   %v", col, txt)
	}
}

// nearCopyRows is every page's stored near-copy fields, one line a page.
func nearCopyRows(t *testing.T, st *store.Crawl) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT url, COALESCE(near_copies, 'null'), COALESCE(near_copy_count, -1),
		closest_similarity, near_dup_count FROM page_metrics ORDER BY url`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var url, list string
		var count, dups int
		var closest float64
		if err := rows.Scan(&url, &list, &count, &closest, &dups); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s|%s|%d|%.2f|%d", url, list, count, closest, dups))
	}
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
