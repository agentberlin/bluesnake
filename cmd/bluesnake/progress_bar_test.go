package main

// Tests for the --progress bar output: the one-line form written off a
// terminal, the panel redrawn on one, and the pieces they are drawn from.

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// midCrawl is a crawl 12 minutes in, with pages in every bucket.
func midCrawl() runner.Snapshot {
	return runner.Snapshot{
		Seed: "https://www.example.com/", Total: 3712, Discovered: 11879, MaxURLs: 5000000, ElapsedSec: 724,
		S2xx: 3402, S3xx: 180, S4xx: 118, S5xx: 4, Blocked: 6, NoResponse: 2,
		StatusCodes: map[int]int{200: 3390, 204: 12, 301: 150, 302: 30, 404: 110, 403: 8, 503: 4},
	}
}

// busyMinute is a last minute of 212 pages, a few of them not 2xx.
var busyMinute = recentPages{
	span: time.Minute, pages: 212, buckets: [nBuckets]int{198, 6, 7, 1, 0, 0},
	answered: 212, ms: 412 * 212, bytes: 48 * 1024 * 212,
	lastErr: "dial tcp 93.184.216.34:443: i/o timeout",
}

// TestProgressBarLine pins the one-line form (a file, a pipe, a small
// terminal) at each point of a crawl.
func TestProgressBarLine(t *testing.T) {
	running := func(total, discovered, maxURLs, elapsed int) progressReading {
		return progressReading{state: "running", snap: runner.Snapshot{
			Total: total, S2xx: total, Discovered: discovered, MaxURLs: maxURLs, ElapsedSec: elapsed}}
	}
	mixed := running(2480, 11879, 5000000, 775)
	mixed.snap.S2xx, mixed.snap.S4xx, mixed.snap.NoResponse = 2400, 70, 10
	cases := []struct {
		name     string
		readings []progressReading // the last one's line is checked
		want     string
	}{
		{"just started", []progressReading{running(0, 1, 5000000, 0)},
			"░░░░░░░░░░░░░░░░░░░░    0%  0/1  1 left  0.0 URLs/s  ETA --"},
		{"mid-crawl", []progressReading{running(0, 1, 5000000, 0), mixed},
			"████░░░░░░░░░░░░░░░░   20%  2,480/11,879  9,399 left  3.2 URLs/s  ETA 48m57s  ·  2xx 2,400  4xx 70  no-response 10"},
		{"capped by max_urls", []progressReading{running(0, 1, 30, 0), running(10, 11879, 30, 5)},
			"██████░░░░░░░░░░░░░░   33%  10/30  20 left  2.0 URLs/s  ETA 10s  ·  2xx 10"},
		// a resume starts at the earlier sessions' count; only this run's
		// pages count toward the rate
		{"resumed", []progressReading{running(352, 11879, 5000000, 0), running(452, 11879, 5000000, 100)},
			"░░░░░░░░░░░░░░░░░░░░    3%  452/11,879  11,427 left  1.0 URLs/s  ETA 3h10m  ·  2xx 452"},
		{"finalizing", []progressReading{{state: "finalizing", snap: runner.Snapshot{Total: 30, S2xx: 30, Discovered: 30, MaxURLs: 30, ElapsedSec: 70}}},
			"████████████████████  100%  30/30  analysing…  ·  2xx 30"},
		{"completed short of discovered", []progressReading{{state: store.StatusCompleted, final: true,
			snap: runner.Snapshot{Total: 30, S2xx: 28, S5xx: 2, Discovered: 11879, MaxURLs: 5000000, ElapsedSec: 27660}}},
			"████████████████████  100%  30/30  done in 7h41m  ·  2xx 28  5xx 2"},
		{"interrupted", []progressReading{{state: store.StatusInterrupted, final: true,
			snap: runner.Snapshot{Total: 352, S2xx: 352, Discovered: 11879, MaxURLs: 5000000, ElapsedSec: 720}}},
			"░░░░░░░░░░░░░░░░░░░░    2%  352/11,879  interrupted after 12m00s  ·  2xx 352"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		o := &progressBarOutput{w: &buf}
		for _, r := range c.readings {
			o.write(r)
		}
		if strings.Contains(buf.String(), "\x1b") || strings.Contains(buf.String(), "\r") {
			t.Errorf("%s: off a terminal the line carries an escape or carriage return: %q", c.name, buf.String())
		}
		lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		if len(lines) != len(c.readings) {
			t.Errorf("%s: %d readings wrote %d lines", c.name, len(c.readings), len(lines))
		}
		if got := lines[len(lines)-1]; got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// TestProgressBarPanel pins the terminal panel's text at each point of a
// crawl. The bar's filled cells are split by bucket, every non-empty bucket
// getting one (without colour they all draw as █).
func TestProgressBarPanel(t *testing.T) {
	rows := []string{
		"",
		"■ 2xx          3,402  91.6%  200 ×3,390 · 204 ×12",
		"■ 3xx            180   4.8%  301 ×150 · 302 ×30",
		"■ 4xx            118   3.2%  404 ×110 · 403 ×8",
		"■ 5xx              4   0.1%  503 ×4",
		"■ blocked          6   0.2%  by robots.txt",
		"■ no response      2   0.1%  latest: dial tcp 93.184.216.34:443: i/o timeout",
	}
	bar := func(filled int, pct string) string {
		return strings.Repeat("█", filled) + strings.Repeat("░", 60-filled) + pct
	}
	cases := []struct {
		state string
		want  []string
	}{
		{"running", append(append([]string{
			"crawling https://www.example.com/                           12m04s",
			bar(18, "   31%"),
			"3,712 of 11,879 URLs · 8,167 left · ETA 42m29s",
		}, rows...),
			"",
			"last minute  ████████████  212 URLs · 3.5/s · 412 ms avg · 48 KB avg",
		)},
		{"finalizing", append([]string{
			"analysing https://www.example.com/                          12m04s",
			bar(18, "   31%"),
			"3,712 URLs · 3.2 URLs/s avg",
		}, rows...)},
		{store.StatusCompleted, append([]string{
			"done https://www.example.com/                               12m04s",
			bar(60, "  100%"),
			"3,712 URLs · 3.2 URLs/s avg",
		}, rows...)},
		{store.StatusInterrupted, append([]string{
			"interrupted https://www.example.com/                        12m04s",
			bar(18, "   31%"),
			"3,712 of 11,879 URLs · 8,167 left",
		}, rows...)},
	}
	for _, c := range cases {
		o := &progressBarOutput{started: true, base: 1392} // 2,320 pages this run: 3.2/s
		var got []string
		for _, l := range o.panel(progressReading{snap: midCrawl(), state: c.state}, busyMinute, 79) {
			got = append(got, l.render(false, 79))
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s panel:\n%s\nwant:\n%s", c.state, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// TestProgressBarPanelEdges: a long seed is cut to the panel, a narrow
// terminal narrows the bar and cuts the code list with a count of the rest,
// and the site-check pass gets a line while it is part of the crawl.
func TestProgressBarPanelEdges(t *testing.T) {
	s := midCrawl()
	s.Seed = "https://www.example.com/" + strings.Repeat("very-long-path/", 10)
	s.StatusCodes = map[int]int{200: 3000, 201: 100, 202: 100, 203: 100, 204: 102, 301: 180, 404: 118, 503: 4}
	s.SiteChecksState, s.SiteChecksRan, s.SiteChecksFindings = "running", 3, 2
	o := &progressBarOutput{started: true}
	var got []string
	for _, l := range o.panel(progressReading{snap: s, state: "running"}, recentPages{}, 60) {
		got = append(got, l.render(false, 60))
	}
	checks := []struct {
		line int
		want string
	}{
		{0, "crawling https://www.example.com/very-long-path/ver…  12m04s"},
		{1, strings.Repeat("█", 16) + strings.Repeat("░", 38) + "   31%"},
		{4, "■ 2xx          3,402  91.6%  200 ×3,000 · 204 ×102 · +3 more"},
		{11, "last minute  ░░░░░░░░░░░░  no URLs processed"},
		{12, "site checks  running · 3 checks run · 2 findings"},
	}
	for _, c := range checks {
		if c.line >= len(got) || got[c.line] != c.want {
			t.Errorf("line %d:\n got %q\nwant %q\npanel:\n%s", c.line, at(got, c.line), c.want, strings.Join(got, "\n"))
		}
	}
	for i, l := range got {
		if n := utf8.RuneCountInString(l); n > 60 {
			t.Errorf("line %d is %d columns, wider than the terminal's 60: %q", i, n, l)
		}
	}
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestProgressBarRedraw: on a terminal each frame backs up over the last one,
// clears what it leaves behind, and ends at the start of the line below it;
// the bar's buckets are coloured only when colour is on; and a terminal too
// small for the panel gets the one-line form, drawn the same way.
func TestProgressBarRedraw(t *testing.T) {
	size := func(cols, rows int) func() (int, int) { return func() (int, int) { return cols, rows } }
	running := progressReading{state: "running", snap: midCrawl()}
	done := progressReading{state: store.StatusCompleted, final: true, snap: midCrawl()}

	var buf bytes.Buffer
	o := &progressBarOutput{w: &buf, redraw: true, color: true, size: size(100, 40), pages: &pageStats{}}
	o.write(running)
	first := buf.String()
	o.write(done)
	second := strings.TrimPrefix(buf.String(), first)

	if !strings.HasPrefix(first, "\r") || !strings.HasSuffix(first, "\x1b[K\n\x1b[J") {
		t.Errorf("first frame = %q, want \\r, lines ending in \\x1b[K\\n, then \\x1b[J", first)
	}
	shown := strings.Count(first, "\n")
	if want := fmt.Sprintf("\x1b[%dA\r", shown); !strings.HasPrefix(second, want) {
		t.Errorf("second frame starts %q, want it to back up over the %d lines shown: %q", second[:min(len(second), 12)], shown, want)
	}
	// the final frame keeps a blank line between it and the summary
	if !strings.HasSuffix(second, "\n\x1b[K\n\x1b[J") {
		t.Errorf("final frame does not end on a blank line: %q", second[max(len(second)-40, 0):])
	}
	for _, c := range []sgr{green, cyan, yellow, red, blue, magenta} {
		if !strings.Contains(first, "\x1b["+string(c)+"m█") {
			t.Errorf("no %s-coloured bar cells in the frame", c)
		}
	}
	for i, l := range strings.Split(sgrRe.ReplaceAllString(first, ""), "\n") {
		l = strings.TrimSuffix(strings.TrimPrefix(l, "\r"), "\x1b[K")
		if n := utf8.RuneCountInString(l); n > 99 {
			t.Errorf("line %d is %d columns on a 100-column terminal: %q", i, n, l)
		}
	}

	buf.Reset()
	o = &progressBarOutput{w: &buf, redraw: true, size: size(100, 40)}
	o.write(running)
	if sgrRe.MatchString(buf.String()) {
		t.Errorf("NO_COLOR frame carries colour: %q", buf.String())
	}

	for _, small := range []struct{ cols, rows int }{{100, 8}, {30, 40}} {
		buf.Reset()
		o = &progressBarOutput{w: &buf, redraw: true, size: size(small.cols, small.rows)}
		o.write(running)
		o.write(done)
		frames := strings.Split(buf.String(), "\x1b[1A\r")
		if len(frames) != 2 || strings.Count(buf.String(), "\n") != 2 || !strings.HasPrefix(frames[1], "█") {
			t.Errorf("%dx%d terminal: want two one-line frames, got %q", small.cols, small.rows, buf.String())
		}
	}
}

// TestSplitCells: the filled cells are shared by count, always add up to the
// cells given, and a bucket with any pages shows while a larger one can spare
// a cell.
func TestSplitCells(t *testing.T) {
	cases := []struct {
		counts [nBuckets]int
		cells  int
		want   [nBuckets]int
	}{
		{[nBuckets]int{50, 25, 25}, 20, [nBuckets]int{10, 5, 5}},
		{[nBuckets]int{996, 0, 0, 4}, 60, [nBuckets]int{59, 0, 0, 1}},
		{[nBuckets]int{3402, 180, 118, 4, 6, 2}, 18, [nBuckets]int{13, 1, 1, 1, 1, 1}},
		{[nBuckets]int{1, 1, 1}, 2, [nBuckets]int{1, 1, 0}}, // no cell to spare
		{[nBuckets]int{}, 20, [nBuckets]int{}},
		{[nBuckets]int{5}, 0, [nBuckets]int{}},
	}
	for _, c := range cases {
		got := splitCells(c.counts, c.cells)
		if got != c.want {
			t.Errorf("splitCells(%v, %d) = %v, want %v", c.counts, c.cells, got, c.want)
		}
		sum := 0
		for _, n := range got {
			sum += n
		}
		if c.counts != ([nBuckets]int{}) && sum != c.cells {
			t.Errorf("splitCells(%v, %d) hands out %d cells", c.counts, c.cells, sum)
		}
	}
}

// TestPageStats: the last minute counts only the pages of the last 60
// seconds (never before the first reading), averages over the answered ones,
// and keeps the latest fetch error without the URL it opens with.
func TestPageStats(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	p := &pageStats{}
	p.read(t0) // the crawl starts
	page := func(state string, code int, ms int64, size int) *crawler.PageRecord {
		return &crawler.PageRecord{State: state, StatusCode: code, ResponseTimeMs: ms, Size: size}
	}
	p.add(page(crawler.StateCrawled, 200, 100, 1000), t0.Add(time.Second))
	p.add(page(crawler.StateCrawled, 404, 300, 3000), t0.Add(30*time.Second))
	p.add(&crawler.PageRecord{State: crawler.StateError, FetchError: `Get "https://ex.com/a": dial tcp: lookup ex.com: no such host`}, t0.Add(40*time.Second))
	p.add(page(crawler.StateBlockedRobots, 0, 0, 0), t0.Add(40*time.Second))

	r := p.read(t0.Add(45 * time.Second))
	want := recentPages{span: 45 * time.Second, pages: 4, buckets: [nBuckets]int{1, 0, 1, 0, 1, 1},
		answered: 2, ms: 400, bytes: 4000, lastErr: "dial tcp: lookup ex.com: no such host"}
	if r != want {
		t.Errorf("at 45s: %+v\nwant %+v", r, want)
	}
	if got := r.line().render(false, 0); got != "last minute  ████████████  4 URLs · 0.1/s · 200 ms avg · 2.0 KB avg" {
		t.Errorf("at 45s the line = %q", got)
	}

	// the page at 1s has left the window; the one at 61s reuses its slot
	p.add(page(crawler.StateCrawled, 301, 50, 500), t0.Add(61*time.Second))
	if r := p.read(t0.Add(75 * time.Second)); r.pages != 4 || r.buckets != ([nBuckets]int{0, 1, 1, 0, 1, 1}) || r.span != time.Minute {
		t.Errorf("at 75s: %+v, want the pages from 30s to 61s over a full minute", r)
	}

	// a whole minute without a page reads as a stall
	r = p.read(t0.Add(200 * time.Second))
	if r.pages != 0 {
		t.Errorf("at 200s: %d pages in the last minute, want none", r.pages)
	}
	if got := r.line().render(true, 0); !strings.Contains(got, "\x1b[33m  no URLs processed") {
		t.Errorf("a minute without pages is not flagged: %q", got)
	}
}

func TestProgressBarHelpers(t *testing.T) {
	for _, c := range []struct {
		n, total int
		want     string
	}{
		{0, 10, ""}, {10, 10, "100%"}, {1, 3, "33.3%"}, {4, 3712, "0.1%"}, {1, 100000, "<0.1%"},
	} {
		if got := share(c.n, c.total); got != c.want {
			t.Errorf("share(%d, %d) = %q, want %q", c.n, c.total, got, c.want)
		}
	}
	items := []string{"200 ×3,000", "204 ×102", "201 ×100", "202 ×100"}
	for width, want := range map[int]string{
		100: "200 ×3,000 · 204 ×102 · 201 ×100 · 202 ×100",
		40:  "200 ×3,000 · 204 ×102 · +2 more",
		25:  "200 ×3,000 · +3 more",
		10:  "",
	} {
		if got := fitList(items, width); got != want {
			t.Errorf("fitList(width %d) = %q, want %q", width, got, want)
		}
	}
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 49152: "48 KB", 1288490: "1.2 MB", 5 << 30: "5.0 GB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
	for ms, want := range map[int64]string{0: "0 ms", 412: "412 ms", 2400: "2.4 s"} {
		if got := formatMillis(ms); got != want {
			t.Errorf("formatMillis(%d) = %q, want %q", ms, got, want)
		}
	}
	for in, want := range map[string]string{
		`Get "https://ex.com/a?b=\"c\"": context deadline exceeded`: `context deadline exceeded`,
		"EOF":            "EOF",
		"tls: bad\nmore": "tls: bad",
	} {
		if got := fetchErrorText(in); got != want {
			t.Errorf("fetchErrorText(%q) = %q, want %q", in, got, want)
		}
	}
	codes := codesByBucket(map[int]int{404: 3, 200: 9, 410: 3, 999: 1, 301: 0})
	if got := fmt.Sprint(codes); got != "[[200 ×9] [] [404 ×3 410 ×3] [999 ×1]]" {
		t.Errorf("codesByBucket = %s", got)
	}
	for _, c := range []struct {
		rec  crawler.PageRecord
		want int
	}{
		{crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 200}, b2xx},
		{crawler.PageRecord{State: crawler.StateSkippedTooLarge, StatusCode: 302}, b3xx},
		{crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 999}, b5xx},
		{crawler.PageRecord{State: crawler.StateBlockedRobots, StatusCode: 200}, bBlocked},
		{crawler.PageRecord{State: crawler.StateError, StatusCode: 500}, bNoResponse},
		{crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 101}, bNoResponse},
	} {
		if got := pageBucket(&c.rec); got != c.want {
			t.Errorf("pageBucket(%s %d) = %d, want %d", c.rec.State, c.rec.StatusCode, got, c.want)
		}
	}
}

func TestGroupDigits(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 11879: "11,879", 5000000: "5,000,000", -1234: "-1,234"} {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0s", 45 * time.Second: "45s", 192 * time.Second: "3m12s",
		7*time.Hour + 41*time.Minute + 20*time.Second: "7h41m", 53 * time.Hour: "2d5h",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
