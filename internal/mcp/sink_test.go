package mcp

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// runnerSink tees the crawl stream from the MCP server into the store. The
// compile-time assertion (runner.go) only proves it HAS the SitemapEntry
// method; this drives a real store through the tee and reads the entry back,
// pinning that sitemap entries actually reach the database from the MCP
// runner — without it the sitemap analyses silently do nothing.
func TestRunnerSinkForwardsSitemapEntryToStore(t *testing.T) {
	var _ crawler.SitemapSink = (*runnerSink)(nil)

	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, "", "http://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatalf("CreateCrawl: %v", err)
	}
	defer st.Close()

	sink := &runnerSink{inner: st}
	if err := sink.SitemapEntry("http://ex.com/sitemap.xml", "http://ex.com/page"); err != nil {
		t.Fatalf("SitemapEntry: %v", err)
	}

	idx, err := st.SitemapIndex()
	if err != nil {
		t.Fatalf("SitemapIndex: %v", err)
	}
	if got := idx["http://ex.com/page"]; len(got) != 1 || got[0] != "http://ex.com/sitemap.xml" {
		t.Errorf("sitemap entry not forwarded through runnerSink to the store: index = %v", idx)
	}
}
