package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// legacyFactsJSON is f as a crawl stored it before v9: the current shape with
// Headings swapped for the four legacy heading fields.
func legacyFactsJSON(t *testing.T, f *parse.Facts, l legacyHeadings) []byte {
	t.Helper()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "Headings")
	for key, v := range map[string]any{"H1s": l.H1s, "H2s": l.H2s, "HeadingLevels": l.HeadingLevels, "H1AltText": l.H1AltText} {
		if m[key], err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// v8Crawl creates a crawl with one crawled page per entry of facts, rewrites
// each page's stored facts into the legacy shape, and leaves the database at
// v8, closed: a crawl as an older bluesnake left it.
func v8Crawl(t *testing.T, facts map[string][]byte) (dir, id string) {
	t.Helper()
	dir = t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	for url, raw := range facts {
		if err := c.Page(&crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, ContentType: "text/html", Indexable: true, Facts: &parse.Facts{}}); err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			_, err = c.db.Exec(`UPDATE pages SET facts = NULL WHERE url = ?`, url)
		} else {
			_, err = c.db.Exec(`UPDATE pages SET facts = ? WHERE url = ?`, raw, url)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := setUserVersion(c.db, 8); err != nil {
		t.Fatal(err)
	}
	id = c.ID
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, id
}

// Opening a crawl stored before v9 rewrites each page's facts into the one
// heading record: levels from HeadingLevels, h1 and h2 text from H1s and H2s,
// "" for the deeper levels whose text was never kept, the page-level alt flag
// on the first h1 — and every other fact exactly as it was.
func TestMigrationRewritesLegacyHeadings(t *testing.T) {
	kept := &parse.Facts{
		Titles: []string{"Home <title> & more"}, Flesch: 70.25, WordCount: 321,
		Authors: []parse.Author{{Source: parse.AuthorMeta, Name: "Jane Doe"}},
		Links:   []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.com/a", Anchor: "A", NoAltAttr: false}},
	}
	dir, id := v8Crawl(t, map[string][]byte{
		"https://ex.com/": legacyFactsJSON(t, kept, legacyHeadings{
			H1s: []string{"Logo", "Second"}, H2s: []string{"Intro", "More"},
			HeadingLevels: []int{2, 1, 3, 1, 4, 2, 6}, H1AltText: true,
		}),
		"https://ex.com/none":     legacyFactsJSON(t, &parse.Facts{}, legacyHeadings{}),
		"https://ex.com/no-facts": nil,
	})

	c, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer c.Close()
	var v int
	if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 9 {
		t.Errorf("user_version = %d (err %v), want 9", v, err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}

	home := pages["https://ex.com/"].Facts
	want := []parse.Heading{
		{Level: 2, Text: "Intro"},
		{Level: 1, Text: "Logo", FromAlt: true},
		{Level: 3},
		{Level: 1, Text: "Second"},
		{Level: 4},
		{Level: 2, Text: "More"},
		{Level: 6},
	}
	if !slices.Equal(home.Headings, want) {
		t.Errorf("headings =\n  %+v\nwant\n  %+v", home.Headings, want)
	}
	home.Headings = nil
	if got, _ := json.Marshal(home); string(got) != string(mustJSON(t, kept)) {
		t.Errorf("the other facts changed:\n got %s\nwant %s", got, mustJSON(t, kept))
	}
	if f := pages["https://ex.com/none"].Facts; f == nil || f.Headings != nil {
		t.Errorf("a page without headings = %+v, want facts with no headings", f)
	}
	if f := pages["https://ex.com/no-facts"].Facts; f != nil {
		t.Errorf("a page without facts gained some: %+v", f)
	}

	// The legacy keys are gone from the stored document, not just ignored.
	var stale int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM pages WHERE json_type(facts, '$.H1s') IS NOT NULL
		OR json_type(facts, '$.H2s') IS NOT NULL OR json_type(facts, '$.HeadingLevels') IS NOT NULL
		OR json_type(facts, '$.H1AltText') IS NOT NULL`).Scan(&stale); err != nil || stale != 0 {
		t.Errorf("%d pages still carry a legacy heading key (err %v)", stale, err)
	}
	c.Close()

	// Re-opening finds nothing left to do.
	c, err = OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer c.Close()
	if again, err := c.LoadPages(); err != nil || !slices.Equal(again["https://ex.com/"].Facts.Headings, want) {
		t.Errorf("re-open headings = %+v (err %v)", again["https://ex.com/"].Facts.Headings, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An old crawl's h1 and h2 checks read the migrated record, including the SQL
// duplicate rules, which walk Headings rather than the arrays they replaced —
// and every page is migrated, across as many batches as the crawl needs.
func TestMigratedCrawlKeepsItsHeadingChecks(t *testing.T) {
	facts := map[string][]byte{}
	for i := range 2*headingsBatch + 3 {
		h1 := fmt.Sprintf("Unique %d", i)
		if i < 2 {
			h1 = "Shared heading"
		}
		facts[fmt.Sprintf("https://ex.com/p%d", i)] = legacyFactsJSON(t, &parse.Facts{}, legacyHeadings{
			H1s: []string{h1}, H2s: []string{"Same sub"}, HeadingLevels: []int{1, 2},
		})
	}
	dir, id := v8Crawl(t, facts)
	c, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var unmigrated int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM pages WHERE facts IS NOT NULL
		AND json_type(facts, '$.Headings') IS NULL`).Scan(&unmigrated); err != nil || unmigrated != 0 {
		t.Errorf("%d pages left unmigrated (err %v)", unmigrated, err)
	}
	dups, err := c.DuplicateIssues(false, false)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, o := range dups {
		count[o.IssueID]++
		if o.IssueID == "h1_duplicate" && (o.Detail != "Shared heading" || !strings.HasPrefix(o.URL, "https://ex.com/p")) {
			t.Errorf("h1_duplicate occurrence = %+v", o)
		}
	}
	if count["h1_duplicate"] != 2 || count["h2_duplicate"] != len(facts) {
		t.Errorf("h1_duplicate = %d (want 2), h2_duplicate = %d (want %d)", count["h1_duplicate"], count["h2_duplicate"], len(facts))
	}
}

// The rebuild itself: levels drive the record, each h1 and h2 takes the next
// text of its level, and lists that disagree with the levels never index past
// their end.
func TestLegacyHeadingsRebuild(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   legacyHeadings
		want []parse.Heading
	}{
		{"none", legacyHeadings{}, nil},
		{"alt flag lands on the first h1", legacyHeadings{H1s: []string{"a", "b"}, HeadingLevels: []int{1, 1}, H1AltText: true},
			[]parse.Heading{{Level: 1, Text: "a", FromAlt: true}, {Level: 1, Text: "b"}}},
		{"more levels than texts", legacyHeadings{H1s: []string{"a"}, HeadingLevels: []int{1, 1, 2}},
			[]parse.Heading{{Level: 1, Text: "a"}, {Level: 1}, {Level: 2}}},
	} {
		if got := tt.in.headings(); !slices.Equal(got, tt.want) {
			t.Errorf("%s: headings = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
