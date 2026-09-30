package main

// Tests for --progress json on crawl/list/resume. Records are decoded into
// generic maps and read by their wire names, so a renamed field fails here the
// way it would fail a consumer's parser.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// syncBuffer is a bytes.Buffer safe to read while the progress feed writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lagWriter holds each progress record back before it reaches the buffer.
// That widens the window in which a command could print, return or (as a
// process) exit ahead of its feed's final record, so a missing wait fails the
// tests instead of hiding in a microsecond race.
type lagWriter struct{ *syncBuffer }

func (w lagWriter) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte("{")) {
		time.Sleep(30 * time.Millisecond)
	}
	return w.syncBuffer.Write(p)
}

// runSplit runs the root command with stdout and stderr captured apart, the
// way a headless consumer reading two pipes sees them. stderr lags its
// progress records (lagWriter) and can be read while the crawl runs.
func runSplit(ctx context.Context, stderr *syncBuffer, args ...string) (string, int) {
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(lagWriter{stderr})
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return stdout.String(), 0
	}
	var ee exitErr
	if errors.As(err, &ee) {
		return stdout.String(), ee.code
	}
	return stdout.String(), 1
}

func runSplitT(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var eb syncBuffer
	stdout, code = runSplit(context.Background(), &eb, args...)
	return stdout, eb.String(), code
}

// progressRecords splits stderr into decoded progress records and the other
// (plain-text) lines. Every record must be one newline-terminated JSON object
// of type "progress"; stderr must never carry a carriage return.
func progressRecords(t *testing.T, stderr string) (recs []map[string]any, other []string) {
	t.Helper()
	if strings.Contains(stderr, "\r") {
		t.Errorf("stderr contains a carriage return:\n%q", stderr)
	}
	if stderr != "" && !strings.HasSuffix(stderr, "\n") {
		t.Errorf("stderr does not end with a newline:\n%q", stderr)
	}
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if !strings.HasPrefix(line, "{") {
			if line != "" {
				other = append(other, line)
			}
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("malformed progress line %q: %v", line, err)
		}
		if rec["type"] != "progress" {
			t.Fatalf("JSON line without type progress: %s", line)
		}
		recs = append(recs, rec)
	}
	return recs, other
}

// num reads a numeric field by its wire name, failing when it is absent.
func num(t *testing.T, rec map[string]any, key string) int {
	t.Helper()
	v, ok := rec[key].(float64)
	if !ok {
		t.Fatalf("record has no numeric %q: %v", key, rec)
	}
	return int(v)
}

// progressSite serves "/" linking to /a and /b; /b answers after delay, so a
// crawl spans at least one progress interval.
func progressSite(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/a">a</a> <a href="/b">b</a></body></html>`)
		case "/a", "/b":
			if r.URL.Path == "/b" {
				time.Sleep(delay)
			}
			fmt.Fprint(w, `<html><head><title>Leaf</title></head><body><p>leaf</p></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var (
	crawlIDRe   = regexp.MustCompile(`\d{8}-\d{6}-[0-9a-f]{6}`)
	durationRe  = regexp.MustCompile(`crawled in \S+`)
	crawlIDLine = regexp.MustCompile(`(?m)^Crawl ID: (\S+)$`)
)

// normalizeRun blanks the per-run crawl id and duration, leaving what must be
// identical between two crawls of the same site.
func normalizeRun(s string) string {
	return durationRe.ReplaceAllString(crawlIDRe.ReplaceAllString(s, "<id>"), "crawled in <d>")
}

// TestCrawlProgressJSON pins the headless-progress contract end to end: with
// --progress json, stderr carries only progress records (a start record, one
// per interval while the crawl runs, a final record with the terminal state)
// and stdout is what it is without the flag.
func TestCrawlProgressJSON(t *testing.T) {
	srv := progressSite(t, 1500*time.Millisecond)

	plainOut, plainErr, code := runSplitT(t, "crawl", srv.URL+"/", "--store-dir", t.TempDir(), "--setup", "defaults")
	if code != 0 {
		t.Fatalf("plain crawl: exit %d\n%s%s", code, plainOut, plainErr)
	}
	if plainErr != "" {
		t.Errorf("a crawl without --progress wrote to stderr:\n%s", plainErr)
	}

	out, stderr, code := runSplitT(t, "crawl", srv.URL+"/", "--store-dir", t.TempDir(), "--setup", "defaults",
		"--progress", "json", "--progress-interval", "1s")
	if code != 0 {
		t.Fatalf("progress crawl: exit %d\n%s%s", code, out, stderr)
	}
	if got, want := normalizeRun(out), normalizeRun(plainOut); got != want {
		t.Errorf("stdout changed by --progress json:\n--- with\n%s--- without\n%s", got, want)
	}

	recs, other := progressRecords(t, stderr)
	if len(other) != 0 {
		t.Errorf("stderr carries non-progress lines: %q", other)
	}
	if len(recs) < 3 {
		t.Fatalf("got %d progress records, want start + at least one interval + final:\n%s", len(recs), stderr)
	}
	m := crawlIDLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no Crawl ID line on stdout:\n%s", out)
	}
	for i, r := range recs {
		if r["crawl_id"] != m[1] {
			t.Errorf("record %d crawl_id = %v, want the stdout Crawl ID %s", i, r["crawl_id"], m[1])
		}
		if r["seed"] != srv.URL+"/" {
			t.Errorf("record %d seed = %v, want %s/", i, r["seed"], srv.URL)
		}
		if _, err := time.Parse(time.RFC3339, fmt.Sprint(r["time"])); err != nil {
			t.Errorf("record %d time %v is not RFC 3339: %v", i, r["time"], err)
		}
		if i > 0 && num(t, r, "processed") < num(t, recs[i-1], "processed") {
			t.Errorf("processed went backwards at record %d: %d -> %d", i, num(t, recs[i-1], "processed"), num(t, r, "processed"))
		}
	}
	if s := recs[0]["state"]; s != "running" {
		t.Errorf("first record state = %v, want running", s)
	}
	// /b is still in flight when the 1s tick fires, so a mid-crawl record
	// already counts the pages done by then.
	var midCrawl bool
	for _, r := range recs[1 : len(recs)-1] {
		if r["state"] == "running" && num(t, r, "processed") >= 1 {
			midCrawl = true
		}
	}
	if !midCrawl {
		t.Errorf("no mid-crawl running record with processed >= 1:\n%s", stderr)
	}
	last := recs[len(recs)-1]
	if last["state"] != store.StatusCompleted {
		t.Errorf("final record state = %v, want completed", last["state"])
	}
	for key, want := range map[string]int{
		"processed": 3, "discovered": 3, "queued": 0, "status_2xx": 3, "status_3xx": 0,
		"status_4xx": 0, "status_5xx": 0, "blocked_by_robots": 0, "no_response": 0, "indexable": 3,
	} {
		if got := num(t, last, key); got != want {
			t.Errorf("final %s = %d, want %d", key, got, want)
		}
	}
	for _, key := range []string{"elapsed_sec", "urls_per_sec"} {
		num(t, last, key)
	}
	if _, has := last["error"]; has {
		t.Errorf("a clean crawl's final record carries an error: %v", last["error"])
	}
}

// TestCrawlProgressQuiet: --quiet silences the summary, not the feed.
func TestCrawlProgressQuiet(t *testing.T) {
	srv := twoPageSite(t)
	out, stderr, code := runSplitT(t, "crawl", srv.URL+"/", "--store-dir", t.TempDir(), "--quiet", "--progress", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, stderr)
	}
	if out != "" {
		t.Errorf("--quiet stdout = %q, want empty", out)
	}
	recs, other := progressRecords(t, stderr)
	if len(other) != 0 || len(recs) < 2 {
		t.Fatalf("stderr = %d records + %q, want start and final records only:\n%s", len(recs), other, stderr)
	}
	if last := recs[len(recs)-1]; last["state"] != store.StatusCompleted || num(t, last, "processed") != 2 {
		t.Errorf("final record = %v, want completed with 2 processed", last)
	}
}

// TestCrawlProgressInterrupted: a signal mid-crawl ends the feed with an
// interrupted record, then the resume hint as stderr's last line, and exit 3.
func TestCrawlProgressInterrupted(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><body><a href="/hang">hang</a></body></html>`)
		case "/hang":
			select { // held in flight until the crawl is cancelled
			case <-r.Context().Done():
			case <-release:
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr syncBuffer
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := runSplit(ctx, &stderr, "crawl", srv.URL+"/", "--store-dir", dir, "--progress", "json")
		done <- result{out, code}
	}()
	// cancel once the feed shows the crawl under way (the start record)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), `"type":"progress"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no start record within 10s; stderr:\n%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	var res result
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("interrupted crawl did not exit")
	}
	if res.code != 3 {
		t.Errorf("exit = %d, want 3\n%s%s", res.code, res.out, stderr.String())
	}
	recs, other := progressRecords(t, stderr.String())
	if len(recs) < 2 {
		t.Fatalf("got %d records, want start + final:\n%s", len(recs), stderr.String())
	}
	last := recs[len(recs)-1]
	if last["state"] != store.StatusInterrupted {
		t.Errorf("final record state = %v, want interrupted", last["state"])
	}
	// the hint comes after the final record, never before or between records,
	// and nothing follows it
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	hint := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "crawl interrupted — resume with: bluesnake resume "+fmt.Sprint(last["crawl_id"])) {
			hint = i
		}
	}
	if hint < 0 || len(other) != 1 || hint != len(recs) {
		t.Errorf("stderr should end with the final record, then the resume hint alone:\n%s", stderr.String())
	}
}

// TestResumeProgressCoversWholeCrawl: resume streams the same feed, and its
// counters (breakdown included) cover both sessions, like the stdout summary.
func TestResumeProgressCoversWholeCrawl(t *testing.T) {
	srv := leafServer(t)
	dir := t.TempDir()
	id := forgeInterrupted(t, dir, srv) // one 200 page recorded, /next pending

	out, stderr, code := runSplitT(t, "resume", id, "--store-dir", dir, "--progress", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, stderr)
	}
	recs, other := progressRecords(t, stderr)
	if len(other) != 0 || len(recs) < 2 {
		t.Fatalf("stderr = %d records + %q, want start and final records only:\n%s", len(recs), other, stderr)
	}
	first, last := recs[0], recs[len(recs)-1]
	if first["crawl_id"] != id || last["crawl_id"] != id {
		t.Errorf("records name crawl %v/%v, want %s", first["crawl_id"], last["crawl_id"], id)
	}
	if num(t, first, "processed") != 1 || num(t, first, "status_2xx") != 1 {
		t.Errorf("start record = %v, want the first session's 1 processed / 1 2xx", first)
	}
	if last["state"] != store.StatusCompleted || num(t, last, "processed") != 2 || num(t, last, "status_2xx") != 2 {
		t.Errorf("final record = %v, want completed with 2 processed / 2 2xx", last)
	}
	if !strings.Contains(out, "2xx: 2 ") {
		t.Errorf("stdout summary disagrees with the feed:\n%s", out)
	}
}

// TestListProgress: list mode's existing stderr notice stays, the feed follows.
func TestListProgress(t *testing.T) {
	srv := twoPageSite(t)
	dir := t.TempDir()
	listFile := filepath.Join(dir, "urls.txt")
	if err := os.WriteFile(listFile, []byte(srv.URL+"/\n"+srv.URL+"/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runSplitT(t, "list", listFile, "--store-dir", dir, "--progress", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, stderr)
	}
	if !strings.HasPrefix(stderr, "list mode: 2 URLs\n") {
		t.Errorf("list-mode notice moved or missing:\n%s", stderr)
	}
	recs, other := progressRecords(t, stderr)
	if len(other) != 1 || len(recs) < 2 {
		t.Fatalf("stderr = %d records + %q, want the notice then the feed:\n%s", len(recs), other, stderr)
	}
	if last := recs[len(recs)-1]; last["state"] != store.StatusCompleted || num(t, last, "processed") != 2 {
		t.Errorf("final record = %v, want completed with 2 processed", last)
	}
}

// TestProgressFlagValidation: unusable --progress flags are config errors
// (exit 2) on every crawl command, rejected before anything reaches stdout.
func TestProgressFlagValidation(t *testing.T) {
	dir := t.TempDir()
	cases := [][]string{
		{"--progress", "xml"},
		{"--progress", "json", "--progress-interval", "500ms"},
		{"--progress", "bar", "--progress-interval", "500ms"},
		{"--progress-interval", "30s"},
		{"--progress", "none", "--progress-interval", "30s"},
	}
	cmds := map[string][]string{
		"crawl":  {"crawl", "https://e.com/", "--store-dir", dir},
		"list":   {"list", "--sitemap", "https://e.com/sitemap.xml", "--store-dir", dir},
		"resume": {"resume", "no-such-crawl", "--store-dir", dir},
	}
	for name, base := range cmds {
		for _, flags := range cases {
			out, stderr, code := runSplitT(t, append(append([]string{}, base...), flags...)...)
			if code != 2 {
				t.Errorf("%s %v: exit %d, want 2\n%s", name, flags, code, stderr)
			}
			if out != "" {
				t.Errorf("%s %v wrote to stdout before rejecting: %q", name, flags, out)
			}
			if !strings.Contains(stderr, "--progress") {
				t.Errorf("%s %v: error does not name the flag:\n%s", name, flags, stderr)
			}
		}
	}
	if infos, _ := store.ListCrawls(dir); len(infos) != 0 {
		t.Errorf("a rejected command still created crawls: %+v", infos)
	}
}

// TestProgressFeedLines drives the feed with a scripted snapshot: a record at
// start, one per tick, the terminal record last, and nothing written after it.
func TestProgressFeedLines(t *testing.T) {
	var buf syncBuffer
	// Each read processes one more page until the crawl "ends"; from then on
	// the counters hold still, as the executor's do once the engine returns.
	var (
		mu        sync.Mutex
		processed int
		ended     bool
	)
	snap := func(id string) (runner.Snapshot, bool) {
		mu.Lock()
		if !ended {
			processed++
		}
		n := processed
		mu.Unlock()
		return runner.Snapshot{
			CrawlID: id, Seed: "https://ex.com/?a=1&b=2",
			Total: n, Discovered: n + 5, Queue: 5, S2xx: n,
		}, true
	}
	f := newProgressFeed(&buf, 10*time.Millisecond, snap)
	f.start("c1")
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(buf.String(), "\n") < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("feed wrote %q, want >= 3 lines", buf.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	ended = true
	final := processed
	mu.Unlock()
	f.finish(runner.Outcome{CrawlID: "c1", Status: store.StatusCompleted, Err: errors.New("finalize: disk full\nretry")})
	f.wait()
	got := buf.String()
	time.Sleep(50 * time.Millisecond)
	if buf.String() != got {
		t.Error("the feed wrote after its final line")
	}

	if !strings.Contains(got, `"seed":"https://ex.com/?a=1&b=2"`) {
		t.Errorf("seed not written verbatim (HTML-escaped?):\n%s", got)
	}
	recs, other := progressRecords(t, got)
	if len(other) != 0 {
		t.Errorf("non-record lines: %q", other)
	}
	for i, r := range recs[:len(recs)-1] {
		if r["state"] != "running" {
			t.Errorf("interim record state = %v, want running", r["state"])
		}
		if num(t, recs[i+1], "processed") < num(t, r, "processed") {
			t.Errorf("processed went backwards after record %d:\n%s", i, got)
		}
		if recs[i+1]["time"].(string) < r["time"].(string) {
			t.Errorf("time went backwards after record %d:\n%s", i, got)
		}
		if _, has := r["error"]; has {
			t.Errorf("interim record carries an error: %v", r)
		}
		if _, has := r["site_checks"]; has {
			t.Errorf("site_checks present for a crawl without the pass: %v", r)
		}
	}
	last := recs[len(recs)-1]
	if last["state"] != store.StatusCompleted || last["error"] != "finalize: disk full\nretry" {
		t.Errorf("final record = %v, want completed with the outcome error", last)
	}
	if num(t, last, "processed") != final {
		t.Errorf("final record processed = %d, want the final count %d", num(t, last, "processed"), final)
	}
}

// TestProgressFeedStates covers the snapshot-derived fields: finalizing, and
// site_checks present exactly when the pass is part of the crawl.
func TestProgressFeedStates(t *testing.T) {
	var buf syncBuffer
	snap := func(id string) (runner.Snapshot, bool) {
		return runner.Snapshot{CrawlID: id, Finalizing: true,
			SiteChecksState: "done", SiteChecksRan: 3, SiteChecksFindings: 2}, true
	}
	f := newProgressFeed(&buf, time.Hour, snap)
	f.start("c1")
	f.finish(runner.Outcome{CrawlID: "c1", Status: store.StatusInterrupted})
	f.wait()
	recs, _ := progressRecords(t, buf.String())
	if len(recs) != 2 {
		t.Fatalf("got %d records, want the start record and the final:\n%s", len(recs), buf.String())
	}
	if recs[0]["state"] != "finalizing" {
		t.Errorf("start record state = %v, want finalizing", recs[0]["state"])
	}
	last := recs[len(recs)-1]
	if last["state"] != store.StatusInterrupted {
		t.Errorf("final state = %v, want interrupted", last["state"])
	}
	sc, ok := last["site_checks"].(map[string]any)
	if !ok || sc["state"] != "done" || sc["ran"] != 3.0 || sc["findings"] != 2.0 {
		t.Errorf("site_checks = %v, want {state:done ran:3 findings:2}", last["site_checks"])
	}
}

// TestProgressFeedNeverStarted: a crawl that fails before it starts has no
// feed; finish and wait are no-ops and nothing is written.
func TestProgressFeedNeverStarted(t *testing.T) {
	var buf syncBuffer
	f := newProgressFeed(&buf, time.Second, func(string) (runner.Snapshot, bool) {
		t.Error("snapshot read for a crawl that never started")
		return runner.Snapshot{}, false
	})
	f.finish(runner.Outcome{Err: errors.New("bad seed")})
	f.wait()
	if buf.String() != "" {
		t.Errorf("never-started feed wrote %q", buf.String())
	}
}

// TestCrawlProgressBar: off a terminal (stderr is a pipe here) the bar writes
// a plain line per reading, ending on the completed crawl's line, and stdout
// is what it is without the flag.
func TestCrawlProgressBar(t *testing.T) {
	srv := progressSite(t, 1500*time.Millisecond)
	plainOut, _, code := runSplitT(t, "crawl", srv.URL+"/", "--store-dir", t.TempDir(), "--setup", "defaults")
	if code != 0 {
		t.Fatalf("plain crawl: exit %d\n%s", code, plainOut)
	}
	out, stderr, code := runSplitT(t, "crawl", srv.URL+"/", "--store-dir", t.TempDir(), "--setup", "defaults",
		"--progress", "bar", "--progress-interval", "1s")
	if code != 0 {
		t.Fatalf("progress crawl: exit %d\n%s%s", code, out, stderr)
	}
	if got, want := normalizeRun(out), normalizeRun(plainOut); got != want {
		t.Errorf("stdout changed by --progress bar:\n--- with\n%s--- without\n%s", got, want)
	}
	if strings.Contains(stderr, "\r") || !strings.HasSuffix(stderr, "\n") {
		t.Fatalf("off a terminal the bar must write whole lines:\n%q", stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("got %d lines, want start + at least one interval + final:\n%s", len(lines), stderr)
	}
	for _, l := range lines[:len(lines)-1] {
		if !strings.Contains(l, " left ") || !strings.Contains(l, "ETA") {
			t.Errorf("running line %q lacks what is left and the ETA", l)
		}
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "100%  3/3  done in ") || !strings.HasSuffix(last, "  ·  2xx 3") {
		t.Errorf("final line = %q, want the completed crawl's 3/3, all of them 2xx", last)
	}
}
