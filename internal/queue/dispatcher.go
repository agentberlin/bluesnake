package queue

import (
	"context"
	"sync"
)

// Dispatcher is the single crawl consumer. It owns the one-crawl-at-a-time slot
// (replacing the old per-surface "a crawl is already running" mutex): it claims
// the oldest queued job, runs it through the Executor to completion, records the
// outcome, then claims the next. Enqueue wakes it; on idle it blocks until woken
// or shut down.
type Dispatcher struct {
	store Store
	exec  Executor

	wakeCh chan struct{}
	stopCh chan struct{}
	doneCh chan struct{}

	mu        sync.Mutex
	started   bool
	stopping  bool
	current   *Job   // the job whose crawl is in flight, nil when idle
	currentID string // crawl id of the in-flight job once known
}

// New builds a dispatcher over a job store and a crawl executor.
func New(s Store, e Executor) *Dispatcher {
	return &Dispatcher{
		store:  s,
		exec:   e,
		wakeCh: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start reconciles any job left running by a previous crash (-> interrupted, the
// partial crawl stays resumable) and launches the drain loop. Idempotent.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.mu.Unlock()

	if _, err := d.store.Reconcile(); err != nil {
		return err
	}
	go d.loop(ctx)
	return nil
}

// Enqueue adds a job and wakes the loop.
func (d *Dispatcher) Enqueue(spec JobSpec, source, projectID, label string) (Job, error) {
	j, err := d.store.Enqueue(spec, source, projectID, label)
	if err != nil {
		return Job{}, err
	}
	d.wake()
	return j, nil
}

// List returns the current queue (every job, in order).
func (d *Dispatcher) List() ([]Job, error) { return d.store.List() }

// Current returns the in-flight job (with its crawl id once known), or nil when
// the dispatcher is idle.
func (d *Dispatcher) Current() *Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == nil {
		return nil
	}
	j := *d.current
	j.CrawlID = d.currentID
	return &j
}

// Pause asks the in-flight crawl to pause (left resumable); no-op when idle.
func (d *Dispatcher) Pause() { d.exec.Pause() }

// Stop asks the in-flight crawl to stop and finalise as completed; no-op when idle.
func (d *Dispatcher) Stop() { d.exec.Stop() }

// Cancel cancels a job. A still-queued job is dropped; the in-flight job is
// stopped (finalised as completed). Reports whether anything changed.
func (d *Dispatcher) Cancel(jobID string) (bool, error) {
	d.mu.Lock()
	running := d.current != nil && d.current.ID == jobID
	d.mu.Unlock()
	if running {
		d.exec.Stop()
		return true, nil
	}
	return d.store.Cancel(jobID)
}

// Shutdown pauses any in-flight crawl (leaving it resumable) and stops the loop,
// waiting for it to exit. Safe to call when not started.
func (d *Dispatcher) Shutdown() {
	d.mu.Lock()
	if !d.started || d.stopping {
		d.mu.Unlock()
		return
	}
	d.stopping = true
	d.mu.Unlock()

	close(d.stopCh)
	d.exec.Pause() // turn the in-flight crawl around so Run returns
	<-d.doneCh
}

func (d *Dispatcher) wake() {
	select {
	case d.wakeCh <- struct{}{}:
	default: // a wake is already pending; the loop will see the new job
	}
}

func (d *Dispatcher) loop(ctx context.Context) {
	defer close(d.doneCh)
	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		job, err := d.store.ClaimNext()
		if err != nil || job == nil {
			// nothing to do (or a transient claim error) — wait for a wake/stop
			select {
			case <-d.wakeCh:
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			}
			continue
		}
		d.runJob(ctx, job)
	}
}

func (d *Dispatcher) runJob(ctx context.Context, job *Job) {
	d.mu.Lock()
	d.current = job
	d.currentID = ""
	d.mu.Unlock()

	status, runErr := d.exec.Run(ctx, job.Spec, func(crawlID string) {
		d.mu.Lock()
		d.currentID = crawlID
		d.mu.Unlock()
		_ = d.store.SetCrawlID(job.ID, crawlID)
	})

	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	_ = d.store.Finish(job.ID, jobStatusFor(status, runErr), errMsg)

	d.mu.Lock()
	d.current = nil
	d.currentID = ""
	d.mu.Unlock()
}
