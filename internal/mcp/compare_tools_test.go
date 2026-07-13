package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

func cmpPage(url string, status int, indexable bool, title string) *crawler.PageRecord {
	pg := &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: status, ContentType: "text/html", Indexable: indexable}
	if title != "" {
		pg.Facts = &parse.Facts{Titles: []string{title}, WordCount: 100}
	}
	return pg
}

// seedComparisonCrawl registers a completed spider crawl of the seed's root
// with the given pages and title_missing occurrences.
func seedComparisonCrawl(t *testing.T, dir, seed string, pages []*crawler.PageRecord, issueURLs []string) string {
	t.Helper()
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", config.Default())
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

// the fields the tool tests read back from the embedded report JSON
type reportView struct {
	V           int    `json:"v"`
	PrevID      string `json:"prev_id"`
	CurrID      string `json:"curr_id"`
	Cached      bool   `json:"cached"`
	NewCount    int    `json:"new_count"`
	MissCount   int    `json:"missing_count"`
	StatusFlips int    `json:"status_flip_count"`
	NewPages    []struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"new_pages"`
	IssueDeltas []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Severity string `json:"severity"`
	} `json:"issue_deltas"`
	Note string `json:"note"`
	OK   *bool  `json:"ok"` // project_diff only
}

func seedComparisonPair(t *testing.T, dir string) (prevID, currID string) {
	t.Helper()
	prevID = seedComparisonCrawl(t, dir, "https://ex.com/", []*crawler.PageRecord{
		cmpPage("https://ex.com/a", 200, true, "Alpha"),
		cmpPage("https://ex.com/gone", 200, true, "Doomed"),
	}, []string{"https://ex.com/gone"})
	// registry `started` has second resolution — keep the pair order stable
	time.Sleep(1100 * time.Millisecond)
	currID = seedComparisonCrawl(t, dir, "https://ex.com/", []*crawler.PageRecord{
		cmpPage("https://ex.com/a", 404, false, ""),
		cmpPage("https://ex.com/fresh1", 200, true, "Fresh One"),
		cmpPage("https://ex.com/fresh2", 200, true, "Fresh Two"),
	}, []string{"https://ex.com/fresh1"})
	return prevID, currID
}

func TestCompareCrawlsTool(t *testing.T) {
	s, dir := projectServer(t)

	// argument validation before any crawls exist
	if _, isErr := callTool(t, s, "compare_crawls", map[string]any{"prev_crawl_id": "only-one"}); !isErr {
		t.Error("single id accepted")
	}
	if _, isErr := callTool(t, s, "compare_crawls", map[string]any{"site": "ex.com", "prev_crawl_id": "x", "curr_crawl_id": "y"}); !isErr {
		t.Error("site + ids accepted")
	}
	if _, isErr := callTool(t, s, "compare_crawls", map[string]any{"site": "ex.com"}); !isErr {
		t.Error("site without two completed crawls accepted")
	}

	prevID, currID := seedComparisonPair(t, dir)

	// by site (URL form tolerated) — fresh computation
	text, isErr := callTool(t, s, "compare_crawls", map[string]any{"site": "https://ex.com/anything"})
	if isErr {
		t.Fatalf("compare_crawls by site: %s", text)
	}
	var r reportView
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if r.PrevID != prevID || r.CurrID != currID {
		t.Errorf("pair = %s -> %s, want %s -> %s", r.PrevID, r.CurrID, prevID, currID)
	}
	if r.Cached || r.NewCount != 2 || r.MissCount != 1 || r.StatusFlips != 1 {
		t.Errorf("report = %+v", r)
	}
	if len(r.IssueDeltas) != 1 || r.IssueDeltas[0].ID != "title_missing" || r.IssueDeltas[0].Name == "title_missing" {
		t.Errorf("issue deltas not decorated: %+v", r.IssueDeltas)
	}
	if r.Note == "" {
		t.Error("note missing")
	}

	// explicit ids in the wrong order are fixed chronologically, and the pair
	// is now served from the shared cache
	text, isErr = callTool(t, s, "compare_crawls", map[string]any{"prev_crawl_id": currID, "curr_crawl_id": prevID})
	if isErr {
		t.Fatalf("compare_crawls by ids: %s", text)
	}
	r = reportView{}
	json.Unmarshal([]byte(text), &r)
	if r.PrevID != prevID || r.CurrID != currID {
		t.Errorf("swapped ids not reordered: %s -> %s", r.PrevID, r.CurrID)
	}
	if !r.Cached {
		t.Error("second comparison of the pair not served from cache")
	}

	// max_list trims lists but keeps counts exact
	text, _ = callTool(t, s, "compare_crawls", map[string]any{"site": "ex.com", "max_list": 1})
	r = reportView{}
	json.Unmarshal([]byte(text), &r)
	if r.NewCount != 2 || len(r.NewPages) != 1 {
		t.Errorf("max_list=1: count %d, list %d — want 2/1", r.NewCount, len(r.NewPages))
	}

	// unknown crawl id surfaces as a tool error
	if _, isErr := callTool(t, s, "compare_crawls", map[string]any{"prev_crawl_id": prevID, "curr_crawl_id": "nope"}); !isErr {
		t.Error("unknown crawl id accepted")
	}
}

func TestProjectDiffTool(t *testing.T) {
	s, dir := projectServer(t)

	text, isErr := callTool(t, s, "create_project", map[string]any{"main_domain": "ex.com"})
	if isErr {
		t.Fatalf("create_project: %s", text)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &proj); err != nil {
		t.Fatal(err)
	}

	// not a member
	if text, isErr := callTool(t, s, "project_diff", map[string]any{"project_id": proj.ID, "domain": "other.com"}); !isErr || !strings.Contains(text, "not a member") {
		t.Errorf("non-member diff = %v %s", isErr, text)
	}

	// member with no comparable history: ok=false, not an error
	text, isErr = callTool(t, s, "project_diff", map[string]any{"project_id": proj.ID, "domain": "ex.com"})
	if isErr {
		t.Fatalf("project_diff (no history): %s", text)
	}
	var r reportView
	json.Unmarshal([]byte(text), &r)
	if r.OK == nil || *r.OK {
		t.Errorf("expected ok=false, got %s", text)
	}

	seedComparisonPair(t, dir)

	text, isErr = callTool(t, s, "project_diff", map[string]any{"project_id": proj.ID, "domain": "https://ex.com"})
	if isErr {
		t.Fatalf("project_diff: %s", text)
	}
	r = reportView{}
	json.Unmarshal([]byte(text), &r)
	if r.OK == nil || !*r.OK || r.NewCount != 2 || r.StatusFlips != 1 {
		t.Errorf("diff = %s", text)
	}
	if len(r.IssueDeltas) != 1 || r.IssueDeltas[0].Severity == "" {
		t.Errorf("issue deltas = %+v", r.IssueDeltas)
	}
}
