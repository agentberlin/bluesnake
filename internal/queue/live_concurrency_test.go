package queue

// Live W (SetConcurrency): the dispatcher's parallel-crawl width retargets at
// runtime with no restart. Raising must start already-queued jobs immediately;
// lowering must never interrupt a running crawl and must converge to the new
// width as jobs finish; the wake-passing on retirement must not strand a job
// enqueued while loops retire.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// waitActive polls until exactly n crawls are in flight, failing after 2s —
// used where the assertion is a level, not a start event.
func waitActive(t *testing.T, exec *fakeExec, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exec.mu.Lock()
		active := exec.active
		exec.mu.Unlock()
		if active == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	t.Fatalf("active crawls = %d, want %d", exec.active, n)
}

// TestSetConcurrencyRaiseStartsQueuedJobs: with W=1 and three jobs queued, the
// second and third wait; raising W to 3 must start them immediately — the
// whole point of a live knob (no restart, no waiting for the head to finish).
func TestSetConcurrencyRaiseStartsQueuedJobs(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec) // W=1
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

	d.SetConcurrency(3)
	waitStarted(t, exec)
	waitStarted(t, exec)
	waitActive(t, exec, 3) // raise took effect live: all three overlap

	for i := 0; i < 3; i++ {
		exec.complete()
	}
	d.Shutdown()
	if exec.maxActive != 3 {
		t.Errorf("peak concurrent crawls = %d, want 3", exec.maxActive)
	}
}

// TestSetConcurrencyLowerDrainsWithoutKillingRunning: lowering W from 3 to 1
// with three crawls in flight must not pause/stop any of them; as they finish,
// the width converges to 1 and stays there for the remaining queue.
func TestSetConcurrencyLowerDrainsWithoutKillingRunning(t *testing.T) {
	const jobs = 6
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(3))
	for i := 0; i < jobs; i++ {
		u := fmt.Sprintf("u%d", i)
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		waitStarted(t, exec)
	}

	d.SetConcurrency(1)
	exec.mu.Lock()
	pauses, stops := exec.pauses, exec.stops
	exec.mu.Unlock()
	if pauses != 0 || stops != 0 {
		t.Fatalf("lowering W signalled crawls (pauses=%d stops=%d), want none — shrink must never interrupt a running crawl", pauses, stops)
	}
	waitActive(t, exec, 3) // the three in-flight crawls keep running

	// Finish the three; only ONE loop survives, so exactly one new job starts
	// at a time from here on.
	exec.mu.Lock()
	exec.maxActive = 0 // reset the peak: measure only the post-shrink phase
	exec.mu.Unlock()
	for i := 0; i < 3; i++ {
		exec.complete()
	}
	for i := 3; i < jobs; i++ {
		waitStarted(t, exec)
		waitActive(t, exec, 1)
		exec.complete()
	}
	d.Shutdown()

	exec.mu.Lock()
	defer exec.mu.Unlock()
	if exec.maxActive != 1 {
		t.Errorf("peak concurrency after shrink = %d, want 1", exec.maxActive)
	}
	if exec.runCalls != jobs {
		t.Errorf("Run called %d times, want %d (every job ran exactly once)", exec.runCalls, jobs)
	}
}

// TestSetConcurrencyRetireDoesNotStrandEnqueue: with W=3 idle (all loops parked)
// lowered to 1, two loops retire off the wake channel. A job enqueued right
// after must still start — the retiring loops must chain the wake to the
// survivor rather than consuming it and exiting.
func TestSetConcurrencyRetireDoesNotStrandEnqueue(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(3))
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let all three loops park on wakeCh
	d.SetConcurrency(1)
	time.Sleep(50 * time.Millisecond) // let retirement churn settle

	if _, err := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a"); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec) // would hang if a retiring loop swallowed the wake
	exec.complete()
	d.Shutdown()
}

// TestSetConcurrencyLowerThenRaise: shrink to 1 then re-raise to 2 while jobs
// remain queued — the dispatcher must spawn back up and run two at once again.
// Pins that liveLoops bookkeeping survives a full down-up cycle.
func TestSetConcurrencyLowerThenRaise(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(2))
	for _, u := range []string{"a", "b", "c", "d"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec)
	waitStarted(t, exec)

	d.SetConcurrency(1)
	exec.complete()
	exec.complete() // both finish; one loop retires, the survivor claims c
	waitStarted(t, exec)
	waitActive(t, exec, 1)

	d.SetConcurrency(2) // d starts on the newly spawned loop
	waitStarted(t, exec)
	waitActive(t, exec, 2)

	exec.complete()
	exec.complete()
	d.Shutdown()

	list, _ := d.List()
	for _, j := range list {
		if j.Status != store.JobDone {
			t.Errorf("job %s = %q, want done", j.Label, j.Status)
		}
	}
}

// TestSetConcurrencyBeforeStart: before Start it replaces the WithConcurrency
// value (negatives clamp to 0 = unlimited), and Start spawns that many loops.
func TestSetConcurrencyBeforeStart(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	d.SetConcurrency(-1) // clamps to 0 = unlimited
	if got := d.Concurrency(); got != 0 {
		t.Fatalf("Concurrency after SetConcurrency(-1) = %d, want 0 (unlimited)", got)
	}
	d.SetConcurrency(2)
	for _, u := range []string{"a", "b"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, exec)
	waitStarted(t, exec)
	waitActive(t, exec, 2)
	exec.complete()
	exec.complete()
	d.Shutdown()
}
