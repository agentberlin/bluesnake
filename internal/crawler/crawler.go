// Package crawler is the orchestrator (DESIGN.md §5.2): a bounded worker
// pool runs the fetch → parse → evaluate → discover pipeline over the
// frontier, stringing together urlutil (discovery filter chain), robots,
// fetch, parse and indexability. Results are in-memory for now; the SQLite
// store and resume arrive with the storage milestone.
package crawler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/extract"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/indexability"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/minhash"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/render"
	"github.com/agentberlin/bluesnake/internal/structured"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// Crawl states.
const (
	StateCrawled         = "crawled"
	StateBlockedRobots   = "blocked_robots"
	StateError           = "error"
	StateSkippedTooLarge = "skipped_too_large"
)

// PageRecord is everything recorded for one URL.
type PageRecord struct {
	URL                string
	Scope              string // internal | external
	State              string
	Depth              int
	StatusCode         int
	Status             string
	ContentType        string
	HTTPVersion        string // negotiated protocol, e.g. HTTP/1.1, HTTP/2.0
	ResponseTimeMs     int64
	Size               int
	FetchError         string
	RedirectURL        string
	RedirectType       string // http | hsts | meta_refresh
	MatchedRobotsLine  int
	Indexable          bool
	IndexabilityStatus string
	Headers            map[string]string    // response headers (first value each)
	Facts              *parse.Facts         // nil for non-HTML and external pages
	CustomResults      []extract.Result     // custom search/extraction values
	StructuredData     *structured.PageData // schema.org extraction + validation
	JSDiff             *JSDiff              // raw-vs-rendered comparison (JS rendering mode)
	Inlinks            int
	DiscoveredFrom     string // first discovering page
	OutsideStartFolder bool
	// GatedEdges are this page's followed discovery edges (rewritten/admitted
	// targets), persisted to the edges table so finalize can derive inlinks/
	// depth/discovered_from in SQL. Populated at crawl time, dropped after persist.
	GatedEdges  []GatedEdge
	DuplicateOf string // canonical URL when this page's raw body was byte-identical to an already-crawled one (not rendered/expanded)
	// Minhash is the content-similarity signature (minhash.Encode), computed at
	// crawl time and persisted to the pages.minhash column WHEN near-duplicate
	// analysis is enabled, so near-dup reads it instead of re-materialising
	// ContentText (MEMORY-SCALING.md §5.5). Empty when near-dup is off.
	Minhash []byte

	// analysis outputs (populated when loaded from a store after analyze)
	LinkScore         float64
	UniqueInlinks     int
	UniqueOutlinks    int
	ClosestSimilarity float64
}

// GatedEdge is one followed discovery edge from a page: a REWRITTEN/admitted
// target (the link as the crawler resolved it, not the raw href) plus whether it
// is a hyperlink — the subset that counts towards inlinks. Seq is the monotonic
// crawl-order rank used for run-to-run-stable first-wins discovered_from.
type GatedEdge struct {
	Dst       string
	Hyperlink bool
	Seq       int64
}

// JSDiff captures raw-vs-rendered differences in JavaScript rendering mode
// (the JavaScript tab data).
type JSDiff struct {
	RenderedWordCount  int      `json:"rendered_word_count"`
	WordCountChange    int      `json:"word_count_change"`
	TitleChanged       bool     `json:"title_changed,omitempty"`
	RenderedTitle      string   `json:"rendered_title,omitempty"`
	DescriptionChanged bool     `json:"description_changed,omitempty"`
	H1Changed          bool     `json:"h1_changed,omitempty"`
	CanonicalChanged   bool     `json:"canonical_changed,omitempty"`
	RenderedCanonical  string   `json:"rendered_canonical,omitempty"`
	NoindexOnlyRaw     bool     `json:"noindex_only_raw,omitempty"`
	JSLinks            int      `json:"js_links,omitempty"` // links only in the rendered DOM
	ConsoleErrors      []string `json:"console_errors,omitempty"`
	// StructuredJSOnly lists schema.org types present in the rendered DOM but
	// absent from the raw HTML — structured data injected by JavaScript. It is
	// invisible to consumers that don't render (most crawlers and LLMs), so it
	// is surfaced as a warning even though bluesnake itself recovers it (R18).
	StructuredJSOnly []string `json:"structured_js_only,omitempty"`
}

// Sink receives crawl output as it is produced (the store implements this).
// Pending frontier rows are persisted by the dedup authority's Admit (the store
// when present, see frontier.Dedup), not a separate sink call; FrontierDone marks
// a row processed so an interrupted crawl resumes exactly where it stopped.
type Sink interface {
	Page(*PageRecord) error
	FrontierDone(url string) error
}

// BlobSink is the optional sink extension for stored page sources and
// screenshots (extraction.store_html / rendering screenshots).
type BlobSink interface {
	Blob(url, kind string, data []byte) error
}

// ArchiveSink is the optional sink extension for WARC archiving
// (extraction.store_warc): every fetched response — any status, including
// redirects and errors pages, but not robots-blocked URLs or transport
// failures — is offered for archiving.
type ArchiveSink interface {
	Archive(url string, res *fetch.Result) error
}

// ContentSink is the optional sink extension for the on-disk identical-content
// authority (the content_hash table). When a sink implements it, the crawler
// delegates the raw-body byte-identical short-circuit (skip_identical_content_links)
// to it instead of an in-RAM map — so the visited-content set stays bounded
// (#70 M4) AND survives a resume (the canonical owner persists). A sink that omits
// it (library/tests) keeps the in-memory set.
type ContentSink interface {
	// FirstWithContent reports the canonical URL for a raw-body hash and whether
	// url is the first page seen with it. claim=false records nothing — a page that
	// will not expand must not become canonical and shadow a later in-folder twin.
	FirstWithContent(hash, url string, claim bool) (canonical string, first bool, err error)
}

// pageRenderer is the engine's view of the JS renderer; *render.Renderer is the
// production implementation (constructed per crawl in New). The seam exists so
// the render-slot bounding (REN-01/#76) is testable without Chrome — tests
// inject a fake via withRenderer that observes render concurrency.
type pageRenderer interface {
	Render(ctx context.Context, url string) (*render.Result, error)
	Close()
}

// Option configures a Crawler.
type Option func(*Crawler)

// WithSink streams pages and frontier mutations into a persistent store.
func WithSink(s Sink) Option { return func(c *Crawler) { c.sink = s } }

// withRenderer injects a renderer, bypassing the Chrome-backed render.New in
// New. Test seam only: production keeps one *render.Renderer per crawl.
func withRenderer(r pageRenderer) Option { return func(c *Crawler) { c.renderer = r } }

// WithFetchOptions passes options to the underlying HTTP client.
func WithFetchOptions(opts ...fetch.Option) Option {
	return func(c *Crawler) { c.fetchOpts = opts }
}

// WithLimiter injects the process-wide concurrency limiter (shared across every
// running crawl) so total in-flight fetches stay under speed.max_global_threads.
// Omit it (or pass nil) for an unbounded single crawl — the default.
func WithLimiter(l *limiter.Limiter) Option {
	return func(c *Crawler) { c.limiter = l }
}

// Resume is the complete preseeded state of a stored crawl, loaded by the
// caller (the runner's single resume-open path) and handed to the engine as
// plain data. The engine performs NO capability sniffing on its sink to
// reconstruct resume state — the #74 R1/R2/R3 class, where each production
// surface silently carried a different subset of the resume fix, lived in
// exactly those anonymous type-assertions.
type Resume struct {
	// Processed lists every URL already recorded (never re-fetched) — for the
	// IN-MEMORY dedup path only. A durable dedup authority already knows its
	// pages rows (its MarkSeen is a no-op), so loaders backed by one leave this
	// nil rather than materialise a crawl-sized slice (issue #77).
	Processed []string
	// Fetched is how many MaxURLs fetch slots the prior session(s) consumed —
	// recorded pages MINUS robots-blocked ones, which record without a fetch
	// (store.FetchedCount). It seeds the cumulative MaxURLs budget; seeding
	// from the processed count would over-charge blocked pages (#74 N11).
	Fetched int
	// Pending is the admitted-but-unprocessed frontier tail to re-queue — for
	// the IN-MEMORY queue path only. A durable work-queue authority still
	// holds those rows (Recover resets their claims); loaders backed by one
	// leave this nil rather than materialise the frontier tail (issue #77).
	Pending []frontier.Item
	// MaxEdgeSeq continues the gated-edge sequence past the prior session, so
	// MIN(seq) first-wins discovered_from stays stable across the resume.
	MaxEdgeSeq int64
	// Admitted replays the full admitted set through the frontier's per-bucket
	// counters (FR-08). Loaded only when a bucket cap is configured
	// (config.LimitsConfig.AnyBucketCap); empty otherwise.
	Admitted []frontier.Item
}

// WithResume preseeds the crawler from a stored crawl: processed URLs are
// never re-fetched; pending items re-enter the frontier; the edge sequence and
// per-bucket admission counters continue where the prior session stopped.
func WithResume(r Resume) Option {
	return func(c *Crawler) { c.resume = r }
}

// Result is the outcome of a crawl. Page records are streamed to the Sink as the
// crawl runs and are NOT retained here (stream-and-drop, MEMORY-SCALING.md §5.4);
// finalize reads them — and every per-page aggregate (inlinks, first-wins
// discovered_from, depth) — back from the store's gated `edges`/`links` tables, so
// the Result carries only process-wide counters, not a frontier-sized map.
type Result struct {
	Crawled     int // URLs fetched (state == crawled); excludes robots-blocked/errored
	Total       int // all URLs recorded (crawled + robots-blocked + errored): SF's "URLs Encountered"
	Interrupted bool
	Duration    time.Duration
}

type Crawler struct {
	cfg    *config.Config
	client *fetch.Client
	opts   urlutil.Options

	scope       *urlutil.Scope
	seedAuth    map[string]bool // list mode: every seed authority is internal
	rewriter    *urlutil.Rewriter
	filter      *urlutil.Filter
	robots      *robotsMgr
	frontier    *frontier.Frontier
	queue       frontier.Queue // work-queue authority behind the bounded ready-buffer (§5.2)
	startFolder string

	tokens  chan struct{}
	fetched atomic.Int64

	sink          Sink
	renderer      pageRenderer
	extractEngine *extract.Engine
	fetchOpts     []fetch.Option
	limiter       *limiter.Limiter // process-wide fetch cap; nil ⇒ unlimited
	resume        Resume
	sinkErrOnce   sync.Once
	sinkErr       error

	// Page records are streamed straight to the sink and never retained
	// (stream-and-drop); these atomic tallies replace counting over a held map.
	crawledCount atomic.Int64
	totalCount   atomic.Int64
	// edgeSeq assigns a monotonic crawl-order rank to each gated edge, so a
	// SQL MIN(seq) first-wins discovered_from is run-to-run stable.
	edgeSeq atomic.Int64

	hashMu      sync.Mutex
	seenContent map[string]string // raw-body content hash -> first (canonical) URL

	sitemapMu    sync.Mutex
	sitemapHosts map[string]bool // authority -> sitemap auto-discovery already run (R17)
}

func New(cfg *config.Config, opts ...Option) (*Crawler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c := &Crawler{cfg: cfg}
	for _, opt := range opts {
		opt(c)
	}
	client, err := fetch.New(cfg, c.fetchOpts...)
	if err != nil {
		return nil, err
	}
	robots, err := newRobotsMgr(cfg, client)
	if err != nil {
		return nil, err
	}
	c.extractEngine, err = extract.New(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Rendering.Mode == "javascript" && c.renderer == nil {
		r, err := render.New(cfg)
		if err != nil {
			return nil, err
		}
		c.renderer = r
	}
	uopts := urlutil.Options{
		KeepFragments: cfg.Advanced.CrawlFragments,
		LowercaseHex:  cfg.Advanced.PercentEncoding == "lower",
	}
	var replaces []urlutil.RegexReplace
	for _, rr := range cfg.URLRewriting.RegexReplace {
		replaces = append(replaces, urlutil.RegexReplace{
			Pattern: mustCompile(rr.Pattern), Replace: rr.Replace,
		})
	}
	c.client = client
	c.opts = uopts
	c.rewriter = urlutil.NewRewriter(cfg.URLRewriting.RemoveParams, replaces, cfg.URLRewriting.Lowercase, uopts)
	c.filter = urlutil.NewFilter(cfg.Scope.IncludeRE(), cfg.Scope.ExcludeRE())
	c.robots = robots
	// When the sink also implements frontier.Dedup (the SQLite store does), it is
	// the on-disk dedup authority (frontier ∪ pages), so the unbounded in-memory
	// visited set is dropped — the bounded-RAM contract. CONTRACT: every PERSISTENT
	// production sink MUST implement frontier.Dedup; the silent in-RAM memDedup
	// fallback below is intended ONLY for library/no-persistence sinks (small crawls,
	// no resume). The production sinks enforce this at compile time, not by luck:
	// runner.sink pins `_ frontier.Dedup = (*sink)(nil)` (the C1 fix), so a sink that
	// drops Dedup fails to build rather than silently degrading to the unbounded set.
	var dedup frontier.Dedup
	if d, ok := c.sink.(frontier.Dedup); ok {
		dedup = d
		// The work-queue authority rides the same sink (issue #77, §5.2): the
		// durable dedup's Admit writes the born-claimed frontier rows that the
		// queue publishes (Enqueue) and hands back out (ClaimBatch), so the two
		// capabilities only mean anything together — a sink that deduplicates
		// on disk but queues in RAM keeps half the frontier state resident, and
		// a durable queue over an in-RAM dedup would claim rows nothing ever
		// inserted. The store implements both; runner.sink compile-pins both.
		if q, ok := c.sink.(frontier.Queue); ok {
			c.queue = q
		}
	}
	if c.queue == nil {
		// No durable authority: the in-RAM FIFO preserves the engine's
		// behaviour for bare library/test sinks (frontier-linear by design).
		c.queue = &memQueue{}
	}
	// Dedup-authority errors flow into the crawl's sink-error latch: a store
	// failure declines the URL (conservative) AND fails the run — silent drops
	// would report an incomplete crawl as success (#74 R6/D4).
	c.frontier = frontier.New(cfg, frontier.WithDedup(dedup), frontier.WithErrorSink(c.noteSinkErr))
	c.seenContent = make(map[string]string)
	c.sitemapHosts = make(map[string]bool)
	return c, nil
}

// Run crawls from the seeds until the frontier drains, the URL limit is
// reached, or the context is cancelled (graceful stop). Spider mode uses one
// seed; list mode passes many. Scope derives from the first seed.
func (c *Crawler) Run(ctx context.Context, seedsRaw ...string) (*Result, error) {
	start := time.Now()
	if len(seedsRaw) == 0 {
		return nil, fmt.Errorf("crawler: no seed URLs")
	}
	seeds := make([]string, 0, len(seedsRaw))
	for _, raw := range seedsRaw {
		seed, err := urlutil.Normalize(raw, c.opts)
		if err != nil {
			return nil, err
		}
		seeds = append(seeds, seed)
	}
	var err error
	c.scope, err = urlutil.NewScope(seeds[0], c.cfg.Scope.CrawlAllSubdomains, c.cfg.Scope.CDNs)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 1 && c.cfg.Mode != "list" {
		c.startFolder = startFolderOf(seeds[0])
	}
	if c.cfg.Mode == "list" {
		c.seedAuth = make(map[string]bool, len(seeds))
		for _, s := range seeds {
			c.seedAuth[urlutil.Authority(s)] = true
		}
	}

	if rate := c.cfg.Speed.MaxURLsPerSec; rate > 0 {
		c.tokens = make(chan struct{}, 1)
		ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
		defer ticker.Stop()
		// The feeder exits when Run returns (ticker.Stop does not close the
		// channel, so `for range ticker.C` would leak one goroutine per Run —
		// #74 N12; processes are long-lived multi-crawl now).
		feederDone := make(chan struct{})
		defer close(feederDone)
		go func() {
			for {
				select {
				case <-ticker.C:
					select {
					case c.tokens <- struct{}{}:
					default:
					}
				case <-feederDone:
					return
				}
			}
		}()
	}

	// Resume state arrives as plain data (WithResume), loaded by the caller from
	// the store — the engine never sniffs its sink for resume capabilities (the
	// #74 R1/R2/R3 class of surface-asymmetric bugs lived in those assertions).
	c.frontier.MarkSeen(c.resume.Processed)
	// Resume the crawl-total budget cumulatively: the MaxURLs limit (checked via
	// c.fetched below) is a fetch counter that starts at zero each session, so
	// without this seed every resumed session would grant a fresh MaxURLs budget
	// and a paused-then-resumed crawl could fetch far more than a straight one.
	// Seeded from the slots the prior sessions actually consumed (not the page
	// count — robots-blocked pages record without a fetch, #74 N11).
	c.fetched.Store(int64(c.resume.Fetched))
	// Continue the gated-edge seq past the prior session so resume's new edges
	// sort after session-1's: MIN(seq) first-wins discovered_from stays stable.
	c.edgeSeq.Store(c.resume.MaxEdgeSeq)
	// Rehydrate the per-bucket admission counters from the stored admitted set so
	// this resumed session enforces per-depth / per-subdomain / per-path caps
	// against the totals the earlier session(s) accrued, instead of restarting
	// each bucket at zero and over-admitting (FR-08 / MEMORY-SCALING.md §5.1).
	// The loader supplies Admitted only when such a cap is configured.
	if len(c.resume.Admitted) > 0 {
		c.frontier.RehydrateCounters(c.resume.Admitted)
	}

	// Bounded worker pool (MEMORY-SCALING.md §5.2/§5.3, issue #77): N persistent
	// workers drain a bounded in-RAM ready-buffer that a single feeder goroutine
	// refills from the work-queue authority in deterministic (depth, seq)
	// batches. The buffer is a fixed WINDOW over the frontier, not the frontier:
	// an admitted item lives durably in the authority (the store) until the
	// feeder claims it, so per-crawl RAM no longer scales with the discovered
	// frontier. The §11 #1 termination trap is honoured one level down from the
	// old push-before-done: a worker PUBLISHES its admitted discoveries durably
	// (queue.Enqueue) BEFORE done() decrements its own item, so in-flight can
	// only reach zero once every reachable item is visible to the feeder.
	n := c.cfg.Speed.MaxThreads
	if n < 1 {
		n = 1
	}
	pool := newWorkPool(c.queue, n, c.noteSinkErr)
	// Recover the queue FIRST — before anything is admitted and before the
	// feeder's first claim: orphaned in-flight claims from a crash or pause
	// become claimable exactly once (EC-01), structurally, on every surface.
	// The in-memory authority re-enqueues the loader-supplied pending items
	// instead (its queue died with the process).
	if err := c.queue.Recover(c.resume.Pending); err != nil {
		return nil, fmt.Errorf("crawler: recover pending frontier: %w", err)
	}
	enqueue := func(item frontier.Item) {
		// Admit is the dedup + limit gate. With a store-backed dedup it also
		// writes the durable frontier row (the admission authority), born
		// claimed — invisible to the feeder until the caps have passed.
		if !c.frontier.Admit(item) {
			return
		}
		// Publish the fully-admitted item as claimable work. On error the URL
		// is lost to this session — surfaced, never silent (#74 R6/D4); the
		// still-claimed row resurfaces as pending via the next Recover.
		if err := c.queue.Enqueue(item); err != nil {
			c.noteSinkErr(err)
			return
		}
		pool.notify()
	}
	for _, seed := range seeds {
		enqueue(frontier.Item{URL: seed, Depth: 0})
	}
	// list mode audits exactly the supplied URLs — sitemaps are an input
	// source there (--sitemap), never an extra discovery channel
	if c.cfg.Sitemaps.CrawlLinked && c.cfg.Mode != "list" {
		for _, item := range c.crawlSitemaps(ctx, seeds[0]) {
			enqueue(item)
		}
	}
	// /llms.txt is a site-level file (like robots.txt) fetched once for the seed
	// host; its curated links are validated against the crawl in analysis. List
	// mode audits exactly the supplied URLs, so it is skipped there.
	if c.cfg.LlmsTxt.Check && c.cfg.Mode != "list" {
		for _, item := range c.crawlLlmsTxt(ctx, seeds[0]) {
			enqueue(item)
		}
	}
	// A page first linked before an interrupt keeps its true (session-1)
	// discovered_from across resume because that discovery edge persists in the
	// store's gated `edges` table; finalize's first-wins (seq-MIN) read recovers it
	// — no in-RAM seeding needed (the dropped c.inlinks map is gone).
	//
	// Resume's pending rows are ALREADY admitted (they survive in the frontier
	// table / the in-memory set), so Admit would dedup-reject them. Readmit
	// re-records them in the dedup set without consuming limit budget; their
	// re-QUEUEING was Recover's job above (durable rows reset to claimable, or
	// the in-memory queue re-seeded).
	for _, item := range c.resume.Pending {
		c.frontier.Readmit(item)
	}

	// Start the single producer only now, AFTER every pre-run enqueue (seeds,
	// sitemaps, llms.txt, Recover): an earlier start could observe an empty
	// authority with zero in-flight and close the buffer before the seeds land.
	// With nothing admitted at all (every seed deduped/over-limit) its first
	// claim comes back empty and it closes the buffer, letting workers exit.
	go pool.feed(ctx)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			for {
				item, ok := pool.pull()
				if !ok {
					return
				}
				// A cancelled crawl drains the buffer without fetching: each
				// remaining item is left pending (not FrontierDone) so a resume
				// re-fetches it rather than recording a stale error.
				if ctx.Err() == nil {
					disc, done := c.crawlOne(ctx, item)
					for _, d := range disc {
						enqueue(d) // publish admitted children durably FIRST
					}
					if done {
						c.sinkFrontierDone(item.URL)
					}
				}
				pool.done() // ...then decrement this item; the feeder closes at 0+drained
			}
		}()
	}
	wg.Wait()

	// Page records were streamed to the store and dropped; every per-page aggregate
	// — shortest-path depth, full-graph inlinks, first-wins (seed-locked)
	// discovered_from — is derived by finalize over the stored gated `edges`/`links`
	// tables (the same store-backed path resume uses), so Run returns only the
	// process-wide counters. (MEMORY-SCALING.md §5.4/§5.5.)
	res := &Result{
		Crawled:     int(c.crawledCount.Load()),
		Total:       int(c.totalCount.Load()),
		Interrupted: ctx.Err() != nil,
		Duration:    time.Since(start),
	}
	return res, c.sinkErr
}

// NoDepth marks pages with no followed-link path from a seed (discovered
// via sitemaps only, or linked solely from such pages).
const NoDepth = -1

// NormalizeSeeds normalizes raw seed URLs to the form used as page-map / edges
// keys, so finalize can seed-lock their discovered_from. Unparseable seeds drop.
func (c *Crawler) NormalizeSeeds(raw ...string) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, err := urlutil.Normalize(r, c.opts); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// LinkRow is a stored raw link (links table) fed to the depth CSR. Type round-
// trips through parse.LinkType so the follow gate can be re-applied.
type LinkRow struct {
	Src, Dst string
	Type     string
	Nofollow bool
}

// RecomputeDepthsFromLinks computes shortest-followed-path depth purely from the
// stored link rows + redirect edges — the SQL/CSR equivalent of RecomputeDepths
// over the in-RAM Facts.Links (MEMORY-SCALING.md §5.5, FIN-DEPTH). It re-applies
// the exact followsForDepth gate over the raw `links` superset (which has no
// follow-gate at write), so it reproduces the in-RAM BFS byte-for-byte. Returns
// url -> depth (NoDepth for pages with no followed path from a seed).
func (c *Crawler) RecomputeDepthsFromLinks(links []LinkRow, redirects map[string]string, pageURLs, seedsRaw []string) map[string]int {
	pageSet := make(map[string]bool, len(pageURLs))
	for _, u := range pageURLs {
		pageSet[u] = true
	}
	adj := make(map[string][]string, len(pageSet))
	for src, dst := range redirects { // redirect counts as a hop (like a followed link)
		if dst != "" && dst != src {
			adj[src] = append(adj[src], dst)
		}
	}
	for _, l := range links {
		if l.Dst == "" || l.Dst == l.Src {
			continue
		}
		if !c.followsForDepthRow(l.Type, l.Dst, l.Nofollow) {
			continue
		}
		adj[l.Src] = append(adj[l.Src], l.Dst)
	}
	depth := make(map[string]int, len(pageSet))
	for u := range pageSet {
		depth[u] = NoDepth
	}
	var queue []string
	for _, s := range c.NormalizeSeeds(seedsRaw...) {
		if pageSet[s] && depth[s] == NoDepth {
			depth[s] = 0
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		d := depth[u]
		for _, v := range adj[u] {
			if pageSet[v] && depth[v] == NoDepth {
				depth[v] = d + 1
				queue = append(queue, v)
			}
		}
	}
	return depth
}

// followsForDepthRow is followsForDepth over a stored link row (type as a string,
// raw dst, nofollow flag) — kept in lockstep with followsForDepth.
func (c *Crawler) followsForDepthRow(typeStr, dst string, nofollow bool) bool {
	targetScope := c.classify(dst)
	if _, crawl := c.typeFlags(parse.LinkType(typeStr), targetScope); !crawl {
		return false
	}
	if nofollow {
		follow := c.cfg.Scope.FollowInternalNofollow
		if targetScope == urlutil.External {
			follow = c.cfg.Scope.FollowExternalNofollow
		}
		if !follow {
			return false
		}
	}
	return true
}

func (c *Crawler) noteSinkErr(err error) {
	if err != nil {
		c.sinkErrOnce.Do(func() { c.sinkErr = err })
	}
}

func (c *Crawler) sinkFrontierDone(url string) {
	if c.sink != nil {
		c.noteSinkErr(c.sink.FrontierDone(url))
	}
}

// classify wraps scope classification; in list mode every uploaded host is
// internal (Screaming Frog semantics for list audits).
func (c *Crawler) classify(url string) urlutil.ScopeClass {
	if c.seedAuth != nil && c.seedAuth[urlutil.Authority(url)] {
		return urlutil.Internal
	}
	return c.scope.Classify(url)
}

// crawlOne fetches and processes one URL, returning the URLs it discovered and
// whether the item is "done" (fully processed). done is false only when a
// pause/stop cancelled the crawl mid-fetch: the caller then leaves the frontier
// item pending so a resume re-fetches it rather than recording a stale error.
func (c *Crawler) crawlOne(ctx context.Context, it frontier.Item) ([]frontier.Item, bool) {
	scopeClass := c.classify(it.URL)
	rec := &PageRecord{URL: it.URL, Depth: it.Depth, Scope: scopeClass.String()}

	// robots.txt gate (never fetch a blocked URL)
	verdict := c.robots.check(ctx, it.URL)
	if !verdict.Allowed {
		show := c.cfg.Robots.ShowBlockedInternal
		if scopeClass == urlutil.External {
			show = c.cfg.Robots.ShowBlockedExternal
		}
		if show {
			rec.State = StateBlockedRobots
			rec.IndexabilityStatus = indexability.BlockedByRobots
			if verdict.Rule != nil {
				rec.MatchedRobotsLine = verdict.Rule.Line
			}
			c.record(rec)
		}
		return nil, true
	}

	// crawl-total limit: reserve a fetch slot
	if c.fetched.Add(1) > int64(c.cfg.Limits.MaxURLs) {
		return nil, true
	}
	c.rateWait(ctx)

	// Take a global fetch slot AFTER the per-crawl rate wait (so a rate-blocked
	// worker never sits on a shared slot, WP-11) and only around the network call
	// itself. A cancel while waiting for a slot leaves the item pending, like a
	// pause mid-fetch. The slot is released the moment Fetch returns — even on a
	// panic — so the rest of the pipeline (parse/evaluate) never holds it.
	if !c.limiter.AcquireFetch(ctx) {
		return nil, false
	}
	res := func() *fetch.Result {
		defer c.limiter.ReleaseFetch()
		return c.client.Fetch(ctx, it.URL)
	}()
	// A fetch that failed because the crawl was paused/stopped (parent context
	// cancelled) is not a real error: abandon the URL without recording it and
	// leave it pending for resume. A genuine per-request timeout leaves the
	// parent context live (ctx.Err() == nil), so it still records as an error.
	if res.FetchError != "" && ctx.Err() != nil {
		return nil, false
	}
	rec.StatusCode = res.StatusCode
	rec.Status = res.Status
	rec.ContentType = res.ContentType
	rec.HTTPVersion = res.HTTPVersion
	rec.ResponseTimeMs = res.ResponseTimeMs
	rec.Size = len(res.Body)
	rec.FetchError = res.FetchError
	rec.RedirectURL = res.RedirectURL
	rec.RedirectType = res.RedirectType
	if len(res.Headers) > 0 {
		rec.Headers = make(map[string]string, len(res.Headers))
		for name := range res.Headers {
			// Set-Cookie is the one header that legitimately repeats (one per
			// cookie); keep every value, newline-joined, so the cookie checks
			// see them all (a header value can never contain a newline).
			if http.CanonicalHeaderKey(name) == "Set-Cookie" {
				rec.Headers[name] = strings.Join(res.Headers.Values(name), "\n")
				continue
			}
			rec.Headers[name] = res.Headers.Get(name)
		}
	}

	if c.cfg.Extraction.StoreWARC && res.FetchError == "" {
		if as, ok := c.sink.(ArchiveSink); ok {
			c.noteSinkErr(as.Archive(it.URL, res))
		}
	}

	var discoveries []frontier.Item
	switch {
	case res.FetchError != "":
		rec.State = StateError
	case res.Truncated:
		rec.State = StateSkippedTooLarge
	default:
		rec.State = StateCrawled
		var ok bool
		if discoveries, ok = c.handleContent(ctx, it, scopeClass, res, rec); !ok {
			// pause/stop interrupted the page's render: abandon without
			// recording (a raw-only record would be permanent — resume never
			// re-renders a processed page) and leave the item pending
			return nil, false
		}
	}

	// The first time the crawl reaches an in-scope host, discover that host's
	// own sitemaps (R17). Sitemap auto-discovery is otherwise seed-host-only, so
	// sitemap-only pages on other in-scope hosts (additional subdomains under
	// crawl_all_subdomains) would be missed. Gated to a real HTTP response and
	// run once per host; the seed host was already claimed at startup.
	if scopeClass == urlutil.Internal && res.FetchError == "" {
		discoveries = append(discoveries, c.discoverHostSitemaps(ctx, it.URL)...)
	}

	c.record(rec)
	return discoveries, true
}

// handleContent parses HTML, evaluates indexability, and produces discoveries.
// ctx is the crawl context — the render path fetches under it (#74 N2a). ok is
// false only when a pause/stop cancelled the crawl mid-render: the caller then
// abandons the item (nothing recorded) so a resume re-fetches and re-renders it.
func (c *Crawler) handleContent(ctx context.Context, it frontier.Item, scopeClass urlutil.ScopeClass, res *fetch.Result, rec *PageRecord) ([]frontier.Item, bool) {
	var discoveries []frontier.Item

	// redirect target re-enters discovery, bounded by the chain limit; with
	// always_follow_redirects (list-mode migration audits) the target keeps
	// the source depth so a depth-0 list still follows whole chains
	if res.RedirectURL != "" {
		if c.cfg.Limits.MaxRedirects < 0 || it.RedirectHops+1 <= c.cfg.Limits.MaxRedirects {
			// external redirect targets obey the external-links gate, like
			// hyperlinks: with externals off the redirect is recorded but
			// its target is never fetched
			if c.classify(res.RedirectURL) != urlutil.External || c.cfg.Links.External.Crawl {
				if d, ok := c.admitTarget(res.RedirectURL, it, true); ok {
					if c.cfg.Advanced.AlwaysFollowRedirects {
						d.Depth = it.Depth
					}
					rec.GatedEdges = append(rec.GatedEdges, GatedEdge{Dst: d.URL, Hyperlink: false, Seq: c.edgeSeq.Add(1)})
					discoveries = append(discoveries, d)
				}
			}
		}
	}

	idxInput := indexability.Input{
		PageURL:                   it.URL,
		StatusCode:                res.StatusCode,
		RobotsUserAgent:           c.cfg.HTTP.RobotsUserAgent,
		RespectSelfRefMetaRefresh: c.cfg.Advanced.RespectSelfReferencingMetaRefresh,
		Opts:                      c.opts,
	}

	parseable := scopeClass == urlutil.Internal && res.StatusCode >= 200 && res.StatusCode < 300 &&
		(isHTML(res.ContentType) || (res.ContentType == "" && c.cfg.Advanced.AssumePagesAreHTML))

	if parseable {
		facts := parse.Parse(it.URL, res.Body, res.Headers, c.cfg)
		rec.Facts = facts

		// A page expands (discovers and contributes its outlinks) only when it
		// is inside the start folder or outside-folder crawling is enabled.
		// Compute it up front: it gates both the content-hash claim below and
		// link discovery further down.
		outside := c.outsideStartFolder(it.URL)
		rec.OutsideStartFolder = outside
		willExpand := !outside || c.cfg.Scope.CrawlOutsideStartFolder

		// Identical-content short-circuit (Screaming Frog parity + frontier-RAM
		// guard). A client-routed SPA serves the SAME raw shell at thousands of
		// URLs; only after rendering does each shell mint a different per-URL
		// link set (sweetgreen order.* = 3,424 byte-identical shells, R8). Hash
		// the RAW body — the shells are byte-identical there and diverge only
		// once rendered, so a rendered-DOM hash would never match. When a
		// parseable page's body is byte-identical to one already crawled, record
		// it but neither render nor expand it: re-rendering only balloons the
		// frontier (and RAM) with more identical shells. Only FULL byte identity
		// triggers this — never a near-duplicate.
		//
		// A page may claim a content hash as its canonical only when it will
		// actually expand. Otherwise an undiscovered outside-start-folder page
		// could become canonical for a hash without contributing its links,
		// then shadow a later in-folder byte-identical twin — suppressing that
		// twin's in-scope outlinks and breaking the "no reachable URL is lost"
		// invariant. A non-expanding page may still be marked a duplicate of an
		// existing canonical, which only skips its (useless) re-render.
		dup := false
		if c.cfg.Advanced.SkipIdenticalContentLinks {
			if canonical, first := c.firstWithContent(facts.Hash, it.URL, willExpand); !first {
				dup = true
				rec.DuplicateOf = canonical
			}
		}

		if c.cfg.Extraction.StoreHTML {
			if bs, ok := c.sink.(BlobSink); ok && c.sink != nil {
				c.noteSinkErr(bs.Blob(it.URL, "html", res.Body))
			}
		}
		renderedOK := false
		if c.renderer != nil && !dup {
			var interrupted bool
			if renderedOK, interrupted = c.renderAndDiff(ctx, it.URL, rec, facts, res); interrupted {
				return nil, false
			}
		}
		if c.extractEngine != nil {
			// append, not assign: renderAndDiff may already have stored
			// custom JS (kind=js) results on this record
			rec.CustomResults = append(rec.CustomResults, c.extractEngine.Run(res.Body, facts.ContentText)...)
		}
		// Structured data reflects what Google (and Screaming Frog) see — the
		// rendered DOM when rendering is on. renderAndDiff already extracted it
		// from the rendered HTML, where JS-injected JSON-LD (e.g. FAQ blocks) is
		// visible but the raw body is not (R16). Fall back to the raw body only
		// when rendering didn't run: rendering off, a duplicate shell, or a
		// render failure.
		if !renderedOK {
			rec.StructuredData = structured.Extract(res.Body, c.cfg)
		}
		idxInput.MetaRobots = facts.MetaRobots
		idxInput.XRobotsTag = facts.XRobotsTag
		idxInput.Canonicals = append(append([]string{}, facts.CanonicalHTML...), facts.CanonicalHTTP...)
		idxInput.MetaRefreshURL = facts.MetaRefreshURL

		if facts.MetaRefreshURL != "" && facts.MetaRefreshURL != it.URL {
			rec.RedirectURL = facts.MetaRefreshURL
			rec.RedirectType = "meta_refresh"
		}

		if willExpand && !dup {
			discoveries = append(discoveries, c.discoverLinks(it, facts, &rec.GatedEdges)...)
		}
	}

	idx := indexability.Evaluate(idxInput)
	rec.Indexable = idx.Indexable
	rec.IndexabilityStatus = idx.Status
	return discoveries, true
}

// Close releases the renderer (JS rendering mode).
func (c *Crawler) Close() {
	if c.renderer != nil {
		c.renderer.Close()
	}
}

// renderAndDiff renders the page in Chrome, parses the rendered DOM, extracts
// structured data from it, merges rendered-only links into the link set
// (origin=rendered) and records the raw-vs-rendered element differences. It
// returns renderedOK=true when rendering succeeded — the caller then trusts the
// rendered-DOM structured data instead of re-extracting from the raw body.
// interrupted=true means a pause/stop cancelled the crawl while waiting for a
// render slot or mid-render: the caller must abandon the item (leave it
// pending, record nothing) so a resume re-fetches and re-renders it — a
// degraded raw-only record would be permanent. A render failure with the crawl
// still live (interrupted=false, renderedOK=false) degrades to raw-HTML
// behaviour as before.
//
// The render runs under the CRAWL context and holds a global RENDER slot
// (REN-01/#76): it re-fetches the page plus its subresources in a Chrome tab —
// a different weight on a different resource axis (~100-300MB RAM per tab) than
// an HTTP fetch, so it has its own pool in the process-wide limiter. It must
// NOT hold a fetch slot at the same time (nested acquires across M crawls
// starve the fetch pool and can deadlock it). The slot is released the moment
// Render returns — even on a panic — so parsing/diffing never holds it.
func (c *Crawler) renderAndDiff(ctx context.Context, url string, rec *PageRecord, facts *parse.Facts, res *fetch.Result) (renderedOK, interrupted bool) {
	if !c.limiter.AcquireRender(ctx) {
		return false, true // cancelled while waiting for a render slot
	}
	rendered, err := func() (*render.Result, error) {
		defer c.limiter.ReleaseRender()
		return c.renderer.Render(ctx, url)
	}()
	if err != nil {
		if ctx.Err() != nil {
			return false, true // pause/stop tore the render down, not the page
		}
		return false, false // rendering failure degrades to raw-HTML behaviour
	}

	// persist the rendered DOM / screenshot to the assets dir when asked
	// (the blobs table records the file refs); same BlobSink path as raw HTML
	if bs, ok := c.sink.(BlobSink); ok && c.sink != nil {
		if c.cfg.Extraction.StoreRenderedHTML {
			c.noteSinkErr(bs.Blob(url, "rendered_html", []byte(rendered.HTML)))
		}
		if c.cfg.Rendering.Screenshots && len(rendered.Screenshot) > 0 {
			c.noteSinkErr(bs.Blob(url, "screenshot", rendered.Screenshot))
		}
	}

	rFacts := parse.Parse(url, []byte(rendered.HTML), res.Headers, c.cfg)

	// Structured data is extracted from the rendered DOM (R16): JSON-LD,
	// microdata and RDFa injected by JavaScript are absent from the raw body
	// but are what Google and Screaming Frog read. The raw-body extraction in
	// crawlOne runs only when this render path doesn't.
	rec.StructuredData = structured.Extract([]byte(rendered.HTML), c.cfg)

	first := func(v []string) string {
		if len(v) > 0 {
			return v[0]
		}
		return ""
	}
	diff := &JSDiff{
		RenderedWordCount: rFacts.WordCount,
		WordCountChange:   rFacts.WordCount - facts.WordCount,
		ConsoleErrors:     rendered.ConsoleErrors,
	}
	if rt, t := first(rFacts.Titles), first(facts.Titles); rt != t {
		diff.TitleChanged = true
		diff.RenderedTitle = rt
	}
	diff.DescriptionChanged = first(rFacts.Descriptions) != first(facts.Descriptions)
	diff.H1Changed = first(rFacts.H1s) != first(facts.H1s)
	if rc, cn := first(rFacts.CanonicalHTML), first(facts.CanonicalHTML); rc != cn {
		diff.CanonicalChanged = true
		diff.RenderedCanonical = rc
	}
	rawNoindex := hasNoindexValue(facts.MetaRobots)
	diff.NoindexOnlyRaw = rawNoindex && !hasNoindexValue(rFacts.MetaRobots)

	// custom JS extraction values join the custom results (kind=js); a
	// snippet with a content_types list only applies to matching pages
	for _, jr := range rendered.JSResults {
		if !c.customJSApplies(jr.Name, res.ContentType) {
			continue
		}
		rec.CustomResults = append(rec.CustomResults, extract.Result{Kind: "js", Name: jr.Name, Value: jr.Value})
	}

	// merge rendered-only links (and use them for discovery)
	seen := map[string]bool{}
	for i := range facts.Links {
		facts.Links[i].Origin = "html"
		seen[string(facts.Links[i].Type)+"|"+facts.Links[i].URL] = true
	}
	for _, l := range rFacts.Links {
		if seen[string(l.Type)+"|"+l.URL] {
			continue
		}
		l.Origin = "rendered"
		facts.Links = append(facts.Links, l)
		// "Contains JavaScript Links" counts rendered-only HYPERLINKS, like
		// Screaming Frog — scripts/images injected by analytics don't count
		if l.Type == parse.Hyperlink {
			diff.JSLinks++
		}
	}
	// XHR/fetch requests observed during rendering join the link set too
	// (Screaming Frog parity: an SPA's data endpoints are discovered URLs)
	for _, u := range rendered.XHRURLs {
		if seen["xhr|"+u] {
			continue
		}
		seen["xhr|"+u] = true
		facts.Links = append(facts.Links, parse.Link{Type: parse.XHR, URL: u, Origin: "xhr"})
	}

	// Flag schema.org types that exist only after rendering — structured data
	// injected by JavaScript, invisible to non-rendering crawlers and LLMs even
	// though bluesnake now recovers it (R18). Compare the rendered types against
	// the raw body's types; anything rendered-only is render-dependent.
	if rec.StructuredData != nil && len(rec.StructuredData.Types) > 0 {
		rawTypes := map[string]bool{}
		if rawSD := structured.Extract(res.Body, c.cfg); rawSD != nil {
			for _, t := range rawSD.Types {
				rawTypes[t] = true
			}
		}
		seenType := map[string]bool{}
		for _, t := range rec.StructuredData.Types {
			if !rawTypes[t] && !seenType[t] {
				seenType[t] = true
				diff.StructuredJSOnly = append(diff.StructuredJSOnly, t)
			}
		}
	}

	rec.JSDiff = diff
	return true, false
}

// customJSApplies checks a snippet's content_types filter against the page.
func (c *Crawler) customJSApplies(name, contentType string) bool {
	for _, cj := range c.cfg.CustomJS {
		if cj.Name != name {
			continue
		}
		if len(cj.ContentTypes) == 0 {
			return true
		}
		for _, ct := range cj.ContentTypes {
			if strings.Contains(contentType, ct) {
				return true
			}
		}
		return false
	}
	return false
}

func hasNoindexValue(values []string) bool {
	for _, v := range values {
		if strings.Contains(strings.ToLower(v), "noindex") {
			return true
		}
	}
	return false
}

// discoverLinks runs every parsed link through the discovery filter chain. When
// edges is non-nil (crawl time, not the finalize replay) it also records each
// admitted edge — rewritten target + hyperlink flag + a monotonic seq — for the
// edges table.
func (c *Crawler) discoverLinks(it frontier.Item, facts *parse.Facts, edges *[]GatedEdge) []frontier.Item {
	var out []frontier.Item
	links := facts.Links
	if max := c.cfg.Limits.MaxLinksPerPage; max >= 0 && len(links) > max {
		links = links[:max]
	}
	for _, l := range links {
		if l.URL == "" {
			continue
		}
		targetScope := c.classify(l.URL)
		_, crawl := c.typeFlags(l.Type, targetScope)
		if !crawl {
			continue
		}
		if l.Nofollow {
			follow := c.cfg.Scope.FollowInternalNofollow
			if targetScope == urlutil.External {
				follow = c.cfg.Scope.FollowExternalNofollow
			}
			if !follow {
				continue
			}
		}
		isRedirect := l.Type == parse.MetaRefreshLink
		if d, ok := c.admitTarget(l.URL, it, isRedirect); ok {
			if l.Type == parse.Canonical && c.cfg.Advanced.AlwaysFollowCanonicals {
				d.Depth = it.Depth
			}
			hyperlink := l.Type == parse.Hyperlink
			if edges != nil {
				*edges = append(*edges, GatedEdge{Dst: d.URL, Hyperlink: hyperlink, Seq: c.edgeSeq.Add(1)})
			}
			out = append(out, d)
		}
	}
	return out
}

// admitTarget applies the per-URL parts of the discovery filter chain:
// rewriting, validity, include/exclude, start-folder scoping. Frontier
// admission (dedup + limits) happens at spawn time.
func (c *Crawler) admitTarget(rawURL string, src frontier.Item, viaRedirect bool) (frontier.Item, bool) {
	target := c.rewriter.Rewrite(rawURL)
	if !urlutil.IsValid(target) && !c.cfg.Scope.CrawlInvalidLinks {
		return frontier.Item{}, false
	}
	if !c.filter.Allowed(target) {
		return frontier.Item{}, false
	}
	if c.outsideStartFolder(target) &&
		!c.cfg.Scope.CrawlOutsideStartFolder && !c.cfg.Scope.CheckLinksOutsideStartFolder {
		return frontier.Item{}, false
	}
	hops := 0
	if viaRedirect {
		hops = src.RedirectHops + 1
	}
	return frontier.Item{URL: target, Depth: src.Depth + 1, RedirectHops: hops, Source: src.URL}, true
}

// typeFlags maps a link type to its store/crawl config. External targets
// additionally require external crawling to be enabled.
func (c *Crawler) typeFlags(lt parse.LinkType, scopeClass urlutil.ScopeClass) (store, crawl bool) {
	L, R := &c.cfg.Links, &c.cfg.Resources
	var sc config.StoreCrawl
	switch lt {
	case parse.Hyperlink:
		if scopeClass == urlutil.External {
			return L.External.Store, L.External.Crawl
		}
		return L.Internal.Store, L.Internal.Crawl
	case parse.XHR:
		// XHR/fetch requests observed during rendering are JS data
		// endpoints, not page links: Screaming Frog buckets them under
		// JavaScript resources and never enqueues them as pages (measured
		// 2026-06-12 — SF rendered crawls skip Next.js ?_rsc prefetches,
		// which mint a fresh token per render and explode the frontier).
		sc = R.JavaScript
	case parse.Image:
		sc = R.Images
	case parse.CSS:
		sc = R.CSS
	case parse.JS:
		sc = R.JavaScript
	case parse.Media:
		sc = R.Media
	case parse.SWF:
		sc = R.SWF
	case parse.IFrame:
		sc = L.IFrames
	case parse.Canonical:
		sc = L.Canonicals
	case parse.HreflangLink:
		sc = L.Hreflang
	case parse.Next, parse.Prev:
		sc = L.Pagination
	case parse.AMP:
		sc = L.AMP
	case parse.MetaRefreshLink:
		sc = L.MetaRefresh
	case parse.MobileAlternate:
		sc = L.MobileAlternate
	default: // form_action, uncrawlable: never fetched
		return false, false
	}
	if scopeClass == urlutil.External {
		sc.Crawl = sc.Crawl && L.External.Crawl
		sc.Store = sc.Store && L.External.Store
	}
	return sc.Store, sc.Crawl
}

func (c *Crawler) outsideStartFolder(url string) bool {
	if c.startFolder == "" {
		return false
	}
	if c.classify(url) == urlutil.External {
		return false
	}
	rest, ok := strings.CutPrefix(url, "http://")
	if !ok {
		rest, _ = strings.CutPrefix(url, "https://")
	}
	_, path, _ := strings.Cut(rest, "/")
	return !strings.HasPrefix("/"+path, c.startFolder)
}

// startFolderOf extracts the subfolder scope of a seed URL ("" when the seed
// is the root or a file at the root).
func startFolderOf(seed string) string {
	rest, ok := strings.CutPrefix(seed, "http://")
	if !ok {
		rest, _ = strings.CutPrefix(seed, "https://")
	}
	_, path, found := strings.Cut(rest, "/")
	if !found {
		return ""
	}
	path = "/" + path
	if q := strings.IndexAny(path, "?#"); q >= 0 {
		path = path[:q]
	}
	dir := path[:strings.LastIndex(path, "/")+1]
	if dir == "/" {
		return ""
	}
	return dir
}

func (c *Crawler) rateWait(ctx context.Context) {
	if c.tokens == nil {
		return
	}
	select {
	case <-c.tokens:
	case <-ctx.Done():
	}
}

// firstWithContent claims a raw-body content hash for url and reports whether
// url is the first page seen with that exact hash. Byte-identical pages racing
// through the worker pool resolve deterministically: whichever takes the lock
// first owns the hash as the canonical page; the rest are duplicates of it.
// firstWithContent reports whether url is the first page crawled with this raw
// body hash. claim records url as the canonical for the hash; callers that will
// not expand the page (outside-folder, not crawled) pass claim=false so they
// never become canonical for content whose links they did not contribute.
func (c *Crawler) firstWithContent(hash, url string, claim bool) (canonical string, first bool) {
	// With a store-backed sink the content_hash table is the authority, so the
	// in-RAM seenContent map is never grown (it stays O(0) in production) — the
	// #70 M4 bound. A non-store sink (library/tests) keeps the in-memory set.
	if fc, ok := c.sink.(ContentSink); ok {
		canonical, first, err := fc.FirstWithContent(hash, url, claim)
		if err != nil {
			// On a store error, conservatively treat the page as first/novel (never
			// silently mark a page a duplicate) and surface the error via the sink.
			c.noteSinkErr(err)
			return url, true
		}
		return canonical, first
	}
	c.hashMu.Lock()
	defer c.hashMu.Unlock()
	if existing, ok := c.seenContent[hash]; ok {
		return existing, false
	}
	if claim {
		c.seenContent[hash] = url
	}
	return url, true
}

func (c *Crawler) record(rec *PageRecord) {
	c.totalCount.Add(1)
	if rec.State == StateCrawled {
		c.crawledCount.Add(1)
	}
	if c.sink != nil {
		// Precompute the near-dup signature from the live body (before it is freed
		// below) so finalize never reloads ContentText for near-dup. Gated on the
		// feature being enabled — a default crawl pays nothing (MEMORY-SCALING.md
		// §5.5). The analyze-time gate (indexable/paginated/wordcount) is applied
		// later over this stored signature, so toggling those flags at analyze
		// time still changes the result set identically.
		if c.cfg.Content.NearDuplicates.Enabled && rec.Facts != nil && rec.Facts.ContentText != "" {
			rec.Minhash = minhash.Of(rec.Facts.ContentText).Encode()
		}
		c.noteSinkErr(c.sink.Page(rec))
		// The full page text is now durably in the store (serialized into the
		// facts JSON by Page() above). Free the in-RAM copy: ContentText is the
		// bulk of a PageRecord (~one page-body each), and at thousands of pages
		// its retention is the dominant per-page RAM sink (MEMORY-SCALING.md §4
		// regime 2 / Phase 0). Every consumer — near-duplicates, lorem/soft-404
		// issue checks, compare's content-change detection — reads it back via
		// LoadPages, never off the live result; the sole live reader
		// (extractEngine in handleContent) already ran before record().
		if rec.Facts != nil {
			rec.Facts.ContentText = ""
		}
		rec.GatedEdges = nil // persisted to the edges table; free the RAM
	}
}

func isHTML(contentType string) bool {
	return strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "application/xhtml")
}
