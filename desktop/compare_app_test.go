package main

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// seedCompareCrawl registers a completed crawl holding the given pages and
// title_missing occurrences.
func seedCompareCrawl(t *testing.T, dir string, pages []*crawler.PageRecord, issueURLs []string) string {
	t.Helper()
	st, err := store.CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, pg := range pages {
		if err := st.Page(pg); err != nil {
			t.Fatal(err)
		}
	}
	var occs []issues.Occurrence
	for _, u := range issueURLs {
		occs = append(occs, issues.Occurrence{URL: u, IssueID: "title_missing"})
	}
	if err := st.SaveIssues(nil, occs); err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusCompleted, len(pages), len(pages)); err != nil {
		t.Fatal(err)
	}
	return id
}

func page(url string, status int, indexable bool, title string) *crawler.PageRecord {
	pg := &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: status, ContentType: "text/html", Indexable: indexable}
	if title != "" {
		pg.Facts = &parse.Facts{Titles: []string{title}, WordCount: 100}
	}
	return pg
}

// The enriched compare payload: page briefs, always-on state changes, issue
// deltas decorated from the catalogue, and the cache-first round-trip.
func TestCompareCrawlsPayloadAndCache(t *testing.T) {
	a := testApp(t)
	prevID := seedCompareCrawl(t, a.storeDir, []*crawler.PageRecord{
		page("https://ex.com/a", 200, true, "Alpha"),
		page("https://ex.com/b", 200, true, "Old Title B"),
		page("https://ex.com/gone", 200, true, "Doomed"),
	}, []string{"https://ex.com/gone"})
	currID := seedCompareCrawl(t, a.storeDir, []*crawler.PageRecord{
		page("https://ex.com/a", 404, false, ""),
		page("https://ex.com/b", 200, true, "New Title B"),
		page("https://ex.com/fresh", 200, true, "Fresh"),
	}, []string{"https://ex.com/fresh"})

	p, err := a.CompareCrawls(prevID, currID, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Cached {
		t.Error("first comparison claimed to be cached")
	}
	if p.PagesPrev != 3 || p.PagesCurr != 3 {
		t.Errorf("pages = %d -> %d, want 3 -> 3", p.PagesPrev, p.PagesCurr)
	}
	if p.NewCount != 1 || len(p.NewPages) != 1 || p.NewPages[0].URL != "https://ex.com/fresh" ||
		p.NewPages[0].Status != 200 || p.NewPages[0].Title != "Fresh" {
		t.Errorf("NewPages = %+v", p.NewPages)
	}
	if p.MissingCount != 1 || len(p.MissingPages) != 1 || p.MissingPages[0].URL != "https://ex.com/gone" ||
		p.MissingPages[0].Title != "Doomed" {
		t.Errorf("MissingPages = %+v", p.MissingPages)
	}
	// /a went 200 -> 404 and lost indexability
	if p.StateChangeCount != 1 || len(p.StateChanges) != 1 {
		t.Fatalf("StateChanges = %+v", p.StateChanges)
	}
	if p.StatusFlipCount != 1 || p.IndexFlipCount != 1 {
		t.Errorf("flip counts = %d/%d, want 1/1", p.StatusFlipCount, p.IndexFlipCount)
	}
	sc := p.StateChanges[0]
	if sc.URL != "https://ex.com/a" || sc.PrevStatus != 200 || sc.CurrStatus != 404 ||
		!sc.PrevIndexable || sc.CurrIndexable {
		t.Errorf("state change = %+v", sc)
	}
	// /b's title changed
	foundTitle := false
	for _, c := range p.ElementChanges {
		if c.URL == "https://ex.com/b" && c.Element == "titles" &&
			c.Previous == "Old Title B" && c.Current == "New Title B" {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Errorf("title change missing: %+v", p.ElementChanges)
	}
	// issue delta decorated from the catalogue with both totals
	var td *compare.IssueMovement
	for i := range p.IssueDeltas {
		if p.IssueDeltas[i].ID == "title_missing" {
			td = &p.IssueDeltas[i]
		}
	}
	if td == nil {
		t.Fatalf("no title_missing delta: %+v", p.IssueDeltas)
	}
	def, _ := issues.Lookup("title_missing")
	if td.Name != def.Name || td.Severity != string(def.Severity) {
		t.Errorf("delta decoration = %q/%q, want %q/%q", td.Name, td.Severity, def.Name, def.Severity)
	}
	if td.PrevCount != 1 || td.CurrCount != 1 || td.NewCount != 1 || td.MissingCount != 1 {
		t.Errorf("delta counts = %+v", td)
	}
	// distributions follow the overview's bucketing
	if p.StatusMixPrev["2xx"] != 3 || p.StatusMixCurr["2xx"] != 2 || p.StatusMixCurr["4xx"] != 1 {
		t.Errorf("status mix = %v -> %v", p.StatusMixPrev, p.StatusMixCurr)
	}
	if p.IndexablePrev != 3 || p.IndexableCurr != 2 || p.NonIndexableCurr != 1 {
		t.Errorf("indexability = %d/%d -> %d/%d", p.IndexablePrev, p.NonIndexablePrev, p.IndexableCurr, p.NonIndexableCurr)
	}

	// second call is served from the cache; force recomputes; delete drops it
	p2, err := a.CompareCrawls(prevID, currID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p2.Cached || p2.NewCount != 1 || p2.IssueDeltas[0].Name != p.IssueDeltas[0].Name {
		t.Errorf("cached payload: cached=%v %+v", p2.Cached, p2)
	}
	p3, err := a.CompareCrawls(prevID, currID, true)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Cached {
		t.Error("force still served the cache")
	}
	if err := a.DeleteComparison(prevID, currID); err != nil {
		t.Fatal(err)
	}
	if p4, _ := a.CompareCrawls(prevID, currID, false); p4.Cached {
		t.Error("cache row survived DeleteComparison")
	}

	// invalidate (delete / resume / re-analyze path) purges the pair too
	a.invalidate(currID)
	if p5, _ := a.CompareCrawls(prevID, currID, false); p5.Cached {
		t.Error("cache row survived invalidate")
	}
}
