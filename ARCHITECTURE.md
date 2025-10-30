# BlueSnake Architecture

## Overview

BlueSnake is a production-grade web crawler with multiple interfaces. It consists of:

1. **BlueSnake Crawler Package** - Go-based crawling library with channel-based architecture (root directory)
2. **Internal Packages** - Shared business logic and data layers (internal/ directory)
3. **Desktop Application** - Wails-based GUI application (cmd/desktop/ directory)
4. **HTTP Server** - REST API server (cmd/server/ directory)

## Project Structure

```
bluesnake/
├── *.go                     # Core crawler package files
│   ├── collector.go          # Low-level HTTP engine
│   ├── crawler.go            # High-level orchestration
│   ├── worker_pool.go        # Fixed-size worker pool
│   ├── request.go            # HTTP request abstraction
│   ├── response.go           # HTTP response abstraction
│   ├── htmlelement.go        # HTML element handling
│   ├── xmlelement.go         # XML element handling
│   ├── sitemap.go            # Sitemap parsing
│   ├── content_hash.go       # Content hashing for duplicates
│   ├── text_extraction.go    # Text extraction from HTML
│   ├── link_context.go       # Link context extraction
│   ├── link_position.go      # Link position detection
│   ├── chromedp_backend.go   # JavaScript rendering
│   └── http_backend.go       # HTTP client
├── storage/                  # Storage abstractions
│   ├── crawler_store.go      # Crawler state (visit tracking, URL actions, metadata)
│   └── collector_storage.go  # HTTP-level storage (cookies, content hashes)
├── internal/                 # Internal packages (shared across transports)
│   ├── version/              # Version constant
│   │   └── version.go
│   ├── types/                # Shared types (ProjectInfo, CrawlInfo, etc.)
│   │   └── types.go
│   ├── store/                # Database layer (repository pattern)
│   │   ├── store.go          # Store initialization
│   │   ├── models.go         # GORM models
│   │   ├── projects.go       # Project CRUD operations
│   │   ├── crawls.go         # Crawl CRUD operations
│   │   ├── config.go         # Config CRUD operations
│   │   └── links.go          # PageLink CRUD operations
│   ├── app/                  # Business logic (transport-agnostic)
│   │   ├── events.go         # EventEmitter interface
│   │   ├── app.go            # Core App struct
│   │   ├── utils.go          # URL normalization helpers
│   │   ├── crawler.go        # Crawl orchestration
│   │   ├── active_crawls.go  # Active crawl tracking
│   │   ├── projects.go       # Project management
│   │   ├── crawls.go         # Crawl management
│   │   ├── config.go         # Config management
│   │   ├── links.go          # Link management
│   │   ├── favicon.go        # Favicon handling
│   │   └── updater.go        # Update checking
│   └── framework/            # Framework detection
│       ├── detector.go       # Framework detection logic
│       └── filters.go        # Framework-specific URL filters
└── cmd/                      # Application executables
    ├── desktop/              # Wails desktop application
    │   ├── main.go           # Application entry point
    │   ├── adapter.go        # Wails adapter (WailsEmitter, DesktopApp)
    │   └── frontend/         # React/TypeScript UI
    │       └── src/
    │           ├── App.tsx       # Main UI component
    │           └── Config.tsx    # Configuration UI
    └── server/               # HTTP REST API server
        ├── main.go           # Server initialization
        └── server.go         # HTTP handlers and routing
```

---

## Part 1: BlueSnake Crawler Package

### Architecture Overview

The crawler package uses a **channel-based architecture with a single-threaded URL processor** to eliminate race conditions in URL visit tracking and guarantee deterministic, complete crawls.

### Design Principles

1. **Single source of truth**: Only ONE goroutine (the processor) decides what gets crawled
2. **Clear separation of concerns**:
   - `Collector` = low-level HTTP engine (fetch, parse, callbacks)
   - `Crawler` = high-level orchestration (discovery, deduplication, queueing)
3. **Guaranteed work assignment**: URLs marked as visited are immediately assigned to workers
4. **Controlled concurrency**: Fixed worker pool, no unbounded goroutine creation

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────┐
│                    URL Discovery                         │
│  (Multiple sources: sitemap, spider, network, resources) │
│              (Many goroutines)                           │
└────────────────────┬────────────────────────────────────┘
                     │ Non-blocking sends
                     ▼
          ┌─────────────────────┐
          │ Discovery Channel   │  (Buffered: 50k URLs)
          │  (Drop if full)     │
          └──────────┬──────────┘
                     │
                     ▼
          ┌─────────────────────┐
          │   URL Processor     │  ★ SINGLE goroutine
          │                     │  - Check if visited
          │  (Serialized)       │  - Mark as visited
          │                     │  - Submit to workers
          └──────────┬──────────┘
                     │ Blocking submits (backpressure)
                     ▼
          ┌─────────────────────┐
          │   Work Queue        │  (Buffered: 1k work items)
          └──────────┬──────────┘
                     │
                     ▼
          ┌─────────────────────┐
          │   Worker Pool       │  (10 workers)
          │                     │  - Fetch HTTP
          │  (N workers)        │  - Parse HTML
          │                     │  - Extract links
          └─────────────────────┘
```

### Separation of Responsibilities

**CRITICAL DESIGN PRINCIPLE:**

| Aspect | Collector (Low-Level) | Crawler (High-Level) |
|--------|----------------------|----------------------|
| **Purpose** | HTTP engine | Crawl orchestration |
| **Scope** | Single HTTP request | Entire crawl session |
| **URL Filtering** | ❌ None | ✅ All filtering logic |
| **Visit Tracking** | ❌ None | ✅ Single-threaded processor |
| **Concurrency** | ❌ None (synchronous) | ✅ Worker pool + processor |
| **Callbacks** | Low-level (OnHTML, OnResponse) | High-level (OnPageCrawled) |
| **Configuration** | HTTP settings only | Discovery + filtering + orchestration |

**Architectural Boundary:**
- Collector knows nothing about: crawl strategy, URL filtering, visit tracking, concurrency
- Crawler knows nothing about: HTTP transport, HTML parsing, response handling
- Communication: Crawler calls `Collector.FetchURL()` to fetch URLs; Collector calls callbacks to report results

**Key Separations:**
- **URL filtering** - Handled in Crawler, not Collector (eliminates duplicate filtering)
- **Redirect validation** - Crawler injects filtering via OnRedirect callback
- **Visit tracking** - ALL visit tracking (including redirect destinations) handled in Crawler

### Core Components

#### 1. Crawler (High-Level Orchestration - `crawler.go`)

**Responsibilities:**
- URL discovery coordination (spider, sitemap, network, resources)
- **URL filtering** (domain allowlists/blocklists, URL pattern matching)
- **Redirect validation** (via OnRedirect callback injection)
- Deduplication (track discovered URLs, call `OnURLDiscovered` callback once)
- Visit tracking (single-threaded processor marks URLs as visited)
- Work distribution (submit to worker pool)
- Link graph building (track relationships between pages)
- Statistics and metrics (crawled pages, discovered URLs, dropped URLs)

**Does NOT do:**
- HTTP requests
- HTML parsing
- Managing HTTP client/transport
- Response handling

**Key Type:**
```go
type Crawler struct {
    Collector       *Collector  // Low-level HTTP engine (exported)
    ctx             context.Context
    onPageCrawled   OnPageCrawledFunc
    onResourceVisit OnResourceVisitFunc
    onCrawlComplete OnCrawlCompleteFunc
    onURLDiscovered OnURLDiscoveredFunc

    // URL filtering configuration (owned by Crawler, NOT Collector)
    allowedDomains       []string          // Domain whitelist
    disallowedDomains    []string          // Domain blacklist
    urlFilters           []*regexp.Regexp  // URL pattern whitelist
    disallowedURLFilters []*regexp.Regexp  // URL pattern blacklist
    maxDepth             int               // Maximum crawl depth

    // Channel-based URL processing
    discoveryChannel chan URLDiscoveryRequest
    processorDone    chan struct{}
    workerPool       *WorkerPool
    droppedURLs      uint64

    // State tracking
    store        *storage.CrawlerStore  // Visit tracking, URL actions, metadata
    crawledPages int
    rootDomain   string

    // Discovery configuration
    discoveryMechanisms []DiscoveryMechanism
    sitemapURLs         []string

    // Work coordination
    wg sync.WaitGroup // Tracks pending work items
}
```

#### 2. Collector (Low-Level HTTP Engine - `collector.go`)

**Responsibilities:**
- HTTP requests (GET, POST, HEAD with customizable headers)
- Response handling (status codes, headers, body)
- HTML/XML parsing (GoQuery, xmlquery)
- Low-level callback execution (OnRequest, OnResponse, OnHTML, OnXML, OnScraped, OnError, **OnRedirect**)
- Caching (optional disk cache)
- Robots.txt parsing (checks robots.txt rules only, no URL filtering)
- Content hashing (duplicate content detection)
- JavaScript rendering (optional chromedp integration)

**Does NOT do:**
- **URL filtering** (no domain checks, no URL pattern matching - handled by Crawler)
- **Visit tracking** (no deduplication - managed by Crawler's single-threaded processor)
- **Redirect validation** (delegates to Crawler via OnRedirect callback)
- URL discovery (doesn't decide what to crawl next)
- Work queueing (doesn't manage goroutines)
- Crawl orchestration (no knowledge of crawl strategy)

**Note**: `Collector` is an internal implementation detail. Users interact with `Crawler`, which provides the high-level crawling API.

**Configuration:**
```go
type CrawlerConfig struct {
    // URL Filtering (applied by Crawler)
    MaxDepth             int
    AllowedDomains       []string
    DisallowedDomains    []string
    URLFilters           []*regexp.Regexp
    DisallowedURLFilters []*regexp.Regexp

    // Discovery
    DiscoveryMechanisms  []DiscoveryMechanism
    SitemapURLs          []string

    // Crawler Directives
    ResourceValidation         *ResourceValidationConfig
    RobotsTxtMode              string  // "respect", "ignore", "ignore-report"
    FollowInternalNofollow     bool
    FollowExternalNofollow     bool
    RespectMetaRobotsNoindex   bool
    RespectNoindex             bool

    // Channel-based architecture
    DiscoveryChannelSize int  // Default: 50000
    WorkQueueSize        int  // Default: 1000
    Parallelism          int  // Default: 10

    // HTTP settings (passed to Collector)
    HTTP *HTTPConfig
}

type HTTPConfig struct {
    UserAgent              string
    Headers                map[string]string
    MaxBodySize            int
    CacheDir               string
    EnableRendering        bool
    RenderingConfig        *RenderingConfig
    EnableContentHash      bool
    ContentHashAlgorithm   string
    ContentHashConfig      *ContentHashConfig
    // ... other HTTP-specific settings
}
```

#### 3. URL Processor (The Heart of the Solution)

**Single goroutine** that serializes all visit decisions:

```go
func (cr *Crawler) runURLProcessor() {
    defer close(cr.processorDone)

    for {
        select {
        case req := <-cr.discoveryChannel:
            // Process URL (SERIALIZED - only one at a time)
            cr.processDiscoveredURL(req)

        case <-cr.ctx.Done():
            // Drain remaining URLs
            return
        }
    }
}

func (cr *Crawler) processDiscoveredURL(req URLDiscoveryRequest) {
    // Ensure wg.Done() is always called
    defer cr.wg.Done()

    // Step 1: Determine action (callback called once, memoized)
    action := cr.getOrDetermineURLAction(req.URL)
    if action == URLActionSkip {
        return
    }

    // Step 2: Check max depth
    if cr.maxDepth > 0 && req.Depth > cr.maxDepth {
        return
    }

    // Step 3: Pre-filtering (domain checks, URL pattern filters)
    if !cr.isURLCrawlable(req.URL) {
        return
    }

    // Step 4: Check and mark as visited (ATOMIC, SINGLE-THREADED)
    uHash := requestHash(req.URL, nil)
    alreadyVisited, err := cr.store.VisitIfNotVisited(uHash)
    if err != nil || alreadyVisited {
        return
    }

    // Step 5: Only crawl if action is "crawl" (not "record")
    if action != URLActionCrawl {
        return
    }

    // Step 6: Submit to worker pool (BLOCKS if queue full)
    err = cr.workerPool.Submit(func() {
        defer cr.wg.Done() // Called after fetch completes
        cr.Collector.FetchURL(req.URL, "GET", req.Depth, nil, req.Context, nil)
    })

    if err != nil {
        // Submit failed - call Done since worker won't run
        return  // wg.Done() already called via defer
    }
}
```

**Why this eliminates race conditions:**
- Only ONE goroutine calls `VisitIfNotVisited()` at a time
- Impossible for two goroutines to race on the same URL
- URL is marked visited → immediately submitted to worker → guaranteed assignment

**Why filtering happens here (in Crawler, not Collector):**
- **Single source of truth** - All URLs filtered in one place
- **No duplicate filtering** - Collector has no filtering logic
- **Architectural separation** - Collector = HTTP engine, Crawler = orchestration + filtering
- **Redirect validation** - Crawler injects filtering via OnRedirect callback

#### 4. Worker Pool (`worker_pool.go`)

**Fixed-size pool** of goroutines that fetch URLs:

```go
type WorkerPool struct {
    maxWorkers int
    workQueue  chan func()      // Buffered channel (1000 items)
    wg         *sync.WaitGroup
    ctx        context.Context
}
```

**Benefits:**
- Controlled concurrency (10 workers = 10 concurrent HTTP requests)
- Bounded memory usage (no unbounded goroutine creation)
- Clean shutdown (close queue → wait for workers to finish)

#### 5. Discovery Channel

**Non-blocking sends** to prevent deadlocks:

```go
func (cr *Crawler) queueURL(req URLDiscoveryRequest) {
    // Add to WaitGroup BEFORE queuing (tracks pending work)
    cr.wg.Add(1)

    select {
    case cr.discoveryChannel <- req:
        // Successfully queued

    case <-cr.ctx.Done():
        // Context cancelled - undo the Add
        cr.wg.Done()

    default:
        // Channel full - drop URL
        cr.wg.Done()
        if req.Source == "initial" {
            log.Printf("ERROR: Dropped initial URL: %s", req.URL)
        }
        atomic.AddUint64(&cr.droppedURLs, 1)
    }
}
```

**Why non-blocking:**
- Discovery goroutines never block (drop URLs if channel is full)
- Processor continues draining at its own pace
- Workers process URLs as capacity allows
- No circular dependency → no deadlock possible

#### 6. Storage Package (`storage/`)

**Two separate storage systems:**

**CrawlerStore** (`storage/crawler_store.go`) - Crawler-specific state:
```go
type CrawlerStore struct {
    visited      map[uint64]bool         // Visit tracking
    queuedURLs   map[string]interface{}  // URL actions (memoization)
    pageMetadata map[string]interface{}  // Crawled page metadata
    mu           sync.RWMutex
}

// CRITICAL: Atomic operation for visit tracking
func (s *CrawlerStore) VisitIfNotVisited(hash uint64) (bool, error)
```

**CollectorStorage** (`storage/collector_storage.go`) - HTTP-level storage:
- Cookies via `http.CookieJar`
- Content hashes for duplicate detection

**Lifecycle:**
- Created when crawler starts
- Destroyed when crawler completes
- **Non-persistent** - no disk storage

### Callback Architecture: Two Levels

The framework has **two distinct levels of callbacks**:

#### Low-Level Callbacks (Collector - Internal Use Only)

Used internally by `Crawler` to build high-level functionality. **Users should not interact with these directly.**

```go
// Internal use by Crawler's setupCallbacks()
collector.OnHTML("html", func(e *HTMLElement) {
    // Extract links, build link graph
})

collector.OnResponse(func(r *Response) {
    // Handle response metadata
})

collector.OnScraped(func(r *Response) {
    // Final processing, build PageResult
})

collector.OnError(func(r *Response, err error) {
    // Error handling
})
```

#### High-Level Callbacks (Crawler - User API)

These provide the **user-facing API** with processed, structured data:

```go
// 1. OnURLDiscovered - Called once per unique URL
crawler.SetOnURLDiscovered(func(url string) URLAction {
    // Decide: crawl, record, or skip this URL
    return URLActionCrawl
})

// 2. OnPageCrawled - Called after each page is crawled
crawler.SetOnPageCrawled(func(result *PageResult) {
    // Process crawled page
    // result contains: URL, title, links, status, etc.
})

// 3. OnResourceVisit - Called after each resource is visited
crawler.SetOnResourceVisit(func(result *ResourceResult) {
    // Process non-HTML resources (images, CSS, JS)
})

// 4. OnCrawlComplete - Called once when crawl finishes
crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
    // Crawl finished
})
```

#### URL Action System

```go
type URLAction string

const (
    // URLActionCrawl - Add to links AND crawl
    URLActionCrawl URLAction = "crawl"

    // URLActionRecordOnly - Add to links but DON'T crawl
    URLActionRecordOnly URLAction = "record"

    // URLActionSkip - Ignore completely
    URLActionSkip URLAction = "skip"
)
```

**Use cases:**
- `URLActionCrawl` for normal URLs
- `URLActionRecordOnly` for framework-specific paths (e.g., Next.js data endpoints)
- `URLActionSkip` for analytics/tracking URLs

### Complete URL Lifecycle

```
1. URL Discovered (spider finds <a href="/page">)
   ↓
2. queueURL() - Non-blocking send to discovery channel
   ↓  (wg.Add(1) called here)
   ↓
3. Processor receives URL from channel (SINGLE-THREADED)
   ↓  (defer wg.Done() in processDiscoveredURL)
   ↓
4. Processor: getOrDetermineURLAction() - Call callback (once per URL)
   ↓
5. Processor: isURLCrawlable() - Check filters, robots.txt
   ↓
6. Processor: VisitIfNotVisited() - Mark as visited (ATOMIC)
   ↓
7. Processor: workerPool.Submit() - Add to work queue (BLOCKS if full)
   ↓
8. Worker: Receives work from queue
   ↓  (wg.Add(1) in Submit, defer wg.Done() in worker function)
   ↓
9. Worker: FetchURL() - Fetch HTTP
   ↓
10. Worker: OnResponse → OnHTML → OnScraped callbacks
   ↓
11. Worker: Links extracted → back to step 1 (discovery)
```

### Discovery Sources

All discovery mechanisms feed into the same channel:

```
┌──────────────┐
│   Initial    │ → queueURL(source="initial")
│     URL      │
└──────────────┘
                    ↘
┌──────────────┐       ┌─────────────────┐
│   Sitemap    │ ───→  │   Discovery     │
│  (XML parse) │       │    Channel      │
└──────────────┘       └─────────────────┘
                    ↗
┌──────────────┐
│   Spider     │ → queueURL(source="spider")
│ (HTML links) │
└──────────────┘
                    ↗
┌──────────────┐
│   Network    │ → queueURL(source="network")
│ (JS, Fetch)  │
└──────────────┘
                    ↗
┌──────────────┐
│  Resources   │ → queueURL(source="resource")
│ (CSS, fonts) │
└──────────────┘
```

### Deadlock Prevention Strategy

**Channel Sizing:**
```go
const (
    DefaultDiscoveryChannelSize = 50000  // 50k discovered URLs
    DefaultWorkQueueSize        = 1000   // 1k pending work items
    DefaultWorkerPoolSize       = 10     // 10 concurrent fetches
)
```

**Why these sizes:**
- **Large discovery channel (50k)**: Buffers bursty URL discovery (sitemap returns 10k URLs at once)
- **Smaller work queue (1k)**: Provides backpressure without excessive memory usage
- **Worker pool (10)**: Matches typical parallelism limits for web crawling

**Trade-offs:**
- ✅ **Deadlock-free**: Discovery goroutines never block
- ✅ **Minimal URL loss**: 50k buffer holds most discoveries
- ⚠️ **Might drop URLs**: If discovery rate >> processing rate
- ✅ **Acceptable**: Crawler is best-effort, not exhaustive

### Wait() Synchronization

The Wait() method ensures all work completes before returning:

```go
func (cr *Crawler) Wait() {
    // Step 1: Wait for ALL pending work items to complete
    // wg counts: queued URLs + URLs being processed by workers
    cr.wg.Wait()

    // Step 2: Close discovery channel (safe - no more URLs will be queued)
    close(cr.discoveryChannel)

    // Step 3: Wait for processor to finish draining
    <-cr.processorDone

    // Step 4: Close worker pool
    cr.workerPool.Close()

    // Step 5: Call completion callback
    if cr.onCrawlComplete != nil {
        wasStopped := cr.ctx.Done()
        cr.onCrawlComplete(wasStopped, cr.crawledPages, cr.store.CountActions())
    }
}
```

**Key insight**: The WaitGroup tracks BOTH queued work (in discovery channel) AND processing work (in workers), ensuring Wait() doesn't return prematurely.

### Usage Example

```go
// Create crawler configuration
ctx := context.Background()
config := &bluesnake.CrawlerConfig{
    AllowedDomains:      []string{"example.com"},
    DiscoveryMechanisms: []bluesnake.DiscoveryMechanism{
        bluesnake.DiscoverySpider,
        bluesnake.DiscoverySitemap,
    },
    HTTP: &bluesnake.HTTPConfig{
        EnableRendering: true,
    },
    Parallelism: 10,
}

// Create crawler
crawler := bluesnake.NewCrawler(ctx, config)

// Set URL discovery handler (optional)
crawler.SetOnURLDiscovered(func(url string) bluesnake.URLAction {
    if strings.Contains(url, "/gtag/js") {
        return bluesnake.URLActionSkip
    }
    if strings.Contains(url, "/_next/data/") {
        return bluesnake.URLActionRecordOnly
    }
    return bluesnake.URLActionCrawl
})

// Set page crawled callback
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    fmt.Printf("Crawled: %s (status: %d, title: %s)\n",
        result.URL, result.Status, result.Title)
})

// Set completion callback
crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
    fmt.Printf("Crawl complete: %d pages, %d URLs discovered\n",
        totalPages, totalDiscovered)
})

// Start crawl
crawler.Start("https://example.com")

// Wait for completion
crawler.Wait()
```

---

## Part 2: Multi-Transport Architecture

BlueSnake uses a layered architecture that separates business logic from transport layers.

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    Transport Layer                          │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │   Desktop    │  │  HTTP Server │  │  MCP Server  │    │
│  │   (Wails)    │  │   (REST)     │  │   (Future)   │    │
│  │              │  │              │  │              │    │
│  │ WailsEmitter │  │  NoOpEmitter │  │  MCPEmitter  │    │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘    │
│         │                 │                  │             │
└─────────┼─────────────────┼──────────────────┼─────────────┘
          │                 │                  │
          └─────────────────┼──────────────────┘
                            ▼
┌─────────────────────────────────────────────────────────────┐
│              Business Logic Layer (internal/app/)           │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐  │
│  │  App (transport-agnostic)                           │  │
│  │                                                      │  │
│  │  • StartCrawl(), StopCrawl()                        │  │
│  │  • GetProjects(), GetCrawls()                       │  │
│  │  • GetConfigForDomain(), UpdateConfigForDomain()    │  │
│  │  • Uses EventEmitter interface (injected)           │  │
│  │  • No knowledge of Wails/HTTP/MCP                   │  │
│  └──────────────────────┬──────────────────────────────┘  │
│                         │                                  │
└─────────────────────────┼──────────────────────────────────┘
                          ▼
┌─────────────────────────────────────────────────────────────┐
│              Data Layer (internal/store/)                   │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐  │
│  │  Store (database operations)                        │  │
│  │                                                      │  │
│  │  • GetOrCreateProject(), DeleteProject()            │  │
│  │  • CreateCrawl(), GetCrawlResults()                 │  │
│  │  • GetOrCreateConfig(), UpdateConfig()              │  │
│  │  • SavePageLinks(), GetPageLinks()                  │  │
│  │                                                      │  │
│  │  SQLite (~/.bluesnake/bluesnake.db)                 │  │
│  └─────────────────────────────────────────────────────┘  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
                          ▲
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│           BlueSnake Crawler Package (root/)                 │
│                                                             │
│  • Channel-based URL processing (race-free)                 │
│  • HTTP requests/responses                                  │
│  • HTML parsing and link extraction                         │
│  • URL deduplication (via Crawler)                          │
│  • JavaScript rendering (chromedp)                          │
│  • Worker pool and controlled concurrency                   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### Key Design Principles

1. **Dependency Injection**: Store and EventEmitter injected into App
2. **Interface-Based Design**: EventEmitter interface allows different transport implementations
3. **Transport Agnostic**: internal/app has NO imports of Wails or HTTP libraries
4. **Separation of Concerns**: Clear boundaries between layers
5. **Testability**: Business logic can be tested without transport dependencies

### EventEmitter Pattern

```go
// internal/app/events.go
type EventEmitter interface {
    Emit(eventType EventType, data interface{})
}

// Desktop implementation (cmd/desktop/adapter.go)
type WailsEmitter struct {
    ctx context.Context
}

func (w *WailsEmitter) Emit(eventType app.EventType, data interface{}) {
    runtime.EventsEmit(w.ctx, string(eventType), data)
}

// HTTP implementation (cmd/server/main.go)
type NoOpEmitter struct{}

func (n *NoOpEmitter) Emit(eventType app.EventType, data interface{}) {
    // Do nothing - HTTP server uses polling instead
}
```

### Internal Packages (internal/)

#### App Layer (internal/app/)

**Purpose:** Business logic and crawl orchestration

**Public Methods:**
```go
// System Health
CheckSystemHealth() *types.SystemHealthCheck

// Crawl Management
StartCrawl(urlStr string) error
StopCrawl(projectID uint) error
GetActiveCrawls() []types.CrawlProgress
GetActiveCrawlData(projectID uint) (*types.CrawlResultDetailed, error)

// Project Management
GetProjects() ([]types.ProjectInfo, error)
DeleteProjectByID(projectID uint) error

// Crawl History
GetCrawls(projectID uint) ([]types.CrawlInfo, error)
GetCrawlWithResults(crawlID uint) (*types.CrawlResultDetailed, error)
DeleteCrawlByID(crawlID uint) error

// Configuration
GetConfigForDomain(urlStr string) (*types.ConfigResponse, error)
UpdateConfigForDomain(...) error

// Links
GetPageLinksForURL(crawlID uint, pageURL string) (*types.PageLinksResponse, error)

// Utilities
GetFaviconData(faviconPath string) (string, error)
GetVersion() string

// Updates (Desktop only)
CheckForUpdate() (*types.UpdateInfo, error)
DownloadAndInstallUpdate() error
```

**Constructor with Dependency Injection:**
```go
func NewApp(store *store.Store, emitter EventEmitter) *App {
    if emitter == nil {
        emitter = &NoOpEmitter{}
    }
    return &App{
        store:        store,
        emitter:      emitter,
        activeCrawls: make(map[uint]*activeCrawl),
    }
}
```

**System Health Check:**

The app provides a `CheckSystemHealth()` method that validates critical dependencies on startup:

```go
type SystemHealthCheck struct {
    IsHealthy   bool   // Overall health status
    ErrorTitle  string // Error title if not healthy
    ErrorMsg    string // Detailed error message
    Suggestion  string // Suggestion to fix the issue
}
```

**Health Checks Performed:**
1. **Chrome/Chromium Availability**: Required for JavaScript rendering
   - Checks `CHROME_EXECUTABLE_PATH` environment variable
   - Searches common installation paths (macOS, Windows, Linux)
   - Searches system PATH for Chrome executables

**Frontend Integration:**
- Called on app startup (before UI loads)
- Displays modal dialog if health check fails
- User-friendly error messages with actionable suggestions
- Non-blocking: app continues to work without Chrome (JS rendering disabled)

**Design Philosophy:**
- Method-based (not event-based) for predictable startup validation
- Fails gracefully with clear user guidance
- Testable and maintainable

**Key Implementation Details:**

1. **Crawler Integration:**
   - Creates `bluesnake.Crawler` for each crawl
   - Configures with domain-specific settings
   - Sets up high-level callbacks:
     - `OnURLDiscovered`: Filter analytics/tracking URLs
     - `OnPageCrawled`: Save pages to database
     - `OnResourceVisit`: Save resources to database
     - `OnCrawlComplete`: Update statistics and emit completion event
   - Runs in goroutine to prevent UI blocking

2. **Event Emission:**
   ```go
   a.emitter.Emit(EventCrawlStarted, nil)    // Indicational only
   a.emitter.Emit(EventCrawlCompleted, nil)  // Indicational only
   a.emitter.Emit(EventCrawlStopped, nil)    // Indicational only
   ```

   Events are **indicational only** with no payload. Frontend uses polling to fetch actual data.

   **Note:** Errors are NOT communicated via events. System errors are detected via `CheckSystemHealth()`
   at startup, and crawl errors are logged but not surfaced to the UI in real-time.

3. **Framework Detection:**
   - Uses `internal/framework/detector.go` to detect web frameworks
   - Applies framework-specific URL filters automatically
   - Supports Next.js, Nuxt.js, WordPress, Shopify, Webflow, Wix, etc.

#### Store Layer (internal/store/)

**Purpose:** Database operations and data persistence

**Key Components:**
- `Store` struct: Main database interface
- `models.go`: GORM models
- `projects.go`: Project CRUD operations
- `crawls.go`: Crawl CRUD operations
- `config.go`: Configuration management
- `links.go`: Link management

**Database Location:** `~/.bluesnake/bluesnake.db`

**Schema:**
```go
// Config - Per-domain crawl configuration
type Config struct {
    ID                     uint
    Domain                 string  // unique
    JSRenderingEnabled     bool
    Parallelism            int
    UserAgent              string
    IncludeSubdomains      bool
    SinglePageMode         bool
    DiscoveryMechanisms    string  // JSON array
    SitemapURLs            string  // JSON array
    CheckExternalResources bool
    CreatedAt              int64
    UpdatedAt              int64
}

// Project - Represents a website/domain
type Project struct {
    ID        uint
    URL       string
    Domain    string  // unique
    Crawls    []Crawl
    CreatedAt int64
    UpdatedAt int64
}

// Crawl - Individual crawl session
type Crawl struct {
    ID             uint
    ProjectID      uint
    CrawlDateTime  int64
    CrawlDuration  int64
    PagesCrawled   int
    DiscoveredUrls []DiscoveredUrl
    CreatedAt      int64
    UpdatedAt      int64
}

// DiscoveredUrl - Individual URL
type DiscoveredUrl struct {
    ID              uint
    CrawlID         uint
    URL             string
    Visited         bool
    Status          int
    Title           string
    MetaDescription string
    ContentHash     string
    Indexable       string
    ContentType     string
    Error           string
    CreatedAt       int64
}

// PageLink - Link relationship
type PageLink struct {
    ID          uint
    CrawlID     uint
    SourceURL   string
    TargetURL   string
    Type        string  // "anchor", "image", "script", etc.
    Text        string
    Context     string
    IsInternal  bool
    Status      int
    Title       string
    ContentType string
    Position    string  // "content", "navigation", etc.
    DOMPath     string
    URLAction   string  // "crawl", "record", "skip"
}
```

---

## Part 3: Desktop Application (cmd/desktop/)

### Technology Stack

- **Backend:** Go with Wails v2 (adapter layer only)
- **Frontend:** React + TypeScript + Vite
- **Database:** SQLite with GORM (via internal/store)
- **Communication:** Wails runtime bindings + events

### Backend Components

#### Main Entry Point (`main.go`)

```go
func main() {
    // Initialize store
    st, err := store.NewStore()

    var desktopApp *DesktopApp

    err = wails.Run(&options.App{
        Title: "BlueSnake - Web Crawler",
        OnStartup: func(ctx context.Context) {
            // Create Wails-specific event emitter
            emitter := NewWailsEmitter(ctx)

            // Create core app with injected dependencies
            coreApp := app.NewApp(st, emitter)

            // Create desktop adapter
            desktopApp = NewDesktopApp(coreApp)
            desktopApp.Startup(ctx)
        },
        Bind: []interface{}{desktopApp},
    })
}
```

#### Wails Adapter (`adapter.go`)

**WailsEmitter:**
```go
type WailsEmitter struct {
    ctx context.Context
}

func (w *WailsEmitter) Emit(eventType app.EventType, data interface{}) {
    runtime.EventsEmit(w.ctx, string(eventType), data)
}
```

**DesktopApp:** Thin wrapper that delegates to internal/app
```go
type DesktopApp struct {
    app *app.App
    ctx context.Context
}

func (d *DesktopApp) GetProjects() ([]types.ProjectInfo, error) {
    return d.app.GetProjects() // Simple delegation
}
```

### Frontend Components

#### App Component (`App.tsx`)

**Views:**
- **Start Screen:** URL input, recent projects grid
- **Crawl Screen:** Live crawl results, statistics
- **Config Screen:** Domain-specific settings

**State Management:**
```typescript
interface CrawlResult {
  url: string
  status: number
  title: string
  indexable: string
  error?: string
}

interface ProjectInfo {
  id: number
  url: string
  domain: string
  crawlDateTime: number
  crawlDuration: number
  pagesCrawled: number
  latestCrawlId: number
}
```

**Polling Architecture:**

The frontend uses polling instead of event-driven updates:

**Home Page Polling (500ms):**
```typescript
useEffect(() => {
  if (view !== 'start') return

  const pollHomeData = async () => {
    const projectList = await GetProjects()
    const crawls = await GetActiveCrawls()
    // Update state
  }

  pollHomeData()
  if (activeCrawls.size > 0) {
    const interval = setInterval(pollHomeData, 500)
    return () => clearInterval(interval)
  }
}, [view, activeCrawls.size])
```

**Crawl Page Polling (2s normal, 500ms when stopping):**
```typescript
useEffect(() => {
  if (view !== 'crawl' || !currentProject) return

  const pollCrawlData = async () => {
    const crawls = await GetActiveCrawls()
    const activeCrawl = crawls.find(c => c.projectId === currentProject.id)

    if (activeCrawl) {
      const crawlData = await GetActiveCrawlData(currentProject.id)
      setResults(crawlData.results)
    } else {
      const crawlData = await GetCrawlWithResults(currentCrawlId)
      setResults(crawlData.results)
    }
  }

  pollCrawlData()
  const isStoppingProject = stoppingProjects.has(currentProject.id)
  if (isCrawling || isStoppingProject) {
    const pollInterval = isStoppingProject ? 500 : 2000
    const interval = setInterval(pollCrawlData, pollInterval)
    return () => clearInterval(interval)
  }
}, [view, currentProject, isCrawling, stoppingProjects])
```

**Event Listeners (Indicational Only):**
```typescript
EventsOn("crawl:started", () => {
  loadProjects() // Just trigger refresh
})

EventsOn("crawl:completed", () => {
  loadProjects() // Just trigger refresh
})
```

**Why Polling Instead of Events:**
- Database is single source of truth
- More reliable at scale
- Easier to optimize (batching, pagination)
- Less network traffic
- Simpler code

#### Config Component (`Config.tsx`)

**Settings:**
- JavaScript Rendering (enable/disable chromedp)
- Parallelism (1-100 concurrent requests)
- Include Subdomains
- Single Page Mode
- Discovery Mechanisms (Spider, Sitemap, or both)
- Custom Sitemap URLs
- Check External Resources

### Communication Architecture

#### Frontend → Backend (Method Calls)

```typescript
import { StartCrawl, GetProjects } from "../wailsjs/go/main/DesktopApp"

await StartCrawl("https://example.com")
const projects = await GetProjects()
```

#### Backend → Frontend (Events)

```go
runtime.EventsEmit(ctx, "crawl:started")    // No payload
runtime.EventsEmit(ctx, "crawl:completed")  // No payload
```

---

## Part 4: Storage Architecture

### Two Separate Storage Systems

#### 1. Crawler Storage (In-Memory)

**Location:** `storage.CrawlerStore`

**Purpose:** Track visited URLs during active crawl

**Lifecycle:**
- Created when crawler starts
- Destroyed when crawler completes
- **Non-persistent**

**Data Stored:**
- Hash of visited URLs (atomic tracking)
- URL actions (memoization)
- Page metadata (for link population)

#### 2. Application Storage (SQLite)

**Location:** `~/.bluesnake/bluesnake.db`

**Purpose:** Persist crawl history and results

**Lifecycle:**
- Initialized on app startup
- Persists across app restarts

**Data Stored:**
- Projects (domains)
- Crawls (sessions)
- Discovered URLs (visited and unvisited)
- Page Links (with URLAction)
- Configurations (per-domain settings)

### Storage Relationship

```
┌─────────────────────────────────────────┐
│      Desktop/Server App                 │
│                                         │
│  ┌───────────────────────────────────┐ │
│  │  SQLite (persistent)              │ │
│  │  • Projects                       │ │
│  │  • Crawls                         │ │
│  │  • DiscoveredUrls                 │ │
│  │  • PageLinks                      │ │
│  │  • Config                         │ │
│  └───────────────────────────────────┘ │
│                ▲                        │
│                │ Saves results          │
│  ┌─────────────┴─────────────────────┐ │
│  │  Crawler Integration (app.go)     │ │
│  │  • Creates Crawler                │ │
│  │  • Sets callbacks                 │ │
│  │  • Emits events                   │ │
│  └─────────────┬─────────────────────┘ │
└────────────────┼───────────────────────┘
                 │ Uses
                 ▼
┌─────────────────────────────────────────┐
│    BlueSnake Crawler Package            │
│                                         │
│  ┌───────────────────────────────────┐ │
│  │  Crawler                          │ │
│  │  • Channel-based processing       │ │
│  │  • Worker pool                    │ │
│  │  • Callbacks                      │ │
│  └─────────────┬─────────────────────┘ │
│                │                        │
│  ┌─────────────┴─────────────────────┐ │
│  │  CrawlerStore (ephemeral)         │ │
│  │  • Visit tracking                 │ │
│  │  • URL actions                    │ │
│  │  • Page metadata                  │ │
│  └───────────────────────────────────┘ │
│                                         │
│  • No persistence                       │
│  • No knowledge of SQLite               │
│  • Fresh instance per crawl             │
└─────────────────────────────────────────┘
```

---

## Crawl Execution Flow

```
Frontend (React)          Backend (Go)           Crawler              Database
     │                         │                    │                    │
     │ StartCrawl(url)        │                    │                    │
     ├────────────────────────▶│                    │                    │
     │                         │ normalizeURL()     │                    │
     │                         │ GetOrCreateProject()──────────────────▶│
     │                         │ CreateCrawl()──────────────────────────▶│
     │                         │ GetOrCreateConfig()────────────────────▶│
     │                         │                    │                    │
     │                         │ go runCrawler()    │                    │
     │                         │    NewCrawler()───▶│                    │
     │                         │                    │ Init()             │
     │                         │                    │ Start worker pool  │
     │                         │                    │ Start processor    │
     │◀─ "crawl:started" ──────│ (no payload)       │                    │
     │  loadProjects()         │                    │                    │
     │                         │                    │                    │
     │  [Polling @ 2s]         │                    │                    │
     │ GetActiveCrawlData()────▶│                    │                    │
     │◀────────────────────────│◀─GetCrawlResults()─────────────────────▶│
     │                         │                    │                    │
     │                         │  [URL Processor]   │                    │
     │                         │  processDiscoveredURL()                 │
     │                         │  VisitIfNotVisited()─▶│ (ATOMIC)       │
     │                         │  Submit to pool    │                    │
     │                         │                    │                    │
     │                         │  [Worker]          │                    │
     │                         │  FetchURL()        │                    │
     │                         │  OnResponse()      │                    │
     │                         │  OnHTML()          │                    │
     │                         │  OnScraped()       │                    │
     │                         │                    │                    │
     │                         ├──SaveDiscoveredUrl()──────────────────▶│
     │                         ├──SavePageLinks()──────────────────────▶│
     │                         │                    │                    │
     │                         │  Wait()            │                    │
     │                         │                    │ (wg.Wait())        │
     │                         │                    │                    │
     │                         ├──UpdateCrawlStats()───────────────────▶│
     │◀─ "crawl:completed" ────│ (no payload)       │                    │
     │  loadProjects()         │                    │                    │
```

---

## HTTP Server (cmd/server/)

### Initialization

```go
func main() {
    st, err := store.NewStore()

    coreApp := app.NewApp(st, &app.NoOpEmitter{})
    coreApp.Startup(context.Background())

    server := NewServer(coreApp)

    httpServer := &http.Server{
        Addr:    ":8080",
        Handler: server,
    }
    httpServer.ListenAndServe()
}
```

### REST API Endpoints

```
GET    /api/v1/health
GET    /api/v1/version
GET    /api/v1/projects
GET    /api/v1/projects/{id}/crawls
DELETE /api/v1/projects/{id}

GET    /api/v1/crawls/{id}
DELETE /api/v1/crawls/{id}
GET    /api/v1/crawls/{id}/pages/{url}/links

POST   /api/v1/crawl
POST   /api/v1/stop-crawl/{projectID}
GET    /api/v1/active-crawls

GET    /api/v1/config?url=example.com
PUT    /api/v1/config
```

---

## Key Architectural Patterns

### 1. Separation of Concerns

- **Crawler Package:** Pure crawling logic, no UI dependencies
- **Desktop/Server App:** UI and persistence
- **Database Layer:** Abstracted via GORM

### 2. Polling-Based Architecture

- Crawler callbacks save to database
- Frontend polls for updates
- Events used only for immediate refresh triggers
- Database is single source of truth

### 3. Channel-Based Concurrency

- Single-threaded URL processor (eliminates race conditions)
- Non-blocking discovery channel (prevents deadlocks)
- Fixed worker pool (controls concurrency)
- Backpressure via blocking worker submission
- Clean shutdown via context cancellation

### 4. Domain-Driven Design

- Projects organized by domain
- Per-domain configuration
- Crawl history per project

### 5. Asynchronous Execution

- Crawler runs in goroutine
- Non-blocking UI
- Frontend polls at regular intervals

---

## Responsibility Division

### BlueSnake Package (Core Library)

**Belongs:**
- Channel-based URL processing
- HTTP request/response handling
- HTML/XML parsing
- URL deduplication (via CrawlerStore)
- Domain filtering and robots.txt
- JavaScript rendering
- Worker pool management
- In-memory state during crawl

**Does NOT belong:**
- Database operations
- UI code
- Persistent storage
- Configuration persistence
- Favicon management
- Historical crawl comparison

### Internal Packages

**internal/store/ (Database Layer):**
- GORM models
- SQLite operations
- CRUD operations
- Favicon file management

**internal/app/ (Business Logic):**
- Crawl orchestration
- Project management
- Configuration management
- Active crawl tracking
- URL normalization
- Callbacks that save to database
- **NO transport-specific code**
- **NO UI logic**

**internal/framework/ (Framework Detection):**
- Web framework detection
- Framework-specific URL filters

### Desktop/Server Apps (Transports)

**Belongs:**
- Wails/HTTP initialization
- Event emitter implementations
- UI integration
- User interaction handling
- Polling logic

**Does NOT belong:**
- Business logic (use internal/app)
- Database operations (use internal/store)
- Crawl orchestration (use internal/app)

---

## Extension Points

### 1. Custom Callbacks

```go
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    // Custom page processing
})

crawler.SetOnURLDiscovered(func(url string) bluesnake.URLAction {
    // Custom URL filtering
    return bluesnake.URLActionCrawl
})
```

### 2. Additional Backend APIs

```go
func (a *App) ExportCrawlToCSV(crawlID uint) (string, error) {
    // Export logic
}
```

### 3. Database Schema Extensions

```go
type PerformanceMetric struct {
    ID            uint
    CrawlID       uint
    URL           string
    ResponseTime  int64
}

db.AutoMigrate(&PerformanceMetric{})
```

---

## Performance Considerations

### Concurrency

- **Crawler:** Channel-based with fixed worker pool
- **Worker Pool:** Configurable parallelism (default: 10)
- **URL Processor:** Single-threaded (prevents race conditions)
- **Goroutines:** Bounded (1 processor + N workers)

### Memory Management

- **Discovery Channel:** 50k buffer
- **Work Queue:** 1k buffer
- **MaxBodySize:** Limit response size
- **Cleanup:** CrawlerStore cleared after crawl

### Database Optimization

- **Indexes:** Domain and CrawlID indexed
- **Cascade Deletes:** Efficient cleanup

---

## Security Considerations

### URL Validation

- Normalization prevents ambiguous URLs
- Domain whitelisting enforces boundaries
- Protocol validation

### Rate Limiting

- Configurable delays
- Per-domain limits via worker pool

### Robots.txt Compliance

- Optional compliance
- Three modes: "respect", "ignore", "ignore-report"

### Database Access

- Local SQLite file
- No network exposure
- OS-managed permissions

---

## Adding New Transports

### Example: CLI

```go
func main() {
    st, err := store.NewStore()
    coreApp := app.NewApp(st, &app.NoOpEmitter{})
    coreApp.Startup(context.Background())

    url := flag.String("url", "", "URL to crawl")
    flag.Parse()

    if err := coreApp.StartCrawl(*url); err != nil {
        log.Fatal(err)
    }

    for {
        crawls := coreApp.GetActiveCrawls()
        if len(crawls) == 0 {
            break
        }
        time.Sleep(1 * time.Second)
    }
}
```

No changes to internal packages required!

---

## Conclusion

BlueSnake demonstrates a clean layered architecture:

1. **Crawler Package** - Channel-based crawling with race-free visit tracking
2. **Store Layer** - Data persistence
3. **Business Logic** - Transport-agnostic orchestration
4. **Transport Layers** - Thin adapters (Desktop, HTTP Server, future: CLI, MCP)

### Key Benefits

1. **Code Reuse**: Same business logic powers all transports
2. **Testability**: Each layer tested independently
3. **Extensibility**: Add new transports without modifying core
4. **Maintainability**: Clear boundaries and single responsibility
5. **Reliability**: Channel-based architecture eliminates race conditions
6. **Determinism**: Every crawl produces consistent, complete results

### Design Patterns

- **Repository Pattern**: Store layer abstracts database
- **Dependency Injection**: Store and EventEmitter injected
- **Adapter Pattern**: Transport layers adapt core App
- **Observer Pattern**: EventEmitter for decoupled events
- **Channel-Based Concurrency**: Single-threaded processor with worker pool
- **Pipeline Pattern**: URL discovery → processor → worker pool
