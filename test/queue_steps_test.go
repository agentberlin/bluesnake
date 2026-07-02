package acceptance

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/cucumber/godog"
)

func (w *world) registerQueueSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a fixture page "([^"]*)" and a fixture page "([^"]*)"$`, w.twoFixturePages)
	sc.Step(`^spider crawls of "([^"]*)" and "([^"]*)" are queued$`, w.queueTwoCrawls)
	sc.Step(`^spider crawls of "([^"]*)" and "([^"]*)" are queued on a parallel queue of (\d+)$`, w.queueTwoCrawlsParallel)
	sc.Step(`^the crawls ran concurrently$`, w.crawlsRanConcurrently)
	sc.Step(`^the queue is drained$`, w.drainQueue)
	sc.Step(`^both crawls complete in the registry$`, w.bothCrawlsComplete)
	sc.Step(`^the crawls ran one at a time in the order they were queued$`, w.crawlsRanInOrder)
	sc.Step(`^a job left running in the queue and a fresh job queued behind it$`, w.crashedAndFreshJob)
	sc.Step(`^the abandoned job is marked interrupted$`, w.abandonedInterrupted)
	sc.Step(`^only the fresh job ran$`, w.onlyFreshRan)
}

// queueObserver records what the dispatcher ran: the seeds in start order (to
// assert FIFO), the peak concurrent crawls (to assert no overlap), and signals
// done once `total` crawls finish.
type queueObserver struct {
	mu         sync.Mutex
	startSeeds []string
	active     int
	maxActive  int
	finished   int
	total      int
	done       chan struct{}
}

func (o *queueObserver) OnStart(crawlID, seed string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.startSeeds = append(o.startSeeds, seed)
	o.active++
	if o.active > o.maxActive {
		o.maxActive = o.active
	}
}

func (o *queueObserver) OnPage(string, *crawler.PageRecord) {}

func (o *queueObserver) OnDone(runner.Outcome) {
	o.mu.Lock()
	o.active--
	o.finished++
	done := o.finished == o.total
	o.mu.Unlock()
	if done {
		close(o.done)
	}
}

func (w *world) twoFixturePages(a, b string) error {
	for _, p := range []string{a, b} {
		r := w.route(p)
		r.status, r.body = 200, `<html><head><title>p</title></head><body>x</body></html>`
	}
	return nil
}

func (w *world) queueTwoCrawls(a, b string) error {
	srv := w.ensureServer()
	w.queueObs = &queueObserver{total: 2, done: make(chan struct{})}
	w.queueDisp = queue.New(queue.NewSQLiteStore(w.storeDirPath()), runner.New(w.storeDirPath(), w.queueObs))
	w.queueSeeds = nil
	for _, p := range []string{a, b} {
		seed := srv.URL + p
		w.queueSeeds = append(w.queueSeeds, seed)
		if _, err := w.queueDisp.Enqueue(queue.JobSpec{URL: seed, Config: map[string]any{"speed.max_threads": 1}}, "manual", "", seed); err != nil {
			return err
		}
	}
	return nil
}

// queueTwoCrawlsParallel queues two single-page crawls on a dispatcher running
// `parallel` drain loops with ONE shared limiter (the #78 wiring every parallel
// surface uses). Each page holds its response long enough that the two crawls
// reliably overlap when — and only when — the dispatcher really runs them
// concurrently.
func (w *world) queueTwoCrawlsParallel(a, b string, parallel int) error {
	srv := w.ensureServer()
	for _, p := range []string{a, b} {
		w.route(p).sleep = 400 * time.Millisecond
	}
	w.queueObs = &queueObserver{total: 2, done: make(chan struct{})}
	w.queueDisp = queue.New(queue.NewSQLiteStore(w.storeDirPath()),
		runner.New(w.storeDirPath(), w.queueObs, runner.WithLimiter(limiter.New(0, 1, 0))),
		queue.WithConcurrency(parallel))
	w.queueSeeds = nil
	for _, p := range []string{a, b} {
		seed := srv.URL + p
		w.queueSeeds = append(w.queueSeeds, seed)
		if _, err := w.queueDisp.Enqueue(queue.JobSpec{URL: seed, Config: map[string]any{"speed.max_threads": 1}}, "manual", "", seed); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) crawlsRanConcurrently() error {
	w.queueObs.mu.Lock()
	defer w.queueObs.mu.Unlock()
	if w.queueObs.maxActive != 2 {
		return fmt.Errorf("peak concurrent crawls = %d, want 2 (the parallel queue must overlap them)", w.queueObs.maxActive)
	}
	return nil
}

func (w *world) drainQueue() error {
	if err := w.queueDisp.Start(context.Background()); err != nil {
		return err
	}
	select {
	case <-w.queueObs.done:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("queue did not drain within 30s")
	}
	w.queueDisp.Shutdown()
	return nil
}

func (w *world) bothCrawlsComplete() error {
	infos, err := store.ListCrawls(w.storeDirPath())
	if err != nil {
		return err
	}
	completed := 0
	for _, in := range infos {
		if in.Status == store.StatusCompleted {
			completed++
		}
	}
	if completed != 2 {
		return fmt.Errorf("registry has %d completed crawls, want 2", completed)
	}
	return nil
}

func (w *world) crawlsRanInOrder() error {
	w.queueObs.mu.Lock()
	defer w.queueObs.mu.Unlock()
	if w.queueObs.maxActive != 1 {
		return fmt.Errorf("peak concurrent crawls = %d, want 1 (no overlap)", w.queueObs.maxActive)
	}
	if !reflect.DeepEqual(w.queueObs.startSeeds, w.queueSeeds) {
		return fmt.Errorf("crawls started in order %v, want %v", w.queueObs.startSeeds, w.queueSeeds)
	}
	return nil
}

func (w *world) crashedAndFreshJob() error {
	srv := w.ensureServer()
	r := w.route("/a")
	r.status, r.body = 200, `<html><head><title>fresh</title></head><body>x</body></html>`

	w.queueObs = &queueObserver{total: 1, done: make(chan struct{})}
	w.queueDisp = queue.New(queue.NewSQLiteStore(w.storeDirPath()), runner.New(w.storeDirPath(), w.queueObs))

	// A job that was running when the host died: enqueue then claim it (-> running)
	// without ever executing it, exactly the state a crash leaves behind.
	crashed, err := store.EnqueueJob(w.storeDirPath(), store.Job{Source: "manual", Label: "crashed", Request: `{"url":"` + srv.URL + `/gone"}`})
	if err != nil {
		return err
	}
	w.crashedJobID = crashed.ID
	if _, err := store.ClaimNextJob(w.storeDirPath()); err != nil {
		return err
	}
	// A fresh job queued behind it.
	if _, err := store.EnqueueJob(w.storeDirPath(), store.Job{Source: "manual", Label: "fresh", Request: `{"url":"` + srv.URL + `/a"}`}); err != nil {
		return err
	}
	return nil
}

func (w *world) abandonedInterrupted() error {
	jobs, err := store.ListJobs(w.storeDirPath())
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.ID == w.crashedJobID {
			if j.Status != store.JobInterrupted {
				return fmt.Errorf("abandoned job status = %q, want interrupted", j.Status)
			}
			return nil
		}
	}
	return fmt.Errorf("crashed job %s not found", w.crashedJobID)
}

func (w *world) onlyFreshRan() error {
	w.queueObs.mu.Lock()
	seeds := append([]string(nil), w.queueObs.startSeeds...)
	w.queueObs.mu.Unlock()
	want := []string{w.server.URL + "/a"}
	if !reflect.DeepEqual(seeds, want) {
		return fmt.Errorf("crawls that ran = %v, want only the fresh job %v", seeds, want)
	}
	return nil
}
