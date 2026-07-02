package crawler

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/agentberlin/bluesnake/internal/frontier"
)

// Ready-buffer sizing (MEMORY-SCALING.md §5.3): the in-RAM window over the
// work-queue authority is a small multiple of the worker count — a scheduling
// cushion that keeps workers fed between feeder refills, NOT the frontier.
// Everything past the window lives durably in the authority (the store), which
// is what makes per-crawl RAM independent of the discovered frontier (#77).
const (
	bufferFactor = 4
	bufferMin    = 32   // floor: tiny thread counts still get batch-sized refills
	bufferMax    = 1024 // ceiling: ~200 B/item ⇒ the window stays in the low hundreds of KB
)

func bufferCap(threads int) int {
	c := threads * bufferFactor
	if c < bufferMin {
		c = bufferMin
	}
	if c > bufferMax {
		c = bufferMax
	}
	return c
}

// workPool is the crawl's scheduling core (MEMORY-SCALING.md §5.2/§5.3): a
// bounded in-RAM ready-buffer refilled by a SINGLE feeder goroutine from the
// work-queue authority, drained by N persistent workers. What it bounds is both
// the goroutine count (N workers + 1 feeder) AND the queue's memory (the
// buffer is a fixed window; the frontier itself lives in the authority).
//
// The single-producer shape is load-bearing, not incidental:
//   - workers never push, so a worker can never block on its own full buffer
//     (the WP-03 sole-producer deadlock is impossible by construction);
//   - only the feeder claims, so no item is ever handed out twice (WP-08);
//   - the feeder pulls in the authority's deterministic (depth, seq) order.
//
// in-flight = (claimed items in the feeder's hands or the buffer) + (items
// being processed). The §11 #1 termination trap is honoured one level down
// from the old push-before-done: a worker PUBLISHES its admitted discoveries
// durably (frontier.Queue.Enqueue) BEFORE done() decrements its own item, so
// when in-flight reaches zero every reachable item is already visible to the
// feeder's claim — and the feeder re-claims once more before closing.
type workPool struct {
	queue    frontier.Queue
	buf      chan frontier.Item
	batch    int
	inflight atomic.Int64
	// poke wakes a waiting feeder: new work was published, or in-flight hit
	// zero (termination check). One buffered slot holds a pending signal so a
	// wake that lands between the feeder's claim and its wait is never lost.
	poke  chan struct{}
	onErr func(error)
}

func newWorkPool(queue frontier.Queue, threads int, onErr func(error)) *workPool {
	cap := bufferCap(threads)
	return &workPool{
		queue: queue,
		buf:   make(chan frontier.Item, cap),
		batch: cap / 2,
		poke:  make(chan struct{}, 1),
		onErr: onErr,
	}
}

// notify signals the feeder that claimable work may exist (an item was
// published) or that the in-flight count hit zero. Non-blocking.
func (p *workPool) notify() {
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

// pull blocks until an item is available, returning ok=false once the buffer
// is drained and closed (the feeder decided no further work can arrive).
func (p *workPool) pull() (frontier.Item, bool) {
	it, ok := <-p.buf
	return it, ok
}

// done marks one item fully processed (its discoveries already published).
// When the in-flight count reaches zero it wakes the feeder, which owns the
// close decision — there may still be unclaimed work in the authority.
func (p *workPool) done() {
	if p.inflight.Add(-1) == 0 {
		p.notify()
	}
}

// feed is the pool's single producer. It claims batches from the work-queue
// authority and delivers them to the buffer, blocking on the buffer for
// backpressure (workers always drain, so a blocked send always frees). It
// exits — closing the buffer, which releases every worker — when the crawl is
// cancelled, the authority fails, or the crawl is complete: no buffered items,
// no in-flight items, and (checked twice, see below) no claimable rows.
func (p *workPool) feed(ctx context.Context) {
	defer close(p.buf)
	for {
		if ctx.Err() != nil {
			return // cancelled: workers drain the buffer, leaving items pending
		}
		items, err := p.queue.ClaimBatch(p.batch)
		if err != nil {
			p.onErr(err) // fail loudly — a silently parked feeder would hang the crawl
			return
		}
		if len(items) == 0 {
			if p.inflight.Load() != 0 {
				// Workers are mid-item; their discoveries may not be published
				// yet. Sleep until something is published or the count hits 0.
				select {
				case <-p.poke:
				case <-ctx.Done():
				}
				continue
			}
			// in-flight == 0: no worker is mid-item, and every publish happens
			// before its parent's done(), so the authority is now stable — but
			// the empty claim above may predate the LAST worker's publishes.
			// One authoritative re-claim closes that race.
			if items, err = p.queue.ClaimBatch(p.batch); err != nil {
				p.onErr(err)
				return
			}
			if len(items) == 0 {
				return // drained: nothing buffered, in-flight, or claimable
			}
		}
		p.inflight.Add(int64(len(items)))
		for i, it := range items {
			select {
			case p.buf <- it:
			case <-ctx.Done():
				// Cancelled mid-delivery: the undelivered tail stays claimed in
				// the authority — the next session's Recover resets it to
				// pending (EC-01) — so only its in-flight count is returned.
				p.inflight.Add(-int64(len(items) - i))
				return
			}
		}
	}
}

// memQueue is the in-RAM frontier.Queue used when the sink supplies no durable
// authority (bare library/test use, no persistence): a FIFO in admission order
// — the exact pre-feeder queue semantics, including the head-compaction that
// keeps the backing array tracking live depth rather than items-ever-seen. RAM
// stays frontier-linear here BY DESIGN; the bounded-RAM contract belongs to
// the store-backed authority, which every production sink provides
// (compile-pinned in internal/runner).
type memQueue struct {
	mu    sync.Mutex
	items []frontier.Item
	head  int
}

func (q *memQueue) Enqueue(it frontier.Item) error {
	q.mu.Lock()
	q.items = append(q.items, it)
	q.mu.Unlock()
	return nil
}

func (q *memQueue) ClaimBatch(n int) ([]frontier.Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if avail := len(q.items) - q.head; n > avail {
		n = avail
	}
	if n <= 0 {
		return nil, nil
	}
	out := make([]frontier.Item, n)
	copy(out, q.items[q.head:q.head+n])
	for i := q.head; i < q.head+n; i++ {
		q.items[i] = frontier.Item{} // drop the reference so the URL string can GC
	}
	q.head += n
	// Reclaim the consumed prefix once it dominates, so the backing array tracks
	// the live queue depth, not the cumulative number of items ever seen.
	if q.head > 1024 && q.head*2 >= len(q.items) {
		q.items = append(q.items[:0], q.items[q.head:]...)
		q.head = 0
	}
	return out, nil
}

// Recover re-enqueues a resume's pending items: this authority's queue died
// with the process that built it, so the loader-supplied list is the only
// source. (The durable authority ignores the list — its rows survived — and
// resets their orphaned claims instead.)
func (q *memQueue) Recover(pending []frontier.Item) error {
	q.mu.Lock()
	q.items = append(q.items, pending...)
	q.mu.Unlock()
	return nil
}
