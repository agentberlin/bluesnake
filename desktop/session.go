package main

import (
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// uiObserver turns queue-driven crawls' lifecycles into the desktop's realtime
// Wails events. The engine already streams every page through the runner's
// sink; this observer aggregates the UI-specific bits the core counters don't
// carry — a feed of notable URLs — and runs a throttled "crawl:progress"
// emitter (~4/s) per live crawl reading the executor's per-crawl snapshot.
// With several crawls running in parallel, every event carries its crawl id
// and the per-crawl state is keyed by it, so two crawls' progress streams and
// feeds never cross. "crawl:started" fires when a job begins (so the UI opens
// the live view, whether the crawl was hand-started or started by an MCP
// agent), and "crawl:done" fires once per crawl when it ends.

// FeedItem is one notable (non-2xx) URL for the live feed.
type FeedItem struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	State  string `json:"state"`
	Seq    int    `json:"seq"`
}

// ProgressSnapshot is the payload of the "crawl:progress" event.
type ProgressSnapshot struct {
	CrawlID    string     `json:"crawlId"`
	Seed       string     `json:"seed"`
	State      string     `json:"state"` // running | done
	Total      int        `json:"total"` // URLs processed so far (fetched + robots-blocked + errored)
	Discovered int        `json:"discovered"`
	Queue      int        `json:"queue"`
	S2xx       int        `json:"s2xx"`
	S3xx       int        `json:"s3xx"`
	S4xx       int        `json:"s4xx"`
	S5xx       int        `json:"s5xx"`
	Blocked    int        `json:"blocked"`
	NoResp     int        `json:"noresp"`
	Indexable  int        `json:"indexable"`
	Rate       float64    `json:"rate"` // pages/sec over the last few seconds
	ElapsedSec int        `json:"elapsedSec"`
	Threads    int        `json:"threads"`
	Feed       []FeedItem `json:"feed"`
}

// DoneEvent is the payload of the "crawl:done" event.
type DoneEvent struct {
	CrawlID     string `json:"crawlId"`
	Status      string `json:"status"`  // completed | interrupted | error (failed to start)
	Crawled     int    `json:"crawled"` // URLs fetched
	Total       int    `json:"total"`   // URLs encountered (fetched + blocked + errored)
	DurationSec int    `json:"durationSec"`
	Analyzed    bool   `json:"analyzed"`
	Error       string `json:"error,omitempty"`
}

// uiRun is one live crawl's UI-side state: its notable-URL feed and the stop
// signal for its progress ticker.
type uiRun struct {
	seq      int
	feed     []FeedItem
	stopTick chan struct{}
}

// uiObserver implements runner.Observer for the desktop app. emit abstracts
// runtime.EventsEmit so tests can capture the event stream headlessly.
type uiObserver struct {
	app  *App
	emit func(event string, data ...interface{})

	mu   sync.Mutex
	runs map[string]*uiRun // crawl id -> live UI state
}

func (o *uiObserver) OnStart(crawlID, seed string) {
	run := &uiRun{stopTick: make(chan struct{})}
	o.mu.Lock()
	if o.runs == nil {
		o.runs = map[string]*uiRun{}
	}
	o.runs[crawlID] = run
	o.mu.Unlock()

	o.emit("crawl:started", crawlID)

	// throttled per-crawl progress emitter
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if snap, ok := o.app.exec.SnapshotCrawl(crawlID); ok {
					o.emit("crawl:progress", o.build(snap, "running"))
				}
			case <-run.stopTick:
				return
			}
		}
	}()
}

func (o *uiObserver) OnPage(crawlID string, rec *crawler.PageRecord) {
	notable := false
	status := rec.StatusCode
	switch rec.State {
	case crawler.StateBlockedRobots:
		notable = true
		status = -1
	case crawler.StateError:
		notable = true
	default:
		if rec.StatusCode >= 300 {
			notable = true
		}
	}
	if !notable || rec.Scope != "internal" {
		return
	}
	o.mu.Lock()
	if run := o.runs[crawlID]; run != nil {
		run.seq++
		run.feed = append([]FeedItem{{URL: rec.URL, Status: status, State: rec.State, Seq: run.seq}}, run.feed...)
		if len(run.feed) > 60 {
			run.feed = run.feed[:60]
		}
	}
	o.mu.Unlock()
}

func (o *uiObserver) OnDone(out runner.Outcome) {
	o.mu.Lock()
	run := o.runs[out.CrawlID]
	if run != nil {
		close(run.stopTick)
	}
	o.mu.Unlock()

	o.app.invalidate(out.CrawlID)

	// final snapshot so the UI lands on exact numbers (the executor's in-flight
	// state is still live at OnDone, before it clears). A crawl that failed to
	// start has no run/snapshot — only the done event fires for it.
	if run != nil {
		if snap, ok := o.app.exec.SnapshotCrawl(out.CrawlID); ok {
			o.emit("crawl:progress", o.build(snap, "done"))
		}
	}
	done := DoneEvent{
		CrawlID: out.CrawlID, Status: doneStatus(out),
		Crawled: out.Crawled, Total: out.Total,
		DurationSec: out.DurationSec, Analyzed: out.Analyzed,
	}
	if out.Err != nil {
		done.Error = out.Err.Error()
	}
	o.emit("crawl:done", done)

	o.mu.Lock()
	delete(o.runs, out.CrawlID)
	o.mu.Unlock()
}

// doneStatus maps an Outcome to the UI's terminal status. A crawl that FAILED
// TO START (no status, an error, typically no crawl id) must read "error", not
// "completed" — the empty-status default used to make a failed start render as
// a successful crawl (#74 N10).
func doneStatus(out runner.Outcome) string {
	if out.Status != "" {
		return out.Status
	}
	if out.Err != nil {
		return "error"
	}
	return store.StatusCompleted
}

// build composes a ProgressSnapshot from the executor's live counters plus the
// snapshot's own crawl's notable-URL feed.
func (o *uiObserver) build(snap runner.Snapshot, state string) ProgressSnapshot {
	o.mu.Lock()
	var feed []FeedItem
	if run := o.runs[snap.CrawlID]; run != nil {
		feed = make([]FeedItem, len(run.feed))
		copy(feed, run.feed)
	}
	o.mu.Unlock()
	return ProgressSnapshot{
		CrawlID: snap.CrawlID, Seed: snap.Seed, State: state,
		Total: snap.Total, Discovered: snap.Discovered, Queue: snap.Queue,
		S2xx: snap.S2xx, S3xx: snap.S3xx, S4xx: snap.S4xx, S5xx: snap.S5xx,
		Blocked: snap.Blocked, NoResp: snap.NoResponse, Indexable: snap.Indexable,
		Rate: snap.RatePerSec, ElapsedSec: snap.ElapsedSec, Threads: snap.Threads,
		Feed: feed,
	}
}
