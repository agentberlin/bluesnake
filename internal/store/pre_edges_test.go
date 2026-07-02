package store

// P3 (issue #73): a crawl created before the gated `edges` table existed cannot
// be safely resumed — the SQL finalize derives inlinks/discovered_from solely
// from the (empty) edges table. The v4 forward-migration marks such DBs durably
// (`pre_edges` meta), which the resume path refuses on. These pin the marker:
// set on a forward-migrated pre-edges DB, absent on a fresh edges-era crawl.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

func TestPreEdges_FreshCrawlNotMarked(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pre, err := c.PreEdges()
	if err != nil {
		t.Fatal(err)
	}
	if pre {
		t.Error("a fresh edges-era crawl must not be marked pre_edges")
	}
}

func TestPreEdges_ForwardMigratedCrawlMarked(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct a pre-edges crawl DB: drop the minhash column and stamp the
	// stored revision back to v3 (before the edges table / minhash column existed).
	if _, err := c.db.Exec(`ALTER TABLE pages DROP COLUMN minhash;
		PRAGMA user_version = 3;`); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening runs the v4 forward-migration, which sets the durable marker.
	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("opening a pre-edges DB should forward-migrate for read, not fail: %v", err)
	}
	defer c2.Close()
	pre, err := c2.PreEdges()
	if err != nil {
		t.Fatal(err)
	}
	if !pre {
		t.Error("a forward-migrated pre-edges crawl must be marked pre_edges so resume can refuse it")
	}

	// The marker is durable: a later reopen (version already at v4) still sees it.
	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}
	c3, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if pre, _ := c3.PreEdges(); !pre {
		t.Error("pre_edges marker did not survive the version bump (a re-resume would corrupt)")
	}
}
