package compare

import (
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/parse"
)

func rec(url, title string) *crawler.PageRecord {
	return &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html", Indexable: true,
		Facts: &parse.Facts{Titles: []string{title}, WordCount: 100}}
}

func pagesOf(recs ...*crawler.PageRecord) map[string]*crawler.PageRecord {
	m := map[string]*crawler.PageRecord{}
	for _, r := range recs {
		m[r.URL] = r
	}
	return m
}

func delta(r *Result, id string) *Delta {
	for i := range r.Deltas {
		if r.Deltas[i].IssueID == id {
			return &r.Deltas[i]
		}
	}
	return nil
}

func TestIssueDeltas(t *testing.T) {
	prev := Input{
		Pages: pagesOf(rec("https://ex.com/a", "A"), rec("https://ex.com/b", "B"), rec("https://ex.com/gone", "G")),
		Issues: map[string][]string{
			"title_missing": {"https://ex.com/a", "https://ex.com/gone"},
		},
	}
	curr := Input{
		Pages: pagesOf(rec("https://ex.com/a", "A"), rec("https://ex.com/b", "B"), rec("https://ex.com/fresh", "F")),
		Issues: map[string][]string{
			"title_missing": {"https://ex.com/b", "https://ex.com/fresh"},
		},
	}
	r, err := Run(prev, curr, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	d := delta(r, "title_missing")
	if d == nil {
		t.Fatal("no delta for title_missing")
	}
	// /b exists in both, newly has the issue -> Added
	if len(d.Added) != 1 || d.Added[0] != "https://ex.com/b" {
		t.Errorf("Added = %v", d.Added)
	}
	// /fresh exists only in current -> New
	if len(d.New) != 1 || d.New[0] != "https://ex.com/fresh" {
		t.Errorf("New = %v", d.New)
	}
	// /a exists in both, no longer has the issue -> Removed
	if len(d.Removed) != 1 || d.Removed[0] != "https://ex.com/a" {
		t.Errorf("Removed = %v", d.Removed)
	}
	// /gone existed only in previous -> Missing
	if len(d.Missing) != 1 || d.Missing[0] != "https://ex.com/gone" {
		t.Errorf("Missing = %v", d.Missing)
	}
	if len(r.NewPages) != 1 || r.NewPages[0] != "https://ex.com/fresh" {
		t.Errorf("NewPages = %v", r.NewPages)
	}
	if len(r.MissingPages) != 1 || r.MissingPages[0] != "https://ex.com/gone" {
		t.Errorf("MissingPages = %v", r.MissingPages)
	}
}

func TestChangeDetection(t *testing.T) {
	prev := Input{Pages: pagesOf(rec("https://ex.com/a", "Old Title"))}
	curr := Input{Pages: pagesOf(rec("https://ex.com/a", "New Title"))}
	r, err := Run(prev, curr, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || r.Changes[0].Element != "titles" ||
		r.Changes[0].Previous != "Old Title" || r.Changes[0].Current != "New Title" {
		t.Errorf("changes = %+v", r.Changes)
	}

	// disabled element: no change reported
	cfg := config.Default()
	cfg.Compare.ChangeDetection = []string{"h1"}
	r, _ = Run(prev, curr, cfg)
	if len(r.Changes) != 0 {
		t.Errorf("changes with titles disabled = %+v", r.Changes)
	}
}

func TestURLMapping(t *testing.T) {
	prev := Input{Pages: pagesOf(rec("https://staging.ex.com/a", "Same"))}
	curr := Input{Pages: pagesOf(rec("https://www.ex.com/a", "Same"))}
	cfg := config.Default()
	cfg.Compare.URLMapping = []config.URLMapping{
		{Pattern: `^https://staging\.`, Replace: "https://www."},
	}
	r, err := Run(prev, curr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.NewPages) != 0 || len(r.MissingPages) != 0 {
		t.Errorf("mapped URLs must align: new=%v missing=%v", r.NewPages, r.MissingPages)
	}
}
