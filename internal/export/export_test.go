package export

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/extract"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/structured"
)

func seededStore(t *testing.T) *store.Crawl {
	t.Helper()
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	recs := []*crawler.PageRecord{
		{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html", Indexable: true,
			Facts: &parse.Facts{Titles: []string{"Home"}, Descriptions: []string{"d"},
				H1s: []string{"H"}, WordCount: 100,
				HreflangHTML: []parse.Hreflang{{Lang: "de", URL: "https://ex.com/de"}},
				Links:        []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.com/a", Anchor: "a"}},
			}},
		{URL: "https://ex.com/a", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 404, Status: "Not Found", ContentType: "text/html"},
		{URL: "https://other.com/x", Scope: "external", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK"},
		{URL: "https://ex.com/img.png", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, ContentType: "image/png", Size: 4096, Indexable: true},
	}
	for _, r := range recs {
		if err := st.Page(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveIssues([]issues.Occurrence{
		{URL: "https://ex.com/a", IssueID: "internal_client_error"},
		{URL: "https://ex.com/", IssueID: "security_missing_hsts"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAnalysis(&analyze.Results{
		LinkScores: map[string]float64{"https://ex.com/": 100},
		UniqueIn:   map[string]int{"https://ex.com/a": 1},
		UniqueOut:  map[string]int{"https://ex.com/": 1},
		NearDups:   map[string]analyze.NearDup{},
		Chains: []analyze.Chain{
			{Type: "redirect", Source: "https://ex.com/r", Hops: []string{"https://ex.com/r2", "https://ex.com/"},
				Final: "https://ex.com/", FinalStatus: 200},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func find(d *Dataset, col int, value string) bool {
	for _, row := range d.Rows {
		if col < len(row) && row[col] == value {
			return true
		}
	}
	return false
}

func TestTabExports(t *testing.T) {
	st := seededStore(t)

	internal, err := Build(st, "internal", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(internal.Rows) != 3 { // /, /a, /img.png
		t.Errorf("internal rows = %d", len(internal.Rows))
	}
	if !find(internal, 0, "https://ex.com/") || find(internal, 0, "https://other.com/x") {
		t.Error("internal tab scope wrong")
	}

	external, _ := Build(st, "external", "")
	if len(external.Rows) != 1 {
		t.Errorf("external rows = %d", len(external.Rows))
	}

	titles, _ := Build(st, "titles", "")
	if !find(titles, 1, "Home") {
		t.Error("titles tab missing Home")
	}

	images, _ := Build(st, "images", "")
	if len(images.Rows) != 1 || images.Rows[0][0] != "https://ex.com/img.png" {
		t.Errorf("images = %+v", images.Rows)
	}

	hreflang, err := BuildAny(st, "hreflang", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hreflang.Rows) != 1 || hreflang.Rows[0][1] != "de" {
		t.Errorf("hreflang = %+v", hreflang.Rows)
	}

	links, _ := Build(st, "links", "")
	if len(links.Rows) != 1 || links.Rows[0][1] != "https://ex.com/a" {
		t.Errorf("links = %+v", links.Rows)
	}

	iss, _ := Build(st, "issues", "")
	if len(iss.Rows) != 2 {
		t.Errorf("issues rows = %d", len(iss.Rows))
	}

	if _, err := Build(st, "nonsense", ""); err == nil {
		t.Error("unknown tab must error")
	}
}

// TestIssuesExportListsEveryDetail pins the real path end to end: a page whose
// schema.org validation reports two missing required properties produces two
// structured_validation_error occurrences (issues.Evaluate), both of which must
// survive storage and appear in the issues export — while the per-issue
// affected-URL count stays per-URL. This is the regression the (url, issue, detail)
// key exists for; the old (url, issue) key kept only the last property.
func TestIssuesExportListsEveryDetail(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	pages := map[string]*crawler.PageRecord{
		"https://ex.com/recipe": {
			URL: "https://ex.com/recipe", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html", Indexable: true,
			Facts: &parse.Facts{Titles: []string{"Recipe"}, H1s: []string{"Recipe"}},
			StructuredData: &structured.PageData{
				Formats: []string{"jsonld"}, Types: []string{"Recipe"},
				Errors: []string{
					"Recipe: missing required property name",
					"Recipe: missing required property image",
				},
			},
		},
	}
	if err := st.SaveIssues(issues.Evaluate(pages, config.Default())); err != nil {
		t.Fatal(err)
	}

	d, err := Build(st, "issues", "")
	if err != nil {
		t.Fatal(err)
	}
	details := map[string]bool{}
	for _, row := range d.Rows {
		// header: url, issue, severity, priority, tab, detail
		if row[1] == "structured_validation_error" {
			details[row[len(row)-1]] = true
		}
	}
	if !details["Recipe: missing required property name"] || !details["Recipe: missing required property image"] {
		t.Errorf("issues export details = %v, want both Recipe properties", details)
	}

	counts, err := st.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["structured_validation_error"] != 1 {
		t.Errorf("affected URLs = %d, want 1", counts["structured_validation_error"])
	}
}

func TestIssueFilter(t *testing.T) {
	st := seededStore(t)
	d, err := Build(st, "internal", "internal_client_error")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) != 1 || d.Rows[0][0] != "https://ex.com/a" {
		t.Errorf("filtered rows = %+v", d.Rows)
	}
}

func TestReports(t *testing.T) {
	st := seededStore(t)

	overview, err := BuildReport(st, "crawl_overview")
	if err != nil {
		t.Fatal(err)
	}
	if !find(overview, 0, "total") || !find(overview, 0, "status:4xx") {
		t.Errorf("overview rows = %+v", overview.Rows)
	}

	chains, err := BuildReport(st, "redirect_chains")
	if err != nil {
		t.Fatal(err)
	}
	if len(chains.Rows) != 1 || chains.Rows[0][0] != "https://ex.com/r" {
		t.Errorf("chains = %+v", chains.Rows)
	}

	insecure, _ := BuildReport(st, "insecure_content")
	if len(insecure.Rows) != 1 {
		t.Errorf("insecure = %+v", insecure.Rows)
	}

	if _, err := BuildReport(st, "nope"); err == nil {
		t.Error("unknown report must error")
	}
}

func TestWriters(t *testing.T) {
	d := &Dataset{Header: []string{"a", "b"}, Rows: [][]string{{"1", "x,y"}, {"2", `q"u`}}}

	var csvBuf bytes.Buffer
	if err := Write(d, "csv", &csvBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvBuf.String(), `"x,y"`) {
		t.Errorf("csv quoting: %q", csvBuf.String())
	}

	var jsonBuf bytes.Buffer
	if err := Write(d, "json", &jsonBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), `"a": "1"`) {
		t.Errorf("json: %q", jsonBuf.String())
	}

	var jsonlBuf bytes.Buffer
	if err := Write(d, "jsonl", &jsonlBuf); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(jsonlBuf.String()), "\n") + 1; lines != 2 {
		t.Errorf("jsonl lines = %d", lines)
	}

	if err := Write(d, "tsv", &csvBuf); err == nil {
		t.Error("unknown format must error")
	}

	xlsxPath := filepath.Join(t.TempDir(), "out.xlsx")
	if err := WriteXLSX(d, xlsxPath); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingTabsAndReports(t *testing.T) {
	st := seededStore(t)
	if err := st.SaveIssues([]issues.Occurrence{
		{URL: "https://ex.com/a", IssueID: "internal_client_error"},
		{URL: "https://ex.com/", IssueID: "sitemap_orphan", Detail: "https://ex.com/s.xml"},
	}); err != nil {
		t.Fatal(err)
	}
	// custom results
	if err := st.Page(&crawler.PageRecord{
		URL: "https://ex.com/c", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html",
		CustomResults: []extract.Result{{Kind: "extraction", Name: "sku", Value: "AB-12"}},
	}); err != nil {
		t.Fatal(err)
	}

	for _, tab := range []string{"response_codes", "descriptions", "h1", "canonicals", "security"} {
		d, err := Build(st, tab, "")
		if err != nil {
			t.Fatalf("%s: %v", tab, err)
		}
		if len(d.Header) == 0 {
			t.Errorf("%s: empty header", tab)
		}
	}
	custom, err := Build(st, "custom", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(custom.Rows) != 1 || custom.Rows[0][3] != "AB-12" {
		t.Errorf("custom = %+v", custom.Rows)
	}
	orphans, err := BuildReport(st, "orphan_pages")
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans.Rows) != 1 {
		t.Errorf("orphans = %+v", orphans.Rows)
	}
	canonChains, err := BuildReport(st, "canonical_chains")
	if err != nil {
		t.Fatal(err)
	}
	if len(canonChains.Rows) != 0 {
		t.Errorf("canonical chains = %+v", canonChains.Rows)
	}
}
