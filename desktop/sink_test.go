package main

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// The engine reaches WARC archiving by asserting the sink implements
// crawler.ArchiveSink; the desktop wraps the store in uiSink, so uiSink must
// forward the extension or extraction.store_warc silently does nothing.
func TestUISinkImplementsArchiveSink(t *testing.T) {
	var _ crawler.Sink = (*uiSink)(nil)
	if _, ok := interface{}((*uiSink)(nil)).(crawler.ArchiveSink); !ok {
		t.Error("uiSink must implement crawler.ArchiveSink so the WARC toggle works in the desktop app")
	}
	if _, ok := interface{}((*uiSink)(nil)).(crawler.BlobSink); !ok {
		t.Error("uiSink must implement crawler.BlobSink")
	}
}

// The compile-time assertion only proves uiSink HAS a SitemapEntry method; a
// stub that dropped the entry on the floor would still satisfy it. This drives
// a real store through the tee and reads the entry back, pinning that sitemap
// entries actually reach the database from the desktop app (without it the
// sitemap analyses — orphans, in-sitemap set ops — silently do nothing).
func TestUISinkForwardsSitemapEntryToStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, "", []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatalf("CreateCrawl: %v", err)
	}
	defer st.Close()

	sink := &uiSink{inner: st}
	if err := sink.SitemapEntry("http://ex.com/sitemap.xml", "http://ex.com/page"); err != nil {
		t.Fatalf("SitemapEntry: %v", err)
	}

	idx, err := st.SitemapIndex()
	if err != nil {
		t.Fatalf("SitemapIndex: %v", err)
	}
	if got := idx["http://ex.com/page"]; len(got) != 1 || got[0] != "http://ex.com/sitemap.xml" {
		t.Errorf("sitemap entry not forwarded through uiSink to the store: index = %v", idx)
	}
}
