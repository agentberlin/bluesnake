package runner

// The resume-open guards live in openForResume — the ONLY resume-open path
// (every surface reaches it through the queue dispatcher), so each guard holds
// structurally rather than per-surface (#74 D1/D2).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestResumeCompletedCrawlRefused pins #74 N9: a resume job for a completed
// crawl is refused before anything is written — previously it was accepted,
// briefly de-completed the registry row (finalize's interim interrupted
// status), and a failure mid-resume left it that way.
func TestResumeCompletedCrawlRefused(t *testing.T) {
	srv := chainServer(t, 3)
	dir := t.TempDir()
	obs := &recObs{}
	e := New(dir, obs)
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	id := obs.startID

	_, err := New(dir, &recObs{}).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil)
	if err == nil {
		t.Fatal("resuming a completed crawl should be refused, got nil error")
	}
	if !strings.Contains(err.Error(), "completed") {
		t.Errorf("refusal error = %q, want it to name the completed status", err)
	}
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Status != store.StatusCompleted {
		t.Errorf("registry after refused resume = %+v, want the crawl untouched at completed", infos)
	}
}

// TestOpenForResumePurgesStrandedFrontierRows pins #74 N14: a pages∩frontier
// pair stranded by a crash in the EC-02 window (between Page() and
// FrontierDone()) is purged at resume-open. Left in place it double-counts in
// the admitted-set rehydration (R7's consumer side) and accretes across every
// subsequent resume — PendingFrontier skips it but nothing ever deleted it.
func TestOpenForResumePurgesStrandedFrontierRows(t *testing.T) {
	srv := chainServer(t, 4)
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	id := obs.startID

	// Forge the EC-02 crash state: a frontier row for a URL that already has a
	// pages row (Page() committed, FrontierDone() never ran).
	func() {
		st, err := store.OpenCrawl(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.DB().Exec(
			`INSERT OR IGNORE INTO frontier(url, depth, redirect_hops, source) VALUES(?, 1, 0, '')`,
			srv.URL+"/l1"); err != nil {
			t.Fatalf("forge stranded pair: %v", err)
		}
	}()

	st, _, _, resume, err := openForResume(dir, id)
	if err != nil {
		t.Fatalf("openForResume: %v", err)
	}
	defer st.Close()

	var stranded int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM frontier WHERE EXISTS (SELECT 1 FROM pages WHERE pages.url = frontier.url)`,
	).Scan(&stranded); err != nil {
		t.Fatal(err)
	}
	if stranded != 0 {
		t.Errorf("%d stranded frontier row(s) survived resume-open, want 0 (purged)", stranded)
	}
	// Rehydration must see each URL exactly once (pages ∪ frontier disjoint again).
	seen := map[string]int{}
	for _, u := range resume.Processed {
		seen[u]++
	}
	for _, it := range resume.Pending {
		seen[it.URL]++
	}
	for u, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in the resume state, want once", u, n)
		}
	}
}

// erroringResumeSource fails a chosen loader method, driving loadResume's
// refusal arms (#74 N15): a resume-state read error must refuse the resume,
// not silently degrade (e.g. an edge-seq of 0 reproduces the R2 corruption).
type erroringResumeSource struct {
	failProcessed, failFetched, failPending, failSeq, failAdmitted bool
}

var errLoad = errors.New("store read failed")

func (s *erroringResumeSource) ProcessedURLs() ([]string, error) {
	if s.failProcessed {
		return nil, errLoad
	}
	return []string{"https://e.com/"}, nil
}
func (s *erroringResumeSource) FetchedCount() (int, error) {
	if s.failFetched {
		return 0, errLoad
	}
	return 1, nil
}
func (s *erroringResumeSource) PendingFrontier() ([]frontier.Item, error) {
	if s.failPending {
		return nil, errLoad
	}
	return []frontier.Item{{URL: "https://e.com/a", Depth: 1}}, nil
}
func (s *erroringResumeSource) MaxEdgeSeq() (int64, error) {
	if s.failSeq {
		return 0, errLoad
	}
	return 9, nil
}
func (s *erroringResumeSource) AdmittedItems() ([]frontier.Item, error) {
	if s.failAdmitted {
		return nil, errLoad
	}
	return []frontier.Item{{URL: "https://e.com/", Depth: 0}}, nil
}

func TestResumeRefusedOnResumeStateLoadError(t *testing.T) {
	cases := []struct {
		name string
		src  *erroringResumeSource
		// admitted loads only when a bucket cap is configured
		needAdmitted bool
	}{
		{"processed", &erroringResumeSource{failProcessed: true}, false},
		{"fetched-count", &erroringResumeSource{failFetched: true}, false},
		{"pending", &erroringResumeSource{failPending: true}, false},
		{"edge-seq", &erroringResumeSource{failSeq: true}, false},
		{"admitted", &erroringResumeSource{failAdmitted: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadResume(tc.src, tc.needAdmitted); !errors.Is(err, errLoad) {
				t.Errorf("loadResume with a failing %s read = %v, want the load error surfaced (refusal), not a silent degrade", tc.name, err)
			}
		})
	}
	// The admitted set is loaded ONLY under a bucket cap: with none configured a
	// failing AdmittedItems must not even be consulted.
	r, err := loadResume(&erroringResumeSource{failAdmitted: true}, false)
	if err != nil {
		t.Fatalf("loadResume without a bucket cap consulted AdmittedItems: %v", err)
	}
	if len(r.Admitted) != 0 {
		t.Errorf("Admitted loaded without a bucket cap: %v", r.Admitted)
	}
	if r.MaxEdgeSeq != 9 {
		t.Errorf("MaxEdgeSeq = %d, want 9", r.MaxEdgeSeq)
	}
}
