package compare

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
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

// contentRec builds an internal HTML page with the given content-area text and
// whole-body hash (Facts.Hash is the md5 of the full response, distinct from
// the content-area text so a footer-only edit changes the hash but not content).
func contentRec(url, text, hash string) *crawler.PageRecord {
	return &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html", Indexable: true,
		Facts: &parse.Facts{ContentText: text, Hash: hash}}
}

// sdRec builds an internal HTML page carrying the given schema.org types. A nil
// types slice means no structured data was extracted at all.
func sdRec(url string, types []string) *crawler.PageRecord {
	r := &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html", Indexable: true,
		Facts: &parse.Facts{}}
	if types != nil {
		r.StructuredData = &structured.PageData{Types: types}
	}
	return r
}

func change(r *Result, url, element string) *Change {
	for i := range r.Changes {
		if r.Changes[i].URL == url && r.Changes[i].Element == element {
			return &r.Changes[i]
		}
	}
	return nil
}

func TestContentChangeDetection(t *testing.T) {
	const url = "https://ex.com/p"
	const alpha = "the quick brown fox jumps over the lazy dog beneath a pale winter moon"
	const beta = "distributed consensus protocols tolerate faults by replicating an ordered command log"

	// Material content change (disjoint text, different body) is reported, and
	// the change carries the similarity for the user.
	r, err := Run(
		Input{Pages: pagesOf(contentRec(url, alpha, "h1"))},
		Input{Pages: pagesOf(contentRec(url, beta, "h2"))},
		config.Default())
	if err != nil {
		t.Fatal(err)
	}
	ch := change(r, url, "content")
	if ch == nil {
		t.Fatalf("material content change not reported: %+v", r.Changes)
	}
	if !strings.Contains(ch.Current, "% similar") {
		t.Errorf("content change Current = %q, want an \"N%% similar\" value", ch.Current)
	}

	// Identical content and body: no change.
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, alpha, "h1"))},
		Input{Pages: pagesOf(contentRec(url, alpha, "h1"))},
		config.Default())
	if change(r, url, "content") != nil {
		t.Errorf("identical content reported a change: %+v", r.Changes)
	}

	// Footer-only edit: the body hash changed but the content area is identical,
	// so no content change is reported (we measure content-area text, not body).
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, alpha, "body-v1"))},
		Input{Pages: pagesOf(contentRec(url, alpha, "body-v2"))},
		config.Default())
	if change(r, url, "content") != nil {
		t.Errorf("footer-only edit reported a content change: %+v", r.Changes)
	}

	// Threshold gating: a huge threshold suppresses even a total rewrite.
	cfg := config.Default()
	cfg.Compare.ContentChangeThreshold = 100
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, alpha, "h1"))},
		Input{Pages: pagesOf(contentRec(url, beta, "h2"))},
		cfg)
	if change(r, url, "content") != nil {
		t.Errorf("threshold=100 still reported a content change: %+v", r.Changes)
	}

	// Disabled element: no content change even on a total rewrite.
	cfg = config.Default()
	cfg.Compare.ChangeDetection = []string{"titles"}
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, alpha, "h1"))},
		Input{Pages: pagesOf(contentRec(url, beta, "h2"))},
		cfg)
	if change(r, url, "content") != nil {
		t.Errorf("content disabled but change reported: %+v", r.Changes)
	}

	// Content appears where there was none, and vanishes: both are changes.
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, "", "empty"))},
		Input{Pages: pagesOf(contentRec(url, beta, "full"))},
		config.Default())
	if change(r, url, "content") == nil {
		t.Errorf("empty->content not reported: %+v", r.Changes)
	}
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, beta, "full"))},
		Input{Pages: pagesOf(contentRec(url, "", "empty"))},
		config.Default())
	if change(r, url, "content") == nil {
		t.Errorf("content->empty not reported: %+v", r.Changes)
	}

	// Nil Facts on either side: content lives inside Facts, so the comparison is
	// skipped (no change, no panic) — the intentional nil-safety contract.
	r, _ = Run(
		Input{Pages: pagesOf(&crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled})},
		Input{Pages: pagesOf(contentRec(url, beta, "full"))},
		config.Default())
	if change(r, url, "content") != nil {
		t.Errorf("nil Facts should skip content detection: %+v", r.Changes)
	}
}

// TestContentChangeThresholdBoundary pins the threshold direction and the
// inclusive boundary of the content detector independently of the exact minhash
// value: a partially-overlapping pair must fire just below its own change
// magnitude and stay silent at or above it, and an identical pair must not fire
// even at threshold 0 (the `<=` boundary at equality — a flip to `<` would
// wrongly report unchanged content).
func TestContentChangeThresholdBoundary(t *testing.T) {
	const url = "https://ex.com/p"
	// Shares its first nine words, then diverges -> intermediate similarity.
	const base = "the quick brown fox jumps over the lazy dog near the river bank at dawn"
	const variant = "the quick brown fox jumps over the lazy dog beside a busy city street tonight"

	chg := 100 - analyze.ContentSimilarity(base, variant) // the % the content moved
	if chg <= 5 || chg >= 95 {
		t.Fatalf("need an intermediate change magnitude for a boundary test, got %.1f%%", chg)
	}

	// Threshold below the change magnitude: reported.
	cfg := config.Default()
	cfg.Compare.ContentChangeThreshold = int(chg) - 2
	r, _ := Run(
		Input{Pages: pagesOf(contentRec(url, base, "h1"))},
		Input{Pages: pagesOf(contentRec(url, variant, "h2"))},
		cfg)
	if change(r, url, "content") == nil {
		t.Errorf("change %.1f%% > threshold %d should report: %+v", chg, cfg.Compare.ContentChangeThreshold, r.Changes)
	}

	// Threshold at or above the change magnitude: not reported.
	cfg.Compare.ContentChangeThreshold = int(chg) + 2
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, base, "h1"))},
		Input{Pages: pagesOf(contentRec(url, variant, "h2"))},
		cfg)
	if change(r, url, "content") != nil {
		t.Errorf("change %.1f%% <= threshold %d should be silent: %+v", chg, cfg.Compare.ContentChangeThreshold, r.Changes)
	}

	// Identical content at threshold 0: 0 <= 0 must stay silent.
	cfg.Compare.ContentChangeThreshold = 0
	r, _ = Run(
		Input{Pages: pagesOf(contentRec(url, base, "same"))},
		Input{Pages: pagesOf(contentRec(url, base, "same"))},
		cfg)
	if change(r, url, "content") != nil {
		t.Errorf("identical content at threshold 0 reported a change: %+v", r.Changes)
	}
}

func TestStructuredDataChangeDetection(t *testing.T) {
	const url = "https://ex.com/p"

	// A new schema.org type is reported, normalized as a sorted unique set.
	r, err := Run(
		Input{Pages: pagesOf(sdRec(url, []string{"Article"}))},
		Input{Pages: pagesOf(sdRec(url, []string{"BreadcrumbList", "Article"}))},
		config.Default())
	if err != nil {
		t.Fatal(err)
	}
	ch := change(r, url, "structured_data")
	if ch == nil {
		t.Fatalf("structured data type change not reported: %+v", r.Changes)
	}
	if ch.Previous != "Article" || ch.Current != "Article, BreadcrumbList" {
		t.Errorf("structured_data change = %q -> %q, want \"Article\" -> \"Article, BreadcrumbList\"", ch.Previous, ch.Current)
	}

	// Same unique types in a different order with duplicates: no change.
	r, _ = Run(
		Input{Pages: pagesOf(sdRec(url, []string{"BreadcrumbList", "Article"}))},
		Input{Pages: pagesOf(sdRec(url, []string{"Article", "Article", "BreadcrumbList"}))},
		config.Default())
	if change(r, url, "structured_data") != nil {
		t.Errorf("equal type sets reported a change: %+v", r.Changes)
	}

	// No structured data on either side: no change.
	r, _ = Run(
		Input{Pages: pagesOf(sdRec(url, nil))},
		Input{Pages: pagesOf(sdRec(url, nil))},
		config.Default())
	if change(r, url, "structured_data") != nil {
		t.Errorf("absent structured data reported a change: %+v", r.Changes)
	}

	// Structured data appears where there was none: reported.
	r, _ = Run(
		Input{Pages: pagesOf(sdRec(url, nil))},
		Input{Pages: pagesOf(sdRec(url, []string{"Product"}))},
		config.Default())
	ch = change(r, url, "structured_data")
	if ch == nil || ch.Previous != "" || ch.Current != "Product" {
		t.Errorf("appearance change = %+v, want \"\" -> \"Product\"", ch)
	}

	// Disabled element: no change.
	cfg := config.Default()
	cfg.Compare.ChangeDetection = []string{"titles"}
	r, _ = Run(
		Input{Pages: pagesOf(sdRec(url, []string{"Article"}))},
		Input{Pages: pagesOf(sdRec(url, []string{"Product"}))},
		cfg)
	if change(r, url, "structured_data") != nil {
		t.Errorf("structured_data disabled but change reported: %+v", r.Changes)
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
