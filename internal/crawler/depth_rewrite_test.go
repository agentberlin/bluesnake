package crawler

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// newDepthCrawler builds a Crawler for a pure depth-CSR test (no fetch, no
// Run): the scope the follow gate classifies against is what Run would have
// derived from the seed.
func newDepthCrawler(t *testing.T, cfg *config.Config, seed string) *Crawler {
	t.Helper()
	c, err := New(cfg, WithSink(newCapSink()))
	if err != nil {
		t.Fatal(err)
	}
	c.scope, err = urlutil.NewScope(seed, cfg.Scope.CrawlAllSubdomains, cfg.Scope.CDNs)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestRecomputeDepthsFromLinksRewrittenHops (FIN-DEPTH) pins that the depth
// walk keys a hyperlink hop on the URL the crawl actually fetched. At crawl
// time every discovered link goes through the url_rewriting rules before it is
// fetched and stored, but `links.dst` keeps the raw href. So a link to
// `/x?utm_source=a` lands in the page map as `/x`: if the walk looks the raw
// dst up in the page set, the hop is dropped and `/x` (and everything reachable
// only through it) is left with no depth.
func TestRecomputeDepthsFromLinksRewrittenHops(t *testing.T) {
	const seed = "http://example.com/"
	hl := string(parse.Hyperlink)

	t.Run("remove_params", func(t *testing.T) {
		cfg := config.Default()
		cfg.URLRewriting.RemoveParams = []string{"utm_source"}
		c := newDepthCrawler(t, cfg, seed)

		// "/" links to the tracking variant; the crawl fetched and stored it as
		// "/x", whose own page links on to "/y".
		links := []LinkRow{
			{Src: seed, Dst: seed + "x?utm_source=a", Type: hl},
			{Src: seed + "x", Dst: seed + "y", Type: hl},
		}
		pages := []string{seed, seed + "x", seed + "y"}

		got := c.RecomputeDepthsFromLinks(links, nil, pages, []string{seed})
		want := map[string]int{seed: 0, seed + "x": 1, seed + "y": 2}
		for u, d := range want {
			if got[u] != d {
				t.Errorf("depth(%s) = %d, want %d (hop to the rewritten key must count)", u, got[u], d)
			}
		}
	})

	t.Run("lowercase", func(t *testing.T) {
		cfg := config.Default()
		cfg.URLRewriting.Lowercase = true
		c := newDepthCrawler(t, cfg, seed)

		links := []LinkRow{
			{Src: seed, Dst: seed + "Products", Type: hl},
		}
		pages := []string{seed, seed + "products"}

		got := c.RecomputeDepthsFromLinks(links, nil, pages, []string{seed})
		want := map[string]int{seed: 0, seed + "products": 1}
		for u, d := range want {
			if got[u] != d {
				t.Errorf("depth(%s) = %d, want %d (lowercased key must count)", u, got[u], d)
			}
		}
	})

	// A rewrite that collapses a link back onto its own page is a self-hop and
	// must stay out of the adjacency, exactly as a raw self-link does.
	t.Run("rewritten_self_hop", func(t *testing.T) {
		cfg := config.Default()
		cfg.URLRewriting.RemoveParams = []string{"utm_source"}
		c := newDepthCrawler(t, cfg, seed)

		links := []LinkRow{
			{Src: seed + "x", Dst: seed + "x?utm_source=self", Type: hl},
		}
		pages := []string{seed, seed + "x"}

		got := c.RecomputeDepthsFromLinks(links, nil, pages, []string{seed})
		if got[seed+"x"] != NoDepth {
			t.Errorf("depth(/x) = %d, want NoDepth (its only link is a rewritten self-hop)", got[seed+"x"])
		}
	})
}
