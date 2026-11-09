package store

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/minhash"
	"github.com/agentberlin/bluesnake/internal/nearcopy"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestNearCopiesMigration: a crawl stored at v10, whose page_metrics has no
// near-copy columns, gains them at open (v11) with every page NULL — not
// compared, never an empty list — and keeps its other metrics.
func TestNearCopiesMigration(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Facts: &parse.Facts{Titles: []string{"t"}}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE page_metrics DROP COLUMN near_copies`,
		`ALTER TABLE page_metrics DROP COLUMN near_copy_count`,
		`UPDATE page_metrics SET link_score = 42, closest_similarity = 91.5, near_dup_count = 1`,
		`PRAGMA user_version = 10`,
	} {
		if _, err := c.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	id := c.ID
	c.Close()
	if c, err = OpenCrawl(dir, id); err != nil {
		t.Fatalf("open the v10 crawl: %v", err)
	}
	defer c.Close()
	var list sql.NullString
	var count sql.NullInt64
	var score, closest float64
	if err := c.db.QueryRow(`SELECT near_copies, near_copy_count, link_score, closest_similarity FROM page_metrics`).
		Scan(&list, &count, &score, &closest); err != nil {
		t.Fatal(err)
	}
	if list.Valid || count.Valid || score != 42 || closest != 91.5 {
		t.Errorf("after migration: near_copies %v count %v link_score %v closest %v", list, count, score, closest)
	}
	if s, err := NearCopySettings(c.db); err != nil || s != nil {
		t.Errorf("an older crawl reads near-copy settings %+v (err %v), want none", s, err)
	}
	var v int
	c.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != 11 {
		t.Errorf("user_version = %d, want 11", v)
	}
}

// TestNearCopyPagesStreamsWhatTheStepReads: the stream carries each internal
// crawled page's filter fields, stored signature and text; the SQL pre-filter
// leaves out what the step would drop anyway.
func TestNearCopyPagesStreamsWhatTheStepReads(t *testing.T) {
	c, err := CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	text := strings.Repeat("some words here ", 20)
	sig := minhash.Of(text).Encode()
	for _, rec := range []*crawler.PageRecord{
		{URL: "https://ex.com/a", Scope: "internal", State: crawler.StateCrawled, Indexable: true, Minhash: sig,
			Facts: &parse.Facts{WordCount: 60, Hash: "h-a", ContentText: text, PrevHTML: []string{"https://ex.com/"}}},
		{URL: "https://ex.com/noindex", Scope: "internal", State: crawler.StateCrawled,
			Facts: &parse.Facts{WordCount: 60, Hash: "h-n", ContentText: text, PrevHTTP: []string{"https://ex.com/"}}},
		{URL: "https://other.com/", Scope: "external", State: crawler.StateCrawled, Facts: &parse.Facts{ContentText: text}},
		{URL: "https://ex.com/blocked", Scope: "internal", State: crawler.StateBlockedRobots},
	} {
		if err := c.Page(rec); err != nil {
			t.Fatal(err)
		}
	}
	read := func(indexableOnly bool) map[string]nearcopy.Page {
		got := map[string]nearcopy.Page{}
		if err := c.NearCopyPages(indexableOnly)(func(p *nearcopy.Page) error {
			got[p.URL] = *p
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	all := read(false)
	if len(all) != 2 {
		t.Fatalf("streamed %d pages, want the two internal crawled ones: %v", len(all), all)
	}
	a := all["https://ex.com/a"]
	if !a.Internal || !a.Crawled || !a.Indexable || !a.Paginated || a.WordCount != 60 || a.Hash != "h-a" ||
		a.Text != text || string(a.Signature) != string(sig) {
		t.Errorf("/a = %+v", a)
	}
	if n := all["https://ex.com/noindex"]; n.Indexable || !n.Paginated || len(n.Signature) != 0 {
		t.Errorf("/noindex = %+v, want not indexable, paginated by header, no signature", n)
	}
	if only := read(true); len(only) != 1 || only["https://ex.com/a"].URL == "" {
		t.Errorf("indexable only streamed %v", only)
	}
}
