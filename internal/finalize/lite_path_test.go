package finalize

// The lite finalize path evaluates a crawl from a page map that carries no
// link lists and no page bodies, streaming the per-page link and content checks
// in one page at a time and reading the link graph from the links table. These
// tests pin what that path must keep: the whole-map issue set, byte for byte,
// on a fixture where the link checks actually fire; and the facts-bearing pages
// rows, which finalize must leave untouched now that its outputs live in
// page_metrics.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// linkySite serves a graph whose pages trip the link-reading checks: image
// references with and without alt and size attributes, a nofollow internal
// link, an empty-anchor link, a link to a 404 and to a redirect, an external
// target=_blank link without noopener, a relative canonical carrying a
// fragment, a rel=next target no anchor points at, and a page whose only
// inlinks are nofollow.
func linkySite(t *testing.T) *httptest.Server {
	t.Helper()
	bodies := map[string]string{
		"/": `<html><head><title>Home</title></head><body>
			<a href="/a">alpha</a> <a href="/b">beta</a> <a href="/only-nofollow" rel="nofollow">nf</a>
			<a href="/missing">gone</a> <a href="/r">moved</a> <a href="/b"></a>
			<a href="http://elsewhere.example/" target="_blank">out</a>
			<img src="/img/noalt.png"> <img src="/img/empty.png" alt=""> <img src="/img/sized.png" alt="sized" width="10" height="10">
			</body></html>`,
		"/a": `<html><head><title>Alpha</title><link rel="canonical" href="/a#top"><link rel="next" href="/a2"></head><body>
			<a href="/b">beta</a> <a href="/only-nofollow" rel="nofollow">nf again</a> <a href="/">home</a>
			</body></html>`,
		"/a2":            `<html><head><title>Alpha 2</title><link rel="prev" href="/a"></head><body><a href="/a">back</a></body></html>`,
		"/b":             `<html><head><title>Beta</title></head><body><a href="/a">alpha</a> <a href="/c">gamma</a></body></html>`,
		"/c":             `<html><head><title>Gamma</title></head><body><p>leaf</p></body></html>`,
		"/only-nofollow": `<html><head><title>Only nofollow</title></head><body><p>reached by nofollow links only</p></body></html>`,
		"/target":        `<html><head><title>Target</title></head><body><p>redirect target</p></body></html>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r" {
			http.Redirect(w, r, "/target", http.StatusMovedPermanently)
			return
		}
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

// crawlLinkySite crawls linkySite into a fresh store without finalizing it.
func crawlLinkySite(t *testing.T, cfg *config.Config) (*store.Crawl, *crawler.Crawler, *crawler.Result, string, string) {
	t.Helper()
	srv := linkySite(t)
	seed := srv.URL + "/"
	dir := t.TempDir()
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
	return st, c, res, dir, seed
}

// TestLitePathIssuesMatchWholeMapEvaluate: the issue set the lite path computes
// — Evaluate over the lite map with the link checks skipped, plus the streamed
// LinkChecker and ContentTextChecks — equals a whole-map issues.Evaluate exactly,
// on a fixture where every link-reading check family fires. The persisted
// issues table carries that set too (the analysis phase adds its own rows on
// top).
func TestLitePathIssuesMatchWholeMapEvaluate(t *testing.T) {
	cfg := config.Default()
	cfg.Resources.Images.Store = true       // the image checks run only when image references are stored
	cfg.Scope.FollowInternalNofollow = true // so /only-nofollow is crawled, and its inlinks are all nofollow
	st, c, res, dir, seed := crawlLinkySite(t, cfg)
	if _, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true}); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	full, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	lite, err := st.LoadPagesLite()
	if err != nil {
		t.Fatal(err)
	}
	for url, rec := range lite {
		if rec.Facts != nil && (len(rec.Facts.Links) != 0 || rec.Facts.ContentText != "") {
			t.Fatalf("lite map for %s holds %d links and %d bytes of text — the lite path is not lite", url, len(rec.Facts.Links), len(rec.Facts.ContentText))
		}
	}
	want := occurrenceSet(issues.Evaluate(full, cfg))
	got, err := evaluateIssues(st, lite, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gotSet := occurrenceSet(got)
	for k := range want {
		if _, ok := gotSet[k]; !ok {
			t.Errorf("lite path missing %q", k)
		}
	}
	for k := range gotSet {
		if _, ok := want[k]; !ok {
			t.Errorf("lite path has extra %q", k)
		}
	}
	// Vacuity guard: each family of link-reading checks fired on the fixture.
	fired := map[string]bool{}
	for _, id := range want {
		fired[id] = true
	}
	for _, id := range []string{
		"image_missing_alt_attribute", "image_missing_alt", "image_missing_size_attributes",
		"links_internal_nofollow_outlinks", "links_no_anchor_text", "links_outlinks_to_broken", "links_outlinks_to_redirect",
		"security_unsafe_cross_origin", "canonical_relative", "canonical_contains_fragment",
		"pagination_not_in_anchor", "links_nofollow_inlinks_only",
	} {
		if !fired[id] {
			t.Errorf("fixture never fired %s — the parity check does not cover it", id)
		}
	}
	// Every catalogue occurrence is in the issues table finalize wrote.
	rows, err := st.DB().Query(`SELECT url, issue, detail FROM issues`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	persisted := map[string]bool{}
	for rows.Next() {
		var url, id, detail string
		if err := rows.Scan(&url, &id, &detail); err != nil {
			t.Fatal(err)
		}
		persisted[url+"|"+id+"|"+detail] = true
	}
	for k := range want {
		if !persisted[k] {
			t.Errorf("finalize did not persist %q", k)
		}
	}
}

// TestFinalizeLeavesPagesRowsUntouched: a completed finalize — inlinks, depth,
// link score, unique in/outlinks, issues — changes no byte of any pages row.
// Its outputs land in page_metrics, so the facts blobs (hundreds of KB on a
// link-heavy page) are never rewritten; on the reported crawls the old UPDATEs
// over pages grew the WAL to the size of every facts blob.
func TestFinalizeLeavesPagesRowsUntouched(t *testing.T) {
	cfg := config.Default()
	st, c, res, dir, seed := crawlLinkySite(t, cfg)

	pagesBefore := tableDigest(t, st, "pages")
	metricsBefore := tableDigest(t, st, "page_metrics")
	out, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if out.Status != store.StatusCompleted || !out.Analyzed {
		t.Fatalf("finalize outcome = %+v, want completed + analyzed", out)
	}
	if got := tableDigest(t, st, "pages"); got != pagesBefore {
		t.Error("finalize rewrote pages rows — its per-page outputs must go to page_metrics only")
	}
	if got := tableDigest(t, st, "page_metrics"); got == metricsBefore {
		t.Error("finalize left page_metrics as the crawl wrote it — the check above is vacuous")
	}
	// And the metrics did land: the seed has inlinks (/a and /b link back) and
	// a link score, /c sits at depth 2.
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if home := pages[seed]; home.Inlinks == 0 || home.LinkScore == 0 || home.UniqueOutlinks == 0 {
		t.Errorf("seed metrics = %+v, want inlinks, link score and unique outlinks set", home)
	}
	if got := pages[seed+"c"].Depth; got != 2 {
		t.Errorf("depth(/c) = %d, want 2", got)
	}
}

// tableDigest hashes every row of a table in URL order, every column included.
func tableDigest(t *testing.T, st *store.Crawl, table string) string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT * FROM ` + table + ` ORDER BY url`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			fmt.Fprintf(h, "%s=%v|", cols[i], v)
		}
		fmt.Fprint(h, "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
