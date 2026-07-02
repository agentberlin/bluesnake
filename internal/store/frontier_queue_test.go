package store

// The durable frontier work-queue authority (issue #77, MEMORY-SCALING.md
// §5.2/§5.3): admitted rows are born claimed=1 (invisible to the feeder),
// published claimable (claimed=0) by Enqueue only after the frontier's cap
// checks pass, claimed back in deterministic (depth, seq) batches by the
// pool's feeder, and deleted by FrontierDone. Recover resets orphaned claims
// (EC-01) so a crash/pause never strands work. seq is monotonic across
// sessions over the live rows (EC-07) so resumed pull order stays stable.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

func queueCrawl(t *testing.T) (*Crawl, string, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, dir, c.ID
}

// admitAndEnqueue runs the full two-step admission publish: the born-claimed
// INSERT (Admit) then the claimable publish (Enqueue) — what frontier.Frontier
// + the crawler's enqueue do for every URL that passes dedup and caps.
func admitAndEnqueue(t *testing.T, c *Crawl, it frontier.Item) {
	t.Helper()
	first, err := c.Admit(it)
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatalf("Admit(%q) not first — test fixture bug", it.URL)
	}
	if err := c.Enqueue(it); err != nil {
		t.Fatal(err)
	}
}

func claimURLs(t *testing.T, c *Crawl, n int) []string {
	t.Helper()
	items, err := c.ClaimBatch(n)
	if err != nil {
		t.Fatal(err)
	}
	urls := make([]string, len(items))
	for i, it := range items {
		urls[i] = it.URL
	}
	return urls
}

// A row Admit wrote but Enqueue has not published must be invisible to
// ClaimBatch. This is the structural closure of the cap-rollback race: between
// Admit's INSERT and a cap-overflow Remove, the feeder must never be able to
// claim (and a worker crawl) the not-yet-admitted URL.
func TestFrontierQueue_BornClaimedInvisibleUntilEnqueued(t *testing.T) {
	c, _, _ := queueCrawl(t)
	it := frontier.Item{URL: "https://ex.com/a", Depth: 1}
	if _, err := c.Admit(it); err != nil {
		t.Fatal(err)
	}
	if got := claimURLs(t, c, 10); len(got) != 0 {
		t.Fatalf("ClaimBatch returned %v for a born-claimed (unpublished) row — the cap-rollback race is open", got)
	}
	// The rollback path: Remove deletes the still-unpublished row outright.
	if err := c.Remove(it.URL); err != nil {
		t.Fatal(err)
	}
	if got := claimURLs(t, c, 10); len(got) != 0 {
		t.Fatalf("ClaimBatch returned %v after the row was rolled back", got)
	}
	// The publish path: after Enqueue the row is claimable exactly once.
	admitAndEnqueue(t, c, it)
	if got := claimURLs(t, c, 10); len(got) != 1 || got[0] != it.URL {
		t.Fatalf("ClaimBatch after Enqueue = %v, want [%s]", got, it.URL)
	}
	if got := claimURLs(t, c, 10); len(got) != 0 {
		t.Fatalf("ClaimBatch re-claimed an already-claimed row: %v (WP-08 double-claim)", got)
	}
}

// ClaimBatch pulls in (depth, seq) order — depth-major, admission order within
// a depth — never insertion racing or rowid order, and honours the batch limit.
func TestFrontierQueue_ClaimOrderDepthThenSeq(t *testing.T) {
	c, _, _ := queueCrawl(t)
	// Admit deliberately out of depth order: a depth-2 row first, then depth-1
	// rows, then another depth-2. (depth, seq) order must yield d1 rows in
	// admission order, then d2 rows in admission order.
	seq := []frontier.Item{
		{URL: "https://ex.com/d2-first", Depth: 2},
		{URL: "https://ex.com/d1-a", Depth: 1},
		{URL: "https://ex.com/d1-b", Depth: 1},
		{URL: "https://ex.com/d2-second", Depth: 2},
	}
	for _, it := range seq {
		admitAndEnqueue(t, c, it)
	}
	want := []string{
		"https://ex.com/d1-a", "https://ex.com/d1-b",
		"https://ex.com/d2-first", "https://ex.com/d2-second",
	}
	// Batch limit: claim 3, then the remaining 1.
	got := claimURLs(t, c, 3)
	got = append(got, claimURLs(t, c, 3)...)
	if len(got) != len(want) {
		t.Fatalf("claimed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order[%d] = %q, want %q (full: %v)", i, got[i], want[i], want)
		}
	}
}

// EC-02 defense in depth: a frontier row whose URL already has a pages row (the
// crash window between Page and FrontierDone) must never be claimed — a worker
// would re-fetch an already-crawled page and double-charge the MaxURLs budget.
func TestFrontierQueue_ClaimSkipsAlreadyCrawled(t *testing.T) {
	c, _, _ := queueCrawl(t)
	crashed := frontier.Item{URL: "https://ex.com/crashed", Depth: 1}
	pending := frontier.Item{URL: "https://ex.com/pending", Depth: 1}
	admitAndEnqueue(t, c, crashed)
	admitAndEnqueue(t, c, pending)
	if err := c.Page(&crawler.PageRecord{URL: crashed.URL, Scope: "internal", State: crawler.StateCrawled}); err != nil {
		t.Fatal(err)
	}
	got := claimURLs(t, c, 10)
	if len(got) != 1 || got[0] != pending.URL {
		t.Fatalf("ClaimBatch = %v, want only %q (crashed row has a pages row)", got, pending.URL)
	}
}

// EC-01: Recover resets every claimed row (a crash's orphaned claimed=1 rows,
// a pause's in-buffer rows) back to claimable, exactly once each — without it
// the feeder's WHERE claimed=0 skips them forever and the URLs are silently
// lost. On the durable authority the rows themselves are the queue, so the
// pending argument is ignored.
func TestFrontierQueue_RecoverResetsOrphanedClaims(t *testing.T) {
	c, _, _ := queueCrawl(t)
	a := frontier.Item{URL: "https://ex.com/a", Depth: 1}
	b := frontier.Item{URL: "https://ex.com/b", Depth: 1}
	admitAndEnqueue(t, c, a)
	admitAndEnqueue(t, c, b)
	if got := claimURLs(t, c, 10); len(got) != 2 {
		t.Fatalf("setup: claimed %v, want both rows", got)
	}
	// Simulated crash/pause: nothing was FrontierDone'd; both rows sit claimed.
	if got := claimURLs(t, c, 10); len(got) != 0 {
		t.Fatalf("claimed rows re-claimable without Recover: %v", got)
	}
	if err := c.Recover(nil); err != nil {
		t.Fatal(err)
	}
	got := claimURLs(t, c, 10)
	if len(got) != 2 {
		t.Fatalf("after Recover: claimable = %v, want both orphaned rows (EC-01)", got)
	}
	// A done row must stay gone: Recover resurrects claims, never deletions.
	if err := c.FrontierDone(a.URL); err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(nil); err != nil {
		t.Fatal(err)
	}
	got = claimURLs(t, c, 10)
	if len(got) != 1 || got[0] != b.URL {
		t.Fatalf("after FrontierDone(a)+Recover: claimable = %v, want [%s]", got, b.URL)
	}
}

// EC-07 (adapted to the shipped design): a resumed session's admissions get
// seq strictly above every surviving row's seq, so pending session-1 work is
// pulled before session-2 discoveries at the same depth and the pull order is
// stable across the interrupt boundary.
func TestFrontierQueue_SeqMonotonicAcrossSessions(t *testing.T) {
	c, dir, id := queueCrawl(t)
	first := frontier.Item{URL: "https://ex.com/session1-pending", Depth: 1}
	admitAndEnqueue(t, c, first)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Recover(nil); err != nil {
		t.Fatal(err)
	}
	second := frontier.Item{URL: "https://ex.com/session2-new", Depth: 1}
	admitAndEnqueue(t, c2, second)

	got := claimURLs(t, c2, 10)
	want := []string{first.URL, second.URL}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cross-session claim order = %v, want %v (session-2 seq must sort after session-1's)", got, want)
	}
}

// Forward-migration: a v4 (pre-claimed/seq) crawl DB gains both columns on
// open; surviving pending rows get seq backfilled from their insertion order
// (rowid) so an interrupted pre-v5 crawl resumes with a deterministic pull
// order, and claimed=0 so every pending row is immediately claimable.
func TestFrontierClaimedSeqForwardMigration(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	for _, u := range []string{"https://ex.com/a", "https://ex.com/b"} {
		if _, err := c.Admit(frontier.Item{URL: u, Depth: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Reconstruct the v4 shape: drop the queue columns and stamp back to v4.
	if _, err := c.db.Exec(`DROP INDEX IF EXISTS frontier_claim;
		ALTER TABLE frontier DROP COLUMN claimed;
		ALTER TABLE frontier DROP COLUMN seq;
		PRAGMA user_version = 4;`); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("opening a v4 DB should forward-migrate, not fail: %v", err)
	}
	defer c2.Close()
	rows, err := c2.db.Query(`SELECT url, claimed, seq FROM frontier ORDER BY seq`)
	if err != nil {
		t.Fatalf("queue columns missing after forward-migration from v4: %v", err)
	}
	defer rows.Close()
	var urls []string
	lastSeq := int64(-1)
	for rows.Next() {
		var url string
		var claimed int
		var seqv int64
		if err := rows.Scan(&url, &claimed, &seqv); err != nil {
			t.Fatal(err)
		}
		if claimed != 0 {
			t.Errorf("migrated row %q claimed = %d, want 0 (immediately claimable)", url, claimed)
		}
		if seqv <= lastSeq {
			t.Errorf("migrated seq not increasing: %q at %d after %d", url, seqv, lastSeq)
		}
		lastSeq = seqv
		urls = append(urls, url)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "https://ex.com/a" || urls[1] != "https://ex.com/b" {
		t.Fatalf("backfilled seq order = %v, want insertion order [a b]", urls)
	}
	// Post-migration admissions continue above the backfilled seqs.
	if _, err := c2.Admit(frontier.Item{URL: "https://ex.com/c", Depth: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c2.Enqueue(frontier.Item{URL: "https://ex.com/c", Depth: 1}); err != nil {
		t.Fatal(err)
	}
	var maxSeq int64
	if err := c2.db.QueryRow(`SELECT seq FROM frontier WHERE url = 'https://ex.com/c'`).Scan(&maxSeq); err != nil {
		t.Fatal(err)
	}
	if maxSeq <= lastSeq {
		t.Errorf("post-migration admission seq = %d, want > backfilled max %d", maxSeq, lastSeq)
	}
	var ver int
	c2.db.QueryRow(`PRAGMA user_version`).Scan(&ver)
	if want := ladderTop(crawlMigrations); ver != want {
		t.Errorf("user_version after migration = %d, want %d", ver, want)
	}
}
