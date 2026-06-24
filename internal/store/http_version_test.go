package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

func TestHTTPVersionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		HTTPVersion: "HTTP/2.0",
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}

	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"]
	if got == nil {
		t.Fatal("page not loaded")
	}
	if got.HTTPVersion != "HTTP/2.0" {
		t.Errorf("HTTPVersion = %q, want %q", got.HTTPVersion, "HTTP/2.0")
	}
}
