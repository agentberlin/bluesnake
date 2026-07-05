package store

// P3 (issue #73): a crawl created before the gated `edges` table existed cannot
// be safely resumed — the SQL finalize derives inlinks/discovered_from solely
// from the (empty) edges table. The durable `pre_edges` meta marker (once written
// by the now-retired v4 forward-migration, still carried by any DB migrated up
// under an older binary) is what the resume path refuses on. This pins the reader:
// a fresh edges-era crawl is never marked. The refusal on a marked DB is covered
// by internal/runner and cmd/bluesnake, which plant the marker directly.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
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
