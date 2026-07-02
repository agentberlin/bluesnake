package store

// #75 bugs 1+2: the issues table and the per-page analysis columns have TWO
// writers — the catalogue evaluation (SaveIssues) and the analysis phase
// (SaveAnalysis) — and each must atomically replace EXACTLY the state it owns.
// These tests pin that partition at the store layer: a catalogue-only refresh
// (the `issues` command) must not wipe analysis-phase rows, a re-analysis must
// not leave stale analysis rows or stale per-page metric columns behind, and
// neither writer may touch the other's partition.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
)

// TestIssuesCmdPreservesAnalysisIssues (#75 bug 1, store half): SaveIssues
// replaces only the catalogue-evaluation partition; the analysis-phase rows
// (redirect_chain, content_near_duplicate, ...) survive a catalogue refresh,
// and a re-analysis (SaveAnalysis) replaces exactly the analysis partition —
// stale analysis rows do not accumulate and catalogue rows survive it.
func TestSaveIssuesPreservesAnalysisPartition(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.SaveIssues([]issues.Occurrence{
		{URL: "https://ex.com/", IssueID: "title_missing"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveAnalysis(&analyze.Results{Occurrences: []issues.Occurrence{
		{URL: "https://ex.com/r", IssueID: "redirect_chain", Detail: "3 hops"},
		{URL: "https://ex.com/a", IssueID: "content_near_duplicate", Detail: "closest match https://ex.com/b (95%)"},
	}}); err != nil {
		t.Fatal(err)
	}

	// A catalogue-only refresh (what the `issues` command runs) must leave the
	// analysis-phase rows byte-identical while replacing its own partition.
	if err := c.SaveIssues([]issues.Occurrence{
		{URL: "https://ex.com/", IssueID: "title_missing"},
		{URL: "https://ex.com/b", IssueID: "h1_missing"},
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := c.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["redirect_chain"] != 1 || counts["content_near_duplicate"] != 1 {
		t.Errorf("catalogue refresh wiped analysis-phase rows: %v", counts)
	}
	if counts["title_missing"] != 1 || counts["h1_missing"] != 1 {
		t.Errorf("catalogue partition not replaced: %v", counts)
	}

	// A re-analysis replaces the analysis partition: the chain shrank to 2 hops
	// and near-duplicates were turned off, so the old rows must be gone — while
	// the catalogue partition survives untouched.
	if err := c.SaveAnalysis(&analyze.Results{Occurrences: []issues.Occurrence{
		{URL: "https://ex.com/r", IssueID: "redirect_chain", Detail: "2 hops"},
	}}); err != nil {
		t.Fatal(err)
	}
	counts, err = c.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["content_near_duplicate"] != 0 {
		t.Errorf("re-analysis left a stale near-duplicate row: %v", counts)
	}
	if counts["redirect_chain"] != 1 {
		t.Errorf("re-analysis did not keep exactly the new chain row: %v", counts)
	}
	var detail string
	if err := c.db.QueryRow(`SELECT detail FROM issues WHERE issue = 'redirect_chain'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != "2 hops" {
		t.Errorf("redirect_chain detail = %q, want the re-analysis value \"2 hops\" (stale row survived)", detail)
	}
	if counts["title_missing"] != 1 || counts["h1_missing"] != 1 {
		t.Errorf("re-analysis touched the catalogue partition: %v", counts)
	}
}

// TestReanalyzeClearsStaleScoreColumns (#75 bug 2): SaveAnalysis resets the
// analysis-owned per-page columns to their schema defaults before applying the
// new result maps, so a page that dropped out of the new result set (near-dup
// disabled, link-score changes) reads as never-computed instead of carrying the
// previous run's values.
func TestReanalyzeClearsStaleScoreColumns(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, url := range []string{"https://ex.com/a", "https://ex.com/b"} {
		if err := c.Page(&crawler.PageRecord{URL: url, Scope: "internal",
			State: crawler.StateCrawled, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.SaveAnalysis(&analyze.Results{
		LinkScores: map[string]float64{"https://ex.com/a": 80, "https://ex.com/b": 100},
		UniqueIn:   map[string]int{"https://ex.com/a": 3},
		UniqueOut:  map[string]int{"https://ex.com/a": 5},
		NearDups: map[string]analyze.NearDup{
			"https://ex.com/a": {ClosestMatch: "https://ex.com/b", ClosestSimilarity: 95, Count: 1},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Re-analyze with near-duplicates off and /a out of every result map.
	if err := c.SaveAnalysis(&analyze.Results{
		LinkScores: map[string]float64{"https://ex.com/b": 100},
	}); err != nil {
		t.Fatal(err)
	}

	var linkScore, closest float64
	var uniqueIn, uniqueOut, nearDupCount int
	if err := c.db.QueryRow(`SELECT link_score, unique_inlinks, unique_outlinks,
		closest_similarity, near_dup_count FROM pages WHERE url = 'https://ex.com/a'`).
		Scan(&linkScore, &uniqueIn, &uniqueOut, &closest, &nearDupCount); err != nil {
		t.Fatal(err)
	}
	if linkScore != 0 || uniqueIn != 0 || uniqueOut != 0 || closest != 0 || nearDupCount != 0 {
		t.Errorf("stale analysis columns on /a after re-analysis: link_score=%v unique_in=%d unique_out=%d closest=%v near_dups=%d — all must reset to defaults",
			linkScore, uniqueIn, uniqueOut, closest, nearDupCount)
	}
	var bScore float64
	if err := c.db.QueryRow(`SELECT link_score FROM pages WHERE url = 'https://ex.com/b'`).Scan(&bScore); err != nil {
		t.Fatal(err)
	}
	if bScore != 100 {
		t.Errorf("link_score(/b) = %v, want 100 (new result map applied after the reset)", bScore)
	}
}
