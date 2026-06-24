package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func TestJobQueueLifecycle(t *testing.T) {
	dir := t.TempDir()

	j, err := EnqueueJob(dir, Job{Source: "manual", Label: "https://ex.com/", Request: `{"url":"https://ex.com/"}`})
	if err != nil {
		t.Fatal(err)
	}
	if j.ID == "" || j.Status != JobQueued || j.Position == 0 || j.Enqueued.IsZero() {
		t.Fatalf("enqueued job not fully populated: %+v", j)
	}

	jobs, err := ListJobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != j.ID || jobs[0].Request != `{"url":"https://ex.com/"}` {
		t.Fatalf("ListJobs = %+v", jobs)
	}

	// claim it -> running, started stamped
	claimed, err := ClaimNextJob(dir)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != j.ID || claimed.Status != JobRunning || claimed.Started.IsZero() {
		t.Fatalf("ClaimNextJob = %+v", claimed)
	}
	// queue is now empty of claimable work
	if next, err := ClaimNextJob(dir); err != nil || next != nil {
		t.Fatalf("second claim should be empty: %+v %v", next, err)
	}

	// link the spawned crawl, then finish
	if err := SetJobCrawlID(dir, j.ID, "20260101-000000-abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := FinishJob(dir, j.ID, JobDone, ""); err != nil {
		t.Fatal(err)
	}
	jobs, _ = ListJobs(dir)
	if jobs[0].Status != JobDone || jobs[0].CrawlID != "20260101-000000-abcdef" || jobs[0].Finished.IsZero() {
		t.Fatalf("finished job = %+v", jobs[0])
	}
}

func TestClaimNextJobOrdering(t *testing.T) {
	dir := t.TempDir()
	var ids []string
	for i := 0; i < 3; i++ {
		j, err := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	// FIFO: claims come back in enqueue order regardless of id randomness
	for i := 0; i < 3; i++ {
		c, err := ClaimNextJob(dir)
		if err != nil {
			t.Fatal(err)
		}
		if c == nil || c.ID != ids[i] {
			t.Fatalf("claim %d = %+v, want %s", i, c, ids[i])
		}
		// each claimed job must not be re-claimable while running
	}
	if c, err := ClaimNextJob(dir); err != nil || c != nil {
		t.Fatalf("queue should be drained: %+v %v", c, err)
	}
}

func TestCancelJobQueuedOnly(t *testing.T) {
	dir := t.TempDir()
	j, _ := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})

	ok, err := CancelJob(dir, j.ID)
	if err != nil || !ok {
		t.Fatalf("cancel queued job: ok=%v err=%v", ok, err)
	}
	// a canceled job is never claimed
	if c, _ := ClaimNextJob(dir); c != nil {
		t.Fatalf("canceled job was claimed: %+v", c)
	}
	// canceling again (now canceled, not queued) is a no-op
	if ok, _ := CancelJob(dir, j.ID); ok {
		t.Fatal("re-cancel reported a change")
	}
	// canceling a running job via CancelJob is refused (dispatcher stops it instead)
	r, _ := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})
	if _, err := ClaimNextJob(dir); err != nil {
		t.Fatal(err)
	}
	if ok, _ := CancelJob(dir, r.ID); ok {
		t.Fatal("CancelJob changed a running job")
	}
}

func TestReconcileRunningJobs(t *testing.T) {
	dir := t.TempDir()
	a, _ := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})
	b, _ := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})
	// a is mid-flight (running) when the app dies; b is still queued
	if _, err := ClaimNextJob(dir); err != nil {
		t.Fatal(err)
	}

	n, err := ReconcileRunningJobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d running jobs, want 1", n)
	}
	jobs, _ := ListJobs(dir)
	byID := map[string]Job{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	if byID[a.ID].Status != JobInterrupted || byID[a.ID].Finished.IsZero() {
		t.Errorf("running job not reconciled to interrupted: %+v", byID[a.ID])
	}
	if byID[b.ID].Status != JobQueued {
		t.Errorf("queued job disturbed by reconcile: %+v", byID[b.ID])
	}
	// after reconcile the still-queued job is claimable; the interrupted one is not
	c, _ := ClaimNextJob(dir)
	if c == nil || c.ID != b.ID {
		t.Fatalf("post-reconcile claim = %+v, want %s", c, b.ID)
	}
}

func TestDeleteJob(t *testing.T) {
	dir := t.TempDir()
	j, err := EnqueueJob(dir, Job{Source: "manual", Request: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteJob(dir, j.ID); err != nil {
		t.Fatal(err)
	}
	jobs, _ := ListJobs(dir)
	if len(jobs) != 0 {
		t.Fatalf("job not removed by DeleteJob: %+v", jobs)
	}
}

// TestJobsTableOnExistingRegistry pins that a registry created before the jobs
// table existed gains it on the next open (the CREATE-IF-NOT-EXISTS path, same
// as the brands table), so no migration step is needed.
func TestJobsTableOnExistingRegistry(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	reg, err := sql.Open("sqlite", filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Exec(`DROP TABLE jobs`); err != nil {
		t.Fatalf("drop jobs to simulate old registry: %v", err)
	}
	reg.Close()

	// any registry-backed op reopens and recreates the table
	if _, err := ListJobs(dir); err != nil {
		t.Fatalf("ListJobs after dropping jobs table: %v", err)
	}
	if _, err := EnqueueJob(dir, Job{Source: "manual", Request: `{}`}); err != nil {
		t.Fatalf("EnqueueJob after table recreated: %v", err)
	}
}
