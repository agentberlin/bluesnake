package runner

// The resume-open guards live in openForResume — the ONLY resume-open path
// (every surface reaches it through the queue dispatcher), so each guard holds
// structurally rather than per-surface (#74 D1/D2).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
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
	if resume == nil {
		t.Fatal("openForResume returned no resume state")
	}
	// Rehydration must see each URL exactly once (pages ∪ frontier disjoint
	// again): EachAdmitted is the counter-rehydration input the stranded pair
	// would double-count in (#74 R7).
	seen := map[string]int{}
	if err := st.EachAdmitted(func(url string, _ int) error {
		seen[url]++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for u, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in the admitted set, want once", u, n)
		}
	}
}

// erroringResumeSource fails a chosen loader method, driving loadResume's
// refusal arms (#74 N15): a resume-state read error must refuse the resume,
// not silently degrade (e.g. an edge-seq of 0 reproduces the R2 corruption).
type erroringResumeSource struct {
	failPageCount, failFetched, failCount, failSeq, failAdmitted bool
}

var errLoad = errors.New("store read failed")

func (s *erroringResumeSource) PageCount() (int, error) {
	if s.failPageCount {
		return 0, errLoad
	}
	return 1, nil
}
func (s *erroringResumeSource) FetchedCount() (int, error) {
	if s.failFetched {
		return 0, errLoad
	}
	return 1, nil
}
func (s *erroringResumeSource) Count() (int, error) {
	if s.failCount {
		return 0, errLoad
	}
	return 2, nil
}
func (s *erroringResumeSource) MaxEdgeSeq() (int64, error) {
	if s.failSeq {
		return 0, errLoad
	}
	return 9, nil
}
func (s *erroringResumeSource) EachAdmitted(fn func(url string, depth int) error) error {
	if s.failAdmitted {
		return errLoad
	}
	return fn("https://e.com/", 0)
}

func TestResumeRefusedOnResumeStateLoadError(t *testing.T) {
	capOff := config.Default().Limits // no bucket caps (all -1) → AnyBucketCap false
	capOn := capOff
	capOn.MaxURLsPerDepth = 1 // AnyBucketCap() == true → the admitted stream is consulted

	cases := []struct {
		name string
		src  *erroringResumeSource
		lim  config.LimitsConfig
	}{
		{"processed-count", &erroringResumeSource{failPageCount: true}, capOff},
		{"fetched-count", &erroringResumeSource{failFetched: true}, capOff},
		{"discovered-count", &erroringResumeSource{failCount: true}, capOff},
		{"edge-seq", &erroringResumeSource{failSeq: true}, capOff},
		{"admitted-stream", &erroringResumeSource{failAdmitted: true}, capOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadResume(tc.src, &tc.lim); !errors.Is(err, errLoad) {
				t.Errorf("loadResume with a failing %s read = %v, want the load error surfaced (refusal), not a silent degrade", tc.name, err)
			}
		})
	}
	// The admitted stream is consulted ONLY under a bucket cap: with none
	// configured a failing EachAdmitted must not even be reached.
	r, err := loadResume(&erroringResumeSource{failAdmitted: true}, &capOff)
	if err != nil {
		t.Fatalf("loadResume without a bucket cap consulted EachAdmitted: %v", err)
	}
	if r.PerDepth != nil || r.PerSub != nil || r.PerPath != nil {
		t.Errorf("bucket counters loaded without a bucket cap: %v / %v / %v", r.PerDepth, r.PerSub, r.PerPath)
	}
	if r.MaxEdgeSeq != 9 {
		t.Errorf("MaxEdgeSeq = %d, want 9", r.MaxEdgeSeq)
	}
}
