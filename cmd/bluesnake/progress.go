package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

// Live progress on stderr (DESIGN.md §3), from the executor's live snapshot
// (the runner.Snapshot the desktop and MCP surfaces read): a reading when the
// crawl starts, one per --progress-interval while it runs, and a final one
// carrying the terminal state. `--progress json` writes each reading as a JSON
// Lines record for unattended crawls; `--progress bar` draws it for a person
// watching a terminal. stdout is exactly what it is without the flag, so the
// summary and the "Crawl ID:" line stay where scripts already parse them.

const (
	progressNone = "none"
	progressBar  = "bar"
	progressJSON = "json"

	defaultProgressInterval = 10 * time.Second
	// minProgressInterval stops a mistyped interval (30ms for 30s) from
	// flooding a days-long log; the snapshot's rate is a 4s window anyway.
	minProgressInterval = time.Second
	// barRedrawInterval is how often a bar on a terminal redraws unless
	// --progress-interval says otherwise: a person is watching it.
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
		"live progress on stderr while the crawl runs: none, bar (a progress bar: crawled, left, rate, ETA), or json (one JSON object per line)")
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
		redraw := isTerminal(w)
		interval := p.interval
		if redraw && !p.intervalSet {
			interval = barRedrawInterval
		}
		f := newProgressFeed(w, interval, exec.SnapshotCrawl)
		f.out = &progressBarOutput{w: w, redraw: redraw}
		return f
	}
	return nil
}

// isTerminal reports whether w is a terminal, where a bar can redraw its line
// in place.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
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

// progressBarOutput draws each reading as one line for a person to read:
//
//	████░░░░░░░░░░░░░░░░  21%  2,480/11,879  9,399 left  3.2 URLs/s  ETA 49m
//
// On a terminal it redraws that line in place. Anywhere else (a file, a pipe)
// carriage returns would pile up, so each reading gets a line of its own. The
// final reading always ends its line, so the summary starts on a fresh one.
type progressBarOutput struct {
	w      io.Writer
	redraw bool

	shown int // columns of the line on screen, for blanking a shorter redraw
	// base is the processed count at the first reading. A resume's earlier
	// pages took no time in this run, so they stay out of the rate.
	base    int
	started bool // base is set
}

const barCells = 20

func (o *progressBarOutput) write(r progressReading) {
	if !o.started {
		o.base, o.started = r.snap.Total, true
	}
	line := o.line(r)
	if !o.redraw {
		io.WriteString(o.w, line+"\n")
		return
	}
	n := utf8.RuneCountInString(line)
	out := "\r" + line + strings.Repeat(" ", max(o.shown-n, 0))
	if r.final {
		out += "\n"
	}
	o.shown = n
	io.WriteString(o.w, out) // one Write, so the line never tears
}

func (o *progressBarOutput) line(r progressReading) string {
	s := r.snap
	done := s.Total
	// What the crawl will process: what it has discovered, unless it stops
	// short at max_urls. The total grows while discovery goes on.
	total := s.Discovered
	if s.MaxURLs > 0 && s.MaxURLs < total {
		total = s.MaxURLs
	}
	if total < done || r.state == store.StatusCompleted {
		total = done
	}
	pct, filled := 0, 0
	if total > 0 {
		pct, filled = done*100/total, done*barCells/total
	}
	head := fmt.Sprintf("%s%s %4d%%  %s/%s",
		strings.Repeat("█", filled), strings.Repeat("░", barCells-filled),
		pct, groupDigits(done), groupDigits(total))
	elapsed := time.Duration(s.ElapsedSec) * time.Second

	switch r.state {
	case store.StatusCompleted:
		return head + "  done in " + shortDuration(elapsed)
	case store.StatusInterrupted:
		return head + "  interrupted after " + shortDuration(elapsed)
	case "finalizing":
		return head + "  analysing…"
	}
	// Rate and ETA over this run so far; the snapshot's own rate is a 4s
	// window, too jumpy to project hours ahead from.
	left := total - done
	var rate float64
	if s.ElapsedSec > 0 {
		rate = float64(done-o.base) / float64(s.ElapsedSec)
	}
	eta := "--"
	if rate > 0 {
		eta = shortDuration(time.Duration(float64(left) / rate * float64(time.Second)))
	}
	return fmt.Sprintf("%s  %s left  %s URLs/s  ETA %s", head, groupDigits(left), formatRate(rate), eta)
}

// formatRate keeps a slow rendering crawl's rate readable (0.4, not 0) and a
// fast one's short (212, not 212.4).
func formatRate(r float64) string {
	if r < 10 {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	return strconv.FormatFloat(r, 'f', 0, 64)
}

// groupDigits writes n with thousands separators: 11879 -> "11,879".
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + groupDigits(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// shortDuration rounds d to its two leading units: 45s, 3m12s, 7h41m, 2d5h.
func shortDuration(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec < 3600:
		return fmt.Sprintf("%dm%02ds", sec/60, sec%60)
	case sec < 86400:
		return fmt.Sprintf("%dh%02dm", sec/3600, sec%3600/60)
	}
	return fmt.Sprintf("%dd%dh", sec/86400, sec%86400/3600)
}
