# Refactoring Proposal: Collector/Crawler Separation

## Current Problem

The `Crawler` currently accesses `Collector` internals in ways that break encapsulation and create unclear responsibility boundaries. This makes the code harder to understand and maintain.

## Issues Identified

### 1. Direct Field Access (Breaks Encapsulation)

**Problem:** Crawler accesses Collector's private fields directly

| Field Access | Location | Current Usage | Issue |
|--------------|----------|---------------|-------|
| `cr.Collector.Context` | crawler.go:307, 339 | Check context cancellation | Should use method |
| `cr.Collector.store` | crawler.go:374 | Call `VisitIfNotVisited()` | Should use method |
| `cr.Collector.backend.Client` | crawler.go:249 | Get HTTP client for sitemap fetching | Should use method |
| `cr.Collector.wg` | (via Wait) | Wait for HTTP completion | Internal implementation detail |

**Example of bad pattern:**
```go
// crawler.go:307
case <-cr.Collector.Context.Done():  // ❌ Direct field access

// crawler.go:374
alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)  // ❌ Direct field access
```

### 2. Configuration Duplication

**Problem:** URL filtering configuration exists in both Collector and Crawler, causing duplication

| Configuration Field | Used In | Purpose |
|---------------------|---------|---------|
| `AllowedDomains` | Collector.requestCheck(), Crawler.isURLCrawlable() | Domain whitelist |
| `DisallowedDomains` | Collector.requestCheck(), Crawler.isURLCrawlable() | Domain blacklist |
| `URLFilters` | Collector.requestCheck(), Crawler.isURLCrawlable() | URL whitelist patterns |
| `DisallowedURLFilters` | Collector.requestCheck(), Crawler.isURLCrawlable() | URL blacklist patterns |
| `FollowInternalNofollow` | Crawler only | Nofollow link handling |
| `ResourceValidation` | Crawler only | Resource validation config |

#### Detailed Duplication Analysis

**A. Domain Filtering - Duplicated in 2 locations:**

1. **Collector.isDomainAllowed()** (bluesnake.go:1117-1125)
```go
func (c *Collector) isDomainAllowed(domain string) bool {
    for _, d2 := range c.DisallowedDomains {
        if d2 == domain {
            return false
        }
    }
    if c.AllowedDomains == nil || len(c.AllowedDomains) == 0 {
        return true
    }
    for _, d2 := range c.AllowedDomains {
        if d2 == domain {
            return true
        }
    }
    return false
}
```

2. **Crawler.isURLCrawlable()** (crawler.go:903-920)
```go
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
    // ... parsing code ...

    // Check domain blocklist
    if len(cr.Collector.DisallowedDomains) > 0 {
        for _, d := range cr.Collector.DisallowedDomains {
            if d == hostname {
                return false
            }
        }
    }

    // Check domain allowlist
    if len(cr.Collector.AllowedDomains) > 0 {
        allowed := false
        for _, d := range cr.Collector.AllowedDomains {
            if d == hostname {
                allowed = true
                break
            }
        }
        if !allowed {
            return false
        }
    }
}
```

**B. URL Pattern Filtering - Duplicated in 2 locations:**

1. **Collector.checkFilters()** (bluesnake.go:1100-1115)
```go
func (c *Collector) checkFilters(URL, domain string) error {
    if len(c.DisallowedURLFilters) > 0 {
        if isMatchingFilter(c.DisallowedURLFilters, []byte(URL)) {
            return ErrForbiddenURL
        }
    }
    if len(c.URLFilters) > 0 {
        if !isMatchingFilter(c.URLFilters, []byte(URL)) {
            return ErrNoURLFiltersMatch
        }
    }
    if !c.isDomainAllowed(domain) {
        return ErrForbiddenDomain
    }
    return nil
}
```

Called from **Collector.requestCheck()** (bluesnake.go:1067-1098):
```go
func (c *Collector) requestCheck(u string, parsedURL *url.URL, depth int) error {
    // ... depth checks ...
    if err := c.checkFilters(u, parsedURL.Hostname()); err != nil {
        return err
    }
    // ...
}
```

2. **Crawler.isURLCrawlable()** (crawler.go:928-947)
```go
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
    // ... domain checks above ...

    urlBytes := []byte(urlStr)

    // Check URL pattern blocklist
    if len(cr.Collector.DisallowedURLFilters) > 0 {
        for _, filter := range cr.Collector.DisallowedURLFilters {
            if filter.Match(urlBytes) {
                return false
            }
        }
    }

    // Check URL pattern allowlist
    if len(cr.Collector.URLFilters) > 0 {
        matched := false
        for _, filter := range cr.Collector.URLFilters {
            if filter.Match(urlBytes) {
                matched = true
                break
            }
        }
        if !matched {
            return false
        }
    }

    return true
}
```

**C. Robots.txt Handling - Only in Collector**

**Collector.checkRobots()** (bluesnake.go:1127-1176)
```go
func (c *Collector) checkRobots(parsedURL *url.URL) error {
    if c.IgnoreRobotsTxt {
        return nil
    }
    // ... robots.txt checking logic ...
}
```

Called from **Collector.requestCheck()** (bluesnake.go:1089-1092):
```go
if err := c.checkRobots(parsedURL); err != nil {
    return err
}
```

**Note:** Crawler does NOT check robots.txt before queueing URLs. This means:
- URLs may be queued that will later be rejected by Collector due to robots.txt
- Wasted work discovering and processing URLs that can't be crawled

**D. Configuration Fields Accessed by Crawler**

From Collector configuration, Crawler directly accesses:

| Field | Location in Crawler | Purpose |
|-------|---------------------|---------|
| `AllowedDomains` | crawler.go:909 | Domain whitelist check |
| `DisallowedDomains` | crawler.go:903 | Domain blacklist check |
| `URLFilters` | crawler.go:938 | URL pattern whitelist |
| `DisallowedURLFilters` | crawler.go:928 | URL pattern blacklist |
| `FollowInternalNofollow` | crawler.go:599 | Whether to follow nofollow links |
| `ResourceValidation` | crawler.go:631 | Validate URLs against resource types |

**E. Summary of Duplication**

| Feature | Collector Implementation | Crawler Implementation | Result |
|---------|--------------------------|------------------------|---------|
| Domain Blocklist | `isDomainAllowed()` checks `DisallowedDomains` | `isURLCrawlable()` checks `DisallowedDomains` | **Duplicate** |
| Domain Allowlist | `isDomainAllowed()` checks `AllowedDomains` | `isURLCrawlable()` checks `AllowedDomains` | **Duplicate** |
| URL Pattern Blocklist | `checkFilters()` checks `DisallowedURLFilters` | `isURLCrawlable()` checks `DisallowedURLFilters` | **Duplicate** |
| URL Pattern Allowlist | `checkFilters()` checks `URLFilters` | `isURLCrawlable()` checks `URLFilters` | **Duplicate** |
| Robots.txt | `checkRobots()` validates | Not checked | **Missing in Crawler** |
| Nofollow Links | Not applicable | `FollowInternalNofollow` config | **Crawler only** |
| Resource Validation | Not applicable | `ResourceValidation` config | **Crawler only** |

**Impact:**
- **Code duplication:** Same filtering logic implemented twice (100+ lines duplicated)
- **Maintenance burden:** Changes to filtering require updates in 2 places
- **Inconsistency risk:** Easy for implementations to diverge
- **Performance:** URLs filtered twice (once in Crawler, once in Collector)
- **Confusion:** Unclear which layer is responsible for what

### 3. Exposed Internal Method

**Problem:** `scrape()` should be private but is called by Crawler

```go
// crawler.go:400
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)  // ❌ Internal method
```

This method contains low-level HTTP details and WaitGroup management that should be hidden from Crawler.

### 4. Unclear Responsibility

**Current state:** Both Collector and Crawler do URL filtering

```
URL Discovery → Crawler filters → Processor → Crawler filters again →
Collector filters → HTTP fetch
```

This creates:
- **Redundant checks** - Same filters applied multiple times
- **Confusion** - Which layer is responsible for what?
- **Tight coupling** - Crawler needs to know Collector's filtering logic

## Proposed Solution

### Design Principle

**Collector** = Low-level HTTP engine (fetch, parse, callbacks)
**Crawler** = High-level orchestration (discovery, filtering, queueing)

### Option 1: Minimal Collector API (Recommended)

#### Goal
Make Collector a black box with a minimal, clean API. All orchestration logic stays in Crawler.

#### Changes to Collector

```go
// Collector - Low-level HTTP engine
type Collector struct {
    // All fields become PRIVATE (unexported)
    context context.Context  // was: Context
    store   storage.Storage  // was: store
    backend *httpBackend     // was: backend
    wg      *sync.WaitGroup  // was: wg

    // Configuration for HTTP-level decisions only
    maxDepth               int
    maxBodySize            int
    detectCharset          bool
    parseHTTPErrorResponse bool
    enableRendering        bool
    renderingConfig        *RenderingConfig

    // Callback registrations remain public
    htmlCallbacks     []*htmlCallbackContainer
    responseCallbacks []ResponseCallback
    errorCallbacks    []ErrorCallback
    scrapedCallbacks  []ScrapedCallback
}

// PUBLIC API - Only these methods exposed:

// Callback registration
func (c *Collector) OnHTML(selector string, f HTMLCallback)
func (c *Collector) OnResponse(f ResponseCallback)
func (c *Collector) OnError(f ErrorCallback)
func (c *Collector) OnScraped(f ScrapedCallback)

// HTTP client configuration
func (c *Collector) SetClient(client *http.Client)
func (c *Collector) WithTransport(transport http.RoundTripper)
func (c *Collector) SetRequestTimeout(timeout time.Duration)

// Accessor methods for Crawler
func (c *Collector) GetContext() context.Context
func (c *Collector) GetHTTPClient() *http.Client
func (c *Collector) MarkVisited(urlHash uint64) (alreadyVisited bool, err error)
func (c *Collector) Wait()
func (c *Collector) IsCancelled() bool

// INTERNAL method - only called by Crawler
// Renamed from scrape() to make it clear this is internal
func (c *Collector) fetchURL(url, method string, depth int, requestData io.Reader,
                             ctx *Context, headers http.Header) error {
    // Does NOT check visited status - Crawler handles that
    // Does NOT check domain filters - Crawler handles that
    // ONLY does HTTP fetch + parsing + callbacks
}
```

#### Changes to Crawler

```go
type Crawler struct {
    Collector *Collector

    // URL filtering configuration (moved from Collector)
    allowedDomains         []string
    disallowedDomains      []string
    urlFilters             []*regexp.Regexp
    disallowedURLFilters   []*regexp.Regexp

    // Crawler-specific configuration
    followInternalNofollow bool
    resourceValidation     *ResourceValidationConfig
    discoveryMechanisms    []DiscoveryMechanism

    // Rest remains the same...
}

// Use accessor methods instead of direct field access
func (cr *Crawler) queueURL(req URLDiscoveryRequest) {
    select {
    case cr.discoveryChannel <- req:
        // ...
    case <-cr.Collector.GetContext().Done():  // ✅ Use method
        // ...
    }
}

func (cr *Crawler) processDiscoveredURL(req URLDiscoveryRequest) {
    // Use accessor method instead of direct field access
    alreadyVisited, err := cr.Collector.MarkVisited(uHash)  // ✅ Use method

    // Use renamed internal method
    err = cr.workerPool.Submit(func() {
        cr.Collector.fetchURL(req.URL, "GET", req.Depth, nil, req.Context, nil)  // ✅ Clearer name
    })
}

// Crawler owns ALL filtering logic
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
    // Use cr.allowedDomains instead of cr.Collector.AllowedDomains
    if len(cr.allowedDomains) > 0 {
        // ... filtering logic
    }
}
```

#### Migration Path

1. **Add accessor methods to Collector:**
   ```go
   func (c *Collector) GetContext() context.Context { return c.context }
   func (c *Collector) GetHTTPClient() *http.Client { return c.backend.Client }
   func (c *Collector) MarkVisited(hash uint64) (bool, error) {
       return c.store.VisitIfNotVisited(hash)
   }
   ```

2. **Update Crawler to use accessors:**
   ```go
   // Before
   case <-cr.Collector.Context.Done():

   // After
   case <-cr.Collector.GetContext().Done():
   ```

3. **Move configuration from Collector to Crawler:**
   ```go
   // Move these fields from CollectorConfig to CrawlerConfig
   AllowedDomains, DisallowedDomains, URLFilters, DisallowedURLFilters
   ```

4. **Remove filtering from Collector.requestCheck():**
   ```go
   // Collector.requestCheck() should only check:
   // - MaxDepth
   // - MaxRequests
   // NOT domain filters or URL filters (Crawler handles those)
   ```

5. **Rename scrape() → fetchURL():**
   ```go
   func (c *Collector) fetchURL(...) error {
       // Remove visit checking (Crawler handles that)
       // Remove domain filtering (Crawler handles that)
       // ONLY fetch + parse + callbacks
   }
   ```

6. **Make Collector fields private:**
   ```go
   type Collector struct {
       context context.Context  // lowercase = private
       store   storage.Storage  // lowercase = private
       // ...
   }
   ```

### Option 2: Move More Logic to Collector (NOT Recommended)

Keep Collector as the main orchestrator and make Crawler a thin wrapper. This is the opposite direction and goes against our architecture goals.

**Why not recommended:**
- Goes against the separation of concerns
- Collector becomes too complex
- Harder to test in isolation
- Doesn't solve the encapsulation problem

## Benefits of Proposed Refactoring

### 1. Clear Separation of Concerns

```
Crawler (High-level):
  ✓ URL discovery (spider, sitemap, network)
  ✓ URL filtering (domains, patterns)
  ✓ Visit tracking (VisitIfNotVisited)
  ✓ Work distribution (worker pool)
  ✓ Link graph building

Collector (Low-level):
  ✓ HTTP requests
  ✓ HTML/XML parsing
  ✓ Callback execution
  ✓ Content rendering (chromedp)
  ✓ Cache management
```

### 2. Better Encapsulation

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

### 3. Easier to Understand

**Current:** "Wait, who does filtering? Both? Why is it duplicated?"

**After:** "Crawler filters everything, Collector just fetches what Crawler tells it to."

### 4. Easier to Test

```go
// Mock Collector with minimal interface
type MockCollector struct {
    fetchCalled bool
    visitedURLs map[uint64]bool
}

func (m *MockCollector) MarkVisited(hash uint64) (bool, error) {
    alreadyVisited := m.visitedURLs[hash]
    m.visitedURLs[hash] = true
    return alreadyVisited, nil
}

func (m *MockCollector) fetchURL(...) error {
    m.fetchCalled = true
    return nil
}
```

### 5. Reduced Duplication

```go
// Before: Filtering in 2 places
// Collector.requestCheck() + Crawler.isURLCrawlable()

// After: Filtering in 1 place
// Crawler.isURLCrawlable() only
```

## Implementation Plan

### Phase 1: Add Accessor Methods (Non-breaking)
- Add `GetContext()`, `GetHTTPClient()`, `MarkVisited()` to Collector
- Keep existing field access working
- Update Crawler to use new methods

### Phase 2: Move Configuration (Breaking)
- Move filtering config from `CollectorConfig` to `CrawlerConfig`
- Update `NewCrawler()` to handle both configs
- Remove filtering from `Collector.requestCheck()`

### Phase 3: Rename Internal Methods (Breaking)
- Rename `scrape()` → `fetchURL()`
- Remove visit checking from `fetchURL()`
- Update Crawler to call new method

### Phase 4: Make Fields Private (Breaking)
- Lowercase all Collector internal fields
- Force all access through methods
- Update any external code that was accessing fields

## Testing Strategy

1. **Add tests for new accessor methods**
2. **Verify Crawler still works with accessor methods**
3. **Add integration tests for filtering at Crawler level**
4. **Remove filtering tests from Collector (move to Crawler)**
5. **Verify no regressions in existing crawls**

## Questions to Answer

1. **Should we keep `Collector` exported at all?**
   - Current: `Crawler.Collector` is exported for "advanced configuration"
   - Proposal: Keep it exported but with minimal public methods
   - Alternative: Make it private, expose only what's needed through Crawler

2. **How to handle configuration migration?**
   - Option A: Breaking change (move config fields)
   - Option B: Deprecation period (support both locations)
   - Option C: Config adapter that maps old → new

3. **What about backwards compatibility?**
   - Users might be calling `collector.OnHTML()` directly
   - Should we support this or force them to go through Crawler?

## Decision Required

**Approve this refactoring?** If yes, should we:
- [ ] Start with Phase 1 (accessors) immediately
- [ ] Plan for breaking changes in next major version
- [ ] Create deprecation warnings for direct field access
- [ ] Write migration guide for users

## Related Issues

This refactoring addresses the "keeping reader's head sane" goal by:
- Reducing cognitive load (fewer ways to do the same thing)
- Clear responsibilities (Crawler = what to crawl, Collector = how to fetch)
- Better encapsulation (can't accidentally break internals)
- Easier to explain to new contributors

---

# FEASIBILITY ASSESSMENT (Added 2025-10-16)

## Executive Summary

**VERDICT: The filtering duplication is COMPLETELY REDUNDANT and provides ZERO value.**

Moving all filtering to Crawler is:
- ✅ **Technically feasible** - No architectural blockers
- ✅ **Straightforward** - 2-3 days of work (17 hours estimated)
- ✅ **Low risk** - No production code uses Collector directly
- ⚠️ **One decision needed** - Backward compatibility strategy

---

## EVIDENCE: Collector Is Not Used Directly

### Codebase Analysis Results

**Production Code:**
- ❌ Zero calls to `collector.Visit()` in production code
- ✅ All production code uses `NewCrawler()` → `Crawler.Start()`
- ✅ Crawler is documented as the public API in ARCHITECTURE.md

**Test Code:**
- ✅ All tests either use `NewCrawler()` or test internal Collector behavior
- ✅ No external/integration tests rely on `collector.Visit()`

**Documentation:**
- ✅ ARCHITECTURE.md shows `Crawler` as the entry point
- ✅ No examples show direct `Collector.Visit()` usage
- ✅ Code comments refer to Crawler as "high-level interface"

**Conclusion:** Making this a breaking change affects **ZERO existing code paths**.

---

## EXECUTION FLOW ANALYSIS

### Current Flow (With Duplication)

```
User Code
  ↓
Crawler.Start(url)
  ↓
Crawler.queueURL(url)                    [Queues URL to discovery channel]
  ↓
Crawler.processDiscoveredURL(url)        [Single-threaded processor]
  ↓
Crawler.isURLCrawlable(url)             ← ✅ FILTERS: domains, URL patterns
  ├─ Check DisallowedDomains            ← ✅ First check
  ├─ Check AllowedDomains               ← ✅ First check
  ├─ Check DisallowedURLFilters         ← ✅ First check
  └─ Check URLFilters                   ← ✅ First check
  ↓
store.VisitIfNotVisited(hash)            [Marks visited atomically]
  ↓
Crawler submits to worker pool
  ↓
Collector.scrape(url)
  ↓
Collector.requestCheck(url)             ← ❌ FILTERS AGAIN (redundant)
  ├─ checkFilters(url)
  │  ├─ DisallowedURLFilters            ← ❌ Duplicate check
  │  ├─ URLFilters                      ← ❌ Duplicate check
  │  └─ isDomainAllowed()               ← ❌ Duplicate check
  │     ├─ DisallowedDomains            ← ❌ Duplicate check
  │     └─ AllowedDomains               ← ❌ Duplicate check
  └─ checkRobots(url)                   ← ⚠️ ONLY unique check
  ↓
Collector.fetch(url)                     [HTTP GET]
  ↓
Collector.handleOnHTML/OnResponse        [Callbacks]
```

### Proposed Flow (No Duplication)

```
User Code
  ↓
Crawler.Start(url)
  ↓
Crawler.queueURL(url)
  ↓
Crawler.processDiscoveredURL(url)
  ↓
Crawler.isURLCrawlable(url)             ← ✅ ALL FILTERS HERE
  ├─ Check DisallowedDomains            ← ✅ Only check (moved from Collector)
  ├─ Check AllowedDomains               ← ✅ Only check (moved from Collector)
  ├─ Check DisallowedURLFilters         ← ✅ Only check (moved from Collector)
  ├─ Check URLFilters                   ← ✅ Only check (moved from Collector)
  └─ Check robots.txt                   ← ✅ Moved from Collector
  ↓
store.VisitIfNotVisited(hash)
  ↓
Crawler submits to worker pool
  ↓
Collector.scrape(url)
  ↓
Collector.requestCheck(url)             ← ✅ Only rate limits now
  ├─ Check MaxDepth                     ← ✅ Legitimate rate limit
  └─ Check MaxRequests                  ← ✅ Legitimate rate limit
  ↓
Collector.fetch(url)                     [HTTP GET - no filtering]
  ↓
Collector.handleOnHTML/OnResponse        [Callbacks]
```

**Improvement:**
- **50% fewer filter checks** - each URL checked once instead of twice
- **Clear responsibility** - Crawler owns ALL filtering decisions
- **Better performance** - reduces regex matching overhead

---

## VALUE ANALYSIS: Does Collector Filtering Provide Value?

### Check-by-Check Breakdown

| Check Type | Location | Current Value | After Refactoring |
|------------|----------|---------------|-------------------|
| **DisallowedDomains** | Collector.isDomainAllowed() | ❌ **REDUNDANT** - Already checked by Crawler | ✅ Removed - only in Crawler |
| **AllowedDomains** | Collector.isDomainAllowed() | ❌ **REDUNDANT** - Already checked by Crawler | ✅ Removed - only in Crawler |
| **DisallowedURLFilters** | Collector.checkFilters() | ❌ **REDUNDANT** - Already checked by Crawler | ✅ Removed - only in Crawler |
| **URLFilters** | Collector.checkFilters() | ❌ **REDUNDANT** - Already checked by Crawler | ✅ Removed - only in Crawler |
| **robots.txt** | Collector.checkRobots() | ⚠️ **UNIQUE** - NOT in Crawler (BUG) | ✅ Moved to Crawler |
| **MaxDepth** | Collector.requestCheck() | ✅ **VALID** - Rate limiting concern | ✅ Kept in Collector |
| **MaxRequests** | Collector.requestCheck() | ✅ **VALID** - Rate limiting concern | ✅ Kept in Collector |

### Why MaxDepth and MaxRequests Stay in Collector

These are **not filtering** - they are **resource limits**:
- `MaxDepth` prevents infinite recursion (safety mechanism)
- `MaxRequests` prevents runaway crawls (resource protection)

They belong in Collector because they protect against:
1. Bugs in Crawler logic
2. Misconfiguration by users
3. Defense-in-depth for HTTP resource usage

**Analogy:** Like having both a circuit breaker AND a fuse in an electrical system.

---

## TRANSITION DIFFICULTY ASSESSMENT

### Detailed Task Breakdown with Time Estimates

| Phase | Task | Difficulty | Time | Blocker? | Notes |
|-------|------|-----------|------|----------|-------|
| **Phase 1: Preparation (Non-Breaking)** | | | | | |
| 1.1 | Add `GetContext()` to Collector | Easy | 30 min | No | Simple accessor method |
| 1.2 | Add `GetHTTPClient()` to Collector | Easy | 30 min | No | Returns `c.backend.Client` |
| 1.3 | Add `MarkVisited()` to Collector | Easy | 30 min | No | Wraps `c.store.VisitIfNotVisited()` |
| 1.4 | Update Crawler to use accessors | Easy | 1 hr | No | Replace 3-4 direct field accesses |
| 1.5 | Add unit tests for accessors | Easy | 1 hr | No | Test each accessor method |
| | **Phase 1 Subtotal** | | **3.5 hrs** | | |
| **Phase 2: Move Configuration (Non-Breaking)** | | | | | |
| 2.1 | Add filter fields to Crawler struct | Easy | 30 min | No | 4 new fields |
| 2.2 | Update NewCrawler() initialization | Easy | 1 hr | No | Copy from CollectorConfig |
| 2.3 | Update isURLCrawlable() references | Easy | 1 hr | No | Change `cr.Collector.X` → `cr.X` |
| 2.4 | Test with both locations populated | Easy | 30 min | No | Verify no behavior change |
| | **Phase 2 Subtotal** | | **3 hrs** | | |
| **Phase 3: Move robots.txt (Moderate)** | | | | | |
| 3.1 | Add robotsMap to Crawler | Easy | 30 min | No | `map[string]*robotstxt.RobotsData` |
| 3.2 | Copy checkRobots() to Crawler | Moderate | 2 hrs | No | Need HTTP client access |
| 3.3 | Add robots check to isURLCrawlable() | Easy | 30 min | No | Call `cr.checkRobots()` |
| 3.4 | Handle thread safety | Moderate | 1 hr | No | Processor is single-threaded, but HTTP fetches are concurrent |
| 3.5 | Test robots.txt checking | Easy | 1 hr | No | Verify blocking works |
| | **Phase 3 Subtotal** | | **5 hrs** | | |
| **Phase 4: Remove Redundancy (BREAKING)** | | | | | |
| 4.1 | Remove checkFilters() calls | Easy | 30 min | No | Delete from requestCheck() |
| 4.2 | Remove checkRobots() calls | Easy | 30 min | No | Delete from requestCheck() |
| 4.3 | Remove filter fields from Collector | Easy | 30 min | No | Delete 4 fields |
| 4.4 | Update all tests | Moderate | 2 hrs | No | Fix tests that relied on Collector filtering |
| 4.5 | Run integration test suite | Easy | 30 min | No | Verify no regressions |
| 4.6 | **DECISION: Backward compatibility** | N/A | 1 hr | **YES** | See options below |
| | **Phase 4 Subtotal** | | **5 hrs** | | |
| **Testing & Documentation** | | | | | |
| 5.1 | Write migration guide | Easy | 30 min | No | If needed (depends on decision) |
| 5.2 | Update ARCHITECTURE.md | Easy | 30 min | No | Document new flow |
| | **Phase 5 Subtotal** | | **1 hr** | | |
| | **TOTAL ESTIMATED EFFORT** | | **17.5 hrs** | | **2-3 days** |

---

## BACKWARD COMPATIBILITY ANALYSIS

### Current Usage Patterns

Based on codebase analysis:

1. **Production Code:**
   - ✅ Uses `NewCrawler()` exclusively
   - ✅ Never calls `collector.Visit()` directly
   - ✅ Only accesses `crawler.Collector` for callbacks (e.g., `OnHTML`)

2. **Test Code:**
   - ✅ Tests use `NewCollector()` to test Collector internals
   - ✅ No tests rely on Collector filtering behavior externally

3. **Potential External Users:**
   - ⚠️ Unknown - if package is public, external code might call `collector.Visit()`
   - ⚠️ Unknown - external code might access `crawler.Collector.AllowedDomains`

### Three Options for Backward Compatibility

#### Option A: Clean Breaking Change (RECOMMENDED)

**Approach:**
- Make Collector private (lowercase `collector`)
- Remove filtering fields from Collector entirely
- Only expose via Crawler

**Example:**
```go
// Before (public)
type Collector struct {
    AllowedDomains []string  // Exported
}

// After (private)
type collector struct {  // Lowercase = unexported
    // No filtering fields
}
```

**Pros:**
- ✅ Clean architecture
- ✅ Forces best practices (use Crawler)
- ✅ Prevents confusion
- ✅ Simplest implementation

**Cons:**
- ❌ Breaking change
- ❌ Affects any external code using Collector directly (if any exists)

**Migration for External Users:**
```go
// Before (if someone was doing this - unlikely)
c := bluesnake.NewCollector(&bluesnake.CollectorConfig{
    AllowedDomains: []string{"example.com"},
})
c.Visit("https://example.com")

// After (migration path)
crawler := bluesnake.NewCrawler(&bluesnake.CollectorConfig{
    AllowedDomains: []string{"example.com"},
})
crawler.Start("https://example.com")
crawler.Wait()
```

**Recommendation:** ✅ **Use this option** - evidence shows zero production usage of Collector directly.

---

#### Option B: Deprecation Period

**Approach:**
- Keep Collector public
- Add deprecation warnings to `collector.Visit()`
- Keep filtering fields but mark as deprecated
- Support both for 1-2 versions

**Example:**
```go
// Collector keeps filtering for now
type Collector struct {
    // Deprecated: Use Crawler instead. This field will be removed in v2.0.
    AllowedDomains []string
}

func (c *Collector) Visit(URL string) error {
    log.Println("DEPRECATED: Collector.Visit() is deprecated. Use Crawler.Start() instead.")
    // Still works, but warns
    return c.scrape(URL, "GET", 1, nil, nil, nil, true)
}
```

**Pros:**
- ✅ Safer migration path
- ✅ Gives external users time to migrate
- ✅ Non-breaking initially

**Cons:**
- ❌ Keeps ugly code around
- ❌ Maintains duplication temporarily
- ❌ More complex implementation
- ❌ Still requires breaking change eventually

**Recommendation:** ⚠️ Use only if external usage is confirmed (which evidence suggests it's not).

---

#### Option C: Keep Minimal Collector Filtering

**Approach:**
- Keep *some* filtering in Collector as a safety net
- Removes duplication but keeps basic protection

**Example:**
```go
// Crawler does full filtering
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
    // All filters here
}

// Collector keeps minimal safety checks
func (c *Collector) requestCheck(...) error {
    // Only check if URL is obviously invalid (e.g., empty)
    if parsedURL.Host == "" {
        return ErrInvalidURL
    }
    // No domain/pattern filtering
}
```

**Pros:**
- ✅ Most backward compatible
- ✅ Safety net for misconfiguration

**Cons:**
- ❌ Still has some duplication
- ❌ Defeats the purpose
- ❌ Unclear responsibility boundary

**Recommendation:** ❌ Don't use this - it compromises the architecture without clear benefit.

---

## UPDATED RECOMMENDATIONS

### Final Verdict

**Question: Is it possible to move ALL filtering to Crawler?**
**Answer: ✅ YES - Not only possible, but RECOMMENDED.**

**Question: How hard is the transition?**
**Answer: ✅ EASY to MODERATE - 17 hours (2-3 days), no technical blockers.**

**Question: What's the risk?**
**Answer: ✅ LOW - Zero production code uses Collector directly.**

### Recommended Approach

**Phase 1-3 (Non-Breaking): Start Immediately**
1. Add accessor methods (3.5 hrs)
2. Move configuration to Crawler (3 hrs)
3. Move robots.txt checking to Crawler (5 hrs)
4. Deploy and validate in production

**Phase 4 (Breaking): Schedule for Next Major Version**
5. Remove filtering from Collector (5 hrs)
6. **Choose Option A (Clean Breaking Change)**
7. Write migration guide (1 hr)
8. Bump version to v2.0

### Why Option A (Breaking Change) is Best

**Evidence:**
1. ✅ Zero production calls to `collector.Visit()`
2. ✅ Zero tests rely on external Collector usage
3. ✅ Documentation shows Crawler as public API
4. ✅ Codebase consistently uses `NewCrawler()`

**Impact:**
- Affects **0** known code paths
- If external users exist (unproven), migration is trivial:
  - Change `NewCollector()` → `NewCrawler()`
  - Change `collector.Visit()` → `crawler.Start()`

**Benefit:**
- Clean architecture
- No confusion about which API to use
- Forces best practices
- Eliminates 100+ lines of duplicate code

### Next Steps

1. **Decision Required:** Approve Option A (Clean Breaking Change) ✅
2. **Start Phase 1:** Add accessor methods (can start today)
3. **Continue Phase 2-3:** Move config and robots.txt (this week)
4. **Plan Phase 4:** Schedule breaking changes for next major release

### Success Metrics

After refactoring:
- ✅ Zero duplicate filtering logic
- ✅ Clear separation: Crawler = what to crawl, Collector = how to fetch
- ✅ 50% fewer filter checks per URL
- ✅ Single source of truth for filtering decisions
- ✅ Easier to test and maintain

---

## Questions and Answers

**Q: What if someone is using Collector directly?**
**A:** Evidence shows this is not happening. If it is, migration is trivial (see Option A migration example above).

**Q: Will this break existing tests?**
**A:** Internal tests will need updates (Phase 4.4). External tests using Crawler will be unaffected.

**Q: What about MaxDepth and MaxRequests?**
**A:** These stay in Collector - they are resource limits, not filtering. See "Value Analysis" section.

**Q: Do we need to check robots.txt in both places?**
**A:** No - only in Crawler. Checking in both places wastes HTTP requests and is redundant.

**Q: How do we handle the robotsMap cache?**
**A:** Move it to Crawler. Crawler's processor is single-threaded, but HTTP fetches are concurrent, so we need a mutex around the cache.

**Q: What about callback registration (OnHTML, OnResponse)?**
**A:** These stay on Collector - they are part of the "how to parse" responsibility, not "what to crawl".

---

## Implementation Checklist

### Phase 1: Preparation (Non-Breaking)
- [ ] Add `GetContext()` accessor to Collector
- [ ] Add `GetHTTPClient()` accessor to Collector
- [ ] Add `MarkVisited()` accessor to Collector
- [ ] Update Crawler to use new accessors
- [ ] Add unit tests for accessors
- [ ] Deploy and validate

### Phase 2: Move Configuration (Non-Breaking)
- [ ] Add filter fields to Crawler struct
- [ ] Update `NewCrawler()` to initialize filters
- [ ] Update `isURLCrawlable()` to use `cr.allowedDomains` etc.
- [ ] Test with both Collector and Crawler having filters
- [ ] Deploy and validate

### Phase 3: Move robots.txt (Non-Breaking)
- [ ] Add `robotsMap` field to Crawler
- [ ] Copy `checkRobots()` logic to Crawler
- [ ] Add robots.txt check to `isURLCrawlable()`
- [ ] Handle thread safety (mutex around cache)
- [ ] Test robots.txt blocking
- [ ] Deploy and validate

### Phase 4: Remove Redundancy (BREAKING)
- [ ] **DECISION:** Confirm Option A (Clean Breaking Change)
- [ ] Remove `checkFilters()` from `Collector.requestCheck()`
- [ ] Remove `checkRobots()` from `Collector.requestCheck()`
- [ ] Remove filter fields from Collector struct
- [ ] Update all internal tests
- [ ] Run full integration test suite
- [ ] Write migration guide (if needed)
- [ ] Update ARCHITECTURE.md
- [ ] Bump version to v2.0
- [ ] Deploy

---

## Timeline Estimate

**Conservative Estimate (with testing and validation):**
- Week 1: Phase 1-2 (non-breaking preparation)
- Week 2: Phase 3 (robots.txt migration)
- Week 3: Phase 4 (breaking changes + testing)
- **Total: 3 weeks** (including buffer for testing and validation)

**Aggressive Estimate (minimal testing):**
- Days 1-2: Phase 1-3 (non-breaking)
- Day 3: Phase 4 (breaking changes)
- **Total: 3 days** (2-3 person-days of focused work)

---

**Document Updated:** 2025-10-16
**Status:** Ready for implementation
**Recommendation:** Proceed with Option A (Clean Breaking Change)

---

# CODE REVIEW UPDATE (2025-10-16 - Post Implementation Check)

## Executive Summary

**After reviewing the current codebase against this refactoring document, the following has been found:**

### Issues Status:
- ✅ **Issue #1 (Direct Field Access):** STILL PRESENT - No changes made yet
- ✅ **Issue #2 (Configuration Duplication):** STILL PRESENT - No changes made yet
- ✅ **Issue #3 (Exposed Internal Method):** STILL PRESENT - No changes made yet

### MAJOR CHANGES IMPLEMENTED (Not Documented):
- ✅ **URL Revisiting Logic REFACTORED** - Visit checking moved from Collector to Crawler
- ✅ **Collector is Now SYNCHRONOUS** - Async removed, concurrency handled by Crawler's worker pool
- ✅ **scrape() Signature Changed** - Added `checkRevisit bool` parameter

---

## Changes Implemented Since Document Creation

### 1. URL Revisiting Logic - COMPLETED ✅

**What Changed:**
- Visit checking has been **completely removed** from `Collector.requestCheck()` (bluesnake.go:1065-1072)
- Crawler is now solely responsible for visit tracking via single-threaded processor (crawler.go:381)

**Current Implementation:**
```go
// bluesnake.go:1065-1072
func (c *Collector) requestCheck(..., checkRevisit bool) error {
    // Visit checking has been removed from Collector.
    // The Crawler is now responsible for all visit tracking (single-threaded processor).
    // This eliminates race conditions where multiple goroutines could mark the same URL
    // as visited but never actually crawl it.
    //
    // If checkRevisit is true and you need visit checking, you must handle it externally
    // before calling this method (see Crawler.processDiscoveredURL for the proper pattern).
    return nil
}
```

```go
// crawler.go:378-389
func (cr *Crawler) processDiscoveredURL(req URLDiscoveryRequest) {
    // ...
    // Step 3: Check if already visited and mark as visited (ATOMIC)
    // This is the CRITICAL section - only ONE goroutine executes this
    uHash := requestHash(req.URL, nil)
    alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)
    if err != nil {
        cr.wg.Done()
        return
    }
    if alreadyVisited {
        cr.wg.Done()
        return
    }
    // Step 4: URL is now marked as visited - we own it
    // ...
}
```

**Impact:**
- ✅ Eliminates race conditions in visit tracking
- ✅ Single source of truth for visit decisions (Crawler's processor)
- ✅ Aligns with proposed refactoring goals

### 2. Collector Sync Behavior - COMPLETED ✅

**What Changed:**
- Collector's `scrape()` and `fetch()` methods now run **synchronously**
- Removed all internal async goroutine spawning from Collector
- Crawler's worker pool manages all concurrency

**Current Implementation:**
```go
// bluesnake.go:924-926
func (c *Collector) scrape(...) error {
    // Always run synchronously - concurrency is managed by Crawler's worker pool
    return c.fetch(u, method, depth, requestData, ctx, hdr, req)
}

// bluesnake.go:928-1040
func (c *Collector) fetch(...) error {
    // Runs directly without spawning goroutines
    // Crawler's worker pool already handles concurrency
    ...
}
```

```go
// crawler.go:401-410
err = cr.workerPool.Submit(func() {
    defer cr.wg.Done()
    // We already marked this URL as visited.
    // Call scrape() directly with checkRevisit=false.
    cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)
})
```

**Impact:**
- ✅ Clear separation: Collector = HTTP engine, Crawler = concurrency manager
- ✅ Easier to reason about control flow
- ✅ Eliminates nested concurrency complexity

### 3. New `checkRevisit` Parameter - COMPLETED ✅

**What Changed:**
- Added `checkRevisit bool` parameter to `scrape()` and `requestCheck()`
- Allows Crawler to bypass visit checking (since it already handled it)

**Signature Changes:**
```go
// OLD (not in current code, inferred from document):
func (c *Collector) scrape(u, method string, depth int, requestData io.Reader, ctx *Context, hdr http.Header) error

// NEW (bluesnake.go:869):
func (c *Collector) scrape(u, method string, depth int, requestData io.Reader, ctx *Context, hdr http.Header, checkRevisit bool) error

// OLD:
func (c *Collector) requestCheck(parsedURL *url.URL, method string, getBody func() (io.ReadCloser, error), depth int) error

// NEW (bluesnake.go:1042):
func (c *Collector) requestCheck(parsedURL *url.URL, method string, getBody func() (io.ReadCloser, error), depth int, checkRevisit bool) error
```

**Usage:**
```go
// crawler.go:408 - Crawler calls with checkRevisit=false
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)

// bluesnake.go:773,777,793,799,805,815,830 - Public API calls with checkRevisit=true
return c.scrape(URL, "GET", 1, nil, nil, nil, true)
return c.scrape(URL, "HEAD", 1, nil, nil, nil, false)
return c.scrape(URL, "POST", 1, createFormReader(requestData), nil, nil, true)
// ... etc
```

**Impact:**
- ✅ Eliminates duplicate visit checking
- ✅ Crawler controls visit tracking completely
- ✅ Collector respects Crawler's authority

---

## Updated Issue Status

### Issue #1: Direct Field Access - UNCHANGED ❌

**Status:** Still present in code, no changes made

**Current violations (as of 2025-10-16):**

| Field Access | Current Location | Status |
|--------------|------------------|--------|
| `cr.Collector.Context` | crawler.go:315 | ❌ Still present |
| `cr.Collector.Context` | crawler.go:345 | ❌ Still present |
| `cr.Collector.store` | crawler.go:381 | ❌ Still present |
| `cr.Collector.backend.Client` | crawler.go:252 | ❌ Still present |

**Recommendation:** Phase 1 (Add accessor methods) still needs to be implemented.

### Issue #2: Configuration Duplication - UNCHANGED ❌

**Status:** Still present in code, no changes made

**Current duplication (as of 2025-10-16):**

| Configuration | Crawler Usage | Collector Implementation | Status |
|---------------|---------------|--------------------------|--------|
| `AllowedDomains` | crawler.go:910 | bluesnake.go:226, 1092-1100 | ❌ Duplicated |
| `DisallowedDomains` | crawler.go:902 | bluesnake.go:228, 1092-1100 | ❌ Duplicated |
| `URLFilters` | crawler.go:934 | bluesnake.go:241, 1075-1090 | ❌ Duplicated |
| `DisallowedURLFilters` | crawler.go:926 | bluesnake.go:234, 1075-1090 | ❌ Duplicated |

**Example of duplication:**
```go
// crawler.go:890-952 - Crawler checks filters
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
    // Check domain blocklist
    if len(cr.Collector.DisallowedDomains) > 0 {
        for _, d := range cr.Collector.DisallowedDomains {
            if d == hostname { return false }
        }
    }
    // Check URL pattern blocklist
    if len(cr.Collector.DisallowedURLFilters) > 0 {
        for _, filter := range cr.Collector.DisallowedURLFilters {
            if filter.Match(urlBytes) { return false }
        }
    }
    // ... more checks
}

// bluesnake.go:1075-1100 - Collector ALSO checks filters
func (c *Collector) checkFilters(URL, domain string) error {
    if len(c.DisallowedURLFilters) > 0 {
        if isMatchingFilter(c.DisallowedURLFilters, []byte(URL)) {
            return ErrForbiddenURL
        }
    }
    if len(c.URLFilters) > 0 {
        if !isMatchingFilter(c.URLFilters, []byte(URL)) {
            return ErrNoURLFiltersMatch
        }
    }
    if !c.isDomainAllowed(domain) {
        return ErrForbiddenDomain
    }
    return nil
}
```

**Impact:** Every URL is filtered TWICE - once in Crawler, once in Collector.

**Recommendation:** Phase 2-3 (Move configuration to Crawler) still needs to be implemented.

### Issue #3: Exposed Internal Method - UNCHANGED ❌

**Status:** Still present in code, no changes made

**Current usage (as of 2025-10-16):**
```go
// crawler.go:408
cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)
```

**Note:** While the `checkRevisit` parameter was added, `scrape()` is still exported and called by Crawler.

**Recommendation:** Phase 3 (Rename scrape → fetchURL, make private) still needs to be implemented.

---

## What This Means

### Good News ✅
1. **Major architectural improvements already implemented:**
   - Visit tracking centralized in Crawler (eliminates races)
   - Collector is now synchronous (cleaner control flow)
   - Clear concurrency model (worker pool in Crawler)

2. **Progress toward goals:**
   - Single source of truth for visit decisions ✅
   - Crawler owns orchestration logic ✅
   - Collector is lower-level HTTP engine ✅

### Remaining Work ❌
1. **Encapsulation issues still present:**
   - Direct field access still happening (Context, store, backend.Client)
   - Need accessor methods (Phase 1)

2. **Duplication still present:**
   - URL filtering happens in BOTH Collector and Crawler
   - Domain checks duplicated
   - Need to move config to Crawler (Phase 2-3)

3. **API cleanup needed:**
   - scrape() still exposed to Crawler
   - Should be renamed to fetchURL() and made internal (Phase 3)

---

## Updated Implementation Plan

### ✅ COMPLETED (Already in codebase):
- Move visit checking to Crawler (single-threaded processor)
- Remove async from Collector (synchronous fetch)
- Add checkRevisit parameter to scrape()

### ❌ REMAINING (Still needs to be done):

**Phase 1: Add Accessor Methods (3.5 hrs)**
- [ ] Add `GetContext()` to Collector
- [ ] Add `GetHTTPClient()` to Collector
- [ ] Add `MarkVisited()` to Collector
- [ ] Update Crawler to use accessors
- [ ] Add unit tests

**Phase 2: Move Configuration (3 hrs)**
- [ ] Add filter fields to Crawler struct
- [ ] Update NewCrawler() initialization
- [ ] Update isURLCrawlable() references
- [ ] Test with both locations

**Phase 3: Remove Redundancy (5 hrs)**
- [ ] Remove checkFilters() from Collector.requestCheck()
- [ ] Remove filter fields from Collector
- [ ] Update all tests
- [ ] Write migration guide

**Phase 4: Rename Internal Methods (1 hr)**
- [ ] Rename scrape() → fetchURL()
- [ ] Update Crawler calls
- [ ] Update documentation

**Total Remaining Effort:** ~12.5 hours

---

**Document Updated:** 2025-10-16 (Code Review)
**Status:** Partially implemented - visit tracking and sync refactoring complete, encapsulation and duplication work remains
**Recommendation:** Continue with Phase 1 (Accessor Methods)