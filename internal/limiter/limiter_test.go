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
// fetch (and every render). Many concurrent acquires must all proceed.
func TestUnlimitedNeverBlocks(t *testing.T) {
	l := New(0, 1, 0) // 0 ⇒ unlimited (fetches AND renders)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !l.AcquireFetch(context.Background()) {
				t.Error("unlimited AcquireFetch returned false")
			}
			l.ReleaseFetch()
			if !l.AcquireRender(context.Background()) {
				t.Error("unlimited AcquireRender returned false")
			}
			l.ReleaseRender()
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
	if !l.AcquireRender(context.Background()) {
		t.Error("nil AcquireRender should be true")
	}
	l.ReleaseRender()
}

// TestFetchCapBoundsConcurrency pins the core property: at most G holders at once.
func TestFetchCapBoundsConcurrency(t *testing.T) {
	const G = 3
	l := New(G, 1, 0)
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
	l := New(1, 1, 0)
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
	l := New(0, 0, 0)
	if !l.AcquireFinalize(context.Background()) {
		t.Fatal("finalize cap of 0 admitted nobody")
	}
	l.ReleaseFinalize()
}

// TestRenderCapBoundsConcurrency (REN-01/#76) pins the render-slot pool: at most
// R concurrent holders, and the pool is DISTINCT from the fetch pool — renders
// are a different resource (a Chrome tab), so a saturated render pool must not
// consume fetch slots and vice versa.
func TestRenderCapBoundsConcurrency(t *testing.T) {
	const R = 2
	l := New(1, 1, R)
	var cur, max int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.AcquireRender(context.Background())
			c := atomic.AddInt64(&cur, 1)
			for {
				m := atomic.LoadInt64(&max)
				if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&cur, -1)
			l.ReleaseRender()
		}()
	}
	watchdog(t, &wg, "render-capped limiter deadlocked")
	if max > R {
		t.Errorf("peak concurrent render slots = %d, want <= %d", max, R)
	}
	if max < R {
		t.Errorf("peak concurrency = %d; the render cap never bound (test too quiet)", max)
	}
}

// TestRenderSlotIndependentOfFetchSlot pins the no-nested-acquire contract: with
// the single fetch slot held, render slots must still be grantable (and the
// reverse), or a render waiting on a fetch slot across M crawls would deadlock.
func TestRenderSlotIndependentOfFetchSlot(t *testing.T) {
	l := New(1, 1, 1)
	if !l.AcquireFetch(context.Background()) {
		t.Fatal("fetch acquire failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !l.AcquireRender(ctx) {
		t.Fatal("render slot unavailable while a fetch slot is held — pools are not independent")
	}
	l.ReleaseRender()
	l.ReleaseFetch()

	if !l.AcquireRender(context.Background()) {
		t.Fatal("render acquire failed")
	}
	if !l.AcquireFetch(ctx) {
		t.Fatal("fetch slot unavailable while a render slot is held — pools are not independent")
	}
	l.ReleaseFetch()
	l.ReleaseRender()
}

// TestRenderAcquireCancelReturnsFalse pins the cancel contract for render slots:
// a worker waiting when the crawl is paused/stopped gets false (no slot held).
func TestRenderAcquireCancelReturnsFalse(t *testing.T) {
	l := New(0, 1, 1)
	if !l.AcquireRender(context.Background()) { // hold the only render slot
		t.Fatal("first render acquire failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l.AcquireRender(ctx) {
		t.Error("AcquireRender with a cancelled ctx returned true (would leak a slot)")
	}
	l.ReleaseRender()
}
