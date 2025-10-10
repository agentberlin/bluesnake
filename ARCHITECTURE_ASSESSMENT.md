# BlueSnake Architecture Assessment & Future Roadmap

**Assessment Date:** 2025-10-09
**Purpose:** Evaluate if current architecture can support planned features without requiring a complete rewrite

## Executive Summary

### Verdict: ✅ **EXTENSIBLE WITHOUT REWRITE**

The current BlueSnake architecture has a solid foundation that can accommodate all planned features through **incremental additions and refactoring**. No complete rewrite is necessary.

**Key Strengths:**
- Clean separation between crawler package and UI layer
- Extensible callback-based event system
- Event-driven real-time updates architecture
- Appropriate technology choices (Wails, SQLite, Go)
- Modular crawler design with functional options

**Required Changes:**
- Database schema expansion (new tables, no breaking changes to existing)
- Code reorganization into shared packages
- Addition of new architectural layers (export, integrations, programmatic API)
- Enhanced data capture in crawler callbacks

---

## Current Architecture Analysis

### Strengths

#### 1. Separation of Concerns ✅
The three-layer architecture is well-designed:
- **Crawler Package**: Framework-agnostic, reusable web scraping logic
- **Desktop App**: UI and persistence layer built with Wails
- **Database Layer**: SQLite with GORM for local storage

This separation means changes to one layer don't cascade to others.

#### 2. Callback System ✅
The crawler's callback pattern is highly extensible:
```go
c.OnRequest(func(*Request))
c.OnResponse(func(*Response))
c.OnHTML(selector, func(*HTMLElement))
c.OnError(func(*Response, error))
```

New features can be added by registering additional callbacks without modifying core crawler code.

#### 3. Event-Driven Communication ✅
Backend → Frontend events via Wails runtime:
```go
runtime.EventsEmit(ctx, "crawl:result", result)
```

This pattern scales well for real-time updates as more data types are captured.

#### 4. Technology Stack ✅
- **Go**: Excellent for concurrent crawling, low-level control
- **Wails**: Cross-platform desktop apps with native performance
- **SQLite**: Perfect for local desktop storage, no server needed
- **React/TypeScript**: Modern, maintainable UI development

### Current Limitations

#### 1. Database Schema - Too Simple ⚠️

**Current `CrawledUrl` table only stores:**
```go
type CrawledUrl struct {
    URL       string
    Status    int
    Title     string
    Indexable string
    Error     string
}
```

**Planned features require:**
- Meta descriptions, H1/H2 tags, word count, content hashes
- Response times, HTTP headers, cookies, last-modified dates
- Full HTML content, rendered HTML, screenshots
- JavaScript errors
- Link relationships (internal/external/canonical/hreflang)

**Impact:** Major schema expansion needed, but no breaking changes to existing tables.

#### 2. Link Graph Architecture - Missing ❌

**Critical Gap:** No mechanism to track link relationships.

**Current behavior:**
- Crawler visits links but doesn't persist source → target relationships
- Cannot answer: "What pages link to this URL?"
- Cannot export inlink/outlink graphs
- No canonical/hreflang/pagination relationship tracking

**Required:**
- New `Link` table to store edge data in link graph
- New callbacks to capture links before visiting them
- Query methods: `GetInlinks()`, `GetOutlinks()`, `GetLinksByType()`

#### 3. Large Asset Storage - Strategy Undefined ⚠️

Storing HTML content, rendered HTML, and screenshots will dramatically increase storage requirements.

**Current approach:** Everything in SQLite database

**Problem:** SQLite is optimized for structured data, not large BLOBs. Storing HTML/screenshots as BLOBs will:
- Bloat database file size
- Slow down queries
- Complicate backups

**Recommended approach:**
- Store assets on filesystem: `~/.bluesnake/data/{crawl_id}/{page_id}.{ext}`
- Store file paths in database
- Keep database lightweight and fast

#### 4. External API Integrations - No Architecture ❌

**Planned integrations:**
- Google Analytics API
- Google Search Console API
- PageSpeed Insights API
- Structured Data Validation API

**Current architecture has no provision for:**
- API credential storage (encrypted tokens)
- API client implementations
- Correlation between crawled data and API data
- Data models for external API responses
- OAuth2 flows or API key management

**Required:** New architectural layer for third-party integrations.

#### 5. Programmatic API Access - Unclear 🤔

**Current state:**
- Crawler package is usable as Go library ✅
- Desktop app database layer is NOT exposed ❌
- No REST/HTTP API ❌

**User request from goals.md:**
> "Ability to export all the crawled URLs, inlinks and outlinks"

**This implies:**
- Users need programmatic access to crawl results
- CLI tools may need to query the database
- Future automation workflows need an API

**Gap:** Desktop app's database operations are tightly coupled to Wails app, not exposed as reusable library.

---

## Planned Features Analysis

### Category 1: Enhanced Data Extraction

**Goals:**
- Page titles, meta descriptions, H1/H2 tags ✅ (simple additions)
- Word count, content hashes ✅ (simple additions)
- HTTP headers, cookies, response times ✅ (simple additions)
- Last-Modified headers ✅ (simple additions)

**Architecture Impact:** LOW
- Add fields to database schema
- Add data extraction in existing callbacks
- No structural changes needed

**Implementation:** Expand `Page` table (renamed from `CrawledUrl`), add extraction logic in `OnResponse` and `OnHTML` callbacks.

---

### Category 2: Link Relationship Tracking

**Goals:**
- Internal hyperlinks with inlink/outlink tracking
- External links
- Canonical links
- Hreflang attributes
- Pagination (rel=next/prev)
- Export inlinks/outlinks

**Architecture Impact:** HIGH
- Requires new `Link` table
- Requires new data model for relationships
- Requires new callbacks to capture links
- Requires new query methods
- Changes export format

**Implementation:**
1. Create `Link` table with `FromPageID`, `ToURL`, `LinkType`, `Rel`, etc.
2. Add `OnHTML("a[href]")` callback to capture all links (not just visit them)
3. Add `OnHTML("link[rel=canonical]")`, `OnHTML("link[rel=alternate]")` for special link types
4. Create query methods: `GetInlinks(pageID)`, `GetOutlinks(pageID)`
5. Update export functionality to include link data

**Critical:** This is the biggest architectural addition, but it's **additive** not **breaking**.

---

### Category 3: Content Storage

**Goals:**
- Store original HTML
- Store rendered HTML (post-JavaScript)
- Store screenshots

**Architecture Impact:** MEDIUM
- Requires file storage strategy
- Requires asset management system
- May require large disk space

**Implementation:**
1. Create `~/.bluesnake/data/` directory structure
2. Create `Asset` table with `PageID`, `Type`, `FilePath`, `Size`
3. Add file writing in callbacks:
   - `OnResponse`: Save original HTML
   - `OnScraped`: Save rendered HTML (if JS rendering enabled)
   - Screenshot capture: Add chromedp screenshot logic
4. Add file retrieval methods in client API

---

### Category 4: JavaScript Rendering Enhancements

**Goals:**
- Rendered page screenshots ✅ (already have chromedp infrastructure)
- Custom screenshot window sizes ✅ (configuration option)
- JavaScript error reporting ⚠️ (requires new capture mechanism)

**Architecture Impact:** LOW-MEDIUM
- Screenshot capture: Extend existing chromedp integration
- JS errors: Requires capturing browser console logs
- Configuration: Expand `Config` table

**Implementation:**
1. Add screenshot capture to chromedp renderer
2. Add console log listener to chromedp
3. Create `JSError` table
4. Add config fields: `TakeScreenshots`, `ScreenshotWidth`, `ScreenshotHeight`, `CaptureJSErrors`

---

### Category 5: Performance & Network Settings

**Goals:**
- Response timeout configuration
- 5XX retry logic
- Memory allocation control
- Max threads configuration
- Max URLs per second throttling
- Proxy configuration
- Custom user-agent
- Robots.txt handling

**Architecture Impact:** LOW
- Mostly configuration options
- Crawler already supports most of these via `CollectorOption` functions

**Implementation:**
1. Expand `Config` table with new fields
2. Map config fields to `CollectorOption` functions when creating crawler
3. Add UI controls in Config component

**Note:** Many of these already exist in the crawler but aren't exposed in the UI.

---

### Category 6: Subdomain & External Crawling

**Goals:**
- Crawl all subdomains
- Crawl external links
- Override nofollow attributes
- Crawl linked XML sitemaps

**Architecture Impact:** MEDIUM
- Changes crawl scope significantly
- May create very large datasets
- Requires subdomain discovery mechanism
- Requires sitemap parsing

**Implementation:**
1. Subdomain crawling: Modify `AllowedDomains` logic to use wildcard matching (`*.example.com`)
2. External crawling: Remove domain restrictions or add multiple domains
3. Nofollow override: Modify link following logic to ignore `rel=nofollow`
4. Sitemap crawling: Add sitemap parser, discover sitemaps from robots.txt or sitemap index

**Considerations:**
- External crawling can explode the crawl size (entire web)
- Need crawl depth limits and URL filtering
- May need separate "external crawl" project type

---

### Category 7: External API Integrations

**Goals:**
- Google Analytics integration
- Google Search Console integration
- PageSpeed Insights integration
- Structured Data validation

**Architecture Impact:** HIGH
- Requires new architectural layer: `integrations/`
- Requires credential management (encrypted storage)
- Requires API client implementations
- Requires OAuth2 flows (for GA/GSC)
- Requires data correlation between crawl results and API data

**Implementation:**
1. Create `integrations/` package with subpackages:
   - `integrations/analytics` - GA client
   - `integrations/searchconsole` - GSC client
   - `integrations/pagespeed` - PageSpeed Insights client
   - `integrations/structureddata` - Schema.org validator
2. Create `Credential` table for encrypted API tokens
3. Create `APIData` table for storing API responses
4. Add OAuth2 flow UI in desktop app
5. Create correlation logic: Match URLs between crawl and API data
6. Add UI to view combined data (crawl + API)

**Security Considerations:**
- Use `crypto/aes` or OS keychain for credential encryption
- Never store plaintext tokens
- Implement token refresh logic for OAuth2

---

### Category 8: Export Functionality

**Goals:**
- Export crawled URLs, inlinks, outlinks to CSV/JSON

**Architecture Impact:** MEDIUM
- Requires new `export/` package
- Requires data transformation logic
- Requires format-specific encoders

**Implementation:**
1. Create `export/` package with:
   - `export/csv` - CSV encoder
   - `export/json` - JSON encoder
2. Define export schemas:
   - Pages export: All page data
   - Links export: Link graph data
   - Combined export: Pages + Links
3. Add export methods to `client` package:
   ```go
   func (c *Client) ExportToCSV(crawlID uint, path string) error
   func (c *Client) ExportToJSON(crawlID uint, path string) error
   ```
4. Add export UI in desktop app (button + file picker)

---

### Category 9: LLM Integration (Future)

**Goals from goals.md:**
> "Ability to run a local LLM using ollama... make decisions about data... expose to users to create custom charts or custom python code and report generation"

**Architecture Impact:** VERY HIGH
- Requires LLM client integration (Ollama API)
- Requires Python execution sandbox
- Requires code generation and validation
- Requires security sandboxing (prevent arbitrary code execution)
- Requires chart generation libraries

**Implementation Considerations:**
1. **LLM Integration:**
   - Ollama REST API client
   - Prompt engineering for data analysis tasks
   - Context window management (may need to summarize large crawl data)

2. **Python Execution:**
   - Embedded Python interpreter (gopy or exec Python subprocess)
   - Sandboxing (Docker container or restricted user)
   - Library restrictions (pandas, matplotlib allowed; os, subprocess blocked)

3. **Code Generation Flow:**
   - User describes task in natural language
   - LLM generates Python code
   - Show code to user for approval
   - Execute in sandbox
   - Return results/charts

**Security Concerns:**
- Code execution is inherently risky
- Must sandbox Python environment
- May need code review/approval UI
- Consider WebAssembly Python (Pyodide) for browser-based execution

**Recommendation:** This is a Phase 5+ feature. Architecture not needed immediately, but keep in mind:
- Design export formats (CSV/JSON) to be Python-friendly
- Keep database schema predictable for data analysis
- Consider adding a plugin system for extensibility

---

## Proposed Architecture Restructuring

### New Directory Structure

```
bluesnake/
├── *.go                    # Core crawler package (UNCHANGED)
├── storage/                # Crawler storage (UNCHANGED)
├── debug/                  # Debug utilities (UNCHANGED)
├── extensions/             # Crawler extensions (UNCHANGED)
├── proxy/                  # Proxy support (UNCHANGED)
├── queue/                  # Request queuing (UNCHANGED)
│
├── models/                 # NEW: Shared data models
│   └── models.go           # Page, Link, Asset, Config, etc.
│
├── database/               # NEW: Database layer (extracted from desktop)
│   ├── db.go               # GORM operations, connection management
│   └── migrations.go       # Schema migrations
│
├── export/                 # NEW: Export functionality
│   ├── csv.go              # CSV export
│   ├── json.go             # JSON export
│   └── types.go            # Export format definitions
│
├── integrations/           # NEW: External API integrations
│   ├── analytics.go        # Google Analytics client
│   ├── searchconsole.go    # Google Search Console client
│   ├── pagespeed.go        # PageSpeed Insights client
│   └── structureddata.go   # Schema.org validation
│
├── client/                 # NEW: Programmatic API
│   └── client.go           # Library interface to database
│
└── cmd/                    # Application executables
    ├── cli/                # Command-line interface (UNCHANGED)
    │   └── main.go
    │
    ├── desktop/            # Desktop application (REFACTORED)
    │   ├── main.go         # Entry point (minimal changes)
    │   ├── app.go          # Backend API (now uses client package)
    │   └── frontend/       # React UI (UNCHANGED)
    │
    └── server/             # OPTIONAL: REST API server (FUTURE)
        └── main.go
```

### Rationale for Changes

#### 1. Extract `models/` Package
**Why:** Data models are currently embedded in `cmd/desktop/database.go`. This couples them to the Wails app.

**Solution:** Move all GORM models to `models/models.go`. Now they can be imported by:
- Desktop app
- CLI tool
- Client library
- Future REST API

**Migration:** Simple code move, no logic changes.

---

#### 2. Extract `database/` Package
**Why:** Database operations are currently in `cmd/desktop/database.go`, tightly coupled to the Wails app.

**Solution:** Move database logic to `database/db.go`. Expose clean interfaces:
```go
type DB struct {
    *gorm.DB
}

func Open(path string) (*DB, error)
func (db *DB) GetProjects() ([]models.Project, error)
func (db *DB) GetCrawls(projectID uint) ([]models.Crawl, error)
func (db *DB) SavePage(page *models.Page) error
func (db *DB) SaveLink(link *models.Link) error
// ... etc
```

**Benefits:**
- Reusable by CLI, client library, REST API
- Testable in isolation
- Clear separation between data access and business logic

**Migration:** Refactor `cmd/desktop/database.go` into `database/db.go`, update imports in `cmd/desktop/app.go`.

---

#### 3. Create `client/` Package
**Why:** Users need programmatic access to crawl results.

**Solution:** Create a high-level client library:
```go
package client

import "github.com/agentberlin/bluesnake/database"

type Client struct {
    db *database.DB
}

func NewClient(dbPath string) (*Client, error) {
    db, err := database.Open(dbPath)
    if err != nil {
        return nil, err
    }
    return &Client{db: db}, nil
}

// High-level query methods
func (c *Client) GetProjects() ([]models.Project, error)
func (c *Client) GetInlinks(pageID uint) ([]models.Link, error)
func (c *Client) GetOutlinks(pageID uint) ([]models.Link, error)
func (c *Client) ExportToCSV(crawlID uint, path string) error
```

**Usage:**
```go
// User's script
package main

import "github.com/agentberlin/bluesnake/client"

func main() {
    c, _ := client.NewClient("~/.bluesnake/bluesnake.db")
    projects, _ := c.GetProjects()
    for _, p := range projects {
        c.ExportToCSV(p.LatestCrawlID, fmt.Sprintf("%s.csv", p.Domain))
    }
}
```

**Desktop app usage:**
```go
// cmd/desktop/app.go
type App struct {
    ctx    context.Context
    client *client.Client  // Use same client package
}
```

**Benefits:**
- Single source of truth for database operations
- Desktop app and external tools use same API
- Easier to maintain and test

---

#### 4. Create `export/` Package
**Why:** Export logic will be complex (CSV encoding, JSON marshaling, link graph traversal).

**Solution:** Dedicated package for export operations:
```go
package export

type PageExport struct {
    URL         string
    Status      int
    Title       string
    // ... all fields
}

type LinkExport struct {
    FromURL    string
    ToURL      string
    LinkType   string
    AnchorText string
}

func ToCSV(crawlID uint, db *database.DB, w io.Writer) error
func ToJSON(crawlID uint, db *database.DB, w io.Writer) error
```

**Benefits:**
- Reusable across CLI, desktop app, REST API
- Format-specific logic isolated
- Easy to add new formats (Excel, XML, etc.)

---

#### 5. Create `integrations/` Package
**Why:** External API integrations have complex logic (OAuth, rate limiting, data mapping).

**Solution:** One subpackage per integration:

**Google Analytics:**
```go
package analytics

type Client struct {
    credentials *oauth2.Config
    token       *oauth2.Token
}

func NewClient(credentialJSON string) (*Client, error)
func (c *Client) FetchPageviews(projectID string, startDate, endDate string) ([]PageviewData, error)
```

**Google Search Console:**
```go
package searchconsole

type Client struct {
    credentials *oauth2.Config
    token       *oauth2.Token
}

func NewClient(credentialJSON string) (*Client, error)
func (c *Client) FetchSearchAnalytics(siteURL string, startDate, endDate string) ([]QueryData, error)
```

**PageSpeed Insights:**
```go
package pagespeed

type Client struct {
    apiKey string
}

func NewClient(apiKey string) *Client
func (c *Client) Analyze(url string) (*PageSpeedResult, error)
```

**Structured Data:**
```go
package structureddata

func Validate(html string) ([]ValidationError, error)
```

**Benefits:**
- Clean separation of concerns
- Each integration can be tested independently
- Easy to add new integrations
- Credentials isolated per integration

---

### Enhanced Database Schema

#### Core Tables (Expanded)

**`Config` (EXPANDED):**
```go
type Config struct {
    ID                 uint
    Domain             string  // unique

    // Rendering
    JSRenderingEnabled bool
    TakeScreenshots    bool
    ScreenshotWidth    int    // default: 1920
    ScreenshotHeight   int    // default: 1080
    CaptureJSErrors    bool

    // Crawling
    Parallelism        int    // default: 5
    FollowExternal     bool
    FollowNofollow     bool
    CrawlSubdomains    bool
    CrawlSitemaps      bool

    // Extraction
    ExtractMeta        bool   // default: true
    ExtractH1H2        bool   // default: true
    ExtractWordCount   bool
    ComputeHash        bool
    StoreHTML          bool
    StoreRenderedHTML  bool
    StoreHeaders       bool
    StoreCookies       bool

    // Performance
    ResponseTimeout    int    // seconds, default: 20
    Retry5xx           bool
    MaxThreads         int
    MaxURLsPerSecond   float64

    // Network
    UserAgent          string
    ProxyURL           string
    IgnoreRobotsTxt    bool

    CreatedAt          int64
    UpdatedAt          int64
}
```

**`Project` (UNCHANGED):**
```go
type Project struct {
    ID        uint
    URL       string
    Domain    string  // unique
    Crawls    []Crawl
    CreatedAt int64
    UpdatedAt int64
}
```

**`Crawl` (UNCHANGED):**
```go
type Crawl struct {
    ID            uint
    ProjectID     uint
    CrawlDateTime int64
    CrawlDuration int64
    PagesCrawled  int
    Pages         []Page        // Renamed from CrawledUrls
    Links         []Link        // NEW
    CreatedAt     int64
    UpdatedAt     int64
}
```

**`Page` (EXPANDED, renamed from `CrawledUrl`):**
```go
type Page struct {
    ID               uint
    CrawlID          uint

    // URL & Status
    URL              string
    Status           int
    Error            string

    // Content
    Title            string
    MetaDescription  string  // NEW
    H1               string  // NEW
    H2s              string  // NEW: JSON array ["H2 1", "H2 2"]
    WordCount        int     // NEW
    Hash             string  // NEW: SHA256 of content for duplicate detection

    // Performance
    ResponseTime     int64   // NEW: milliseconds
    LastModified     string  // NEW: HTTP header

    // Indexability
    Indexable        string  // "Yes" or "No"
    IndexableReason  string  // NEW: "robots meta", "noindex", "blocked by robots.txt"

    // Assets
    HasHTML          bool    // NEW: Original HTML stored
    HasRenderedHTML  bool    // NEW: Rendered HTML stored
    HasScreenshot    bool    // NEW: Screenshot captured

    // Relationships
    HTTPHeaders      []HTTPHeader  // NEW
    Cookies          []Cookie      // NEW
    JSErrors         []JSError     // NEW
    Assets           []Asset       // NEW

    CreatedAt        int64
}
```

#### New Tables

**`Link` (NEW):**
```go
type Link struct {
    ID           uint
    CrawlID      uint

    // Relationship
    FromPageID   uint    // Source page (FK to Page)
    FromURL      string  // Source URL (denormalized for convenience)
    ToURL        string  // Target URL (may not be crawled)
    ToPageID     uint    // Target page ID if crawled (nullable)

    // Link attributes
    LinkType     string  // "internal", "external", "canonical", "hreflang", "pagination"
    Rel          string  // "nofollow", "sponsored", "ugc", etc.
    AnchorText   string  // Link text

    // Special attributes
    HreflangLang string  // For hreflang links: "en-US", "de-DE", etc.

    CreatedAt    int64
}

// Indexes
// - Index on (CrawlID, FromPageID) for outlink queries
// - Index on (CrawlID, ToURL) for inlink queries
// - Index on LinkType for filtering
```

**`Asset` (NEW):**
```go
type Asset struct {
    ID       uint
    PageID   uint

    Type     string  // "html", "rendered_html", "screenshot"
    FilePath string  // Relative to ~/.bluesnake/data/
    Size     int64   // Bytes

    CreatedAt int64
}
```

**`HTTPHeader` (NEW):**
```go
type HTTPHeader struct {
    ID       uint
    PageID   uint

    Key      string  // "Content-Type", "Cache-Control", etc.
    Value    string

    CreatedAt int64
}

// Index on PageID for fast lookup
```

**`Cookie` (NEW):**
```go
type Cookie struct {
    ID       uint
    PageID   uint

    Name     string
    Value    string
    Domain   string
    Path     string
    Expires  int64   // Unix timestamp
    Secure   bool
    HttpOnly bool
    SameSite string  // "Strict", "Lax", "None"

    CreatedAt int64
}
```

**`JSError` (NEW):**
```go
type JSError struct {
    ID       uint
    PageID   uint

    Message  string
    Source   string  // URL of script
    Line     int
    Column   int
    Stack    string  // Stack trace

    CreatedAt int64
}
```

**`APIData` (NEW):**
```go
type APIData struct {
    ID        uint
    ProjectID uint
    PageID    uint    // Nullable - some data is project-level (e.g., GA site-wide metrics)

    Source    string  // "ga", "gsc", "pagespeed", "structured_data"
    DataType  string  // "pageviews", "clicks", "performance_score", "schema_errors"
    Data      string  // JSON blob with actual data
    DateRange string  // "2025-01-01:2025-01-31" for time-series data

    FetchedAt int64   // When API was called
    CreatedAt int64
}

// Indexes
// - Index on (ProjectID, Source, DataType) for fast filtering
// - Index on PageID for page-specific data
```

**`Credential` (NEW):**
```go
type Credential struct {
    ID             uint
    ProjectID      uint

    Service        string  // "ga", "gsc", "pagespeed"
    EncryptedToken string  // AES-encrypted OAuth token or API key
    TokenType      string  // "oauth2", "api_key"
    ExpiresAt      int64   // For OAuth2 tokens

    CreatedAt      int64
    UpdatedAt      int64
}

// Note: Use crypto/aes for encryption with a master key stored in OS keychain
```

---

### Communication Architecture

#### Current (Preserved)

**Frontend → Backend:**
```typescript
import { StartCrawl, GetProjects } from "../wailsjs/go/main/App"

await StartCrawl("https://example.com")
const projects = await GetProjects()
```

**Backend → Frontend:**
```go
runtime.EventsEmit(ctx, "crawl:result", result)
runtime.EventsEmit(ctx, "crawl:error", error)
```

#### Enhanced (New Flows)

**Desktop App → Client Package:**
```go
// cmd/desktop/app.go
type App struct {
    ctx    context.Context
    client *client.Client  // NEW: Use shared client
}

func (a *App) GetProjects() ([]models.Project, error) {
    return a.client.GetProjects()  // Delegate to client
}

func (a *App) ExportToCSV(crawlID uint, path string) error {
    return a.client.ExportToCSV(crawlID, path)
}
```

**External Tool → Client Package:**
```go
// User's automation script
package main

import "github.com/agentberlin/bluesnake/client"

func main() {
    c, err := client.NewClient("~/.bluesnake/bluesnake.db")
    if err != nil {
        log.Fatal(err)
    }

    projects, _ := c.GetProjects()
    for _, project := range projects {
        crawls, _ := c.GetCrawls(project.ID)
        latestCrawl := crawls[0]

        // Export data
        c.ExportToCSV(latestCrawl.ID, fmt.Sprintf("%s_pages.csv", project.Domain))

        // Analyze links
        pages, _ := c.GetPages(latestCrawl.ID)
        for _, page := range pages {
            inlinks, _ := c.GetInlinks(page.ID)
            fmt.Printf("%s has %d inlinks\n", page.URL, len(inlinks))
        }
    }
}
```

**Backend → Integrations:**
```go
// Fetch Google Analytics data
gaClient, _ := analytics.NewClient(gaCredentials)
pageviews, _ := gaClient.FetchPageviews(project.ID, "2025-01-01", "2025-01-31")

// Store in database
for _, pv := range pageviews {
    apiData := &models.APIData{
        ProjectID: project.ID,
        PageID:    findPageByURL(pv.URL),
        Source:    "ga",
        DataType:  "pageviews",
        Data:      toJSON(pv),
        DateRange: "2025-01-01:2025-01-31",
        FetchedAt: time.Now().Unix(),
    }
    a.client.SaveAPIData(apiData)
}

// Emit to frontend
runtime.EventsEmit(a.ctx, "api:data:fetched", "Google Analytics data updated")
```

---

## Migration Strategy

### Phase 0: Preparation (Non-Breaking)
**Goal:** Set up foundation without breaking existing functionality.

**Tasks:**
1. Create `models/` package and move existing models
2. Create `database/` package and move database logic
3. Update `cmd/desktop/` to import from new packages
4. Add tests for database operations
5. Ensure desktop app still works identically

**Time estimate:** 1-2 days
**Risk:** Low (pure refactoring)

---

### Phase 1: Schema Expansion (Database Migration)
**Goal:** Add new tables and fields without losing existing data.

**Tasks:**
1. Add new tables: `Link`, `Asset`, `HTTPHeader`, `Cookie`, `JSError`, `APIData`, `Credential`
2. Expand `Page` table (rename from `CrawledUrl`) with new fields
3. Expand `Config` table with all new settings
4. Write GORM AutoMigrate code
5. Test migration on test database
6. Run migration on existing user databases

**Schema migration code:**
```go
// database/migrations.go
func Migrate(db *gorm.DB) error {
    // Auto-migrate all models (safe, non-destructive)
    return db.AutoMigrate(
        &models.Config{},
        &models.Project{},
        &models.Crawl{},
        &models.Page{},      // Renamed from CrawledUrl
        &models.Link{},      // NEW
        &models.Asset{},     // NEW
        &models.HTTPHeader{},  // NEW
        &models.Cookie{},    // NEW
        &models.JSError{},   // NEW
        &models.APIData{},   // NEW
        &models.Credential{}, // NEW
    )
}
```

**Data migration (if renaming `CrawledUrl` → `Page`):**
```go
// GORM will handle this automatically if you use gorm:"table:crawled_urls"
type Page struct {
    gorm.Model
    // ... fields
}

func (Page) TableName() string {
    return "crawled_urls"  // Keep old table name for backward compatibility
}
```

**Time estimate:** 2-3 days
**Risk:** Medium (database changes always risky, but GORM AutoMigrate is safe)

---

### Phase 2: Link Tracking (Core Feature)
**Goal:** Capture and store link relationships.

**Tasks:**
1. Add `SaveLink()` method to database package
2. Add link extraction callback to crawler:
   ```go
   c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
       link := &models.Link{
           CrawlID:    crawlID,
           FromPageID: currentPageID,
           FromURL:    e.Request.URL.String(),
           ToURL:      e.Request.AbsoluteURL(e.Attr("href")),
           LinkType:   determineLinkType(e),
           Rel:        e.Attr("rel"),
           AnchorText: e.Text,
       }
       app.client.SaveLink(link)
   })
   ```
3. Add canonical/hreflang/pagination link extraction:
   ```go
   c.OnHTML("link[rel=canonical]", func(e *bluesnake.HTMLElement) { ... })
   c.OnHTML("link[rel=alternate][hreflang]", func(e *bluesnake.HTMLElement) { ... })
   c.OnHTML("link[rel=next], link[rel=prev]", func(e *bluesnake.HTMLElement) { ... })
   ```
4. Add query methods: `GetInlinks()`, `GetOutlinks()`, `GetLinksByType()`
5. Update UI to show link data

**Time estimate:** 3-4 days
**Risk:** Low-medium (additive feature)

---

### Phase 3: Enhanced Data Extraction
**Goal:** Capture all SEO-relevant data.

**Tasks:**
1. Expand page data extraction in callbacks:
   ```go
   c.OnHTML("meta[name=description]", func(e *bluesnake.HTMLElement) {
       page.MetaDescription = e.Attr("content")
   })

   c.OnHTML("h1", func(e *bluesnake.HTMLElement) {
       page.H1 = e.Text
   })

   c.OnHTML("h2", func(e *bluesnake.HTMLElement) {
       page.H2s = append(page.H2s, e.Text)  // Store as array
   })
   ```
2. Add word count calculation (strip HTML, count words)
3. Add content hash calculation (SHA256 of normalized text)
4. Add HTTP header capture:
   ```go
   c.OnResponse(func(r *bluesnake.Response) {
       for key, values := range r.Headers {
           for _, value := range values {
               header := &models.HTTPHeader{
                   PageID: page.ID,
                   Key:    key,
                   Value:  value,
               }
               app.client.SaveHTTPHeader(header)
           }
       }
   })
   ```
5. Add cookie capture (from response headers)
6. Add response time tracking (start time in OnRequest, calculate in OnResponse)

**Time estimate:** 2-3 days
**Risk:** Low (straightforward extraction logic)

---

### Phase 4: Asset Storage
**Goal:** Store HTML content and screenshots.

**Tasks:**
1. Create asset storage directory structure:
   ```go
   // Create directories
   os.MkdirAll("~/.bluesnake/data/{crawlID}/html/", 0755)
   os.MkdirAll("~/.bluesnake/data/{crawlID}/rendered/", 0755)
   os.MkdirAll("~/.bluesnake/data/{crawlID}/screenshots/", 0755)
   ```
2. Add HTML storage in OnResponse callback:
   ```go
   if config.StoreHTML {
       path := fmt.Sprintf("~/.bluesnake/data/%d/html/%d.html", crawlID, pageID)
       os.WriteFile(path, r.Body, 0644)
       asset := &models.Asset{
           PageID:   pageID,
           Type:     "html",
           FilePath: path,
           Size:     int64(len(r.Body)),
       }
       app.client.SaveAsset(asset)
   }
   ```
3. Add screenshot capture in chromedp backend:
   ```go
   if config.TakeScreenshots {
       var buf []byte
       chromedp.Run(ctx,
           chromedp.EmulateViewport(config.ScreenshotWidth, config.ScreenshotHeight),
           chromedp.FullScreenshot(&buf, 90),
       )
       path := fmt.Sprintf("~/.bluesnake/data/%d/screenshots/%d.png", crawlID, pageID)
       os.WriteFile(path, buf, 0644)
   }
   ```
4. Add asset retrieval methods in client package
5. Add UI to view stored HTML/screenshots

**Time estimate:** 3-4 days
**Risk:** Medium (file I/O, disk space management)

---

### Phase 5: Export Functionality
**Goal:** Export crawl results to CSV/JSON.

**Tasks:**
1. Create `export/` package with CSV and JSON encoders
2. Define export schemas (what fields to include)
3. Implement graph traversal for link export
4. Add export methods to client package:
   ```go
   func (c *Client) ExportPagesToCSV(crawlID uint, w io.Writer) error
   func (c *Client) ExportLinksToCSV(crawlID uint, w io.Writer) error
   func (c *Client) ExportToJSON(crawlID uint, w io.Writer) error
   ```
5. Add export UI to desktop app (button + file picker dialog)
6. Add export command to CLI tool

**Time estimate:** 2-3 days
**Risk:** Low

---

### Phase 6: Client Package & Programmatic API
**Goal:** Enable external tools to access crawl data.

**Tasks:**
1. Create `client/` package wrapping database operations
2. Design clean API for common operations
3. Write comprehensive documentation with examples
4. Create example scripts in `examples/` directory
5. Update CLI tool to use client package
6. Update desktop app to use client package

**Time estimate:** 2-3 days
**Risk:** Low (mostly wrapping existing functionality)

---

### Phase 7: Configuration Expansion
**Goal:** Expose all crawl settings in UI.

**Tasks:**
1. Expand Config model with all planned settings
2. Update Config UI with new controls:
   - Subdomain crawling toggle
   - External link crawling toggle
   - Nofollow override toggle
   - Screenshot settings (width, height)
   - Performance settings (timeout, retries, rate limits)
   - Network settings (proxy, user agent)
3. Map config to CollectorOptions in crawler setup
4. Add validation (e.g., parallelism between 1-100)

**Time estimate:** 2-3 days
**Risk:** Low (UI work)

---

### Phase 8: External API Integrations (Major)
**Goal:** Integrate Google Analytics, Search Console, PageSpeed Insights.

**Tasks:**
1. Create `integrations/` package structure
2. Implement Google Analytics integration:
   - OAuth2 flow
   - API client
   - Data fetching (pageviews, sessions, bounce rate)
   - Data storage in APIData table
3. Implement Google Search Console integration:
   - OAuth2 flow
   - API client
   - Data fetching (queries, clicks, impressions, position)
4. Implement PageSpeed Insights integration:
   - API key configuration
   - Performance score fetching
5. Implement structured data validation (use online validator API or local parser)
6. Add credential management:
   - Encrypted storage
   - UI for OAuth2 authorization
   - Token refresh logic
7. Add UI to view API data:
   - Correlate crawl data with API data
   - Show combined view (e.g., page with GA pageviews and GSC clicks)

**Time estimate:** 2-3 weeks (complex OAuth flows, multiple APIs)
**Risk:** High (external dependencies, OAuth complexity, rate limits)

---

### Phase 9: Advanced Features (Future)
**Goal:** LLM integration, Python execution, custom reports.

**Deferred to later phases.** Focus on core crawling and data collection first.

---

## Key Architectural Decisions

### 1. SQLite vs. PostgreSQL/MySQL

**Decision: Stick with SQLite**

**Rationale:**
- Desktop app = local database is appropriate
- SQLite handles millions of rows efficiently
- No server setup required
- Easy backup (single file)
- GORM supports both, so migration path exists if needed

**When to reconsider:** If users want centralized crawl data across multiple machines or team collaboration.

---

### 2. Filesystem Storage for Assets

**Decision: Store HTML/screenshots on filesystem, references in DB**

**Rationale:**
- SQLite BLOBs slow down queries
- File system is optimized for large files
- Easier to inspect/debug (can open HTML files directly)
- Simpler backup strategy (can exclude large assets)

**Structure:**
```
~/.bluesnake/
├── bluesnake.db                    # SQLite database
└── data/                           # Asset storage
    └── {crawlID}/                  # Per-crawl directory
        ├── html/                   # Original HTML
        │   ├── {pageID}.html
        │   └── ...
        ├── rendered/               # Rendered HTML
        │   ├── {pageID}.html
        │   └── ...
        └── screenshots/            # Screenshots
            ├── {pageID}.png
            └── ...
```

---

### 3. Link Storage: Adjacency List vs. Graph Database

**Decision: Adjacency list in SQLite**

**Rationale:**
- Graph databases (Neo4j, etc.) add complexity
- SQLite with proper indexes handles link queries well
- Link graph queries are not the primary use case (yet)
- Can migrate to graph DB later if needed

**Link table design supports:**
- Inlink queries: `SELECT * FROM links WHERE ToURL = ?`
- Outlink queries: `SELECT * FROM links WHERE FromPageID = ?`
- Link type filtering: `SELECT * FROM links WHERE LinkType = 'canonical'`
- Graph traversal (with recursive CTE if needed)

**When to reconsider:** If users need complex graph algorithms (PageRank, clustering, shortest paths).

---

### 4. Monolithic vs. Microservices

**Decision: Monolithic architecture (for now)**

**Rationale:**
- Desktop app = single process is simpler
- No network complexity
- Shared memory between components
- Easier debugging

**When to reconsider:** If adding a web-based UI or multi-user access.

---

### 5. Event-Driven vs. Polling

**Decision: Keep event-driven architecture**

**Rationale:**
- Real-time UI updates are a key feature
- Wails events work well
- Low latency, efficient

**No changes needed here.**

---

### 6. Synchronous vs. Asynchronous Crawling

**Decision: Support both (already implemented)**

**Current crawler supports:**
- Synchronous mode: `c.Visit()` blocks until complete
- Asynchronous mode: `c.Async = true`, uses goroutines

**For desktop app:** Async mode is better (non-blocking UI).

**No changes needed.**

---

### 7. Credential Storage: OS Keychain vs. Encrypted File

**Decision: Encrypted file with master key in OS keychain**

**Rationale:**
- Cross-platform (macOS Keychain, Windows Credential Manager, Linux Secret Service)
- Go library: `github.com/zalando/go-keyring`
- Master key in OS keychain, encrypted credentials in SQLite

**Implementation:**
```go
// Encrypt credential with master key
masterKey, _ := keyring.Get("bluesnake", "master_key")
encrypted := encryptAES(token, masterKey)
db.Save(&models.Credential{EncryptedToken: encrypted})

// Decrypt when needed
encrypted := credential.EncryptedToken
decrypted := decryptAES(encrypted, masterKey)
```

**Security:** Master key never stored in plaintext on disk.

---

## Performance Considerations

### 1. Database Indexes

**Critical indexes:**
```sql
-- Page lookups by URL
CREATE INDEX idx_pages_url ON pages(url);

-- Link inlink queries
CREATE INDEX idx_links_to_url ON links(to_url, crawl_id);

-- Link outlink queries
CREATE INDEX idx_links_from_page ON links(from_page_id, crawl_id);

-- Link type filtering
CREATE INDEX idx_links_type ON links(link_type, crawl_id);

-- API data lookups
CREATE INDEX idx_api_data_project ON api_data(project_id, source, data_type);
CREATE INDEX idx_api_data_page ON api_data(page_id);

-- Foreign keys (GORM creates these automatically)
```

### 2. Batch Inserts

**Current approach:** One INSERT per page/link (slow for large crawls)

**Optimization:**
```go
// Buffer inserts, flush periodically
buffer := make([]*models.Page, 0, 100)
buffer = append(buffer, page)
if len(buffer) >= 100 {
    db.CreateInBatches(buffer, 100)
    buffer = buffer[:0]
}
```

**When to implement:** When crawls exceed 1000+ pages.

### 3. Database Connection Pooling

**Current:** Single connection (fine for desktop app)

**Future:** If adding REST API, use connection pool:
```go
sqlDB, _ := db.DB()
sqlDB.SetMaxOpenConns(25)
sqlDB.SetMaxIdleConns(5)
```

### 4. Asset Storage Cleanup

**Problem:** Old crawls accumulate assets, consume disk space.

**Solution:** Add cleanup job:
```go
func (c *Client) DeleteCrawl(crawlID uint) error {
    // Delete database records
    db.Delete(&models.Crawl{}, crawlID)

    // Delete asset directory
    assetDir := fmt.Sprintf("~/.bluesnake/data/%d", crawlID)
    os.RemoveAll(assetDir)
}
```

**UI:** Add "Delete old crawls" button with disk space indicator.

---

## Security Considerations

### 1. Credential Encryption

**Must:** Encrypt all API tokens/keys before storing in database.

**Implementation:** Use AES-256-GCM with master key from OS keychain.

### 2. File Path Validation

**Risk:** Path traversal attacks if user-controlled paths are used.

**Mitigation:**
```go
// Validate asset paths
func validateAssetPath(path string) error {
    cleanPath := filepath.Clean(path)
    if !strings.HasPrefix(cleanPath, "~/.bluesnake/data/") {
        return errors.New("invalid asset path")
    }
    return nil
}
```

### 3. Python Code Execution (Future)

**Risk:** Arbitrary code execution if LLM generates malicious code.

**Mitigations:**
- Sandbox environment (Docker container, restricted user)
- Code review UI (show generated code, require user approval)
- Whitelist allowed libraries
- Disable dangerous modules (os, subprocess, socket)
- Timeout execution (kill after 30 seconds)

**Consider:** WebAssembly Python (Pyodide) running in browser (safest option).

### 4. URL Validation

**Current:** Basic URL parsing with `url.Parse()`

**Enhancement:** Validate against SSRF attacks:
```go
// Block internal IPs
func isInternalIP(ip string) bool {
    // Check 127.0.0.1, 10.x.x.x, 192.168.x.x, 169.254.x.x
}
```

### 5. Database Injection

**Current:** GORM parameterized queries (safe)

**Ensure:** Never use raw SQL with user input:
```go
// BAD
db.Raw("SELECT * FROM pages WHERE url = '" + userInput + "'")

// GOOD
db.Where("url = ?", userInput)
```

---

## Testing Strategy

### 1. Unit Tests

**Coverage:**
- Database operations (CRUD)
- Export functions (CSV/JSON encoding)
- Link graph queries (inlinks/outlinks)
- Integration clients (mock API responses)

**Example:**
```go
func TestGetInlinks(t *testing.T) {
    db := setupTestDB()
    defer db.Close()

    // Create test data
    page := &models.Page{URL: "https://example.com/page1"}
    db.Save(page)

    link := &models.Link{
        FromURL: "https://example.com/page2",
        ToURL:   "https://example.com/page1",
    }
    db.Save(link)

    // Query inlinks
    client := &client.Client{db: db}
    inlinks, _ := client.GetInlinks(page.ID)

    assert.Len(t, inlinks, 1)
    assert.Equal(t, "https://example.com/page2", inlinks[0].FromURL)
}
```

### 2. Integration Tests

**Coverage:**
- End-to-end crawl flow
- Database migrations
- File system operations (asset storage)

**Example:**
```go
func TestCrawlFlow(t *testing.T) {
    // Set up test server
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(`<html><title>Test</title><a href="/page2">Link</a></html>`))
    }))
    defer ts.Close()

    // Run crawler
    c := bluesnake.NewCollector()
    var pages []*models.Page
    c.OnHTML("title", func(e *bluesnake.HTMLElement) {
        page := &models.Page{
            URL:   e.Request.URL.String(),
            Title: e.Text,
        }
        pages = append(pages, page)
    })
    c.Visit(ts.URL)
    c.Wait()

    assert.Len(t, pages, 1)
    assert.Equal(t, "Test", pages[0].Title)
}
```

### 3. UI Tests

**Framework:** Playwright or Wails testing utilities

**Coverage:**
- Start crawl flow
- View crawl results
- Export functionality
- Configuration changes

---

## Documentation Requirements

### 1. User Documentation

**Needs:**
- Installation guide
- Quick start tutorial
- Feature overview
- Configuration reference
- Troubleshooting guide

**Format:** Markdown in `docs/` directory, rendered as static site (VitePress, MkDocs)

### 2. Developer Documentation

**Needs:**
- Architecture overview (this document!)
- API reference (godoc)
- Database schema reference
- Contributing guide
- Migration guide (for breaking changes)

### 3. API Documentation

**For programmatic access:**
```go
// Example usage
package main

import "github.com/agentberlin/bluesnake/client"

func main() {
    c, err := client.NewClient("~/.bluesnake/bluesnake.db")
    if err != nil {
        log.Fatal(err)
    }

    projects, _ := c.GetProjects()
    for _, p := range projects {
        fmt.Printf("Project: %s\n", p.Domain)

        crawls, _ := c.GetCrawls(p.ID)
        fmt.Printf("  Crawls: %d\n", len(crawls))

        if len(crawls) > 0 {
            pages, _ := c.GetPages(crawls[0].ID)
            fmt.Printf("  Pages: %d\n", len(pages))

            // Export
            c.ExportToCSV(crawls[0].ID, p.Domain + ".csv")
        }
    }
}
```

---

## Conclusion

### Summary

**Current architecture is fundamentally sound but requires significant expansion to support planned features.**

**No rewrite is necessary.** The existing patterns (callbacks, events, separation of concerns) are extensible and appropriate.

**Key additions needed:**
1. **Database schema expansion** - Add tables for links, assets, API data
2. **Code reorganization** - Extract shared packages (models, database, client)
3. **New layers** - Export, integrations, programmatic API
4. **Asset storage** - Filesystem-based storage for large content
5. **Link tracking** - Capture and query link relationships

**Migration path is incremental:** Each phase adds functionality without breaking existing features.

### Prioritized Next Steps

**Immediate (Phase 0-1):**
1. Refactor `cmd/desktop/database.go` → `database/` package
2. Create `models/` package
3. Run database migration with new schema
4. Ensure existing desktop app works with refactored code

**Short-term (Phase 2-4):**
1. Implement link tracking
2. Implement enhanced data extraction
3. Implement asset storage

**Medium-term (Phase 5-7):**
1. Create client package for programmatic access
2. Implement export functionality
3. Expand configuration UI

**Long-term (Phase 8+):**
1. External API integrations
2. Advanced features (LLM, Python execution)

### Risk Assessment

**Low risk:**
- Database schema expansion (GORM AutoMigrate is non-destructive)
- Code refactoring (pure code movement)
- New features (additive, not breaking)

**Medium risk:**
- Asset storage (file I/O, disk space management)
- Link tracking (query performance on large graphs)

**High risk:**
- External API integrations (OAuth complexity, rate limits, breaking API changes)
- Python execution (security, sandboxing)

### Final Recommendation

**Proceed with the proposed architecture.** It maintains the strengths of the current system while adding necessary layers for future features. The migration path is clear, incremental, and low-risk.

**Start with Phase 0-1** (refactoring + schema migration) to establish the foundation, then build features iteratively based on user priorities.

The architecture is designed to scale from a simple desktop crawler to a comprehensive SEO analysis platform without requiring a rewrite.
