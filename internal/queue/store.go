package queue

import (
	"fmt"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// SQLiteStore is the persistent queue backed by the registry DB. It survives
// restarts and is what the desktop drains.
type SQLiteStore struct{ dir string }

// NewSQLiteStore returns a queue store over the registry DB at dir.
func NewSQLiteStore(dir string) *SQLiteStore { return &SQLiteStore{dir: dir} }

func toJob(j store.Job) Job {
	return Job{
		ID: j.ID, Status: j.Status, Position: j.Position,
		Source: j.Source, ProjectID: j.ProjectID, Label: j.Label,
		CrawlID: j.CrawlID, Error: j.Error, Spec: decodeSpec(j.Request),
		Enqueued: j.Enqueued, Started: j.Started, Finished: j.Finished,
	}
}

func (s *SQLiteStore) Enqueue(spec JobSpec, source, projectID, label string) (Job, error) {
	raw, err := encodeSpec(spec)
	if err != nil {
		return Job{}, err
	}
	j, err := store.EnqueueJob(s.dir, store.Job{
		Source: source, ProjectID: projectID, Label: label, Request: raw,
	})
	if err != nil {
		return Job{}, err
	}
	return toJob(j), nil
}

func (s *SQLiteStore) List() ([]Job, error) {
	rows, err := store.ListJobs(s.dir)
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, len(rows))
	for i, r := range rows {
		jobs[i] = toJob(r)
	}
	return jobs, nil
}

func (s *SQLiteStore) ClaimNext() (*Job, error) {
	r, err := store.ClaimNextJob(s.dir)
	if err != nil || r == nil {
		return nil, err
	}
	j := toJob(*r)
	return &j, nil
}

func (s *SQLiteStore) SetCrawlID(jobID, crawlID string) error {
	return store.SetJobCrawlID(s.dir, jobID, crawlID)
}

func (s *SQLiteStore) Finish(jobID, status, errMsg string) error {
	return store.FinishJob(s.dir, jobID, status, errMsg)
}

func (s *SQLiteStore) Cancel(jobID string) (bool, error) {
	return store.CancelJob(s.dir, jobID)
}

func (s *SQLiteStore) Unclaim(jobID string) error {
	return store.UnclaimJob(s.dir, jobID)
}

func (s *SQLiteStore) Reconcile() (int, error) {
	return store.ReconcileRunningJobs(s.dir)
}

// MemStore is an in-process queue with the same semantics as SQLiteStore, used
// by the CLI and the standalone MCP server (transient, drained in-process).
type MemStore struct {
	mu   sync.Mutex
	jobs []Job
	seq  int64
}

// NewMemStore returns an empty in-memory queue store.
func NewMemStore() *MemStore { return &MemStore{} }

func (m *MemStore) Enqueue(spec JobSpec, source, projectID, label string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	j := Job{
		ID: fmt.Sprintf("mem-%d", m.seq), Status: store.JobQueued, Position: m.seq,
		Source: source, ProjectID: projectID, Label: label, Spec: spec, Enqueued: time.Now(),
	}
	m.jobs = append(m.jobs, j)
	return j, nil
}

func (m *MemStore) List() ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, len(m.jobs))
	copy(out, m.jobs)
	return out, nil
}

func (m *MemStore) ClaimNext() (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.jobs {
		if m.jobs[i].Status == store.JobQueued {
			m.jobs[i].Status = store.JobRunning
			m.jobs[i].Started = time.Now()
			j := m.jobs[i]
			return &j, nil
		}
	}
	return nil, nil
}

func (m *MemStore) find(id string) *Job {
	for i := range m.jobs {
		if m.jobs[i].ID == id {
			return &m.jobs[i]
		}
	}
	return nil
}

func (m *MemStore) SetCrawlID(jobID, crawlID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.find(jobID); j != nil {
		j.CrawlID = crawlID
	}
	return nil
}

func (m *MemStore) Finish(jobID, status, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.find(jobID); j != nil {
		j.Status = status
		j.Error = errMsg
		j.Finished = time.Now()
	}
	return nil
}

func (m *MemStore) Cancel(jobID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.find(jobID); j != nil && j.Status == store.JobQueued {
		j.Status = store.JobCanceled
		j.Finished = time.Now()
		return true, nil
	}
	return false, nil
}

func (m *MemStore) Unclaim(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.find(jobID); j != nil && j.Status == store.JobRunning {
		j.Status = store.JobQueued
		j.Started = time.Time{}
	}
	return nil
}

func (m *MemStore) Reconcile() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for i := range m.jobs {
		if m.jobs[i].Status == store.JobRunning {
			m.jobs[i].Status = store.JobInterrupted
			m.jobs[i].Finished = time.Now()
			n++
		}
	}
	return n, nil
}
