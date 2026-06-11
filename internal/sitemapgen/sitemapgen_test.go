package sitemapgen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/crawler"
)

func mkPages(n int) map[string]*crawler.PageRecord {
	pages := map[string]*crawler.PageRecord{}
	for i := range n {
		url := fmt.Sprintf("https://ex.com/p%04d", i)
		pages[url] = &crawler.PageRecord{URL: url, Scope: "internal",
			State: crawler.StateCrawled, StatusCode: 200,
			ContentType: "text/html", Indexable: true}
	}
	return pages
}

func TestGenerateBasic(t *testing.T) {
	pages := mkPages(3)
	pages["https://ex.com/noindex"] = &crawler.PageRecord{URL: "https://ex.com/noindex",
		Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/html", Indexable: false}
	pages["https://ex.com/redir"] = &crawler.PageRecord{URL: "https://ex.com/redir",
		Scope: "internal", State: crawler.StateCrawled, StatusCode: 301,
		ContentType: "text/html"}
	pages["https://other.com/x"] = &crawler.PageRecord{URL: "https://other.com/x",
		Scope: "external", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/html", Indexable: true}
	pages["https://ex.com/style.css"] = &crawler.PageRecord{URL: "https://ex.com/style.css",
		Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		ContentType: "text/css", Indexable: true}

	files, err := Generate(pages, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "sitemap.xml" {
		t.Fatalf("files = %+v", files)
	}
	content := string(files[0].Data)
	if strings.Count(content, "<url>") != 3 {
		t.Errorf("url count = %d, want 3 (indexable 200 HTML only)\n%s",
			strings.Count(content, "<url>"), content)
	}
	for _, bad := range []string{"noindex", "redir", "other.com", "style.css"} {
		if strings.Contains(content, bad) {
			t.Errorf("sitemap must not contain %s", bad)
		}
	}
	if !strings.Contains(content, "http://www.sitemaps.org/schemas/sitemap/0.9") {
		t.Error("missing xmlns")
	}
}

func TestGenerateLastmod(t *testing.T) {
	pages := mkPages(1)
	for _, p := range pages {
		p.Headers = map[string]string{"Last-Modified": "Tue, 10 Jun 2025 10:00:00 GMT"}
	}
	files, err := Generate(pages, Options{IncludeLastmod: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files[0].Data), "<lastmod>2025-06-10</lastmod>") {
		t.Errorf("lastmod missing:\n%s", files[0].Data)
	}
}

func TestGenerateSplitting(t *testing.T) {
	files, err := Generate(mkPages(25), Options{MaxPerFile: 10, IndexBaseURL: "https://ex.com"})
	if err != nil {
		t.Fatal(err)
	}
	// 3 chunks + 1 index
	if len(files) != 4 {
		t.Fatalf("files = %d, want 4", len(files))
	}
	index := files[3]
	if index.Name != "sitemap-index.xml" {
		t.Errorf("index name = %s", index.Name)
	}
	if strings.Count(string(index.Data), "<sitemap>") != 3 {
		t.Errorf("index entries:\n%s", index.Data)
	}
	if !strings.Contains(string(index.Data), "https://ex.com/sitemap-1.xml") {
		t.Errorf("index loc:\n%s", index.Data)
	}
}
