// Package runner is the single crawl-execution path. It collapses what used to
// be two near-identical session managers (the desktop's crawlSession and the
// MCP Runner's runnerSession) into one Executor that satisfies queue.Executor:
// the queue dispatcher drives it, and every surface plugs in an Observer for its
// own live feedback (the desktop emits Wails events; the CLI prints a progress
// line; the MCP server exposes a status snapshot). The engine and the shared
// finalize path are untouched — this is orchestration, not a new crawler.
package runner

import (
	"context"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// Snapshot is a live-crawl progress reading (a superset of every surface's
// progress payload). Surfaces add their own extras (desktop: thread count + a
// notable-URL feed).
type Snapshot struct {
	CrawlID    string
	Seed       string
	Total      int // URLs processed so far (fetched + robots-blocked + errored)
	Discovered int
	Queue      int
	S2xx       int
	S3xx       int
	S4xx       int
	S5xx       int
	Blocked    int
	NoResponse int
	Indexable  int
	RatePerSec float64
	ElapsedSec int
	Threads    int
}

// Outcome is the terminal result handed to Observer.OnDone. The analysis fields
// mirror finalize.Outcome so a surface can report what the post-crawl analysis
// found without re-querying.
type Outcome struct {
	CrawlID     string
	Status      string // store.StatusCompleted | store.StatusInterrupted
	Crawled     int
	Total       int
	DurationSec int
	Analyzed    bool
	Chains      int
	NearDups    int
	IssueTotal  int
	IssueChecks int
	Err         error
}

// Observer receives a crawl's lifecycle so a surface can render it live. All
// methods may be called from the executor's goroutine; implementations must not
// block. A nil observer is fine (headless).
type Observer interface {
	OnStart(crawlID, seed string)
	OnPage(rec *crawler.PageRecord) // for surfaces that build a live feed
	OnDone(o Outcome)
}

// Executor runs crawls one at a time for a single surface. It holds the one
// in-flight crawl; Pause/Stop signal it, Snapshot reads its live counters.
type Executor struct {
	storeDir string
	obs      Observer

	mu  sync.Mutex
	cur *run // in-flight crawl, nil when idle
}

// New builds an executor rooted at storeDir. obs may be nil.
func New(storeDir string, obs Observer) *Executor {
	return &Executor{storeDir: storeDir, obs: obs}
}

// StoreDir reports the store directory the executor runs against.
func (e *Executor) StoreDir() string { return e.storeDir }

// run is the per-crawl state, including the live counters.
type run struct {
	st      *store.Crawl
	c       *crawler.Crawler
	seeds   []string
	resumed bool
	cancel  context.CancelFunc

	threads int

	mu             sync.Mutex
	stopMode       string // "" | "pause" | "stop"
	total          int
	discovered     int
	s2, s3, s4, s5 int
	blocked        int
	noresp         int
	indexable      int
	recent         []time.Time
	started        time.Time
}

// Run executes the crawl described by spec and blocks until it ends. It
// satisfies queue.Executor.
func (e *Executor) Run(ctx context.Context, spec queue.JobSpec, onStart func(crawlID string)) (string, error) {
	st, cfg, seeds, processed, pending, resumed, err := e.open(ctx, spec)
	if err != nil {
		// surface the failure to the observer so callers awaiting a start (the MCP
		// backend, the CLI) always see a terminal event even when no crawl began.
		if e.obs != nil {
			e.obs.OnDone(Outcome{Err: err})
		}
		return "", err
	}

	r := &run{
		st: st, seeds: seeds, resumed: resumed, started: time.Now(),
		threads:    cfg.Speed.MaxThreads,
		total:      len(processed),
		discovered: len(processed) + len(pending),
	}
	opts := []crawler.Option{crawler.WithSink(&sink{inner: st, r: r, obs: e.obs})}
	if resumed {
		opts = append(opts, crawler.WithResume(processed, pending))
	}
	c, err := crawler.New(cfg, opts...)
	if err != nil {
		st.Close()
		return "", err
	}
	r.c = c
	var runCtx context.Context
	runCtx, r.cancel = context.WithCancel(ctx)

	e.mu.Lock()
	e.cur = r
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.cur = nil
		e.mu.Unlock()
	}()

	if onStart != nil {
		onStart(st.ID)
	}
	if e.obs != nil {
		e.obs.OnStart(st.ID, seeds[0])
	}

	res, runErr := c.Run(runCtx, seeds...)
	c.Close()
	defer st.Close()

	r.mu.Lock()
	mode := r.stopMode
	r.mu.Unlock()

	out := Outcome{CrawlID: st.ID, Status: store.StatusInterrupted, Err: runErr}
	if res != nil {
		out.DurationSec = int(res.Duration.Seconds())
		// Pause keeps the crawl resumable; Stop finalises early as completed. The
		// shared finalize path persists aggregates + status and, when completed,
		// recomputes depth + inlinks over the full graph (resume) and runs analysis.
		fo, ferr := finalize.Crawl(c, st, res, finalize.Params{
			StoreDir:  e.storeDir,
			Cfg:       cfg,
			Seeds:     seeds,
			Resumed:   resumed,
			Completed: !res.Interrupted || mode == "stop",
		})
		out.Status, out.Crawled, out.Total, out.Analyzed = fo.Status, fo.Crawled, fo.Total, fo.Analyzed
		out.Chains, out.NearDups, out.IssueTotal, out.IssueChecks = fo.Chains, fo.NearDups, fo.IssueTotal, fo.IssueChecks
		if ferr != nil && out.Err == nil {
			out.Err = ferr
		}
	}
	if e.obs != nil {
		e.obs.OnDone(out)
	}
	return out.Status, out.Err
}

// open resolves a spec into an open crawl ready to run: a fresh crawl, or an
// existing one restored for resume (which uses the crawl's frozen config and
// seeds, not the spec).
func (e *Executor) open(ctx context.Context, spec queue.JobSpec) (
	st *store.Crawl, cfg *config.Config, seeds, processed []string, pending []frontier.Item, resumed bool, err error,
) {
	if spec.ResumeID != "" {
		return openForResume(e.storeDir, spec.ResumeID)
	}
	if spec.ConfigYAML != "" {
		cfg, err = config.Load([]byte(spec.ConfigYAML))
	} else {
		cfg, err = BuildConfig(e.storeDir, spec)
	}
	if err != nil {
		return
	}
	var mode string
	seeds, mode, err = ResolveSeeds(ctx, cfg, spec)
	if err != nil {
		return
	}
	st, err = store.CreateCrawl(e.storeDir, seeds, mode, cfg)
	return
}

// openForResume restores an existing crawl for a resume job: its frozen config,
// every seed (so host classification and the depth BFS re-root from all of them),
// and the processed/pending frontier sets. The spec's profile/config are ignored
// — a resume must run the crawl's own frozen config.
func openForResume(storeDir, id string) (
	st *store.Crawl, cfg *config.Config, seeds, processed []string, pending []frontier.Item, resumed bool, err error,
) {
	st, err = store.OpenCrawl(storeDir, id)
	if err != nil {
		return
	}
	closeOnErr := func() { st.Close(); st = nil }
	cfgYAML, err := st.Meta("config")
	if err != nil {
		closeOnErr()
		return
	}
	cfg, err = config.Load([]byte(cfgYAML))
	if err != nil {
		closeOnErr()
		return
	}
	seeds, err = st.Seeds()
	if err != nil {
		closeOnErr()
		return
	}
	if len(seeds) == 0 {
		closeOnErr()
		err = errNoSeed(id)
		return
	}
	processed, err = st.ProcessedURLs()
	if err != nil {
		closeOnErr()
		return
	}
	pending, err = st.PendingFrontier()
	if err != nil {
		closeOnErr()
		return
	}
	return st, cfg, seeds, processed, pending, true, nil
}

type errNoSeed string

func (e errNoSeed) Error() string { return "crawl " + string(e) + " has no stored seed" }

// Pause asks the in-flight crawl to pause (left resumable); no-op when idle.
func (e *Executor) Pause() { e.signal("pause") }

// Stop asks the in-flight crawl to stop and finalise as completed; no-op when idle.
func (e *Executor) Stop() { e.signal("stop") }

func (e *Executor) signal(mode string) {
	e.mu.Lock()
	r := e.cur
	e.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.stopMode == "" {
		r.stopMode = mode
	}
	r.mu.Unlock()
	r.cancel()
}

// Snapshot returns the in-flight crawl's live progress, ok=false when idle.
func (e *Executor) Snapshot() (Snapshot, bool) {
	e.mu.Lock()
	r := e.cur
	e.mu.Unlock()
	if r == nil {
		return Snapshot{}, false
	}
	return r.snapshot(), true
}

func (r *run) snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	// live rate over a 4s sliding window
	cutoff := time.Now().Add(-4 * time.Second)
	i := 0
	for i < len(r.recent) && r.recent[i].Before(cutoff) {
		i++
	}
	r.recent = r.recent[i:]
	queueLen := r.discovered - r.total
	if queueLen < 0 {
		queueLen = 0
	}
	seed := ""
	if len(r.seeds) > 0 {
		seed = r.seeds[0]
	}
	return Snapshot{
		CrawlID: r.st.ID, Seed: seed,
		Total: r.total, Discovered: r.discovered, Queue: queueLen,
		S2xx: r.s2, S3xx: r.s3, S4xx: r.s4, S5xx: r.s5,
		Blocked: r.blocked, NoResponse: r.noresp, Indexable: r.indexable,
		RatePerSec: float64(len(r.recent)) / 4.0,
		ElapsedSec: int(time.Since(r.started).Seconds()),
		Threads:    r.threads,
	}
}

func (r *run) onPage(rec *crawler.PageRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	r.recent = append(r.recent, time.Now())
	switch rec.State {
	case crawler.StateBlockedRobots:
		r.blocked++
	case crawler.StateError:
		r.noresp++
	default:
		switch {
		case rec.StatusCode >= 500:
			r.s5++
		case rec.StatusCode >= 400:
			r.s4++
		case rec.StatusCode >= 300:
			r.s3++
		case rec.StatusCode >= 200:
			r.s2++
		}
		if rec.Indexable {
			r.indexable++
		}
	}
}

// sink tees the crawl stream: persistence first (the real store sink), then the
// executor's live counters and the surface observer's per-page hook. It forwards
// every optional sink extension the store implements (blobs, WARC, sitemaps).
type sink struct {
	inner *store.Crawl
	r     *run
	obs   Observer
}

var (
	_ crawler.Sink        = (*sink)(nil)
	_ crawler.BlobSink    = (*sink)(nil)
	_ crawler.ArchiveSink = (*sink)(nil)
	_ crawler.SitemapSink = (*sink)(nil)
)

func (t *sink) Page(rec *crawler.PageRecord) error {
	if err := t.inner.Page(rec); err != nil {
		return err
	}
	t.r.onPage(rec)
	if t.obs != nil {
		t.obs.OnPage(rec)
	}
	return nil
}

func (t *sink) FrontierAdd(it frontier.Item) error {
	if err := t.inner.FrontierAdd(it); err != nil {
		return err
	}
	t.r.mu.Lock()
	t.r.discovered++
	t.r.mu.Unlock()
	return nil
}

func (t *sink) FrontierDone(url string) error { return t.inner.FrontierDone(url) }

func (t *sink) Blob(url, kind string, data []byte) error { return t.inner.Blob(url, kind, data) }

func (t *sink) SitemapEntry(sitemap, url string) error { return t.inner.SitemapEntry(sitemap, url) }

func (t *sink) Archive(url string, res *fetch.Result) error { return t.inner.Archive(url, res) }
