package main

import (
	"context"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Realtime model: the crawler already streams every page and frontier
// mutation through its Sink interface (that is how the store persists a
// crawl incrementally). uiSink tees that stream — records keep flowing to
// the real *store.Crawl sink, while a crawlSession aggregates counters and a
// feed of notable URLs. A ticker goroutine emits a throttled
// "crawl:progress" Wails event (~4/s) so the UI animates smoothly without
// flooding the bridge, and "crawl:done" fires once when the run ends.

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
	Crawled    int        `json:"crawled"`
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
	Status      string `json:"status"` // completed | interrupted
	Crawled     int    `json:"crawled"`
	DurationSec int    `json:"durationSec"`
	Analyzed    bool   `json:"analyzed"`
	Error       string `json:"error,omitempty"`
}

type crawlSession struct {
	app    *App
	st     *store.Crawl
	cfg    *config.Config
	c      *crawler.Crawler
	seeds  []string
	cancel context.CancelFunc
	ctx    context.Context
	doneCh chan struct{}

	mu         sync.Mutex
	stopMode   string // "" | "pause" | "stop"
	done       bool
	crawled    int
	discovered int
	doneFront  int
	s2, s3     int
	s4, s5     int
	blocked    int
	noresp     int
	indexable  int
	seq        int
	feed       []FeedItem
	recent     []time.Time // page completion times for the live rate
	started    time.Time
}

func newCrawlSession(a *App, st *store.Crawl, cfg *config.Config, seeds []string, processed []string, pending []frontier.Item) (*crawlSession, error) {
	s := &crawlSession{
		app: a, st: st, cfg: cfg, seeds: seeds,
		started: time.Now(),
		doneCh:  make(chan struct{}),
		// resumed crawls start from what is already on disk
		crawled:    len(processed),
		discovered: len(processed) + len(pending),
	}
	opts := []crawler.Option{crawler.WithSink(&uiSink{inner: st, s: s})}
	if processed != nil || pending != nil {
		opts = append(opts, crawler.WithResume(processed, pending))
	}
	c, err := crawler.New(cfg, opts...)
	if err != nil {
		return nil, err
	}
	s.c = c
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

func (s *crawlSession) run() {
	defer close(s.doneCh)
	defer s.c.Close()
	defer s.st.Close()

	// throttled progress emitter
	tick := time.NewTicker(250 * time.Millisecond)
	stopTick := make(chan struct{})
	go func() {
		for {
			select {
			case <-tick.C:
				runtime.EventsEmit(s.app.ctx, "crawl:progress", s.snapshot())
			case <-stopTick:
				tick.Stop()
				return
			}
		}
	}()

	res, runErr := s.c.Run(s.ctx, s.seeds...)

	s.mu.Lock()
	s.done = true
	mode := s.stopMode
	s.mu.Unlock()
	close(stopTick)

	done := DoneEvent{CrawlID: s.st.ID, Status: store.StatusCompleted}
	if runErr != nil {
		done.Error = runErr.Error()
	}
	if res != nil {
		done.Crawled = res.Crawled
		done.DurationSec = int(res.Duration.Seconds())
		if err := s.st.UpdateInlinks(res.Pages); err != nil && done.Error == "" {
			done.Error = err.Error()
		}
		// Pause keeps the crawl resumable; Stop finalises early as completed.
		if res.Interrupted && mode != "stop" {
			done.Status = store.StatusInterrupted
		}
		_ = store.SetStatus(s.app.storeDir, s.st.ID, done.Status, res.Crawled)
		if done.Status == store.StatusCompleted && s.cfg.Analysis.Auto {
			if err := reanalyze(s.st); err == nil {
				done.Analyzed = true
			} else if done.Error == "" {
				done.Error = "analysis: " + err.Error()
			}
		}
	}
	s.app.invalidate(s.st.ID)

	// final snapshot so the UI lands on exact numbers
	snap := s.snapshot()
	snap.State = "done"
	runtime.EventsEmit(s.app.ctx, "crawl:progress", snap)
	runtime.EventsEmit(s.app.ctx, "crawl:done", done)
}

func (s *crawlSession) stop(mode string) {
	s.mu.Lock()
	if s.stopMode == "" {
		s.stopMode = mode
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *crawlSession) wait() { <-s.doneCh }

func (s *crawlSession) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *crawlSession) snapshot() ProgressSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	// live rate over a 4s sliding window
	cutoff := time.Now().Add(-4 * time.Second)
	i := 0
	for i < len(s.recent) && s.recent[i].Before(cutoff) {
		i++
	}
	s.recent = s.recent[i:]
	rate := float64(len(s.recent)) / 4.0

	state := "running"
	if s.done {
		state = "done"
	}
	queue := s.discovered - s.crawled
	if queue < 0 {
		queue = 0
	}
	feed := make([]FeedItem, len(s.feed))
	copy(feed, s.feed)
	return ProgressSnapshot{
		CrawlID: s.st.ID, Seed: s.seeds[0], State: state,
		Crawled: s.crawled, Discovered: s.discovered, Queue: queue,
		S2xx: s.s2, S3xx: s.s3, S4xx: s.s4, S5xx: s.s5,
		Blocked: s.blocked, NoResp: s.noresp, Indexable: s.indexable,
		Rate: rate, ElapsedSec: int(time.Since(s.started).Seconds()),
		Threads: s.cfg.Speed.MaxThreads, Feed: feed,
	}
}

func (s *crawlSession) onPage(rec *crawler.PageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crawled++
	s.recent = append(s.recent, time.Now())

	notable := false
	switch rec.State {
	case crawler.StateBlockedRobots:
		s.blocked++
		notable = true
	case crawler.StateError:
		s.noresp++
		notable = true
	default:
		switch {
		case rec.StatusCode >= 500:
			s.s5++
			notable = true
		case rec.StatusCode >= 400:
			s.s4++
			notable = true
		case rec.StatusCode >= 300:
			s.s3++
			notable = true
		case rec.StatusCode >= 200:
			s.s2++
		}
		if rec.Indexable {
			s.indexable++
		}
	}
	if notable && rec.Scope == "internal" {
		s.seq++
		status := rec.StatusCode
		if rec.State == crawler.StateBlockedRobots {
			status = -1
		}
		s.feed = append([]FeedItem{{URL: rec.URL, Status: status, State: rec.State, Seq: s.seq}}, s.feed...)
		if len(s.feed) > 60 {
			s.feed = s.feed[:60]
		}
	}
}

func (s *crawlSession) onFrontierAdd() {
	s.mu.Lock()
	s.discovered++
	s.mu.Unlock()
}

// uiSink tees the crawl stream: persistence first, then UI counters.
type uiSink struct {
	inner *store.Crawl
	s     *crawlSession
}

func (t *uiSink) Page(rec *crawler.PageRecord) error {
	if err := t.inner.Page(rec); err != nil {
		return err
	}
	t.s.onPage(rec)
	return nil
}

func (t *uiSink) FrontierAdd(it frontier.Item) error {
	if err := t.inner.FrontierAdd(it); err != nil {
		return err
	}
	t.s.onFrontierAdd()
	return nil
}

func (t *uiSink) FrontierDone(url string) error { return t.inner.FrontierDone(url) }

// Blob keeps the optional BlobSink extension working (stored HTML,
// screenshots) when the engine asks for it.
func (t *uiSink) Blob(url, kind string, data []byte) error {
	return t.inner.Blob(url, kind, data)
}

// Archive forwards the optional ArchiveSink extension so extraction.store_warc
// works from the desktop app (the engine reaches it by type assertion).
func (t *uiSink) Archive(url string, res *fetch.Result) error {
	return t.inner.Archive(url, res)
}
