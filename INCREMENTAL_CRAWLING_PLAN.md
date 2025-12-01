# Incremental Crawling Implementation

## Status: In Progress ⚠️ (Resume consistency issue)

## Overview
Incremental crawling allows users to crawl websites in configurable chunks with pause/resume capability. Users set a "crawl budget" (max URLs per session), and the crawler pauses when the limit is reached, saving pending URLs for later resume.

---

## Implementation Summary

### Completed Backend Components

| Component | Files Modified | Status |
|-----------|---------------|--------|
| Database Models | `internal/store/models.go`, `store.go` | ✅ |
| Framework | `crawler.go`, `collector.go`, `storage/crawler_store.go` | ✅ |
| Store Layer | `internal/store/queue.go`, `crawls.go`, `config.go` | ✅ |
| App Layer | `internal/app/crawler.go`, `config.go` | ✅ |
| Types | `internal/types/types.go` | ✅ |
| Desktop Adapter | `cmd/desktop/adapter.go` | ✅ |

### Completed Frontend Components

| Component | Files Modified | Status |
|-----------|---------------|--------|
| Config Panel | `cmd/desktop/frontend/src/Config.tsx`, `Config.css` | ✅ |
| Main App | `cmd/desktop/frontend/src/App.tsx`, `App.css` | ✅ |
| Wails Bindings | `wailsjs/go/main/DesktopApp.d.ts`, `wailsjs/go/models.ts` | ✅ |

### New Database Tables
- `crawl_queue_items` - Persistent URL queue per project

### New Config Fields
- `IncrementalCrawlingEnabled` (bool) - Enable/disable feature
- `CrawlBudget` (int) - Max URLs per session (0 = unlimited)

### New Crawl States
- `in_progress` - Currently crawling
- `paused` - Stopped at budget limit, can resume
- `completed` - Finished all URLs
- `failed` - Error or user-stopped

### New API Methods (exposed via DesktopApp)
```go
ResumeCrawl(projectID uint) (*ProjectInfo, error)
GetQueueStatus(projectID uint) (*QueueStatus, error)
ClearCrawlQueue(projectID uint) error
UpdateIncrementalConfigForDomain(url string, enabled bool, budget int) error
```

### QueueStatus Response
```typescript
interface QueueStatus {
  projectId: number;
  hasQueue: boolean;
  visited: number;    // URLs already crawled
  pending: number;    // URLs waiting to be crawled
  total: number;
  canResume: boolean; // true if paused + pending > 0
  lastCrawlId: number;
  lastState: string;  // "paused", "completed", etc.
}
```

---

## UI Implementation (Completed)

### 1. Config Panel - "Budget" Tab ✅

**Location:** Config → Budget tab

**Features:**
- Toggle: "Enable Incremental Crawling"
- Number input: "URLs per Session" (visible when enabled)
- Queue status display (visited/pending/total counts)
- "Clear Queue" button with confirmation modal
- Info box explaining how incremental crawling works

### 2. Dashboard Header Buttons ✅

**Button States:**
- **Crawling**: Shows "Stop Crawl" button
- **Paused (canResume=true)**: Shows "Resume Crawl" (blue) + "Start Fresh"
- **Completed**: Shows "New Crawl"

### 3. Footer Status Display ✅

**States:**
- **Crawling**: Progress ring + "Crawling..."
- **Paused**: Orange "Paused" badge + "X URLs pending"
- **Completed**: Green "Completed" text

### 4. Queue Status Integration ✅

- Queue status loaded when viewing a project
- Queue status refreshed when crawl completes (to detect paused state)
- Queue status cleared when starting fresh crawl or going home

---

## User Flow Examples

### Flow 1: First-time Incremental Crawl
1. User opens Config → enables "Incremental Crawling" → sets budget to 1000
2. User clicks "Start Crawl"
3. Crawler runs, UI shows progress
4. At 1000 URLs, crawl auto-pauses
5. UI shows: "Paused (1,000 crawled, 4,000 pending)" + "Resume" button

### Flow 2: Resume
1. User returns to project with paused crawl
2. UI shows "Resume" button (primary) and "Start Fresh" (secondary)
3. User clicks "Resume"
4. Crawler continues from where it left off

### Flow 3: Increase Budget Mid-Crawl
1. User has paused crawl (1000 crawled, 4000 pending)
2. User opens Config → changes budget from 1000 to 5000
3. User clicks "Resume"
4. Crawler processes up to 5000 URLs this session

### Flow 4: Disable Incremental
1. User opens Config → disables "Incremental Crawling"
2. System clears queue automatically
3. User clicks "Start Crawl" → full crawl from scratch

---

## Testing Checklist

### Backend (Complete)
- [x] Build succeeds
- [x] All existing tests pass
- [x] Fresh crawl with incremental disabled works
- [x] Fresh crawl with incremental enabled respects budget

### Frontend (Complete)
- [x] Config toggle works
- [x] Budget input validates (positive integer or 0)
- [x] Resume button appears when canResume=true
- [x] Clear queue works with confirmation
- [x] Paused status badge displays in footer
- [x] Build succeeds with all frontend changes

---

## Architecture Notes

### Why Project-Level Queue?
The queue is stored at the project level (not crawl level) so that:
- URLs persist across crawl sessions
- Resume loads from the same queue
- Clearing queue resets all progress

### Why Lock Certain Settings?
When incremental crawling has pending URLs, changing these would invalidate the queue:
- **Subdomains**: Queue might contain URLs that would now be filtered
- **Discovery mechanisms**: Different mechanisms discover different URLs
- **Robots.txt mode**: URLs might now be blocked

### Channel Size Increase
Discovery channel increased from 50k to 500k for incremental crawling to prevent URL loss during pause.

---

## Known Issues & Debugging Session (2025-11-29)

### Issue: Resume produces different URLs than single crawl

**Problem**: When comparing 3 sessions of 100 URLs (incremental) vs 1 session of 300 URLs (single), the URL sets are significantly different:
- Two single 300-crawls: **299/300 URLs in common** (99.7% match)
- Incremental (100×3) vs Single (300): **Only 102 URLs in common** (~34% match)

**Expected**: If crawling is deterministic, incremental should produce same URLs as single crawl.

### Bugs Fixed During Debugging

1. **uint64 SQLite issue** (`internal/store/queue.go`, `internal/store/models.go`)
   - SQLite doesn't support unsigned 64-bit integers with high bit set
   - Changed `URLHash` from `uint64` to `int64` in `CrawlQueueItem` model
   - Updated all callers to cast `bluesnake.URLHash()` to `int64`
   - Updated `GetVisitedURLHashes()` to return `[]int64` and convert to `[]uint64` for bluesnake

2. **Queue not populated during fresh crawl** (`internal/app/crawler.go`)
   - The `SetOnPageCrawled` callback that adds URLs to queue was only in `runCrawlerWithResume()`
   - Added the same queue logic to `runCrawler()` (fresh crawl function) around line 434
   - Now both fresh and resume crawls populate the queue correctly

3. **HTTP API endpoints added** (`internal/server/server.go`)
   - Added `incrementalCrawlingEnabled` and `crawlBudget` fields to PUT `/api/v1/config`
   - Added POST `/api/v1/resume-crawl/{projectID}` endpoint

### Current State

**Working**:
- Fresh crawl respects budget (pauses at limit) ✅
- Queue is populated with discovered URLs during crawl ✅
- Resume endpoint works and loads pending URLs ✅
- Pre-visited hashes are loaded to avoid re-crawling ✅

**Not Working**:
- Resume crawl produces different URL set than expected
- Incremental (100×3) differs significantly from single (300)

### Suspected Root Cause

The issue is likely in how resume handles the crawl:
1. **Parallelism + ordering**: With parallelism=10, URL processing order may differ
2. **Seed URLs vs discovery**: Resume uses pending queue as seeds, but original crawl discovers URLs dynamically
3. **Pre-visited hash mismatch**: The int64↔uint64 conversion might cause hash mismatches

### Next Steps to Debug

1. **Reduce parallelism to 1** and compare incremental vs single crawl
   - If they match, the issue is parallelism-induced ordering differences
   - If they don't match, there's a deeper logic issue

2. **Add logging** to trace:
   - How many URLs are loaded from queue on resume
   - How many pre-visited hashes are loaded
   - Whether URLs are being re-crawled (check if same URL appears in multiple crawl sessions)

3. **Check for URL overlap between sessions**:
   ```sql
   -- Find URLs crawled in multiple sessions
   SELECT url, COUNT(DISTINCT crawl_id) as sessions
   FROM discovered_urls
   GROUP BY url
   HAVING sessions > 1;
   ```

4. **Verify hash consistency**:
   - Print hash values when storing to queue
   - Print hash values when checking pre-visited
   - Ensure int64↔uint64 conversion doesn't cause mismatches

### Test Commands

```bash
# Configure incremental crawling
curl -X PUT http://localhost:8080/api/v1/config \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","incrementalCrawlingEnabled":true,"crawlBudget":100,...}'

# Start crawl
curl -X POST http://localhost:8080/api/v1/crawl \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

# Resume crawl
curl -X POST http://localhost:8080/api/v1/resume-crawl/1

# Check queue stats
sqlite3 ~/.bluesnake/bluesnake.db \
  "SELECT visited, COUNT(*) FROM crawl_queue_items WHERE project_id=1 GROUP BY visited;"

# Check URLs per crawl session
sqlite3 ~/.bluesnake/bluesnake.db \
  "SELECT crawl_id, COUNT(*) FROM discovered_urls GROUP BY crawl_id;"
```
