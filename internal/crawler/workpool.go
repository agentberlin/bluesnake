package crawler

import (
	"sync"
	"sync/atomic"

	"github.com/agentberlin/bluesnake/internal/frontier"
)

// workPool is the crawl's work queue: a deadlock-free, unbounded in-RAM FIFO
// of frontier items plus an atomic in-flight counter. What it bounds is the
// GOROUTINE count (N persistent workers), not the queue's memory — the queue
// grows with the ready frontier (the documented frontier-linear residual,
// MEMORY-SCALING.md; the bounded feeder is #77). N persistent
// workers pull from it; pushing never blocks (so a worker enqueuing its own
// discoveries can never deadlock on a full buffer), and the queue self-terminates
// when in-flight reaches zero. It replaces the goroutine-per-URL model
// (MEMORY-SCALING.md §5.2): the live goroutine count is the worker count,
// independent of the discovered frontier.
//
// in-flight = (items queued) + (items being processed). The load-bearing
// invariant (§11, the #1 swap trap): a worker pushes its admitted discoveries —
// each bumping in-flight — BEFORE calling done() to decrement its own item, so
// the counter can never reach zero while reachable work remains.
type workPool struct {
	mu       sync.Mutex
	cond     *sync.Cond
	items    []frontier.Item
	head     int
	closed   bool
	inflight atomic.Int64
}

func newWorkPool() *workPool {
	p := &workPool{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// push enqueues an admitted item and increments the in-flight count. It must be
// called once per admitted item before the parent worker's done(), and never
// blocks — the queue grows to absorb a high-fan-out page.
func (p *workPool) push(item frontier.Item) {
	p.inflight.Add(1)
	p.mu.Lock()
	p.items = append(p.items, item)
	p.mu.Unlock()
	p.cond.Signal()
}

// pull blocks until an item is available, returning ok=false once the queue is
// drained and closed (no further work can arrive).
func (p *workPool) pull() (frontier.Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.head == len(p.items) && !p.closed {
		p.cond.Wait()
	}
	if p.head == len(p.items) {
		return frontier.Item{}, false
	}
	item := p.items[p.head]
	p.items[p.head] = frontier.Item{} // drop the reference so the URL string can GC
	p.head++
	// Reclaim the consumed prefix once it dominates, so the backing array tracks
	// the live queue depth, not the cumulative number of items ever seen.
	if p.head > 1024 && p.head*2 >= len(p.items) {
		p.items = append(p.items[:0], p.items[p.head:]...)
		p.head = 0
	}
	return item, true
}

// done marks one item fully processed (its discoveries already pushed). When the
// in-flight count reaches zero it closes the queue, waking every idle worker to
// exit.
func (p *workPool) done() {
	if p.inflight.Add(-1) == 0 {
		p.close()
	}
}

// closeIfDrained closes the queue when nothing was ever enqueued (no admitted
// seeds), so workers exit immediately instead of blocking forever. Called once,
// before the workers start.
func (p *workPool) closeIfDrained() {
	if p.inflight.Load() == 0 {
		p.close()
	}
}

func (p *workPool) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}
