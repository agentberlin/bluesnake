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
// FrontierAdd/FrontierDone mirror the in-memory frontier so an interrupted
// crawl can resume exactly where it stopped.
type Sink interface {
	Page(*PageRecord) error
	FrontierAdd(frontier.Item) error
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

// Option configures a Crawler.
type Option func(*Crawler)

// WithSink streams pages and frontier mutations into a persistent store.
func WithSink(s Sink) Option { return func(c *Crawler) { c.sink = s } }

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

// WithResume preseeds the crawler from a stored crawl: processed URLs are
// never re-fetched; pending items re-enter the frontier.
func WithResume(processed []string, pending []frontier.Item) Option {
	return func(c *Crawler) {
		c.resumeProcessed = processed
		c.resumePending = pending
	}
}

// InlinkAgg is the per-URL discovery aggregate the crawler hands to finalize in
// place of the full page map: the raw hyperlink inlink count and the first
// (seed-locked) discoverer. It is frontier-sized, not page-sized — it carries no
// page content, so retaining it costs orders of magnitude less than the records.
type InlinkAgg struct {
	Count int    // raw hyperlink inlinks (self-excluded, nofollow-gated)
	First string // first discoverer; "" for seeds (seed-lock)
}

// Result is the outcome of a crawl. Page records are streamed to the Sink as the
// crawl runs and are NOT retained here (stream-and-drop, MEMORY-SCALING.md §5.4);
// finalize reads them back from the store. Inlinks carries the slim discovery
// aggregate the store-backed finalize needs that the store cannot yet derive on
// its own (first-wins discovered_from), keyed by URL.
type Result struct {
	Inlinks     map[string]InlinkAgg
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
	startFolder string

	tokens  chan struct{}
	fetched atomic.Int64

	sink            Sink
	renderer        *render.Renderer
	extractEngine   *extract.Engine
	fetchOpts       []fetch.Option
	limiter         *limiter.Limiter // process-wide fetch cap; nil ⇒ unlimited
	resumeProcessed []string
	resumePending   []frontier.Item
	sinkErrOnce     sync.Once
	sinkErr         error

	mu      sync.Mutex
	inlinks map[string]inlinkInfo

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

type inlinkInfo struct {
	count int
	first string
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
	if cfg.Rendering.Mode == "javascript" {
		if c.renderer, err = render.New(cfg); err != nil {
			return nil, err
		}
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
	// When the sink is the SQLite store, it doubles as the frontier dedup
	// authority (frontier ∪ pages on disk), so the in-memory visited set is
	// dropped; otherwise the frontier keeps an exact in-memory set.
	var dedup frontier.Dedup
	if d, ok := c.sink.(frontier.Dedup); ok {
		dedup = d
	}
	c.frontier = frontier.New(cfg, frontier.WithDedup(dedup))
	c.inlinks = make(map[string]inlinkInfo)
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
		go func() {
			for range ticker.C {
				select {
				case c.tokens <- struct{}{}:
				default:
				}
			}
		}()
	}

	c.frontier.MarkSeen(c.resumeProcessed)
	// Resume the crawl-total budget cumulatively: the MaxURLs limit (checked via
	// c.fetched below) is a fetch counter that starts at zero each session, so
	// without this seed every resumed session would grant a fresh MaxURLs budget
	// and a paused-then-resumed crawl could fetch far more than a straight one.
	// Seeding from the already-recorded pages makes the cap span both sessions.
	c.fetched.Store(int64(len(c.resumeProcessed)))
	// Continue the gated-edge seq past the prior session so resume's new edges
	// sort after session-1's: MIN(seq) first-wins discovered_from stays stable.
	if len(c.resumeProcessed) > 0 {
		if m, ok := c.sink.(interface{ MaxEdgeSeq() (int64, error) }); ok {
			if maxSeq, err := m.MaxEdgeSeq(); err == nil {
				c.edgeSeq.Store(maxSeq)
			}
		}
	}

	// Bounded worker pool (MEMORY-SCALING.md §5.2): N persistent workers drain a
	// deadlock-free unbounded in-RAM queue, with an atomic in-flight counter
	// replacing the old goroutine-per-URL model and its wg.Wait(). in-flight =
	// (queued items) + (items being processed). The #1 swap trap (§11): a worker
	// increments its admitted discoveries' count BEFORE decrementing its own item,
	// so the counter never reaches 0 while reachable work remains. A worker never
	// blocks pushing its discoveries (the queue grows), so the sole producer can't
	// deadlock on a full buffer.
	pool := newWorkPool()
	enqueue := func(item frontier.Item) {
		// Admit is the dedup + limit gate. With a store-backed dedup it also
		// writes the durable frontier row (the admission authority), so there is
		// no separate sink FrontierAdd to make.
		if !c.frontier.Admit(item) {
			return
		}
		pool.push(item) // increments in-flight for admitted items only
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
	// On resume, preserve each pending URL's original (session-1) discoverer,
	// captured in the frontier when it was first admitted, so a page first
	// linked before the interrupt keeps its true DiscoveredFrom instead of
	// whichever this-session page happens to re-link it first. Seed before any
	// pending enqueue so the first-wins rule in noteInlink respects it.
	if len(c.resumePending) > 0 {
		c.mu.Lock()
		for _, item := range c.resumePending {
			if item.Source == "" {
				continue
			}
			if info := c.inlinks[item.URL]; info.first == "" {
				info.first = item.Source
				c.inlinks[item.URL] = info
			}
		}
		c.mu.Unlock()
	}
	// Resume's pending rows are ALREADY admitted (they survive in the frontier
	// table / the in-memory set), so Admit would dedup-reject them. Readmit re-
	// records them without consuming limit budget and re-queues them for crawling.
	for _, item := range c.resumePending {
		c.frontier.Readmit(item)
		pool.push(item)
	}
	// Nothing admitted (every seed deduped/over-limit) → no work; let workers exit.
	pool.closeIfDrained()

	n := c.cfg.Speed.MaxThreads
	if n < 1 {
		n = 1
	}
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
				// A cancelled crawl drains the queue without fetching: each
				// remaining item is left pending (not FrontierDone) so a resume
				// re-fetches it rather than recording a stale error.
				if ctx.Err() == nil {
					disc, done := c.crawlOne(ctx, item)
					for _, d := range disc {
						enqueue(d) // in-flight++ for admitted children FIRST
					}
					if done {
						c.sinkFrontierDone(item.URL)
					}
				}
				pool.done() // ...then decrement this item; closes the queue at 0
			}
		}()
	}
	wg.Wait()

	// Page records were streamed to the store and dropped; the per-page
	// aggregates a fresh crawl used to write into the live map — shortest-path
	// depth and full-graph inlinks — are now recomputed by finalize over the
	// stored graph (the same store-backed path resume already uses). Run hands
	// finalize only the slim discovery aggregate the store cannot derive itself:
	// the seed-locked first-wins discovered_from, plus the inlink count as a
	// fallback. (MEMORY-SCALING.md §5.4/§5.5.)
	res := &Result{
		Inlinks:     make(map[string]InlinkAgg, len(c.inlinks)),
		Crawled:     int(c.crawledCount.Load()),
		Total:       int(c.totalCount.Load()),
		Interrupted: ctx.Err() != nil,
		Duration:    time.Since(start),
	}
	seedSet := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		seedSet[s] = true
	}
	c.mu.Lock()
	for url, info := range c.inlinks {
		// A seed is a discovery root: a backlink to it must not become its
		// "discovered from" — that both misrepresents provenance and loops the
		// discovery path. Empty also makes resume match a fresh crawl, where the
		// seed is processed before any page that could link back to it.
		first := info.first
		if seedSet[url] {
			first = ""
		}
		res.Inlinks[url] = InlinkAgg{Count: info.count, First: first}
	}
	c.mu.Unlock()
	return res, c.sinkErr
}

// NoDepth marks pages with no followed-link path from a seed (discovered
// via sitemaps only, or linked solely from such pages).
const NoDepth = -1

// RecomputeDepths reruns the depth BFS over an externally supplied page set —
// used on resume, where the full two-session graph (this session's pages plus
// the previously-processed pages reloaded from the store) is needed for the
// shortest-path search to root from the already-crawled seed. Seeds are raw
// (un-normalised) URLs, matching Run's inputs; it mutates each record's Depth.
// A fresh crawl recomputes internally over c.pages, so a resumed crawl that
// recomputes over the merged graph yields identical depths (SF parity).
func (c *Crawler) RecomputeDepths(pages map[string]*PageRecord, seedsRaw ...string) {
	seeds := make([]string, 0, len(seedsRaw))
	for _, raw := range seedsRaw {
		if s, err := urlutil.Normalize(raw, c.opts); err == nil {
			seeds = append(seeds, s)
		}
	}
	c.recomputeDepthsOver(pages, seeds)
}

// recomputeDepthsOver assigns the shortest-followed-link-path depth from a seed
// to every page in pages. Seeds must already be normalised (page-map keys are).
func (c *Crawler) recomputeDepthsOver(pages map[string]*PageRecord, seeds []string) {
	adj := make(map[string][]string, len(pages))
	for url, rec := range pages {
		var out []string
		if rec.RedirectURL != "" && rec.RedirectURL != url {
			out = append(out, rec.RedirectURL)
		}
		if rec.Facts != nil {
			for _, l := range rec.Facts.Links {
				if l.URL == "" || l.URL == url {
					continue
				}
				// A link contributes to depth iff the crawler would actually
				// follow it — reuse the same crawl gate discoverLinks applied
				// (typeFlags by link type + scope, plus the per-scope nofollow
				// rule) rather than hardcoding a subset. This keeps depth
				// correct for canonical/pagination/hreflang/amp/mobile-alternate
				// edges when their crawling is enabled, instead of leaving those
				// pages blank.
				if !c.followsForDepth(l) {
					continue
				}
				out = append(out, l.URL)
			}
		}
		adj[url] = out
	}
	for _, rec := range pages {
		rec.Depth = NoDepth
	}
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if rec, ok := pages[s]; ok && rec.Depth == NoDepth {
			rec.Depth = 0
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		d := pages[u].Depth
		for _, v := range adj[u] {
			if rec, ok := pages[v]; ok && rec.Depth == NoDepth {
				rec.Depth = d + 1
				queue = append(queue, v)
			}
		}
	}
}

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

// RecomputeInlinks recounts the raw hyperlink inlinks for every page over an
// externally supplied page set, by replaying the exact crawl-time discovery
// gate (discoverLinks → noteInlink) every crawled page's links pass through.
// Resume uses it so a resumed crawl's Inlinks equal a fresh crawl's: the
// per-session count (UpdateInlinks) sees only this session's edges and
// under-counts pages linked across the interrupt boundary. The count is
// order-independent, so a replay over the merged two-session graph reproduces
// the fresh-crawl value. Only the raw inlink count is rewritten on the records;
// discovered_from (the discovery-tree parent) is preserved per-session and
// seed-locked in Run, so it is intentionally left untouched here.
func (c *Crawler) RecomputeInlinks(pages map[string]*PageRecord) {
	c.mu.Lock()
	c.inlinks = make(map[string]inlinkInfo)
	c.mu.Unlock()
	for url, rec := range pages {
		if rec.Facts == nil {
			continue // only crawled pages have parsed outlinks, as during the crawl
		}
		c.discoverLinks(frontier.Item{URL: url, Depth: rec.Depth}, rec.Facts, nil)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for url, rec := range pages {
		rec.Inlinks = c.inlinks[url].count
	}
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

// followsForDepth reports whether a parsed link is a followed edge for the
// depth BFS — the same predicate discoverLinks uses to enqueue it (the link
// type's crawl flag for its scope, gated by the per-scope nofollow rule).
func (c *Crawler) followsForDepth(l parse.Link) bool {
	targetScope := c.classify(l.URL)
	if _, crawl := c.typeFlags(l.Type, targetScope); !crawl {
		return false
	}
	if l.Nofollow {
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

func (c *Crawler) sinkFrontierAdd(it frontier.Item) {
	if c.sink != nil {
		c.noteSinkErr(c.sink.FrontierAdd(it))
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
		discoveries = c.handleContent(it, scopeClass, res, rec)
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
func (c *Crawler) handleContent(it frontier.Item, scopeClass urlutil.ScopeClass, res *fetch.Result, rec *PageRecord) []frontier.Item {
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
					c.noteInlink(d.URL, it.URL, false)
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
			renderedOK = c.renderAndDiff(it.URL, rec, facts, res)
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
	return discoveries
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
// returns true when rendering succeeded — the caller then trusts the
// rendered-DOM structured data instead of re-extracting from the raw body.
func (c *Crawler) renderAndDiff(url string, rec *PageRecord, facts *parse.Facts, res *fetch.Result) bool {
	rendered, err := c.renderer.Render(context.Background(), url)
	if err != nil {
		return false // rendering failure degrades to raw-HTML behaviour
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
	return true
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
			c.noteInlink(d.URL, it.URL, hyperlink)
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

// noteInlink records a discovery edge. countIt distinguishes hyperlink
// edges — the only kind Screaming Frog's "Inlinks" column counts — from
// redirect/iframe/meta-refresh discoveries, which still set the
// discovered-from source but don't inflate the inlink count.
func (c *Crawler) noteInlink(target, source string, countIt bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.inlinks[target]
	if countIt {
		info.count++
	}
	if info.first == "" {
		info.first = source
	}
	c.inlinks[target] = info
}

func isHTML(contentType string) bool {
	return strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "application/xhtml")
}
