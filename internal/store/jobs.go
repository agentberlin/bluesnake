package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// The crawl queue. Jobs are persisted in the registry DB so the queue survives
// restarts and crashes (DESIGN.md §5.3). A job describes a crawl to run; the
// queue dispatcher's drain loops claim jobs (atomically, so parallel loops
// never double-claim) and run each via an executor. The
// store treats Request as opaque JSON — internal/queue owns its meaning — so the
// persistence layer never depends on the crawl-request shape.

// Job statuses in the registry `jobs` table.
const (
	JobQueued      = "queued"
	JobRunning     = "running"
	JobDone        = "done"
	JobFailed      = "failed"
	JobInterrupted = "interrupted" // host died mid-crawl; the partial crawl is resumable
	JobCanceled    = "canceled"
)

// Job is one crawl-queue entry.
type Job struct {
	ID        string
	Status    string
	Position  int64  // monotonic enqueue order; the queue drains lowest-first
	Source    string // "manual" | "project"
	ProjectID string // set when Source == "project"
	Label     string // human label (seed URL or competitor domain)
	Request   string // opaque JSON spec the dispatcher's executor turns into a crawl
	CrawlID   string // the crawl this job spawned, filled when it starts
	Error     string // failure detail
	Enqueued  time.Time
	Started   time.Time
	Finished  time.Time
}

func newJobID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "job-" + time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// EnqueueJob appends a queued job. The store assigns ID, Position (next in the
// monotonic order), Status=queued and the enqueued timestamp; the caller supplies
// Source/ProjectID/Label/Request. The fully populated row is returned.
func EnqueueJob(dir string, j Job) (Job, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return Job{}, err
	}
	defer reg.Close()

	tx, err := reg.Begin()
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	var pos int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM jobs`).Scan(&pos); err != nil {
		return Job{}, err
	}
	j.ID = newJobID()
	j.Status = JobQueued
	j.Position = pos
	j.Enqueued = time.Now()
	if _, err := tx.Exec(
		`INSERT INTO jobs(id, status, position, source, project_id, label, request, enqueued) VALUES(?,?,?,?,?,?,?,?)`,
		j.ID, j.Status, j.Position, j.Source, nullStr(j.ProjectID), nullStr(j.Label), j.Request, j.Enqueued.Unix()); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}

const jobColumns = `id, status, position, source,
	COALESCE(project_id, ''), COALESCE(label, ''), request, COALESCE(crawl_id, ''),
	COALESCE(error, ''), enqueued, COALESCE(started, 0), COALESCE(finished, 0)`

func scanJob(s interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var enqueued, started, finished int64
	if err := s.Scan(&j.ID, &j.Status, &j.Position, &j.Source,
		&j.ProjectID, &j.Label, &j.Request, &j.CrawlID,
		&j.Error, &enqueued, &started, &finished); err != nil {
		return Job{}, err
	}
	j.Enqueued = time.Unix(enqueued, 0)
	if started > 0 {
		j.Started = time.Unix(started, 0)
	}
	if finished > 0 {
		j.Finished = time.Unix(finished, 0)
	}
	return j, nil
}

// ListJobs returns every job in queue order (lowest position first).
func ListJobs(dir string) ([]Job, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	rows, err := reg.Query(`SELECT ` + jobColumns + ` FROM jobs ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ClaimNextJob atomically moves the oldest queued job to running (stamping its
// start time) and returns it, or (nil, nil) when nothing is queued. The
// status='queued' guard keeps a future multi-consumer setup from handing the
// same job to two workers.
func ClaimNextJob(dir string) (*Job, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, err
	}
	defer reg.Close()

	tx, err := reg.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRow(`SELECT id FROM jobs WHERE status = ? ORDER BY position LIMIT 1`, JobQueued).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res, err := tx.Exec(`UPDATE jobs SET status = ?, started = ? WHERE id = ? AND status = ?`,
		JobRunning, now, id, JobQueued)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil // raced away by another consumer
	}
	j, err := scanJob(tx.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &j, nil
}

// SetJobCrawlID records the crawl a running job spawned, so the UI can link the
// queue entry to its crawl and a crash can be tied back to a resumable crawl.
func SetJobCrawlID(dir, id, crawlID string) error {
	return execReg(dir, `UPDATE jobs SET crawl_id = ? WHERE id = ?`, crawlID, id)
}

// FinishJob records a terminal status (done/failed/interrupted/canceled) with an
// optional error and the finish time.
func FinishJob(dir, id, status, errMsg string) error {
	return execReg(dir, `UPDATE jobs SET status = ?, error = ?, finished = ? WHERE id = ?`,
		status, nullStr(errMsg), time.Now().Unix(), id)
}

// CancelJob cancels a still-queued job and reports whether it changed anything.
// A running job is left untouched (the dispatcher stops it via the live crawl);
// a finished/canceled job is a no-op.
func CancelJob(dir, id string) (bool, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return false, err
	}
	defer reg.Close()
	res, err := reg.Exec(`UPDATE jobs SET status = ?, finished = ? WHERE id = ? AND status = ?`,
		JobCanceled, time.Now().Unix(), id, JobQueued)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnclaimJob returns a claimed-but-never-started job to the queue (running →
// queued, clearing the start stamp). The dispatcher uses it when a shutdown
// lands between claiming a job and starting its crawl: the job must neither
// run after the stop signal nor be lost to "interrupted" (no crawl ever
// existed to resume) — it simply waits for the next drain.
func UnclaimJob(dir, id string) error {
	return execReg(dir, `UPDATE jobs SET status = ?, started = NULL WHERE id = ? AND status = ?`,
		JobQueued, id, JobRunning)
}

// ReconcileRunningJobs marks every job left running (the host died mid-crawl) as
// interrupted, returning how many were reconciled. The crawl each spawned is a
// resumable registry entry, so nothing is lost; the dispatcher calls this once
// on startup before it begins draining.
func ReconcileRunningJobs(dir string) (int, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return 0, err
	}
	defer reg.Close()
	res, err := reg.Exec(`UPDATE jobs SET status = ?, finished = ? WHERE status = ?`,
		JobInterrupted, time.Now().Unix(), JobRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteJob removes a job row (used to clear finished entries from the queue).
func DeleteJob(dir, id string) error {
	return execReg(dir, `DELETE FROM jobs WHERE id = ?`, id)
}

func execReg(dir, query string, args ...any) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(query, args...)
	return err
}

// nullStr stores "" as SQL NULL so optional text columns stay null rather than
// empty-string, matching the COALESCE reads in jobColumns.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
