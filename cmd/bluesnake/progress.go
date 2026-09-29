package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
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

// progressOpts are the --progress flags shared by crawl, list and resume.
type progressOpts struct {
	mode        string
	interval    time.Duration
	intervalSet bool // --progress-interval given explicitly
}

func (p *progressOpts) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.mode, "progress", progressNone,
		"live progress on stderr while the crawl runs: none, bar (a live panel: a bar split by status class, each class's status codes, ETA, the last minute's rate and response times), or json (one JSON object per line)")
	cmd.Flags().DurationVar(&p.interval, "progress-interval", defaultProgressInterval,
		"how often --progress reports (minimum 1s); a bar on a terminal redraws every second unless this is set")
}

// validate rejects unusable --progress flags before anything runs or prints,
// as a config error (exit 2).
func (p *progressOpts) validate(cmd *cobra.Command) error {
	var err error
	p.intervalSet = cmd.Flags().Changed("progress-interval")
	switch p.mode {
	case progressNone:
		if p.intervalSet {
			err = fmt.Errorf("--progress-interval needs --progress bar or json")
		}
	case progressBar, progressJSON:
		if p.interval < minProgressInterval {
			err = fmt.Errorf("--progress-interval must be at least %s, got %s", minProgressInterval, p.interval)
		}
	default:
		err = fmt.Errorf("invalid --progress %q (want none, bar or json)", p.mode)
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
	Error      string          `json:"error,omitempty"`       // final line only
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

// progressFeed writes one crawl's progress. The CLI observer drives it from
// the executor's callbacks (start on OnStart, finish on OnDone); a single feed
// goroutine does every write, so lines never interleave, and wait lets the
// command print its summary only after the final reading is out.
type progressFeed struct {
	out      progressOutput
	interval time.Duration
	snapshot func(crawlID string) (runner.Snapshot, bool)
	pages    *pageStats // the bar's per-page tallies; nil for JSON

	final chan progressReading // the terminal reading; buffered so finish never blocks
	done  chan struct{}        // closed once the final reading is written; nil until start
}

// newProgressFeed returns a feed writing JSON Lines records to w.
func newProgressFeed(w io.Writer, interval time.Duration, snapshot func(string) (runner.Snapshot, bool)) *progressFeed {
	return &progressFeed{out: jsonOutput{w}, interval: interval, snapshot: snapshot, final: make(chan progressReading, 1)}
}

// start begins the feed for a crawl that has just started: a line right away,
// so a consumer learns the crawl id without waiting an interval, then one per
// interval.
func (f *progressFeed) start(crawlID string) {
	f.done = make(chan struct{})
	go f.run(crawlID)
}

func (f *progressFeed) run(crawlID string) {
	defer close(f.done)
	f.tick(crawlID)
	t := time.NewTicker(f.interval)
	defer t.Stop()
	for {
		select {
		case r := <-f.final:
			f.out.write(r)
			return
		case <-t.C:
			// the final reading wins a tie, so nothing is ever written after it
			select {
			case r := <-f.final:
				f.out.write(r)
				return
			default:
				f.tick(crawlID)
			}
		}
	}
}

func (f *progressFeed) tick(crawlID string) {
	s, ok := f.snapshot(crawlID)
	if !ok {
		return
	}
	state := "running"
	if s.Finalizing {
		state = "finalizing"
	}
	f.out.write(progressReading{snap: s, state: state})
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
	if f.done == nil {
		return
	}
	s, _ := f.snapshot(out.CrawlID)
	r := progressReading{snap: s, state: out.Status, final: true}
	if out.Err != nil {
		r.err = out.Err.Error()
	}
	f.final <- r
}

// wait blocks until the final line is written; a no-op when the feed never
// started.
func (f *progressFeed) wait() {
	if f.done != nil {
		<-f.done
	}
}

// jsonOutput writes each reading as a JSON Lines record.
type jsonOutput struct{ w io.Writer }

// write stamps and emits one record in a single Write, so a line is never
// split across writes even when stderr is a pipe shared with other output.
// Only the feed goroutine writes, so the stamps never go backwards.
func (o jsonOutput) write(r progressReading) {
	l := newProgressLine(r.snap, r.state)
	l.Error = r.err
	l.Time = time.Now().UTC().Format(time.RFC3339)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep URLs readable: & stays &
	if err := enc.Encode(l); err != nil {
		return // unreachable: the record is plain strings and numbers
	}
	o.w.Write(buf.Bytes())
}
