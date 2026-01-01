# Incremental Crawling Implementation

## Status: Phase 2 Implemented (2025-12-31)

- **Phase 1 Complete:** URL tracking fixed and validated - no duplicate URLs across sessions
- **Phase 2 Complete:** Run grouping system implemented - `IncrementalCrawlRun` table groups related crawls, "paused" state moved to run level

---

## What Exists Today

### Backend Components

| Component | Files | Status |
|-----------|-------|--------|
| Database Models | `internal/store/models.go`, `store.go` | Implemented |
| Queue Operations | `internal/store/queue.go` | Implemented |
| Crawl State Mgmt | `internal/store/crawls.go` | Implemented |
| Config Fields | `internal/store/config.go` | Implemented |
| App Layer | `internal/app/crawler.go` | Implemented |
| Desktop Adapter | `cmd/desktop/adapter.go` | Implemented |

### Frontend Components

| Component | Files | Status |
|-----------|-------|--------|
| Config Panel | `Config.tsx`, `Config.css` | Implemented |
| Main App | `App.tsx`, `App.css` | Implemented |
| Wails Bindings | `wailsjs/go/main/DesktopApp.d.ts` | Implemented |

### Database Schema

**CrawlQueueItem** - Persistent URL queue:
```go
type CrawlQueueItem struct {
    ID        uint
    ProjectID uint    // Links to Project
    URL       string
    URLHash   int64   // Hash for efficient lookup (int64 for SQLite)
    Source    string  // "initial", "spider", "sitemap"
    Depth     int
    Visited   bool    // true = crawled, false = pending
    CreatedAt int64
    UpdatedAt int64
}
```

**Crawl.State** - Crawl lifecycle states:
- `in_progress` - Currently crawling
- `paused` - Stopped at budget limit, can resume
- `completed` - Finished all URLs
- `failed` - Error or user-stopped

**Config Fields**:
- `IncrementalCrawlingEnabled` (bool) - Enable/disable feature
- `CrawlBudget` (int) - Max URLs per session (0 = unlimited)

### API Methods

```go
ResumeCrawl(projectID uint) (*ProjectInfo, error)
GetQueueStatus(projectID uint) (*QueueStatus, error)
ClearCrawlQueue(projectID uint) error
UpdateIncrementalConfigForDomain(url string, enabled bool, budget int) error
```

---

## The Problem

### Observed Behavior

When comparing 3 sessions of 100 URLs (incremental) vs 1 session of 300 URLs (single):
- Two single 300-crawls: **299/300 URLs in common** (99.7% match)
- Incremental (100x3) vs Single (300): **Only 102 URLs in common** (~34% match)

### Root Cause Analysis

The issue is that **not all crawled URLs are being tracked in the queue**. The current implementation uses `MarkURLVisitedByHash()` which does `UPDATE WHERE hash = ?`. If the URL isn't already in the queue, nothing happens.

**URLs that may NOT be in the queue before being crawled:**
1. Sitemap URLs (discovered from XML, not spider)
2. Redirect destinations (discovered during HTTP request)
3. Initial URL (may be crawled before being queued)
4. Resources (JS, CSS, images, fonts)

**Result:** On resume, many previously-crawled URLs are re-crawled because their hashes weren't in the queue.

### Validation Test (2025-12-30)

**Test Setup:**
- Site: www.amahahealth.com
- Budget: 50 URLs
- Incremental crawling enabled

**Results:**
```
discovered_urls: 50 total, 50 visited (correct - hit budget)
crawl_queue_items: 84 total, 5 visited (WRONG - should be 50)
```

**Queue Breakdown:**
```
source    | total | visited
----------|-------|--------
initial   |     1 |       0  <- Initial URL NOT marked visited
spider    |    79 |       1  <- Spider URLs NOT being updated
resource  |     0 |       0  <- NO resource entries at all!
```

**Content Types Crawled:**
```
text/html                 |  5 URLs  <- Only 5 HTML pages
application/javascript    | 22 URLs
image/*                   | 10 URLs
font/*                    |  8 URLs
text/css                  |  3 URLs
text/xml (sitemap)        |  1 URL
```

45 out of 50 URLs were resources (JS, CSS, images, fonts), but NONE were tracked in the queue.

---

## Implementation Plan

### Phase 1: Fix URL Tracking in Queue - COMPLETED & VALIDATED (2025-12-31)

**Problem:** `MarkURLVisitedByHash()` only updates existing entries. URLs not in queue are silently ignored.

**Solution:** Created `AddAndMarkVisited()` function that uses upsert. See `internal/store/queue.go`.

**Changes made:**
- Added `AddAndMarkVisited()` to `internal/store/queue.go`
- Removed `MarkURLVisitedByHash()` (was buggy, replaced entirely)
- Updated all 4 callback locations in `internal/app/crawler.go` to use `AddAndMarkVisited()`:
  1. `runCrawler()` → `SetOnResourceVisit` (fresh crawl, resources)
  2. `runCrawler()` → `SetOnPageCrawled` (fresh crawl, pages)
  3. `setupCrawlerCallbacks()` → `SetOnResourceVisit` (resume, resources)
  4. `setupCrawlerCallbacks()` → `SetOnPageCrawled` (resume, pages)
- Added unit tests in `internal/store/queue_test.go`

**Validation Results (2025-12-31):**

*Test 1: Basic functionality (amahahealth.com, budget=30)*
```
Session 1: 30 URLs | Session 2 (resume): 16 NEW URLs | Total: 46

| Test                          | Expected | Actual | Status |
|-------------------------------|----------|--------|--------|
| Queue visited = Discovered    | 30       | 30     | PASS   |
| URL leakage (missing)         | 0        | 0      | PASS   |
| Resources tracked             | Yes      | Yes    | PASS   |
| Duplicates between sessions   | 0        | 0      | PASS   |
```

*Test 2: Determinism with Parallelism=1 (Wikipedia)*
```
Single crawl (60 URLs):      61 URLs visited
Incremental (20×3 URLs):     21 + 20 + 20 = 61 URLs visited

| Test                          | Expected | Actual | Status |
|-------------------------------|----------|--------|--------|
| URL sets match                | 100%     | 100%   | PASS   |
| Duplicates between sessions   | 0        | 0      | PASS   |

✓ PERFECT MATCH - Both crawls visited exactly the same URLs!
```

*Test 3: Parallelism > 1 produces different URL sets (expected)*
```
With Parallelism=5, two independent 300-URL crawls had 59% overlap.
This is expected - concurrent workers cause non-deterministic crawl order.
Within each incremental run, no duplicates occurred (correct behavior).
```

### Phase 2: Incremental Crawl Run Grouping (Ready for Implementation)

**Problem Summary:**
1. No way to group related incremental crawls
2. "paused" state on individual crawls is confusing
3. Queue is project-scoped but unclear which run it belongs to

**Current behavior:**
```
Project 1
├── Crawl 1 (fresh, paused)      ─┐
├── Crawl 2 (resume, paused)      ├── No explicit link between these
├── Crawl 3 (resume, completed)  ─┘
├── Crawl 4 (fresh, paused)      ─┐
└── Crawl 5 (resume, completed)  ─┘

crawl_queue_items (project_id=1) <- Ambiguous: which run does it belong to?
```

---

## Design Decision: New Table vs Junction Table

### Option A: Junction Table (User's Original Proposal)
```go
type IncrementalRunMember struct {
    RunID   uint
    CrawlID uint
}
```
- Requires extra table and join queries
- More complex to manage

### Option B: Direct Foreign Key (Recommended)
```go
type Crawl struct {
    RunID *uint  // nullable FK to IncrementalCrawlRun
}
```
- Simpler schema
- Direct relationship
- Backwards compatible (null for non-incremental crawls)

**Recommendation:** Option B - simpler and achieves the same goal.

---

## New Schema

### New Table: IncrementalCrawlRun

```go
// IncrementalCrawlRun groups multiple crawls in a single incremental run
type IncrementalCrawlRun struct {
    ID        uint     `gorm:"primaryKey"`
    ProjectID uint     `gorm:"not null;index"`
    State     string   `gorm:"not null;default:'in_progress'"` // in_progress, paused, completed
    Project   *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
    Crawls    []Crawl  `gorm:"foreignKey:RunID"`
    CreatedAt int64    `gorm:"autoCreateTime"`
    UpdatedAt int64    `gorm:"autoUpdateTime"`
}

// Run state constants
const (
    RunStateInProgress = "in_progress"  // A crawl is currently running
    RunStatePaused     = "paused"       // Budget hit, more URLs to crawl
    RunStateCompleted  = "completed"    // All URLs crawled or manually completed
)
```

### Modified Crawl Table

```go
type Crawl struct {
    ID             uint                 `gorm:"primaryKey"`
    ProjectID      uint                 `gorm:"not null;index"`
    RunID          *uint                `gorm:"index"`  // nullable FK to IncrementalCrawlRun
    CrawlDateTime  int64                `gorm:"not null"`
    CrawlDuration  int64                `gorm:"not null"`
    PagesCrawled   int                  `gorm:"not null"`
    State          string               `gorm:"not null;default:'completed'"` // REMOVED 'paused'
    DiscoveredUrls []DiscoveredUrl      `gorm:"foreignKey:CrawlID;constraint:OnDelete:CASCADE"`
    Run            *IncrementalCrawlRun `gorm:"foreignKey:RunID"`
    CreatedAt      int64                `gorm:"autoCreateTime"`
    UpdatedAt      int64                `gorm:"autoUpdateTime"`
}

// Crawl state constants (REMOVED CrawlStatePaused)
const (
    CrawlStateInProgress = "in_progress"  // Currently crawling
    CrawlStateCompleted  = "completed"    // Done (hit budget OR all URLs)
    CrawlStateFailed     = "failed"       // Error or user-stopped
)
```

### CrawlQueueItem (Unchanged)

Queue remains project-scoped. Behavior changes:
- **New run:** Clear queue entirely (fresh start)
- **Resume:** Use existing queue with visited state preserved

---

## State Machine

### Run States
```
                 ┌─────────────────────────────────────────┐
                 │                                         │
                 ▼                                         │
    ┌────────────────────┐    budget hit     ┌────────────────────┐
    │    in_progress     │ ─────────────────▶│      paused        │
    └────────────────────┘                   └────────────────────┘
                 │                                         │
                 │ all URLs crawled                        │ user resumes
                 │                                         │
                 ▼                                         │
    ┌────────────────────┐                                 │
    │     completed      │ ◀───────────────────────────────┘
    └────────────────────┘   (when all URLs done on resume)
```

### Crawl States (Simplified)
```
    ┌────────────────────┐
    │    in_progress     │
    └────────────────────┘
                 │
        ┌───────┴────────┐
        │                │
        ▼                ▼
    ┌────────┐      ┌────────┐
    │completed│      │ failed │
    └────────┘      └────────┘
```

---

## Flow Changes

### StartCrawl (Incremental Enabled)

```go
func (a *App) StartCrawl(urlStr string) (*types.ProjectInfo, error) {
    // ... existing normalization ...

    if config.IncrementalCrawlingEnabled {
        // Check for paused run
        pausedRun, err := a.store.GetPausedRun(projectID)
        if err != nil {
            return nil, err
        }

        if pausedRun != nil {
            // Auto-resume: mark run as in_progress, create crawl under it
            if err := a.store.UpdateRunState(pausedRun.ID, RunStateInProgress); err != nil {
                return nil, err
            }
            crawl, err := a.store.CreateCrawlWithRun(projectID, pausedRun.ID)
            if err != nil {
                return nil, err
            }
            go a.runCrawlerWithResume(...)
            return &types.ProjectInfo{...}
        }

        // New run: clear queue, create run + crawl
        if err := a.store.ClearQueue(projectID); err != nil {
            return nil, err
        }
        run, err := a.store.CreateIncrementalRun(projectID)
        if err != nil {
            return nil, err
        }
        crawl, err := a.store.CreateCrawlWithRun(projectID, run.ID)
        if err != nil {
            return nil, err
        }
        go a.runCrawler(...)
        return &types.ProjectInfo{...}
    }

    // Non-incremental: create crawl without run
    crawl, err := a.store.CreateCrawl(projectID, ...)
    // ... existing logic ...
}
```

### ResumeCrawl

```go
func (a *App) ResumeCrawl(projectID uint) (*types.ProjectInfo, error) {
    // Find paused run
    pausedRun, err := a.store.GetPausedRun(projectID)
    if err != nil || pausedRun == nil {
        return nil, fmt.Errorf("no paused run to resume")
    }

    // Check for pending URLs
    hasPending, err := a.store.HasPendingURLs(projectID)
    if !hasPending {
        return nil, fmt.Errorf("no pending URLs to crawl")
    }

    // Mark run as in_progress
    if err := a.store.UpdateRunState(pausedRun.ID, RunStateInProgress); err != nil {
        return nil, err
    }

    // Create new crawl under this run
    crawl, err := a.store.CreateCrawlWithRun(projectID, pausedRun.ID)
    if err != nil {
        return nil, err
    }

    go a.runCrawlerWithResume(...)
    return &types.ProjectInfo{...}
}
```

### OnCrawlPaused Callback

```go
crawler.SetOnCrawlPaused(func(urlsVisited int, pendingURLs []bluesnake.URLDiscoveryRequest) {
    // Save pending URLs to queue
    // ... existing logic ...

    // Mark CRAWL as completed (not paused!)
    a.store.UpdateCrawlStatsAndState(crawlID, duration, pages, CrawlStateCompleted)

    // Mark RUN as paused (budget hit)
    if runID != nil {
        a.store.UpdateRunState(*runID, RunStatePaused)
    }
})
```

### OnCrawlComplete Callback

```go
crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
    // Mark crawl as completed or failed
    state := CrawlStateCompleted
    if wasStopped {
        state = CrawlStateFailed
    }
    a.store.UpdateCrawlStatsAndState(crawlID, duration, pages, state)

    // For incremental runs, determine run state
    if runID != nil && !wasStopped {
        hasPending, _ := a.store.HasPendingURLs(projectID)
        if hasPending {
            // Budget hit - OnCrawlPaused already set run to paused
            // (this case shouldn't happen - complete is called after pause)
        } else {
            // All URLs visited - run is complete
            a.store.UpdateRunState(*runID, RunStateCompleted)
        }
    }
})
```

---

## New Store Functions

```go
// CreateIncrementalRun creates a new incremental crawl run
func (s *Store) CreateIncrementalRun(projectID uint) (*IncrementalCrawlRun, error) {
    run := IncrementalCrawlRun{
        ProjectID: projectID,
        State:     RunStateInProgress,
    }
    if err := s.db.Create(&run).Error; err != nil {
        return nil, fmt.Errorf("failed to create run: %v", err)
    }
    return &run, nil
}

// GetPausedRun returns the paused run for a project, if any
func (s *Store) GetPausedRun(projectID uint) (*IncrementalCrawlRun, error) {
    var run IncrementalCrawlRun
    result := s.db.Where("project_id = ? AND state = ?", projectID, RunStatePaused).
        Order("created_at DESC").First(&run)
    if result.Error == gorm.ErrRecordNotFound {
        return nil, nil
    }
    if result.Error != nil {
        return nil, result.Error
    }
    return &run, nil
}

// GetInProgressRun returns the in-progress run for a project, if any
func (s *Store) GetInProgressRun(projectID uint) (*IncrementalCrawlRun, error) {
    var run IncrementalCrawlRun
    result := s.db.Where("project_id = ? AND state = ?", projectID, RunStateInProgress).First(&run)
    if result.Error == gorm.ErrRecordNotFound {
        return nil, nil
    }
    if result.Error != nil {
        return nil, result.Error
    }
    return &run, nil
}

// UpdateRunState updates the state of a run
func (s *Store) UpdateRunState(runID uint, state string) error {
    return s.db.Model(&IncrementalCrawlRun{}).Where("id = ?", runID).Update("state", state).Error
}

// CreateCrawlWithRun creates a crawl associated with a run
func (s *Store) CreateCrawlWithRun(projectID uint, runID uint) (*Crawl, error) {
    crawl := Crawl{
        ProjectID:     projectID,
        RunID:         &runID,
        CrawlDateTime: time.Now().Unix(),
        State:         CrawlStateInProgress,
    }
    if err := s.db.Create(&crawl).Error; err != nil {
        return nil, fmt.Errorf("failed to create crawl: %v", err)
    }
    return &crawl, nil
}

// GetRunCrawls returns all crawls for a run
func (s *Store) GetRunCrawls(runID uint) ([]Crawl, error) {
    var crawls []Crawl
    result := s.db.Where("run_id = ?", runID).Order("crawl_date_time ASC").Find(&crawls)
    if result.Error != nil {
        return nil, result.Error
    }
    return crawls, nil
}

// GetRunWithCrawls returns a run with all its crawls preloaded
func (s *Store) GetRunWithCrawls(runID uint) (*IncrementalCrawlRun, error) {
    var run IncrementalCrawlRun
    result := s.db.Preload("Crawls").First(&run, runID)
    if result.Error != nil {
        return nil, result.Error
    }
    return &run, nil
}
```

---

## Migration Plan

### Step 1: Add New Table and Column

```go
// In store.go AutoMigrate
db.AutoMigrate(
    &IncrementalCrawlRun{},  // New table
    &Crawl{},                 // Add RunID column
    // ... existing tables
)
```

### Step 2: Data Migration (for existing paused crawls)

```go
// Migrate existing paused crawls to runs
func (s *Store) MigratePausedCrawlsToRuns() error {
    // Find all paused crawls without a run_id
    var pausedCrawls []Crawl
    s.db.Where("state = ? AND run_id IS NULL", CrawlStatePaused).Find(&pausedCrawls)

    for _, crawl := range pausedCrawls {
        // Create a run for each paused crawl
        run := IncrementalCrawlRun{
            ProjectID: crawl.ProjectID,
            State:     RunStatePaused,
        }
        s.db.Create(&run)

        // Update crawl to point to run and mark as completed
        s.db.Model(&crawl).Updates(map[string]interface{}{
            "run_id": run.ID,
            "state":  CrawlStateCompleted,
        })
    }
    return nil
}
```

### Step 3: Update Code References

Replace all uses of:
- `CrawlStatePaused` → `CrawlStateCompleted` (for individual crawls)
- `GetLastPausedCrawl()` → `GetPausedRun()`
- `HasPausedCrawl()` → check for paused run instead

---

## Files to Modify

| File | Changes |
|------|---------|
| `internal/store/models.go` | Add `IncrementalCrawlRun` model, add `RunID` to `Crawl`, remove `CrawlStatePaused` |
| `internal/store/store.go` | Add migration for new table |
| `internal/store/crawls.go` | Add run-related functions, update CreateCrawl |
| `internal/store/queue.go` | No changes (queue stays project-scoped) |
| `internal/app/crawler.go` | Update StartCrawl, ResumeCrawl, all callbacks |
| `internal/types/types.go` | Add `RunInfo` type, update `QueueStatus` |
| `cmd/desktop/adapter.go` | Update to expose run info |
| `frontend/.../App.tsx` | Update UI to show run grouping (optional) |

---

## API Changes

### GetQueueStatus Response Update

```go
type QueueStatus struct {
    ProjectID   uint
    HasQueue    bool
    Visited     int
    Pending     int
    Total       int
    CanResume   bool
    // NEW fields:
    CurrentRunID *uint  // nil if no active run
    RunState     string // "", "in_progress", "paused", "completed"
    RunCrawlIDs  []uint // All crawl IDs in current run
}
```

### New Endpoint: GetRunInfo

```
GET /api/v1/run/{runID}

Response:
{
    "id": 1,
    "projectID": 1,
    "state": "paused",
    "crawls": [
        {"id": 1, "state": "completed", "pagesCrawled": 50},
        {"id": 2, "state": "completed", "pagesCrawled": 50}
    ],
    "totalPagesCrawled": 100,
    "createdAt": 1735689600
}
```

---

## Benefits of This Design

1. **Clear separation:** Crawl = single session, Run = logical grouping
2. **Individual crawls always complete:** No stuck "paused" states
3. **Easy queries:** "Get all crawls for this run" = simple FK query
4. **Backwards compatible:** Existing non-incremental crawls have RunID = null
5. **Queue stays simple:** Project-scoped, cleared on new run
6. **UI-friendly:** Can show run progress with grouped crawls

---

## After Implementation

```
Project 1
├── Run 1 (state=completed)
│   ├── Crawl 1 (state=completed, pages=50)
│   ├── Crawl 2 (state=completed, pages=50)
│   └── Crawl 3 (state=completed, pages=47)
│
├── Run 2 (state=paused)
│   ├── Crawl 4 (state=completed, pages=50)
│   └── Crawl 5 (state=completed, pages=50)
│
└── Run 3 (state=in_progress)
    └── Crawl 6 (state=in_progress, pages=23)

crawl_queue_items (project_id=1) <- Belongs to active/paused run
```

---

## Architectural Context

### Two-Level State Tracking

The system has two storage systems that must stay synchronized:

**CrawlerStore (In-Memory, Ephemeral):**
- Tracks visited URLs via hash during a single crawl session
- Uses `VisitIfNotVisited(hash)` for atomic deduplication
- Destroyed when crawl completes or pauses
- Must be restored on resume from persistent storage

**crawl_queue_items (SQLite, Persistent):**
- Persistence layer for CrawlerStore's visited state
- Tracks URLs at PROJECT level (not crawl level)
- Contains both visited (done) and unvisited (pending) URLs
- Cleared on fresh crawl start (new incremental run)

### State Synchronization Flow

```
Crawler marks URL visited in CrawlerStore (ephemeral)
                    │
                    ▼
Crawler fires callback (OnPageCrawled / OnResourceVisit)
                    │
                    ▼
App layer receives callback
  ├── SaveDiscoveredUrl() → discovered_urls (crawl_id)
  └── AddAndMarkVisited() → crawl_queue_items (project_id)  <-- THE FIX
```

### Resume Flow

```
1. User clicks "Resume"
   │
2. App creates NEW Crawl record (new crawl_id)
   │
3. App loads state from queue:
   │  ├── GetVisitedURLHashes(projectID) → pre-visited hashes
   │  └── GetPendingURLs(projectID) → seed URLs
   │
4. App creates Crawler with:
   │  ├── PreVisitedHashes → pre-populate CrawlerStore
   │  └── SeedURLs → URLs to crawl first
   │
5. Crawler runs, callbacks fire:
   │  ├── SaveDiscoveredUrl(crawl_id) → new session's results
   │  └── AddAndMarkVisited(project_id) → update queue
   │
6. On pause/complete:
      ├── Pending URLs saved to queue (visited=false)
      └── Crawl state updated (paused/completed)
```

---

## Testing Plan

### Unit Tests

1. **AddAndMarkVisited function**:
   - Creates new entry when URL doesn't exist
   - Updates existing entry when URL exists
   - Sets visited=true in both cases

2. **Queue state after crawl**:
   - All crawled URLs appear in queue with visited=true
   - Both HTML pages and resources are tracked
   - Initial URL is marked visited

### Integration Tests

1. **Single crawl with budget**:
   - Start crawl with budget=50
   - Verify queue has 50 visited URLs
   - Verify queue visited count matches discovered_urls count

2. **Resume consistency**:
   - Start crawl with budget=50, pause
   - Resume with budget=50, pause
   - Resume with budget=50, complete
   - Compare URL set with single crawl of budget=150
   - Should have high overlap (>95%)

### Validation Queries

```sql
-- Check for URL leakage (URLs not in queue)
SELECT COUNT(*) as missing
FROM discovered_urls d
WHERE d.crawl_id = ?
AND d.visited = 1
AND NOT EXISTS (
    SELECT 1 FROM crawl_queue_items q
    WHERE q.project_id = ? AND q.url = d.url AND q.visited = 1
);

-- Should return 0

-- Check for duplicate crawls across sessions
SELECT url, COUNT(DISTINCT crawl_id) as sessions
FROM discovered_urls
WHERE visited = 1
GROUP BY url
HAVING sessions > 1;

-- Should return 0 rows if working correctly
```

---

## Files Modified

### Phase 1 (Completed)

| File | Changes |
|------|---------|
| `internal/store/queue.go` | Added `AddAndMarkVisited()`, removed `MarkURLVisitedByHash()` |
| `internal/store/queue_test.go` | Added unit tests for `AddAndMarkVisited()` |
| `internal/store/crawls_test.go` | Added baseline tests for crawl state management |
| `internal/app/crawler.go` | Updated 4 callbacks to use `AddAndMarkVisited()`, fixed state bugs |

### Phase 2 (Ready for Implementation)

| File | Changes | Priority |
|------|---------|----------|
| `internal/store/models.go` | Add `IncrementalCrawlRun` model, add `RunID` to `Crawl`, remove `CrawlStatePaused` | 1 |
| `internal/store/store.go` | Add AutoMigrate for new table, add migration for existing paused crawls | 2 |
| `internal/store/crawls.go` | Add run functions: `CreateIncrementalRun`, `GetPausedRun`, `UpdateRunState`, `CreateCrawlWithRun`, `GetRunCrawls` | 3 |
| `internal/app/crawler.go` | Update `StartCrawl`, `ResumeCrawl`, callbacks to use run state | 4 |
| `internal/types/types.go` | Add `RunInfo` type, update `QueueStatus` with run fields | 5 |
| `cmd/desktop/adapter.go` | Expose run info to frontend | 6 |
| `cmd/http/handlers.go` | Add `/api/v1/run/{runID}` endpoint (optional) | 7 |
| `frontend/.../App.tsx` | UI updates for run grouping (optional) | 8 |

---

## Test Commands

```bash
# Configure incremental crawling
curl -X PUT http://localhost:8080/api/v1/config \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","incrementalCrawlingEnabled":true,"crawlBudget":50}'

# Start crawl
curl -X POST http://localhost:8080/api/v1/crawl \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# Resume crawl
curl -X POST http://localhost:8080/api/v1/resume-crawl/1

# Check queue stats
sqlite3 ~/.bluesnake/bluesnake.db \
  "SELECT visited, COUNT(*) FROM crawl_queue_items WHERE project_id=1 GROUP BY visited;"

# Check source breakdown
sqlite3 ~/.bluesnake/bluesnake.db \
  "SELECT source, visited, COUNT(*) FROM crawl_queue_items WHERE project_id=1 GROUP BY source, visited;"

# Check for URL leakage
sqlite3 ~/.bluesnake/bluesnake.db "
  SELECT COUNT(*) as missing
  FROM discovered_urls d
  WHERE d.crawl_id = 1 AND d.visited = 1
  AND NOT EXISTS (
    SELECT 1 FROM crawl_queue_items q
    WHERE q.project_id = 1 AND q.url = d.url AND q.visited = 1
  );
"
```

---

## Historical Notes

### Bugs Fixed

1. **uint64 SQLite issue** (fixed)
   - SQLite doesn't support unsigned 64-bit integers with high bit set
   - Changed `URLHash` from `uint64` to `int64` in `CrawlQueueItem` model
   - Updated all callers to cast `bluesnake.URLHash()` to `int64`

2. **URLs not tracked in queue** (fixed 2025-12-31)
   - `MarkURLVisitedByHash()` did `UPDATE WHERE hash = ?` - silent no-op if URL not in queue
   - Replaced with `AddAndMarkVisited()` which uses upsert to always create/update
   - Removed `MarkURLVisitedByHash()` entirely

3. **HTTP API endpoints** (fixed)
   - Added `incrementalCrawlingEnabled` and `crawlBudget` fields to PUT `/api/v1/config`
   - Added POST `/api/v1/resume-crawl/{projectID}` endpoint

4. **Resources not tracked in fresh crawl** (fixed 2025-12-31)
   - The `SetOnResourceVisit` callback in `runCrawler()` was missing `AddAndMarkVisited()`
   - Only pages were tracked, not resources (JS, CSS, images, fonts)
   - Added the missing call to track all resource URLs

5. **SQLite "too many SQL variables" error** (fixed 2025-12-31)
   - `AddToQueue()` was inserting all URLs in a single statement
   - SQLite has a limit of ~999 variables per statement
   - Fixed by batching inserts into chunks of 100 items

6. **Crawl state stuck at in_progress after resume** (fixed 2025-12-31)
   - `ResumeCrawl()` created new crawls without updating the old paused crawl's state
   - Old crawls stayed "paused" while new ones were "in_progress"
   - Fixed by marking old paused crawl as "completed" before creating resume crawl

7. **Crawl state stuck at in_progress when finishing before budget** (fixed 2025-12-31)
   - When incremental crawling was enabled but site had fewer URLs than budget
   - Crawl finished but state stayed "in_progress" instead of "completed"
   - Code assumed pause callback always ran, but it only runs when budget is hit
   - Fixed by checking if budget was actually reached in `SetOnCrawlComplete` callback

---

## Known Issues

### Non-Deterministic Crawl Order (with Parallelism > 1)

**Observed:** Two independent crawls of the same site produce different URL sets (~59% overlap for Wikipedia with 300 URLs and Parallelism=5).

**Cause:** The crawler uses concurrent workers (`Parallelism` setting, default 5). Multiple workers fetch URLs in parallel, and completion order depends on network timing. This affects link discovery order.

**Impact:**
- Two separate crawl runs may visit different URLs
- Within a single incremental run, no duplicates occur (this is correct)

**Workaround:** Set `Parallelism: 1` for deterministic crawls.
- **Validated:** With Parallelism=1, single 60 crawl vs 20×3 incremental produced **100% identical URL sets**

**Not a bug:** This is expected behavior for concurrent crawlers. The incremental crawling fix ensures no duplicates *within* a run, not between independent runs.

### Crawl State Management Bug - FIXED (2025-12-31)

**Observed:** During validation testing, multiple crawls showed `in_progress` state simultaneously:
```
Crawl 1: state=paused, pages=1
Crawl 2: state=in_progress, pages=0
Crawl 3: state=in_progress, pages=0
```

**Root cause:** In `ResumeCrawl()`, when creating a new crawl to resume, the old paused crawl was never updated. It stayed "paused" forever while new crawls were created as "in_progress".

**Fix:** Added code in `ResumeCrawl()` to mark the old paused crawl as "completed" before creating the new resume crawl:
```go
// Mark the old paused crawl as completed (it's being superseded by this resume)
if err := a.store.UpdateCrawlState(pausedCrawl.ID, store.CrawlStateCompleted); err != nil {
    log.Printf("Failed to mark previous crawl as completed: %v", err)
}
```

**File:** `internal/app/crawler.go` line ~727
