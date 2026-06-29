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

// TestRedirectPageEdgePersisted is the #70 H5 regression guard: a redirect page
// has a GatedEdge to its target but NO Facts (no HTML parsed), and store.Page used
// to return early when Facts==nil — dropping the redirect edge so the SQL finalize
// lost the target's discovered_from. The edge must persist regardless of Facts.
func TestRedirectPageEdgePersisted(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// A redirect page: StateCrawled, a redirect target, a non-hyperlink GatedEdge,
	// and NO Facts (a 3xx response is never parsed into HTML).
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.com/old", Scope: "internal", State: crawler.StateCrawled, StatusCode: 301,
		RedirectURL: "https://ex.com/new",
		GatedEdges:  []crawler.GatedEdge{{Dst: "https://ex.com/new", Hyperlink: false, Seq: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	// The redirect target page itself.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.com/new", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/html", Facts: &parse.Facts{},
	}); err != nil {
		t.Fatal(err)
	}

	from, err := c.DiscoveredFromEdges()
	if err != nil {
		t.Fatal(err)
	}
	if from["https://ex.com/new"] != "https://ex.com/old" {
		t.Errorf("DiscoveredFromEdges[/new] = %q, want /old (redirect edge dropped — H5 bug)", from["https://ex.com/new"])
	}
	if err := c.SaveInlinksFromEdges([]string{"https://ex.com/"}); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if got := pages["https://ex.com/new"].DiscoveredFrom; got != "https://ex.com/old" {
		t.Errorf("/new discovered_from = %q, want /old", got)
	}
	// The hyperlink=1 inlink gate (P10): the redirect edge /old -> /new is NOT a
	// hyperlink, so it must NOT count as an inlink. Dropping `AND hyperlink=1` from
	// SaveInlinksFromEdges would make this 1 — exactly the miscount this pins.
	if got := pages["https://ex.com/new"].Inlinks; got != 0 {
		t.Errorf("/new inlinks = %d, want 0 (a non-hyperlink redirect edge must not count as an inlink)", got)
	}
}
