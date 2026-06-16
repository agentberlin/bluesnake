package export

import (
	"slices"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestHTTPVersionColumn(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), "", []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	recs := []*crawler.PageRecord{
		{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html",
			HTTPVersion: "HTTP/1.1"},
		{URL: "https://other.com/x", Scope: "external", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html",
			HTTPVersion: "HTTP/2.0"},
	}
	for _, r := range recs {
		if err := st.Page(r); err != nil {
			t.Fatal(err)
		}
	}

	for tab, version := range map[string]string{"internal": "HTTP/1.1", "external": "HTTP/2.0"} {
		d, err := Build(st, tab, "")
		if err != nil {
			t.Fatalf("%s: %v", tab, err)
		}
		ct := slices.Index(d.Header, "content_type")
		if ct < 0 {
			t.Fatalf("%s header missing content_type: %v", tab, d.Header)
		}
		col := ct + 1
		if col >= len(d.Header) || d.Header[col] != "http_version" {
			t.Fatalf("%s header = %v, want http_version immediately after content_type", tab, d.Header)
		}
		if !find(d, col, version) {
			t.Errorf("%s rows missing %q in http_version column: %+v", tab, version, d.Rows)
		}
	}
}
