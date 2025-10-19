# Refactoring Proposal: Collector/Crawler Separation

## Current Problem

The `Crawler` currently accesses `Collector` internals in ways that break encapsulation. This makes the code harder to understand and maintain.

## Remaining Issues to Fix

### Issue #1: Exposed Internal Method

**Problem:** `scrape()` should be private but is called by Crawler

```go
// crawler.go:408
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)  // ❌ Internal method
```

**Why this is bad:**
- `scrape()` is an internal implementation detail
- Name doesn't clearly indicate it's meant for Crawler use only
- Should be renamed to something more intentional (e.g., `FetchURL`)
- Ideally would be unexported, but Crawler needs access

### Issue #2: Crawler Directives on Collector (Layer Violation)

**Problem:** Collector (HTTP client) implements crawler policy logic

| Field | Type | Location | Used By | Issue |
|-------|------|----------|---------|-------|
| `RobotsTxtMode` | string | Collector:277 | `checkRobots()` at collector.go:1052 | Policy, not HTTP |
| `FollowInternalNofollow` | bool | Collector:279 | Not used in Collector | Should be Crawler-only |
| `FollowExternalNofollow` | bool | Collector:281 | Not used in Collector | Should be Crawler-only |
| `RespectMetaRobotsNoindex` | bool | Collector:283 | Not used in Collector | Should be Crawler-only |
| `RespectNoindex` | bool | Collector:285 | Not used in Collector | Should be Crawler-only |
| `ResourceValidation` | *Config | Collector:275 | Not used in Collector | Should be Crawler-only |
| `robotsMap` | map | Collector:290 | `checkRobots()` cache | Should be Crawler-owned |

**Example of layer violation:**
```go
// collector.go:979 - HTTP client checking crawling policy
if method != "HEAD" && !c.IgnoreRobotsTxt {
    if err := c.checkRobots(parsedURL); err != nil {  // ❌ Crawler policy in HTTP client
        return err
    }
}

// collector.go:1052 - HTTP client implementing crawler logic
if c.RobotsTxtMode == "ignore-report" {  // ❌ Crawler directive in HTTP client
    log.Printf("[robots.txt] Would block %s", u.String())
    return nil
}
```

**Why this is bad:**
- **Layer violation**: HTTP client (Collector) should not know about crawling policies
- **Poor separation**: Crawler policies (robots.txt, nofollow) implemented in wrong layer
- **Coupling**: Config exists in both CrawlerConfig (configuration) AND Collector (runtime)
- **Reusability**: Cannot use Collector standalone without crawler-specific config

**Current state (transitional):**
- ✅ Config separated (CrawlerConfig has crawler directives, HTTPConfig doesn't)
- ❌ Implementation NOT separated (Collector still has crawler directive fields + checkRobots logic)
- Comment in code says "should stay on Collector for now" (collector.go:635-640)

**The ideal architecture:**

| Component | Responsibility | Methods |
|-----------|----------------|---------|
| **Collector** | HTTP client only | Fetch URLs, parse HTML, run callbacks |
| **Crawler** | Policy enforcement | Check robots.txt, enforce nofollow/noindex, filter URLs |

## Proposed Solution

### Design Principle

**Collector** = Low-level HTTP engine (fetch, parse, callbacks)
**Crawler** = High-level orchestration (discovery, filtering, queueing)

### Phase 1: Rename scrape() Method (Fix Issue #1)

**Goal:** Make it clear that this method is intentionally exposed for Crawler use.

**Option A: Rename to FetchURL (Recommended)**
```go
// Before
func (c *Collector) scrape(u, method string, depth int, requestData io.Reader,
                          ctx *Context, hdr http.Header) error

// After
func (c *Collector) FetchURL(u, method string, depth int, requestData io.Reader,
                             ctx *Context, hdr http.Header) error
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

### Clearer Intent

```go
// Before (❌ Ambiguous)
cr.Collector.scrape(...)  // Is this internal? Can I call it?

// After (✅ Clear)
cr.Collector.FetchURL(...)  // Clearly intentional API for Crawler
```

## Implementation Plan

### Phase 1: Rename scrape() Method (Non-breaking) - ~1.5 hours

**Option A: Rename to FetchURL (Recommended)**
1. Rename `scrape()` to `FetchURL()` (15 min)
2. Update all `Collector` methods that call it (15 min)
3. Update Crawler to call `FetchURL()` (15 min)
4. Update tests (30 min)
5. Run full test suite (15 min)

**Option B: Add Documentation Only**
1. Add clear godoc comment explaining `scrape()` is for Crawler use (15 min)
2. No code changes needed

### Phase 2: Move Crawler Directives to Crawler (Fix Issue #2) - ~8 hours (BREAKING)

**Goal:** Move robots.txt checking and crawler directives from Collector to Crawler.

**Step 1: Move robots.txt logic to Crawler (4 hrs)**
1. Add `robotsMap` field to Crawler struct (15 min)
2. Move `checkRobots()` method from Collector to Crawler (30 min)
3. Have Crawler check robots.txt in `isURLCrawlable()` BEFORE enqueueing (1 hr)
4. Remove `checkRobots()` call from `Collector.requestCheck()` (15 min)
5. Update Crawler to pass HTTP client to robots.txt checker (30 min)
6. Add unit tests for Crawler.checkRobots() (1 hr)
7. Update integration tests (30 min)

**Step 2: Remove crawler directive fields from Collector (4 hrs)**
1. Remove fields from Collector struct: (30 min)
   - `RobotsTxtMode`, `FollowInternalNofollow`, `FollowExternalNofollow`
   - `RespectMetaRobotsNoindex`, `RespectNoindex`, `ResourceValidation`
   - `robotsMap`
2. Remove initialization in `NewCollector()` (15 min)
3. Remove from `Collector.Clone()` if present (15 min)
4. Update NewCrawler to set directives on Crawler, not Collector (30 min)
5. Update all tests that set crawler directives on Collector (1.5 hrs)
6. Update documentation/comments (30 min)
7. Run full test suite and fix breakages (1 hr)

**Breaking Changes:**
- Direct use of `Collector.checkRobots()` will break (migrate to Crawler)
- Setting crawler directives on Collector will no longer work
- Standalone Collector use won't check robots.txt (need to use Crawler)

**Total Remaining Effort:** ~9.5 hours (1.2 days)
- Phase 1: ~1.5 hours
- Phase 2: ~8 hours (breaking changes)

## Testing Strategy

1. **Integration tests** (Phase 1) - Verify Crawler still works after changes
2. **Robots.txt tests** (Phase 2) - Move/update tests from Collector to Crawler for checkRobots()
3. **Crawler directive tests** (Phase 2) - Verify RobotsTxtMode, nofollow, noindex work from Crawler
4. **Regression tests** - Run existing test suite after each phase

## Next Steps

1. **Phase 1 (Non-breaking):** Rename scrape() to FetchURL (~1.5 hrs) - Option A recommended
2. **Phase 2 (BREAKING - decide if/when):** Move crawler directives to Crawler (~8 hrs)
   - This is a significant architectural improvement but requires breaking changes
   - Should coordinate with any planned major version releases
   - Consider user impact before proceeding

---

# Summary

## Remaining Issues

1. **Issue #1: Method Naming** (~1.5 hours, non-breaking)
   - Rename `scrape()` to `FetchURL()` for clarity

2. **Issue #2: Crawler Directives on Collector** (~8 hours, BREAKING)
   - Move `robotsMap` from Collector to Crawler
   - Move `checkRobots()` logic from Collector to Crawler
   - Remove crawler directive fields from Collector struct
   - Have Crawler check robots.txt BEFORE passing URLs to Collector

---

**Document Last Updated:** 2025-10-19
**Status:** Two remaining issues to address.
