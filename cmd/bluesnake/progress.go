package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Live progress on stderr (DESIGN.md §3), from the executor's live snapshot
// (the runner.Snapshot the desktop and MCP surfaces read): a reading when the
// crawl starts, one per --progress-interval while it runs, and a final one
// carrying the terminal state. `--progress json` writes each reading as a JSON
// Lines record for unattended crawls; `--progress bar` draws it for a person
// watching a terminal (progress_bar.go). stdout is exactly what it is without
// the flag, so the summary and the "Crawl ID:" line stay where scripts already
// parse them.
//
// `tools aibots` streams its check's progress the same way (`--progress json`
// only: it has no panel to draw), from the counters the check keeps
// (sitecheck.AIBotsProgress) rather than an executor's snapshot, in a record
// of its own.

const (
	progressNone = "none"
	progressBar  = "bar"
	progressJSON = "json"

	defaultProgressInterval = 10 * time.Second
	// minProgressInterval stops a mistyped interval (30ms for 30s) from
	// flooding a days-long log; the snapshot's rate is a 4s window anyway.
	minProgressInterval = time.Second
	// barRedrawInterval is how often the bar's panel redraws on a terminal
	// unless --progress-interval says otherwise: a person is watching it.
	barRedrawInterval = time.Second
)

// progressOpts are the --progress flags shared by crawl, list, resume and
// tools aibots.
type progressOpts struct {
	mode        string
	interval    time.Duration
	intervalSet bool // --progress-interval given explicitly
	jsonOnly    bool // the command has no bar to draw
}

func (p *progressOpts) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.mode, "progress", progressNone,
		"live progress on stderr while the crawl runs: none, bar (a live panel: a bar split by status class, each class's status codes, ETA, the last minute's rate and response times), or json (one JSON object per line)")
	cmd.Flags().DurationVar(&p.interval, "progress-interval", defaultProgressInterval,
		"how often --progress reports (minimum 1s); a bar on a terminal redraws every second unless this is set")
}

// registerJSON registers the flags for a command whose progress is JSON Lines
// only, described by usage; --progress bar is a config error there.
func (p *progressOpts) registerJSON(cmd *cobra.Command, usage string) {
	p.jsonOnly = true
	cmd.Flags().StringVar(&p.mode, "progress", progressNone, usage)
	cmd.Flags().DurationVar(&p.interval, "progress-interval", defaultProgressInterval,
		"how often --progress reports (minimum 1s)")
}

// validate rejects unusable --progress flags before anything runs or prints,
// as a config error (exit 2).
func (p *progressOpts) validate(cmd *cobra.Command) error {
	modes := "bar or json"
	if p.jsonOnly {
		modes = "json"
	}
	var err error
	p.intervalSet = cmd.Flags().Changed("progress-interval")
	switch {
	case p.mode == progressNone:
		if p.intervalSet {
			err = fmt.Errorf("--progress-interval needs --progress %s", modes)
		}
	case p.mode == progressJSON || p.mode == progressBar && !p.jsonOnly:
		if p.interval < minProgressInterval {
			err = fmt.Errorf("--progress-interval must be at least %s, got %s", minProgressInterval, p.interval)
		}
	default:
		err = fmt.Errorf("invalid --progress %q (want none or %s)", p.mode, modes)
	}
	if err != nil {
		return exitErr{2, err}
	}
	return nil
}

// feed returns the progress feed for a crawl run by exec, writing to w; nil
// when progress is off.
func (p *progressOpts) feed(w io.Writer, exec *runner.Executor) *progressFeed {
	switch p.mode {
	case progressJSON:
		return newProgressFeed(w, p.interval, exec.SnapshotCrawl)
	case progressBar:
		out := &progressBarOutput{w: w, pages: &pageStats{}}
		interval := p.interval
		if tty, ok := w.(*os.File); ok && term.IsTerminal(int(tty.Fd())) && os.Getenv("TERM") != "dumb" {
			out.redraw = true
			out.color = os.Getenv("NO_COLOR") == "" // https://no-color.org
			out.size = func() (int, int) {
				cols, rows, err := term.GetSize(int(tty.Fd()))
				if err != nil {
					return 80, 24
				}
				return cols, rows
			}
			if !p.intervalSet {
				interval = barRedrawInterval
			}
		}
		f := newProgressFeed(w, interval, exec.SnapshotCrawl)
		f.out, f.pages = out, out.pages
		return f
	}
	return nil
}

// progressLine is one JSON Lines record. Names match the MCP crawl_status
// payload where the meaning is the same; the snapshot's Total is spelled
// "processed" because the registry's "total" counts something else.
type progressLine struct {
	Type       string          `json:"type"` // always "progress"
	Time       string          `json:"time"` // RFC 3339, UTC, stamped when written
	CrawlID    string          `json:"crawl_id"`
	Seed       string          `json:"seed"`
	State      string          `json:"state"`       // running | finalizing | completed | interrupted
	ElapsedSec int             `json:"elapsed_sec"` // since this run started (a resume counts from the resume)
	Processed  int             `json:"processed"`   // fetched + robots-blocked + no response
	Discovered int             `json:"discovered"`  // admitted so far, queued ones included
	Queued     int             `json:"queued"`
	URLsPerSec float64         `json:"urls_per_sec"` // over the last ~4s
	S2xx       int             `json:"status_2xx"`
	S3xx       int             `json:"status_3xx"`
	S4xx       int             `json:"status_4xx"`
	S5xx       int             `json:"status_5xx"`
	Blocked    int             `json:"blocked_by_robots"`
	NoResponse int             `json:"no_response"`
	Indexable  int             `json:"indexable"`
	SiteChecks *siteChecksLine `json:"site_checks,omitempty"` // only when the pass is part of the crawl
	Egress     *egressLine     `json:"egress,omitempty"`      // only with http.proxy_on_block
	Error      string          `json:"error,omitempty"`       // final line only
}

// egressLine is the http.proxy_on_block switch: mode direct | draining | proxy.
type egressLine struct {
	Mode          string `json:"mode"`
	SwitchedAfter int64  `json:"switched_after,omitempty"` // pages recorded when it tripped
	Refetched     int64  `json:"refetched,omitempty"`      // blocked URLs re-fetched through the proxy
	StillBlocked  int64  `json:"still_blocked,omitempty"`  // block responses through the proxy
}

type siteChecksLine struct {
	State    string `json:"state"` // running | done
	Ran      int    `json:"ran"`
	Findings int    `json:"findings"`
}

func newProgressLine(s runner.Snapshot, state string) progressLine {
	l := progressLine{
		Type:    "progress",
		CrawlID: s.CrawlID, Seed: s.Seed, State: state, ElapsedSec: s.ElapsedSec,
		Processed: s.Total, Discovered: s.Discovered, Queued: s.Queue, URLsPerSec: s.RatePerSec,
		S2xx: s.S2xx, S3xx: s.S3xx, S4xx: s.S4xx, S5xx: s.S5xx,
		Blocked: s.Blocked, NoResponse: s.NoResponse, Indexable: s.Indexable,
	}
	if s.SiteChecksState != "" {
		l.SiteChecks = &siteChecksLine{State: s.SiteChecksState, Ran: s.SiteChecksRan, Findings: s.SiteChecksFindings}
	}
	if e := s.Egress; e.Mode != "" {
		l.Egress = &egressLine{Mode: e.Mode, SwitchedAfter: e.SwitchedAfter, Refetched: e.Refetched, StillBlocked: e.StillBlocked}
	}
	return l
}

// progressReading is one reading of a crawl's progress: its live snapshot and
// state (running, finalizing, or on the final reading the terminal status and
// any error from the outcome).
type progressReading struct {
	snap  runner.Snapshot
	state string
	err   string
	final bool
}

// progressOutput renders readings. Only the feed goroutine calls it, so an
// output never sees two readings at once.
type progressOutput interface {
	write(progressReading)
}

// feedLoop is a progress feed's ticker, shared by the crawl feed and the
// AI-bot check's: one goroutine writes a reading as soon as the run starts,
// one per interval, and the final reading, so lines never interleave and
// nothing is written after the final one; wait lets a command print what
// comes next only once the final reading is out.
type feedLoop[R any] struct {
	interval time.Duration
	final    chan R        // the terminal reading; buffered so finish never blocks
	done     chan struct{} // closed once the final reading is written; nil until start
}

func newFeedLoop[R any](interval time.Duration) feedLoop[R] {
	return feedLoop[R]{interval: interval, final: make(chan R, 1)}
}

// start runs the loop: read is taken right away, then once per interval, and
// each reading it returns ok is written.
func (l *feedLoop[R]) start(read func() (R, bool), write func(R)) {
	l.done = make(chan struct{})
	go l.run(read, write)
}

func (l *feedLoop[R]) run(read func() (R, bool), write func(R)) {
	defer close(l.done)
	tick := func() {
		if r, ok := read(); ok {
			write(r)
		}
	}
	tick()
	t := time.NewTicker(l.interval)
	defer t.Stop()
	for {
		select {
		case r := <-l.final:
			write(r)
			return
		case <-t.C:
			// the final reading wins a tie, so nothing is ever written after it
			select {
			case r := <-l.final:
				write(r)
				return
			default:
				tick()
			}
		}
	}
}

func (l *feedLoop[R]) started() bool { return l.done != nil }

// finish hands the started loop its final reading. It never blocks.
func (l *feedLoop[R]) finish(r R) { l.final <- r }

// wait blocks until the final reading is written; a no-op when the loop never
// started.
func (l *feedLoop[R]) wait() {
	if l.done != nil {
		<-l.done
	}
}

// progressFeed writes one crawl's progress. The CLI observer drives it from
// the executor's callbacks (start on OnStart, finish on OnDone).
type progressFeed struct {
	loop     feedLoop[progressReading]
	out      progressOutput
	snapshot func(crawlID string) (runner.Snapshot, bool)
	pages    *pageStats // the bar's per-page tallies; nil for JSON
}

// newProgressFeed returns a feed writing JSON Lines records to w.
func newProgressFeed(w io.Writer, interval time.Duration, snapshot func(string) (runner.Snapshot, bool)) *progressFeed {
	return &progressFeed{loop: newFeedLoop[progressReading](interval), out: jsonOutput{w}, snapshot: snapshot}
}

// start begins the feed for a crawl that has just started: a line right away,
// so a consumer learns the crawl id without waiting an interval, then one per
// interval.
func (f *progressFeed) start(crawlID string) {
	f.loop.start(func() (progressReading, bool) { return f.read(crawlID) }, f.out.write)
}

func (f *progressFeed) read(crawlID string) (progressReading, bool) {
	s, ok := f.snapshot(crawlID)
	if !ok {
		return progressReading{}, false
	}
	state := "running"
	if s.Finalizing {
		state = "finalizing"
	}
	return progressReading{snap: s, state: state}, true
}

// page hands the feed a page the crawl has processed, for an output that
// tallies pages itself (the bar's last minute). The observer calls it from the
// crawl's goroutines.
func (f *progressFeed) page(rec *crawler.PageRecord) {
	if f.pages != nil {
		f.pages.page(rec)
	}
}

// finish hands the feed its final line: the crawl's last live snapshot with
// the terminal state. It runs in OnDone, while the executor still holds the
// crawl (it deregisters after OnDone returns), and never blocks, per the
// Observer contract. A crawl that never started has no feed to finish.
func (f *progressFeed) finish(out runner.Outcome) {
	if !f.loop.started() {
		return
	}
	s, _ := f.snapshot(out.CrawlID)
	r := progressReading{snap: s, state: out.Status, final: true}
	if out.Err != nil {
		r.err = out.Err.Error()
	}
	f.loop.finish(r)
}

// wait blocks until the final line is written; a no-op when the feed never
// started.
func (f *progressFeed) wait() { f.loop.wait() }

// jsonOutput writes each reading as a JSON Lines record.
type jsonOutput struct{ w io.Writer }

func (o jsonOutput) write(r progressReading) {
	l := newProgressLine(r.snap, r.state)
	l.Error = r.err
	l.Time = progressTime()
	writeJSONLine(o.w, l)
}

// progressTime stamps a record as it is written. Only a feed's goroutine
// writes, so the stamps never go backwards.
func progressTime() string { return time.Now().UTC().Format(time.RFC3339) }

// writeJSONLine emits one record in a single Write, so a line is never split
// across writes even when stderr is a pipe shared with other output.
func writeJSONLine(w io.Writer, record any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep URLs readable: & stays &
	if err := enc.Encode(record); err != nil {
		return // unreachable: a record is plain strings and numbers
	}
	w.Write(buf.Bytes())
}

// aibotsFeed writes `tools aibots` progress: a record when the check starts —
// once its site and URLs are valid, with the run's size already counted — one
// per interval, and a final one carrying how the check ended.
type aibotsFeed struct {
	loop  feedLoop[aibotsProgressLine]
	w     io.Writer
	prog  sitecheck.AIBotsProgress
	site  string
	began time.Time
}

// aibotsProgressLine is one record of the check's feed. It shares the crawl
// record's names where the meaning is the same and carries none of its crawl
// fields. The check has nothing to fail once it has started — a bad site or
// URL list is rejected before the first record — so it has no error field.
type aibotsProgressLine struct {
	Type         string `json:"type"`          // always "progress"
	Time         string `json:"time"`          // RFC 3339, UTC, stamped when written
	Site         string `json:"site"`          // the site root the check runs against
	State        string `json:"state"`         // running | completed | interrupted
	ElapsedSec   int    `json:"elapsed_sec"`   // since the check started
	PagesTotal   int    `json:"pages_total"`   // after repeats are dropped; 1 for the root check
	PagesDone    int    `json:"pages_done"`    // control fetch and every probe finished, errors included
	FetchesTotal int    `json:"fetches_total"` // control + one per fetcher bot, per page; 0 without --live
	FetchesDone  int    `json:"fetches_done"`  // errors included
}

// aibotsFeed returns the check's feed, writing to w; nil when progress is off.
func (p *progressOpts) aibotsFeed(w io.Writer) *aibotsFeed {
	if p.mode != progressJSON {
		return nil
	}
	f := &aibotsFeed{loop: newFeedLoop[aibotsProgressLine](p.interval), w: w}
	f.prog.OnStart = f.start
	return f
}

// counters returns what the check counts into; nil (count nothing) without a
// feed.
func (f *aibotsFeed) counters() *sitecheck.AIBotsProgress {
	if f == nil {
		return nil
	}
	return &f.prog
}

// start is the check's OnStart, called once before its first fetch.
func (f *aibotsFeed) start(site string) {
	f.site, f.began = site, time.Now()
	f.loop.start(func() (aibotsProgressLine, bool) { return f.read("running"), true }, f.write)
}

func (f *aibotsFeed) read(state string) aibotsProgressLine {
	c := f.prog.Counts()
	return aibotsProgressLine{
		Type: "progress", Site: f.site, State: state, ElapsedSec: int(time.Since(f.began).Seconds()),
		PagesTotal: c.PagesTotal, PagesDone: c.PagesDone, FetchesTotal: c.FetchesTotal, FetchesDone: c.FetchesDone,
	}
}

func (f *aibotsFeed) write(l aibotsProgressLine) {
	l.Time = progressTime()
	writeJSONLine(f.w, l)
}

// end writes the final record for a check that has returned and maps how it
// ended onto the exit codes. A check rejected before it started (a bad site or
// URL list) is a config error, with no record written. With a feed, a check
// whose context ended — Ctrl-C — was interrupted (exit 3): its final record
// says so and its report, made of cut-short fetches, is not written.
func (f *aibotsFeed) end(ctx context.Context, err error) error {
	if f != nil && ctx.Err() != nil {
		f.finish(store.StatusInterrupted)
		return exitErr{3, errors.New("interrupted")}
	}
	if err != nil {
		return exitErr{2, err}
	}
	if f != nil {
		f.finish(store.StatusCompleted)
	}
	return nil
}

// finish writes the final record and waits for it, so the report the command
// prints next never lands before it.
func (f *aibotsFeed) finish(state string) {
	if !f.loop.started() {
		return
	}
	f.loop.finish(f.read(state))
	f.loop.wait()
}
