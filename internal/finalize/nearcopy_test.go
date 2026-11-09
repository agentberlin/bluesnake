package finalize

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// berlinCfg is the configuration Berlin crawls with for a brand's own site:
// the analysis pass off, near copies switched on with --set flags.
func berlinCfg() *config.Config {
	cfg := config.Default()
	cfg.Analysis.Auto = false
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 80
	cfg.Content.NearDuplicates.MinWords = 50
	return cfg
}

// crawlWith crawls nearDupSite under cfg and finalizes it, completed or not.
// It returns the store, the site's base URL and the finalize outcome.
func crawlWith(t *testing.T, cfg *config.Config, completed bool) (*store.Crawl, string, Outcome) {
	t.Helper()
	srv := nearDupSite(t)
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
	out, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: completed})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return st, strings.TrimSuffix(seed, "/"), out
}

func nearCopiesOf(t *testing.T, st *store.Crawl, url string) (list sql.NullString, count sql.NullInt64) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT near_copies, near_copy_count FROM page_metrics WHERE url = ?`, url).
		Scan(&list, &count); err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	return list, count
}

// TestNearCopiesRunWithAnalysisOff: with analysis.auto off and the check on,
// a completed crawl gets its near-copy lists — and nothing else of the
// analysis: no link scores, no issues.
func TestNearCopiesRunWithAnalysisOff(t *testing.T) {
	st, base, out := crawlWith(t, berlinCfg(), true)
	if out.Status != store.StatusCompleted || out.Analyzed || !out.NearCopies || out.NearDups != 2 {
		t.Errorf("outcome = %+v, want completed, not analyzed, near copies on 2 pages", out)
	}
	list, count := nearCopiesOf(t, st, base+"/a")
	if !list.Valid || !strings.Contains(list.String, base+"/b") || count.Int64 != 1 {
		t.Errorf("/a near copies = %v %v, want /b", list, count)
	}
	// /c was compared and has none: an empty list, not null.
	if list, count := nearCopiesOf(t, st, base+"/c"); list.String != "[]" || !count.Valid || count.Int64 != 0 {
		t.Errorf("/c near copies = %v %v, want [] and 0", list, count)
	}
	// The home page has under 50 words: not compared.
	if list, count := nearCopiesOf(t, st, base+"/"); list.Valid || count.Valid {
		t.Errorf("/ near copies = %v %v, want null (under min_words)", list, count)
	}
	var scored, issues int
	st.DB().QueryRow(`SELECT COUNT(*) FROM page_metrics WHERE link_score != 0`).Scan(&scored)
	st.DB().QueryRow(`SELECT COUNT(*) FROM issues`).Scan(&issues)
	if scored != 0 || issues != 0 {
		t.Errorf("the analysis ran: %d link scores, %d issues", scored, issues)
	}
	s, err := store.NearCopySettings(st.DB())
	if err != nil || s == nil || s.Threshold != 80 || s.MinWords != 50 || !s.IndexableOnly {
		t.Errorf("settings = %+v (err %v)", s, err)
	}
}

// TestNearCopiesWaitForCompletion: a paused (interrupted) crawl has no lists
// yet; the finalize that completes it — a resume's — works them out once, so
// the bundle after it carries them.
func TestNearCopiesWaitForCompletion(t *testing.T) {
	cfg := berlinCfg()
	st, base, out := crawlWith(t, cfg, false)
	if out.NearCopies {
		t.Error("an interrupted crawl ran the near-copy step")
	}
	if list, _ := nearCopiesOf(t, st, base+"/a"); list.Valid {
		t.Errorf("/a has near copies before completion: %v", list)
	}
	// The resume: a crawler over the same store, which has every page already
	// (the pause came after the last one), drains at once; its finalize
	// completes the crawl.
	seed := base + "/"
	c, err := crawler.New(cfg, crawler.WithSink(st), crawler.WithResume(crawler.Resume{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Dir(st.TempDir()) // the store dir holds crawls/
	out, err = Crawl(c, st, res, Params{StoreDir: storeDir, Cfg: cfg, Seeds: []string{seed}, Resumed: true, Completed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != store.StatusCompleted || !out.NearCopies || out.NearDups != 2 {
		t.Errorf("completing finalize = %+v", out)
	}
	if list, _ := nearCopiesOf(t, st, base+"/a"); !strings.Contains(list.String, base+"/b") {
		t.Errorf("/a near copies after completion = %v", list)
	}
}

// TestNearCopiesOffLeavesNothing: with the check off nothing is compared —
// every list reads null, and no settings are recorded — and a re-analysis that
// turns the check off clears an earlier run's lists rather than leaving them.
func TestNearCopiesOffLeavesNothing(t *testing.T) {
	cfg := berlinCfg()
	cfg.Content.NearDuplicates.Enabled = false
	st, base, out := crawlWith(t, cfg, true)
	if out.NearCopies {
		t.Error("the step ran with the check off")
	}
	assertNotCompared := func(when string) {
		t.Helper()
		var n int
		st.DB().QueryRow(`SELECT COUNT(*) FROM page_metrics WHERE near_copies IS NOT NULL OR near_copy_count IS NOT NULL
			OR closest_similarity != 0 OR near_dup_count != 0`).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d pages have near-copy fields", when, n)
		}
		if s, _ := store.NearCopySettings(st.DB()); s != nil {
			t.Errorf("%s: settings recorded: %+v", when, s)
		}
	}
	assertNotCompared("check off")

	on := berlinCfg()
	on.Analysis.Auto = true
	if _, err := Analyze(st, on); err != nil {
		t.Fatal(err)
	}
	if list, _ := nearCopiesOf(t, st, base+"/a"); !strings.Contains(list.String, base+"/b") {
		t.Fatalf("re-analysis with the check on found no near copies: %v", list)
	}
	if _, err := Analyze(st, cfg); err != nil {
		t.Fatal(err)
	}
	assertNotCompared("re-analysis with the check off")
}

// TestNearCopiesWithTheAnalysisMatchOnTheirOwn: the step gives the same lists
// whether it runs in the analysis pass or on its own.
func TestNearCopiesWithTheAnalysisMatchOnTheirOwn(t *testing.T) {
	st, _, _ := crawlWith(t, berlinCfg(), true)
	alone := nearCopyRows(t, st)
	withAnalysis := berlinCfg()
	withAnalysis.Analysis.Auto = true
	if _, err := Analyze(st, withAnalysis); err != nil {
		t.Fatal(err)
	}
	if got := nearCopyRows(t, st); strings.Join(got, "\n") != strings.Join(alone, "\n") {
		t.Errorf("analysis pass != step alone\n alone:    %v\n analysis: %v", alone, got)
	}
}
