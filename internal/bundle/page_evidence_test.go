package bundle

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// bundledFacts stores one internal page with the given facts and bundles it
// with every link type.
func bundledFacts(t *testing.T, f *parse.Facts) (Page, string) {
	t.Helper()
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200, Facts: f,
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{LinkTypes: []string{LinkTypeAll}})
	if len(pages) != 1 {
		t.Fatalf("pages = %+v", pages)
	}
	return pages[0], pageLines(raw)[0]
}

// The author evidence the parser stored rides on the page in document order,
// each entry with its source, and both keys present even when one is empty.
func TestPageCarriesItsAuthorEvidence(t *testing.T) {
	p, line := bundledFacts(t, &parse.Facts{Authors: []parse.Author{
		{Source: parse.AuthorMeta, Name: "Jane Doe"},
		{Source: parse.AuthorRel, Name: "Jane Doe", URL: "https://ex.test/people/jane"},
		{Source: parse.AuthorByline, Name: "By Jane Doe"},
	}})
	want := []Author{
		{Source: "meta", Name: "Jane Doe"},
		{Source: "rel", Name: "Jane Doe", URL: "https://ex.test/people/jane"},
		{Source: "byline", Name: "By Jane Doe"},
	}
	if !slices.Equal(p.Authors, want) {
		t.Errorf("authors = %+v, want %+v", p.Authors, want)
	}
	if !strings.Contains(line, `"authors":[{"source":"meta","name":"Jane Doe","url":""},`) {
		t.Errorf("authors on the wire:\n%s", line)
	}
}

// An iframe link carries its title; an untitled iframe and every other link
// type carry no title key, the way alt is omitted.
func TestIframeLinksCarryTheirTitle(t *testing.T) {
	p, line := bundledFacts(t, &parse.Facts{Links: []parse.Link{
		{Type: parse.IFrame, URL: "https://www.youtube.com/embed/abc", Raw: "https://www.youtube.com/embed/abc", Title: "How to crawl a site"},
		{Type: parse.IFrame, URL: "https://ex.test/widget", Raw: "/widget"},
		{Type: parse.Hyperlink, URL: "https://ex.test/x", Raw: "/x", Anchor: "x"},
	}})
	if len(p.Links) != 3 || p.Links[0].Title != "How to crawl a site" || p.Links[1].Title != "" {
		t.Errorf("links = %+v", p.Links)
	}
	if n := strings.Count(line, `"title":`); n != 2 { // the page's own title, and the titled iframe's
		t.Errorf(`"title": appears %d times, want the page's and one iframe's:\n%s`, n, line)
	}
	if !strings.Contains(line, `"title":"How to crawl a site"`) {
		t.Errorf("iframe title missing on the wire:\n%s", line)
	}
}
