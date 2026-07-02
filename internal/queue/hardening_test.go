package queue

// #74 N4/N5/N6/N8 — dispatcher lifecycle hardening. Each test models one
// operational edge the drain loops used to mishandle: a transient Reconcile
// error permanently killing the dispatcher, a shutdown racing ClaimNext into a
// fresh crawl, a Cancel landing in the claim→register gap, and a transient
// claim error stranding the queue.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

var errStore = errors.New("registry unavailable")

// flakyStore wraps MemStore, failing a set number of Reconcile/ClaimNext calls.
type flakyStore struct {
	*MemStore
	reconcileErrs atomic.Int32
	claimErrs     atomic.Int32
}

func (f *flakyStore) Reconcile() (int, error) {
	if f.reconcileErrs.Add(-1) >= 0 {
		return 0, errStore
	}
	return f.MemStore.Reconcile()
}

func (f *flakyStore) ClaimNext() (*Job, error) {
	if f.claimErrs.Add(-1) >= 0 {
		return nil, errStore
	}
	return f.MemStore.ClaimNext()
}

// TestStartRetryableAfterReconcileError (#74 N4): a Reconcile failure must not
// latch the dispatcher as started — the old code did, so every later Start
// no-opped and jobs were accepted forever without a single drain loop running.
func TestStartRetryableAfterReconcileError(t *testing.T) {
	st := &flakyStore{MemStore: NewMemStore()}
	st.reconcileErrs.Store(1)
	exec := newFakeExec()
	d := New(st, exec)

	if err := d.Start(context.Background()); err == nil {
		t.Fatal("Start with a failing Reconcile should return the error")
	}
	// The failure is transient; a retry must actually start the drain loops.
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("retried Start: %v", err)
	}
	if _, err := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a"); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec) // hangs (fails) if no loop ever started
	exec.complete()
	d.Shutdown()
}

// gatedClaimStore blocks inside ClaimNext (before the claim) until released,
// so a test can hold a drain loop exactly there.
type gatedClaimStore struct {
	*MemStore
	claiming chan struct{}
	gate     chan struct{}
	armed    atomic.Bool
}

func (g *gatedClaimStore) ClaimNext() (*Job, error) {
	if g.armed.Load() {
		g.claiming <- struct{}{}
		<-g.gate
	}
	return g.MemStore.ClaimNext()
}

// TestShutdownDoesNotStartNewCrawlAfterStop (#74 N5): a loop already inside
// ClaimNext when Shutdown fires must NOT start the crawl it claims — Shutdown
// would block on it (the old code hung on wg.Wait while the fresh crawl ran).
// The claimed job goes back to queued.
func TestShutdownDoesNotStartNewCrawlAfterStop(t *testing.T) {
	st := &gatedClaimStore{MemStore: NewMemStore(), claiming: make(chan struct{}), gate: make(chan struct{})}
	exec := newFakeExec()
	d := New(st, exec)
	job, _ := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	st.armed.Store(true)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	<-st.claiming // a loop is now inside ClaimNext
	done := make(chan struct{})
	go func() { d.Shutdown(); close(done) }()
	// Give Shutdown a moment to close stopCh, then let ClaimNext return the job.
	time.Sleep(50 * time.Millisecond)
	st.armed.Store(false)
	close(st.gate)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not return — the loop started a new crawl after the stop signal (N5)")
	}
	exec.mu.Lock()
	runs := exec.runCalls
	exec.mu.Unlock()
	if runs != 0 {
		t.Errorf("executor ran %d crawls after Shutdown, want 0", runs)
	}
	jobs, _ := st.List()
	if len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != store.JobQueued {
		t.Errorf("claimed-then-stopped job = %+v, want back to queued (unclaimed)", jobs)
	}
}

// postClaimGateStore claims, then blocks before RETURNING the claimed job —
// the store row already says running, but the dispatcher hasn't registered the
// job in-flight yet: the exact claim→register gap.
type postClaimGateStore struct {
	*MemStore
	claimed chan string
	release chan struct{}
	armed   atomic.Bool
}

func (g *postClaimGateStore) ClaimNext() (*Job, error) {
	j, err := g.MemStore.ClaimNext()
	if j != nil && g.armed.Load() {
		g.claimed <- j.ID
		<-g.release
	}
	return j, err
}

// TestCancelInClaimGapPreventsRun (#74 N6): a Cancel landing while the job sits
// in the claim→register gap used to return (false, nil) — reporting nothing to
// cancel — while the job went on to run anyway.
func TestCancelInClaimGapPreventsRun(t *testing.T) {
	st := &postClaimGateStore{MemStore: NewMemStore(), claimed: make(chan string), release: make(chan struct{})}
	exec := newFakeExec()
	d := New(st, exec)
	job, _ := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	st.armed.Store(true)
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if id := <-st.claimed; id != job.ID {
		t.Fatalf("claimed %s, want %s", id, job.ID)
	}
	// The job's row says running; it is not in-flight yet.
	ok, err := d.Cancel(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("Cancel in the claim gap reported nothing to cancel (N6)")
	}
	st.armed.Store(false)
	close(st.release)

	// The job must reach a terminal cancelled state without the crawl running.
	deadline := time.After(3 * time.Second)
	for {
		jobs, _ := st.List()
		if jobs[0].Status == store.JobCanceled {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("job status = %q, want canceled", jobs[0].Status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	exec.mu.Lock()
	runs := exec.runCalls
	exec.mu.Unlock()
	if runs != 0 {
		t.Errorf("executor ran %d crawls for a cancelled job, want 0", runs)
	}
	d.Shutdown()
}

// TestClaimErrorRetriesWithBackoff (#74 N8): a transient ClaimNext error must
// not park the loop solely on wakeCh — with no further Enqueue to wake it, the
// queued job would strand forever. The loop retries on a timer.
func TestClaimErrorRetriesWithBackoff(t *testing.T) {
	old := claimRetryDelay
	claimRetryDelay = 20 * time.Millisecond
	defer func() { claimRetryDelay = old }()

	st := &flakyStore{MemStore: NewMemStore()}
	// Two failures: the first is absorbed by the buffered wake token the
	// Enqueue left, the second is what used to strand the queue.
	st.claimErrs.Store(2)
	exec := newFakeExec()
	d := New(st, exec)
	if _, err := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a"); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	waitStarted(t, exec) // hangs (fails) if the loop parked on wakeCh after the claim errors
	exec.complete()
	d.Shutdown()
}
