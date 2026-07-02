package runner

import "testing"

// TestExecutorCrawlIDAddressedControl (GL-03/GL-04) pins that PauseCrawl/StopCrawl
// signal ONLY the targeted crawl (cancel its context, latch its stop mode), while
// other in-flight crawls are untouched — the prerequisite for cancelling one of
// several parallel crawls without disturbing the rest.
func TestExecutorCrawlIDAddressedControl(t *testing.T) {
	e := New(t.TempDir(), nil)
	aCancelled, bCancelled := false, false
	a := &run{cancel: func() { aCancelled = true }}
	b := &run{cancel: func() { bCancelled = true }}
	e.cur["A"] = a
	e.cur["B"] = b

	e.PauseCrawl("B")
	if b.stopMode != "pause" || !bCancelled {
		t.Errorf("PauseCrawl(B): b.stopMode=%q cancelled=%v, want pause/true", b.stopMode, bCancelled)
	}
	if a.stopMode != "" || aCancelled {
		t.Errorf("PauseCrawl(B) disturbed A: stopMode=%q cancelled=%v", a.stopMode, aCancelled)
	}

	e.StopCrawl("A")
	if a.stopMode != "stop" || !aCancelled {
		t.Errorf("StopCrawl(A): a.stopMode=%q cancelled=%v, want stop/true", a.stopMode, aCancelled)
	}

	// First-wins latch: a second signal does not change an already-set mode.
	e.StopCrawl("B")
	if b.stopMode != "pause" {
		t.Errorf("StopCrawl(B) after PauseCrawl(B) changed mode to %q, want the first-wins pause", b.stopMode)
	}

	// Addressing a crawl that is not in flight is a no-op (no panic).
	e.PauseCrawl("missing")
}

// TestExecutorPauseFansOutToAll (GL-13) pins that the no-argument Pause/Stop
// signal EVERY in-flight crawl — what Shutdown relies on to leave no crawl
// running.
func TestExecutorPauseFansOutToAll(t *testing.T) {
	e := New(t.TempDir(), nil)
	cancelled := map[string]bool{}
	for _, id := range []string{"A", "B", "C"} {
		id := id
		e.cur[id] = &run{cancel: func() { cancelled[id] = true }}
	}

	e.Pause()
	for id, r := range e.cur {
		if r.stopMode != "pause" || !cancelled[id] {
			t.Errorf("after Pause(): crawl %s stopMode=%q cancelled=%v, want all paused", id, r.stopMode, cancelled[id])
		}
	}
}

// TestExecutorSnapshotIdle pins the snapshot API when no crawl is in flight:
// Snapshots is empty and SnapshotCrawl reports ok=false.
func TestExecutorSnapshotIdle(t *testing.T) {
	e := New(t.TempDir(), nil)
	if snaps := e.Snapshots(); len(snaps) != 0 {
		t.Errorf("Snapshots with no crawls = %d entries, want none", len(snaps))
	}
	if _, ok := e.SnapshotCrawl("missing"); ok {
		t.Error("SnapshotCrawl for an unknown crawl returned ok=true")
	}
}
