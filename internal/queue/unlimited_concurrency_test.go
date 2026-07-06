package queue

// Unlimited width (concurrency 0, the speed.max_concurrent_crawls default):
// the dispatcher must run EVERY queued job at once — no job ever waits behind
// a running crawl. The pool grows a drain loop per claimed job (spawn-on-claim)
// and converges back to a single parked drainer when the queue empties, so
// unlimited is a growth mode, not a giant fixed pool. 0 matches the limiter's
// "<= 0 = unlimited" convention (speed.max_global_threads et al).

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// waitLoops polls until the dispatcher's live loop count reaches n, failing
// after 2s — used to observe the unlimited pool converging after a drain.
func waitLoops(t *testing.T, d *Dispatcher, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		live := d.liveLoops
		d.mu.Unlock()
		if live == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	t.Fatalf("live loops = %d, want %d", d.liveLoops, n)
}

// TestUnlimitedRunsWholeBacklogAtOnce: with W=0 a backlog queued before Start
// runs entirely in parallel — the single starter loop's claim cascade spawns a
// loop per job, so no job waits behind another.
func TestUnlimitedRunsWholeBacklogAtOnce(t *testing.T) {
	const n = 5
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(0))
	for i := 0; i < n; i++ {
		if _, err := d.Enqueue(JobSpec{URL: fmt.Sprintf("u%d", i)}, "manual", "", "u"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		waitStarted(t, exec)
	}
	waitActive(t, exec, n) // all five overlap — nothing queued behind a crawl
	for i := 0; i < n; i++ {
		exec.complete()
	}
	d.Shutdown()
	if exec.maxActive != n {
		t.Errorf("peak concurrent crawls = %d, want %d (unlimited)", exec.maxActive, n)
	}
}

// TestUnlimitedStartsLateJobsImmediately: with W=0 and crawls already running,
// a newly enqueued job must start at once — the parked spare drainer claims it
// and spawns its own replacement, keeping the always-a-free-drainer invariant.
func TestUnlimitedStartsLateJobsImmediately(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(0))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Enqueue(JobSpec{URL: fmt.Sprintf("u%d", i)}, "manual", "", "u"); err != nil {
			t.Fatal(err)
		}
		waitStarted(t, exec)
		waitActive(t, exec, i+1) // each start while the others still run
	}
	for i := 0; i < 3; i++ {
		exec.complete()
	}
	d.Shutdown()
}

// TestUnlimitedPoolConverges: after an unlimited burst drains, the grown loop
// pool must shrink back to ONE parked drainer — spawn-on-claim without the
// reverse edge would leak a goroutine per job ever run.
func TestUnlimitedPoolConverges(t *testing.T) {
	const n = 4
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(0))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := d.Enqueue(JobSpec{URL: fmt.Sprintf("u%d", i)}, "manual", "", "u"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		waitStarted(t, exec)
	}
	waitActive(t, exec, n)
	for i := 0; i < n; i++ {
		exec.complete()
	}
	waitActive(t, exec, 0)
	waitLoops(t, d, 1) // grown pool converged to the single parked drainer
	d.Shutdown()
}

// TestSetConcurrencyBoundedToUnlimited: flipping a saturated bounded queue to
// W=0 must start every waiting job immediately — the live-knob contract
// ("raising starts queued jobs, no restart") extended to unlimited.
func TestSetConcurrencyBoundedToUnlimited(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(1))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"a", "b", "c"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	waitStarted(t, exec)
	waitActive(t, exec, 1) // b and c wait behind a under W=1

	d.SetConcurrency(0) // unlimited: b and c must start now
	waitStarted(t, exec)
	waitStarted(t, exec)
	waitActive(t, exec, 3)

	for i := 0; i < 3; i++ {
		exec.complete()
	}
	d.Shutdown()
	if exec.maxActive != 3 {
		t.Errorf("peak concurrent crawls = %d, want 3", exec.maxActive)
	}
}

// TestSetConcurrencyUnlimitedToBounded: flipping W=0 with three crawls live
// down to 1 must not interrupt any of them; as they finish the pool converges
// and the remaining queue drains one at a time.
func TestSetConcurrencyUnlimitedToBounded(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(0))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"a", "b", "c", "d"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		waitStarted(t, exec)
	}
	waitActive(t, exec, 4)

	d.SetConcurrency(1)
	time.Sleep(50 * time.Millisecond) // the lower target must not touch live crawls
	waitActive(t, exec, 4)
	exec.mu.Lock()
	pauses, stops := exec.pauses, exec.stops
	exec.mu.Unlock()
	if pauses != 0 || stops != 0 {
		t.Fatalf("lowering to 1 signalled crawls (pauses=%d stops=%d), want none", pauses, stops)
	}

	for i := 0; i < 4; i++ {
		exec.complete()
	}
	waitActive(t, exec, 0)

	// e runs alone under the new bound.
	if _, err := d.Enqueue(JobSpec{URL: "e"}, "manual", "", "e"); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec)
	waitActive(t, exec, 1)
	exec.complete()
	d.Shutdown()
}

// TestUnlimitedRepeatedRetargetDoesNotLeakLoops: the desktop re-applies the
// knob on EVERY profile save; hammering SetConcurrency(0) while idle must not
// accumulate parked drainers (each save spawning one would leak goroutines).
func TestUnlimitedRepeatedRetargetDoesNotLeakLoops(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(0))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		d.SetConcurrency(0)
	}
	waitLoops(t, d, 1) // idle spares retire; one parked drainer remains

	// The surviving drainer still works.
	if _, err := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a"); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec)
	exec.complete()
	d.Shutdown()
}
