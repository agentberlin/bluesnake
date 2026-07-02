package runner

// #74 N11: robots-blocked pages are recorded WITHOUT consuming a MaxURLs fetch
// slot (the reserve happens after the robots gate), but resume used to seed the
// budget with len(ProcessedURLs()) — every recorded page including the blocked
// ones — so a paused+resumed crawl fetched FEWER pages than a straight one
// before hitting the cap. The budget must be seeded from the fetch-consuming
// count instead.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// robotsSite serves "/" linking /blocked, /a, /b, /c, with robots.txt
// disallowing /blocked. Every other path is a leaf.
func robotsSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /blocked\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>H</title></head><body>`+
				`<a href="/blocked">x</a> <a href="/a">a</a> <a href="/b">b</a> <a href="/c">c</a></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>L</title></head><body><p>leaf</p></body></html>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResumeBudgetExcludesRobotsBlocked(t *testing.T) {
	cfg := map[string]any{"speed.max_threads": 1, "limits.max_urls": 4}

	// Straight: / (1 slot) → /blocked recorded slot-free → /a /b /c (slots
	// 2-4). All four fetchable pages fit the MaxURLs=4 budget.
	straightSrv := robotsSite(t)
	straightDir := t.TempDir()
	sObs := &recObs{}
	if _, err := New(straightDir, sObs).Run(context.Background(),
		queue.JobSpec{URL: straightSrv.URL + "/", Config: cfg}, nil); err != nil {
		t.Fatal(err)
	}
	want := crawledCount(t, straightDir, sObs.startID)
	if want != 4 {
		t.Fatalf("straight crawl fetched %d pages, want 4 (fixture/budget drifted)", want)
	}

	// Pause after two recorded pages (/ and the slot-free /blocked), resume to
	// completion: the resumed sessions together must fetch the same 4 pages —
	// not stop early because the blocked page was charged a fetch slot.
	srv := robotsSite(t)
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	status, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: cfg}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusInterrupted {
		t.Fatalf("session 1 status = %q, want interrupted", status)
	}
	if got := crawledCount(t, dir, obs.startID); got != 1 {
		t.Fatalf("session 1 fetched %d pages, want exactly 1 (/, with /blocked recorded slot-free)", got)
	}
	status, err = New(dir, &recObs{}).Run(context.Background(),
		queue.JobSpec{ResumeID: obs.startID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != store.StatusCompleted {
		t.Fatalf("resume status = %q, want completed", status)
	}
	if got := crawledCount(t, dir, obs.startID); got != want {
		t.Errorf("resumed crawl fetched %d pages under MaxURLs=4, straight fetched %d — the budget over-charged the robots-blocked page (N11)",
			got, want)
	}
}
