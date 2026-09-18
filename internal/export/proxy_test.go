package export

import (
	"slices"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// The last link of the attribution chain (D7/REQ-S7). response_codes is the
// diagnostic tab — the one an operator opens to ask "why did these URLs 403?" —
// so the egress has to be answerable there, not just stored.
func TestProxyColumnInResponseCodes(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	want := map[string]string{
		"https://ex.com/a": "http://brd.superproxy.io:44445",
		"https://ex.com/b": "direct",
	}
	for url, proxy := range want {
		if err := st.Page(&crawler.PageRecord{
			URL: url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 403, Status: "Forbidden", ContentType: "text/html",
			Proxy: proxy,
		}); err != nil {
			t.Fatal(err)
		}
	}

	d, err := Build(st, "response_codes", "")
	if err != nil {
		t.Fatal(err)
	}
	col := slices.Index(d.Header, "proxy")
	if col < 0 {
		t.Fatalf("response_codes header has no proxy column: %v", d.Header)
	}
	urlCol := slices.Index(d.Header, "url")
	if urlCol < 0 {
		t.Fatalf("response_codes header has no url column: %v", d.Header)
	}

	got := map[string]string{}
	for _, row := range d.Rows {
		if col >= len(row) {
			t.Fatalf("row shorter than the header: %v", row)
		}
		got[row[urlCol]] = row[col]
	}
	for url, proxy := range want {
		if got[url] != proxy {
			t.Errorf("%s proxy column = %q, want %q", url, got[url], proxy)
		}
	}
}

// Every other tab stays as it was: the egress belongs on the diagnostic view,
// and widening the primary internal/external exports would change the shape of
// every stored comparison for no benefit.
func TestProxyColumnIsScopedToResponseCodes(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Page(&crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html", Proxy: "http://p:8080",
	}); err != nil {
		t.Fatal(err)
	}

	for _, tab := range []string{"internal", "external"} {
		d, err := Build(st, tab, "")
		if err != nil {
			t.Fatalf("%s: %v", tab, err)
		}
		if i := slices.Index(d.Header, "proxy"); i >= 0 {
			t.Errorf("%s tab gained a proxy column at %d: %v", tab, i, d.Header)
		}
	}
}
