package queue

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// claimRetryDelay is how long a drain loop waits after a TRANSIENT ClaimNext
// error before retrying. Waiting solely on wakeCh would strand every queued
// job until the next Enqueue happens to wake the loop (#74 N8). Package-level
// so tests can shorten it.
var claimRetryDelay = 500 * time.Millisecond

// Dispatcher is the crawl consumer. It runs up to `concurrency` crawls at once
// (default 1; 0 = unlimited): each of W drain loops claims the oldest queued
// job via the atomic ClaimNext (so W loops never double-claim), runs it through
// the Executor to completion, records the outcome, then claims the next.
// Enqueue wakes a loop; a loop that claims a job wakes another (wake-the-next),
// so a burst of jobs spreads across idle loops instead of draining serially.
// Unlimited mode grows instead of capping: a loop that claims a job spawns its
// replacement first, so a free drainer always exists and every queued job
// starts immediately; idle loops converge back down to a single parked
// drainer. W is live: SetConcurrency retargets it at any time without a
// restart — raising spawns loops immediately, lowering retires loops as their
// current job ends.
type Dispatcher struct {
	store       Store
	exec        Executor
	concurrency int // target W; 0 = unlimited; live loops converge on it (mu)
	liveLoops   int // drain loops currently alive (mu)
	busyLoops   int // loops currently running a job (mu) — liveLoops-busyLoops = parked drainers

	wakeCh chan struct{}
	stopCh chan struct{}

	mu       sync.Mutex
	started  bool
	stopping bool
	startCtx context.Context         // Start's ctx, reused by loops SetConcurrency spawns later
	inflight map[string]*inFlightJob // jobID -> running job
	// pendingCancel latches a Cancel that lands in the claim→register gap —
	// the job's store row already says running but runJob has not yet
	// registered it in-flight — so runJob drops the job before its crawl
	// starts instead of Cancel silently no-opping (#74 N6).
	pendingCancel map[string]bool

	wg sync.WaitGroup // the W drain loops
}

// inFlightJob is a job whose crawl is currently running; crawlID fills in once
// the crawl exists (so Cancel/Current can address it). cancelled latches a Cancel
// that arrives in the startup window — after the job is registered in-flight but
// before its crawlID is known — so the crawl is stopped the moment its id lands.
type inFlightJob struct {
	job       Job
	crawlID   string
	cancelled bool
}

// New builds a dispatcher over a job store and a crawl executor. By default it
// runs one crawl at a time; WithConcurrency raises the parallelism.
func New(s Store, e Executor, opts ...Option) *Dispatcher {
	d := &Dispatcher{
		store:         s,
		exec:          e,
		concurrency:   1,
		wakeCh:        make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		inflight:      map[string]*inFlightJob{},
		pendingCancel: map[string]bool{},
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// WithConcurrency sets how many crawls run in parallel: n >= 1 caps the width
// at n; 0 (and below, matching the limiter's <=0 convention) means UNLIMITED —
// every queued job starts immediately, each crawl on its own drain loop. A
// shared limiter on the Executor still bounds total fetches across them, so the
// per-crawl fixed overhead — not the fetch concurrency — is what this bounds.
// SetConcurrency retargets the same value after construction, live.
func WithConcurrency(n int) Option {
	return func(d *Dispatcher) {
		if n < 0 {
			n = 0
		}
		d.concurrency = n
	}
}

// Start reconciles any job left running by a previous crash (-> interrupted, the
// partial crawl stays resumable) and launches the W drain loops. Idempotent.
// A Reconcile failure leaves the dispatcher UNSTARTED so the caller can retry —
// latching `started` before it would permanently kill the dispatcher on one
// transient registry error: jobs accepted forever, never drained (#74 N4).
func (d *Dispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	if _, err := d.store.Reconcile(); err != nil {
		return err
	}

	d.mu.Lock()
	if d.started { // a concurrent Start won the race while we reconciled
		d.mu.Unlock()
		return nil
	}
	d.started = true
	d.startCtx = ctx
	n := d.concurrency
	if n == 0 {
		// Unlimited: one parked drainer is enough — its first claim spawns a
		// replacement, so a startup backlog cascades fully into flight.
		n = 1
	}
	d.liveLoops = n
	d.mu.Unlock()

	d.wg.Add(n)
	for i := 0; i < n; i++ {
		go d.loop(ctx)
	}
	return nil
}

// SetConcurrency retargets how many crawls may run at once, live — no restart.
// 0 = unlimited. Raising (or going unlimited) spawns drain loops immediately,
// so already-queued jobs start without waiting for a running crawl to finish.
// Lowering it never interrupts a running crawl: excess loops retire as their
// current job ends (idle loops retire at once). Callable at any time; before
// Start it just replaces the WithConcurrency value.
func (d *Dispatcher) SetConcurrency(n int) {
	if n < 0 {
		n = 0
	}
	d.mu.Lock()
	d.concurrency = n
	spawn := 0
	if d.started && !d.stopping {
		if n == 0 {
			// Unlimited: ensure a free drainer exists — one is enough, its
			// first claim spawns a replacement so the whole backlog cascades
			// into flight. Skip when an idle loop is already parked.
			if idle := d.liveLoops - d.busyLoops; idle < 1 {
				spawn = 1
			}
		} else if delta := n - d.liveLoops; delta > 0 {
			spawn = delta
		}
	}
	if spawn > 0 {
		d.liveLoops += spawn
		d.wg.Add(spawn)
		for i := 0; i < spawn; i++ {
			go d.loop(d.startCtx)
		}
	}
	d.mu.Unlock()
	// Nudge a parked loop: on lowering it notices the smaller target and
	// retires; on other transitions a spurious wake is a harmless no-op.
	d.wake()
}

// Concurrency reports the current target for how many crawls run at once
// (0 = unlimited).
func (d *Dispatcher) Concurrency() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.concurrency
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
// idle. With several crawls running it returns an arbitrary one — enough to
// answer "is a crawl running"; parallel-aware callers use CurrentAll.
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

// CurrentAll returns every in-flight job (with crawl ids once known), oldest
// first. A job claimed but still in its startup window has an empty CrawlID.
func (d *Dispatcher) CurrentAll() []Job {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Job, 0, len(d.inflight))
	for _, f := range d.inflight {
		j := f.job
		j.CrawlID = f.crawlID
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// Pause asks every in-flight crawl to pause (left resumable); no-op when idle.
func (d *Dispatcher) Pause() { d.exec.Pause() }

// Stop asks every in-flight crawl to stop and finalise as completed; no-op idle.
func (d *Dispatcher) Stop() { d.exec.Stop() }

// PauseCrawl pauses one specific in-flight crawl by id (left resumable),
// leaving every other running crawl untouched. Surfaces address per-crawl
// control through the dispatcher — never the executor directly — so the
// layering stays one-way (surface → dispatcher → executor).
func (d *Dispatcher) PauseCrawl(crawlID string) { d.exec.PauseCrawl(crawlID) }

// StopCrawl stops one specific in-flight crawl by id, finalising it as
// completed; every other running crawl is untouched.
func (d *Dispatcher) StopCrawl(crawlID string) { d.exec.StopCrawl(crawlID) }

// AwaitCrawl blocks until jobID's crawl exists (returning its crawl id) or the
// job reaches a terminal state without one (returning its failure). It is the
// blocking-start handshake the MCP surfaces put behind start_crawl: keyed by
// job id, so with several starts in flight each caller gets its own crawl id —
// a shared "the current crawl started" signal cannot associate them (#78).
func (d *Dispatcher) AwaitCrawl(ctx context.Context, jobID string) (string, error) {
	deadline := time.Now().Add(30 * time.Second) // failsafe: a claimed job starts within ticks
	for time.Now().Before(deadline) {
		jobs, err := d.store.List()
		if err != nil {
			return "", err
		}
		for _, j := range jobs {
			if j.ID != jobID {
				continue
			}
			if j.CrawlID != "" {
				if j.Status == store.JobFailed && j.Error != "" {
					return j.CrawlID, fmt.Errorf("%s", j.Error)
				}
				return j.CrawlID, nil
			}
			switch j.Status {
			case store.JobFailed:
				msg := j.Error
				if msg == "" {
					msg = "crawl failed to start"
				}
				return "", fmt.Errorf("%s", msg)
			case store.JobCanceled:
				return "", fmt.Errorf("crawl was canceled")
			case store.JobInterrupted:
				return "", fmt.Errorf("crawl was interrupted before it started")
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("crawl did not start in time")
}

// Cancel cancels a job. A still-queued job is dropped; an in-flight job is
// stopped by crawl id (finalised as completed), targeting just that one crawl
// even when several run in parallel. A job caught in the claim→register gap —
// its store row already running, runJob not yet started — is latched so runJob
// drops it before the crawl begins (#74 N6). Reports whether anything changed.
func (d *Dispatcher) Cancel(jobID string) (bool, error) {
	if ok := d.cancelInFlight(jobID); ok {
		return true, nil
	}
	if changed, err := d.store.Cancel(jobID); changed || err != nil {
		return changed, err
	}
	// Neither queued nor registered in-flight. If the store row says running,
	// the job sits in the claim gap (or registered while we looked): latch a
	// pending cancel — runJob checks the latch, under the same mutex, before
	// starting the crawl — then re-check the in-flight path once.
	d.mu.Lock()
	d.pendingCancel[jobID] = true
	d.mu.Unlock()
	if ok := d.cancelInFlight(jobID); ok {
		d.mu.Lock()
		delete(d.pendingCancel, jobID)
		d.mu.Unlock()
		return true, nil
	}
	jobs, err := d.store.List()
	if err == nil {
		for _, j := range jobs {
			if j.ID == jobID && j.Status == store.JobRunning {
				return true, nil // the latch will drop it (or runJob already honored it)
			}
		}
	}
	// Unknown or already terminal: nothing to cancel; clear the latch.
	d.mu.Lock()
	delete(d.pendingCancel, jobID)
	d.mu.Unlock()
	return false, err
}

// cancelInFlight cancels a registered in-flight job, reporting whether one was
// found. In the ms-wide startup window crawlID is still empty — StopCrawl("")
// would silently no-op while falsely reporting success, so it is skipped; the
// latched cancelled flag makes the crawl stop the instant its id lands (see
// runJob's onStart).
func (d *Dispatcher) cancelInFlight(jobID string) bool {
	d.mu.Lock()
	f := d.inflight[jobID]
	crawlID := ""
	if f != nil {
		f.cancelled = true
		crawlID = f.crawlID
	}
	d.mu.Unlock()
	if f == nil {
		return false
	}
	if crawlID != "" {
		d.exec.StopCrawl(crawlID)
	}
	return true
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
		// Retire when SetConcurrency lowered the target below the live loop
		// count — checked between jobs, so shrinking never interrupts a running
		// crawl. The re-wake hands any pending wake this loop would have
		// consumed to a surviving loop, so a job enqueued mid-retirement can't
		// strand; retiring loops chain the same wake until a survivor keeps it.
		// Unlimited (0) never retires here — its pool converges in the
		// empty-queue branch below instead.
		d.mu.Lock()
		if d.concurrency > 0 && d.liveLoops > d.concurrency {
			d.liveLoops--
			d.mu.Unlock()
			d.wake()
			return
		}
		d.mu.Unlock()

		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		job, err := d.store.ClaimNext()
		if err != nil {
			// A transient claim error must not park the loop on wakeCh alone:
			// with no further Enqueue to wake it, every queued job would strand
			// (#74 N8). Retry on a timer instead.
			select {
			case <-time.After(claimRetryDelay):
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			}
			continue
		}
		if job == nil {
			// Unlimited mode grows a loop per claimed job; this is the reverse
			// edge — with the queue empty, surplus idle loops retire until one
			// parked drainer remains beside the running crawls (liveLoops -
			// busyLoops is the idle count). The re-wake chains the convergence
			// (and hands off any pending wake) exactly like the bounded retire.
			d.mu.Lock()
			if d.concurrency == 0 && d.liveLoops-d.busyLoops > 1 {
				d.liveLoops--
				d.mu.Unlock()
				d.wake()
				return
			}
			d.mu.Unlock()
			// nothing to do — wait for a wake/stop
			select {
			case <-d.wakeCh:
			case <-d.stopCh:
				return
			case <-ctx.Done():
				return
			}
			continue
		}
		// A stop can land between ClaimNext and here (a loop already inside
		// ClaimNext when Shutdown fires). Starting a whole new crawl now would
		// make Shutdown block on it (#74 N5) — return the job to the queue.
		select {
		case <-d.stopCh:
			_ = d.store.Unclaim(job.ID)
			return
		case <-ctx.Done():
			_ = d.store.Unclaim(job.ID)
			return
		default:
		}
		// Unlimited: this loop goes busy for the whole crawl — mark it busy and
		// spawn its replacement in the same critical section, so a free drainer
		// always exists and a queued job never waits behind a running crawl.
		// (busyLoops, not the inflight map, is the busy count: a job registers
		// in-flight only inside runJob, and that gap would let the fresh
		// replacement mistake itself for surplus and retire. Bounded mode never
		// spawns here; its width is exactly the W loops Start/SetConcurrency
		// created.)
		d.mu.Lock()
		d.busyLoops++
		if d.concurrency == 0 && !d.stopping {
			d.liveLoops++
			d.wg.Add(1)
			go d.loop(ctx)
		}
		d.mu.Unlock()
		d.wake() // wake-the-next: another idle loop can grab the next queued job
		d.runJob(ctx, job)
		d.mu.Lock()
		d.busyLoops--
		d.mu.Unlock()
	}
}

func (d *Dispatcher) runJob(ctx context.Context, job *Job) {
	f := &inFlightJob{job: *job}
	d.mu.Lock()
	if d.pendingCancel[job.ID] {
		// A Cancel landed in the claim→register gap; honor it before any crawl
		// starts (#74 N6).
		delete(d.pendingCancel, job.ID)
		d.mu.Unlock()
		_ = d.store.Finish(job.ID, store.JobCanceled, "")
		return
	}
	d.inflight[job.ID] = f
	d.mu.Unlock()

	status, runErr := d.exec.Run(ctx, job.Spec, func(crawlID string) {
		d.mu.Lock()
		f.crawlID = crawlID
		cancelled := f.cancelled
		d.mu.Unlock()
		_ = d.store.SetCrawlID(job.ID, crawlID)
		if cancelled {
			// A Cancel landed during the startup window before this id was known;
			// honor it now that the crawl is addressable.
			d.exec.StopCrawl(crawlID)
		}
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
