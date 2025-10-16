# Refactoring Proposal: Collector/Crawler Separation

## Current Problem

The `Crawler` currently accesses `Collector` internals in ways that break encapsulation. This makes the code harder to understand and maintain.

## Remaining Issues to Fix

### Issue #1: Direct Field Access (Breaks Encapsulation)

**Problem:** Crawler accesses Collector's private fields directly

| Field Access | Location | Current Usage | Issue |
|--------------|----------|---------------|-------|
| `cr.Collector.Context` | crawler.go:307, 339 | Check context cancellation | Should use method |
| `cr.Collector.store` | crawler.go:374 | Call `VisitIfNotVisited()` | Should use method |
| `cr.Collector.backend.Client` | crawler.go:249 | Get HTTP client for sitemap fetching | Should use method |
| `cr.Collector.wg` | (via Wait) | Wait for HTTP completion | Internal implementation detail |

**Example of bad pattern:**
```go
// crawler.go:315, 345
case <-cr.Collector.Context.Done():  // ❌ Direct field access

// crawler.go:381
alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)  // ❌ Direct field access

// crawler.go:252
client := cr.Collector.backend.Client  // ❌ Direct field access to nested struct
```

**Why this is bad:**
- Breaks encapsulation
- Creates tight coupling between Crawler and Collector internals
- Makes it hard to refactor Collector's internal structure
- Exposes implementation details that should be hidden

### Issue #2: Exposed Internal Method

**Problem:** `scrape()` should be private but is called by Crawler

```go
// crawler.go:408
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)  // ❌ Internal method
```

**Why this is bad:**
- `scrape()` is an internal implementation detail
- Name doesn't clearly indicate it's meant for Crawler use only
- Should be renamed to something more intentional (e.g., `fetchURL`)
- Ideally would be unexported, but Crawler needs access

## Proposed Solution

### Design Principle

**Collector** = Low-level HTTP engine (fetch, parse, callbacks)
**Crawler** = High-level orchestration (discovery, filtering, queueing)

### Phase 1: Add Accessor Methods (Fix Issue #1)

**Goal:** Stop direct field access by providing proper accessor methods.

**Add to Collector:**
```go
// Accessor methods for Crawler
func (c *Collector) GetContext() context.Context {
    return c.Context
}

func (c *Collector) GetHTTPClient() *http.Client {
    return c.backend.Client
}

func (c *Collector) MarkVisited(hash uint64) (bool, error) {
    return c.store.VisitIfNotVisited(hash)
}
```

**Update Crawler:**
```go
// Before (❌ Direct field access)
case <-cr.Collector.Context.Done():
alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)
client := cr.Collector.backend.Client

// After (✅ Use accessor methods)
case <-cr.Collector.GetContext().Done():
alreadyVisited, err := cr.Collector.MarkVisited(uHash)
client := cr.Collector.GetHTTPClient()
```

### Phase 2: Rename scrape() Method (Fix Issue #2)

**Goal:** Make it clear that this method is intentionally exposed for Crawler use.

**Option A: Rename to fetchURL (Recommended)**
```go
// Before
func (c *Collector) scrape(u, method string, depth int, requestData io.Reader,
                           ctx *Context, hdr http.Header, checkRevisit bool) error

// After
func (c *Collector) FetchURL(u, method string, depth int, requestData io.Reader,
                              ctx *Context, hdr http.Header, checkRevisit bool) error
```

**Option B: Keep scrape() but add comment**
```go
// scrape is intentionally exported for use by Crawler.
// It performs HTTP fetch + parsing + callbacks synchronously.
func (c *Collector) scrape(...) error
```

**Update Crawler:**
```go
// Before
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)

// After (if renamed)
cr.Collector.FetchURL(req.URL, "GET", req.Depth, nil, req.Context, nil, false)
```

## Benefits of Proposed Refactoring

### 1. Better Encapsulation

```go
// Before (❌ Bad)
cr.Collector.Context.Done()           // Exposes internal field
cr.Collector.store.VisitIfNotVisited() // Exposes internal field
cr.Collector.backend.Client.Get()      // Exposes internal structure

// After (✅ Good)
cr.Collector.GetContext().Done()       // Clean API
cr.Collector.MarkVisited(hash)         // Clean API
cr.Collector.GetHTTPClient().Get()     // Clean API
```

### 2. Clearer Intent

```go
// Before (❌ Ambiguous)
cr.Collector.scrape(...)  // Is this internal? Can I call it?

// After (✅ Clear)
cr.Collector.FetchURL(...)  // Clearly intentional API for Crawler
```

### 3. Easier to Refactor

With accessor methods, we can change Collector's internal structure without breaking Crawler:
- Can rename `Context` → `ctx` (make private) without affecting Crawler
- Can change storage implementation without Crawler knowing
- Can refactor `backend` structure independently

## Implementation Plan

### ✅ Already Completed

- **Visit tracking moved to Crawler** - Eliminated race conditions
- **Collector made synchronous** - Cleaner concurrency model
- **URL filtering moved to Crawler** - Single source of truth for filtering
- **Filter configuration moved to Crawler** - No more duplication

### Phase 1: Add Accessor Methods (Non-breaking) - ~3.5 hours

1. Add `GetContext()` to Collector (30 min)
2. Add `GetHTTPClient()` to Collector (30 min)
3. Add `MarkVisited()` to Collector (30 min)
4. Update Crawler to use new accessors (1 hr)
   - Replace `cr.Collector.Context` with `cr.Collector.GetContext()`
   - Replace `cr.Collector.store.VisitIfNotVisited()` with `cr.Collector.MarkVisited()`
   - Replace `cr.Collector.backend.Client` with `cr.Collector.GetHTTPClient()`
5. Add unit tests for accessors (1 hr)
6. Run integration tests to verify (30 min)

### Phase 2: Rename scrape() Method (Non-breaking) - ~1.5 hours

**Option A: Rename to FetchURL (Recommended)**
1. Rename `scrape()` to `FetchURL()` (15 min)
2. Update all `Collector` methods that call it (15 min)
3. Update Crawler to call `FetchURL()` (15 min)
4. Update tests (30 min)
5. Run full test suite (15 min)

**Option B: Add Documentation Only**
1. Add clear godoc comment explaining `scrape()` is for Crawler use (15 min)
2. No code changes needed

**Total Remaining Effort:** ~5 hours (less than 1 day)

## Testing Strategy

1. ✅ **Filtering tests** - Already moved to Crawler tests
2. **Accessor method tests** - Verify GetContext(), GetHTTPClient(), MarkVisited()
3. **Integration tests** - Verify Crawler still works after changes
4. **Regression tests** - Run existing test suite

## Next Steps

1. ✅ **DECISION:** Option A recommended for Phase 2 (rename to FetchURL)
2. **Start Phase 1:** Add accessor methods (can start today)
3. **Complete Phase 2:** Rename scrape() method (same day or next)
4. **Validate:** Run full test suite

---

# Status Summary (Updated 2025-10-16)

## What's Been Completed ✅

1. **URL Visit Tracking Refactored**
   - Visit checking completely moved to Crawler (single-threaded processor)
   - Eliminated race conditions
   - Added `checkRevisit` parameter to `scrape()`

2. **Collector Made Synchronous**
   - Removed async goroutine spawning from Collector
   - All concurrency now managed by Crawler's worker pool
   - Cleaner separation of concerns

3. **URL Filtering Moved to Crawler** ✅
   - Filter fields (`allowedDomains`, `disallowedDomains`, `urlFilters`, `disallowedURLFilters`) moved to Crawler struct
   - `isURLCrawlable()` now uses Crawler's own filter fields
   - `checkFilters()` removed from `Collector.requestCheck()`
   - Filtering tests moved to `crawler_test.go`
   - **NO MORE DUPLICATION** - each URL filtered only once

## What Remains ❌

1. **Issue #1: Direct Field Access** (3.5 hours)
   - Need accessor methods: `GetContext()`, `GetHTTPClient()`, `MarkVisited()`
   - Replace direct field access in Crawler

2. **Issue #2: Method Naming** (1.5 hours)
   - Rename `scrape()` to `FetchURL()` for clarity
   - Or add clear documentation that it's intentionally exported

**Total Remaining: ~5 hours**

---

**Document Last Updated:** 2025-10-16
**Status:** Configuration duplication fixed (Issue #2 ✅ COMPLETE). Encapsulation issues remain (Issues #1 and #2).
**Next Action:** Implement Phase 1 (Add accessor methods) to fix direct field access.

