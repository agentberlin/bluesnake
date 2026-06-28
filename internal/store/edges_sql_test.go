package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestEdgesAndDepthSQLMethods exercises the Phase-2 SQL/CSR store methods over a
// crafted graph: gated-edge inlinks/discovered_from, the depth-CSR inputs
// (LinkRows/Redirects/SaveDepthsMap), and the dup-rule SQL.
func TestEdgesAndDepthSQLMethods(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	hl := func(u string) parse.Link { return parse.Link{Type: parse.Hyperlink, URL: u} }
	page := func(url, title, redirect string, edges []crawler.GatedEdge, links ...parse.Link) {
		rec := &crawler.PageRecord{
			URL: url, Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
			ContentType: "text/html", Indexable: true, RedirectURL: redirect,
			Facts: &parse.Facts{Links: links, Titles: []string{title}}, GatedEdges: edges,
		}
		if err := c.Page(rec); err != nil {
			t.Fatal(err)
		}
	}
	page("https://ex.com/", "Home", "",
		[]crawler.GatedEdge{{Dst: "https://ex.com/a", Hyperlink: true, Seq: 1}, {Dst: "https://ex.com/b", Hyperlink: true, Seq: 2}},
		hl("https://ex.com/a"), hl("https://ex.com/b"))
	page("https://ex.com/a", "Dup", "",
		[]crawler.GatedEdge{{Dst: "https://ex.com/b", Hyperlink: true, Seq: 3}}, hl("https://ex.com/b"))
	page("https://ex.com/b", "Dup", "", nil) // /a and /b share title "Dup"

	inl, err := c.InlinksFromEdges()
	if err != nil {
		t.Fatal(err)
	}
	if inl["https://ex.com/a"] != 1 || inl["https://ex.com/b"] != 2 {
		t.Errorf("InlinksFromEdges = %v, want /a:1 /b:2", inl)
	}
	from, err := c.DiscoveredFromEdges()
	if err != nil {
		t.Fatal(err)
	}
	if from["https://ex.com/a"] != "https://ex.com/" || from["https://ex.com/b"] != "https://ex.com/" {
		t.Errorf("DiscoveredFromEdges = %v, want both from /", from)
	}
	if m, _ := c.MaxEdgeSeq(); m != 3 {
		t.Errorf("MaxEdgeSeq = %d, want 3", m)
	}
	rows, err := c.LinkRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("LinkRows = %d, want 3", len(rows))
	}
	if red, _ := c.Redirects(); len(red) != 0 {
		t.Errorf("Redirects = %v, want empty", red)
	}

	if err := c.SaveInlinksFromEdges([]string{"https://ex.com/"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveDepthsMap(map[string]int{
		"https://ex.com/": 0, "https://ex.com/a": 1, "https://ex.com/b": crawler.NoDepth,
	}); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if pages["https://ex.com/b"].Inlinks != 2 {
		t.Errorf("/b inlinks = %d, want 2", pages["https://ex.com/b"].Inlinks)
	}
	if pages["https://ex.com/a"].DiscoveredFrom != "https://ex.com/" {
		t.Errorf("/a discovered_from = %q, want /", pages["https://ex.com/a"].DiscoveredFrom)
	}
	if pages["https://ex.com/a"].Depth != 1 {
		t.Errorf("/a depth = %d, want 1", pages["https://ex.com/a"].Depth)
	}
	if pages["https://ex.com/b"].Depth != crawler.NoDepth {
		t.Errorf("/b depth = %d, want NoDepth", pages["https://ex.com/b"].Depth)
	}

	// dup-rule SQL: /a and /b share title "Dup" (with the ignore flags both off,
	// then on, to exercise the eligibility clauses).
	for _, flags := range [][2]bool{{false, false}, {true, true}} {
		dups, err := c.DuplicateIssues(flags[0], flags[1])
		if err != nil {
			t.Fatal(err)
		}
		titleDups := 0
		for _, o := range dups {
			if o.IssueID == "title_duplicate" && o.Detail == "Dup" {
				titleDups++
			}
		}
		if titleDups != 2 {
			t.Errorf("DuplicateIssues(%v): title_duplicate = %d, want 2 (/a,/b)", flags, titleDups)
		}
	}
}
