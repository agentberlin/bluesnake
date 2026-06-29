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
	"github.com/agentberlin/bluesnake/internal/limiter"
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

// Executor runs crawls for a single surface. It can hold several in flight at
// once (parallel multi-crawl); each is keyed by its crawl id so Pause/Stop/
// Snapshot can address one specific crawl. The no-argument Pause/Stop fan out to
// every in-flight crawl (used by Shutdown), preserving single-crawl behaviour.
type Executor struct {
	storeDir string
	obs      Observer
	lim      *limiter.Limiter // shared process-wide fetch cap across all crawls

	mu  sync.Mutex
	cur map[string]*run // crawl id -> in-flight crawl
}

// New builds an executor rooted at storeDir. obs may be nil. The optional limiter
// caps total concurrent fetches across every crawl this executor runs (nil =
// unlimited); it is shared, so M parallel crawls honour one global ceiling.
func New(storeDir string, obs Observer, opts ...Option) *Executor {
	e := &Executor{storeDir: storeDir, obs: obs, cur: map[string]*run{}}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Option configures an Executor.
type Option func(*Executor)

// WithLimiter shares one process-wide concurrency limiter across every crawl the
// executor runs (the parallel-mode global fetch ceiling).
func WithLimiter(l *limiter.Limiter) Option {
	return func(e *Executor) { e.lim = l }
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
	// One global limiter shared across every crawl this executor runs, so M
	// parallel crawls honour a single process-wide fetch ceiling. Fall back to a
	// per-crawl limiter from this crawl's config when none was injected. INVARIANT
	// (P17): the fallback is sound only because the surfaces that omit WithLimiter
	// (MCP, desktop) run one crawl at a time — that single crawl's limiter then IS
	// the process-wide cap. Any surface that runs crawls in parallel (the CLI's
	// `projects crawl-all --parallel`) MUST inject one shared limiter, else
	// SUM(in-flight fetches) across crawls would be unbounded.
	lim := e.lim
	if lim == nil {
		lim = limiter.New(cfg.Speed.MaxGlobalThreads, 1)
	}
	opts := []crawler.Option{
		crawler.WithSink(&sink{inner: st, r: r, obs: e.obs}),
		crawler.WithLimiter(lim),
	}
	if resumed {
		opts = append(opts, crawler.WithResume(processed, pending))
	}
	c, err := crawler.New(cfg, opts...)
	if err != nil {
		// crawler.New failed after open() registered the crawl as running, so
		// finalize will never run for it. Mark the row terminal here, otherwise
		// it is orphaned at "running" forever (the dispatcher has moved on).
		markTerminal(e.storeDir, st, store.StatusInterrupted)
		st.Close()
		if e.obs != nil {
			e.obs.OnDone(Outcome{CrawlID: st.ID, Status: store.StatusInterrupted, Err: err})
		}
		return "", err
	}
	r.c = c
	var runCtx context.Context
	runCtx, r.cancel = context.WithCancel(ctx)

	e.mu.Lock()
	e.cur[st.ID] = r
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.cur, st.ID)
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
		// Bound how many crawls materialise a finalize/analysis working set at once
		// (§5.6 item 2 / H2): the CSR + analysis passes are CPU+RAM-bursty, so M
		// parallel crawls finishing together must not each build one simultaneously.
		// Acquire on a fresh context — NOT runCtx — because a paused/stopped crawl
		// (runCtx already cancelled) must still persist its aggregates and terminal
		// status here; finalize always releases its slot, so this never blocks for
		// long. A nil limiter makes Acquire/Release no-ops (single-crawl default).
		finCtx := context.Background()
		lim.AcquireFinalize(finCtx)
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
		lim.ReleaseFinalize()
		out.Status, out.Crawled, out.Total, out.Analyzed = fo.Status, fo.Crawled, fo.Total, fo.Analyzed
		out.Chains, out.NearDups, out.IssueTotal, out.IssueChecks = fo.Chains, fo.NearDups, fo.IssueTotal, fo.IssueChecks
		if ferr != nil && out.Err == nil {
			out.Err = ferr
		}
	} else {
		// No Result means the crawler errored before producing one (e.g. a seed
		// or scope failure), so finalize never ran. Persist the terminal status
		// directly so the registry row isn't orphaned at "running".
		if serr := markTerminal(e.storeDir, st, store.StatusInterrupted); serr != nil && out.Err == nil {
			out.Err = serr
		}
	}
	if e.obs != nil {
		e.obs.OnDone(out)
	}
	return out.Status, out.Err
}

// markTerminal records a terminal registry status for a crawl that ended before
// finalize ran — a startup/seed error after the crawl row was already created.
// It mirrors finalize's authoritative counts (read from the stored graph, best
// effort) so the row stops claiming to be running.
func markTerminal(storeDir string, st *store.Crawl, status string) error {
	crawled, total := 0, 0
	if c, t, err := st.Counts(); err == nil {
		crawled, total = c, t
	}
	return store.SetStatus(storeDir, st.ID, status, crawled, total)
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
	// A crawl created before the gated `edges` table existed cannot be safely
	// resumed: the SQL finalize derives inlinks/discovered_from solely from edges,
	// which is empty for such a crawl, so completing it would overwrite both with
	// empty/partial values. Refuse loudly (re-crawl) rather than silently corrupt;
	// reading/querying the completed crawl is unaffected (P3).
	if pre, perr := st.PreEdges(); perr != nil {
		err = perr
		closeOnErr()
		return
	} else if pre {
		err = errPreEdges(id)
		closeOnErr()
		return
	}
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

// errPreEdges is returned when a resume targets a crawl that predates the gated
// edges table — it cannot be finalized without corrupting inlinks/discovered_from,
// so the user must re-crawl.
type errPreEdges string

func (e errPreEdges) Error() string {
	return "crawl " + string(e) + " predates this version's link-graph format and cannot be resumed — please re-crawl"
}

// Pause asks every in-flight crawl to pause (left resumable); no-op when idle.
// Used by the dispatcher's Shutdown to turn all running crawls around.
func (e *Executor) Pause() { e.signalAll("pause") }

// Stop asks every in-flight crawl to stop and finalise as completed.
func (e *Executor) Stop() { e.signalAll("stop") }

// PauseCrawl pauses one specific crawl (left resumable); no-op if not running.
func (e *Executor) PauseCrawl(crawlID string) { e.signalCrawl(crawlID, "pause") }

// StopCrawl stops one specific crawl, finalising it as completed.
func (e *Executor) StopCrawl(crawlID string) { e.signalCrawl(crawlID, "stop") }

// signalCrawl latches the first stop mode for one crawl and cancels its context.
// First-wins: a pause already requested is not upgraded to a stop (or vice versa).
func (e *Executor) signalCrawl(crawlID, mode string) {
	e.mu.Lock()
	r := e.cur[crawlID]
	e.mu.Unlock()
	signalRun(r, mode)
}

func (e *Executor) signalAll(mode string) {
	e.mu.Lock()
	runs := make([]*run, 0, len(e.cur))
	for _, r := range e.cur {
		runs = append(runs, r)
	}
	e.mu.Unlock()
	for _, r := range runs {
		signalRun(r, mode)
	}
}

func signalRun(r *run, mode string) {
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

// Snapshot returns one in-flight crawl's live progress (any, for single-crawl
// surfaces), ok=false when idle.
func (e *Executor) Snapshot() (Snapshot, bool) {
	e.mu.Lock()
	var r *run
	for _, v := range e.cur {
		r = v
		break
	}
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
// executor's live counters and the surface observer's per-page hook. It also IS
// the crawl's frontier dedup authority (MEMORY-SCALING.md §5.1): forwarding the
// five Dedup methods to the store makes store.Admit the on-disk visited-set
// authority on every production surface. That is what persists pending frontier
// rows (so a paused crawl resumes without losing pages) and bounds the visited
// set to disk — previously this struct didn't satisfy frontier.Dedup, so the
// engine silently fell back to the in-RAM set and never wrote frontier rows,
// losing pages on resume (the C1 regression). It forwards every optional sink
// extension the store implements (blobs, WARC, sitemaps).
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
	_ crawler.ContentSink = (*sink)(nil) // the identical-content authority must reach the store, not the in-RAM fallback
	_ frontier.Dedup      = (*sink)(nil)
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

func (t *sink) FrontierDone(url string) error { return t.inner.FrontierDone(url) }

// --- frontier.Dedup: the store is the visited-set authority on every surface ---
// Admission now happens here (not via a separate FrontierAdd), so this is also
// where the live Discovered counter advances — fixing the frozen-at-0 progress
// on MCP/desktop (M1). Admit/Remove/Seen/MarkSeen/Count forward to the store;
// the engine's frontier calls them outside its cap mutex.

// Admit records a novel URL as a durable frontier row (the resume authority) and,
// on a first admission, advances Discovered. A re-discovered URL (already a
// frontier or pages row) returns first=false and is not counted again.
func (t *sink) Admit(it frontier.Item) (first bool, err error) {
	first, err = t.inner.Admit(it)
	if err == nil && first {
		t.r.mu.Lock()
		t.r.discovered++
		t.r.mu.Unlock()
	}
	return first, err
}

// Remove undoes a just-admitted row (the frontier's cap-overflow rollback), so it
// also rolls back the Discovered bump Admit made for it.
func (t *sink) Remove(url string) error {
	if err := t.inner.Remove(url); err != nil {
		return err
	}
	t.r.mu.Lock()
	if t.r.discovered > 0 {
		t.r.discovered--
	}
	t.r.mu.Unlock()
	return nil
}

func (t *sink) Seen(url string) (bool, error) { return t.inner.Seen(url) }
func (t *sink) MarkSeen(urls []string) error  { return t.inner.MarkSeen(urls) }
func (t *sink) Count() (int, error)           { return t.inner.Count() }

// FirstWithContent delegates the raw-body identical-content authority to the
// store's content_hash table (crawler.ContentSink). Without this the engine fell
// back to its in-RAM seenContent map on every surface — the #70 M4 bound was inert
// in production, and a resume's cold map re-minted a second canonical for content
// already claimed in the prior session (P11).
func (t *sink) FirstWithContent(hash, url string, claim bool) (string, bool, error) {
	return t.inner.FirstWithContent(hash, url, claim)
}

func (t *sink) Blob(url, kind string, data []byte) error { return t.inner.Blob(url, kind, data) }

func (t *sink) SitemapEntry(sitemap, url string) error { return t.inner.SitemapEntry(sitemap, url) }

func (t *sink) Archive(url string, res *fetch.Result) error { return t.inner.Archive(url, res) }
