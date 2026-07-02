package finalize

// EC-05 (MEMORY-SCALING.md §13.3): a crawl must never be observable as
// `completed` until its depth recompute AND analysis are durable on disk. A hard
// crash anywhere in the completed tail must leave a resumable crawl, and a resume
// (re-running finalize) must converge on the same end-state. TestFinalize_
// PartialCrash_Idempotent pins both halves: the ordering guarantee (a tail
// failure leaves the crawl StatusInterrupted, not completed, and depths are NOT
// left stale-but-sealed) and idempotency (re-running finalize after the failure
// is cleared yields a byte-identical end-state vs a clean one-shot finalize).

import (
	"context"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// crawlGraphNoFinalize crawls graphSite into a fresh store but stops BEFORE
// finalize, so the caller drives finalize itself (and can simulate a crash in
// its tail). Returns the store, the crawler, the live Result, the store dir and
// the seed URL.
func crawlGraphNoFinalize(t *testing.T) (*store.Crawl, *crawler.Crawler, *crawler.Result, string, string) {
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
	return st, c, res, dir, seed
}

// registryStatus reads a crawl's recorded registry status by id.
func registryStatus(t *testing.T, dir, id string) string {
	t.Helper()
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == id {
			return in.Status
		}
	}
	t.Fatalf("crawl %s not found in registry", id)
	return ""
}

func TestFinalize_PartialCrash_Idempotent(t *testing.T) {
	st, c, res, dir, seed := crawlGraphNoFinalize(t)
	cfg := config.Default()
	params := Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true}

	// Force the analysis step to fail by dropping the table SaveAnalysis writes to,
	// simulating a crash AFTER the depth recompute but BEFORE analysis is durable.
	if _, err := st.DB().Exec(`DROP TABLE analysis`); err != nil {
		t.Fatalf("drop analysis: %v", err)
	}

	// finalize with analysis broken: it must report an error and, crucially, must
	// NOT seal the crawl as completed (the EC-05 ordering guarantee). On the old
	// code SetStatus(completed) ran before analysis, so this sealed completed.
	if _, ferr := Crawl(c, st, res, params); ferr == nil {
		t.Fatal("expected finalize to fail with analysis table dropped, got nil")
	}
	if got := registryStatus(t, dir, st.ID); got == store.StatusCompleted {
		t.Fatalf("crawl sealed %q despite analysis failing — completed must not be readable until analysis is durable (EC-05)", got)
	}
	// Depth WAS recomputed before the crash point: the seed is depth 0 and a
	// deeper page is non-zero, so the completed-tail depth step ran (not left at a
	// uniform admit-time value). This proves the failure was in analysis, not earlier.
	base := seed[:len(seed)-1]
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if pages[base+"/c"] == nil || pages[base+"/c"].Depth != 2 {
		t.Fatalf("depth recompute did not run before the crash point: /c depth = %v", pages[base+"/c"])
	}

	// Clear the failure (the resume world has the table back) and re-run finalize.
	if _, err := st.DB().Exec(`CREATE TABLE IF NOT EXISTS analysis(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("recreate analysis: %v", err)
	}

	if _, ferr := Crawl(c, st, res, params); ferr != nil {
		t.Fatalf("re-run finalize after recovery: %v", ferr)
	}
	if got := registryStatus(t, dir, st.ID); got != store.StatusCompleted {
		t.Fatalf("after recovery re-run, status = %q, want completed", got)
	}

	// The recovered end-state must be byte-identical to a clean one-shot finalize
	// of the same graph: same depths, inlinks, discovered_from, link_score, and the
	// same persisted issue set.
	refSt, _, refSeed := crawlGraph(t) // clean one-shot finalize of graphSite
	refBase := refSeed[:len(refSeed)-1]
	refPages, err := refSt.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	gotPages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/a", "/b", "/c"} {
		got, ref := gotPages[base+p], refPages[refBase+p]
		if got == nil || ref == nil {
			t.Fatalf("page %s missing (got=%v ref=%v)", p, got, ref)
		}
		if got.Depth != ref.Depth || got.Inlinks != ref.Inlinks {
			t.Errorf("%s: recovered depth/inlinks = %d/%d, clean = %d/%d", p, got.Depth, got.Inlinks, ref.Depth, ref.Inlinks)
		}
		// discovered_from is host-relative; compare with the base stripped.
		gotFrom := relPath(got.DiscoveredFrom, base)
		refFrom := relPath(ref.DiscoveredFrom, refBase)
		if gotFrom != refFrom {
			t.Errorf("%s: recovered discovered_from = %q, clean = %q", p, gotFrom, refFrom)
		}
		if got.LinkScore != ref.LinkScore {
			t.Errorf("%s: recovered link_score = %g, clean = %g", p, got.LinkScore, ref.LinkScore)
		}
	}
	// Same persisted duplicate-issue set (analysis re-ran cleanly).
	gotDup, _ := st.IssueURLs("title_duplicate")
	refDup, _ := refSt.IssueURLs("title_duplicate")
	if len(gotDup) != len(refDup) {
		t.Errorf("title_duplicate set size: recovered=%d clean=%d", len(gotDup), len(refDup))
	}
}

// relPath strips a host base prefix from a stored URL so two crawls on different
// ephemeral ports compare equal; "" (seed lock) stays "".
func relPath(url, base string) string {
	if url == "" {
		return ""
	}
	if len(url) >= len(base) && url[:len(base)] == base {
		return url[len(base):]
	}
	return url
}
