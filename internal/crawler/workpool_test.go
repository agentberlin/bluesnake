package crawler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

// testPool builds a pool over q whose queue errors fail the test.
func testPool(t *testing.T, q frontier.Queue, threads int) *workPool {
	t.Helper()
	return newWorkPool(q, threads, func(err error) { t.Errorf("queue error surfaced: %v", err) })
}

// publish is the crawler's enqueue path with admission stripped out: publish
// the item to the work-queue authority, then wake the feeder.
func publish(t *testing.T, p *workPool, it frontier.Item) {
	t.Helper()
	if err := p.queue.Enqueue(it); err != nil {
		t.Errorf("Enqueue(%q): %v", it.URL, err)
	}
	p.notify()
}

// drain starts the feeder plus n workers over the pool, applying produce() to
// each pulled item (which may publish children) before done(), and fails the
// test if the workers do not all terminate within the deadline (a feeder
// stall, a deadlock, or a leaked counter). It returns the items processed.
func drain(t *testing.T, p *workPool, n int, produce func(frontier.Item)) int64 {
	t.Helper()
	go p.feed(context.Background())
	var processed int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for {
				it, ok := p.pull()
				if !ok {
					return
				}
				produce(it) // publishes children BEFORE done — the invariant under test
				atomic.AddInt64(&processed, 1)
				p.done()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("workers did not terminate — feeder stall, deadlock, or leaked in-flight counter")
	}
	return atomic.LoadInt64(&processed)
}

// TestWorkPool_TreeNoEarlyTermination (WP-01) pins the load-bearing termination
// invariant across the feeder hand-off: a worker publishes its children to the
// authority BEFORE decrementing its own item, and the feeder re-claims once
// after in-flight hits zero, so the buffer never closes with reachable work
// left. A balanced tree has a known node count; every node must be processed
// exactly once, under many workers, every run (-race).
func TestWorkPool_TreeNoEarlyTermination(t *testing.T) {
	const depth, branch = 4, 6 // 1+6+36+216+1296 = 1555 nodes
	want := int64(0)
	for d, lvl := 0, int64(1); d <= depth; d, lvl = d+1, lvl*branch {
		want += lvl
	}
	for run := 0; run < 50; run++ {
		p := testPool(t, &memQueue{}, 8)
		publish(t, p, frontier.Item{URL: "root", Depth: 0})
		got := drain(t, p, 8, func(it frontier.Item) {
			if it.Depth < depth {
				for j := 0; j < branch; j++ {
					publish(t, p, frontier.Item{URL: fmt.Sprintf("%s.%d", it.URL, j), Depth: it.Depth + 1})
				}
			}
		})
		if got != want {
			t.Fatalf("run %d: processed %d nodes, want %d (early termination / lost subtree)", run, got, want)
		}
		if n := p.inflight.Load(); n != 0 {
			t.Fatalf("run %d: in-flight counter = %d after drain, want 0", run, n)
		}
	}
}

// TestWorkPool_HighFanoutSpillsNotBlocks (WP-03) pins the bounded-buffer spill
// property: a single worker whose one item fans out to thousands of children —
// far past the ready-buffer's capacity — must never wedge. Workers never push
// (the overflow lives in the authority until the feeder claims it), so the old
// sole-producer deadlock is impossible by construction; the assertion is that
// every spilled child still gets processed, in batches, to completion.
func TestWorkPool_HighFanoutSpillsNotBlocks(t *testing.T) {
	p := testPool(t, &memQueue{}, 1) // buffer cap 32 — 10k children spill
	publish(t, p, frontier.Item{URL: "root", Depth: 0})
	const children = 10000
	got := drain(t, p, 1, func(it frontier.Item) {
		if it.Depth == 0 {
			for j := 0; j < children; j++ {
				publish(t, p, frontier.Item{URL: fmt.Sprintf("c%d", j), Depth: 1})
			}
		}
	})
	if got != children+1 {
		t.Errorf("processed %d, want %d (root + children)", got, children+1)
	}
}

// TestWorkPool_NoWorkClosesImmediately: with nothing ever published, the
// feeder's first claim comes back empty with zero in-flight, so it closes the
// buffer and workers exit at once rather than block forever.
func TestWorkPool_NoWorkClosesImmediately(t *testing.T) {
	p := testPool(t, &memQueue{}, 4)
	got := drain(t, p, 4, func(frontier.Item) {})
	if got != 0 {
		t.Errorf("processed %d over an empty pool, want 0", got)
	}
}

// countingQueue counts ClaimBatch calls, to pin the feeder's wakeup discipline.
type countingQueue struct {
	memQueue
	claims atomic.Int64
}

func (q *countingQueue) ClaimBatch(n int) ([]frontier.Item, error) {
	q.claims.Add(1)
	return q.memQueue.ClaimBatch(n)
}

// TestWorkPool_FeederNoBusyLoop (WP-15) pins that a feeder with nothing to do
// PARKS instead of re-polling the authority: while the only in-flight item sits
// in a blocked worker and the authority is empty, the claim count must not
// grow. (A busy-looping feeder would burn CPU and WAL round-trips for the
// whole life of every large crawl.)
func TestWorkPool_FeederNoBusyLoop(t *testing.T) {
	q := &countingQueue{}
	p := testPool(t, q, 1)
	publish(t, p, frontier.Item{URL: "root", Depth: 0})

	gate := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go p.feed(context.Background())
	go func() {
		defer close(done)
		for {
			it, ok := p.pull()
			if !ok {
				return
			}
			if it.URL == "root" {
				close(gate) // signal: the worker now holds the only in-flight item
				<-release   // ...and blocks, with the authority drained
			}
			p.done()
		}
	}()

	<-gate
	// Let the feeder finish any in-progress claim round, then measure quiescence.
	time.Sleep(50 * time.Millisecond)
	before := q.claims.Load()
	time.Sleep(200 * time.Millisecond)
	if after := q.claims.Load(); after > before+1 {
		t.Errorf("feeder issued %d claims while parked (want ≤1) — busy-looping against the authority", after-before)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pool did not terminate after release")
	}
}

// TestWorkerPool_AdmitRejectionBalancesCounter (WP-02) drives the real crawler
// under heavy dedup (a fully-connected graph: every page links to every page, so
// the vast majority of discoveries are Admit-rejected). A counter that leaked a
// +1 on rejection would never reach zero and Run would hang; a watchdog fails
// fast if it does, and the crawl must still cover exactly the distinct pages.
func TestWorkerPool_AdmitRejectionBalancesCounter(t *testing.T) {
	const pages = 40
	bodies := map[string]string{}
	var all strings.Builder
	for i := 0; i < pages; i++ {
		all.WriteString(link(fmt.Sprintf("/p%d", i)))
	}
	body := "<html><body>" + all.String() + "</body></html>"
	bodies["/"] = body
	for i := 0; i < pages; i++ {
		bodies[fmt.Sprintf("/p%d", i)] = body // every page links to every page
	}
	s := newSite(t, bodies)

	cfg := config.Default()
	cfg.Speed.MaxThreads = 8
	sink := newCapSink()
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		res *Result
		err error
	}
	ch := make(chan result, 1)
	go func() {
		res, err := c.Run(context.Background(), s.server.URL+"/")
		ch <- result{res, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		// seed + p0..p39 = 41 distinct pages, each crawled exactly once.
		if r.res.Crawled != pages+1 {
			t.Errorf("crawled = %d, want %d (each distinct page once)", r.res.Crawled, pages+1)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return — Admit-rejected discovery leaked the in-flight counter")
	}
}
