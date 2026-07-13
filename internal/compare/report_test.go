package compare

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

func statusRec(url string, status int, indexable bool, title string) *crawler.PageRecord {
	r := rec(url, title)
	r.StatusCode, r.Indexable = status, indexable
	if title == "" {
		r.Facts = nil
	}
	return r
}

func TestBuildReport(t *testing.T) {
	prev := Input{
		Pages: pagesOf(
			statusRec("https://ex.com/a", 200, true, "Alpha"),
			statusRec("https://ex.com/b", 200, true, "Old B"),
			statusRec("https://ex.com/gone", 200, true, "Doomed"),
		),
		Issues: map[string][]string{"title_missing": {"https://ex.com/gone"}},
	}
	curr := Input{
		Pages: pagesOf(
			statusRec("https://ex.com/a", 404, false, ""),
			statusRec("https://ex.com/b", 200, true, "New B"),
			statusRec("https://ex.com/fresh", 200, true, "Fresh"),
		),
		Issues: map[string][]string{"title_missing": {"https://ex.com/fresh"}},
	}
	r, err := BuildReport(prev, curr, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != ReportVersion || r.PagesPrev != 3 || r.PagesCurr != 3 {
		t.Errorf("header = v%d %d->%d", r.Version, r.PagesPrev, r.PagesCurr)
	}
	if r.NewCount != 1 || r.NewPages[0].URL != "https://ex.com/fresh" || r.NewPages[0].Title != "Fresh" || r.NewPages[0].Status != 200 {
		t.Errorf("NewPages = %+v", r.NewPages)
	}
	if r.MissingCount != 1 || r.MissingPages[0].URL != "https://ex.com/gone" || r.MissingPages[0].Title != "Doomed" {
		t.Errorf("MissingPages = %+v", r.MissingPages)
	}
	if r.StatusFlipCount != 1 || r.IndexFlipCount != 1 || r.StateChangeCount != 1 {
		t.Errorf("flips = %d/%d state=%d", r.StatusFlipCount, r.IndexFlipCount, r.StateChangeCount)
	}
	// /b's title changed — element changes ride through as compare.Change
	found := false
	for _, c := range r.ElementChanges {
		if c.URL == "https://ex.com/b" && c.Element == "titles" && c.Previous == "Old B" && c.Current == "New B" {
			found = true
		}
	}
	if !found {
		t.Errorf("title change missing: %+v", r.ElementChanges)
	}
	// issue movement decorated from the catalogue with both totals
	if len(r.IssueDeltas) != 1 {
		t.Fatalf("IssueDeltas = %+v", r.IssueDeltas)
	}
	d := r.IssueDeltas[0]
	def, _ := issues.Lookup("title_missing")
	if d.ID != "title_missing" || d.Name != def.Name || d.Severity != string(def.Severity) {
		t.Errorf("decoration = %+v", d)
	}
	if d.PrevCount != 1 || d.CurrCount != 1 || d.NewCount != 1 || d.MissingCount != 1 || d.AddedCount != 0 || d.RemovedCount != 0 {
		t.Errorf("buckets = %+v", d)
	}
	// distributions mirror the overview's bucketing
	if r.StatusMixPrev["2xx"] != 3 || r.StatusMixCurr["2xx"] != 2 || r.StatusMixCurr["4xx"] != 1 {
		t.Errorf("status mix = %v -> %v", r.StatusMixPrev, r.StatusMixCurr)
	}
	if r.IndexablePrev != 3 || r.IndexableCurr != 2 || r.NonIndexableCurr != 1 {
		t.Errorf("indexability = %d/%d -> %d/%d", r.IndexablePrev, r.NonIndexablePrev, r.IndexableCurr, r.NonIndexableCurr)
	}
}

// seedCrawl registers a completed crawl with pages and title_missing rows.
func seedCrawl(t *testing.T, dir string, pages []*crawler.PageRecord, issueURLs []string) string {
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

// CachedReport: computes + stores on first call, serves the row after, force
// recomputes, and a stale-version row reads as a miss.
func TestCachedReport(t *testing.T) {
	dir := t.TempDir()
	prevID := seedCrawl(t, dir, []*crawler.PageRecord{
		statusRec("https://ex.com/a", 200, true, "Alpha"),
		statusRec("https://ex.com/gone", 200, true, "Doomed"),
	}, []string{"https://ex.com/gone"})
	currID := seedCrawl(t, dir, []*crawler.PageRecord{
		statusRec("https://ex.com/a", 404, false, ""),
		statusRec("https://ex.com/fresh", 200, true, "Fresh"),
	}, []string{"https://ex.com/fresh"})

	r, err := CachedReport(dir, prevID, currID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cached {
		t.Error("first report claimed to be cached")
	}
	if r.PrevID != prevID || r.CurrID != currID || r.ComputedAt == "" {
		t.Errorf("provenance = %+v", r)
	}
	if r.NewCount != 1 || r.MissingCount != 1 || r.StatusFlipCount != 1 {
		t.Errorf("report = %+v", r)
	}

	r2, err := CachedReport(dir, prevID, currID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Cached || r2.NewCount != 1 || len(r2.IssueDeltas) != len(r.IssueDeltas) {
		t.Errorf("cached read = cached:%v %+v", r2.Cached, r2)
	}

	r3, err := CachedReport(dir, prevID, currID, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r3.Cached {
		t.Error("force still served the cache")
	}

	// a row from an older payload shape is a miss, not garbage
	if err := store.SaveComparison(dir, prevID, currID, []byte(`{"v":1,"new_count":99}`)); err != nil {
		t.Fatal(err)
	}
	r4, err := CachedReport(dir, prevID, currID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r4.Cached || r4.NewCount != 1 {
		t.Errorf("stale-version row served: cached:%v new:%d", r4.Cached, r4.NewCount)
	}

	// custom loader override is honoured (the desktop's page-cache path)
	loads := 0
	loader := func(id string) (Input, error) { loads++; return LoadInput(dir, id) }
	if _, err := CachedReport(dir, prevID, currID, true, loader); err != nil {
		t.Fatal(err)
	}
	if loads != 2 {
		t.Errorf("custom loader called %d times, want 2", loads)
	}

	// unknown crawl id surfaces as an error
	if _, err := CachedReport(dir, prevID, "nope", true, nil); err == nil {
		t.Error("unknown crawl id accepted")
	}
}
