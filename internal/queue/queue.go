// Package queue is the core crawl queue: a persistent (or in-memory) list of
// crawl jobs plus a single Dispatcher that drains them through an Executor. It
// is the one path by which crawls run — every surface (desktop, MCP, CLI)
// submits jobs here rather than starting crawls itself, so the interface never
// dictates how a crawl executes. The desktop backs the queue with the
// persistent registry DB (jobs survive restarts); the CLI and the standalone
// MCP server back it with an in-memory store and drain it in-process.
//
// The dispatcher runs up to speed.max_concurrent_crawls jobs at once
// (WithConcurrency; default 1) with identical semantics on every surface. The
// claim step is atomic on both stores so W drain loops never double-claim, and
// per-crawl control — PauseCrawl/StopCrawl/CurrentAll — is addressed by crawl
// id, so one of several parallel crawls can be paused, stopped or inspected
// without disturbing the rest. Any surface driving W>1 must inject ONE shared
// process-wide limiter into its executor (runner.WithLimiter), so the global
// fetch/finalize/render caps hold across the parallel crawls (H1/P17).
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
	// Unclaim returns a claimed-but-never-started job to queued (the dispatcher
	// claimed it, then a shutdown landed before the crawl began).
	Unclaim(jobID string) error
	// Reconcile marks every job left running (host died mid-crawl) as interrupted
	// and returns the count. Called once before the dispatcher starts draining.
	Reconcile() (int, error)
}

// Executor runs a single crawl to completion per Run call, and can hold several
// in flight at once (one per drain loop). Run blocks until its crawl ends;
// onStart is invoked once with the crawl id as soon as the crawl exists (so the
// dispatcher can link the job to the crawl and surfaces can navigate to the
// live view). The returned status is the crawl's terminal store status
// (store.StatusCompleted | store.StatusInterrupted). All control signals are
// no-ops when the addressed crawl (or, for the fan-outs, any crawl) is not
// running.
type Executor interface {
	Run(ctx context.Context, spec JobSpec, onStart func(crawlID string)) (status string, err error)
	Pause() // pause every in-flight crawl (left resumable)
	Stop()  // stop every in-flight crawl (finalised as completed)
	// PauseCrawl pauses one specific crawl by id (left resumable), leaving any
	// other in-flight crawl untouched.
	PauseCrawl(crawlID string)
	// StopCrawl stops one specific crawl by id (finalised as completed), so
	// Cancel and the per-crawl surface controls can target a single job when
	// several run in parallel.
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
