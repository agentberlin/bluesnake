package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestRedirectsReturnsTheHopTheCrawlFollowed pins where the depth step's redirect
// hop comes from. pages.redirect_url is the Location as the site sent it; the
// crawl followed its admitted form (rewritten + normalized) and recorded that in
// edges. A 3xx page is never parsed, so its only edge IS the hop — and a redirect
// the crawl refused has no edge, so it is no hop either. A meta refresh target is
// already in normal form at parse time, so it is returned as stored.
func TestRedirectsReturnsTheHopTheCrawlFollowed(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// The apex redirects to the www host with the default port written out: the
	// crawl admitted (and stored the target page under) the normal form.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 301,
		RedirectURL: "https://www.ex.com:443/", RedirectType: "http",
		GatedEdges: []crawler.GatedEdge{{Dst: "https://www.ex.com/", Hyperlink: false, Seq: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	// A redirect the crawl did not follow (external target, externals not
	// crawled): recorded as data, but no edge, so no hop.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.com/out", Scope: "internal", State: crawler.StateCrawled, StatusCode: 302,
		RedirectURL: "https://other.com/", RedirectType: "http",
	}); err != nil {
		t.Fatal(err)
	}
	// A meta refresh page is a parsed 200: its stored target is already the
	// parser's normal form, and its other edges are not redirect hops.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.com/meta", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/html", RedirectURL: "https://ex.com/next", RedirectType: "meta_refresh",
		Facts: &parse.Facts{},
		GatedEdges: []crawler.GatedEdge{
			{Dst: "https://ex.com/next", Hyperlink: false, Seq: 2},
			{Dst: "https://ex.com/footer", Hyperlink: true, Seq: 3},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// A page with no redirect contributes nothing, whatever edges it has.
	if err := c.Page(&crawler.PageRecord{
		URL: "https://www.ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/html", Facts: &parse.Facts{},
		GatedEdges: []crawler.GatedEdge{{Dst: "https://www.ex.com/a", Hyperlink: true, Seq: 4}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := c.Redirects()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"https://ex.com/":     "https://www.ex.com/",
		"https://ex.com/meta": "https://ex.com/next",
	}
	if len(got) != len(want) {
		t.Errorf("Redirects() = %v, want %v", got, want)
	}
	for src, dst := range want {
		if got[src] != dst {
			t.Errorf("Redirects()[%s] = %q, want %q", src, got[src], dst)
		}
	}
	if _, ok := got["https://ex.com/out"]; ok {
		t.Errorf("a redirect the crawl refused must not be a hop: %v", got)
	}
}
