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

// TestPageRank_NonCrawledDstsPruned (#75 bug 3): an internal link target that
// was never crawled (robots-blocked, errored, still-queued) is not a node, so
// an edge pointing at it does not exist for PageRank — it must neither receive
// a rank share (which would leak into max-scaling and LinkScores) nor dilute
// its source's out-degree. Same treatment as self-loops: such edges count for
// the unique in/outlink metrics, not for PageRank (the graph is induced on the
// internal ∧ crawled node set).
func TestPageRank_NonCrawledDstsPruned(t *testing.T) {
	cfg := config.Default()
	cfg.Analysis.LinkScore = true
	node := func(u string) *crawler.PageRecord {
		return &crawler.PageRecord{URL: u, Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{}}
	}

	pages := map[string]*crawler.PageRecord{
		"/a":       node("/a"),
		"/b":       node("/b"),
		"/blocked": {URL: "/blocked", Scope: "internal", State: crawler.StateBlockedRobots},
	}
	links := []crawler.LinkRow{
		{Src: "/a", Dst: "/b", Type: string(parse.Hyperlink)},
		{Src: "/a", Dst: "/blocked", Type: string(parse.Hyperlink)},
	}
	res := Run(pages, nil, nil, cfg, WithLinks(links))

	if _, ok := res.LinkScores["/blocked"]; ok {
		t.Error("a non-crawled internal link target must hold no rank / appear in no LinkScores")
	}
	assertScaled(t, res.LinkScores)

	// The pruned edge must not dilute /a's out-degree either: scores equal the
	// two-node graph /a -> /b bit-for-bit (accumulation is canonical-ordered).
	ref := prGraph(t, map[string][]string{"/a": {"/b"}})
	if res.LinkScores["/a"] != ref["/a"] || res.LinkScores["/b"] != ref["/b"] {
		t.Errorf("scores with a pruned non-node dst: /a=%v /b=%v, want /a=%v /b=%v (induced subgraph)",
			res.LinkScores["/a"], res.LinkScores["/b"], ref["/a"], ref["/b"])
	}

	// ...while the unique link metrics keep the edge (self-loop precedent).
	if res.UniqueOut["/a"] != 2 {
		t.Errorf("unique_outlinks(/a) = %d, want 2 — link metrics must keep the non-node edge", res.UniqueOut["/a"])
	}

	// The Facts.Links fallback path shares the same node gate.
	pages["/a"].Facts = &parse.Facts{Links: []parse.Link{
		{Type: parse.Hyperlink, URL: "/b"},
		{Type: parse.Hyperlink, URL: "/blocked"},
	}}
	ram := Run(pages, nil, nil, cfg)
	if _, ok := ram.LinkScores["/blocked"]; ok {
		t.Error("Facts.Links path: non-crawled dst must hold no rank")
	}
	if ram.LinkScores["/a"] != ref["/a"] || ram.LinkScores["/b"] != ref["/b"] {
		t.Errorf("Facts.Links path scores: /a=%v /b=%v, want /a=%v /b=%v",
			ram.LinkScores["/a"], ram.LinkScores["/b"], ref["/a"], ref["/b"])
	}

	// A source whose EVERY outlink leaves the node set is dangling — its mass
	// evaporates per the existing dangling-source rule and the remaining
	// single-node graph scales itself to 100.
	pages2 := map[string]*crawler.PageRecord{
		"/solo": node("/solo"),
		"/err":  {URL: "/err", Scope: "internal", State: crawler.StateError},
	}
	links2 := []crawler.LinkRow{{Src: "/solo", Dst: "/err", Type: string(parse.Hyperlink)}}
	res2 := Run(pages2, nil, nil, cfg, WithLinks(links2))
	if _, ok := res2.LinkScores["/err"]; ok {
		t.Error("errored dst must hold no rank")
	}
	if res2.LinkScores["/solo"] != 100 {
		t.Errorf("all-outlinks-pruned source score = %v, want 100 (dangling)", res2.LinkScores["/solo"])
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
