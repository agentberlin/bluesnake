package finalize

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestIssuesRefreshPopulatesTable runs the full finalize.Issues path (the cheap
// issue-only refresh used by the `issues` command) over a crawl with stored
// pages, asserting the issues table is populated and re-running is idempotent.
func TestIssuesRefreshPopulatesTable(t *testing.T) {
	st, _, _, _ := crawlInto(t)
	cfg := config.Default()

	// The page at /a has no title/h1/meta, so the catalogue fires real checks.
	if err := Issues(st, cfg); err != nil {
		t.Fatalf("Issues: %v", err)
	}
	counts, err := st.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) == 0 {
		t.Fatal("Issues refresh wrote no occurrences")
	}

	// Idempotent: a second refresh yields the same set (SaveIssues replaces).
	if err := Issues(st, cfg); err != nil {
		t.Fatalf("Issues (2nd): %v", err)
	}
	again, err := st.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(counts) {
		t.Errorf("re-running Issues changed check count: %d -> %d", len(counts), len(again))
	}
	for id, n := range counts {
		if again[id] != n {
			t.Errorf("issue %q count drifted: %d -> %d", id, n, again[id])
		}
	}
}

// TestIssuesErrorOnClosedStore covers the LoadPages error return of Issues.
func TestIssuesErrorOnClosedStore(t *testing.T) {
	st, _, _, _ := crawlInto(t)
	st.Close()
	if err := Issues(st, config.Default()); err == nil {
		t.Error("Issues on a closed store: want error")
	}
}

// TestCrawlResumeRecomputesDepthAndInlinks drives the resume branch of Crawl
// (Resumed=true with a non-empty seed set), which reloads the full graph and
// recomputes depths/inlinks before analysis. We assert it completes, analyzes,
// and that the registry counts come from the full stored graph.
func TestCrawlResumeRecomputesDepthAndInlinks(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	cfg := config.Default()

	// Capture the seed the crawl was started with so the resume BFS can re-root.
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 crawl, got %d", len(infos))
	}
	seed := infos[0].Seed

	out, err := Crawl(c, st, res, Params{
		StoreDir:  dir,
		Cfg:       cfg,
		Seeds:     []string{seed},
		Resumed:   true,
		Completed: true,
	})
	if err != nil {
		t.Fatalf("Crawl (resume): %v", err)
	}
	if out.Status != store.StatusCompleted {
		t.Errorf("status = %q, want completed", out.Status)
	}
	if !out.Analyzed {
		t.Error("resume completion should run analysis")
	}
	// Counts are authoritative from the store (root + /a = 2 crawled).
	if out.Crawled != 2 || out.Total != 2 {
		t.Errorf("counts = %d/%d, want 2/2", out.Crawled, out.Total)
	}

	// Depths survived the recompute: the seed root is depth 0, /a is depth 1.
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if root := pages[seed]; root == nil || root.Depth != 0 {
		t.Errorf("root depth = %v, want 0", root)
	}
}

// TestCrawlResumeEmptySeedsSkipsRecompute covers the guard that resume with no
// seeds must NOT recompute depth (rooting from nothing would NULL every depth):
// it still completes and analyzes, taking the !(Resumed && len>0) branch.
func TestCrawlResumeEmptySeedsSkipsRecompute(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	cfg := config.Default()

	out, err := Crawl(c, st, res, Params{
		StoreDir:  dir,
		Cfg:       cfg,
		Seeds:     nil, // empty -> recompute guard skips depth/inlink rewrite
		Resumed:   true,
		Completed: true,
	})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if out.Status != store.StatusCompleted || !out.Analyzed {
		t.Errorf("outcome = %+v, want completed+analyzed", out)
	}
}

// TestCrawlAnalysisDisabled covers the Analysis.Auto=false branch of Crawl: the
// crawl completes and records status/counts but never runs the analysis phase.
func TestCrawlAnalysisDisabled(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	cfg := config.Default()
	cfg.Analysis.Auto = false

	out, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Completed: true})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if out.Status != store.StatusCompleted {
		t.Errorf("status = %q, want completed", out.Status)
	}
	if out.Analyzed {
		t.Error("Analysis.Auto=false must not run analysis")
	}
	if out.IssueTotal != 0 {
		t.Errorf("issue total = %d, want 0 (no analysis)", out.IssueTotal)
	}
}

// TestAnalyzeErrorOnClosedStore covers Analyze's LoadPages error return.
func TestAnalyzeErrorOnClosedStore(t *testing.T) {
	st, _, _, _ := crawlInto(t)
	st.Close()
	if _, err := Analyze(st, config.Default()); err == nil {
		t.Error("Analyze on a closed store: want error")
	}
}

// TestAnalyzeSaveIssuesError covers Analyze's mid-pipeline error return: pages
// load fine but the downstream SaveIssues fails because the issues table is gone.
// (A deterministic stand-in for any store write failing after LoadPages.)
func TestAnalyzeSaveIssuesError(t *testing.T) {
	st, _, _, _ := crawlInto(t)
	if _, err := st.DB().Exec(`DROP TABLE issues`); err != nil {
		t.Fatalf("drop issues: %v", err)
	}
	if _, err := Analyze(st, config.Default()); err == nil {
		t.Error("Analyze should fail when SaveIssues cannot write")
	}
}

// TestCrawlResumeLoadPagesError covers the resume branch's LoadPages error path:
// with the pages table dropped, the full-graph reload inside the resume recompute
// fails, and Crawl surfaces it while still recording status.
func TestCrawlResumeLoadPagesError(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	if _, err := st.DB().Exec(`DROP TABLE pages`); err != nil {
		t.Fatalf("drop pages: %v", err)
	}
	out, err := Crawl(c, st, res, Params{
		StoreDir:  dir,
		Cfg:       config.Default(),
		Seeds:     []string{"http://example.com/"},
		Resumed:   true,
		Completed: true,
	})
	if err == nil {
		t.Error("Crawl should surface the resume LoadPages failure")
	}
	if out.Status != store.StatusCompleted {
		t.Errorf("status = %q, want completed (best-effort records status)", out.Status)
	}
}

// TestCrawlReportsFirstError covers the best-effort error aggregation in Crawl:
// when the store is closed, UpdateInlinks (the first step) fails and Crawl
// returns that first error while still attempting the later best-effort steps.
func TestCrawlReportsFirstError(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	st.Close()
	cfg := config.Default()

	out, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Completed: false})
	if err == nil {
		t.Fatal("Crawl on a closed store should surface the first error")
	}
	// Even on error the interrupted status is resolved from Completed=false.
	if out.Status != store.StatusInterrupted {
		t.Errorf("status = %q, want interrupted", out.Status)
	}
}
