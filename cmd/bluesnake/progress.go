package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/spf13/cobra"
)

// Live progress for unattended crawls (DESIGN.md §3). `--progress json` streams
// the executor's live snapshot (the runner.Snapshot the desktop and MCP
// surfaces read) to stderr as JSON Lines: one line when the crawl starts, one
// per --progress-interval while it runs, and a final line carrying the
// terminal state. stdout is exactly what it is without the flag, so the summary
// and the "Crawl ID:" line stay where scripts already parse them.

const (
	progressNone = "none"
	progressJSON = "json"

	defaultProgressInterval = 10 * time.Second
	// minProgressInterval stops a mistyped interval (30ms for 30s) from
	// flooding a days-long log; the snapshot's rate is a 4s window anyway.
	minProgressInterval = time.Second
)

// progressOpts are the --progress flags shared by crawl, list and resume.
type progressOpts struct {
	mode     string
	interval time.Duration
}

func (p *progressOpts) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&p.mode, "progress", progressNone,
		"live progress on stderr while the crawl runs: none, or json (one JSON object per line)")
	cmd.Flags().DurationVar(&p.interval, "progress-interval", defaultProgressInterval,
		"how often --progress json prints a line (minimum 1s)")
}

// validate rejects unusable --progress flags before anything runs or prints,
// as a config error (exit 2).
func (p *progressOpts) validate(cmd *cobra.Command) error {
	var err error
	switch p.mode {
	case progressNone:
		if cmd.Flags().Changed("progress-interval") {
			err = fmt.Errorf("--progress-interval needs --progress json")
		}
	case progressJSON:
		if p.interval < minProgressInterval {
			err = fmt.Errorf("--progress-interval must be at least %s, got %s", minProgressInterval, p.interval)
		}
	default:
		err = fmt.Errorf("invalid --progress %q (want none or json)", p.mode)
	}
	if err != nil {
		return exitErr{2, err}
	}
	return nil
}

// feed returns the progress feed for a crawl run by exec, writing to w; nil
// when progress is off.
func (p *progressOpts) feed(w io.Writer, exec *runner.Executor) *progressFeed {
	if p.mode != progressJSON {
		return nil
	}
	return newProgressFeed(w, p.interval, exec.SnapshotCrawl)
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

// progressFeed writes one crawl's progress lines. The CLI observer drives it
// from the executor's callbacks (start on OnStart, finish on OnDone); a single
// feed goroutine does every write, so lines never interleave, and wait lets
// the command print its summary only after the final line is out.
type progressFeed struct {
	w        io.Writer
	interval time.Duration
	snapshot func(crawlID string) (runner.Snapshot, bool)

	final chan progressLine // the terminal line; buffered so finish never blocks
	done  chan struct{}     // closed once the final line is written; nil until start
}

func newProgressFeed(w io.Writer, interval time.Duration, snapshot func(string) (runner.Snapshot, bool)) *progressFeed {
	return &progressFeed{w: w, interval: interval, snapshot: snapshot, final: make(chan progressLine, 1)}
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
		case l := <-f.final:
			f.write(l)
			return
		case <-t.C:
			// the final line wins a tie, so nothing is ever written after it
			select {
			case l := <-f.final:
				f.write(l)
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
	f.write(newProgressLine(s, state))
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
	l := newProgressLine(s, out.Status)
	if out.Err != nil {
		l.Error = out.Err.Error()
	}
	f.final <- l
}

// wait blocks until the final line is written; a no-op when the feed never
// started.
func (f *progressFeed) wait() {
	if f.done != nil {
		<-f.done
	}
}

// write stamps and emits one line in a single Write, so a line is never split
// across writes even when stderr is a pipe shared with other output. Only the
// feed goroutine writes, so the stamps never go backwards.
func (f *progressFeed) write(l progressLine) {
	l.Time = time.Now().UTC().Format(time.RFC3339)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep URLs readable: & stays &
	if err := enc.Encode(l); err != nil {
		return // unreachable: the record is plain strings and numbers
	}
	f.w.Write(buf.Bytes())
}
