package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestEdgesAndDepthSQLMethods exercises the Phase-2 SQL store methods over a
// crafted graph: gated-edge inlinks/discovered_from and the depth BFS's inputs
// and output (OutLinks/Redirects/SaveDepthsMap).
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

	if m, _ := c.MaxEdgeSeq(); m != 3 {
		t.Errorf("MaxEdgeSeq = %d, want 3", m)
	}
	// OutLinks serves each page's stored rows on demand — the depth BFS's
	// adjacency — and nothing for a page that stored none.
	out, err := c.OutLinks()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	adjacency := map[string][]string{}
	for _, src := range []string{"https://ex.com/", "https://ex.com/a", "https://ex.com/b"} {
		err := out.LinksFrom(src, func(dst, typ string, nofollow bool) error {
			if typ != string(parse.Hyperlink) || nofollow {
				t.Errorf("LinksFrom(%s) row %s: type %q nofollow %v, want a followed hyperlink", src, dst, typ, nofollow)
			}
			adjacency[src] = append(adjacency[src], dst)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(adjacency["https://ex.com/"]) != 2 || len(adjacency["https://ex.com/a"]) != 1 || len(adjacency["https://ex.com/b"]) != 0 {
		t.Errorf("OutLinks adjacency = %v, want /:2 /a:1 /b:0", adjacency)
	}
	// Hyperlinks streams the same rows for the analysis link graph.
	var streamed int
	stream := c.Hyperlinks()
	stream.EachHyperlink(func(src, dst string, nofollow bool) { streamed++ })
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if streamed != 3 {
		t.Errorf("Hyperlinks streamed %d rows, want 3", streamed)
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
	// inlinks (hyperlink-gated, from edges) + first-wins discovered_from, asserted
	// on the finalized columns the production path writes.
	if pages["https://ex.com/a"].Inlinks != 1 || pages["https://ex.com/b"].Inlinks != 2 {
		t.Errorf("inlinks = /a:%d /b:%d, want /a:1 /b:2", pages["https://ex.com/a"].Inlinks, pages["https://ex.com/b"].Inlinks)
	}
	if pages["https://ex.com/a"].DiscoveredFrom != "https://ex.com/" || pages["https://ex.com/b"].DiscoveredFrom != "https://ex.com/" {
		t.Errorf("discovered_from = /a:%q /b:%q, want both /", pages["https://ex.com/a"].DiscoveredFrom, pages["https://ex.com/b"].DiscoveredFrom)
	}
	if pages["https://ex.com/a"].Depth != 1 {
		t.Errorf("/a depth = %d, want 1", pages["https://ex.com/a"].Depth)
	}
	if pages["https://ex.com/b"].Depth != crawler.NoDepth {
		t.Errorf("/b depth = %d, want NoDepth", pages["https://ex.com/b"].Depth)
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

	if err := c.SaveInlinksFromEdges([]string{"https://ex.com/"}); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	// The redirect edge was persisted (the #70 H5 fix): /new's first-wins
	// discovered_from is /old even though /old had no Facts (a 3xx isn't parsed).
	if got := pages["https://ex.com/new"].DiscoveredFrom; got != "https://ex.com/old" {
		t.Errorf("/new discovered_from = %q, want /old (redirect edge dropped — H5 bug)", got)
	}
	// The hyperlink=1 inlink gate (P10): the redirect edge /old -> /new is NOT a
	// hyperlink, so it must NOT count as an inlink. Dropping `AND hyperlink=1` from
	// SaveInlinksFromEdges would make this 1 — exactly the miscount this pins.
	if got := pages["https://ex.com/new"].Inlinks; got != 0 {
		t.Errorf("/new inlinks = %d, want 0 (a non-hyperlink redirect edge must not count as an inlink)", got)
	}
}
