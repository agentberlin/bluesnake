package analyze

// FIN-CSRID (MEMORY-SCALING.md §13): the stored link_score (PageRank) must be
// reproducible bit-for-bit run-to-run. Go map iteration is randomized and float
// addition is non-associative, so accumulating contributions in map order made
// the score jitter at ~1e-12 between runs. TestLinkScore_DeterministicAcrossRuns
// pins the canonical-order fix: many runs over the same graph yield byte-identical
// scores. The graph gives one hub several in-edges of differing share, so the sum
// is genuinely order-sensitive — the case the old code jittered on.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

func linkScoreFixture() (map[string]*crawler.PageRecord, []crawler.LinkRow) {
	urls := []string{"/", "/a", "/b", "/c", "/d", "/e", "/hub"}
	pages := map[string]*crawler.PageRecord{}
	for _, u := range urls {
		pages[u] = &crawler.PageRecord{
			URL: u, Scope: "internal", State: crawler.StateCrawled, Facts: &parse.Facts{},
		}
	}
	hl := func(src, dst string) crawler.LinkRow {
		return crawler.LinkRow{Src: src, Dst: dst, Type: string(parse.Hyperlink)}
	}
	// /a../e and / all link the hub; the spokes have differing out-degrees so the
	// per-source share differs, making the hub's incoming sum order-sensitive.
	links := []crawler.LinkRow{
		hl("/", "/a"), hl("/", "/b"), hl("/", "/hub"),
		hl("/a", "/hub"), hl("/a", "/b"),
		hl("/b", "/hub"), hl("/b", "/c"), hl("/b", "/d"),
		hl("/c", "/hub"),
		hl("/d", "/hub"), hl("/d", "/e"),
		hl("/e", "/hub"),
		hl("/hub", "/"),
	}
	return pages, links
}

func TestLinkScore_DeterministicAcrossRuns(t *testing.T) {
	cfg := config.Default()
	cfg.Analysis.LinkScore = true

	var first map[string]float64
	const runs = 60
	for i := 0; i < runs; i++ {
		pages, links := linkScoreFixture()
		res := Run(pages, nil, nil, cfg, WithLinks(links))
		if len(res.LinkScores) == 0 {
			t.Fatal("no link scores produced")
		}
		if first == nil {
			first = res.LinkScores
			continue
		}
		for url, want := range first {
			if got := res.LinkScores[url]; got != want {
				t.Fatalf("run %d: link_score(%s) = %v, run 0 = %v — PageRank is not reproducible bit-for-bit", i, url, got, want)
			}
		}
		if len(res.LinkScores) != len(first) {
			t.Fatalf("run %d: produced %d scores, run 0 produced %d", i, len(res.LinkScores), len(first))
		}
	}
}
