package main

import (
	"testing"

	"github.com/hhsecond/acrawler/internal/crawler"
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
