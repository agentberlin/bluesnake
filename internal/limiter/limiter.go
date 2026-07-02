// Package limiter is the thin process-wide ceiling shared across every running
// crawl (MEMORY-SCALING.md §5.6): a cap on total concurrent fetches
// (speed.max_global_threads), a cap on concurrent finalize passes
// (CPU+RAM-bursty), and a cap on concurrent Chrome renders
// (rendering.max_global_renders, REN-01/#76). It owns ONLY what must be global —
// there is no shared per-host politeness (decision §0.2); per-crawl rate
// limiting stays per-crawl.
//
// Renders are a slot pool DISTINCT from fetches: a render re-fetches the page
// plus every subresource inside a Chrome tab (~100-300MB of RAM plus its own
// network fan-out), so it is a different weight on a different resource axis. A
// worker holds a render slot for the Chrome round-trip and NEVER a fetch slot at
// the same time — nested acquires across M crawls would deadlock the pools
// against each other.
//
// A zero cap means unlimited: the slot channel is left nil, so the
// Acquire/Release pair are no-ops and a single crawl runs exactly as it did
// before the limiter existed. The whole type is nil-safe — a nil *Limiter is a
// valid "no global limits" value, so call sites need no nil checks.
package limiter

import "context"

// Limiter bounds global concurrency. The zero value (and a nil pointer) impose
// no limits.
type Limiter struct {
	fetch    chan struct{} // nil ⇒ unlimited concurrent fetches
	finalize chan struct{} // always buffered (≥1) when the limiter is non-nil
	render   chan struct{} // nil ⇒ unlimited concurrent Chrome renders
}

// New builds a limiter. globalFetches ≤ 0 ⇒ unlimited concurrent fetches;
// globalRenders ≤ 0 ⇒ unlimited concurrent renders (production call sites
// resolve rendering.max_global_renders=0 to a cores-scaled cap via
// render.GlobalRenderCap first — the unlimited case is for library use).
// finalizePasses < 1 is clamped to 1. The single biggest impl trap (GL-07) is
// building the unlimited case as make(chan, 0) — an unbuffered channel that
// deadlocks every acquire; unlimited MUST be a nil channel.
func New(globalFetches, finalizePasses, globalRenders int) *Limiter {
	l := &Limiter{}
	if globalFetches > 0 {
		l.fetch = make(chan struct{}, globalFetches)
	}
	if finalizePasses < 1 {
		finalizePasses = 1
	}
	l.finalize = make(chan struct{}, finalizePasses)
	if globalRenders > 0 {
		l.render = make(chan struct{}, globalRenders)
	}
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

// AcquireRender takes one global render slot (bounding concurrent Chrome tabs
// actively rendering across ALL crawls, REN-01/#76). Same contract as
// AcquireFetch: true once held (Release via defer — a panic or cancel mid-render
// must not leak the slot), false if ctx was cancelled while waiting (no slot
// held — do not Release). A nil limiter or unlimited cap returns true
// immediately. A held render slot must never be combined with a fetch slot.
func (l *Limiter) AcquireRender(ctx context.Context) bool {
	if l == nil || l.render == nil {
		return true
	}
	select {
	case l.render <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// ReleaseRender returns a render slot. No-op for a nil limiter / unlimited cap.
func (l *Limiter) ReleaseRender() {
	if l == nil || l.render == nil {
		return
	}
	<-l.render
}
