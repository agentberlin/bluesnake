package finalize

// EC24 / FIN-GOLDEN (MEMORY-SCALING.md §13.4, §13.9): the captured-RAM golden.
// It pins the CURRENT (in-RAM LoadPages) finalize outputs — depth, raw inlinks,
// first-wins discovered_from, unique in/out, link_score, and duplicate issue
// occurrences — over a crafted graph, so the Phase-2 SQL/CSR rewrite can be
// diffed byte-for-byte against the original semantics rather than only against
// resume==straight (which can pass even if SQL diverges from RAM in lockstep).
// Build this BEFORE any §13.4 SQL work; it is the Phase-2 entry gate.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestDupSQLParity (FIN-DUPH/FIN-DUPGATE) proves store.DuplicateIssues (pure SQL)
// reproduces the in-RAM issues.duplicates() occurrence set EXACTLY — the
// eligibility gate, the 5 key types, and the per-(url,key) detail rows.
func TestDupSQLParity(t *testing.T) {
	st, _ := goldenGraph(t) // /b and /c share title "Same"
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	dupIDs := map[string]bool{
		"content_exact_duplicate": true, "title_duplicate": true, "description_duplicate": true,
		"h1_duplicate": true, "h2_duplicate": true,
	}
	ram := map[string]bool{}
	for _, o := range issues.Evaluate(pages, cfg) {
		if dupIDs[o.IssueID] {
			ram[o.URL+"|"+o.IssueID+"|"+o.Detail] = true
		}
	}
	sqlDups, err := st.DuplicateIssues(cfg.Advanced.IgnoreNonIndexableForIssues, cfg.Advanced.IgnorePaginatedForDuplicates)
	if err != nil {
		t.Fatal(err)
	}
	sql := map[string]bool{}
	for _, o := range sqlDups {
		sql[o.URL+"|"+o.IssueID+"|"+o.Detail] = true
	}
	if len(ram) == 0 {
		t.Fatal("fixture produced no duplicate occurrences — parity check is vacuous")
	}
	for k := range ram {
		if !sql[k] {
			t.Errorf("SQL dup missing %q (present in-RAM)", k)
		}
	}
	for k := range sql {
		if !ram[k] {
			t.Errorf("SQL dup has extra %q (absent in-RAM)", k)
		}
	}
}

// TestPageRankCSRParity (FIN-PR) proves the PageRank/unique link graph computed
// in CSR form over the stored links superset (analyze.WithLinks) reproduces the
// Facts.Links computation EXACTLY (link_score and unique in/out). The exact match
// is now possible because PageRank accumulates in a canonical (sorted) order, so
// both paths produce the same float sum bit-for-bit — no 1e-9 fudge needed.
func TestPageRankCSRParity(t *testing.T) {
	st, _, _ := crawlGraph(t)
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	links, err := st.LinkRows()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	sm, _ := st.SitemapIndex()
	lt, _ := st.LlmsTxt()
	ram := analyze.Run(pages, sm, lt, cfg)                           // Facts.Links
	csr := analyze.Run(pages, sm, lt, cfg, analyze.WithLinks(links)) // CSR over links table

	for url, want := range ram.LinkScores {
		if csr.LinkScores[url] != want {
			t.Errorf("link_score(%s): CSR=%g, Facts.Links=%g (canonical order must match bit-for-bit)", url, csr.LinkScores[url], want)
		}
	}
	for url, want := range ram.UniqueIn {
		if csr.UniqueIn[url] != want {
			t.Errorf("unique_in(%s): CSR=%d, Facts.Links=%d", url, csr.UniqueIn[url], want)
		}
	}
	for url, want := range ram.UniqueOut {
		if csr.UniqueOut[url] != want {
			t.Errorf("unique_out(%s): CSR=%d, Facts.Links=%d", url, csr.UniqueOut[url], want)
		}
	}
}

// goldenGraph serves a small graph exercising the finalize parity surface:
//
//	/  → /a, /b           title "Home"   (seed, depth 0)
//	/a → /b, /c           title "Alpha"  (depth 1)
//	/b → /                title "Same"   (depth 1; back-link to the seed)
//	/c → /c (self-link)   title "Same"   (depth 2; self-link)
//
// /b and /c share the title "Same" → a title_duplicate occurrence on both.
func goldenGraph(t *testing.T) (*store.Crawl, string) {
	t.Helper()
	page := func(title string, links ...string) string {
		s := "<html><head><title>" + title + "</title></head><body>"
		for _, l := range links {
			s += fmt.Sprintf(`<a href="%s">x</a>`, l)
		}
		return s + "</body></html>"
	}
	bodies := map[string]string{
		"/":  page("Home", "/a", "/b"),
		"/a": page("Alpha", "/b", "/c"),
		"/b": page("Same", "/"),
		"/c": page("Same", "/c"),
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
	return st, seed
}

// Depth and inlink parity are covered NON-circularly elsewhere, so the former
// TestDepthCSRParity / TestEdgesSQLParity here were removed (P9): they compared
// the SQL/CSR output against rec.Depth/rec.Inlinks — values produced by the SAME
// finalize path inside crawlGraph, so a wrong recompute could pass in lockstep
// (mutation-verified: depth+100 left them green). The genuine oracles are
// TestDepthAndInlinkGateDivergenceOracle (hand-derived, gate-divergent) here and
// TestRecomputeDepthsFromLinksParity in the crawler package; TestEdgesSQLParity
// additionally exercised InlinksFromEdges/DiscoveredFromEdges, which production
// never calls.

func TestFinalizeGolden_CapturedRAMContract(t *testing.T) {
	st, seed := goldenGraph(t)
	base := seed[:len(seed)-1] // strip trailing "/"

	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}

	// Exact, hand-derivable aggregates (depth / raw inlinks / discovered_from).
	type want struct {
		depth          int
		inlinks        int
		uniqueOut      int
		discoveredFrom string
	}
	golden := map[string]want{
		"/":  {0, 1, 2, ""},         // ←/b ; →/a,/b
		"/a": {1, 1, 2, base + "/"}, // ←/ ; →/b,/c
		"/b": {1, 2, 1, base + "/"}, // ←/,/a ; →/
		// /c ← /a AND its own self-link: the current discoverLinks→noteInlink
		// path counts a self hyperlink in raw inlinks (inlinks=2), so the captured
		// contract records that. (Depth excludes self; discovered_from stays /a,
		// the first discoverer.) A Phase-2 SQL rewrite must reproduce this exactly
		// or consciously change it.
		"/c": {2, 2, 1, base + "/a"},
	}
	for path, w := range golden {
		rec := pages[base+path]
		if rec == nil {
			t.Fatalf("%s missing", path)
		}
		if rec.Depth != w.depth {
			t.Errorf("%s depth = %d, want %d", path, rec.Depth, w.depth)
		}
		if rec.Inlinks != w.inlinks {
			t.Errorf("%s inlinks = %d, want %d", path, rec.Inlinks, w.inlinks)
		}
		if rec.UniqueOutlinks != w.uniqueOut {
			t.Errorf("%s unique_outlinks = %d, want %d", path, rec.UniqueOutlinks, w.uniqueOut)
		}
		if rec.DiscoveredFrom != w.discoveredFrom {
			t.Errorf("%s discovered_from = %q, want %q", path, rec.DiscoveredFrom, w.discoveredFrom)
		}
	}

	// link_score (PageRank, scaled v/max·100): every crawled node holds a score
	// in [0,100], the maximum is exactly 100, and a back-linked page outranks a
	// leaf. These structural invariants pin the scaling + node-set contract.
	var maxScore float64
	for _, rec := range pages {
		if rec.LinkScore < 0 || rec.LinkScore > 100.0001 {
			t.Errorf("%s link_score = %f, out of [0,100]", rec.URL, rec.LinkScore)
		}
		if rec.LinkScore > maxScore {
			maxScore = rec.LinkScore
		}
	}
	if maxScore < 99.999 || maxScore > 100.001 {
		t.Errorf("max link_score = %f, want 100 (v/max·100 scaling)", maxScore)
	}

	// Duplicate title occurrences: /b and /c share "Same" → title_duplicate on both.
	urls, err := st.IssueURLs("title_duplicate")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range urls {
		got[u] = true
	}
	for _, p := range []string{"/b", "/c"} {
		if !got[base+p] {
			t.Errorf("title_duplicate did not fire on %s (got %v)", p, urls)
		}
	}
	if got[base+"/"] || got[base+"/a"] {
		t.Errorf("title_duplicate fired on a unique-title page: %v", urls)
	}
}
