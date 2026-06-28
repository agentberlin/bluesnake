package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func watchdog(t *testing.T, wg *sync.WaitGroup, msg string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal(msg)
	}
}

// TestUnlimitedNeverBlocks (GL-07) is the headline trap: a zero global cap must
// be unlimited, NOT make(chan, 0) — an unbuffered channel would deadlock every
// fetch. Many concurrent acquires must all proceed.
func TestUnlimitedNeverBlocks(t *testing.T) {
	l := New(0, 1) // 0 ⇒ unlimited
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !l.AcquireFetch(context.Background()) {
				t.Error("unlimited AcquireFetch returned false")
			}
			l.ReleaseFetch()
		}()
	}
	watchdog(t, &wg, "unlimited limiter blocked — 0 cap built as an unbuffered channel?")
}

// TestNilLimiterNoOp pins that a nil *Limiter is a valid "no limits" value, so
// the crawler hot path needs no nil checks.
func TestNilLimiterNoOp(t *testing.T) {
	var l *Limiter
	if !l.AcquireFetch(context.Background()) {
		t.Error("nil AcquireFetch should be true")
	}
	l.ReleaseFetch() // must not panic
	if !l.AcquireFinalize(context.Background()) {
		t.Error("nil AcquireFinalize should be true")
	}
	l.ReleaseFinalize()
}

// TestFetchCapBoundsConcurrency pins the core property: at most G holders at once.
func TestFetchCapBoundsConcurrency(t *testing.T) {
	const G = 3
	l := New(G, 1)
	var cur, max int64
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.AcquireFetch(context.Background())
			c := atomic.AddInt64(&cur, 1)
			for {
				m := atomic.LoadInt64(&max)
				if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&cur, -1)
			l.ReleaseFetch()
		}()
	}
	watchdog(t, &wg, "fetch-capped limiter deadlocked")
	if max > G {
		t.Errorf("peak concurrent fetch slots = %d, want <= %d", max, G)
	}
	if max < 2 {
		t.Errorf("peak concurrency = %d; the cap never bound (test too quiet)", max)
	}
}

// TestAcquireCancelReturnsFalse pins the cancel contract: a worker waiting for a
// slot when the crawl is cancelled gets false (and must not Release).
func TestAcquireCancelReturnsFalse(t *testing.T) {
	l := New(1, 1)
	if !l.AcquireFetch(context.Background()) { // hold the only slot
		t.Fatal("first acquire failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l.AcquireFetch(ctx) {
		t.Error("AcquireFetch with a cancelled ctx returned true (would leak a slot)")
	}
	l.ReleaseFetch()
}

// TestFinalizeCapClampedToOne pins finalizePasses<1 clamping (a 0/negative cap
// must still admit one pass, not deadlock).
func TestFinalizeCapClampedToOne(t *testing.T) {
	l := New(0, 0)
	if !l.AcquireFinalize(context.Background()) {
		t.Fatal("finalize cap of 0 admitted nobody")
	}
	l.ReleaseFinalize()
}
