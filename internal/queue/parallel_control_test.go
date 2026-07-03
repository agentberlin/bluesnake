package queue

// Parallel multi-crawl control at the dispatcher level (issue #78, the
// MEMORY-SCALING §13.5 GL rows): burst throughput across W loops (GL-17), the
// max_concurrent_crawls bound (GL-18), registry-store integrity under
// concurrent job ops (GL-09), and MemStore↔SQLiteStore behaviour parity for
// the parallel-control surface (GL-22). Plus the new addressed API itself:
// CurrentAll, PauseCrawl/StopCrawl passthroughs, and the AwaitCrawl handshake.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// TestWLoopsDrainConcurrentlyAfterBurstEnqueue (GL-17): W idle loops are all
// parked on the cap-1 wake channel when a burst of W jobs lands. The
// wake-the-next chain (a claiming loop re-arms the wake before running its job)
// must fan the burst across every loop — peak concurrency W, not a serial drain.
func TestWLoopsDrainConcurrentlyAfterBurstEnqueue(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(3))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Let the three loops find an empty queue and park on wakeCh.
	time.Sleep(50 * time.Millisecond)

	for _, u := range []string{"a", "b", "c"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		waitStarted(t, exec)
	}
	exec.mu.Lock()
	active := exec.active
	exec.mu.Unlock()
	if active != 3 {
		t.Fatalf("concurrent crawls after burst enqueue = %d, want 3 (throughput collapsed to serial)", active)
	}
	for i := 0; i < 3; i++ {
		exec.complete()
	}
	d.Shutdown()
}

// TestMaxConcurrentCrawlsBounded (GL-18): a queue far deeper than the cap never
// has more than `concurrency` crawls in flight — the knob that bounds per-crawl
// fixed overhead (DB handles, buffers, Bloom) on the M axis.
func TestMaxConcurrentCrawlsBounded(t *testing.T) {
	const jobs, cap = 20, 4
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(cap))
	for i := 0; i < jobs; i++ {
		if _, err := d.Enqueue(JobSpec{URL: fmt.Sprintf("u%d", i)}, "manual", "", fmt.Sprintf("u%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap; i++ {
		waitStarted(t, exec)
	}
	// Slots are full; drain one job at a time and confirm the freed slot is
	// refilled while the bound holds.
	for i := cap; i < jobs; i++ {
		exec.complete()
		waitStarted(t, exec)
	}
	for i := 0; i < cap; i++ {
		exec.complete()
	}
	d.Shutdown()

	if exec.maxActive != cap {
		t.Errorf("peak concurrent crawls = %d, want exactly %d (bound held and saturated)", exec.maxActive, cap)
	}
	if exec.runCalls != jobs {
		t.Errorf("Run called %d times, want %d (every job claimed exactly once)", exec.runCalls, jobs)
	}
	list, _ := d.List()
	for _, j := range list {
		if j.Status != store.JobDone {
			t.Errorf("job %s = %q, want done", j.Label, j.Status)
		}
	}
}

// TestDispatcherCurrentAllAndAddressedControl pins the parallel-addressing API:
// CurrentAll lists every in-flight job oldest-first with its crawl id, and
// PauseCrawl/StopCrawl pass the id through to the executor without touching
// the sibling crawl.
func TestDispatcherCurrentAllAndAddressedControl(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(2))
	d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")
	d.Start(context.Background())
	waitStarted(t, exec)
	waitStarted(t, exec)

	cur := d.CurrentAll()
	if len(cur) != 2 {
		t.Fatalf("CurrentAll returned %d jobs, want 2", len(cur))
	}
	if cur[0].Label != "a" || cur[1].Label != "b" {
		t.Errorf("CurrentAll order = [%s %s], want oldest-first [a b]", cur[0].Label, cur[1].Label)
	}
	for _, j := range cur {
		if j.CrawlID != "crawl-"+j.Label {
			t.Errorf("job %s crawl id = %q, want %q", j.Label, j.CrawlID, "crawl-"+j.Label)
		}
	}

	d.PauseCrawl("crawl-a")
	d.StopCrawl("crawl-b")
	exec.mu.Lock()
	paused, stopped := exec.pausedIDs, exec.stoppedIDs
	exec.mu.Unlock()
	if len(paused) != 1 || paused[0] != "crawl-a" {
		t.Errorf("PauseCrawl forwarded %v, want [crawl-a]", paused)
	}
	if len(stopped) != 1 || stopped[0] != "crawl-b" {
		t.Errorf("StopCrawl forwarded %v, want [crawl-b]", stopped)
	}

	exec.complete()
	exec.complete()
	d.Shutdown()
	if got := d.CurrentAll(); len(got) != 0 {
		t.Errorf("CurrentAll after drain = %d jobs, want none", len(got))
	}
}

// TestAwaitCrawlPerJobHandshake pins the blocking-start handshake with TWO
// starts in flight: each AwaitCrawl(jobID) resolves to ITS job's crawl id —
// the association a single shared "started" channel cannot make (#78).
func TestAwaitCrawlPerJobHandshake(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(2))
	d.Start(context.Background())
	ja, _ := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	jb, _ := d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")

	type res struct {
		id  string
		err error
	}
	got := make(chan res, 2)
	await := func(jobID string) {
		id, err := d.AwaitCrawl(context.Background(), jobID)
		got <- res{id, err}
	}
	go await(ja.ID)
	go await(jb.ID)

	waitStarted(t, exec)
	waitStarted(t, exec)
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-got:
			if r.err != nil {
				t.Fatalf("AwaitCrawl error: %v", r.err)
			}
			ids[r.id] = true
		case <-time.After(5 * time.Second):
			t.Fatal("AwaitCrawl did not resolve while both crawls run")
		}
	}
	if !ids["crawl-a"] || !ids["crawl-b"] {
		t.Errorf("AwaitCrawl resolved %v, want both crawl-a and crawl-b (each caller its own id)", ids)
	}
	exec.complete()
	exec.complete()
	d.Shutdown()
}

// TestAwaitCrawlFailedStart pins that a job failing before its crawl exists
// resolves the await with the job's error, not a hang.
func TestAwaitCrawlFailedStart(t *testing.T) {
	exec := &noStartExec{}
	d := New(NewMemStore(), exec)
	d.Start(context.Background())
	j, _ := d.Enqueue(JobSpec{URL: "boom"}, "manual", "", "boom")
	if _, err := d.AwaitCrawl(context.Background(), j.ID); err == nil || err.Error() != "no seed" {
		t.Fatalf("AwaitCrawl on a failed start = %v, want the job's error", err)
	}
	d.Shutdown()
}

// noStartExec fails every Run before a crawl id exists.
type noStartExec struct{}

func (*noStartExec) Run(context.Context, JobSpec, func(string)) (string, error) {
	return "", fmt.Errorf("no seed")
}
func (*noStartExec) Pause()            {}
func (*noStartExec) Stop()             {}
func (*noStartExec) PauseCrawl(string) {}
func (*noStartExec) StopCrawl(string)  {}

// TestRegistryDBNoLockErrorUnderConcurrentJobOps (GL-09): the SQLite-backed job
// store under W concurrent workers doing the full job lifecycle must surface
// ZERO lock/busy errors, and every job must end terminal exactly once.
func TestRegistryDBNoLockErrorUnderConcurrentJobOps(t *testing.T) {
	s := NewSQLiteStore(t.TempDir())
	const workers, perWorker = 4, 15

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker*4)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := s.Enqueue(JobSpec{URL: fmt.Sprintf("w%d-%d", w, i)}, "manual", "", "j"); err != nil {
					errs <- fmt.Errorf("enqueue: %w", err)
					continue
				}
				j, err := s.ClaimNext()
				if err != nil {
					errs <- fmt.Errorf("claim: %w", err)
					continue
				}
				if j == nil {
					continue // another worker claimed it; its lifecycle finishes there
				}
				if err := s.SetCrawlID(j.ID, "crawl-"+j.ID); err != nil {
					errs <- fmt.Errorf("set crawl id: %w", err)
				}
				if _, err := s.List(); err != nil {
					errs <- fmt.Errorf("list: %w", err)
				}
				if err := s.Finish(j.ID, store.JobDone, ""); err != nil {
					errs <- fmt.Errorf("finish: %w", err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent job op error (SQLITE_BUSY class must not surface): %v", err)
	}

	jobs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != workers*perWorker {
		t.Fatalf("registry holds %d jobs, want %d", len(jobs), workers*perWorker)
	}
	done := 0
	for _, j := range jobs {
		switch j.Status {
		case store.JobDone:
			done++
		case store.JobQueued, store.JobRunning:
			t.Errorf("job %s left %q, want every job terminal", j.ID, j.Status)
		}
	}
	if done != workers*perWorker {
		t.Errorf("%d jobs done, want %d (each finished exactly once)", done, workers*perWorker)
	}
}

// TestParallelControlParityAcrossStores (GL-22): the parallel-control behaviours
// — burst claim-once, addressed pause of a single crawl, shutdown-pauses-all —
// are identical over MemStore and SQLiteStore, so a bug can't hide in one
// backend.
func TestParallelControlParityAcrossStores(t *testing.T) {
	stores := map[string]func(t *testing.T) Store{
		"MemStore":    func(t *testing.T) Store { return NewMemStore() },
		"SQLiteStore": func(t *testing.T) Store { return NewSQLiteStore(t.TempDir()) },
	}
	for name, mk := range stores {
		t.Run(name+"/claim-once", func(t *testing.T) {
			exec := newFakeExec()
			d := New(mk(t), exec, WithConcurrency(2))
			for _, u := range []string{"a", "b", "c", "d"} {
				d.Enqueue(JobSpec{URL: u}, "manual", "", u)
			}
			d.Start(context.Background())
			for i := 0; i < 4; i++ {
				waitStarted(t, exec)
				exec.complete()
			}
			d.Shutdown()
			if exec.runCalls != 4 {
				t.Errorf("Run called %d times, want 4 (no double-claim)", exec.runCalls)
			}
			jobs, _ := d.List()
			for _, j := range jobs {
				if j.Status != store.JobDone {
					t.Errorf("job %s = %q, want done exactly once", j.Label, j.Status)
				}
			}
		})
		t.Run(name+"/addressed-pause", func(t *testing.T) {
			exec := newFakeExec()
			d := New(mk(t), exec, WithConcurrency(2))
			d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
			d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")
			d.Start(context.Background())
			waitStarted(t, exec)
			waitStarted(t, exec)
			d.PauseCrawl("crawl-a")
			exec.mu.Lock()
			paused := append([]string(nil), exec.pausedIDs...)
			exec.mu.Unlock()
			if len(paused) != 1 || paused[0] != "crawl-a" {
				t.Errorf("addressed pause forwarded %v, want [crawl-a] only", paused)
			}
			exec.complete()
			exec.complete()
			d.Shutdown()
		})
		t.Run(name+"/shutdown-all", func(t *testing.T) {
			exec := newFakeExec()
			d := New(mk(t), exec, WithConcurrency(3))
			for _, u := range []string{"a", "b", "c"} {
				d.Enqueue(JobSpec{URL: u}, "manual", "", u)
			}
			d.Start(context.Background())
			for i := 0; i < 3; i++ {
				waitStarted(t, exec)
			}
			done := make(chan struct{})
			go func() { d.Shutdown(); close(done) }()
			for i := 0; i < 3; i++ {
				exec.release <- result{status: store.StatusInterrupted}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Shutdown did not wait for every drain loop")
			}
			jobs, _ := d.List()
			for _, j := range jobs {
				if j.Status != store.JobInterrupted {
					t.Errorf("job %s = %q, want interrupted (paused, resumable)", j.Label, j.Status)
				}
			}
		})
	}
}
