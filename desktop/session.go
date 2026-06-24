package main

import (
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// uiObserver turns a queue-driven crawl's lifecycle into the desktop's realtime
// Wails events. The engine already streams every page through the runner's sink;
// this observer aggregates the UI-specific bits the core counters don't carry —
// a feed of notable URLs — and runs the throttled "crawl:progress" emitter
// (~4/s) reading the executor's live snapshot. "crawl:started" fires when a job
// begins (so the UI jumps to the live view, whether the crawl was hand-started
// or started by an MCP agent), and "crawl:done" fires once when it ends.

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
	Status      string `json:"status"`  // completed | interrupted
	Crawled     int    `json:"crawled"` // URLs fetched
	Total       int    `json:"total"`   // URLs encountered (fetched + blocked + errored)
	DurationSec int    `json:"durationSec"`
	Analyzed    bool   `json:"analyzed"`
	Error       string `json:"error,omitempty"`
}

// uiObserver implements runner.Observer for the desktop app.
type uiObserver struct {
	app *App

	mu       sync.Mutex
	seq      int
	feed     []FeedItem
	stopTick chan struct{}
}

func (o *uiObserver) OnStart(crawlID, seed string) {
	o.mu.Lock()
	o.seq = 0
	o.feed = nil
	o.stopTick = make(chan struct{})
	stop := o.stopTick
	o.mu.Unlock()

	runtime.EventsEmit(o.app.ctx, "crawl:started", crawlID)

	// throttled progress emitter
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if snap, ok := o.app.exec.Snapshot(); ok {
					runtime.EventsEmit(o.app.ctx, "crawl:progress", o.build(snap, "running"))
				}
			case <-stop:
				return
			}
		}
	}()
}

func (o *uiObserver) OnPage(rec *crawler.PageRecord) {
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
	o.seq++
	o.feed = append([]FeedItem{{URL: rec.URL, Status: status, State: rec.State, Seq: o.seq}}, o.feed...)
	if len(o.feed) > 60 {
		o.feed = o.feed[:60]
	}
	o.mu.Unlock()
}

func (o *uiObserver) OnDone(out runner.Outcome) {
	o.mu.Lock()
	if o.stopTick != nil {
		close(o.stopTick)
		o.stopTick = nil
	}
	o.mu.Unlock()

	o.app.invalidate(out.CrawlID)

	// final snapshot so the UI lands on exact numbers (the executor's in-flight
	// state is still live at OnDone, before it clears).
	if snap, ok := o.app.exec.Snapshot(); ok {
		runtime.EventsEmit(o.app.ctx, "crawl:progress", o.build(snap, "done"))
	}
	done := DoneEvent{
		CrawlID: out.CrawlID, Status: out.Status,
		Crawled: out.Crawled, Total: out.Total,
		DurationSec: out.DurationSec, Analyzed: out.Analyzed,
	}
	if done.Status == "" {
		done.Status = store.StatusCompleted
	}
	if out.Err != nil {
		done.Error = out.Err.Error()
	}
	runtime.EventsEmit(o.app.ctx, "crawl:done", done)
}

// build composes a ProgressSnapshot from the executor's live counters plus the
// observer's notable-URL feed.
func (o *uiObserver) build(snap runner.Snapshot, state string) ProgressSnapshot {
	o.mu.Lock()
	feed := make([]FeedItem, len(o.feed))
	copy(feed, o.feed)
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
