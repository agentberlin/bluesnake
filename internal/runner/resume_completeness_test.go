package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// chainServer serves a deep link chain / -> /l1 -> /l2 -> ... -> /l{n-1}, so
// pausing after a few pages leaves real pending work that resume must recover.
func chainServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// "/" -> /l1, "/lK" -> /l{K+1}, the last page links nowhere.
		next := ""
		switch r.URL.Path {
		case "/":
			next = "/l1"
		default:
			var k int
			if _, err := fmt.Sscanf(r.URL.Path, "/l%d", &k); err == nil && k < n-1 {
				next = fmt.Sprintf("/l%d", k+1)
			}
		}
		if next != "" {
			fmt.Fprintf(w, `<html><head><title>P</title></head><body><a href="%s">next</a></body></html>`, next)
		} else {
			fmt.Fprint(w, `<html><head><title>End</title></head><body><p>end</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawledCount reopens a stored crawl and returns its crawled-page tally.
func crawledCount(t *testing.T, dir, id string) int {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	crawled, _, err := st.Counts()
	if err != nil {
		t.Fatal(err)
	}
	return crawled
}

// pendingCount reopens a stored crawl and returns its persisted pending-frontier
// row count.
func pendingCount(t *testing.T, dir, id string) int {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pending, err := st.PendingFrontier()
	if err != nil {
		t.Fatal(err)
	}
	return len(pending)
}

// TestResumeRecoversAllPagesThroughRunner is the C1 regression guard (#70 C1 /
// #71 Group 5): a pause+resume driven through the production Executor sink must
// recover every page a straight crawl finds. The bug it pins: runner.sink did not
// implement frontier.Dedup, so the engine fell back to the in-RAM dedup and never
// persisted frontier rows — resume read an empty PendingFrontier() and silently
// dropped the un-crawled tail. The earlier pause/resume test only checked the
// final *status* (which stayed "completed" even while losing pages), so it could
// not catch this.
func TestResumeRecoversAllPagesThroughRunner(t *testing.T) {
	const chain = 5 // /, /l1, /l2, /l3, /l4
	srv := chainServer(t, chain)

	// Baseline: a straight crawl finds the whole chain.
	straightDir := t.TempDir()
	straightStatus, err := New(straightDir, nil).Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if straightStatus != store.StatusCompleted {
		t.Fatalf("straight crawl status = %q, want completed", straightStatus)
	}
	straightInfos, _ := store.ListCrawls(straightDir)
	want := crawledCount(t, straightDir, straightInfos[0].ID)
	if want != chain {
		t.Fatalf("straight crawl found %d pages, want %d", want, chain)
	}

	// Interrupt the same shape after 2 pages, then resume.
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusInterrupted {
		t.Fatalf("paused crawl status = %q, want interrupted", status)
	}
	crawlID := obs.startID

	// The headline C1 proof: pending frontier rows were actually persisted. With
	// the bug, store-dedup is inactive and this is 0.
	if got := pendingCount(t, dir, crawlID); got == 0 {
		t.Fatal("no pending frontier rows persisted after pause — resume cannot recover the tail (C1)")
	}
	if got := crawledCount(t, dir, crawlID); got != 2 {
		t.Fatalf("interrupted crawl crawled %d pages, want exactly 2", got)
	}

	// Resume to completion and require the full chain back.
	resObs := &recObs{}
	status, err = New(dir, resObs).Run(context.Background(),
		queue.JobSpec{ResumeID: crawlID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resumed crawl status = %q, want completed", status)
	}
	if got := crawledCount(t, dir, crawlID); got != want {
		t.Errorf("resumed crawl recovered %d pages, want %d (a straight crawl) — pages lost on resume", got, want)
	}
}
