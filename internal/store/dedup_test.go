package store

import (
	"fmt"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

// TestFrontierDBDedupExactlyOnce (FR-04/FR-19 over the SQLite authority) is the
// dedup invariant for the on-disk visited set: a heavily-duplicated URL stream
// pushed through a frontier whose Dedup IS the store, by many workers at once,
// admits each distinct URL EXACTLY once — the atomic INSERT OR IGNORE serialises
// the race. With caps off, Admit is pure dedup, so the admitted set equals the
// distinct input. Runs under -race.
func TestFrontierDBDedupExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	cfg := config.Default()
	cfg.Limits.MaxDepth = -1
	cfg.Limits.MaxURLsPerDepth = -1
	cfg.Limits.MaxPerSubdomain = -1
	f := frontier.New(cfg, frontier.WithDedup(c)) // the store is the dedup authority

	const distinct = 300
	var stream []string
	for r := 0; r < 4; r++ {
		for i := 0; i < distinct; i++ {
			stream = append(stream, fmt.Sprintf("https://ex.com/p%d", i))
		}
	}

	var mu sync.Mutex
	admitted := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, u := range stream {
				if f.Admit(frontier.Item{URL: u, Depth: 1}) {
					mu.Lock()
					admitted[u]++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if len(admitted) != distinct {
		t.Fatalf("admitted %d distinct URLs, want %d", len(admitted), distinct)
	}
	for u, n := range admitted {
		if n != 1 {
			t.Errorf("%s admitted %d times, want exactly 1 (DB dedup not exactly-once)", u, n)
		}
	}

	// EC-14: a URL already crawled (a pages row, with no frontier row after
	// FrontierDone) must still be treated as seen and never re-admitted.
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/crawled", Scope: "internal", State: crawler.StateCrawled}); err != nil {
		t.Fatal(err)
	}
	if f.Admit(frontier.Item{URL: "https://ex.com/crawled", Depth: 1}) {
		t.Error("a URL already present in pages was re-admitted (EC-14 — would re-crawl)")
	}
}

// TestFrontierDBDedupCapRollback pins the cap-overflow rollback over the store
// authority: an over-cap novel URL is removed from the frontier set (Remove), so
// the durable visited set never leaks an un-crawlable row.
func TestFrontierDBDedupCapRollback(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	cfg := config.Default()
	cfg.Limits.MaxURLsPerDepth = 2
	f := frontier.New(cfg, frontier.WithDedup(c))

	admitted := 0
	for i := 0; i < 5; i++ {
		if f.Admit(frontier.Item{URL: fmt.Sprintf("https://ex.com/d/%d", i), Depth: 1}) {
			admitted++
		}
	}
	if admitted != 2 {
		t.Fatalf("admitted %d at depth 1, want 2 (the per-depth cap)", admitted)
	}
	// Exactly the 2 admitted survive as frontier rows; the 3 over-cap were rolled back.
	var rows int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM frontier`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("frontier has %d rows, want 2 (over-cap rows must be rolled back)", rows)
	}
}
