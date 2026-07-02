package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
)

// Runner is the CLI/standalone-MCP Backend, routed through the core queue
// wiring: an in-memory queue drained by the shared dispatcher/executor (so the
// interface doesn't dictate how a crawl runs). It runs up to
// speed.max_concurrent_crawls crawls at once — the knob is read from the
// default profile at NewRunner (restart the server to apply), the same base
// config start_crawl uses, with ONE shared process-wide limiter injected when
// parallel (H1/P17). A start beyond that capacity is rejected (the historical
// one-crawl-at-a-time contract, generalised to W slots) rather than silently
// queued. The start handshake is per job: enqueue, then await THAT job's crawl
// id via the job store — with several starts in flight a shared "started"
// signal could not associate a crawl with its caller (#78).
type Runner struct {
	storeDir  string
	exec      *runner.Executor
	disp      *queue.Dispatcher
	maxCrawls int

	startMu sync.Mutex // serializes the capacity check against racing starts
}

func NewRunner(storeDir string) *Runner {
	r := &Runner{storeDir: storeDir, maxCrawls: 1}
	// An unreadable default profile fails safe to single-crawl here; the same
	// profile is what the first start_crawl loads, so the error surfaces there.
	w, lim, err := runner.ProcessWiring(storeDir)
	if err == nil {
		r.maxCrawls = w
	}
	var opts []runner.Option
	if lim != nil {
		opts = append(opts, runner.WithLimiter(lim))
	}
	r.exec = runner.New(storeDir, nil, opts...)
	r.disp = queue.New(queue.NewMemStore(), r.exec, queue.WithConcurrency(r.maxCrawls))
	_ = r.disp.Start(context.Background())
	return r
}

func (r *Runner) StoreDir() string { return r.storeDir }

// mcp.Backend -------------------------------------------------------------

func (r *Runner) StartCrawl(ctx context.Context, req StartRequest) (string, error) {
	if err := runner.ValidateSpec(r.storeDir, req.Spec()); err != nil {
		return "", err
	}
	return StartViaQueue(ctx, r.disp, r.maxCrawls, &r.startMu, req.Spec(), req.Label())
}

func (r *Runner) ResumeCrawl(id string) (string, error) {
	return StartViaQueue(context.Background(), r.disp, r.maxCrawls, &r.startMu,
		queue.JobSpec{ResumeID: id}, "resume "+id)
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
