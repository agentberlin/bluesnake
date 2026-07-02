package queue

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// fakeExec is a test executor. Run announces its start on started and blocks
// until the test pushes a result on release, so a test fully controls timing and
// can assert that crawls never overlap. Pause/Stop only count calls — the test
// drives completion via release (modelling "signal, crawl drains, Run returns").
type fakeExec struct {
	started chan string
	release chan result

	mu                      sync.Mutex
	active, maxActive       int
	pauses, stops, runCalls int
	pausedIDs, stoppedIDs   []string // crawl ids the addressed signals named
}

type result struct {
	status string
	err    error
}

func newFakeExec() *fakeExec {
	return &fakeExec{started: make(chan string), release: make(chan result)}
}

func (f *fakeExec) Run(ctx context.Context, spec JobSpec, onStart func(string)) (string, error) {
	f.mu.Lock()
	f.runCalls++
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()

	onStart("crawl-" + spec.URL)
	f.started <- spec.URL
	msg := <-f.release

	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return msg.status, msg.err
}

func (f *fakeExec) Pause() { f.mu.Lock(); f.pauses++; f.mu.Unlock() }
func (f *fakeExec) Stop()  { f.mu.Lock(); f.stops++; f.mu.Unlock() }
func (f *fakeExec) PauseCrawl(id string) {
	f.mu.Lock()
	f.pauses++
	f.pausedIDs = append(f.pausedIDs, id)
	f.mu.Unlock()
}
func (f *fakeExec) StopCrawl(id string) {
	f.mu.Lock()
	f.stops++
	f.stoppedIDs = append(f.stoppedIDs, id)
	f.mu.Unlock()
}

func (f *fakeExec) complete() { f.release <- result{status: store.StatusCompleted} }

func waitStarted(t *testing.T, f *fakeExec) string {
	t.Helper()
	select {
	case u := <-f.started:
		return u
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a job to start")
		return ""
	}
}

func TestDispatcherSequentialOrder(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	for _, u := range []string{"a", "b", "c"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var order []string
	for i := 0; i < 3; i++ {
		order = append(order, waitStarted(t, exec))
		exec.mu.Lock()
		active := exec.active
		exec.mu.Unlock()
		if active != 1 {
			t.Fatalf("after start %d, active crawls = %d, want exactly 1", i, active)
		}
		exec.complete()
	}
	d.Shutdown()

	if !reflect.DeepEqual(order, []string{"a", "b", "c"}) {
		t.Errorf("run order = %v, want FIFO [a b c]", order)
	}
	if exec.maxActive != 1 {
		t.Errorf("peak concurrent crawls = %d, want 1 (no parallelism)", exec.maxActive)
	}
	jobs, _ := d.List()
	for _, j := range jobs {
		if j.Status != store.JobDone || j.CrawlID == "" {
			t.Errorf("job %s finished as %q crawl=%q, want done with a crawl id", j.Label, j.Status, j.CrawlID)
		}
	}
}

// TestDispatcherParallelRunsConcurrently (GL-05/GL-17) pins parallel dispatch:
// with concurrency 2 the two queued jobs run at the SAME time — each claimed
// exactly once by the W loops (no double-claim), the burst spread across loops.
func TestDispatcherParallelRunsConcurrently(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(2))
	for _, u := range []string{"a", "b"} {
		if _, err := d.Enqueue(JobSpec{URL: u}, "manual", "", u); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Both must start before either completes — they run concurrently.
	waitStarted(t, exec)
	waitStarted(t, exec)
	exec.mu.Lock()
	active := exec.active
	exec.mu.Unlock()
	if active != 2 {
		t.Fatalf("concurrent crawls = %d, want 2 (parallel dispatch)", active)
	}
	exec.complete()
	exec.complete()
	d.Shutdown()

	if exec.maxActive != 2 {
		t.Errorf("peak concurrent crawls = %d, want 2", exec.maxActive)
	}
	if exec.runCalls != 2 {
		t.Errorf("Run called %d times, want 2 (each job claimed exactly once — no double-claim)", exec.runCalls)
	}
	jobs, _ := d.List()
	for _, j := range jobs {
		if j.Status != store.JobDone {
			t.Errorf("job %s = %q, want done", j.Label, j.Status)
		}
	}
}

// TestDispatcherShutdownWaitsAllLoops (GL-13) pins that Shutdown turns ALL
// in-flight crawls around (one exec.Pause fan-out) and waits for every drain
// loop — not just one — so no crawl is abandoned and Shutdown never returns early.
func TestDispatcherShutdownWaitsAllLoops(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec, WithConcurrency(3))
	for _, u := range []string{"a", "b", "c"} {
		d.Enqueue(JobSpec{URL: u}, "manual", "", u)
	}
	d.Start(context.Background())
	for i := 0; i < 3; i++ {
		waitStarted(t, exec)
	}

	done := make(chan struct{})
	go func() { d.Shutdown(); close(done) }()
	// Shutdown signalled Pause and is blocked on every loop; let the crawls drain.
	exec.complete()
	exec.complete()
	exec.complete()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not return — a drain loop was left running (M-1 crawls abandoned)")
	}
	exec.mu.Lock()
	pauses := exec.pauses
	exec.mu.Unlock()
	if pauses < 1 {
		t.Errorf("Shutdown forwarded %d pauses, want >= 1 (turn in-flight crawls around)", pauses)
	}
}

func TestDispatcherEnqueueWhileRunning(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	if _, err := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a"); err != nil {
		t.Fatal(err)
	}
	d.Start(context.Background())
	waitStarted(t, exec) // a is now running

	// enqueue b while a runs; it must wait, not start a second crawl
	d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")
	jobs, _ := d.List()
	var bStatus string
	for _, j := range jobs {
		if j.Label == "b" {
			bStatus = j.Status
		}
	}
	if bStatus != store.JobQueued {
		t.Fatalf("b status while a runs = %q, want queued", bStatus)
	}
	cur := d.Current()
	if cur == nil || cur.Label != "a" {
		t.Fatalf("Current() = %+v, want the running job a", cur)
	}

	exec.complete() // a finishes
	if waitStarted(t, exec) != "b" {
		t.Fatal("b did not run after a finished")
	}
	exec.complete()
	d.Shutdown()
}

// TestDispatcherCrashRecovery pins that a job left running by a crash is
// reconciled to interrupted on Start and NOT re-run, while a still-queued job
// runs normally. (Matches the chosen recovery: mark interrupted, leave resumable.)
func TestDispatcherCrashRecovery(t *testing.T) {
	st := NewMemStore()
	st.Enqueue(JobSpec{URL: "crashed"}, "manual", "", "crashed")
	if _, err := st.ClaimNext(); err != nil { // simulate: it was running when the host died
		t.Fatal(err)
	}
	st.Enqueue(JobSpec{URL: "queued"}, "manual", "", "queued")

	exec := newFakeExec()
	d := New(st, exec)
	d.Start(context.Background())

	if u := waitStarted(t, exec); u != "queued" {
		t.Fatalf("dispatcher ran %q; the crashed job must not be re-run", u)
	}
	exec.complete()
	d.Shutdown()

	jobs, _ := st.List()
	got := map[string]string{}
	for _, j := range jobs {
		got[j.Label] = j.Status
	}
	if got["crashed"] != store.JobInterrupted {
		t.Errorf("crashed job status = %q, want interrupted", got["crashed"])
	}
	if got["queued"] != store.JobDone {
		t.Errorf("queued job status = %q, want done", got["queued"])
	}
	if exec.runCalls != 1 {
		t.Errorf("executor ran %d times, want 1 (only the queued job)", exec.runCalls)
	}
}

func TestDispatcherExecutorErrorFailsJob(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	d.Enqueue(JobSpec{URL: "boom"}, "manual", "", "boom")
	d.Start(context.Background())
	waitStarted(t, exec)
	exec.release <- result{err: errBoom}
	d.Shutdown()

	jobs, _ := d.List()
	if jobs[0].Status != store.JobFailed || jobs[0].Error == "" {
		t.Fatalf("errored job = %q err=%q, want failed with a message", jobs[0].Status, jobs[0].Error)
	}
}

func TestDispatcherPauseStopForwardAndMap(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	d.Enqueue(JobSpec{URL: "p"}, "manual", "", "p")
	d.Start(context.Background())
	waitStarted(t, exec)

	d.Pause()
	d.Stop()
	exec.mu.Lock()
	pauses, stops := exec.pauses, exec.stops
	exec.mu.Unlock()
	if pauses != 1 || stops != 1 {
		t.Fatalf("forwarding: pauses=%d stops=%d, want 1/1", pauses, stops)
	}
	// crawl drains and reports interrupted (a pause) -> job interrupted
	exec.release <- result{status: store.StatusInterrupted}
	d.Shutdown()
	jobs, _ := d.List()
	if jobs[0].Status != store.JobInterrupted {
		t.Fatalf("paused job status = %q, want interrupted (resumable)", jobs[0].Status)
	}
}

func TestDispatcherCancelQueuedAndRunning(t *testing.T) {
	exec := newFakeExec()
	d := New(NewMemStore(), exec)
	a, _ := d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	b, _ := d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")
	d.Start(context.Background())
	waitStarted(t, exec) // a runs

	// cancel the still-queued b -> dropped, never runs
	if ok, _ := d.Cancel(b.ID); !ok {
		t.Fatal("cancel of queued job reported no change")
	}
	// cancel the running a -> stop forwarded
	if ok, _ := d.Cancel(a.ID); !ok {
		t.Fatal("cancel of running job reported no change")
	}
	exec.mu.Lock()
	stops := exec.stops
	exec.mu.Unlock()
	if stops != 1 {
		t.Fatalf("cancel of running job forwarded %d stops, want 1", stops)
	}

	exec.complete() // a drains
	d.Shutdown()

	jobs, _ := d.List()
	got := map[string]string{}
	for _, j := range jobs {
		got[j.Label] = j.Status
	}
	if got["b"] != store.JobCanceled {
		t.Errorf("queued-then-cancelled job = %q, want canceled", got["b"])
	}
	if exec.runCalls != 1 {
		t.Errorf("executor ran %d times, want 1 (b was cancelled before running)", exec.runCalls)
	}
}

func TestDispatcherWithSQLiteStore(t *testing.T) {
	exec := newFakeExec()
	d := New(NewSQLiteStore(t.TempDir()), exec)
	d.Enqueue(JobSpec{URL: "a"}, "manual", "", "a")
	d.Enqueue(JobSpec{URL: "b"}, "manual", "", "b")
	d.Start(context.Background())

	for i := 0; i < 2; i++ {
		waitStarted(t, exec)
		exec.complete()
	}
	d.Shutdown()

	jobs, _ := d.List()
	if len(jobs) != 2 {
		t.Fatalf("persistent queue has %d jobs, want 2", len(jobs))
	}
	for _, j := range jobs {
		if j.Status != store.JobDone {
			t.Errorf("job %s = %q, want done", j.Label, j.Status)
		}
	}
}

func TestJobStatusMapping(t *testing.T) {
	cases := []struct {
		crawlStatus string
		err         error
		want        string
	}{
		{store.StatusCompleted, nil, store.JobDone},
		{store.StatusInterrupted, nil, store.JobInterrupted},
		{store.StatusCompleted, errBoom, store.JobFailed},
		{"", errBoom, store.JobFailed},
	}
	for _, c := range cases {
		if got := jobStatusFor(c.crawlStatus, c.err); got != c.want {
			t.Errorf("jobStatusFor(%q, %v) = %q, want %q", c.crawlStatus, c.err, got, c.want)
		}
	}
}

var errBoom = &boomErr{}

type boomErr struct{}

func (*boomErr) Error() string { return "boom" }
