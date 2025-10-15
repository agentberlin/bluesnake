# BlueSnake Architecture

## Overview

BlueSnake is a web crawler application with multiple interfaces, consisting of these main components:
1. **BlueSnake Crawler Package** - A Go-based web scraping library with channel-based architecture (root directory)
2. **Internal Packages** - Shared business logic and data layers (internal/ directory)
3. **Desktop Application** - A Wails-based GUI application (cmd/desktop/ directory)
4. **HTTP Server** - A REST API server (cmd/server/ directory)
5. **Future: MCP Server** - Model Context Protocol server (planned)

## Project Structure

```
bluesnake/
├── *.go                    # Core crawler package files
│   ├── bluesnake.go        # Collector (low-level HTTP engine)
│   ├── crawler.go          # Crawler (high-level orchestration)
│   ├── worker_pool.go      # Fixed-size worker pool
├── storage/               # Storage abstraction for crawler
│   └── storage.go         # InMemoryStorage with VisitIfNotVisited
├── debug/                 # Debugging utilities
├── extensions/            # Crawler extensions
├── proxy/                 # Proxy support
├── queue/                 # Request queuing
├── internal/              # Internal packages (shared across transports)
│   ├── version/           # Version constant
│   │   └── version.go
│   ├── types/             # Shared types (ProjectInfo, CrawlInfo, etc.)
│   │   └── types.go
│   ├── store/             # Database layer (repository pattern)
│   │   ├── store.go       # Store initialization
│   │   ├── models.go      # GORM models
│   │   ├── projects.go    # Project CRUD operations
│   │   ├── crawls.go      # Crawl CRUD operations
│   │   ├── config.go      # Config CRUD operations
│   │   └── links.go       # PageLink CRUD operations
│   └── app/               # Business logic (transport-agnostic)
│       ├── events.go      # EventEmitter interface
│       ├── app.go         # Core App struct
│       ├── utils.go       # URL normalization helpers
│       ├── crawler.go     # Crawl orchestration
│       ├── active_crawls.go  # Active crawl tracking
│       ├── projects.go    # Project management
│       ├── crawls.go      # Crawl management
│       ├── config.go      # Config management
│       ├── links.go       # Link management
│       ├── favicon.go     # Favicon handling
│       └── updater.go     # Update checking
└── cmd/                   # Application executables
    ├── desktop/           # Wails desktop application
    │   ├── main.go        # Application entry point (dependency injection)
    │   ├── adapter.go     # Wails adapter (WailsEmitter, DesktopApp)
    │   └── frontend/      # React/TypeScript UI
    │       └── src/
    │           ├── App.tsx     # Main UI component
    │           └── Config.tsx  # Configuration UI
    └── server/            # HTTP REST API server
        ├── main.go        # Server initialization
        └── server.go      # HTTP handlers and routing
```

---

## Part 1: BlueSnake Crawler Package

### Architecture Overview: Channel-Based Race Condition Fix

The crawler package underwent a major architectural transformation to eliminate race conditions in URL visit tracking. The new design uses a **channel-based architecture with a single-threaded URL processor** to guarantee deterministic and complete crawls.

### Problem Statement: Race Condition in URL Visit Tracking

**Original Issue:** Multiple crawls of the same website produced inconsistent results, with URLs randomly missing from different crawl runs. For example, crawling `agentberlin.ai` produced:

- **URL counts varied**: 138-141 URLs per crawl (9 crawls tested)
- **Root URL missing**: `https://agentberlin.ai/` appeared in only 6 out of 9 crawls (67%)
- **Other URLs missing**: 8 different URLs were missing across various crawls

**Root Cause:** Multiple goroutines racing to mark URLs as visited created a "winner-takes-all" scenario where the winner might never complete the fetch:

```
TIME    | Thread 1 (Sitemap)              | Thread 2 (Spider)
--------|----------------------------------|----------------------------------
T0      | Discovers https://example.com/  |
T1      | Visit(url)                       |
T2      | → VisitIfNotVisited() ✓         |
T3      |   (marks as visited)             |
T4      |                                  | Discovers https://example.com/
T5      | Returns from check               | Visit(url)
T6      |                                  | → VisitIfNotVisited() ✗
T7      |                                  |   (already visited!)
T8      |                                  | Returns error, skips URL
T9      | go fetch() spawned               |
T10     | [Goroutine delayed/fails]        |
```

**Result**: URL marked as "visited" but **never actually crawled** because:
1. Thread 2 saw it was already marked and skipped it
2. Thread 1's goroutine was delayed, failed, or cancelled before fetching
3. No mechanism to detect or recover from this state

### Solution: Channel-Based Architecture with Single-Threaded Processor

#### Design Principles

1. **Single source of truth**: Only ONE goroutine (the processor) decides what gets crawled
2. **Clear separation of concerns**:
   - `Collector` = low-level HTTP engine (fetch, parse, callbacks)
   - `Crawler` = high-level orchestration (discovery, deduplication, queueing)
3. **Guaranteed work assignment**: URLs marked as visited are immediately assigned to workers
4. **Controlled concurrency**: Fixed worker pool, no unbounded goroutine creation

#### Architecture Diagram

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

### Core Components

#### 1. Crawler (High-Level Orchestration - `crawler.go`)

**Responsibilities:**
- URL discovery coordination (spider, sitemap, network, resources)
- Deduplication (track discovered URLs, call `OnURLDiscovered` callback once)
- Visit tracking (single-threaded processor marks URLs as visited)
- Work distribution (submit to worker pool)
- Link graph building (track relationships between pages)
- Statistics and metrics (crawled pages, discovered URLs, dropped URLs)

**Does NOT do:**
- HTTP requests
- HTML parsing
- Managing HTTP client/transport

**Key Type:**
```go
type Crawler struct {
    Collector       *Collector  // Low-level HTTP engine (exported)
    onPageCrawled   OnPageCrawledFunc
    onResourceVisit OnResourceVisitFunc
    onCrawlComplete OnCrawlCompleteFunc
    onURLDiscovered OnURLDiscoveredFunc

    // Channel-based URL processing
    discoveryChannel chan URLDiscoveryRequest  // Buffered: 50k
    processorDone    chan struct{}
    workerPool       *WorkerPool               // Fixed size: 10 workers
    droppedURLs      uint64                    // Dropped due to full channel

    queuedURLs      *sync.Map  // map[string]URLAction - memoization
    pageMetadata    *sync.Map  // map[string]PageMetadata - cached data
    rootDomain      string
    crawledPages    int
}
```

#### 2. Collector (Low-Level HTTP Engine - `bluesnake.go`)

**Responsibilities:**
- HTTP requests (GET, POST, HEAD with customizable headers)
- Response handling (status codes, headers, body)
- HTML/XML parsing (GoQuery, xmlquery)
- Low-level callback execution (OnRequest, OnResponse, OnHTML, OnXML, OnScraped, OnError)
- Caching (optional disk cache)
- Robots.txt handling
- Content hashing (duplicate content detection)
- JavaScript rendering (optional chromedp integration)

**Does NOT do:**
- URL deduplication (no visit tracking - removed to eliminate races)
- URL discovery (doesn't decide what to crawl next)
- Work queueing (doesn't manage goroutines)

**Note**: `Collector` is now an internal implementation detail. Users interact with `Crawler`, which provides the high-level crawling API.

**Key Configuration:**
```go
type CollectorConfig struct {
    UserAgent              string
    Headers                map[string]string
    MaxDepth               int
    AllowedDomains         []string
    DisallowedDomains      []string
    DisallowedURLFilters   []*regexp.Regexp
    URLFilters             []*regexp.Regexp
    AllowURLRevisit        bool
    MaxBodySize            int
    CacheDir               string
    IgnoreRobotsTxt        bool
    Async                  bool
    ParseHTTPErrorResponse bool
    ID                     uint32
    DetectCharset          bool
    CheckHead              bool
    TraceHTTP              bool
    Context                context.Context
    MaxRequests            uint32
    EnableRendering        bool
    RenderingConfig        *RenderingConfig
    CacheExpiration        time.Duration
    Debugger               debug.Debugger
    DiscoveryMechanisms    []DiscoveryMechanism  // ["spider"], ["sitemap"], or both
    SitemapURLs            []string
    EnableContentHash      bool
    ContentHashAlgorithm   string
    ContentHashConfig      *ContentHashConfig
    ResourceValidation     *ResourceValidationConfig
    RobotsTxtMode          string  // "respect", "ignore", "ignore-report"
    FollowInternalNofollow bool
    FollowExternalNofollow bool
    RespectMetaRobotsNoindex bool
    RespectNoindex         bool

    // Channel-based architecture configuration
    DiscoveryChannelSize   int  // Default: 50000
    WorkQueueSize          int  // Default: 1000
    Parallelism            int  // Default: 10
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

        case <-cr.Collector.Context.Done():
            cr.drainDiscoveryChannel()
            return
        }
    }
}

func (cr *Crawler) processDiscoveredURL(req URLDiscoveryRequest) {
    // Step 1: Determine action (callback called once, memoized)
    action := cr.getOrDetermineURLAction(req.URL)
    if action == URLActionSkip {
        return
    }

    // Step 2: Pre-filtering (domain checks, URL filters, robots.txt)
    if !cr.isURLCrawlable(req.URL) {
        return
    }

    // Step 3: Check and mark as visited (ATOMIC, SINGLE-THREADED)
    uHash := requestHash(req.URL, nil)
    alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)
    if err != nil || alreadyVisited {
        return
    }

    // Step 4: Only crawl if action is "crawl" (not "record")
    if action != URLActionCrawl {
        return
    }

    // Step 5: Submit to worker pool (BLOCKS if queue full)
    // This provides backpressure - processor can't race ahead
    err = cr.workerPool.Submit(func() {
        // Fetch without revisit check (we already checked above)
        cr.Collector.wg.Add(1)
        cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)
    })
}
```

**Why this eliminates the race:**
- Only ONE goroutine calls `VisitIfNotVisited()` at a time
- Impossible for two goroutines to race on the same URL
- URL is marked visited → immediately submitted to worker → guaranteed assignment

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
    select {
    case cr.discoveryChannel <- req:
        // Successfully queued

    case <-cr.Collector.Context.Done():
        // Context cancelled - drop URL

    default:
        // Channel full - drop URL (prevents deadlock)
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

#### 6. Storage Package (`storage/storage.go`)

Provides an abstraction for storing crawler state:

```go
type Storage interface {
    Init() error
    Visited(requestID uint64) error
    IsVisited(requestID uint64) (bool, error)
    VisitIfNotVisited(requestID uint64) (bool, error)  // ★ Atomic operation
    Cookies(u *url.URL) string
    SetCookies(u *url.URL, cookies string)
    SetContentHash(url string, contentHash string) error
    GetContentHash(url string) (string, error)
    IsContentVisited(contentHash string) (bool, error)
    VisitedContent(contentHash string) error
}
```

**Default Implementation: `InMemoryStorage`**
- Stores visited URLs in memory (hash-based)
- Manages cookies via `http.CookieJar`
- Non-persistent (data lost when crawler stops)
- Thread-safe with mutex locks
- **VisitIfNotVisited()** is atomic: check + mark in one operation under lock

### Callback Architecture: Two Levels

The framework has **two distinct levels of callbacks**:

#### Low-Level Callbacks (Collector - Internal Use Only)

These callbacks are registered on `Collector` and used internally by `Crawler` to build its high-level functionality. **Users should not interact with these directly.**

```go
// Low-level callbacks (internal use by Crawler)
collector.OnHTML("html", func(e *HTMLElement) {
    // Extract links, build link graph
    // Called by Crawler's setupCallbacks()
})

collector.OnResponse(func(r *Response) {
    // Handle response metadata
    // Store metadata for link population
})

collector.OnScraped(func(r *Response) {
    // Final processing after HTML parsing
    // Build PageResult, call high-level callback
})

collector.OnError(func(r *Response, err error) {
    // Error handling
    // Route to appropriate high-level callback
})
```

**Purpose**: Raw HTTP lifecycle hooks for internal orchestration.

#### High-Level Callbacks (Crawler - User API)

These callbacks are registered on `Crawler` and provide the **user-facing API**. They deliver processed, structured data.

```go
// High-level callbacks (user API)

// 1. OnURLDiscovered - Called once per unique URL
crawler.SetOnURLDiscovered(func(url string) URLAction {
    // Decide: crawl, record, or skip this URL
    // Called exactly once per URL (memoized)
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
    // result contains: URL, status, contentType
})

// 4. OnCrawlComplete - Called once when crawl finishes
crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
    // Crawl finished (naturally or via cancellation)
    // Receive final statistics
})
```

**Purpose**: Clean, high-level API for users to consume crawl data.

#### URL Action System

The `OnURLDiscovered` callback returns a `URLAction` to control how each URL is handled:

```go
type URLAction string

const (
    // URLActionCrawl - Add to links AND crawl
    URLActionCrawl URLAction = "crawl"

    // URLActionRecordOnly - Add to links but DON'T crawl
    // (e.g., framework-specific paths like /_next/data/)
    URLActionRecordOnly URLAction = "record"

    // URLActionSkip - Ignore completely
    // (e.g., analytics/tracking URLs like /gtag/js)
    URLActionSkip URLAction = "skip"
)
```

**Use cases:**
- Return `URLActionCrawl` for normal URLs that should be crawled
- Return `URLActionRecordOnly` for framework-specific paths that should appear in links but not be crawled (e.g., Next.js data endpoints)
- Return `URLActionSkip` for analytics/tracking URLs that should be ignored completely

#### Callback Flow Example

```
User registers:  crawler.SetOnPageCrawled(userFunc)
                 crawler.SetOnCrawlComplete(userCompleteFunc)
                         ↓
Crawler registers internal callbacks on Collector:
                 collector.OnHTML(extractLinks)
                 collector.OnScraped(buildPageResult)
                         ↓
Worker fetches URL → Collector processes:
                         ↓
         OnResponse (collector) → Store metadata
                         ↓
         OnHTML (collector) → Extract links → queueURL()
                         ↓
         OnScraped (collector) → Build PageResult
                         ↓
         Call userFunc(PageResult)  ← User callback invoked
                         ↓
All pages done → Call userCompleteFunc()  ← Completion callback
```

**Key insight**: Users never touch `Collector` callbacks. `Crawler` uses them internally to orchestrate the crawl and deliver clean, structured data to user callbacks.

### Complete URL Lifecycle

```
1. URL Discovered (spider finds <a href="/page">)
   ↓
2. queueURL() - Non-blocking send to discovery channel
   ↓
3. Processor receives URL from channel (SINGLE-THREADED)
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
   ↓
9. Worker: scrape() - Fetch HTTP (checkRevisit=false, no double-check)
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

**Trade-off:**
- ✅ **Deadlock-free**: Discovery goroutines never block
- ✅ **Minimal URL loss**: 50k buffer holds most discoveries
- ⚠️ **Might drop URLs**: If discovery rate >> processing rate
- ✅ **Acceptable**: Crawler is best-effort, not exhaustive

### Benefits of Channel-Based Architecture

#### 1. Eliminates Race Condition

**Before:**
```
N goroutines race to call VisitIfNotVisited(url)
→ Multiple goroutines compete
→ Winner marks it, losers skip
→ Winner might fail → URL lost
```

**After:**
```
N goroutines send url to channel (non-blocking)
→ Single processor dequeues sequentially
→ Only ONE call to VisitIfNotVisited per URL
→ Impossible to race
```

#### 2. Guarantees Work Assignment

**Before:**
```
URL marked visited in Collector.requestCheck()
↓
Goroutine spawned (unbounded)
↓
[Goroutine waits for parallelism limit]
↓
[Goroutine might fail or be cancelled]
→ URL marked but never crawled ✗
```

**After:**
```
URL marked visited in Crawler processor
↓
Submit to worker pool (BLOCKS if full)
↓
Worker accepts work
↓
Worker executes fetch
→ URL marked AND assigned to worker ✓
```

#### 3. Controlled Concurrency

| Metric | Before (Unbounded) | After (Worker Pool) |
|--------|-------------------|---------------------|
| Goroutines | 10,000 (one per URL) | 11 (1 processor + 10 workers) |
| Memory | ~20MB (2KB/goroutine) | ~2MB (channel + pool) |
| Parallelism | Hard to control | Exact (10 workers = 10 concurrent) |

#### 4. Clean Shutdown

**Before:**
```
Context cancelled
↓
Goroutines race to completion
↓
Hard to ensure all stopped
```

**After:**
```
Context cancelled
↓
Processor stops accepting URLs
↓
Drains remaining channel URLs
↓
Worker pool completes in-flight requests
↓
Clean shutdown ✓
```

#### 5. Observability

**Metrics available:**
- `len(discoveryChannel)` = pending discoveries
- `len(workQueue)` = pending work items
- `droppedURLs` = URLs dropped due to full channel
- `crawledPages` = successfully crawled pages
- `queuedURLs.Len()` = total discovered URLs

### Configuration and Tuning

#### Recommended Settings

```go
// Small websites (<1000 URLs)
config := &CollectorConfig{
    DiscoveryChannelSize: 5000,
    WorkQueueSize:        500,
    Parallelism:          5,
}

// Medium websites (1000-10000 URLs)
config := &CollectorConfig{
    DiscoveryChannelSize: 20000,
    WorkQueueSize:        1000,
    Parallelism:          10,
}

// Large websites (>10000 URLs)
config := &CollectorConfig{
    DiscoveryChannelSize: 100000,
    WorkQueueSize:        2000,
    Parallelism:          20,
}
```

#### Tuning Guidelines

**Seeing "Discovery channel full" warnings?**
```go
config.DiscoveryChannelSize *= 2  // Double the buffer
```

**Memory usage too high?**
```go
config.DiscoveryChannelSize /= 2  // Reduce buffer
config.Parallelism *= 2           // Drain faster with more workers
```

**Crawl too slow?**
```go
config.Parallelism *= 2           // More concurrent requests
config.WorkQueueSize = config.Parallelism * 100  // Adjust queue
```

### Usage Example

```go
// Create crawler configuration
config := &bluesnake.CollectorConfig{
    AllowedDomains:      []string{"example.com"},
    Async:               true,
    EnableRendering:     true,
    DiscoveryMechanisms: []bluesnake.DiscoveryMechanism{
        bluesnake.DiscoverySpider,
        bluesnake.DiscoverySitemap,
    },
    // Channel-based architecture settings (optional - have defaults)
    DiscoveryChannelSize: 50000,
    WorkQueueSize:        1000,
    Parallelism:          10,
}

// Create crawler
crawler := bluesnake.NewCrawler(config)

// Set URL discovery handler (optional - controls which URLs to crawl)
crawler.SetOnURLDiscovered(func(url string) bluesnake.URLAction {
    // Skip analytics URLs
    if strings.Contains(url, "/gtag/js") || strings.Contains(url, "google-analytics") {
        return bluesnake.URLActionSkip
    }
    // Record but don't crawl Next.js data endpoints
    if strings.Contains(url, "/_next/data/") {
        return bluesnake.URLActionRecordOnly
    }
    // Crawl everything else
    return bluesnake.URLActionCrawl
})

// Set page crawled callback
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    fmt.Printf("Crawled: %s (status: %d, title: %s)\n",
        result.URL, result.Status, result.Title)

    // Access page content
    html := result.GetHTML()
    textContent := result.GetTextContent()

    // Process links
    for _, link := range result.Links.Internal {
        fmt.Printf("  → %s (%s)\n", link.URL, link.Type)
    }
})

// Set resource visit callback (optional)
crawler.SetOnResourceVisit(func(result *bluesnake.ResourceResult) {
    fmt.Printf("Resource: %s (status: %d, type: %s)\n",
        result.URL, result.Status, result.ContentType)
})

// Set completion callback
crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
    fmt.Printf("Crawl complete: %d pages crawled, %d URLs discovered\n",
        totalPages, totalDiscovered)
})

// Start crawl
crawler.Start("https://example.com")

// Wait for completion
crawler.Wait()
```

---

## Part 2: Multi-Transport Architecture

BlueSnake uses a layered architecture that separates business logic from transport layers, enabling the same core functionality to be exposed via multiple interfaces.

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
│  │ EventsEmit() │  │     (stub)   │  │  (planned)   │    │
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

The `EventEmitter` interface allows each transport to handle events differently:

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

The `App` struct provides the core business logic, decoupled from any transport:

**Public Methods (available to all transports):**

```go
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
// Create new App with injected dependencies
func NewApp(store *store.Store, emitter EventEmitter) *App {
    if emitter == nil {
        emitter = &NoOpEmitter{} // Default to no-op
    }
    return &App{
        store:        store,
        emitter:      emitter,
        activeCrawls: make(map[uint]*activeCrawl),
    }
}
```

**Key Implementation Details:**

1. **URL Normalization:**
   - Adds `https://` if no protocol specified
   - Extracts domain for project identification
   - Handles non-standard ports

2. **Crawler Integration (using High-Level API):**
   - Creates new `bluesnake.Crawler` instance for each crawl
   - Configures with domain-specific settings from database
   - Sets up high-level callbacks:
     - `OnURLDiscovered`: Filter analytics/tracking URLs
     - `OnPageCrawled`: Saves each crawled page to database
     - `OnResourceVisit`: Saves resources (images, CSS, JS) to database
     - `OnCrawlComplete`: Updates final statistics and emits completion event
   - Runs in goroutine to prevent UI blocking
   - Desktop app only handles DB/UI logic - all crawling is in bluesnake package

3. **Event Emission:**
   ```go
   a.emitter.Emit(EventCrawlStarted, nil)    // Indicational only, no payload
   a.emitter.Emit(EventCrawlCompleted, nil)  // Indicational only, no payload
   a.emitter.Emit(EventCrawlStopped, nil)    // Indicational only, no payload
   ```

   **Important:** Events are **indicational only** and carry no payload. The frontend uses polling to fetch actual data from the backend via method calls. The emitter interface allows each transport to handle events differently:
   - **Desktop:** Emits Wails events via `runtime.EventsEmit()`
   - **HTTP:** Uses `NoOpEmitter` (no events, clients poll instead)
   - **Future MCP:** Would emit MCP notifications

   This design decision was made because:
   - At scale, emitting millions of URL events adds complexity
   - Polling from database is more reliable and predictable
   - Simpler synchronization logic
   - Easier to implement future optimizations (batching, pagination, etc.)
   - Transport-agnostic approach

#### Store Layer (internal/store/)

**Purpose:** Database operations and data persistence

**Key Components:**
- `Store` struct: Main database interface
- `models.go`: GORM models (Project, Crawl, DiscoveredUrl, PageLink, Config)
- `projects.go`: Project CRUD operations
- `crawls.go`: Crawl CRUD operations
- `config.go`: Configuration management
- `links.go`: Link management

**Database Location:** `~/.bluesnake/bluesnake.db`

**Schema:**

```go
// Config - Per-domain crawl configuration
type Config struct {
    ID                 uint
    Domain             string  // unique
    JSRenderingEnabled bool    // default: false
    Parallelism        int     // default: 5
    UserAgent          string  // default: "bluesnake/1.0"
    IncludeSubdomains  bool    // default: true
    SinglePageMode     bool    // default: false
    DiscoveryMechanisms string // JSON array: ["spider"], ["sitemap"], or both
    SitemapURLs        string  // JSON array of sitemap URLs
    CheckExternalResources bool // default: true
    CreatedAt          int64
    UpdatedAt          int64
}

// Project - Represents a website/domain to crawl
type Project struct {
    ID        uint
    URL       string  // Normalized URL
    Domain    string  // Domain identifier (unique)
    Crawls    []Crawl // One-to-many relationship
    CreatedAt int64
    UpdatedAt int64
}

// Crawl - Individual crawl session
type Crawl struct {
    ID            uint
    ProjectID     uint
    CrawlDateTime int64         // Unix timestamp
    CrawlDuration int64         // Milliseconds
    PagesCrawled  int
    DiscoveredUrls []DiscoveredUrl  // One-to-many relationship
    CreatedAt     int64
    UpdatedAt     int64
}

// DiscoveredUrl - Individual URL discovered during crawl
type DiscoveredUrl struct {
    ID              uint
    CrawlID         uint
    URL             string
    Visited         bool   // true if crawled, false if only discovered
    Status          int    // HTTP status code
    Title           string
    MetaDescription string
    ContentHash     string
    Indexable       string  // "Yes", "No", or "-"
    ContentType     string
    Error           string  // Error message if failed
    CreatedAt       int64
}

// PageLink - Link relationship between pages
type PageLink struct {
    ID          uint
    CrawlID     uint
    SourceURL   string  // Page containing the link
    TargetURL   string  // Link destination
    Type        string  // "anchor", "image", "script", etc.
    Text        string  // Link text or alt text
    Context     string  // Surrounding context
    IsInternal  bool
    Status      int
    Title       string
    ContentType string
    Position    string  // "content", "navigation", etc.
    DOMPath     string  // Simplified DOM path
    URLAction   string  // "crawl", "record", "skip"
}
```

**Database Operations (all in internal/store/):**
- `GetOrCreateProject()` - Find existing or create new project by domain
- `CreateCrawl()` - Create new crawl record
- `SaveDiscoveredUrl()` - Save individual URL result (visited or unvisited)
- `UpdateCrawlStats()` - Update crawl statistics after completion
- `GetAllProjects()` - Retrieve all projects with latest crawl info
- `GetCrawlResults()` - Get all URLs for a specific crawl
- `GetOrCreateConfig()` - Get or create config for domain
- `UpdateConfig()` - Update domain configuration
- `SavePageLinks()` - Save page link relationships
- `GetPageLinks()` - Get inbound/outbound links for a page
- CASCADE deletion for related records

---

## Part 3: Desktop Application (cmd/desktop/)

### Architecture Overview

The desktop application is a thin wrapper around the internal packages, using dependency injection and the adapter pattern.

### Technology Stack

- **Backend:** Go with Wails v2 (adapter layer only)
- **Frontend:** React + TypeScript + Vite
- **Database:** SQLite with GORM ORM (via internal/store)
- **Communication:** Wails runtime bindings + events

### Backend Components

#### 1. Main Entry Point (`main.go`)

```go
func main() {
    // Initialize store
    st, err := store.NewStore()

    var desktopApp *DesktopApp

    err = wails.Run(&options.App{
        Title:    "BlueSnake - Web Crawler",
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

- Initializes the Wails application
- Creates and injects dependencies (store, emitter)
- Binds `DesktopApp` adapter methods for frontend access

#### 2. Wails Adapter (`adapter.go`)

**WailsEmitter:** Implements EventEmitter interface for Wails

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
// ... all other methods follow same pattern
```

- ~20 lines per method (just delegation)
- No business logic in adapter
- Clean separation of concerns

### Frontend Components

#### 1. App Component (`App.tsx`)

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

// Key state variables
const [currentProject, setCurrentProject] = useState<ProjectInfo | null>(null)
const [currentCrawlId, setCurrentCrawlId] = useState<number | null>(null)
const [isCrawling, setIsCrawling] = useState(false)
const [activeCrawls, setActiveCrawls] = useState<Map<number, CrawlProgress>>(new Map())
```

**Crawl ID Tracking:**
The `currentCrawlId` state is critical for data synchronization. It tracks which specific crawl is currently being viewed, allowing the frontend to:
- Filter incoming events to only process those for the current crawl
- Prevent mixing data from different crawls of the same project
- Properly handle navigation between active and historical crawls

**Initial Discovery:**
On app startup, the frontend calls `GetActiveCrawls()` once to discover any running crawls (e.g., if the app restarted during a crawl). This populates the `activeCrawls` map immediately.

**Event Listeners (Indicational Only):**
```typescript
// Events are indicational only - they trigger data refresh via polling,
// but don't carry any payload.

EventsOn("crawl:started", () => {
  // Just trigger a refresh - polling will handle the updates
  loadProjects()
})

EventsOn("crawl:completed", () => {
  // Just trigger a refresh - polling will handle the updates
  loadProjects()
})

EventsOn("crawl:stopped", () => {
  // Just trigger a refresh - polling will handle the updates
  loadProjects()
})
```

**Polling Architecture:**

The frontend uses polling to fetch data at regular intervals:

**Home Page Polling:**
```typescript
// Poll every 500ms when there are active crawls
useEffect(() => {
  if (view !== 'start') return

  const pollHomeData = async () => {
    const projectList = await GetProjects()
    const crawls = await GetActiveCrawls()
    // Update state
  }

  pollHomeData() // Initial load
  if (activeCrawls.size > 0) {
    const interval = setInterval(pollHomeData, 500)
    return () => clearInterval(interval)
  }
}, [view, activeCrawls.size])
```

**Crawl Page Polling:**
```typescript
// Poll every 2s when crawling, 500ms when stopping
useEffect(() => {
  if (view !== 'crawl' || !currentProject) return

  const pollCrawlData = async () => {
    const crawls = await GetActiveCrawls()
    const activeCrawl = crawls.find(c => c.projectId === currentProject.id)

    if (activeCrawl) {
      // Get data from database via GetActiveCrawlData
      const crawlData = await GetActiveCrawlData(currentProject.id)
      setResults(crawlData.results)
    } else {
      // Get completed crawl from database
      const crawlData = await GetCrawlWithResults(currentCrawlId)
      setResults(crawlData.results)
    }
  }

  pollCrawlData() // Initial load
  const isStoppingProject = stoppingProjects.has(currentProject.id)
  if (isCrawling || isStoppingProject) {
    const pollInterval = isStoppingProject ? 500 : 2000
    const interval = setInterval(pollCrawlData, pollInterval)
    return () => clearInterval(interval)
  }
}, [view, currentProject, isCrawling, stoppingProjects])
```

This polling approach:
- Reduces complexity compared to event-driven updates
- More reliable data synchronization from database
- Easier to optimize in the future (batching, pagination)
- Less network traffic (no event for each URL)
- Database is the single source of truth

**Features:**
- Real-time crawl progress display via polling
- Project history with cards
- Crawl comparison (dropdown to select past crawls)
- Delete crawls and projects with confirmation modals
- Active crawl detection when navigating to projects

**Active Crawl Detection:**
When clicking on a project card, the app intelligently determines which crawl to display:

```typescript
const handleProjectClick = async (project: ProjectInfo) => {
  // Check if there's an active crawl for this project
  const activeCrawl = activeCrawls.get(project.id)
  const crawlIdToLoad = activeCrawl ? activeCrawl.crawlId : project.latestCrawlId

  // Set the current crawl ID for tracking
  setCurrentCrawlId(crawlIdToLoad)
  setIsCrawling(!!activeCrawl)

  // Load crawl data - polling will keep it updated if active
  const crawlData = await GetCrawlWithResults(crawlIdToLoad)
  // ...
}
```

This ensures:
- Navigating to a project with an active crawl shows the live crawl data
- Navigating to a completed project shows the latest completed crawl
- Polling continues to update the view for active crawls
- No mixing of data between different crawls

#### 2. Config Component (`Config.tsx`)

Per-domain configuration interface:

**Settings:**
- **JavaScript Rendering:** Enable/disable chromedp rendering
- **Parallelism:** Number of concurrent requests (1-100)
- **Include Subdomains:** Whether to crawl subdomains
- **Single Page Mode:** Only crawl the starting URL
- **Discovery Mechanisms:** Spider, Sitemap, or both
- **Custom Sitemap URLs:** Manual sitemap locations
- **Check External Resources:** Validate external resources for broken links

**Backend Calls:**
```typescript
GetConfigForDomain(url)
UpdateConfigForDomain(url, jsRendering, parallelism, includeSubdomains, singlePageMode, mechanisms, sitemapURLs, checkExternalResources)
```

### Communication Architecture

#### Frontend → Backend (Method Calls)

Wails generates TypeScript bindings in `frontend/wailsjs/go/main/DesktopApp.js`:

```typescript
import { StartCrawl, GetProjects } from "../wailsjs/go/main/DesktopApp"

// Direct method invocation
await StartCrawl("https://example.com")
const projects = await GetProjects()
```

**How it works:**
1. TypeScript calls generated binding function
2. Wails runtime marshals call to Go
3. Go method executes
4. Return value marshaled back to TypeScript
5. Promise resolves with result

#### Backend → Frontend (Events)

Backend emits indicational events using Wails runtime (no payload):

```go
runtime.EventsEmit(ctx, "crawl:started")    // Indicational only
runtime.EventsEmit(ctx, "crawl:completed")  // Indicational only
runtime.EventsEmit(ctx, "crawl:stopped")    // Indicational only
```

Frontend listens with:

```typescript
EventsOn("crawl:started", () => {
  // Just trigger a refresh - polling will handle the data updates
  loadProjects()
})
```

**Event Flow:**
1. Go code calls `runtime.EventsEmit()` with event name only (no data payload)
2. Wails runtime pushes indicational event to frontend
3. All registered listeners invoked
4. Listeners trigger data refresh via polling (GetActiveCrawls, GetActiveCrawlData, etc.)
5. React state updated from polling results, UI re-renders

---

## Part 4: Storage Architecture

### Two Separate Storage Systems

#### 1. Crawler Storage (In-Memory)

**Location:** `storage.InMemoryStorage` in crawler package

**Purpose:** Track visited URLs during active crawl to prevent duplicates

**Lifecycle:**
- Created when `Collector.Init()` called
- Populated during crawl execution
- Destroyed when crawler completes
- **Non-persistent** - no disk storage

**Data Stored:**
- Hash of visited URLs (uint64) - via `VisitIfNotVisited()` (atomic)
- HTTP cookies for domain
- Content hashes for duplicate detection

**Relationship to Desktop App:**
- Used internally by crawler during `app.runCrawler()`
- Desktop app doesn't directly interact with this storage
- Each crawl gets fresh storage instance

#### 2. Wails App Storage (SQLite)

**Location:** `~/.bluesnake/bluesnake.db`

**Purpose:** Persist crawl history, results, and configuration

**Lifecycle:**
- Initialized on app startup
- Persists across app restarts
- Stores all historical data

**Data Stored:**
- Projects (domains)
- Crawls (sessions)
- Discovered URLs (visited and unvisited)
- Page Links (with URLAction: crawl/record/skip)
- Configurations (per-domain settings)

**Relationship to Crawler:**
- **Independent** - not used by crawler package
- Receives crawler results via callbacks
- Stores results in database for historical viewing

### Storage Relationship Diagram

```
┌─────────────────────────────────────────────┐
│          Desktop App (Wails)                │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │   SQLite Database (~/.bluesnake/)   │   │
│  │                                     │   │
│  │   • Projects (persistent)          │   │
│  │   • Crawls (persistent)            │   │
│  │   • DiscoveredUrls (persistent)    │   │
│  │   • PageLinks (persistent)         │   │
│  │   • Config (persistent)            │   │
│  └─────────────────────────────────────┘   │
│                  ▲                          │
│                  │ Saves results            │
│                  │                          │
│  ┌───────────────┴─────────────────────┐   │
│  │   Crawler Integration (app.go)      │   │
│  │                                     │   │
│  │   Creates Crawler instance          │   │
│  │   Sets OnPageCrawled callback       │   │
│  │   Sets OnResourceVisit callback     │   │
│  │   Sets OnCrawlComplete callback     │   │
│  │   Emits events to frontend          │   │
│  └───────────────┬─────────────────────┘   │
│                  │ Uses                     │
└──────────────────┼──────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────┐
│     BlueSnake Crawler Package               │
│                                             │
│  ┌─────────────────────────────────────┐   │
│  │  High-Level Crawler                 │   │
│  │                                     │   │
│  │  • Channel-based URL processing     │   │
│  │  • Single-threaded processor        │   │
│  │  • Worker pool                      │   │
│  │  • Aggregates page results          │   │
│  │  • Tracks discovered URLs           │   │
│  │  • Calls OnPageCrawled for each pg  │   │
│  │  • Calls OnResourceVisit for assets │   │
│  │  • Calls OnCrawlComplete when done  │   │
│  └─────────────────────────────────────┘   │
│                  │                          │
│                  ▼                          │
│  ┌─────────────────────────────────────┐   │
│  │  Low-Level Collector                │   │
│  │                                     │   │
│  │  • HTTP requests/responses          │   │
│  │  • HTML parsing                     │   │
│  │  • Link extraction                  │   │
│  │  • InMemoryStorage (temporary)      │   │
│  │    - VisitIfNotVisited (atomic)     │   │
│  └─────────────────────────────────────┘   │
│                                             │
│  • No persistence                           │
│  • No knowledge of SQLite database          │
│  • Fresh instance per crawl                 │
└─────────────────────────────────────────────┘
```

**Key Points:**
- Storage systems are **completely separate**
- Crawler storage is **ephemeral** (exists only during crawl)
- Wails storage is **persistent** (saved to disk)
- Data flows: Crawler → Callbacks → Database
- No direct connection between the two storage systems

---

## Crawl Execution Flow

### Complete Flow Diagram

```
Frontend (React)                Backend (Go)                    Crawler                  Database
     │                               │                            │                         │
     │ StartCrawl(url)              │                            │                         │
     ├──────────────────────────────▶│                            │                         │
     │                               │ normalizeURL()             │                         │
     │                               │ GetOrCreateProject() ──────┼────────────────────────▶│
     │                               │                            │                         │
     │                               │ CreateCrawl() ─────────────┼────────────────────────▶│
     │                               │ (returns crawlId)          │                         │
     │                               │                            │                         │
     │                               │ GetOrCreateConfig() ───────┼────────────────────────▶│
     │                               │                            │                         │
     │                               │ go runCrawler()            │                         │
     │                               │    │                       │                         │
     │                               │    └──▶NewCrawler()───────▶│                         │
     │                               │                            │ NewCollector()          │
     │                               │                            │ Init()                  │
     │                               │                            │ InMemoryStorage.Init()  │
     │                               │                            │ Start worker pool       │
     │                               │                            │ Start URL processor     │
     │                               │                            │                         │
     │◀─ "crawl:started" ────────────│ (no payload)               │                         │
     │  loadProjects()               │                            │                         │
     │                               │                            │                         │
     │  [Polling Loop @ 2s]          │                            │                         │
     │ GetActiveCrawlData() ─────────▶│                            │                         │
     │◀──────────────────────────────│◀──GetCrawlResults()───────▶│────────────────────────▶│
     │  (crawl results from DB)      │                            │                         │
     │                               │                            │                         │
     │                               │    [URL Processor]         │                         │
     │                               │    processDiscoveredURL()  │                         │
     │                               │    VisitIfNotVisited() ────▶│ (ATOMIC, race-free)    │
     │                               │    Submit to worker pool   │                         │
     │                               │                            │                         │
     │                               │    [Worker]                │                         │
     │                               │    OnResponse()            │                         │
     │                               │    OnHTML("title")         │                         │
     │                               │    OnScraped()             │                         │
     │                               │                            │                         │
     │                               ├───────SaveDiscoveredUrl()─▶│────────────────────────▶│
     │                               ├───────SavePageLinks()─────▶│────────────────────────▶│
     │                               │                            │                         │
     │                               │    OnHTML("a[href]")       │                         │
     │                               │       queueURL()───────────▶│ (non-blocking send)    │
     │                               │                            │                         │
     │                               │    OnError()               │                         │
     │                               │                            │                         │
     │                               ├───────SaveDiscoveredUrl()─▶│────────────────────────▶│
     │                               │                            │                         │
     │                               │    Wait()                  │                         │
     │                               │                            │ (blocks until done)     │
     │                               │                            │                         │
     │                               ├───UpdateCrawlStats()──────▶│────────────────────────▶│
     │◀─ "crawl:completed" ──────────│ (no payload)               │                         │
     │  loadProjects()               │                            │                         │
     │  [Stops polling]              │                            │                         │
     │                               │                            │                         │
```

### Detailed Steps

1. **Initialization:**
   - User enters URL in frontend
   - `StartCrawl()` called via Wails binding
   - URL normalized and domain extracted
   - Project record created/retrieved from database
   - Crawl record created with initial timestamp

2. **Configuration:**
   - Domain-specific config retrieved (or defaults used)
   - `Crawler` instantiated with options:
     - URLFilters (for domain matching based on IncludeSubdomains)
     - EnableRendering (if configured)
     - DiscoveryMechanisms (spider, sitemap, or both)
     - Parallelism via worker pool size
     - DiscoveryChannelSize, WorkQueueSize

3. **Crawler Setup (High-Level API):**
   - Desktop app sets callbacks:
     - `SetOnURLDiscovered`: Filter analytics/tracking URLs
     - `SetOnPageCrawled`: Called after each page is crawled with complete result
     - `SetOnResourceVisit`: Called after each resource is visited
     - `SetOnCrawlComplete`: Called when crawl finishes
   - Bluesnake crawler internally sets up low-level callbacks:
     - `OnResponse`: Detects content type, checks indexability
     - `OnHTML("html")`: Extracts title, meta description, links from HTML pages
     - `OnScraped`: Builds PageResult and calls OnPageCrawled callback
     - `OnError`: Captures errors for failed URLs

4. **Crawling (Channel-Based):**
   - `crawler.Start(url)` initiates crawl
   - URL processor goroutine starts
   - Worker pool goroutines start
   - Initial URL queued to discovery channel
   - Bluesnake crawler internally:
     - URL Processor: Receives URL from discovery channel (SINGLE-THREADED)
     - URL Processor: Calls OnURLDiscovered to get action (crawl/record/skip)
     - URL Processor: Checks if crawlable (filters, robots.txt)
     - URL Processor: Calls `VisitIfNotVisited()` (ATOMIC, race-free)
     - URL Processor: Submits to worker pool (BLOCKS if full for backpressure)
     - Worker: Receives work from queue
     - Worker: Makes HTTP request
     - Worker: Parses HTML (with chromedp if enabled)
     - Worker: Extracts title, links, and metadata
     - Worker: Aggregates all data into PageResult
     - Worker: Calls desktop app's `OnPageCrawled` callback
     - Worker: Discovers new links, queues them to discovery channel (non-blocking)
   - Desktop app `OnPageCrawled` callback:
     - Saves complete PageResult to database (DiscoveredUrl with visited=true)
     - Saves page links to database (PageLink records)
     - Saves unvisited URLs (URLAction="record") to database (visited=false)
     - Updates in-memory tracking for UI

5. **Completion:**
   - `crawler.Wait()` blocks until all work complete:
     - Collector.Wait() waits for all HTTP requests to finish
     - Discovery channel closed
     - URL processor drains remaining URLs
     - Worker pool closes
   - Bluesnake calls desktop app's `OnCrawlComplete` callback with:
     - `wasStopped`: Whether crawl was cancelled
     - `totalPages`: Number of pages successfully crawled
     - `totalDiscovered`: Total unique URLs discovered
   - Desktop app `OnCrawlComplete` callback:
     - Calculates crawl duration
     - Updates crawl statistics in database
     - Emits "crawl:completed" event (indicational only, no payload)
   - UI updates to show final state via polling

---

## HTTP Server (cmd/server/)

### Initialization

```go
func main() {
    // Initialize store
    st, err := store.NewStore()

    // Create app with NoOpEmitter (no events for HTTP)
    coreApp := app.NewApp(st, &app.NoOpEmitter{})
    coreApp.Startup(context.Background())

    // Create HTTP server
    server := NewServer(coreApp)

    // Start server
    httpServer := &http.Server{
        Addr:    ":8080",
        Handler: server,
    }
    httpServer.ListenAndServe()
}
```

### REST API Endpoints

```
GET  /api/v1/health                        - Health check
GET  /api/v1/version                       - App version
GET  /api/v1/projects                      - List all projects
GET  /api/v1/projects/{id}/crawls          - Get project crawls
DELETE /api/v1/projects/{id}               - Delete project

GET  /api/v1/crawls/{id}                   - Get crawl with results
DELETE /api/v1/crawls/{id}                 - Delete crawl
GET  /api/v1/crawls/{id}/pages/{url}/links - Get page links

POST /api/v1/crawl                         - Start new crawl
POST /api/v1/stop-crawl/{projectID}        - Stop active crawl
GET  /api/v1/active-crawls                 - List active crawls

GET  /api/v1/config?url=example.com        - Get config
PUT  /api/v1/config                        - Update config
```

### Running the Server

```bash
# Default (port 8080)
go run ./cmd/server

# Custom port
go run ./cmd/server -port 3000 -host localhost

# Build binary
go build -o bluesnake-server ./cmd/server
./bluesnake-server
```

---

## Key Architectural Patterns

### 1. Separation of Concerns

- **Crawler Package:** Pure crawling logic with channel-based architecture, no UI dependencies
- **Desktop App:** UI and persistence layer
- **Database Layer:** Abstracted via GORM models

### 2. Polling-Based Architecture with Active Crawl Tracking

- Crawler callbacks save data to database
- Frontend polls database for updates
- Events used only for immediate refresh triggers
- Loose coupling between components

**Critical Design Pattern: Polling with Active Crawl Map**

The frontend maintains an `activeCrawls` map (updated via polling `GetActiveCrawls()`) to:
- Detect when navigating to a project with an active crawl
- Automatically load the active crawl instead of the latest completed one
- Show accurate "crawling" status in project cards with real-time progress
- Determine polling interval (2s for active crawls, 500ms when stopping)
- Initialize on app startup by calling `GetActiveCrawls()` once to discover any running crawls

**Why Polling Instead of Events:**
- Database is the single source of truth
- Prevents data mixing and synchronization issues
- More reliable at scale (millions of URLs)
- Easier to optimize (batching, pagination, caching)
- Simpler code with less complexity
- Events only used for immediate UI refresh triggers (no payload data)

### 3. Channel-Based Concurrency Pattern (Crawler)

- Single-threaded URL processor eliminates race conditions
- Non-blocking discovery channel prevents deadlocks
- Fixed worker pool controls concurrency
- Backpressure via blocking worker submission
- Clean shutdown via context cancellation

### 4. Domain-Driven Design

- Projects organized by domain
- Per-domain configuration
- Crawl history per project

### 5. Asynchronous Execution

- Crawler runs in goroutine
- Non-blocking UI
- Frontend polls for results at regular intervals (2s for active crawls, 500ms when stopping)
- Events trigger immediate refresh, but polling provides the actual data

---

## Responsibility Division: What Goes Where?

### BlueSnake Package Responsibilities (Core Crawling Library)

**✅ What BELONGS in bluesnake package:**
- Channel-based URL processing (eliminates race conditions)
- Single-threaded URL processor
- Fixed-size worker pool
- Low-level HTTP request/response handling
- HTML/XML parsing and element extraction
- URL deduplication (via Crawler with VisitIfNotVisited atomic operation)
- Domain filtering and robots.txt checking
- Rate limiting and parallelism control
- JavaScript rendering with chromedp
- Request/response lifecycle callbacks (low-level)
- High-level crawler API with page-level callbacks
- Link discovery and crawl queue management
- Content-type detection and handling
- Error handling for network/parsing issues
- In-memory URL deduplication during active crawls (non-persistent)
- Cookie and session management

**❌ What DOES NOT belong in bluesnake package:**
- Database operations (SQLite, PostgreSQL, etc.)
- UI/frontend code
- Persistent storage of crawl results
- User authentication or authorization
- Application-specific business logic
- Configuration persistence (save/load from files/DB)
- Favicon fetching or asset management
- Project/domain organization
- Historical crawl comparison
- Analytics or reporting logic

### Internal Packages Responsibilities (internal/)

**✅ What BELONGS in internal/store/ (Database Layer):**
- GORM models and schema definitions
- SQLite operations (save, query, delete)
- Database initialization and migrations
- CRUD operations for projects, crawls, configs, links
- Favicon file management
- Repository pattern implementation

**✅ What BELONGS in internal/app/ (Business Logic Layer):**
- Crawl orchestration and management
- Project and domain organization logic
- Configuration management (get, update)
- Active crawl tracking and state management
- URL normalization and validation
- Callbacks that save crawler results to database
- Crawl statistics and aggregation
- Update checking logic
- **NO transport-specific code** (no Wails, HTTP, or MCP dependencies)
- **NO UI logic** (just returns data)

**✅ What BELONGS in internal/types/ (Shared Types):**
- Request/response types (ProjectInfo, CrawlInfo, etc.)
- Shared data structures used across layers
- No business logic, just data definitions

**❌ What DOES NOT belong in internal packages:**
- Low-level HTTP handling (that's in crawler package)
- HTML parsing logic (that's in crawler package)
- Robots.txt parsing (that's in crawler package)
- Chromedp rendering code (that's in crawler package)
- UI components (that's in frontend)
- Event emission specifics (only interface defined)
- HTTP routing/handlers (that's in cmd/server)
- Wails bindings (that's in cmd/desktop)

### Desktop Application Responsibilities (cmd/desktop/)

**✅ What BELONGS in desktop app:**
- Wails initialization and configuration
- WailsEmitter implementation (runtime.EventsEmit)
- DesktopApp adapter (thin wrapper)
- UI integration and event handling
- UI state management (React)
- Frontend polling for real-time updates
- User interaction handling

**❌ What DOES NOT belong in desktop app:**
- Business logic (moved to internal/app)
- Database operations (moved to internal/store)
- Crawl orchestration (moved to internal/app)
- URL normalization (moved to internal/app)
- Any logic that could be shared with HTTP server

### HTTP Server Responsibilities (cmd/server/)

**✅ What BELONGS in HTTP server:**
- HTTP server initialization and configuration
- Route definitions and handlers
- Request/response marshaling (JSON)
- CORS middleware
- Error handling and HTTP status codes
- Graceful shutdown logic

**❌ What DOES NOT belong in HTTP server:**
- Business logic (use internal/app)
- Database operations (use internal/store)
- Crawl orchestration (use internal/app)
- Any logic that could be shared with desktop app

---

## Extension Points

### 1. Custom Storage Backend

Implement `storage.Storage` interface:

```go
type CustomStorage struct {}

func (s *CustomStorage) Init() error { ... }
func (s *CustomStorage) Visited(id uint64) error { ... }
func (s *CustomStorage) IsVisited(id uint64) (bool, error) { ... }
func (s *CustomStorage) VisitIfNotVisited(id uint64) (bool, error) { ... }
// ...

c.SetStorage(&CustomStorage{})
```

### 2. Custom Callbacks

Add specialized crawling behavior:

```go
// High-level callback (user API)
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    // Custom page processing
    // Access structured data: result.Title, result.Links, etc.
})

// Low-level callback (advanced use - not recommended for most users)
crawler.Collector.OnHTML("meta[name='description']", func(e *HTMLElement) {
    description := e.Attr("content")
    // Save SEO metadata
})

crawler.Collector.OnResponse(func(r *Response) {
    // Custom header analysis
    // Performance tracking
    // Content-type filtering
})
```

### 3. Additional Backend APIs

Expose new methods from `App` struct:

```go
func (a *App) ExportCrawlToCSV(crawlID uint) (string, error) {
    // Export logic
}
```

Automatically available in frontend via Wails bindings.

### 4. Database Schema Extensions

Add new models or fields:

```go
type PerformanceMetric struct {
    ID               uint
    CrawlID          uint
    URL              string
    ResponseTime     int64
    FirstByteTime    int64
}

db.AutoMigrate(&PerformanceMetric{})
```

---

## Performance Considerations

### 1. Concurrency

- **Crawler:** Channel-based architecture with fixed worker pool
- **Worker Pool:** Configurable parallelism (default: 10 concurrent fetches)
- **URL Processor:** Single-threaded (eliminates race conditions, serializes visit decisions)
- **Goroutines:** Bounded (1 processor + N workers, not unbounded)

### 2. Memory Management

- **Discovery Channel:** Configurable buffer size (default: 50k URLs)
- **Work Queue:** Configurable buffer size (default: 1k work items)
- **Streaming:** Large responses can be streamed
- **MaxBodySize:** Limit response body size
- **Cleanup:** InMemoryStorage cleared after crawl

### 3. Database Optimization

- **Indexes:** Domain and CrawlID indexed
- **Batch Inserts:** Could be optimized for large crawls
- **Cascade Deletes:** Efficient cleanup of related records

### 4. Caching

- **Crawler:** Optional file-based caching of GET requests
- **Frontend:** Could add React Query for API caching

---

## Security Considerations

### 1. URL Validation

- Normalization prevents ambiguous URLs
- Domain whitelisting enforces boundaries
- Protocol validation (https preferred)

### 2. Rate Limiting

- Prevents overwhelming target servers
- Configurable delays between requests
- Per-domain limits via worker pool

### 3. robots.txt Compliance

- Optional (can be enabled)
- Respects crawl-delay and disallow rules
- Three modes: "respect", "ignore", "ignore-report"

### 4. Database Access

- Local SQLite file (~/.bluesnake/)
- No network exposure
- File permissions managed by OS

### 5. User Agent

- Identifies crawler in requests
- Allows server operators to block if needed

---

## Adding New Transports

The layered architecture makes it easy to add new transport layers (CLI, gRPC, WebSocket, etc.).

### Example: Adding a CLI

1. **Create cmd/cli/main.go:**
```go
func main() {
    // Initialize store
    st, err := store.NewStore()

    // Create app with NoOpEmitter
    coreApp := app.NewApp(st, &app.NoOpEmitter{})
    coreApp.Startup(context.Background())

    // Parse CLI flags
    url := flag.String("url", "", "URL to crawl")
    flag.Parse()

    // Start crawl
    if err := coreApp.StartCrawl(*url); err != nil {
        log.Fatal(err)
    }

    // Wait for completion (poll active crawls)
    for {
        crawls := coreApp.GetActiveCrawls()
        if len(crawls) == 0 {
            break
        }
        time.Sleep(1 * time.Second)
    }
}
```

2. **Build and run:**
```bash
go build -o bluesnake-cli ./cmd/cli
./bluesnake-cli -url https://example.com
```

No changes to internal packages required!

### Example: Adding WebSocket Support to HTTP Server

1. **Create WebSocket emitter in cmd/server:**
```go
type WebSocketEmitter struct {
    subscribers map[string]chan app.Event
    mu          sync.RWMutex
}

func (w *WebSocketEmitter) Emit(eventType app.EventType, data interface{}) {
    w.mu.RLock()
    defer w.mu.RUnlock()

    event := app.Event{Type: eventType, Data: data}
    for _, ch := range w.subscribers {
        select {
        case ch <- event:
        default: // Non-blocking
        }
    }
}
```

2. **Use in server initialization:**
```go
// Create app with WebSocket emitter
wsEmitter := NewWebSocketEmitter()
coreApp := app.NewApp(st, wsEmitter)

// Add WebSocket handler
http.HandleFunc("/ws", wsEmitter.handleWebSocket)
```

Again, no changes to internal packages!

---

## Future Enhancement Opportunities

### Crawler Package
- Redis/PostgreSQL storage backend
- Distributed crawling support
- Advanced JavaScript interaction (form filling, clicking)
- Screenshot capture
- Content extraction templates

### Internal Packages
- Batch operations for performance
- Caching layer (Redis)
- Crawl pause/resume state management
- Advanced filtering and search
- Export to CSV/JSON (refactor from desktop)

### Desktop Application
- Crawl scheduling UI
- Crawl comparison/diff view
- Charts and analytics
- Cloud sync of crawl data
- Browser extension integration

### HTTP Server
- WebSocket support for real-time updates
- API authentication (JWT, OAuth)
- Rate limiting per client
- GraphQL endpoint
- Swagger/OpenAPI documentation
- Pagination for large result sets

### New Transports
- **CLI Tool** - Command-line interface for scripting
- **MCP Server** - Model Context Protocol for AI agents
- **gRPC Server** - For high-performance internal APIs
- **Browser Extension** - Direct integration with browsers

### Performance
- Connection pooling
- Streaming database writes
- Incremental crawls (only new/changed pages)
- Crawl pause/resume
- Database sharding for large datasets

---

## Conclusion

BlueSnake demonstrates a clean layered architecture with separation between:

1. **Crawler Package** (root/) - Channel-based crawling with race condition elimination
2. **Store Layer** (internal/store/) - Data persistence and CRUD operations
3. **Business Logic** (internal/app/) - Transport-agnostic orchestration
4. **Transport Layers** (cmd/*/) - Thin adapters for different interfaces:
   - Desktop (Wails) - Cross-platform GUI
   - HTTP Server - REST API
   - Future: CLI, MCP, gRPC, etc.

### Key Architectural Benefits

1. **Code Reuse**: Same business logic powers all transports
2. **Testability**: Each layer can be tested independently
3. **Extensibility**: Add new transports without modifying core logic
4. **Maintainability**: Clear boundaries and single responsibility
5. **Scalability**: Easy to optimize each layer independently
6. **Reliability**: Channel-based architecture eliminates race conditions
7. **Determinism**: Every crawl produces consistent, complete results

### Design Patterns Used

- **Repository Pattern**: Store layer abstracts database operations
- **Dependency Injection**: Store and EventEmitter injected into App
- **Adapter Pattern**: Transport layers adapt core App to different interfaces
- **Observer Pattern**: EventEmitter allows decoupled event handling
- **Interface-Based Design**: Enables polymorphism and testing
- **Channel-Based Concurrency**: Single-threaded processor with worker pool
- **Pipeline Pattern**: URL discovery → processor → worker pool

The channel-based architecture eliminates race conditions by serializing all visit decisions in a single goroutine, while the polling-based design with database as single source of truth enables reliable UI updates at scale. The callback pattern provides extensibility without modifying core code. Indicational events provide immediate feedback triggers, while polling handles the actual data synchronization.

The architecture is designed to be **future-proof**: adding new transports (MCP, CLI, gRPC) requires only creating a new `cmd/` directory and implementing a thin adapter - no changes to business logic required.


## Architecture Flow: Start to Panic

Initial Setup (Simplified - 1 URL, sitemap with 6 URLs)

Starting state:
- Initial URL: https://agentberlin.ai
- Sitemap URLs: 6 URLs (blog, pricing, newsletter, tools/bot-access, 2 blog posts)
- Configuration: Parallelism=10 workers, Async=true

---
Phase 1: Startup

[App Code] StartCrawl("https://agentberlin.ai")
↓
[crawler.go:445] crawler.Start(url)
├─ [crawler.go:450] go runURLProcessor()  // Goroutine 1: Processor
│   └─ Listening on discoveryChannel
│
├─ [crawler.go:453] queueURL(initial URL)
│   └─ discoveryChannel <- {URL: "agentberlin.ai", Source: "initial"}
│
└─ [crawler.go:460-468] Fetch sitemap, queue 6 URLs
    ├─ queueURL({URL: "agentberlin.ai", Source: "sitemap"})
    ├─ queueURL({URL: "agentberlin.ai/blog", Source: "sitemap"})
    ├─ queueURL({URL: "agentberlin.ai/pricing", Source: "sitemap"})
    ├─ queueURL({URL: "agentberlin.ai/newsletter", Source: "sitemap"})
    ├─ queueURL({URL: "agentberlin.ai/tools/bot-access", Source: "sitemap"})
    └─ queueURL({URL: "agentberlin.ai/blog/...", Source: "sitemap"}) x2

[App Code] crawler.Wait()  // Blocks, waiting for completion

State after startup:
- discoveryChannel: 7 URLs queued (1 initial + 6 sitemap)
- Processor: Reading from channel
- Workers: 10 workers idle, waiting for work
- Collector.wg: 0 (no scrape() calls yet)

---
Phase 2: URL Processing Begins

[Goroutine 1: Processor] runURLProcessor()
↓
Receives: {URL: "agentberlin.ai", Source: "initial"}
↓
[crawler.go:349] processDiscoveredURL(req)
├─ [crawler.go:353] action = getOrDetermineURLAction("agentberlin.ai")
│   └─ Returns: URLActionCrawl
│
├─ [crawler.go:360] isURLCrawlable() → true
│
├─ [crawler.go:368] store.VisitIfNotVisited() → false (not visited)
│   └─ Marks URL as visited in storage
│
└─ [crawler.go:389] workerPool.Submit(func() { ... })
    └─ workQueue <- func() { scrape("agentberlin.ai") }

[Goroutine 2: Worker] worker()
↓
Picks up work from workQueue
↓
Executes: work()
    ├─ [App logs] "[WORKER] Starting to process: https://agentberlin.ai"
    │
    └─ [crawler.go:394] cr.Collector.scrape(url, ...)
        ↓
        [bluesnake.go:881] scrape(url, ...)
        ├─ [bluesnake.go:929] c.wg.Add(1)  // Collector.wg = 1
        │   └─ [Logs] "[SCRAPE] WaitGroup Add(1) for: https://agentberlin.ai"
        │
        └─ [bluesnake.go:938] go c.fetch(...)  // Goroutine 3: Async fetch
            └─ [App logs] "[WORKER] Finished scraping: https://agentberlin.ai"
                (scrape() returns immediately in Async mode)

State after first URL submitted:
- discoveryChannel: 6 URLs remaining
- Processor: Processing next URL
- Worker pool queue: empty (work picked up)
- Collector.wg: 1 (fetch goroutine running)
- Active fetch goroutines: 1 (Goroutine 3)

---
Phase 3: Fetch & Callbacks

[Goroutine 3: Fetch] fetch(url, ...)
├─ [bluesnake.go:945] defer wg.Done()  // Will execute at END
│
├─ [bluesnake.go:1004] response = HTTP GET
│
├─ [bluesnake.go:1046] handleOnResponse(response)
│
├─ [bluesnake.go:1048] handleOnHTML(response)
│   └─ [Logs] "[FETCH-CALLBACKS] Starting OnHTML for: https://agentberlin.ai"
│
└─ [bluesnake.go:1543] handleOnHTML()
    ├─ Parses HTML with goquery
    │
    └─ For each registered OnHTML callback:
        [crawler.go:526-647] OnHTML("html", func(e) { ... })
            ├─ [crawler.go:528] allLinks = extractAllLinks(e)
            │   └─ Finds internal links from page
            │
            ├─ [crawler.go:210-251] Spider mode enabled, iterate links
            │   └─ For each internal anchor link:
            │       [crawler.go:614-620] queueURL({URL: link.URL, Source: "spider"})
            │           ↓
            │           discoveryChannel <- URLDiscoveryRequest
            │           (Successfully queued - channel has space)
            │
            └─ [crawler.go:253-275] Resource validation
                └─ For each resource link:
                    [crawler.go:638-644] queueURL({URL: resource.URL, Source: "resource"})

├─ [Logs] "[FETCH-CALLBACKS] Finished OnHTML"
│
├─ [bluesnake.go:1060] handleOnScraped(response)
│   └─ [Logs] "[FETCH-CALLBACKS] Starting OnScraped"
│   └─ [crawler.go:296-358] OnScraped callback
│       └─ Builds PageResult, calls onPageCrawled
│   └─ [Logs] "[FETCH-CALLBACKS] Finished OnScraped"
│
└─ [bluesnake.go:1064] return
    ↓
    Defer executes: wg.Done()  // Collector.wg = 0
    └─ [Logs] "[FETCH] WaitGroup Done() for: https://agentberlin.ai"

State after first fetch completes:
- discoveryChannel: 6 sitemap URLs + N discovered URLs from page
- Processor: Still processing queued URLs
- Collector.wg: 0 (first fetch done)
- Worker pool queue: May have pending work from processor

---
Phase 4: The Race Begins (CRITICAL!)

Meanwhile, the processor has been working through the 6 sitemap URLs:

[Goroutine 1: Processor] Continues processing...
├─ Processes sitemap URL 1: "agentberlin.ai" (already visited, skipped)
│
├─ Processes sitemap URL 2: "agentberlin.ai/blog"
│   └─ [crawler.go:389] workerPool.Submit(func() { scrape("/blog") })
│       └─ workQueue <- work  // Queued in worker pool
│
├─ Processes sitemap URL 3: "agentberlin.ai/pricing"
│   └─ workerPool.Submit(...)
│
├─ Processes sitemap URL 4: "agentberlin.ai/newsletter"
│   └─ workerPool.Submit(...)
│
├─ ... (continues for all 6 URLs)
│
└─ [Logs] "[PROCESSOR] Successfully submitted to worker pool: ..."

CRITICAL TIMING:

[Main Thread] Wait() has been blocking on:
[crawler.go:487] cr.Collector.Wait()
    └─ Waiting for Collector.wg to reach 0

[Goroutine 3] fetch() completes
└─ wg.Done() called
    └─ Collector.wg = 0

[Main Thread] Collector.Wait() RETURNS!
↓
[crawler.go:488] close(cr.discoveryChannel)  // ❌ CHANNEL CLOSED!
└─ [Logs] "[WAIT] ========== CLOSING DISCOVERY CHANNEL =========="

State at this CRITICAL moment:
- discoveryChannel: CLOSED ❌
- Processor: Draining remaining URLs from closed channel
- Worker pool queue: HAS 6 PENDING WORK ITEMS ⚠️
- Collector.wg: 0
- Workers: About to pick up queued work...

---
Phase 5: The Panic

[Main Thread] Wait() continues...
├─ [crawler.go:492] <-cr.processorDone
│   └─ Processor finishes draining channel
│   └─ [Logs] "[WAIT] Processor finished"
│
└─ [crawler.go:505] cr.workerPool.Close()
    └─ [Logs] "[WAIT] Closing worker pool"
    └─ Closes workQueue channel
    └─ Workers can still process already-queued items

[Goroutine 4: Worker] Picks up queued work for "/newsletter"
↓
[Logs] "[WORKER] Starting to process: https://agentberlin.ai/newsletter"
↓
Executes: work()
    └─ [crawler.go:394] cr.Collector.scrape("/newsletter", ...)
        ↓
        [bluesnake.go:929] c.wg.Add(1)  // Collector.wg = 1 again!
        └─ [Logs] "[SCRAPE] WaitGroup Add(1) for: .../newsletter"
        ↓
        [bluesnake.go:938] go c.fetch(...)  // Goroutine 5: New fetch
        └─ [Logs] "[WORKER] Finished scraping: .../newsletter"

[Goroutine 5: Fetch] fetch("/newsletter", ...)
├─ HTTP GET, parse HTML
│
├─ [bluesnake.go:1048] handleOnHTML()
│   └─ [Logs] "[FETCH-CALLBACKS] Starting OnHTML for: .../newsletter"
│
└─ [crawler.go:614] OnHTML callback: queueURL({URL: "/", Source: "spider"})
    ↓
    [crawler.go:300] queueURL(req)
        └─ [Logs] "[QUEUE-ATTEMPT] URL: https://agentberlin.ai/, Source: spider"
        ↓
        select {
        case cr.discoveryChannel <- req:  // ❌ SEND ON CLOSED CHANNEL!
        }

💥 PANIC: send on closed channel

---
Summary of the Problem

The race condition:

1. Worker pool decouples work submission from execution
- Processor submits func() { scrape() } to worker queue
- Worker picks it up later and calls scrape()
- scrape() adds to Collector.wg
2. Timing issue:
- First URL finishes → wg.Done() → Collector.wg = 0
- Wait() sees wg=0 and closes discovery channel
- Worker pool still has 6 queued work items
- Workers execute queued work → call scrape() → wg.Add(1) AFTER Wait() returned
- OnHTML callbacks try to queue URLs → channel already closed → PANIC

The architectural flaw:
- Collector.Wait() only tracks work that has called scrape()
- It doesn't know about work queued in the worker pool that hasn't called scrape() yet
- This creates a window where Wait() returns too early