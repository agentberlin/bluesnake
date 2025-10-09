# BlueSnake Architecture

## Overview

BlueSnake is a web crawler application with multiple interfaces, consisting of these main components:
1. **BlueSnake Crawler Package** - A Go-based web scraping library (root directory)
2. **BlueSnake Desktop Application** - A Wails-based GUI application (cmd/desktop directory)
3. **BlueSnake CLI Tool** - A command-line interface for quick crawls (cmd/cli directory)

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
    ├── cli/               # Command-line interface tool
    │   └── main.go        # CLI entry point
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
```go
type Collector struct {
    UserAgent            string
    Headers              *http.Header
    MaxDepth             int
    AllowedDomains       []string
    DisallowedDomains    []string
    AllowURLRevisit      bool
    MaxBodySize          int
    CacheDir             string
    IgnoreRobotsTxt      bool
    Async                bool
    EnableRendering      bool  // JavaScript rendering with chromedp
    MaxRequests          uint32
    // ... internal fields
}
```

**Collector Options (Functional Configuration):**
- `UserAgent(ua string)` - Set user agent
- `AllowedDomains(domains ...string)` - Domain whitelist
- `MaxDepth(depth int)` - Limit crawl depth
- `MaxRequests(max uint32)` - Limit total requests
- `EnableJSRendering()` - Enable JavaScript rendering
- `Async(a ...bool)` - Enable async crawling
- `CacheDir(path string)` - Enable request caching
- `IgnoreRobotsTxt()` - Ignore robots.txt restrictions
- And many more...

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

2. **Crawler Integration:**
   - Creates new `bluesnake.Collector` instance for each crawl
   - Configures with domain-specific settings from database
   - Runs in goroutine to prevent UI blocking
   - Uses callbacks to emit events to frontend

3. **Event Emission:**
   ```go
   runtime.EventsEmit(ctx, "crawl:started", data)
   runtime.EventsEmit(ctx, "crawl:request", data)
   runtime.EventsEmit(ctx, "crawl:result", result)
   runtime.EventsEmit(ctx, "crawl:error", error)
   runtime.EventsEmit(ctx, "crawl:completed", data)
   ```

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
```

**Event Listeners:**
```typescript
EventsOn("crawl:started", ...)
EventsOn("crawl:request", ...)   // URL being crawled
EventsOn("crawl:result", ...)    // Successful crawl
EventsOn("crawl:error", ...)     // Error occurred
EventsOn("crawl:completed", ...) // Crawl finished
```

**Features:**
- Real-time crawl progress display
- URL status tracking (discovered → crawling → completed)
- Project history with cards
- Crawl comparison (dropdown to select past crawls)
- Delete crawls and projects with confirmation modals

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

Backend emits events using Wails runtime:

```go
runtime.EventsEmit(ctx, "crawl:result", result)
```

Frontend listens with:

```typescript
EventsOn("crawl:result", (result) => {
  // Update UI
})
```

**Event Flow:**
1. Go code calls `runtime.EventsEmit()`
2. Wails runtime pushes event to frontend
3. All registered listeners invoked
4. React state updated, UI re-renders

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
│  │   Creates Collector instance        │   │
│  │   Registers callbacks               │   │
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
│  │  InMemoryStorage (temporary)        │   │
│  │                                     │   │
│  │  • Visited URLs (hash-based)       │   │
│  │  • Cookies (in-memory jar)         │   │
│  │  • Cleared after crawl completes   │   │
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
     │                               │                            │                         │
     │                               │ GetOrCreateConfig() ───────┼────────────────────────▶│
     │                               │                            │                         │
     │                               │ go runCrawler()            │                         │
     │                               │    │                       │                         │
     │                               │    └──▶NewCollector()─────▶│                         │
     │                               │                            │ Init()                  │
     │                               │                            │ InMemoryStorage.Init()  │
     │                               │                            │                         │
     │◀─ "crawl:started" ────────────│                            │                         │
     │                               │    OnRequest()             │                         │
     │◀─ "crawl:request" ────────────│◀───────────────────────────│                         │
     │                               │                            │                         │
     │                               │    OnResponse()            │                         │
     │                               │    OnHTML("title")         │                         │
     │◀─ "crawl:result" ─────────────│◀───────────────────────────│                         │
     │                               ├───────SaveCrawledUrl()────▶│────────────────────────▶│
     │                               │                            │                         │
     │                               │    OnHTML("a[href]")       │                         │
     │                               │       Visit(link)──────────▶│                         │
     │                               │                            │ (recursive)             │
     │                               │                            │                         │
     │                               │    OnError()               │                         │
     │◀─ "crawl:error" ──────────────│◀───────────────────────────│                         │
     │                               ├───────SaveCrawledUrl()────▶│────────────────────────▶│
     │                               │                            │                         │
     │                               │    Wait()                  │                         │
     │                               │                            │ (blocks until done)     │
     │                               │                            │                         │
     │                               ├───UpdateCrawlStats()──────▶│────────────────────────▶│
     │◀─ "crawl:completed" ──────────│                            │                         │
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

3. **Crawler Setup:**
   - Callbacks registered:
     - `OnRequest`: Emit "crawl:request" event
     - `OnResponse`: Check HTTP headers, indexability
     - `OnHTML("title")`: Extract title, save to DB, emit result
     - `OnHTML("a[href]")`: Find links, queue visits
     - `OnError`: Save error to DB, emit error event

4. **Crawling:**
   - `Visit(url)` initiates crawl
   - Crawler internally:
     - Checks if URL visited (InMemoryStorage)
     - Makes HTTP request
     - Calls OnResponse callback
     - Parses HTML (with chromedp if enabled)
     - Calls OnHTML callbacks
     - Discovers new links, queues them
   - Desktop app callbacks:
     - Save each result to database
     - Emit events to update UI in real-time

5. **Completion:**
   - `Wait()` blocks until all requests complete
   - Calculate crawl duration
   - Update crawl statistics in database
   - Emit "crawl:completed" event
   - UI updates to show final state

---

## Key Architectural Patterns

### 1. Separation of Concerns

- **Crawler Package:** Pure crawling logic, no UI dependencies
- **Desktop App:** UI and persistence layer
- **Database Layer:** Abstracted via GORM models

### 2. Event-Driven Architecture

- Crawler callbacks drive data flow
- Wails events enable real-time UI updates
- Loose coupling between components

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
- Events stream results as they arrive

---

## Configuration System

### Two-Level Configuration

#### 1. Crawler Configuration (Per-Crawl)

Set via `CollectorOption` functions when creating collector:

```go
c := bluesnake.NewCollector(
    bluesnake.AllowedDomains(domain),
    bluesnake.EnableJSRendering(),
)

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
4. Translated to CollectorOptions
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

c := bluesnake.NewCollector()
c.OnHTML("title", func(e *bluesnake.HTMLElement) {
    fmt.Println(e.Text)
})
c.Visit("https://example.com")
c.Wait()
```

### CLI Tool

A simple command-line interface for quick crawls:

```bash
cd cmd/cli
go build -o bluesnake-cli
./bluesnake-cli --url https://example.com
./bluesnake-cli --url https://example.com -r  # With JS rendering
```

**Features:**
- Simple URL crawling
- Optional JavaScript rendering (`-r` flag)
- Prints URLs, titles, status codes, and indexability to stdout
- Perfect for quick tests or automation scripts

---

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

The architecture allows each component to evolve independently while maintaining clear interfaces between layers. The event-driven design enables real-time UI updates, and the callback pattern provides extensibility without modifying core code.
