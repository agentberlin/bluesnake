package analyze

import (
	"fmt"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// Analysis-phase checks of the second catalogue tranche (2026-06):
// pagination loops/unlinked URLs, the hreflang return-link family,
// hreflang-only URLs, and oversized sitemaps.

func occDetails(r *Results, url, id string) []string {
	var ds []string
	for _, o := range r.Occurrences {
		if o.URL == url && o.IssueID == id {
			ds = append(ds, o.Detail)
		}
	}
	return ds
}

func withNext(p *crawler.PageRecord, next ...string) *crawler.PageRecord {
	p.Facts.NextHTML = next
	return p
}

func withPrev(p *crawler.PageRecord, prev ...string) *crawler.PageRecord {
	p.Facts.PrevHTML = prev
	return p
}

func TestPaginationLoop(t *testing.T) {
	// l1 -> l2 -> l1 loop, entered from l0; healthy p1 -> p2 sequence
	pages := toMap(
		withNext(page("https://ex.com/l0"), "https://ex.com/l1"),
		withNext(page("https://ex.com/l1"), "https://ex.com/l2"),
		withNext(page("https://ex.com/l2"), "https://ex.com/l1"),
		withNext(page("https://ex.com/p1"), "https://ex.com/p2"),
		withPrev(page("https://ex.com/p2"), "https://ex.com/p1"),
	)
	r := Run(pages, nil, config.Default())
	for _, url := range []string{"https://ex.com/l0", "https://ex.com/l1", "https://ex.com/l2"} {
		if !hasOcc(r, url, "pagination_loop") {
			t.Errorf("missing pagination_loop on %s", url)
		}
	}
	for _, url := range []string{"https://ex.com/p1", "https://ex.com/p2"} {
		if hasOcc(r, url, "pagination_loop") {
			t.Errorf("healthy reciprocal sequence member %s flagged as a loop", url)
		}
	}
}

func TestPaginationSelfLoop(t *testing.T) {
	pages := toMap(
		withNext(page("https://ex.com/self"), "https://ex.com/self"),
	)
	if r := Run(pages, nil, config.Default()); !hasOcc(r, "https://ex.com/self", "pagination_loop") {
		t.Error("missing pagination_loop on a rel=next pointing at the page itself")
	}
}

func TestPaginationUnlinked(t *testing.T) {
	// p2 is reachable only via rel=next; p3 is also hyperlinked
	pages := toMap(
		withNext(page("https://ex.com/p1", "https://ex.com/p3"), "https://ex.com/p2"),
		withPrev(page("https://ex.com/p2"), "https://ex.com/p1"),
		page("https://ex.com/p3"),
		page("https://ex.com/linker", "https://ex.com/p1"),
	)
	pages["https://ex.com/p2"].Facts.NextHTML = []string{"https://ex.com/p3"}
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, "https://ex.com/p2", "pagination_unlinked") {
		t.Error("missing pagination_unlinked on a pagination URL with no hyperlink inlinks")
	}
	if hasOcc(r, "https://ex.com/p3", "pagination_unlinked") {
		t.Error("hyperlinked pagination URL flagged as unlinked")
	}
	if hasOcc(r, "https://ex.com/p1", "pagination_unlinked") {
		t.Error("hyperlinked series start flagged as unlinked")
	}
}

func hreflangPage(url string, entries ...parse.Hreflang) *crawler.PageRecord {
	p := page(url)
	p.Facts.HreflangHTML = entries
	return p
}

func TestHreflangInconsistentReturn(t *testing.T) {
	// en page annotates the de page as "fr"; the de page declares itself "de"
	pages := toMap(
		hreflangPage("https://ex.com/en",
			parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
			parse.Hreflang{Lang: "fr", URL: "https://ex.com/de"},
			parse.Hreflang{Lang: "x-default", URL: "https://ex.com/en"},
		),
		hreflangPage("https://ex.com/de",
			parse.Hreflang{Lang: "de", URL: "https://ex.com/de"},
			parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
			parse.Hreflang{Lang: "x-default", URL: "https://ex.com/en"},
		),
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, "https://ex.com/en", "hreflang_inconsistent_return") {
		t.Error("missing hreflang_inconsistent_return: annotation code conflicts with the target's self-declaration")
	}
	// the de page's annotations agree with both self-declarations
	if hasOcc(r, "https://ex.com/de", "hreflang_inconsistent_return") {
		t.Error("consistent annotator flagged")
	}
}

func TestHreflangNonCanonicalAndNoindexReturn(t *testing.T) {
	canonicalised := hreflangPage("https://ex.com/dup",
		parse.Hreflang{Lang: "de", URL: "https://ex.com/dup"},
		parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
	)
	canonicalised.Facts.CanonicalHTML = []string{"https://ex.com/master"}
	canonicalised.Indexable, canonicalised.IndexabilityStatus = false, "Canonicalised"

	noidx := hreflangPage("https://ex.com/noidx",
		parse.Hreflang{Lang: "fr", URL: "https://ex.com/noidx"},
		parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
	)
	noidx.Indexable, noidx.IndexabilityStatus = false, "Noindex"

	pages := toMap(
		hreflangPage("https://ex.com/en",
			parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
			parse.Hreflang{Lang: "de", URL: "https://ex.com/dup"},
			parse.Hreflang{Lang: "fr", URL: "https://ex.com/noidx"},
		),
		canonicalised,
		noidx,
		page("https://ex.com/master"),
	)
	r := Run(pages, nil, config.Default())
	if got := occDetails(r, "https://ex.com/en", "hreflang_non_canonical_return"); len(got) != 1 || got[0] != "https://ex.com/dup" {
		t.Errorf("hreflang_non_canonical_return details = %v, want exactly [https://ex.com/dup]", got)
	}
	if got := occDetails(r, "https://ex.com/en", "hreflang_noindex_return"); len(got) != 1 || got[0] != "https://ex.com/noidx" {
		t.Errorf("hreflang_noindex_return details = %v, want exactly [https://ex.com/noidx]", got)
	}
}

func TestHreflangUnlinked(t *testing.T) {
	// reciprocal en/de cluster; only the en page is hyperlinked
	pages := toMap(
		hreflangPage("https://ex.com/en",
			parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
			parse.Hreflang{Lang: "de", URL: "https://ex.com/de"},
		),
		hreflangPage("https://ex.com/de",
			parse.Hreflang{Lang: "de", URL: "https://ex.com/de"},
			parse.Hreflang{Lang: "en", URL: "https://ex.com/en"},
		),
		page("https://ex.com/linker", "https://ex.com/en"),
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, "https://ex.com/de", "hreflang_unlinked") {
		t.Error("missing hreflang_unlinked on a page reachable only via hreflang annotations")
	}
	if hasOcc(r, "https://ex.com/en", "hreflang_unlinked") {
		t.Error("hyperlinked hreflang target flagged as unlinked")
	}
}

func TestSitemapOver50k(t *testing.T) {
	index := SitemapIndex{}
	for i := 0; i < 50001; i++ {
		index[fmt.Sprintf("https://ex.com/p%d", i)] = []string{"https://ex.com/sitemap-big.xml"}
	}
	index["https://ex.com/small"] = append(index["https://ex.com/small"], "https://ex.com/sitemap-small.xml")
	pages := toMap(page("https://ex.com/"))
	r := Run(pages, index, config.Default())
	if got := occDetails(r, "https://ex.com/sitemap-big.xml", "sitemap_over_50k"); len(got) != 1 || got[0] != "50001 URLs" {
		t.Errorf("sitemap_over_50k details = %v, want exactly [50001 URLs]", got)
	}
	if hasOcc(r, "https://ex.com/sitemap-small.xml", "sitemap_over_50k") {
		t.Error("small sitemap flagged as over 50k")
	}
}
