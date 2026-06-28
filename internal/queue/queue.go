// Package queue is the core crawl queue: a persistent (or in-memory) list of
// crawl jobs plus a single Dispatcher that drains them one at a time through an
// Executor. It is the one path by which crawls run — every surface (desktop,
// MCP, CLI) submits jobs here rather than starting crawls itself, so the
// interface never dictates how a crawl executes. The desktop backs the queue
// with the persistent registry DB (jobs survive restarts); the CLI and the
// standalone MCP server back it with an in-memory store and drain it in-process.
//
// Concurrency is deliberately one job at a time (DESIGN.md §8: single-process
// concurrency is the design point). The claim step is atomic and the dispatcher
// loop is structured so a future parallel mode is N loops + N executors, not a
// rewrite.
package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// JobSpec is the neutral description of a crawl to run: the same surface the
// CLI/MCP expose (profile + dotted-path config overrides) plus a ResumeID for a
// job that continues an existing crawl. The executor turns it into a real crawl
// at run time (so e.g. a list-mode sitemap is fetched when the job runs, not when
// it is enqueued).
type JobSpec struct {
	Mode       string         `json:"mode,omitempty"` // spider (default) | list
	URL        string         `json:"url,omitempty"`
	URLs       []string       `json:"urls,omitempty"`
	SitemapURL string         `json:"sitemap_url,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Config     map[string]any `json:"config,omitempty"` // dotted path -> value
	ResumeID   string         `json:"resume_id,omitempty"`
	// ConfigYAML is a fully-frozen config used instead of Profile+Config. The CLI
	// sets it (it builds its config from a file/flags rather than a named
	// profile); the profile-based surfaces leave it empty.
	ConfigYAML string `json:"config_yaml,omitempty"`
}

// Job is one queue entry with its spec decoded.
type Job struct {
	ID        string
	Status    string // store.Job* status constant
	Position  int64
	Source    string // "manual" | "project"
	ProjectID string
	Label     string
	CrawlID   string
	Error     string
	Spec      JobSpec
	Enqueued  time.Time
	Started   time.Time
	Finished  time.Time
}

// Store persists jobs. Two implementations exist: SQLiteStore (registry DB,
// survives restarts) and MemStore (in-process). Both share identical semantics —
// FIFO claim, queued-only cancel, running->interrupted reconcile.
type Store interface {
	Enqueue(spec JobSpec, source, projectID, label string) (Job, error)
	List() ([]Job, error)
	// ClaimNext atomically moves the oldest queued job to running and returns it,
	// or (nil, nil) when nothing is queued.
	ClaimNext() (*Job, error)
	SetCrawlID(jobID, crawlID string) error
	Finish(jobID, status, errMsg string) error
	// Cancel cancels a still-queued job, reporting whether it changed anything.
	Cancel(jobID string) (bool, error)
	// Reconcile marks every job left running (host died mid-crawl) as interrupted
	// and returns the count. Called once before the dispatcher starts draining.
	Reconcile() (int, error)
}

// Executor runs a single crawl to completion. It is stateful about its one
// in-flight crawl: Run blocks until that crawl ends; Pause/Stop signal the
// in-flight crawl (pause leaves it resumable, stop finalises it as completed);
// both are no-ops when nothing is running. onStart is invoked once with the
// crawl id as soon as the crawl exists (so the dispatcher can link the job to
// the crawl and surfaces can navigate to the live view). The returned status is
// the crawl's terminal store status (store.StatusCompleted | store.StatusInterrupted).
type Executor interface {
	Run(ctx context.Context, spec JobSpec, onStart func(crawlID string)) (status string, err error)
	Pause() // pause every in-flight crawl (left resumable)
	Stop()  // stop every in-flight crawl (finalised as completed)
	// StopCrawl stops one specific crawl by id, so Cancel can target a single job
	// when several run in parallel; a no-op when that crawl is not in flight.
	StopCrawl(crawlID string)
}

// jobStatusFor maps a finished crawl's outcome to the terminal job status.
func jobStatusFor(crawlStatus string, err error) string {
	switch {
	case err != nil:
		return store.JobFailed
	case crawlStatus == store.StatusInterrupted:
		return store.JobInterrupted // paused — the crawl is left resumable
	default:
		return store.JobDone
	}
}

func encodeSpec(spec JobSpec) (string, error) {
	b, err := json.Marshal(spec)
	return string(b), err
}

func decodeSpec(raw string) JobSpec {
	var s JobSpec
	_ = json.Unmarshal([]byte(raw), &s) // a malformed spec decodes to zero; the executor validates
	return s
}
