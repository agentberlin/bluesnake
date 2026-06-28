package crawler

import (
	"context"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestRecomputeDepthsFromLinksParity (FIN-DEPTH, in-package) proves the depth CSR
// over the stored link superset reproduces the in-RAM RecomputeDepths BFS exactly,
// without crossing into the finalize/store packages. It crawls an all-internal-
// hyperlink graph, reconstructs the link rows from the captured gated edges, and
// asserts RecomputeDepthsFromLinks == RecomputeDepths (and the hand-derived depths).
func TestRecomputeDepthsFromLinksParity(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a") + link("/b"),
		"/a": link("/c"),
		"/b": "<p>leaf</p>",
		"/c": "<p>leaf</p>",
	})
	sink := newCapSink()
	c, err := New(config.Default(), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	seed := s.server.URL + "/"
	if _, err := c.Run(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	snap := sink.snapshot()

	// Reconstruct the raw links superset from the captured gated edges (this
	// all-follow internal fixture has no gate divergence, so the gated subset
	// equals the depth-followed superset).
	var links []LinkRow
	urls := make([]string, 0, len(snap))
	for src, rec := range snap {
		urls = append(urls, src)
		for _, e := range rec.GatedEdges {
			typ := string(parse.Hyperlink)
			if !e.Hyperlink {
				typ = string(parse.Image)
			}
			links = append(links, LinkRow{Src: src, Dst: e.Dst, Type: typ})
		}
	}

	fromLinks := c.RecomputeDepthsFromLinks(links, nil, urls, []string{seed})

	// In-RAM reference over the same captured snapshot.
	c.RecomputeDepths(snap, seed)
	for url, rec := range snap {
		if fromLinks[url] != rec.Depth {
			t.Errorf("depth(%s): CSR-over-links=%d, in-RAM=%d", url, fromLinks[url], rec.Depth)
		}
	}

	want := map[string]int{seed: 0, seed + "a": 1, seed + "b": 1, seed + "c": 2}
	// seed ends with "/", so seed+"a" == ".../a".
	for u, d := range want {
		if fromLinks[u] != d {
			t.Errorf("depth(%s) = %d, want %d", u, fromLinks[u], d)
		}
	}
}
