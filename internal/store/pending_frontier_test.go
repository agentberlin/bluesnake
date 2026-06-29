package store

// EC-02 (MEMORY-SCALING.md §13.0): a crash between Page() and FrontierDone()
// leaves a URL with BOTH a pages row and a stale frontier row. PendingFrontier()
// must not return such a URL, or a resume re-fetches an already-crawled page —
// wasting a round-trip and double-charging the MaxURLs budget. The two writes are
// non-atomic by design (Page is a heavy multi-table write, FrontierDone a tiny
// delete), so the guard belongs in the read.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

func TestPendingFrontier_ExcludesAlreadyCrawled(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Two admitted frontier rows.
	crashed := "https://ex.com/crashed"
	pending := "https://ex.com/pending"
	for _, u := range []string{crashed, pending} {
		if _, err := c.Admit(frontier.Item{URL: u, Depth: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the crash window: a page was written for `crashed` but FrontierDone
	// never ran, so its frontier row survives alongside its pages row.
	if err := c.Page(&crawler.PageRecord{URL: crashed, Scope: "internal", State: crawler.StateCrawled}); err != nil {
		t.Fatal(err)
	}

	items, err := c.PendingFrontier()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.URL] = true
	}
	if got[crashed] {
		t.Errorf("PendingFrontier returned %q which already has a pages row — a resume would re-fetch it (EC-02)", crashed)
	}
	if !got[pending] {
		t.Errorf("PendingFrontier dropped the genuinely-pending %q", pending)
	}
}
