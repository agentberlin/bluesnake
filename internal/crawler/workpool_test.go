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

// drain runs n workers over the pool, applying produce() to each pulled item
// (which may push children) before done(), and fails the test if the workers do
// not all terminate within the deadline (a deadlock or a leaked counter). It
// returns the number of items processed.
func drain(t *testing.T, p *workPool, n int, produce func(frontier.Item)) int64 {
	t.Helper()
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
				produce(it) // pushes children BEFORE done — the invariant under test
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
		t.Fatal("workers did not terminate — deadlock or leaked in-flight counter")
	}
	return atomic.LoadInt64(&processed)
}

// TestWorkPool_TreeNoEarlyTermination (WP-01) pins the load-bearing termination
// invariant: a worker pushes its children (each bumping in-flight) BEFORE
// decrementing its own item, so the queue never closes with reachable work left.
// A balanced tree has a known node count; every node must be processed exactly
// once, under many workers, every run (-race).
func TestWorkPool_TreeNoEarlyTermination(t *testing.T) {
	const depth, branch = 4, 6 // 1+6+36+216+1296 = 1555 nodes
	want := int64(0)
	for d, lvl := 0, int64(1); d <= depth; d, lvl = d+1, lvl*branch {
		want += lvl
	}
	for run := 0; run < 50; run++ {
		p := newWorkPool()
		p.push(frontier.Item{URL: "root", Depth: 0})
		got := drain(t, p, 8, func(it frontier.Item) {
			if it.Depth < depth {
				for j := 0; j < branch; j++ {
					p.push(frontier.Item{URL: fmt.Sprintf("%s.%d", it.URL, j), Depth: it.Depth + 1})
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

// TestWorkPool_SoleProducerNoDeadlock (WP-03) pins the deadlock-free property:
// a single worker that pushes thousands of children into its own queue must not
// block on the push (the queue is unbounded), then drains them all.
func TestWorkPool_SoleProducerNoDeadlock(t *testing.T) {
	p := newWorkPool()
	p.push(frontier.Item{URL: "root", Depth: 0})
	const children = 10000
	got := drain(t, p, 1, func(it frontier.Item) {
		if it.Depth == 0 {
			for j := 0; j < children; j++ {
				p.push(frontier.Item{URL: fmt.Sprintf("c%d", j), Depth: 1})
			}
		}
	})
	if got != children+1 {
		t.Errorf("processed %d, want %d (root + children)", got, children+1)
	}
}

// TestWorkPool_NoWorkClosesImmediately covers closeIfDrained: with nothing ever
// pushed, workers must exit at once rather than block forever on an empty queue.
func TestWorkPool_NoWorkClosesImmediately(t *testing.T) {
	p := newWorkPool()
	p.closeIfDrained()
	got := drain(t, p, 4, func(frontier.Item) {})
	if got != 0 {
		t.Errorf("processed %d over an empty pool, want 0", got)
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
