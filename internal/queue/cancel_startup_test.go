package queue

// P18 (issue #73): Cancel must handle the ms-wide startup window — after a job is
// registered in-flight but before its crawlID is known. Calling StopCrawl("")
// there silently no-ops while falsely reporting success; the cancel must instead
// be latched and honored the instant the crawl id lands.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// gateExec holds a crawl in the startup window: Run announces it has started
// (crawlID not yet known) and blocks until proceed, so the test can Cancel before
// onStart fires. StopCrawl calls are recorded on stopped.
type gateExec struct {
	running chan string
	proceed chan struct{}
	stopped chan string
}

func (g *gateExec) Run(ctx context.Context, spec JobSpec, onStart func(string)) (string, error) {
	g.running <- spec.URL // in-flight, crawlID still empty
	<-g.proceed
	onStart("crawl-" + spec.URL)
	return store.StatusCompleted, nil
}
func (g *gateExec) Pause()              {}
func (g *gateExec) Stop()               {}
func (g *gateExec) StopCrawl(id string) { g.stopped <- id }

func TestCancel_StartupWindow_NoStopWithEmptyID(t *testing.T) {
	g := &gateExec{running: make(chan string, 1), proceed: make(chan struct{}), stopped: make(chan string, 4)}
	var once sync.Once
	release := func() { once.Do(func() { close(g.proceed) }) }
	d := New(NewMemStore(), g)
	// Defer ordering (LIFO): release the gated executor BEFORE Shutdown waits on it,
	// so an early t.Fatal can't deadlock Shutdown's wg.Wait() on a still-blocked Run.
	defer d.Shutdown()
	defer release()

	job, err := d.Enqueue(JobSpec{URL: "x"}, "manual", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Wait until the job is in-flight but the crawl hasn't started (crawlID empty).
	<-g.running

	// Cancel in the startup window: it must report success without calling
	// StopCrawl("") (which would no-op while claiming the crawl was stopped).
	ok, err := d.Cancel(job.ID)
	if err != nil || !ok {
		t.Fatalf("Cancel in startup window = (%v, %v), want (true, nil)", ok, err)
	}
	select {
	case id := <-g.stopped:
		t.Fatalf("StopCrawl(%q) called during the startup window — must wait for the real crawl id", id)
	case <-time.After(50 * time.Millisecond):
	}

	// Let the crawl start: the latched cancel must now stop it by its real id.
	release()
	select {
	case id := <-g.stopped:
		if id == "" {
			t.Error("StopCrawl called with an empty id after onStart")
		}
	case <-time.After(2 * time.Second):
		t.Error("a Cancel from the startup window was never honored once the crawl id was known")
	}
}
