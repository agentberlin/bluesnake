package analyze

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/parse"
)

func page(url string, links ...string) *crawler.PageRecord {
	f := &parse.Facts{}
	for _, l := range links {
		f.Links = append(f.Links, parse.Link{Type: parse.Hyperlink, URL: l})
	}
	return &crawler.PageRecord{
		URL: url, Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html", Indexable: true, Facts: f,
	}
}

func toMap(pages ...*crawler.PageRecord) map[string]*crawler.PageRecord {
	m := map[string]*crawler.PageRecord{}
	for _, p := range pages {
		m[p.URL] = p
	}
	return m
}

func hasOcc(r *Results, url, id string) bool {
	for _, o := range r.Occurrences {
		if o.URL == url && o.IssueID == id {
			return true
		}
	}
	return false
}

func TestLinkScoreAndAggregates(t *testing.T) {
	// hub: everyone links to /popular; /popular links to /a only
	pages := toMap(
		page("https://ex.com/", "https://ex.com/popular", "https://ex.com/a"),
		page("https://ex.com/a", "https://ex.com/popular"),
		page("https://ex.com/b", "https://ex.com/popular"),
		page("https://ex.com/popular", "https://ex.com/a"),
	)
	r := Run(pages, nil, config.Default())
	if r.LinkScores["https://ex.com/popular"] != 100 {
		t.Errorf("most-linked page must score 100, got %v", r.LinkScores["https://ex.com/popular"])
	}
	if r.LinkScores["https://ex.com/b"] >= r.LinkScores["https://ex.com/popular"] {
		t.Error("unlinked page must score below the hub")
	}
	if r.UniqueIn["https://ex.com/popular"] != 3 {
		t.Errorf("unique inlinks = %d, want 3", r.UniqueIn["https://ex.com/popular"])
	}
	if r.UniqueOut["https://ex.com/"] != 2 {
		t.Errorf("unique outlinks = %d, want 2", r.UniqueOut["https://ex.com/"])
	}
}

func TestRedirectChainsAndLoops(t *testing.T) {
	redirect := func(url, target string) *crawler.PageRecord {
		return &crawler.PageRecord{URL: url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 301, RedirectURL: target, RedirectType: "http"}
	}
	pages := toMap(
		redirect("https://ex.com/a", "https://ex.com/b"),
		redirect("https://ex.com/b", "https://ex.com/c"),
		page("https://ex.com/c"),
		redirect("https://ex.com/l1", "https://ex.com/l2"),
		redirect("https://ex.com/l2", "https://ex.com/l1"),
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, "https://ex.com/a", "redirect_chain") {
		t.Error("missing redirect_chain on /a")
	}
	if !hasOcc(r, "https://ex.com/l1", "redirect_loop") {
		t.Error("missing redirect_loop on /l1")
	}
	if hasOcc(r, "https://ex.com/b", "redirect_chain") {
		t.Error("/b is one hop, not a chain")
	}
	found := false
	for _, c := range r.Chains {
		if c.Source == "https://ex.com/a" && c.Final == "https://ex.com/c" && c.FinalStatus == 200 && !c.Loop {
			found = true
		}
	}
	if !found {
		t.Errorf("chain a->b->c not recorded: %+v", r.Chains)
	}
}

func TestCanonicalChains(t *testing.T) {
	canon := func(url, target string) *crawler.PageRecord {
		p := page(url)
		p.Facts.CanonicalHTML = []string{target}
		return p
	}
	pages := toMap(
		canon("https://ex.com/a", "https://ex.com/b"),
		canon("https://ex.com/b", "https://ex.com/c"),
		page("https://ex.com/c"),
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, "https://ex.com/a", "canonical_chain") {
		t.Error("missing canonical_chain")
	}
}

func TestNearDuplicates(t *testing.T) {
	// non-repetitive text: 200 distinct words, one word changed in `similar`
	var w []string
	for i := range 200 {
		w = append(w, fmt.Sprintf("word%dx", i))
	}
	long := strings.Join(w, " ")
	w[100] = "changed"
	similar := strings.Join(w, " ")
	var d []string
	for i := range 200 {
		d = append(d, fmt.Sprintf("other%dy", i))
	}
	different := strings.Join(d, " ")

	mk := func(url, text, hash string) *crawler.PageRecord {
		p := page(url)
		p.Facts.ContentText = text
		p.Facts.WordCount = len(strings.Fields(text))
		p.Facts.Hash = hash
		return p
	}
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	pages := toMap(
		mk("https://ex.com/a", long, "h1"),
		mk("https://ex.com/b", similar, "h2"),
		mk("https://ex.com/c", different, "h3"),
	)
	r := Run(pages, nil, cfg)
	nd, ok := r.NearDups["https://ex.com/a"]
	if !ok || nd.ClosestMatch != "https://ex.com/b" {
		t.Fatalf("near dup for /a = %+v", nd)
	}
	if nd.ClosestSimilarity < 90 {
		t.Errorf("similarity = %v, want >= 90", nd.ClosestSimilarity)
	}
	if _, ok := r.NearDups["https://ex.com/c"]; ok {
		t.Error("different page flagged as near duplicate")
	}
	if !hasOcc(r, "https://ex.com/a", "content_near_duplicate") {
		t.Error("missing content_near_duplicate occurrence")
	}
}

func TestHreflangReciprocity(t *testing.T) {
	mk := func(url string, hl ...parse.Hreflang) *crawler.PageRecord {
		p := page(url)
		p.Facts.HreflangHTML = hl
		return p
	}
	en := "https://ex.com/en"
	de := "https://ex.com/de"
	fr := "https://ex.com/fr"
	pages := toMap(
		mk(en,
			parse.Hreflang{Lang: "en", URL: en},
			parse.Hreflang{Lang: "de", URL: de},
			parse.Hreflang{Lang: "fr", URL: fr},
			parse.Hreflang{Lang: "x-default", URL: en},
			parse.Hreflang{Lang: "zz-!!", URL: en},
		),
		mk(de, // returns to en: ok
			parse.Hreflang{Lang: "de", URL: de},
			parse.Hreflang{Lang: "en", URL: en},
		),
		mk(fr), // no hreflang at all: missing return
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, en, "hreflang_invalid_code") {
		t.Error("missing hreflang_invalid_code")
	}
	if !hasOcc(r, en, "hreflang_missing_return") {
		t.Error("missing hreflang_missing_return for fr")
	}
	if !hasOcc(r, de, "hreflang_missing_x_default") {
		t.Error("missing hreflang_missing_x_default on de")
	}
	if hasOcc(r, en, "hreflang_missing_x_default") {
		t.Error("en has x-default but was flagged")
	}
	if hasOcc(r, en, "hreflang_missing_self_reference") {
		t.Error("en has self reference but was flagged")
	}
	if !hasOcc(r, de, "hreflang_missing_return") == false {
		t.Error("de->en is reciprocated; must not be flagged") // de returns en and en lists de
	}
}

func TestPagination(t *testing.T) {
	mk := func(url string, next, prev []string) *crawler.PageRecord {
		p := page(url)
		p.Facts.NextHTML = next
		p.Facts.PrevHTML = prev
		return p
	}
	p1 := "https://ex.com/p1"
	p2 := "https://ex.com/p2"
	p3 := "https://ex.com/p3"
	pages := toMap(
		mk(p1, []string{p2}, nil),
		mk(p2, []string{p3}, []string{p1}),
		mk(p3, nil, nil), // broken: doesn't point back to p2
	)
	r := Run(pages, nil, config.Default())
	if !hasOcc(r, p2, "pagination_sequence_error") {
		t.Error("missing pagination_sequence_error on p2 (p3 lacks prev)")
	}
	if hasOcc(r, p1, "pagination_sequence_error") {
		t.Error("p1->p2 is reciprocated; must not be flagged")
	}
}

func TestSitemapSetOps(t *testing.T) {
	inSitemapLinked := page("https://ex.com/linked")
	inSitemapLinked.Inlinks = 3
	orphan := page("https://ex.com/orphan")
	noindexed := page("https://ex.com/noindexed")
	noindexed.Indexable = false
	noindexed.Inlinks = 1
	notListed := page("https://ex.com/unlisted")
	notListed.Inlinks = 1

	index := SitemapIndex{
		"https://ex.com/linked":    {"https://ex.com/sitemap.xml"},
		"https://ex.com/orphan":    {"https://ex.com/sitemap.xml"},
		"https://ex.com/noindexed": {"https://ex.com/sitemap.xml", "https://ex.com/sitemap2.xml"},
	}
	r := Run(toMap(inSitemapLinked, orphan, noindexed, notListed), index, config.Default())
	if !hasOcc(r, "https://ex.com/orphan", "sitemap_orphan") {
		t.Error("missing sitemap_orphan")
	}
	if !hasOcc(r, "https://ex.com/noindexed", "sitemap_non_indexable") {
		t.Error("missing sitemap_non_indexable")
	}
	if !hasOcc(r, "https://ex.com/noindexed", "sitemap_in_multiple") {
		t.Error("missing sitemap_in_multiple")
	}
	if !hasOcc(r, "https://ex.com/unlisted", "sitemap_not_in_sitemap") {
		t.Error("missing sitemap_not_in_sitemap")
	}
	if hasOcc(r, "https://ex.com/linked", "sitemap_orphan") {
		t.Error("linked page flagged as orphan")
	}
}
