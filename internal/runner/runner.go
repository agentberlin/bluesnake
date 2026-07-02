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
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/render"
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
// methods may be called from the executor's goroutines; implementations must
// not block. Every event names its crawl, so an observer watching several
// parallel crawls can keep their streams apart (a feed without the id would
// interleave pages from unrelated crawls). A nil observer is fine (headless).
type Observer interface {
	OnStart(crawlID, seed string)
	OnPage(crawlID string, rec *crawler.PageRecord) // for surfaces that build a live feed
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
	st, cfg, seeds, resume, err := e.open(ctx, spec)
	if err != nil {
		// surface the failure to the observer so callers awaiting a start (the MCP
		// backend, the CLI) always see a terminal event even when no crawl began.
		if e.obs != nil {
			e.obs.OnDone(Outcome{Err: err})
		}
		return "", err
	}

	r := &run{
		st: st, seeds: seeds, resumed: resume != nil, started: time.Now(),
		threads: cfg.Speed.MaxThreads,
	}
	if resume != nil {
		r.total = resume.processed
		r.discovered = resume.discovered
	}
	// One global limiter shared across every crawl this executor runs, so M
	// parallel crawls honour a single process-wide fetch ceiling. Fall back to a
	// per-crawl limiter from this crawl's config when none was injected. INVARIANT
	// (P17): the fallback is sound only for a dispatcher running one crawl at a
	// time — that single crawl's limiter then IS the process-wide cap. Every
	// surface that runs crawls in parallel (CLI `projects crawl-all --parallel`;
	// desktop and MCP when speed.max_concurrent_crawls > 1, via
	// runner.ProcessWiring) MUST inject one shared limiter through WithLimiter,
	// else SUM(in-flight fetches) across crawls would be unbounded.
	lim := e.lim
	if lim == nil {
		lim = limiter.New(cfg.Speed.MaxGlobalThreads, 1, render.GlobalRenderCap(cfg))
	}
	opts := []crawler.Option{
		crawler.WithSink(&sink{Crawl: st, r: r, obs: e.obs}),
		crawler.WithLimiter(lim),
	}
	if resume != nil {
		opts = append(opts, crawler.WithResume(resume.Resume))
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
	if _, dup := e.cur[st.ID]; dup {
		// The same crawl is already in flight (two resume jobs racing for one
		// crawl id). Registering would overwrite the map entry and make the
		// FIRST run unaddressable — Pause/Stop/Cancel would signal the wrong
		// one and its cleanup would delete ours (#74 N7). Refuse the duplicate.
		e.mu.Unlock()
		c.Close()
		st.Close()
		err = fmt.Errorf("crawl %s is already running — not starting a duplicate session", st.ID)
		if e.obs != nil {
			e.obs.OnDone(Outcome{CrawlID: st.ID, Err: err})
		}
		return "", err
	}
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
			Resumed:   resume != nil,
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

// open resolves a spec into an open crawl ready to run: a fresh crawl (resume
// == nil), or an existing one restored for resume (which uses the crawl's
// frozen config and seeds, not the spec).
func (e *Executor) open(ctx context.Context, spec queue.JobSpec) (
	st *store.Crawl, cfg *config.Config, seeds []string, resume *resumeState, err error,
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
// every seed (so host classification and the depth BFS re-root from all of
// them), and the complete crawler.Resume state. It is the ONLY resume-open
// path — every surface reaches it through the queue dispatcher — so its guards
// (pre-edges, completed-status, stranded-row purge, load-error refusal) hold
// structurally rather than per-surface. The spec's profile/config are ignored:
// a resume must run the crawl's own frozen config.
func openForResume(storeDir, id string) (
	st *store.Crawl, cfg *config.Config, seeds []string, resume *resumeState, err error,
) {
	// A completed crawl has nothing to resume; accepting it would briefly
	// de-complete the registry row (finalize records the interim interrupted
	// status first) and, on a later failure, leave it that way (#74 N9).
	status, err := store.CrawlStatus(storeDir, id)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if status == store.StatusCompleted {
		return nil, nil, nil, nil, errCompleted(id)
	}
	st, err = store.OpenCrawl(storeDir, id)
	if err != nil {
		return nil, nil, nil, nil, err
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
	// A crash between Page() and FrontierDone() strands a pages∩frontier pair
	// (EC-02). PendingFrontier skips it, but left in place it double-counts in
	// the admitted-set rehydration below and accretes across resumes (#74 N14).
	if _, err = st.PurgeStrandedFrontier(); err != nil {
		closeOnErr()
		return
	}
	r, err := loadResume(st, cfg.Limits.AnyBucketCap())
	if err != nil {
		closeOnErr()
		return
	}
	return st, cfg, seeds, &r, nil
}

// resumeSource is the store surface loadResume reads (satisfied by *store.Crawl).
type resumeSource interface {
	PageCount() (int, error)
	Count() (int, error)
	FetchedCount() (int, error)
	MaxEdgeSeq() (int64, error)
	AdmittedItems() ([]frontier.Item, error)
}

// resumeState is the loader's result: the engine's plain-data Resume plus the
// live-counter seeds the runner's progress starts from.
type resumeState struct {
	crawler.Resume
	processed  int // recorded pages — seeds the live "total" counter
	discovered int // admitted URLs (frontier ∪ pages) — seeds "discovered"
}

// loadResume assembles the resume state from the store. Any load error refuses
// the resume — silently degrading (e.g. restarting the edge seq at 0 on a
// MaxEdgeSeq read error) would corrupt first-wins discovered_from exactly the
// way the missing-capability bug did (#74 N15). The processed and pending URL
// SETS are deliberately not materialised (issue #77): the store is both the
// dedup authority (its pages rows are the visited set; MarkSeen is a no-op)
// and the work-queue authority (the engine's Recover resets the pending rows'
// orphaned claims and the feeder pulls them straight from the table), so
// loading either slice would put a crawl- or frontier-sized copy back in RAM —
// exactly the term this design removes. Only their counts are read, to seed
// the live progress counters. The admitted set is loaded only when a
// per-bucket cap is configured; it is dead weight otherwise.
func loadResume(src resumeSource, needAdmitted bool) (resumeState, error) {
	var r resumeState
	var err error
	if r.processed, err = src.PageCount(); err != nil {
		return resumeState{}, fmt.Errorf("resume: load processed count: %w", err)
	}
	if r.discovered, err = src.Count(); err != nil {
		return resumeState{}, fmt.Errorf("resume: load discovered count: %w", err)
	}
	if r.Fetched, err = src.FetchedCount(); err != nil {
		return resumeState{}, fmt.Errorf("resume: load fetched count: %w", err)
	}
	if r.MaxEdgeSeq, err = src.MaxEdgeSeq(); err != nil {
		return resumeState{}, fmt.Errorf("resume: load edge sequence: %w", err)
	}
	if needAdmitted {
		if r.Admitted, err = src.AdmittedItems(); err != nil {
			return resumeState{}, fmt.Errorf("resume: load admitted set: %w", err)
		}
	}
	return r, nil
}

type errNoSeed string

func (e errNoSeed) Error() string { return "crawl " + string(e) + " has no stored seed" }

// errCompleted is returned when a resume targets a crawl that already ran to
// completion — there is no pending tail, and re-opening it would de-complete
// the registry row.
type errCompleted string

func (e errCompleted) Error() string {
	return "crawl " + string(e) + " is already completed — nothing to resume (re-crawl for a fresh audit)"
}

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

// SnapshotCrawl returns one specific in-flight crawl's live progress;
// ok=false when that crawl is not running.
func (e *Executor) SnapshotCrawl(crawlID string) (Snapshot, bool) {
	e.mu.Lock()
	r := e.cur[crawlID]
	e.mu.Unlock()
	if r == nil {
		return Snapshot{}, false
	}
	return r.snapshot(), true
}

// Snapshots returns a live progress reading for every in-flight crawl, oldest
// start first (stable across ticks); empty when idle.
func (e *Executor) Snapshots() []Snapshot {
	e.mu.Lock()
	runs := make([]*run, 0, len(e.cur))
	for _, r := range e.cur {
		runs = append(runs, r)
	}
	e.mu.Unlock()
	// started is set once at construction, so reading it lock-free here is safe.
	sort.Slice(runs, func(i, j int) bool { return runs[i].started.Before(runs[j].started) })
	out := make([]Snapshot, len(runs))
	for i, r := range runs {
		out[i] = r.snapshot()
	}
	return out
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
// executor's live counters and the surface observer's per-page hook. It EMBEDS
// *store.Crawl, so every optional store capability the engine sniffs — Blob,
// Archive, SitemapEntry, LlmsTxt, FirstWithContent, the frontier.Dedup methods,
// and anything added later — forwards by promotion; only the methods the
// executor intercepts for live counters (Page, Admit, Remove) are overridden.
// The wrapper used to hand-forward each capability instead, and every method it
// missed became a silent per-surface data-loss bug: frontier.Dedup (the #70 C1
// resume loss), and LlmsTxtSink (llms.txt audit rows recorded on the CLI path
// only) — the class #74 closed. Promotion flips the failure mode of a missed
// method from silent corruption to, at worst, a missed live-counter intercept.
type sink struct {
	*store.Crawl
	r   *run
	obs Observer
}

// Compile-time pins: documentation of the capability surface the production
// sink must carry. With embedding they can no longer fail for a capability the
// store implements; they still catch a store-side capability removal.
var (
	_ crawler.Sink        = (*sink)(nil)
	_ crawler.BlobSink    = (*sink)(nil)
	_ crawler.ArchiveSink = (*sink)(nil)
	_ crawler.SitemapSink = (*sink)(nil)
	_ crawler.LlmsTxtSink = (*sink)(nil)
	_ crawler.ContentSink = (*sink)(nil) // the identical-content authority must reach the store, not the in-RAM fallback
	_ frontier.Dedup      = (*sink)(nil)
	// The work-queue authority (issue #77): without it the engine falls back to
	// the frontier-linear in-RAM queue — the bounded-RAM contract silently gone.
	_ frontier.Queue = (*sink)(nil)
)

func (t *sink) Page(rec *crawler.PageRecord) error {
	if err := t.Crawl.Page(rec); err != nil {
		return err
	}
	t.r.onPage(rec)
	if t.obs != nil {
		t.obs.OnPage(t.Crawl.ID, rec)
	}
	return nil
}

// --- frontier.Dedup live-counter intercepts ----------------------------------
// The store is the visited-set authority on every surface; admission happens in
// its Admit (the durable frontier write), so this is also where the live
// Discovered counter advances (M1). The other Dedup methods (Seen, MarkSeen,
// Count) forward by promotion.

// Admit records a novel URL as a durable frontier row (the resume authority) and,
// on a first admission, advances Discovered. A re-discovered URL (already a
// frontier or pages row) returns first=false and is not counted again.
func (t *sink) Admit(it frontier.Item) (first bool, err error) {
	first, err = t.Crawl.Admit(it)
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
	if err := t.Crawl.Remove(url); err != nil {
		return err
	}
	t.r.mu.Lock()
	if t.r.discovered > 0 {
		t.r.discovered--
	}
	t.r.mu.Unlock()
	return nil
}
