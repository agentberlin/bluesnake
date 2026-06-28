package queue

import (
	"context"
	"sync"
)

// Dispatcher is the crawl consumer. It runs up to `concurrency` crawls at once
// (default 1): each of W drain loops claims the oldest queued job via the atomic
// ClaimNext (so W loops never double-claim), runs it through the Executor to
// completion, records the outcome, then claims the next. Enqueue wakes a loop; a
// loop that claims a job wakes another (wake-the-next), so a burst of jobs
// spreads across idle loops instead of draining serially.
type Dispatcher struct {
	store       Store
	exec        Executor
	concurrency int

	wakeCh chan struct{}
	stopCh chan struct{}

	mu       sync.Mutex
	started  bool
	stopping bool
	inflight map[string]*inFlightJob // jobID -> running job

	wg sync.WaitGroup // the W drain loops
}

// inFlightJob is a job whose crawl is currently running; crawlID fills in once
// the crawl exists (so Cancel/Current can address it).
type inFlightJob struct {
	job     Job
	crawlID string
}

// New builds a dispatcher over a job store and a crawl executor. By default it
// runs one crawl at a time; WithConcurrency raises the parallelism.
func New(s Store, e Executor, opts ...Option) *Dispatcher {
	d := &Dispatcher{
		store:       s,
		exec:        e,
		concurrency: 1,
		wakeCh:      make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
		inflight:    map[string]*inFlightJob{},
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// WithConcurrency caps how many crawls run in parallel (clamped to >= 1). A
// shared limiter on the Executor still bounds total fetches across them, so the
// per-crawl fixed overhead — not the fetch concurrency — is what this bounds.
func WithConcurrency(n int) Option {
	return func(d *Dispatcher) {
		if n < 1 {
			n = 1
		}
		d.concurrency = n
	}
}

// Start reconciles any job left running by a previous crash (-> interrupted, the
// partial crawl stays resumable) and launches the W drain loops. Idempotent.
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = true
	n := d.concurrency
	d.mu.Unlock()

	if _, err := d.store.Reconcile(); err != nil {
		return err
	}
	d.wg.Add(n)
	for i := 0; i < n; i++ {
		go d.loop(ctx)
	}
	return nil
}

// Enqueue adds a job and wakes a loop.
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

// Current returns one in-flight job (with its crawl id once known), or nil when
// idle. With several crawls running it returns an arbitrary one — enough for the
// single-crawl surfaces to answer "is a crawl running"; CurrentAll lists them all.
func (d *Dispatcher) Current() *Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.inflight {
		j := f.job
		j.CrawlID = f.crawlID
		return &j
	}
	return nil
}

// CurrentAll returns every in-flight job (with crawl ids), for surfaces that
// drive parallel crawls.
func (d *Dispatcher) CurrentAll() []Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Job, 0, len(d.inflight))
	for _, f := range d.inflight {
		j := f.job
		j.CrawlID = f.crawlID
		out = append(out, j)
	}
	return out
}

// Pause asks every in-flight crawl to pause (left resumable); no-op when idle.
func (d *Dispatcher) Pause() { d.exec.Pause() }

// Stop asks every in-flight crawl to stop and finalise as completed; no-op idle.
func (d *Dispatcher) Stop() { d.exec.Stop() }

// Cancel cancels a job. A still-queued job is dropped; an in-flight job is
// stopped by crawl id (finalised as completed), targeting just that one crawl
// even when several run in parallel. Reports whether anything changed.
func (d *Dispatcher) Cancel(jobID string) (bool, error) {
	d.mu.Lock()
	f := d.inflight[jobID]
	crawlID := ""
	if f != nil {
		crawlID = f.crawlID
	}
	d.mu.Unlock()
	if f != nil {
		d.exec.StopCrawl(crawlID)
		return true, nil
	}
	return d.store.Cancel(jobID)
}

// Shutdown pauses every in-flight crawl (leaving them resumable) and stops all
// drain loops, waiting for them to exit. Safe to call when not started.
func (d *Dispatcher) Shutdown() {
	d.mu.Lock()
	if !d.started || d.stopping {
		d.mu.Unlock()
		return
	}
	d.stopping = true
	d.mu.Unlock()

	close(d.stopCh)
	d.exec.Pause() // turn every in-flight crawl around so its Run returns
	d.wg.Wait()    // wait for every drain loop to exit
}

func (d *Dispatcher) wake() {
	select {
	case d.wakeCh <- struct{}{}:
	default: // a wake is already pending; a loop will see the new job
	}
}

func (d *Dispatcher) loop(ctx context.Context) {
	defer d.wg.Done()
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
		d.wake() // wake-the-next: another idle loop can grab the next queued job
		d.runJob(ctx, job)
	}
}

func (d *Dispatcher) runJob(ctx context.Context, job *Job) {
	f := &inFlightJob{job: *job}
	d.mu.Lock()
	d.inflight[job.ID] = f
	d.mu.Unlock()

	status, runErr := d.exec.Run(ctx, job.Spec, func(crawlID string) {
		d.mu.Lock()
		f.crawlID = crawlID
		d.mu.Unlock()
		_ = d.store.SetCrawlID(job.ID, crawlID)
	})

	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	_ = d.store.Finish(job.ID, jobStatusFor(status, runErr), errMsg)

	d.mu.Lock()
	delete(d.inflight, job.ID)
	d.mu.Unlock()
}
