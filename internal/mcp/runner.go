package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
)

// Runner is the CLI/standalone-MCP Backend, routed through the core queue
// wiring: an in-memory queue drained by the shared dispatcher/executor (so the
// interface doesn't dictate how a crawl runs). It runs up to
// speed.max_concurrent_crawls crawls at once — the knob is re-read from the
// default profile at every start (liveMaxCrawls), so a profile edit while the
// server runs applies to the next start_crawl, no restart. ONE shared
// process-wide limiter is injected regardless of the width (H1/P17 — the
// width is live, so the executor's single-crawl fallback can't be relied on).
// A start beyond the current capacity is rejected (the historical
// one-crawl-at-a-time contract, generalised to W slots) rather than silently
// queued. The start handshake is per job: enqueue, then await THAT job's crawl
// id via the job store — with several starts in flight a shared "started"
// signal could not associate a crawl with its caller (#78).
type Runner struct {
	storeDir string
	exec     *runner.Executor
	disp     *queue.Dispatcher
	lim      *limiter.Limiter // the shared process limiter every crawl runs under

	startMu sync.Mutex // serializes the capacity check against racing starts
}

func NewRunner(storeDir string) *Runner {
	r := &Runner{storeDir: storeDir}
	// An unreadable default profile fails safe to single-crawl here; the same
	// profile is what the first start_crawl loads, so the error surfaces there.
	w, lim, err := runner.ProcessWiring(storeDir)
	if err != nil {
		w = 1
	}
	r.lim = lim
	var opts []runner.Option
	if lim != nil {
		opts = append(opts, runner.WithLimiter(lim))
	}
	r.exec = runner.New(storeDir, nil, opts...)
	r.disp = queue.New(queue.NewMemStore(), r.exec, queue.WithConcurrency(w))
	_ = r.disp.Start(context.Background())
	return r
}

// liveMaxCrawls re-reads speed.max_concurrent_crawls from the default profile,
// retargets the dispatcher (SetConcurrency — raising applies immediately,
// lowering never interrupts a running crawl), and returns the width the
// capacity check should enforce. An unreadable profile keeps the current width.
func (r *Runner) liveMaxCrawls() int {
	cfg, err := runner.LoadProfile(r.storeDir, "")
	if err != nil {
		return r.disp.Concurrency()
	}
	w := cfg.Speed.MaxConcurrentCrawls
	if w < 1 {
		w = 1
	}
	r.disp.SetConcurrency(w)
	return w
}

func (r *Runner) StoreDir() string { return r.storeDir }

func (r *Runner) ProcessLimiter() *limiter.Limiter { return r.lim }

// mcp.Backend -------------------------------------------------------------

func (r *Runner) StartCrawl(ctx context.Context, req StartRequest) (string, error) {
	// freeze the effective config at enqueue (profile + overrides → ConfigYAML),
	// so a profile edit while the job waits can't reshape it
	spec, err := runner.FreezeSpec(r.storeDir, req.Spec())
	if err != nil {
		return "", err
	}
	return StartViaQueue(ctx, r.disp, r.liveMaxCrawls(), &r.startMu, spec, req.Label())
}

func (r *Runner) ResumeCrawl(id string) (string, error) {
	return StartViaQueue(context.Background(), r.disp, r.liveMaxCrawls(),
		&r.startMu, queue.JobSpec{ResumeID: id}, "resume "+id)
}

func (r *Runner) PauseCrawl(crawlID string) error {
	if _, ok := r.exec.SnapshotCrawl(crawlID); !ok {
		return fmt.Errorf("crawl %s is not running", crawlID)
	}
	r.disp.PauseCrawl(crawlID)
	return nil
}

func (r *Runner) StopCrawl(crawlID string) error {
	if _, ok := r.exec.SnapshotCrawl(crawlID); !ok {
		return fmt.Errorf("crawl %s is not running", crawlID)
	}
	r.disp.StopCrawl(crawlID)
	return nil
}

func (r *Runner) Running() []Progress {
	snaps := r.exec.Snapshots()
	out := make([]Progress, len(snaps))
	for i, s := range snaps {
		out[i] = ProgressFromSnapshot(s)
	}
	return out
}

// Shutdown pauses any live crawls (leaving them resumable) and stops the
// dispatcher — called when the MCP transport closes.
func (r *Runner) Shutdown() { r.disp.Shutdown() }
