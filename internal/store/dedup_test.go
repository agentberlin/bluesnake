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

// TestFirstWithContentAuthority pins the store-backed identical-content
// short-circuit (#70 M4): first-writer-wins per hash, a non-claiming page never
// becomes canonical, and concurrent claimers resolve to exactly one winner.
func TestFirstWithContentAuthority(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// First claimer of hash "h1" becomes canonical.
	if canon, first, err := c.FirstWithContent("h1", "https://ex.com/a", true); err != nil || !first || canon != "https://ex.com/a" {
		t.Fatalf("first claim of h1 = (%q,%v,%v), want (a,true,nil)", canon, first, err)
	}
	// A second page with the same hash is a duplicate of the canonical.
	if canon, first, err := c.FirstWithContent("h1", "https://ex.com/b", true); err != nil || first || canon != "https://ex.com/a" {
		t.Fatalf("second h1 = (%q,%v,%v), want (a,false,nil)", canon, first, err)
	}
	// A non-claiming page (won't expand) on a novel hash is "first" but does NOT
	// record itself — so a later claiming twin still becomes the recorded canonical.
	if _, first, err := c.FirstWithContent("h2", "https://ex.com/x", false); err != nil || !first {
		t.Fatalf("non-claiming novel h2 first = %v (err %v), want true", first, err)
	}
	if canon, first, err := c.FirstWithContent("h2", "https://ex.com/y", true); err != nil || !first || canon != "https://ex.com/y" {
		t.Fatalf("claiming h2 after a non-claiming peek = (%q,%v,%v), want (y,true,nil)", canon, first, err)
	}

	// Concurrent claimers of one hash resolve to exactly one winner.
	var mu sync.Mutex
	firsts := 0
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, first, err := c.FirstWithContent("race", fmt.Sprintf("https://ex.com/r%d", i), true)
			if err != nil {
				t.Errorf("concurrent claim err: %v", err)
				return
			}
			if first {
				mu.Lock()
				firsts++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firsts != 1 {
		t.Errorf("concurrent claimers of one hash produced %d winners, want exactly 1", firsts)
	}
}

// TestBloomDedupMatchesExactOracle is the T4 property guard (#70 H3/T4, doc §8
// test #2): the Bloom-fronted store dedup must equal an exact in-memory set
// oracle on a randomised, heavily-duplicated stream — admit every URL exactly
// once on its first appearance and never again, including URLs that became pages
// rows (EC-14). The Bloom is only a fast negative; a false positive may cost an
// extra confirm read but can never drop a novel URL or re-admit a seen one.
func TestBloomDedupMatchesExactOracle(t *testing.T) {
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
	f := frontier.New(cfg, frontier.WithDedup(c))

	// A deterministic pseudo-random duplicated stream (fixed seed: no Math.rand
	// flakiness, reproducible across runs).
	const distinct = 500
	oracle := map[string]bool{} // exact "ever admitted" set
	seed := uint64(0x9e3779b97f4a7c15)
	next := func() uint64 { seed ^= seed << 13; seed ^= seed >> 7; seed ^= seed << 17; return seed }

	// Pre-crawl a handful so they are pages rows (not frontier) — EC-14 territory.
	for i := 0; i < 20; i++ {
		u := fmt.Sprintf("https://ex.com/p%d", i)
		if err := c.Page(&crawler.PageRecord{URL: u, Scope: "internal", State: crawler.StateCrawled}); err != nil {
			t.Fatal(err)
		}
		oracle[u] = true // already seen — must never be admitted
	}

	for i := 0; i < distinct*8; i++ {
		u := fmt.Sprintf("https://ex.com/p%d", int(next()%distinct))
		admitted := f.Admit(frontier.Item{URL: u, Depth: 1})
		if admitted {
			if oracle[u] {
				t.Fatalf("re-admitted an already-seen URL %s (Bloom/DB dedup leaked)", u)
			}
			oracle[u] = true
		} else if !oracle[u] {
			t.Fatalf("dropped a novel URL %s (Bloom false-negative — must never happen)", u)
		}
	}
	// Every distinct URL p0..p499 must end up admitted-or-already-a-page exactly once.
	if len(oracle) != distinct {
		t.Errorf("oracle holds %d distinct URLs, want %d", len(oracle), distinct)
	}
}

// TestAdmittedItemsUnionForResume (FR-08 / #70 M3) pins that AdmittedItems returns
// every admitted URL — crawled pages plus pending frontier rows — each carrying
// its admit-time depth. This is the input the frontier replays through its
// per-bucket counters on resume so the per-depth/-subdomain/-path caps bind across
// the interrupt boundary; a wrong depth here would mis-bucket the rehydration.
func TestAdmittedItemsUnionForResume(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Two crawled pages (depth 0 and 1) ...
	for _, p := range []struct {
		url   string
		depth int
	}{{"https://ex.com/", 0}, {"https://ex.com/a", 1}} {
		if err := c.Page(&crawler.PageRecord{URL: p.url, Scope: "internal", State: crawler.StateCrawled, Depth: p.depth}); err != nil {
			t.Fatal(err)
		}
	}
	// ... plus two still-pending frontier rows (depth 1 and 2).
	for _, it := range []frontier.Item{{URL: "https://ex.com/b", Depth: 1}, {URL: "https://ex.com/c", Depth: 2}} {
		if first, err := c.Admit(it); err != nil || !first {
			t.Fatalf("Admit(%s) = (%v,%v), want first", it.URL, first, err)
		}
	}

	items, err := c.AdmittedItems()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, it := range items {
		got[it.URL] = it.Depth
	}
	want := map[string]int{
		"https://ex.com/":  0,
		"https://ex.com/a": 1,
		"https://ex.com/b": 1,
		"https://ex.com/c": 2,
	}
	if len(got) != len(want) {
		t.Fatalf("AdmittedItems returned %d urls, want %d: %v", len(got), len(want), got)
	}
	for u, d := range want {
		if got[u] != d {
			t.Errorf("AdmittedItems[%s] depth = %d, want %d", u, got[u], d)
		}
	}
}
