package analyze

// FIN-PRDEG / FIN-PRNODE (MEMORY-SCALING.md §13): PageRank over degenerate graphs
// (empty / single / 2-cycle / dangling / disconnected) and the node-set predicate.
// These cover branches the single 4-node parity fixture never exercised: the
// empty-node early return, the dangling-source (out==0) skip, symmetric fixpoints,
// and that the scored node set is "internal AND crawled pages" — not the set of
// link endpoints.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// prGraph builds an internal crawled page set + hyperlink edges from a src->dsts
// adjacency, then runs link-score analysis and returns the scores.
func prGraph(t *testing.T, adj map[string][]string, extra ...string) map[string]float64 {
	t.Helper()
	cfg := config.Default()
	cfg.Analysis.LinkScore = true
	pages := map[string]*crawler.PageRecord{}
	ensure := func(u string) {
		if pages[u] == nil {
			pages[u] = &crawler.PageRecord{URL: u, Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{}}
		}
	}
	var links []crawler.LinkRow
	for src, dsts := range adj {
		ensure(src)
		for _, dst := range dsts {
			ensure(dst)
			links = append(links, crawler.LinkRow{Src: src, Dst: dst, Type: string(parse.Hyperlink)})
		}
	}
	for _, u := range extra { // isolated / non-linked pages
		ensure(u)
	}
	return Run(pages, nil, nil, cfg, WithLinks(links)).LinkScores
}

func TestPageRank_DegenerateGraphs(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		// No internal/crawled nodes -> early return, no scores.
		cfg := config.Default()
		cfg.Analysis.LinkScore = true
		got := Run(map[string]*crawler.PageRecord{}, nil, nil, cfg).LinkScores
		if len(got) != 0 {
			t.Errorf("empty graph produced scores: %v", got)
		}
	})

	t.Run("single", func(t *testing.T) {
		got := prGraph(t, nil, "/solo")
		if got["/solo"] != 100 {
			t.Errorf("single node score = %v, want 100 (it is its own max)", got["/solo"])
		}
	})

	t.Run("two-cycle-symmetric", func(t *testing.T) {
		got := prGraph(t, map[string][]string{"/a": {"/b"}, "/b": {"/a"}})
		if got["/a"] != got["/b"] {
			t.Errorf("2-cycle scores asymmetric: /a=%v /b=%v", got["/a"], got["/b"])
		}
		if got["/a"] != 100 {
			t.Errorf("2-cycle max score = %v, want 100", got["/a"])
		}
	})

	t.Run("dangling-source", func(t *testing.T) {
		// /b has no out-links (dangling, the out==0 skip). It still receives /a's
		// vote, so it outranks the source. All scores in [0,100], max exactly 100.
		got := prGraph(t, map[string][]string{"/a": {"/b"}})
		if len(got) != 2 {
			t.Fatalf("want scores for both nodes, got %v", got)
		}
		if got["/b"] <= got["/a"] {
			t.Errorf("dangling sink /b (%v) should outrank source /a (%v)", got["/b"], got["/a"])
		}
		assertScaled(t, got)
	})

	t.Run("disconnected", func(t *testing.T) {
		// Two independent components; every node scored, max exactly 100.
		got := prGraph(t, map[string][]string{"/a": {"/b"}, "/c": {"/d"}})
		for _, u := range []string{"/a", "/b", "/c", "/d"} {
			if _, ok := got[u]; !ok {
				t.Errorf("disconnected node %s missing a score", u)
			}
		}
		assertScaled(t, got)
	})
}

// TestPageRank_NodeSetFromPagesPredicate (FIN-PRNODE) pins that the scored node
// set is the internal+crawled pages, NOT the link endpoints: an isolated crawled
// page (no links at all) is still scored, while an external page that links
// reference is never scored.
func TestPageRank_NodeSetFromPagesPredicate(t *testing.T) {
	cfg := config.Default()
	cfg.Analysis.LinkScore = true
	pages := map[string]*crawler.PageRecord{
		"/a":       {URL: "/a", Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{}},
		"/b":       {URL: "/b", Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{}},
		"/lonely":  {URL: "/lonely", Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{}},
		"http://x": {URL: "http://x", Scope: "external", State: crawler.StateCrawled, Facts: &parse.Facts{}},
	}
	links := []crawler.LinkRow{
		{Src: "/a", Dst: "/b", Type: string(parse.Hyperlink)},
		{Src: "/a", Dst: "http://x", Type: string(parse.Hyperlink)}, // external endpoint
	}
	got := Run(pages, nil, nil, cfg, WithLinks(links)).LinkScores

	if _, ok := got["/lonely"]; !ok {
		t.Error("an isolated internal crawled page must still be a scored node (pages predicate, not link endpoints)")
	}
	if _, ok := got["http://x"]; ok {
		t.Error("an external link endpoint must not be a scored node")
	}
}

// assertScaled checks the v/max*100 contract: every score is in [0,100] and the
// maximum is exactly 100.
func assertScaled(t *testing.T, scores map[string]float64) {
	t.Helper()
	max := 0.0
	for u, v := range scores {
		if v < 0 || v > 100.0001 {
			t.Errorf("score(%s) = %v out of [0,100]", u, v)
		}
		if v > max {
			max = v
		}
	}
	if max < 99.999 || max > 100.001 {
		t.Errorf("max score = %v, want exactly 100", max)
	}
}
