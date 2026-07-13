package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func TestComparisonCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// absent pair: nil payload, no error
	if raw, _, err := GetComparison(dir, "a", "b"); err != nil || raw != nil {
		t.Fatalf("empty cache: payload=%q err=%v", raw, err)
	}

	if err := SaveComparison(dir, "a", "b", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	raw, created, err := GetComparison(dir, "a", "b")
	if err != nil || string(raw) != `{"v":1}` || created.IsZero() {
		t.Fatalf("payload=%q created=%v err=%v", raw, created, err)
	}
	// keyed by the ordered pair — the reverse direction is a different diff
	if raw, _, _ := GetComparison(dir, "b", "a"); raw != nil {
		t.Errorf("reversed pair hit the cache: %q", raw)
	}

	// re-save replaces
	if err := SaveComparison(dir, "a", "b", []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if raw, _, _ := GetComparison(dir, "a", "b"); string(raw) != `{"v":2}` {
		t.Errorf("after re-save: %q", raw)
	}

	if err := DeleteComparison(dir, "a", "b"); err != nil {
		t.Fatal(err)
	}
	if raw, _, _ := GetComparison(dir, "a", "b"); raw != nil {
		t.Errorf("survived delete: %q", raw)
	}
	// deleting an absent row is fine
	if err := DeleteComparison(dir, "a", "b"); err != nil {
		t.Fatal(err)
	}
}

func TestComparisonCachePurge(t *testing.T) {
	dir := t.TempDir()
	for _, pair := range [][2]string{{"x", "y"}, {"y", "z"}, {"p", "q"}} {
		if err := SaveComparison(dir, pair[0], pair[1], []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	// purge drops every pair involving the crawl, either side
	if err := PurgeComparisons(dir, "y"); err != nil {
		t.Fatal(err)
	}
	if raw, _, _ := GetComparison(dir, "x", "y"); raw != nil {
		t.Error("x/y survived purge of y")
	}
	if raw, _, _ := GetComparison(dir, "y", "z"); raw != nil {
		t.Error("y/z survived purge of y")
	}
	if raw, _, _ := GetComparison(dir, "p", "q"); raw == nil {
		t.Error("unrelated pair purged")
	}
}

// Deleting a crawl drops its cached comparisons with it.
func TestDeleteCrawlPurgesComparisons(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	c.Close()
	if err := SaveComparison(dir, id, "other", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteCrawl(dir, id); err != nil {
		t.Fatal(err)
	}
	if raw, _, _ := GetComparison(dir, id, "other"); raw != nil {
		t.Error("comparison survived crawl deletion")
	}
}
