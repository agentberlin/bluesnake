package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
)

// Runner is the CLI/standalone-MCP Backend: the same single-crawl-at-a-time
// behaviour as before, but routed through the core queue wiring. It owns an
// in-memory queue and a runner.Executor (so the interface doesn't dictate how a
// crawl runs — the dispatcher does), and adapts that to mcp.Backend. The queue
// is in-process and transient: jobs drain here, in this server, and a second
// start while one runs is rejected (preserving the historical contract) rather
// than silently queued. Runner is its own runner.Observer, using OnStart/OnDone
// to turn the async dispatch back into a synchronous "return the crawl id".
type Runner struct {
	storeDir string
	exec     *runner.Executor
	disp     *queue.Dispatcher

	mu      sync.Mutex
	pending chan startResult // set while a start/resume awaits its crawl id
}

type startResult struct {
	id  string
	err error
}

func NewRunner(storeDir string) *Runner {
	r := &Runner{storeDir: storeDir}
	r.exec = runner.New(storeDir, r)
	// One crawl at a time by design (no WithConcurrency): the MCP tools — crawl_status,
	// issue_summary — assume a single current crawl, and the server's contract is
	// one crawl per server. The executor's per-crawl fallback limiter is therefore
	// the process-wide fetch cap (only one in-flight crawl). speed.max_concurrent_crawls
	// drives the CLI's parallel `projects crawl-all`, not this dispatcher; wiring
	// concurrency>1 here would also need one shared limiter via runner.WithLimiter
	// (P6/P7/P17).
	r.disp = queue.New(queue.NewMemStore(), r.exec)
	_ = r.disp.Start(context.Background())
	return r
}

func (r *Runner) StoreDir() string { return r.storeDir }

// runner.Observer ---------------------------------------------------------

func (r *Runner) OnStart(crawlID, seed string) { r.settle(startResult{id: crawlID}) }
func (r *Runner) OnPage(*crawler.PageRecord)   {}
func (r *Runner) OnDone(o runner.Outcome) {
	// If a start was still awaiting its crawl id, the crawl never started (e.g. a
	// run-time sitemap fetch failed); surface the error to the waiting StartCrawl.
	r.settle(startResult{err: o.Err})
}

// settle delivers the outcome of a pending start exactly once.
func (r *Runner) settle(res startResult) {
	r.mu.Lock()
	ch := r.pending
	r.pending = nil
	r.mu.Unlock()
	if ch != nil {
		ch <- res
	}
}

// mcp.Backend -------------------------------------------------------------

func (r *Runner) StartCrawl(ctx context.Context, req StartRequest) (string, error) {
	if err := runner.ValidateSpec(r.storeDir, req.Spec()); err != nil {
		return "", err
	}
	return r.enqueueAndAwait(ctx, req.Spec(), req.Label())
}

func (r *Runner) ResumeCrawl(id string) (string, error) {
	return r.enqueueAndAwait(context.Background(), queue.JobSpec{ResumeID: id}, "resume "+id)
}

// enqueueAndAwait enqueues a job and blocks until its crawl is created (or the
// attempt fails), returning the crawl id. A second start while one is in flight
// is rejected, keeping the one-crawl-at-a-time contract.
func (r *Runner) enqueueAndAwait(ctx context.Context, spec queue.JobSpec, label string) (string, error) {
	r.mu.Lock()
	if cur := r.disp.Current(); cur != nil {
		r.mu.Unlock()
		return "", fmt.Errorf("a crawl is already running (crawl %s) — pause_crawl or stop_crawl first", cur.CrawlID)
	}
	if r.pending != nil {
		r.mu.Unlock()
		return "", fmt.Errorf("a crawl is already starting")
	}
	ch := make(chan startResult, 1)
	r.pending = ch
	r.mu.Unlock()

	if _, err := r.disp.Enqueue(spec, "manual", "", label); err != nil {
		r.settle(startResult{}) // clear pending
		return "", err
	}
	select {
	case res := <-ch:
		return res.id, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (r *Runner) PauseCrawl() error { return r.signal(r.disp.Pause) }
func (r *Runner) StopCrawl() error  { return r.signal(r.disp.Stop) }

func (r *Runner) signal(fn func()) error {
	if r.disp.Current() == nil {
		return fmt.Errorf("no crawl is running")
	}
	fn()
	return nil
}

func (r *Runner) Progress() *Progress {
	snap, ok := r.exec.Snapshot()
	if !ok {
		return nil
	}
	return &Progress{
		CrawlID: snap.CrawlID, Seed: snap.Seed, State: "running",
		Total: snap.Total, Discovered: snap.Discovered, Queue: snap.Queue,
		S2xx: snap.S2xx, S3xx: snap.S3xx, S4xx: snap.S4xx, S5xx: snap.S5xx,
		Blocked: snap.Blocked, NoResponse: snap.NoResponse, Indexable: snap.Indexable,
		RatePerSec: snap.RatePerSec, ElapsedSec: snap.ElapsedSec,
	}
}

// Shutdown pauses any live crawl (leaving it resumable) and stops the dispatcher
// — called when the MCP transport closes.
func (r *Runner) Shutdown() { r.disp.Shutdown() }
