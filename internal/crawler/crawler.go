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

	// analysis outputs (populated when loaded from a store after analyze)
	LinkScore         float64
	UniqueInlinks     int
	UniqueOutlinks    int
	ClosestSimilarity float64
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

// WithResume preseeds the crawler from a stored crawl: processed URLs are
// never re-fetched; pending items re-enter the frontier.
func WithResume(processed []string, pending []frontier.Item) Option {
	return func(c *Crawler) {
		c.resumeProcessed = processed
		c.resumePending = pending
	}
}

// Result is the outcome of a crawl.
type Result struct {
	Pages       map[string]*PageRecord
	Crawled     int
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

	sem     chan struct{}
	tokens  chan struct{}
	fetched atomic.Int64

	sink            Sink
	renderer        *render.Renderer
	extractEngine   *extract.Engine
	fetchOpts       []fetch.Option
	resumeProcessed []string
	resumePending   []frontier.Item
	sinkErrOnce     sync.Once
	sinkErr         error

	mu      sync.Mutex
	pages   map[string]*PageRecord
	inlinks map[string]inlinkInfo
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
	c.frontier = frontier.New(cfg)
	c.sem = make(chan struct{}, cfg.Speed.MaxThreads)
	c.pages = make(map[string]*PageRecord)
	c.inlinks = make(map[string]inlinkInfo)
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

	var wg sync.WaitGroup
	spawn := func(item frontier.Item) {
		if c.frontier.Admit(item) {
			c.sinkFrontierAdd(item)
			wg.Add(1)
			go c.process(ctx, &wg, item)
		}
	}
	for _, seed := range seeds {
		spawn(frontier.Item{URL: seed, Depth: 0})
	}
	// list mode audits exactly the supplied URLs — sitemaps are an input
	// source there (--sitemap), never an extra discovery channel
	if c.cfg.Sitemaps.CrawlLinked && c.cfg.Mode != "list" {
		for _, item := range c.crawlSitemaps(ctx, seeds[0]) {
			spawn(item)
		}
	}
	for _, item := range c.resumePending {
		spawn(item)
	}
	wg.Wait()

	// resumed runs see only this session's pages — the BFS would have no
	// crawled seed to start from, so keep admit-time depths there
	if len(c.resumeProcessed) == 0 {
		c.recomputeDepths(seeds)
	}

	res := &Result{
		Pages:       c.pages,
		Interrupted: ctx.Err() != nil,
		Duration:    time.Since(start),
	}
	for url, info := range c.inlinks {
		if rec, ok := c.pages[url]; ok {
			rec.Inlinks = info.count
			rec.DiscoveredFrom = info.first
		}
	}
	for _, rec := range c.pages {
		if rec.State == StateCrawled {
			res.Crawled++
		}
	}
	return res, c.sinkErr
}

// NoDepth marks pages with no followed-link path from a seed (discovered
// via sitemaps only, or linked solely from such pages).
const NoDepth = -1

// recomputeDepths replaces admit-time depths with the shortest followed-link
// path from a seed (Screaming Frog parity). Sitemap discovery contributes no
// depth: URLs reachable only that way keep NoDepth, as do their descendants.
// Redirects (HTTP, meta refresh, JS) count as a hop, like a link.
func (c *Crawler) recomputeDepths(seeds []string) {
	adj := make(map[string][]string, len(c.pages))
	for url, rec := range c.pages {
		var out []string
		if rec.RedirectURL != "" && rec.RedirectURL != url {
			out = append(out, rec.RedirectURL)
		}
		if rec.Facts != nil {
			for _, l := range rec.Facts.Links {
				switch l.Type {
				case parse.Hyperlink:
					if l.Nofollow && !c.cfg.Scope.FollowInternalNofollow {
						continue
					}
				case parse.IFrame:
					if !c.cfg.Links.IFrames.Crawl {
						continue
					}
				default:
					continue
				}
				if l.URL == "" || l.URL == url {
					continue
				}
				out = append(out, l.URL)
			}
		}
		adj[url] = out
	}
	for _, rec := range c.pages {
		rec.Depth = NoDepth
	}
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if rec, ok := c.pages[s]; ok && rec.Depth == NoDepth {
			rec.Depth = 0
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		d := c.pages[u].Depth
		for _, v := range adj[u] {
			if rec, ok := c.pages[v]; ok && rec.Depth == NoDepth {
				rec.Depth = d + 1
				queue = append(queue, v)
			}
		}
	}
}

func (c *Crawler) process(ctx context.Context, wg *sync.WaitGroup, it frontier.Item) {
	defer wg.Done()
	c.sem <- struct{}{}
	defer func() { <-c.sem }()
	if ctx.Err() != nil {
		return
	}
	for _, d := range c.crawlOne(ctx, it) {
		c.noteInlink(d.URL, it.URL)
		if c.frontier.Admit(d) {
			c.sinkFrontierAdd(d)
			wg.Add(1)
			go c.process(ctx, wg, d)
		}
	}
	c.sinkFrontierDone(it.URL)
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

func (c *Crawler) crawlOne(ctx context.Context, it frontier.Item) []frontier.Item {
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
		return nil
	}

	// crawl-total limit: reserve a fetch slot
	if c.fetched.Add(1) > int64(c.cfg.Limits.MaxURLs) {
		return nil
	}
	c.rateWait(ctx)

	res := c.client.Fetch(ctx, it.URL)
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

	c.record(rec)
	return discoveries
}

// handleContent parses HTML, evaluates indexability, and produces discoveries.
func (c *Crawler) handleContent(it frontier.Item, scopeClass urlutil.ScopeClass, res *fetch.Result, rec *PageRecord) []frontier.Item {
	var discoveries []frontier.Item

	// redirect target re-enters discovery, bounded by the chain limit; with
	// always_follow_redirects (list-mode migration audits) the target keeps
	// the source depth so a depth-0 list still follows whole chains
	if res.RedirectURL != "" {
		if c.cfg.Limits.MaxRedirects < 0 || it.RedirectHops+1 <= c.cfg.Limits.MaxRedirects {
			if d, ok := c.admitTarget(res.RedirectURL, it, true); ok {
				if c.cfg.Advanced.AlwaysFollowRedirects {
					d.Depth = it.Depth
				}
				discoveries = append(discoveries, d)
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
		if c.cfg.Extraction.StoreHTML {
			if bs, ok := c.sink.(BlobSink); ok && c.sink != nil {
				c.noteSinkErr(bs.Blob(it.URL, "html", res.Body))
			}
		}
		if c.renderer != nil {
			c.renderAndDiff(it.URL, rec, facts, res)
		}
		if c.extractEngine != nil {
			// append, not assign: renderAndDiff may already have stored
			// custom JS (kind=js) results on this record
			rec.CustomResults = append(rec.CustomResults, c.extractEngine.Run(res.Body, facts.ContentText)...)
		}
		rec.StructuredData = structured.Extract(res.Body, c.cfg)
		idxInput.MetaRobots = facts.MetaRobots
		idxInput.XRobotsTag = facts.XRobotsTag
		idxInput.Canonicals = append(append([]string{}, facts.CanonicalHTML...), facts.CanonicalHTTP...)
		idxInput.MetaRefreshURL = facts.MetaRefreshURL

		if facts.MetaRefreshURL != "" && facts.MetaRefreshURL != it.URL {
			rec.RedirectURL = facts.MetaRefreshURL
			rec.RedirectType = "meta_refresh"
		}

		outside := c.outsideStartFolder(it.URL)
		rec.OutsideStartFolder = outside
		discover := !outside || c.cfg.Scope.CrawlOutsideStartFolder
		if discover {
			discoveries = append(discoveries, c.discoverLinks(it, facts)...)
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

// renderAndDiff renders the page in Chrome, parses the rendered DOM, merges
// rendered-only links into the link set (origin=rendered) and records the
// raw-vs-rendered element differences.
func (c *Crawler) renderAndDiff(url string, rec *PageRecord, facts *parse.Facts, res *fetch.Result) {
	rendered, err := c.renderer.Render(context.Background(), url)
	if err != nil {
		return // rendering failure degrades to raw-HTML behaviour
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
		diff.JSLinks++
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
	rec.JSDiff = diff
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

// discoverLinks runs every parsed link through the discovery filter chain.
func (c *Crawler) discoverLinks(it frontier.Item, facts *parse.Facts) []frontier.Item {
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
	case parse.Hyperlink, parse.XHR:
		// XHR-discovered URLs follow the page-link config: Screaming Frog
		// fetches them even when JS/CSS resource crawling is off
		if scopeClass == urlutil.External {
			return L.External.Store, L.External.Crawl
		}
		return L.Internal.Store, L.Internal.Crawl
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

func (c *Crawler) record(rec *PageRecord) {
	c.mu.Lock()
	c.pages[rec.URL] = rec
	c.mu.Unlock()
	if c.sink != nil {
		c.noteSinkErr(c.sink.Page(rec))
	}
}

func (c *Crawler) noteInlink(target, source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.inlinks[target]
	info.count++
	if info.first == "" {
		info.first = source
	}
	c.inlinks[target] = info
}

func isHTML(contentType string) bool {
	return strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "application/xhtml")
}
