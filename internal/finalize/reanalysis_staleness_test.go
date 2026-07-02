package finalize

// Issue #75 (pre-existing finalize/analyze staleness), bugs 1 and 2. The
// contract under test: every issue writer replaces exactly the rows of the
// checks it re-evaluated, and SaveAnalysis resets the analysis-owned page
// columns before applying a new result set — so neither the `issues` command
// nor a re-analysis with different knobs can leave a stored crawl mixing two
// runs' findings.

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

// staleSiteCfg is the crawl config for staleSite: near-duplicate detection on
// (so crawl-time minhash signatures exist and analysis emits near-dup issues).
func staleSiteCfg() *config.Config {
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	return cfg
}

// staleSite serves a fixture exercising all three analysis-phase issue
// families the `issues` command must not wipe:
//
//	/r1 → /r2 → /r3      redirect chain (redirect_chain on /r1)
//	/h1 —hreflang→ /h2   /h2 never links back (hreflang_missing_return on /h1)
//	/d1 ≈ /d2            200-word bodies differing in one word (~95% similar
//	                     → content_near_duplicate on both; hashes differ, so
//	                     the exact-duplicate exclusion does not swallow them)
func staleSite(t *testing.T) *httptest.Server {
	t.Helper()
	words := func(n int, last string) string {
		var b strings.Builder
		for i := 0; i < n-1; i++ {
			fmt.Fprintf(&b, "alpha%d ", i)
		}
		b.WriteString(last)
		return b.String()
	}
	page := func(head, body string) string {
		return "<html><head><title>t</title>" + head + "</head><body>" + body + "</body></html>"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("", `<a href="/r1">r</a> <a href="/h1">h1</a> <a href="/h2">h2</a> <a href="/d1">d1</a> <a href="/d2">d2</a>`))
		case "/r1":
			http.Redirect(w, r, "/r2", http.StatusMovedPermanently)
		case "/r2":
			http.Redirect(w, r, "/r3", http.StatusMovedPermanently)
		case "/r3":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("", "<p>end of the chain</p>"))
		case "/h1":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page(`<link rel="alternate" hreflang="en" href="`+base+`/h2">`, "<p>english</p>"))
		case "/h2":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("", "<p>no return link here</p>"))
		case "/d1":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("", "<p>"+words(200, "omega")+"</p>"))
		case "/d2":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("", "<p>"+words(200, "differs")+"</p>"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawlStaleSite runs staleSite through the full finalize path (analysis on,
// near-dup enabled) and returns the store, config and seed.
func crawlStaleSite(t *testing.T) (*store.Crawl, *config.Config, string) {
	t.Helper()
	srv := staleSite(t)
	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := staleSiteCfg()
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
	if _, err := Crawl(c, st, res, Params{
		StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return st, cfg, seed
}

// allIssueRows reads the full issues table as sorted "url|issue|detail" keys.
func allIssueRows(t *testing.T, st *store.Crawl) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT url, issue, detail FROM issues ORDER BY url, issue, detail`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var url, issue, detail string
		if err := rows.Scan(&url, &issue, &detail); err != nil {
			t.Fatal(err)
		}
		all = append(all, url+"|"+issue+"|"+detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all
}

// TestIssuesCmdPreservesAnalysisIssues (#75 bug 1): the `issues` command is a
// cheap catalogue-only refresh — it re-evaluates only the checks it owns, so
// the analysis-phase occurrences (redirect chains, near-duplicates, hreflang,
// sitemaps, pagination) a completed crawl already computed must survive it
// byte-identically. On a just-analyzed crawl the whole refresh must therefore
// be a no-op on the stored issue set.
func TestIssuesCmdPreservesAnalysisIssues(t *testing.T) {
	st, cfg, seed := crawlStaleSite(t)
	base := strings.TrimSuffix(seed, "/")

	before := allIssueRows(t, st)
	// Sanity: the analysis-phase families actually fired, else the test is vacuous.
	for _, want := range []string{
		base + "/r1|redirect_chain|",
		base + "/d1|content_near_duplicate|",
		base + "/h1|hreflang_missing_return|",
	} {
		found := false
		for _, row := range before {
			if strings.HasPrefix(row, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixture did not produce an occurrence %q — got:\n%s", want, strings.Join(before, "\n"))
		}
	}

	if err := Issues(st, cfg); err != nil {
		t.Fatalf("Issues: %v", err)
	}
	after := allIssueRows(t, st)

	if len(before) != len(after) {
		t.Errorf("issues row count changed: %d -> %d", len(before), len(after))
	}
	afterSet := make(map[string]bool, len(after))
	for _, row := range after {
		afterSet[row] = true
	}
	for _, row := range before {
		if !afterSet[row] {
			t.Errorf("issue row lost by the issues refresh: %s", row)
		}
	}
	beforeSet := make(map[string]bool, len(before))
	for _, row := range before {
		beforeSet[row] = true
	}
	for _, row := range after {
		if !beforeSet[row] {
			t.Errorf("issues refresh minted a new row: %s", row)
		}
	}
}

// TestReanalyzeClearsStaleScoreColumns (#75 bug 2): re-analysis replaces the
// whole analysis result — a page that dropped out of the new result maps
// (near-dup disabled here, link score off) must have its analysis-owned
// columns reset to their pre-analysis defaults, not keep the previous run's
// values.
func TestReanalyzeClearsStaleScoreColumns(t *testing.T) {
	st, _, seed := crawlStaleSite(t)
	base := strings.TrimSuffix(seed, "/")

	var sim float64
	var cnt int
	if err := st.DB().QueryRow(`SELECT closest_similarity, near_dup_count FROM pages WHERE url = ?`,
		base+"/d1").Scan(&sim, &cnt); err != nil {
		t.Fatal(err)
	}
	if sim <= 0 || cnt != 1 {
		t.Fatalf("fixture did not near-dup /d1 (similarity=%v count=%d) — test is vacuous", sim, cnt)
	}
	var score float64
	if err := st.DB().QueryRow(`SELECT link_score FROM pages WHERE url = ?`, base+"/d1").Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score <= 0 {
		t.Fatalf("fixture did not score /d1 (link_score=%v) — test is vacuous", score)
	}

	// Re-analyze with near-duplicates back at the default (off) and link score off.
	cfg2 := config.Default()
	cfg2.Analysis.LinkScore = false
	if _, err := Analyze(st, cfg2); err != nil {
		t.Fatalf("re-analyze: %v", err)
	}

	if err := st.DB().QueryRow(`SELECT closest_similarity, near_dup_count, link_score FROM pages WHERE url = ?`,
		base+"/d1").Scan(&sim, &cnt, &score); err != nil {
		t.Fatal(err)
	}
	if sim != 0 || cnt != 0 {
		t.Errorf("near-dup columns stale after re-analysis with near-dup off: similarity=%v count=%d, want 0/0", sim, cnt)
	}
	if score != 0 {
		t.Errorf("link_score stale after re-analysis with link score off: %v, want 0", score)
	}

	// The replaced issue set follows the same contract: no near-dup occurrences
	// survive a re-analysis that did not compute them.
	for _, row := range allIssueRows(t, st) {
		if strings.Contains(row, "|content_near_duplicate|") {
			t.Errorf("stale near-dup occurrence after re-analysis: %s", row)
		}
	}
}
