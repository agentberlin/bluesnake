// Package limiter is the thin process-wide ceiling shared across every running
// crawl (MEMORY-SCALING.md §5.6): a cap on total concurrent fetches
// (speed.max_global_threads) and a cap on concurrent finalize passes
// (CPU+RAM-bursty). It owns ONLY what must be global — there is no shared
// per-host politeness (decision §0.2); per-crawl rate limiting stays per-crawl.
//
// A zero global-fetch cap means unlimited: the slot channel is left nil, so the
// fetch Acquire/Release are no-ops and a single crawl runs exactly as it did
// before the limiter existed. The whole type is nil-safe — a nil *Limiter is a
// valid "no global limits" value, so call sites need no nil checks.
package limiter

import "context"

// Limiter bounds global concurrency. The zero value (and a nil pointer) impose
// no limits.
type Limiter struct {
	fetch    chan struct{} // nil ⇒ unlimited concurrent fetches
	finalize chan struct{} // always buffered (≥1) when the limiter is non-nil
}

// New builds a limiter. globalFetches ≤ 0 ⇒ unlimited concurrent fetches.
// finalizePasses < 1 is clamped to 1. The single biggest impl trap (GL-07) is
// building the unlimited case as make(chan, 0) — an unbuffered channel that
// deadlocks every fetch; unlimited MUST be a nil channel.
func New(globalFetches, finalizePasses int) *Limiter {
	l := &Limiter{}
	if globalFetches > 0 {
		l.fetch = make(chan struct{}, globalFetches)
	}
	if finalizePasses < 1 {
		finalizePasses = 1
	}
	l.finalize = make(chan struct{}, finalizePasses)
	return l
}

// AcquireFetch takes one global fetch slot, blocking until a slot is free or ctx
// is done. It returns true once a slot is held (the caller MUST later call
// ReleaseFetch, ideally via defer so a panic mid-fetch can't leak the slot), or
// false if ctx was cancelled while waiting (no slot held — do not Release). A nil
// limiter or unlimited cap returns true immediately.
func (l *Limiter) AcquireFetch(ctx context.Context) bool {
	if l == nil || l.fetch == nil {
		return true
	}
	select {
	case l.fetch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// ReleaseFetch returns a fetch slot. No-op for a nil limiter / unlimited cap.
func (l *Limiter) ReleaseFetch() {
	if l == nil || l.fetch == nil {
		return
	}
	<-l.fetch
}

// AcquireFinalize takes one finalize slot (bounding concurrent CSR/analysis
// passes so M crawls finishing together don't each materialise a working set at
// once). Same contract as AcquireFetch.
func (l *Limiter) AcquireFinalize(ctx context.Context) bool {
	if l == nil {
		return true
	}
	select {
	case l.finalize <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// ReleaseFinalize returns a finalize slot. No-op for a nil limiter.
func (l *Limiter) ReleaseFinalize() {
	if l == nil {
		return
	}
	<-l.finalize
}
