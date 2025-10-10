# BlueSnake Architecture

## Overview

BlueSnake is a web crawler application with multiple interfaces, consisting of these main components:
1. **BlueSnake Crawler Package** - A Go-based web scraping library (root directory)
2. **BlueSnake Desktop Application** - A Wails-based GUI application (cmd/desktop directory)

## Project Structure

```
bluesnake/
├── *.go                    # Core crawler package files
├── storage/               # Storage abstraction for crawler
├── debug/                 # Debugging utilities
├── extensions/            # Crawler extensions
├── proxy/                 # Proxy support
├── queue/                 # Request queuing
└── cmd/                   # Application executables
    └── desktop/           # Wails desktop application
        ├── main.go        # Application entry point
        ├── app.go         # Backend API (Go methods exposed to frontend)
        ├── database.go    # SQLite database layer
        └── frontend/      # React/TypeScript UI
            └── src/
                ├── App.tsx     # Main UI component
                └── Config.tsx  # Configuration UI
```

---

## Responsibility Division: What Goes Where?

This section provides clear guidance on where different types of functionality should be implemented.

### BlueSnake Package Responsibilities (Core Crawling Library)

**✅ What BELONGS in bluesnake package:**
- Low-level HTTP request/response handling
- HTML/XML parsing and element extraction
- URL normalization and deduplication
- Domain filtering and robots.txt checking
- Rate limiting and parallelism control
- JavaScript rendering with chromedp
- Request/response lifecycle callbacks
- **High-level crawler API with page-level callbacks** (NEW in v1)
- Link discovery and crawl queue management
- Content-type detection and handling
- Error handling for network/parsing issues
- In-memory URL deduplication during active crawls
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

### Desktop Application Responsibilities (cmd/desktop/)

**✅ What BELONGS in desktop app:**
- Database schema and GORM models
- SQLite operations (save, query, delete)
- Wails UI integration and event handling
- Project and domain organization
- Favicon downloading and caching
- User configuration persistence (JS rendering, parallelism)
- Crawl history and comparison
- UI state management (React)
- Export functionality (CSV, JSON)
- Analytics and reporting
- **Callbacks that save crawler results to database** (NEW in v1)
- **UI polling for real-time updates** (NEW in v1)

**❌ What DOES NOT belong in desktop app:**
- Low-level HTTP handling
- HTML parsing logic
- URL normalization algorithms
- Robots.txt parsing
- Rate limiting implementation
- Link extraction logic
- Chromedp rendering code

### High-Level Crawler API (New in v1)

The new `Crawler` type in the bluesnake package provides a simplified, high-level API that sits on top of the low-level `Collector`:

```go
// In bluesnake package
type Crawler struct {
    Collector *Collector  // Low-level collector (exported for advanced usage)
    // ... internal fields
}

// Callbacks for high-level crawling
type OnPageCrawledFunc func(*PageResult)
type OnCrawlCompleteFunc func(wasStopped bool, totalPages int, totalDiscovered int)
```

**Design Philosophy:**
- **BlueSnake handles:** All crawling logic, URL discovery, and aggregation
- **Desktop app handles:** What to do with the results (save to DB, update UI)

**Example Desktop App Usage:**
```go
config := &bluesnake.CollectorConfig{
    AllowedDomains: []string{domain},
    Async:         true,
}
crawler := bluesnake.NewCrawler(config)

// Desktop app only needs to implement these callbacks
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    // Save to database
    SaveCrawledUrl(crawlID, result.URL, result.Status, result.Title, ...)
})

crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages, totalDiscovered int) {
    // Update database with final stats
    UpdateCrawlStats(crawlID, duration, totalPages)
    // Emit UI event
    runtime.EventsEmit(ctx, "crawl:completed")
})

crawler.Start(url)
crawler.Wait()
```

This design ensures:
- Clean separation of concerns
- Bluesnake can be used in any context (CLI, web server, desktop app)
- Desktop app focuses on UI/DB logic, not crawling mechanics
- Easy to test each layer independently

---

## Part 1: BlueSnake Crawler Package

### Architecture Overview

The crawler package (based on Colly) provides a powerful, callback-driven web scraping framework implemented in Go.

### Core Components

#### 1. Collector (`bluesnake.go`)

The `Collector` is the main entity that manages web crawling operations.

**Key Features:**
- Callback-based event system for request/response handling
- URL filtering and domain restrictions
- Rate limiting and request throttling
- Cookie and session management
- JavaScript rendering support via chromedp
- Caching support for GET requests
- Robots.txt compliance (can be disabled)

**Configuration Options:**

The Collector is configured using the `CollectorConfig` struct:

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
    ID                     uint32  // auto-assigned if 0
    DetectCharset          bool
    CheckHead              bool
    TraceHTTP              bool
    Context                context.Context
    MaxRequests            uint32
    EnableRendering        bool  // JavaScript rendering with chromedp
    CacheExpiration        time.Duration
    Debugger               debug.Debugger
}
```

**Creating a Collector:**
```go
config := &bluesnake.CollectorConfig{
    UserAgent:       "MyBot 1.0",
    AllowedDomains:  []string{"example.com"},
    MaxDepth:        3,
    Async:           true,
    EnableRendering: true,
}
c := bluesnake.NewCollector(config)

// Or use default configuration
c := bluesnake.NewCollector(nil)
```

#### 2. Callback System

The crawler operates through registered callbacks:

```go
// Request lifecycle callbacks
c.OnRequest(func(*Request))           // Before sending request
c.OnRequestHeaders(func(*Request))    // Before request Do
c.OnResponseHeaders(func(*Response))  // After receiving headers
c.OnResponse(func(*Response))         // After receiving full response
c.OnHTML(selector, func(*HTMLElement)) // For matching HTML elements
c.OnXML(xpath, func(*XMLElement))     // For matching XML elements
c.OnError(func(*Response, error))     // On request errors
c.OnScraped(func(*Response))          // After processing complete
```

#### 3. Request/Response Objects

**Request:**
- Represents an HTTP request
- Methods: `Visit()`, `Post()`, `Abort()`, `Retry()`
- Contains context for passing data between callbacks

**Response:**
- Represents an HTTP response
- Properties: `StatusCode`, `Headers`, `Body`
- Methods: `Save()`, `FileName()`

**HTMLElement:**
- Represents HTML DOM elements
- Methods: `Attr()`, `ChildText()`, `ForEach()`, `Unmarshal()`
- Uses goquery for CSS selector-based traversal

**XMLElement:**
- Represents XML nodes
- Methods: `Attr()`, `ChildText()`, `ChildAttr()`
- Uses xpath for XML traversal

#### 4. Storage Package (`storage/storage.go`)

Provides an abstraction for storing crawler state:

```go
type Storage interface {
    Init() error
    Visited(requestID uint64) error
    IsVisited(requestID uint64) (bool, error)
    Cookies(u *url.URL) string
    SetCookies(u *url.URL, cookies string)
}
```

**Default Implementation: `InMemoryStorage`**
- Stores visited URLs in memory (hash-based)
- Manages cookies via `http.CookieJar`
- Non-persistent (data lost when crawler stops)
- Thread-safe with mutex locks

#### 5. Rate Limiting

```go
type LimitRule struct {
    DomainGlob  string
    Parallelism int
    Delay       time.Duration
    RandomDelay time.Duration
}
```

Controls request rate per domain using token bucket algorithm.

#### 6. JavaScript Rendering (`chromedp_backend.go`)

When `EnableRendering` is enabled:
- Uses chromedp to render pages in headless Chrome
- Executes JavaScript before parsing HTML
- Falls back to non-rendered HTML on errors
- Shared renderer instance for efficiency

---

## Part 2: Wails Desktop Application (`cmd/desktop/`)

### Architecture Overview

The desktop application wraps the BlueSnake crawler in a cross-platform GUI using the Wails framework, which binds Go backend methods to a React frontend.

### Technology Stack

- **Backend:** Go with Wails v2
- **Frontend:** React + TypeScript + Vite
- **Database:** SQLite with GORM ORM
- **Communication:** Wails runtime bindings + events

### Backend Components

#### 1. Main Entry Point (`main.go`)

```go
func main() {
    app := NewApp()
    wails.Run(&options.App{
        Title:    "BlueSnake - Web Crawler",
        OnStartup: app.startup,
        Bind: []interface{}{app},  // Exposes App methods to frontend
    })
}
```

- Initializes the Wails application
- Embeds frontend assets
- Binds `App` struct methods for frontend access

#### 2. App Backend API (`app.go`)

The `App` struct provides the bridge between frontend and crawler:

**Exported Methods (callable from frontend):**

```go
// Crawl Management
StartCrawl(urlStr string) error
StopCrawl(projectID uint) error
GetActiveCrawls() []CrawlProgress
GetActiveCrawlData(projectID uint) (*CrawlResultDetailed, error)
GetProjects() ([]ProjectInfo, error)
GetCrawls(projectID uint) ([]CrawlInfo, error)
GetCrawlWithResults(crawlID uint) (*CrawlResultDetailed, error)

// Configuration
GetConfigForDomain(urlStr string) (*Config, error)
UpdateConfigForDomain(urlStr string, jsRendering bool, parallelism int) error

// Deletion
DeleteCrawlByID(crawlID uint) error
DeleteProjectByID(projectID uint) error
```

**Key Implementation Details:**

1. **URL Normalization:**
   - Adds `https://` if no protocol specified
   - Extracts domain for project identification
   - Handles non-standard ports

2. **Crawler Integration (using High-Level API):**
   - Creates new `bluesnake.Crawler` instance for each crawl
   - Configures with domain-specific settings from database
   - Sets up two simple callbacks:
     - `OnPageCrawled`: Saves each crawled page to database
     - `OnCrawlComplete`: Updates final statistics and emits completion event
   - Runs in goroutine to prevent UI blocking
   - Desktop app only handles DB/UI logic - all crawling is in bluesnake package

3. **Event Emission:**
   ```go
   runtime.EventsEmit(ctx, "crawl:started")    // Indicational only, no payload
   runtime.EventsEmit(ctx, "crawl:completed")  // Indicational only, no payload
   runtime.EventsEmit(ctx, "crawl:stopped")    // Indicational only, no payload
   ```

   **Important:** Events are **indicational only** and carry no payload. The frontend uses polling to fetch actual data from the backend via method calls. This design decision was made because:
   - At scale, emitting millions of URL events adds complexity
   - Polling from database is more reliable and predictable
   - Simpler synchronization logic
   - Easier to implement future optimizations (batching, pagination, etc.)

#### 3. Database Layer (`database.go`)

**ORM:** GORM with SQLite driver

**Database Location:** `~/.bluesnake/bluesnake.db`

**Schema:**

```go
// Config - Per-domain crawl configuration
type Config struct {
    ID                 uint
    Domain             string  // unique
    JSRenderingEnabled bool    // default: false
    Parallelism        int     // default: 5
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
    CrawledUrls   []CrawledUrl  // One-to-many relationship
    CreatedAt     int64
    UpdatedAt     int64
}

// CrawledUrl - Individual URL discovered during crawl
type CrawledUrl struct {
    ID        uint
    CrawlID   uint
    URL       string
    Status    int     // HTTP status code
    Title     string
    Indexable string  // "Yes" or "No"
    Error     string  // Error message if failed
    CreatedAt int64
}
```

**Database Operations:**
- `GetOrCreateProject()` - Find existing or create new project by domain
- `CreateCrawl()` - Create new crawl record
- `SaveCrawledUrl()` - Save individual URL result
- `UpdateCrawlStats()` - Update crawl statistics after completion
- `GetAllProjects()` - Retrieve all projects with latest crawl info
- `GetCrawlResults()` - Get all URLs for a specific crawl
- CASCADE deletion for related records

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
// We decided to not rely on events for data update because at the scale we are operating at,
// events add more complexity and we needed to make the system more reliable before getting
// into complication. When we do need to rely on the payload from events, in the future
// (fetching all the crawl url every 500 ms is not good because there are millions of them),
// we'll implement events. For now, events are indicational only - they trigger data refresh
// via polling, but don't carry any payload.

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

**Backend Calls:**
```typescript
GetConfigForDomain(url)
UpdateConfigForDomain(url, jsRendering, parallelism)
```

---

## Communication Architecture

### Frontend → Backend (Method Calls)

Wails generates TypeScript bindings in `frontend/wailsjs/go/main/App.js`:

```typescript
import { StartCrawl, GetProjects } from "../wailsjs/go/main/App"

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

### Backend → Frontend (Events)

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

## Storage Architecture

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
- Hash of visited URLs (uint64)
- HTTP cookies for domain

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
- Crawled URLs (results)
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
│  │   • CrawledUrls (persistent)       │   │
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
│  │  • Aggregates page results          │   │
│  │  • Tracks discovered URLs           │   │
│  │  • Calls OnPageCrawled for each pg  │   │
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
│  │  • URL deduplication                │   │
│  │  • InMemoryStorage (temporary)      │   │
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
     │                               │    └──▶NewCollector()─────▶│                         │
     │                               │                            │ Init()                  │
     │                               │                            │ InMemoryStorage.Init()  │
     │                               │                            │                         │
     │◀─ "crawl:started" ────────────│ (no payload)               │                         │
     │  loadProjects()               │                            │                         │
     │                               │                            │                         │
     │  [Polling Loop @ 2s]          │                            │                         │
     │ GetActiveCrawlData() ─────────▶│                            │                         │
     │◀──────────────────────────────│◀──GetCrawlResults()───────▶│────────────────────────▶│
     │  (crawl results from DB)      │                            │                         │
     │                               │                            │                         │
     │                               │    OnResponse()            │                         │
     │                               │    OnHTML("title")         │                         │
     │                               │                            │                         │
     │                               ├───────SaveCrawledUrl()────▶│────────────────────────▶│
     │                               │                            │                         │
     │                               │    OnHTML("a[href]")       │                         │
     │                               │       Visit(link)──────────▶│                         │
     │                               │                            │ (recursive)             │
     │                               │                            │                         │
     │                               │    OnError()               │                         │
     │                               │                            │                         │
     │                               ├───────SaveCrawledUrl()────▶│────────────────────────▶│
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
   - `Collector` instantiated with options:
     - AllowedDomains (restricts to target domain)
     - EnableJSRendering (if configured)
     - Parallelism via LimitRule

3. **Crawler Setup (High-Level API):**
   - Desktop app sets two callbacks:
     - `SetOnPageCrawled`: Called after each page is crawled with complete result
     - `SetOnCrawlComplete`: Called when crawl finishes
   - Bluesnake crawler internally sets up low-level callbacks:
     - `OnResponse`: Detects content type, checks indexability
     - `OnHTML("html")`: Extracts title from HTML pages
     - `OnHTML("a[href]")`: Discovers and queues links
     - `OnError`: Captures errors for failed URLs

4. **Crawling:**
   - `crawler.Start(url)` initiates crawl
   - Bluesnake crawler internally:
     - Checks if URL visited (InMemoryStorage)
     - Makes HTTP request
     - Parses HTML (with chromedp if enabled)
     - Extracts title, links, and metadata
     - Aggregates all data into PageResult
     - Calls desktop app's `OnPageCrawled` callback
     - Discovers new links, queues them
   - Desktop app `OnPageCrawled` callback:
     - Saves complete PageResult to database
     - Updates in-memory tracking for UI

5. **Completion:**
   - `crawler.Wait()` blocks until all requests complete
   - Bluesnake calls desktop app's `OnCrawlComplete` callback with:
     - `wasStopped`: Whether crawl was cancelled
     - `totalPages`: Number of pages successfully crawled
     - `totalDiscovered`: Total unique URLs discovered
   - Desktop app `OnCrawlComplete` callback:
     - Calculates crawl duration
     - Updates crawl statistics in database
     - Emits "crawl:completed" event
   - UI updates to show final state via polling

---

## Key Architectural Patterns

### 1. Separation of Concerns

- **Crawler Package:** Pure crawling logic, no UI dependencies
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

### 3. Callback Pattern (Crawler)

- Register handlers for lifecycle events
- Composable, extensible crawling behavior
- No need to modify core crawler code

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

## Configuration System

### Two-Level Configuration

#### 1. Crawler Configuration (Per-Crawl)

Set via `CollectorConfig` struct when creating collector:

```go
config := &bluesnake.CollectorConfig{
    AllowedDomains:  []string{domain},
    EnableRendering: true,
    Async:           true,
}
c := bluesnake.NewCollector(config)

c.Limit(&bluesnake.LimitRule{
    DomainGlob:  "*",
    Parallelism: 5,
})
```

**Ephemeral** - only exists during crawl execution

#### 2. Domain Configuration (Persistent)

Stored in SQLite `Config` table:

```go
type Config struct {
    Domain             string
    JSRenderingEnabled bool
    Parallelism        int
}
```

**Flow:**
1. User configures via Config UI
2. Settings saved to database
3. On next crawl, settings retrieved
4. Translated to `CollectorConfig` struct fields
5. Applied to new Collector instance

---

## Error Handling

### Crawler Level

```go
c.OnError(func(r *Response, err error) {
    // Error handling logic
})
```

- HTTP errors (4xx, 5xx)
- Network errors
- Parse errors
- robots.txt blocks

### Desktop App Level

```go
if err := SaveCrawledUrl(..., errorMsg); err != nil {
    runtime.LogErrorf(ctx, "Failed to save: %v", err)
}
```

- Database errors logged
- Events emitted to notify frontend
- Graceful degradation (crawl continues on errors)

### Frontend Level

```typescript
try {
    await StartCrawl(url)
} catch (error) {
    console.error('Failed to start crawl:', error)
    setIsCrawling(false)
}
```

- User-friendly error messages
- Modal dialogs for critical errors
- Console logging for debugging

---

## Extension Points

### 1. Custom Storage Backend

Implement `storage.Storage` interface:

```go
type CustomStorage struct {}

func (s *CustomStorage) Init() error { ... }
func (s *CustomStorage) Visited(id uint64) error { ... }
func (s *CustomStorage) IsVisited(id uint64) (bool, error) { ... }
// ...

c.SetStorage(&CustomStorage{})
```

### 2. Custom Callbacks

Add specialized crawling behavior:

```go
c.OnHTML("meta[name='description']", func(e *HTMLElement) {
    description := e.Attr("content")
    // Save SEO metadata
})

c.OnResponse(func(r *Response) {
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

- **Crawler:** Configurable parallelism per domain
- **Async Mode:** Non-blocking network I/O
- **Goroutines:** Each crawl runs in separate goroutine

### 2. Memory Management

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
- Per-domain limits

### 3. robots.txt Compliance

- Optional (can be enabled)
- Respects crawl-delay and disallow rules

### 4. Database Access

- Local SQLite file (~/.bluesnake/)
- No network exposure
- File permissions managed by OS

### 5. User Agent

- Identifies crawler in requests
- Allows server operators to block if needed

---

## Deployment

### Desktop Application

Built with Wails:

```bash
cd cmd/desktop
wails build
```

Produces native executable for target platform:
- **macOS:** `.app` bundle
- **Windows:** `.exe`
- **Linux:** ELF binary

### Crawler Library

Can be used standalone:

```go
import "github.com/agentberlin/bluesnake"

config := &bluesnake.CollectorConfig{
    AllowedDomains: []string{"example.com"},
}
c := bluesnake.NewCollector(config)
c.OnHTML("title", func(e *bluesnake.HTMLElement) {
    fmt.Println(e.Text)
})
c.Visit("https://example.com")
c.Wait()
```


## Future Enhancement Opportunities

### Crawler Package
- Redis/PostgreSQL storage backend
- Distributed crawling support
- Sitemap.xml parsing
- Advanced JavaScript interaction (form filling, clicking)
- Screenshot capture
- Content extraction templates

### Desktop Application
- Export to CSV/JSON
- Crawl scheduling
- Crawl comparison/diff view
- Advanced filtering/search
- Charts and analytics
- Multiple crawler instances
- Cloud sync of crawl data
- Browser extension integration

### Performance
- Connection pooling
- Streaming database writes
- Incremental crawls (only new/changed pages)
- Crawl pause/resume

---

## Conclusion

BlueSnake demonstrates a clean separation between:
1. **Core Logic** (crawler package) - Reusable, testable, framework-agnostic
2. **User Interface** (Wails app) - Modern, responsive, cross-platform
3. **Data Persistence** (SQLite) - Simple, local, no external dependencies

The architecture allows each component to evolve independently while maintaining clear interfaces between layers. The polling-based design with database as single source of truth enables reliable UI updates at scale, while the callback pattern provides extensibility without modifying core code. Indicational events provide immediate feedback triggers, while polling handles the actual data synchronization.
