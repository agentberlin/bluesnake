package store

// P5 (issue #73): the dedup Bloom is sized from a crawl's MaxURLs ceiling instead
// of a fixed 1M, so its false-positive rate stays near target on the multi-million
// URL crawls this engine targets. These pin the sizing decision and that the
// capacity actually reaches the filter (fresh crawl + reopen/resume).

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

func TestBloomCapacityFor(t *testing.T) {
	cases := []struct {
		name    string
		maxURLs int
		want    int
	}{
		{"unlimited", 0, bloomCapacityDefault},
		{"negative-unlimited", -1, bloomCapacityDefault},
		{"tiny-floored", 10, bloomCapacityMin},
		{"floor-boundary", bloomCapacityMin, bloomCapacityMin},
		{"mid-identity", 2_000_000, 2_000_000},
		{"ceiling-boundary", bloomCapacityMax, bloomCapacityMax},
		{"huge-clamped", 50_000_000, bloomCapacityMax},
	}
	for _, c := range cases {
		if got := bloomCapacityFor(c.maxURLs); got != c.want {
			t.Errorf("%s: bloomCapacityFor(%d) = %d, want %d", c.name, c.maxURLs, got, c.want)
		}
	}
}

// TestBloomSizedFromMaxURLs proves a larger MaxURLs yields a larger filter, on
// both the fresh-crawl and the reopen (resume) paths — the fixed-1M filter would
// give an identical (saturating) size regardless of the cap.
func TestBloomSizedFromMaxURLs(t *testing.T) {
	mk := func(maxURLs int) *Crawl {
		dir := t.TempDir()
		cfg := config.Default()
		cfg.Limits.MaxURLs = maxURLs
		c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}

	small := mk(100_000)
	large := mk(4_000_000)
	if large.bloom.Bits() <= small.bloom.Bits() {
		t.Errorf("a 4M-cap crawl's Bloom (%d bits) must be larger than a 100K-cap crawl's (%d bits)",
			large.bloom.Bits(), small.bloom.Bits())
	}

	// The reopen path derives the same sizing from the frozen config.
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Limits.MaxURLs = 4_000_000
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled}); err != nil {
		t.Fatal(err)
	}
	id := c.ID
	freshBits := c.bloom.Bits()
	c.Close()

	reopened, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.bloom.Bits() != freshBits {
		t.Errorf("reopened Bloom = %d bits, fresh = %d bits — resume must size from the frozen config",
			reopened.bloom.Bits(), freshBits)
	}
}
