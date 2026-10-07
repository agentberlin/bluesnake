package main

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// progressBarOutput draws --progress bar. On a terminal it is a panel redrawn
// in place every second:
//
//	crawling https://www.example.com/                           12m04s
//	██████████████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   31%
//	3,712 of 11,879 URLs · 8,167 left · ETA 48m57s
//
//	■ 2xx          3,402  91.6%  200 ×3,390 · 204 ×12
//	■ 3xx            180   4.8%  301 ×150 · 302 ×30
//	■ 4xx            118   3.2%  404 ×110 · 403 ×8
//	■ 5xx              4   0.1%  503 ×4
//	■ blocked          6   0.2%  by robots.txt
//	■ no response      2   0.1%  latest: dial tcp 93.184.216.34:443: i/o timeout
//
//	last minute  ████████████  212 URLs · 3.5/s · 412 ms avg · 48 KB avg
//
// The bar's filled part is split by status class in the colours of the rows
// under it, and the last-minute line has the same split for just the last
// minute, so a crawl that starts drawing 403s or timeouts shows it as it
// happens instead of hours later in the summary. When the terminal is too
// small for the panel, and anywhere that is not a terminal (a file, a pipe,
// TERM=dumb), each reading is one line instead:
//
//	████░░░░░░░░░░░░░░░░   20%  2,480/11,879  9,399 left  3.2 URLs/s  ETA 48m57s  ·  2xx 2,400  4xx 80
//
// Off a terminal those lines are appended, one per reading, since redrawing
// would pile carriage returns and escapes into the file. The final reading
// stays on screen, so the summary on stdout starts below it.
type progressBarOutput struct {
	w      io.Writer
	redraw bool // w is a terminal that takes cursor movement
	color  bool
	// size reports the terminal's columns and rows; used only when redrawing.
	size  func() (cols, rows int)
	pages *pageStats

	shown int // lines of the last frame, which the next one draws over
	// base is the processed count at the first reading. A resume's earlier
	// pages took no time in this run, so they stay out of the rate.
	base    int
	started bool // base is set
}

const (
	lineCells  = 20 // the one-line form's bar
	panelCells = 60 // the panel's bar, at most; it narrows with the terminal
	mixCells   = 12 // the last-minute split
	// The panel needs a bar of at least minPanelCells and one row of room
	// below it, or it falls back to the line.
	minPanelCells = 30
)

func (o *progressBarOutput) write(r progressReading) {
	if !o.started {
		o.base, o.started = r.snap.Total, true
	}
	if !o.redraw {
		io.WriteString(o.w, o.line(r).render(false, 0)+"\n")
		return
	}
	var recent recentPages
	if o.pages != nil {
		recent = o.pages.read(time.Now())
	}
	cols, rows := o.size()
	width := cols - 1 // a line filling the last column wraps on some terminals
	var frame []styledLine
	if width-6 >= minPanelCells {
		frame = o.panel(r, recent, width)
	}
	if len(frame) == 0 || len(frame) >= rows {
		frame = []styledLine{o.line(r)}
	} else if r.final {
		frame = append(frame, nil) // set the panel off from the summary under it
	}
	// Back up over the last frame and draw this one in its place, clearing each
	// line's tail and anything left below. The cursor ends at the start of the
	// line under the frame, so a ^C echoed there is cleared by the next draw.
	var b strings.Builder
	if o.shown > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", o.shown)
	}
	b.WriteString("\r")
	for _, l := range frame {
		b.WriteString(l.render(o.color, width))
		b.WriteString("\x1b[K\n")
	}
	b.WriteString("\x1b[J")
	o.shown = len(frame)
	io.WriteString(o.w, b.String()) // one Write, so a frame never tears
}

// progressFigures are a reading's headline numbers.
type progressFigures struct {
	done, total, left, pct int
	rate                   float64 // URLs/s averaged over this run
	eta                    string
}

func (o *progressBarOutput) figures(r progressReading) progressFigures {
	s := r.snap
	f := progressFigures{done: s.Total}
	// What the crawl will process: what it has discovered, unless it stops
	// short at max_urls. The total grows while discovery goes on.
	f.total = s.Discovered
	if s.MaxURLs > 0 && s.MaxURLs < f.total {
		f.total = s.MaxURLs
	}
	if f.total < f.done || r.state == store.StatusCompleted {
		f.total = f.done
	}
	if f.total > 0 {
		f.pct = f.done * 100 / f.total
	}
	f.left = f.total - f.done
	// Rate and ETA over this run so far; the snapshot's own rate is a 4s
	// window, too jumpy to project hours ahead from.
	if s.ElapsedSec > 0 {
		f.rate = float64(f.done-o.base) / float64(s.ElapsedSec)
	}
	f.eta = "--"
	if f.rate > 0 {
		f.eta = shortDuration(time.Duration(float64(f.left) / f.rate * float64(time.Second)))
	}
	return f
}

// line is a reading as one line: the bar, the counts and the status classes
// seen so far.
func (o *progressBarOutput) line(r progressReading) styledLine {
	s := r.snap
	f := o.figures(r)
	l := statusBar(bucketCounts(s), f.done, f.total, lineCells)
	l.add("", fmt.Sprintf(" %4d%%  %s/%s", f.pct, groupDigits(f.done), groupDigits(f.total)))
	elapsed := shortDuration(time.Duration(s.ElapsedSec) * time.Second)
	switch r.state {
	case store.StatusCompleted:
		l.add("", "  done in "+elapsed)
	case store.StatusInterrupted:
		l.add("", "  interrupted after "+elapsed)
	case "finalizing":
		l.add("", "  analysing…")
	default:
		l.add("", fmt.Sprintf("  %s left  %s URLs/s  ETA %s", groupDigits(f.left), formatRate(f.rate), f.eta))
	}
	sep := "  ·"
	for i, n := range bucketCounts(s) {
		if n > 0 {
			l.add("", sep+"  ")
			l.add(buckets[i].color, buckets[i].short)
			l.add("", " "+groupDigits(n))
			sep = ""
		}
	}
	return l
}

// panel is a reading as the terminal panel, fitted to width columns.
func (o *progressBarOutput) panel(r progressReading, recent recentPages, width int) []styledLine {
	s := r.snap
	f := o.figures(r)
	cells := min(width-6, panelCells)
	full := cells + 6 // the bar and its percentage

	word, st := "crawling", bold
	switch r.state {
	case "finalizing":
		word = "analysing"
	case store.StatusCompleted:
		word, st = "done", boldGreen
	case store.StatusInterrupted:
		word, st = "interrupted", boldYellow
	}
	elapsed := shortDuration(time.Duration(s.ElapsedSec) * time.Second)
	var head styledLine
	head.add(st, word)
	head.add("", " "+ellipsize(s.Seed, full-utf8.RuneCountInString(word)-len(elapsed)-3))
	head.add("", strings.Repeat(" ", max(full-head.width()-len(elapsed), 2)))
	head.add(dim, elapsed)

	bar := statusBar(bucketCounts(s), f.done, f.total, cells)
	bar.add("", fmt.Sprintf("  %3d%%", f.pct))

	// While the crawl runs, its rate is the last minute's (below); the ETA
	// comes from the average over the run.
	var figs styledLine
	switch r.state {
	case "finalizing", store.StatusCompleted:
		figs.add("", fmt.Sprintf("%s URLs · %s URLs/s avg", groupDigits(f.done), formatRate(f.rate)))
	case store.StatusInterrupted:
		figs.add("", fmt.Sprintf("%s of %s URLs · %s left", groupDigits(f.done), groupDigits(f.total), groupDigits(f.left)))
	default:
		figs.add("", fmt.Sprintf("%s of %s URLs · %s left · ETA %s",
			groupDigits(f.done), groupDigits(f.total), groupDigits(f.left), f.eta))
	}

	frame := []styledLine{head, bar, figs, nil}
	frame = append(frame, statusRows(s, recent.lastErr, width)...)
	var tail []styledLine
	if r.state == "running" {
		tail = append(tail, recent.line())
	}
	if line := egressSummary(s.Egress); line != "" {
		var eg styledLine
		eg.add("", line)
		tail = append(tail, eg)
	}
	if s.SiteChecksState != "" {
		var sc styledLine
		sc.add("", fmt.Sprintf("site checks  %s · %d checks run · %d findings", s.SiteChecksState, s.SiteChecksRan, s.SiteChecksFindings))
		tail = append(tail, sc)
	}
	if len(tail) > 0 {
		frame = append(append(frame, nil), tail...)
	}
	return frame
}

// statusRows is the panel's legend: a row per bucket with its count, its share
// of the pages processed, and what is in it — the status codes, for a class.
func statusRows(s runner.Snapshot, lastErr string, width int) []styledLine {
	counts := bucketCounts(s)
	digits := 1
	for _, n := range counts {
		digits = max(digits, len(groupDigits(n)))
	}
	codes := codesByBucket(s.StatusCodes)
	rows := make([]styledLine, nBuckets)
	for i, n := range counts {
		b := buckets[i]
		marker, text := b.color, sgr("")
		if n == 0 {
			marker, text = dim, dim
		}
		var l styledLine
		l.add(marker, "■ ")
		l.add(text, fmt.Sprintf("%-11s  %*s  %5s", b.label, digits, groupDigits(n), share(n, s.Total)))
		room := width - l.width() - 2
		switch {
		case i < bBlocked:
			if list := fitList(codes[i], room); list != "" {
				l.add("", "  "+list)
			}
		case i == bBlocked && n > 0:
			l.add(dim, "  by robots.txt")
		case i == bNoResponse && n > 0 && lastErr != "":
			l.add(dim, "  latest: "+lastErr)
		}
		rows[i] = l
	}
	return rows
}

// codesByBucket lists each class's status codes as "404 ×110" items, most
// frequent first.
func codesByBucket(codes map[int]int) [bBlocked][]string {
	type codeCount struct{ code, n int }
	var byBucket [bBlocked][]codeCount
	for code, n := range codes {
		if n > 0 && code >= 200 {
			i := codeBucket(code)
			byBucket[i] = append(byBucket[i], codeCount{code, n})
		}
	}
	var out [bBlocked][]string
	for i, cs := range byBucket {
		slices.SortFunc(cs, func(a, b codeCount) int {
			return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.code, b.code))
		})
		for _, c := range cs {
			out[i] = append(out[i], fmt.Sprintf("%d ×%s", c.code, groupDigits(c.n)))
		}
	}
	return out
}

// fitList joins items with " · " in at most width columns, ending in "+N more"
// when not all of them fit.
func fitList(items []string, width int) string {
	var b strings.Builder
	for i, it := range items {
		sep := ""
		if i > 0 {
			sep = " · "
		}
		more := ""
		if i < len(items)-1 {
			more = fmt.Sprintf(" · +%d more", len(items)-1-i)
		}
		// the next item must leave room to say what did not fit after it
		if utf8.RuneCountInString(b.String()+sep+it+more) > width {
			if i == 0 {
				return ""
			}
			return b.String() + fmt.Sprintf(" · +%d more", len(items)-i)
		}
		b.WriteString(sep + it)
	}
	return b.String()
}

// share is n as a percentage of total: "91.6%", "100%", "<0.1%"; blank for
// nothing.
func share(n, total int) string {
	if n == 0 || total == 0 {
		return ""
	}
	p := float64(n) * 100 / float64(total)
	switch {
	case n == total:
		return "100%"
	case p < 0.05:
		return "<0.1%"
	}
	return strconv.FormatFloat(p, 'f', 1, 64) + "%"
}

// ellipsize cuts s to n columns, marking the cut with "…".
func ellipsize(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string([]rune(s)[:n-1]) + "…"
}

// The six buckets of the live status breakdown, in the order the bar and its
// rows draw them. Every page lands in exactly one, so they sum to processed.
const (
	b2xx = iota
	b3xx
	b4xx
	b5xx
	bBlocked
	bNoResponse
	nBuckets
)

var buckets = [nBuckets]struct {
	label string // the panel's row
	short string // the one-line form's
	color sgr
}{
	{"2xx", "2xx", green},
	{"3xx", "3xx", cyan},
	{"4xx", "4xx", yellow},
	{"5xx", "5xx", red},
	{"blocked", "blocked", blue},
	{"no response", "no-response", magenta},
}

func bucketCounts(s runner.Snapshot) [nBuckets]int {
	return [nBuckets]int{s.S2xx, s.S3xx, s.S4xx, s.S5xx, s.Blocked, s.NoResponse}
}

// codeBucket is the class of a status code of 200 or more; 600 and up count
// as 5xx, as they do in the snapshot.
func codeBucket(code int) int {
	switch {
	case code >= 500:
		return b5xx
	case code >= 400:
		return b4xx
	case code >= 300:
		return b3xx
	}
	return b2xx
}

// pageBucket is the bucket a page counts in, by the runner's rule (run.onPage):
// robots-blocked and errored pages by state, the rest by status class, and
// below 200 as no response.
func pageBucket(rec *crawler.PageRecord) int {
	switch {
	case rec.State == crawler.StateBlockedRobots:
		return bBlocked
	case rec.State == crawler.StateError, rec.StatusCode < 200:
		return bNoResponse
	}
	return codeBucket(rec.StatusCode)
}

// statusBar draws a bar of cells cells with done/total of it filled, the
// filled part split among the buckets by count. A bucket with any pages gets a
// cell while there are cells to give, so four 5xx among thousands of 200s
// still show.
func statusBar(counts [nBuckets]int, done, total, cells int) styledLine {
	filled := 0
	if total > 0 {
		filled = min(done*cells/total, cells)
	}
	split := splitCells(counts, filled)
	var l styledLine
	for i, n := range split {
		if n > 0 {
			l.add(buckets[i].color, strings.Repeat("█", n))
		}
	}
	l.add(dim, strings.Repeat("░", cells-filled))
	return l
}

// splitCells shares cells among the buckets in proportion to counts (largest
// remainder), then moves a cell to each empty-handed bucket that has pages
// from the bucket holding the most.
func splitCells(counts [nBuckets]int, cells int) [nBuckets]int {
	var split [nBuckets]int
	sum := 0
	for _, n := range counts {
		sum += n
	}
	if sum == 0 || cells == 0 {
		return split
	}
	var rem [nBuckets]int
	given := 0
	for i, n := range counts {
		split[i] = n * cells / sum
		rem[i] = n * cells % sum
		given += split[i]
	}
	for ; given < cells; given++ {
		top := 0
		for i := range rem {
			if rem[i] > rem[top] {
				top = i
			}
		}
		split[top]++
		rem[top] = -1
	}
	for i, n := range counts {
		if n == 0 || split[i] > 0 {
			continue
		}
		top := 0
		for j := range split {
			if split[j] > split[top] {
				top = j
			}
		}
		if split[top] < 2 {
			break
		}
		split[top]--
		split[i]++
	}
	return split
}

// pageStats keeps what the executor's snapshot does not: this run's pages
// second by second over the last minute (their buckets, response times and
// sizes) and the latest fetch error. The CLI observer feeds it from the
// crawl's goroutines; the bar reads it once per frame.
type pageStats struct {
	mu      sync.Mutex
	secs    [60]pageSecond // a ring indexed by unix second
	start   time.Time      // the first read: the window never reaches before it
	lastErr string
}

type pageSecond struct {
	at       int64 // the unix second this slot holds
	pages    int
	buckets  [nBuckets]int
	answered int   // pages with a status class, which the sums below cover
	ms       int64 // their response times
	bytes    int64 // their body sizes
}

func (p *pageStats) page(rec *crawler.PageRecord) { p.add(rec, time.Now()) }

func (p *pageStats) add(rec *crawler.PageRecord, at time.Time) {
	b := pageBucket(rec)
	sec := at.Unix()
	p.mu.Lock()
	defer p.mu.Unlock()
	slot := &p.secs[sec%int64(len(p.secs))]
	if slot.at != sec {
		*slot = pageSecond{at: sec}
	}
	slot.pages++
	slot.buckets[b]++
	if b < bBlocked {
		slot.answered++
		slot.ms += rec.ResponseTimeMs
		slot.bytes += int64(rec.Size)
	}
	if rec.State == crawler.StateError && rec.FetchError != "" {
		p.lastErr = fetchErrorText(rec.FetchError)
	}
}

// recentPages is this run's last minute of pages, or the time since the crawl
// started when that is shorter.
type recentPages struct {
	span     time.Duration
	pages    int
	buckets  [nBuckets]int
	answered int
	ms       int64
	bytes    int64
	lastErr  string
}

func (p *pageStats) read(now time.Time) recentPages {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.start.IsZero() {
		p.start = now
	}
	r := recentPages{span: min(now.Sub(p.start), time.Minute), lastErr: p.lastErr}
	cutoff := now.Unix() - int64(len(p.secs))
	for _, s := range p.secs {
		if s.at <= cutoff || s.at > now.Unix() {
			continue
		}
		r.pages += s.pages
		r.answered += s.answered
		r.ms += s.ms
		r.bytes += s.bytes
		for i, n := range s.buckets {
			r.buckets[i] += n
		}
	}
	return r
}

// line is the panel's last-minute line: the split of the minute's pages, how
// many, how fast, and how long and how big the average answer was.
func (r recentPages) line() styledLine {
	var l styledLine
	l.add("", "last minute  ")
	l = append(l, statusBar(r.buckets, r.pages, r.pages, mixCells)...)
	if r.pages == 0 {
		st := dim
		if r.span >= time.Minute {
			st = yellow // a whole minute without a page: stalled, or all but
		}
		l.add(st, "  no URLs processed")
		return l
	}
	rate := float64(r.pages) / max(r.span.Seconds(), 1)
	l.add("", fmt.Sprintf("  %s URLs · %s/s", groupDigits(r.pages), formatRate(rate)))
	if r.answered > 0 {
		n := int64(r.answered)
		l.add("", fmt.Sprintf(" · %s avg · %s avg", formatMillis(r.ms/n), formatBytes(r.bytes/n)))
	}
	return l
}

// urlErrPrefix is the `Get "https://…": ` a Go client error (*url.Error, the
// URL %q-quoted) opens with; the URL is the page's own, so only the cause
// after it is news.
var urlErrPrefix = regexp.MustCompile(`^[A-Za-z]+ "(?:[^"\\]|\\.)*": `)

func fetchErrorText(e string) string {
	e = urlErrPrefix.ReplaceAllString(e, "")
	if i := strings.IndexByte(e, '\n'); i >= 0 {
		e = e[:i]
	}
	return e
}

// sgr is an ANSI text style (Select Graphic Rendition parameters); "" is plain.
type sgr string

const (
	bold       sgr = "1"
	dim        sgr = "2"
	red        sgr = "31"
	green      sgr = "32"
	yellow     sgr = "33"
	blue       sgr = "34"
	magenta    sgr = "35"
	cyan       sgr = "36"
	boldGreen  sgr = "1;32"
	boldYellow sgr = "1;33"
)

// styledLine is a line as runs of styled text, so it can be measured and cut
// to the terminal's width before any escape is written.
type styledLine []styledSpan

type styledSpan struct {
	st   sgr
	text string
}

func (l *styledLine) add(st sgr, text string) { *l = append(*l, styledSpan{st, text}) }

// width is the line's length in columns (every rune it draws is one column).
func (l styledLine) width() int {
	n := 0
	for _, s := range l {
		n += utf8.RuneCountInString(s.text)
	}
	return n
}

// render writes the line, styled when color is set, cut to max columns (0:
// uncut).
func (l styledLine) render(color bool, max int) string {
	var b strings.Builder
	left := max
	for _, s := range l {
		text := s.text
		if max > 0 {
			if left == 0 {
				break
			}
			if r := []rune(text); len(r) > left {
				text = string(r[:left])
			}
			left -= utf8.RuneCountInString(text)
		}
		if color && s.st != "" && text != "" {
			b.WriteString("\x1b[" + string(s.st) + "m" + text + "\x1b[0m")
		} else {
			b.WriteString(text)
		}
	}
	return b.String()
}

// formatRate keeps a slow rendering crawl's rate readable (0.4, not 0) and a
// fast one's short (212, not 212.4).
func formatRate(r float64) string {
	if r < 10 {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	return strconv.FormatFloat(r, 'f', 0, 64)
}

// formatMillis is a response time: "412 ms", "2.4 s".
func formatMillis(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64) + " s"
}

// formatBytes is a size in binary units: "512 B", "48 KB", "1.2 MB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, u := float64(n)/unit, 0
	for v >= unit && u < 2 {
		v /= unit
		u++
	}
	prec := 0
	if v < 10 {
		prec = 1
	}
	return strconv.FormatFloat(v, 'f', prec, 64) + " " + [...]string{"KB", "MB", "GB"}[u]
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
