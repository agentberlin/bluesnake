# Race Condition: Redirect Destination URLs Not Appearing in Crawl Results

## Problem Statement

The crawler produces inconsistent results when crawling the same website multiple times. URLs that are redirect destinations (e.g., `handbook.agentberlin.ai/intro` which redirects from `handbook.agentberlin.ai/`) appear in only 78-99.5% of crawls instead of 100%. For example, crawling `agentberlin.ai` yields between 139-145 total URLs per crawl, with certain URLs missing in some crawls.

**Key characteristic**: The race condition occurs even in sequential (non-parallel) crawls, indicating timing-dependent behavior rather than concurrency issues.

**Note**: This document focuses on one identified root cause (redirect destinations not being reported). There may be additional causes contributing to the race condition that require further investigation.

## Root Cause

Intermediate URLs in redirect chains are **marked as visited** but **never reported to application callbacks**, resulting in them not being stored in the database.

### Current Flow for Redirect Chain A→B→C

1. **OnRedirect fires** (crawler.go:303-331):
   - Called for A→B transition: stores B in `redirectDestinations` map
   - Called for B→C transition: stores B and C in `redirectDestinations` map
   - **Note**: OnRedirect is called for EACH redirect in the chain, not just once for the entire chain

2. **HTTP client follows redirects** and fetches final destination (C)

3. **OnResponse fires ONLY for C** (crawler.go:335-356):
   - Marks A, B, C as visited in `CrawlerStore` ✓
   - Clears redirect destinations from map ✓
   - **Problem**: Does NOT report A or B to application callbacks ✗

4. **OnHTML fires ONLY for C** (crawler.go:650-771):
   - Extracts links from C

5. **OnScraped fires ONLY for C** (crawler.go:791-853):
   - Calls `OnPageCrawled(C)` - C is reported to application ✓
   - Stores C's metadata in database ✓

**Result**: Only C appears in crawl results. A and B are marked visited (preventing re-crawl) but never reported to the application layer, so they never reach the database.

### Why URLs Sometimes Appear

The non-deterministic behavior occurs because:

- **If a URL is discovered directly** (via sitemap, spider link, etc.) BEFORE it appears in a redirect chain:
  - It goes through normal flow: OnResponse → OnHTML → OnScraped → OnPageCrawled ✓
  - Gets stored in database ✓

- **If a URL is ONLY discovered as a redirect destination**:
  - Gets marked as visited in OnResponse ✓
  - Never gets reported to OnPageCrawled ✗
  - Never gets stored in database ✗

The race is between:
1. Spider/sitemap discovering the URL directly (works correctly)
2. Redirect chain marking the URL as visited before spider discovers it (URL missing from results)

## Code Locations

### Redirect Handling Setup
- `crawler.go:302-356` - `setupRedirectHandler()` function
  - Lines 303-331: OnRedirect callback (stores redirect destinations)
  - Lines 335-356: OnResponse callback (marks URLs as visited, needs fix)

### Application Callbacks
- `crawler.go:361-394` - Callback setters
  - `SetOnPageCrawled` - For HTML pages (crawler.go:361-365)
  - `SetOnResourceVisit` - For non-HTML resources (crawler.go:370-374)

### Callback Invocation
- `crawler.go:791-853` - OnScraped handler (calls OnPageCrawled for HTML)
- `crawler.go:856-950` - OnResponse handler (calls OnResourceVisit for non-HTML)
- `crawler.go:1184-1203` - Helper methods that invoke callbacks

### Storage
- `storage/crawler_store.go:105-125` - Redirect destination tracking
  - `AddRedirectDestination` (line 109) - Stores intermediate URLs
  - `GetAndClearRedirectDestinations` (line 117) - Retrieves and clears

## Solution Design

### Where to Fix

Fix should be implemented in **OnResponse callback** (crawler.go:335-356) where we currently mark redirect URLs as visited. This is the correct location because:

1. OnResponse fires AFTER all HTML processing is complete for the final destination
2. We already have the redirect chain data (`GetAndClearRedirectDestinations`)
3. We have the final response data (status code, Content-Type)
4. We cannot use OnRedirect because it fires BEFORE the response, so we don't have status codes yet
5. We cannot use OnScraped because it only fires for the final URL, not intermediates

### Implementation Strategy

For each intermediate URL in the redirect chain (excluding the final destination):

1. **Store metadata** in `pageMetadata` map so future links can reference it
2. **Create appropriate result object**:
   - If final destination is HTML (`text/html`): Create `PageResult` and call `OnPageCrawled`
   - If final destination is non-HTML: Create `ResourceResult` and call `OnResourceVisit`
3. **Increment crawled pages counter** (for HTML only)

### Data Available in OnResponse

When processing redirect chain A→B→C in OnResponse, we have:

✅ **Available data:**
- All intermediate URLs (A, B) from `redirectDestinations` map
- Final destination URL (C) from `r.Request.URL.String()`
- Final status code from `r.StatusCode` (e.g., 200)
- Final Content-Type from `r.Headers.Get("Content-Type")`
- Final title from `r.Request.Ctx.Get("title")` (if HTML)

❌ **NOT available (with current implementation):**
- Intermediate redirect status codes (301, 302, 307, 308)
- Intermediate Content-Types
- Intermediate response headers

**Why**: Go's default `http.Client` behavior automatically follows redirects and only returns the final response, discarding all intermediate responses. The `CheckRedirect` callback receives `[]*http.Request` (not `[]*http.Response`), so intermediate response data is not accessible.

### Solution: Custom Redirect Handler

Override Go's default redirect handling to capture each intermediate response.

#### Problem with Default Go Behavior

By default, Go's `http.Client` automatically follows redirects and only returns the final response — discarding all intermediate responses (and their status codes). We need to capture every response in the redirect chain, e.g. [301, 302, 200], for accurate crawl data.

#### What the Default CheckRedirect Does

When `CheckRedirect` is `nil`, Go internally:
- Follows up to 10 redirects (then errors out)
- Copies most headers, but drops sensitive ones like `Authorization` when host changes
- Adjusts request method based on the redirect code:
  - 301/302/303 → converts POST → GET
  - 307/308 → preserves method and body
- Retains cookies via the client's Jar, if configured

#### Implementation Strategy

Override `CheckRedirect` to manually follow redirects and capture each response:

1. **Disable automatic redirect following**: Set `CheckRedirect` to return an error to stop automatic following
2. **Manual redirect handling**: In the main request handling code, catch redirect responses
3. **Capture each response**: Store status code, headers, and body for each step in the redirect chain
4. **Preserve security**: Drop `Authorization` headers when host changes
5. **Preserve default behavior**: Handle method/body conversion based on redirect code (301/302/303 vs 307/308)
6. **Respect redirect limit**: Follow up to 10 redirects maximum
7. **Report all responses**: Pass each intermediate response to application callbacks

**Key Implementation Points:**
- For chain A→B→C, we'll capture all three responses with their actual status codes
- Store intermediate responses in a structure accessible to callbacks
- Ensure cookies are handled correctly across redirects
- Handle edge cases: redirect loops, mixed protocol redirects, etc.

**Benefits:**
- Accurate status codes and headers for all redirects (301 vs 307 vs 302)
- Complete crawl data for SEO analysis
- Better debugging and analytics
- No data assumptions needed

### PageResult/ResourceResult Structure

**PageResult** (for HTML redirects):
```go
&PageResult{
    URL:                intermediateURL,
    Status:             actualRedirectStatus,  // Actual status from intermediate response (301, 302, 307, 308)
    Title:              "",                     // Redirects don't have page content
    MetaDescription:    "",                     // Not available for redirects
    Indexable:          "Yes",
    ContentType:        actualContentType,      // Actual Content-Type from intermediate response
    Error:              "",
    Links:              &Links{Internal: []Link{}, External: []Link{}}, // No links for redirects
    ContentHash:        "",
    IsDuplicateContent: false,
    response:           nil,                    // Can store intermediate response if needed
}
```

**ResourceResult** (for non-HTML redirects):
```go
&ResourceResult{
    URL:         intermediateURL,
    Status:      actualRedirectStatus,  // Actual status from intermediate response (301, 302, 307, 308)
    ContentType: actualContentType,      // Actual Content-Type from intermediate response
    Error:       "",
}
```

## Testing Approach

### Verification Steps

1. **Run sequential crawls** to establish baseline:
   ```bash
   cd analysis/race_condition
   python3 sequential_crawl.py
   ```

2. **Check for unstable URLs**: After fix, all URLs should appear in 100% of crawls

3. **Verify redirect URLs are reported**: Check that intermediate redirect URLs appear in crawl results with appropriate metadata

### Known Unstable URLs (Pre-Fix)

URLs with <100% appearance rate (as of 2025-10-19):
- `handbook.agentberlin.ai/intro` - 78-100% (redirect destination)
- `handbook.agentberlin.ai/topic_first_seo` - 90.0%
- `agentberlin.ai/refund-policy` - 94.0%
- `agentberlin.ai/privacy-policy` - 96.0%
- `agentberlin.ai/blog` - 96.0%
- `workspace.agentberlin.ai/login?next=%2F` - 98.0%
- `workspace.agentberlin.ai/login?next=%2Fcheckout%3Fplan%3Ddominate` - 98.0%
- `workspace.agentberlin.ai/login?next=%2Fcheckout%3Fplan%3Dscale` - 98.0%

### Debugging Tools

- `sequential_crawl.py` - Run multiple sequential crawls to identify unstable URLs
- `mass_crawl.py` - Run parallel crawls for statistical analysis
- `analyze_mass_crawls.py` - Analyze link sources and identify patterns

## Implementation Status (2025-10-20)

### ✅ Implementation Completed

**Step 1: Modified HTTP Client Configuration**
- ✅ Modified `collector.go:checkRedirectFunc()` to return `http.ErrUseLastResponse`
- ✅ Disabled automatic redirect following

**Step 2: Implemented Manual Redirect Handling**
- ✅ Added `RedirectResponse` struct in `response.go` to store intermediate redirect data
- ✅ Implemented manual redirect loop in `http_backend.go:Do()`:
  - ✅ Captures all 3xx redirect responses with actual status codes
  - ✅ Extracts Location header for each redirect
  - ✅ Stores intermediate responses in `Response.RedirectChain`
  - ✅ Handles method/body conversion (301/302/303 → GET, 307/308 → preserve)
  - ✅ Drops Authorization header when host changes
  - ✅ Respects 10 redirect maximum
  - ✅ Integrates with `CheckRedirect` callback for URL filtering

**Step 3: Integrated with Callback System**
- ✅ Modified `crawler.go:setupRedirectHandler()` to process `Response.RedirectChain`
- ✅ For each intermediate redirect:
  - ✅ Marks URL as visited in CrawlerStore
  - ✅ Stores metadata with actual status code and Content-Type
  - ✅ Determines HTML vs resource based on final destination's Content-Type
  - ✅ Creates PageResult or ResourceResult with actual redirect status codes
  - ✅ Calls OnPageCrawled or OnResourceVisit callbacks
- ✅ Marks final destination as visited (fixes case where final URL wasn't queued)

**Step 4: Testing**
- ✅ Added comprehensive unit tests in `redirect_chain_test.go`:
  - ✅ Single redirect (A→B) with status code verification
  - ✅ Redirect chain (A→B→C) with multiple status codes
  - ✅ Different redirect types (301, 302, 307, 308)
  - ✅ Long redirect chains (8 redirects)
  - ✅ All URLs marked as visited verification
- ✅ Added integration test `TestRedirectChainWithStatusCodes` in `integration_tests/crawler_test.go`
- ✅ Updated existing tests to expect new correct behavior (all URLs in chain reported)
- ⏳ Sequential crawl verification in progress (not yet confirmed stable)

### 🔴 Known Test Failures (3 failures)

**1. Build Error - integration_tests**
```
integration_tests/crawler_test.go:598:37: undefined: app.CrawlResult
```
- **Cause**: Integration test uses `app.CrawlResult` type that may not exist or be named differently
- **Impact**: Integration test cannot compile
- **Fix needed**: Update type reference in integration test

**2. TestCrawler_RedirectURLFiltering/disallowed_URL_filter_blocks_redirect_destination**
```
Expected error when redirect destination is blocked by URL filter
Filtered redirect destination should not be successfully crawled
OnRedirect correctly blocked redirect destination: crawled 2 URLs
```
- **Cause**: Manual redirect loop calls `CheckRedirect` but continues processing even when redirect is blocked
- **Impact**: Redirects to disallowed URLs are still being crawled and reported
- **Fix needed**: Handle CheckRedirect errors that indicate blocking (non-ErrUseLastResponse errors) and abort redirect processing

**3. TestExternalRedirect**
```
External redirect destination should not have been crawled
External redirect destination should NOT be marked as visited (redirect blocked)
```
- **Cause**: Same as #2 - external redirects blocked by domain filters are still being processed
- **Impact**: External redirect destinations are being marked as visited and reported
- **Fix needed**: Same as #2

### ✅ Core Functionality Working

All core redirect chain tests passing:
- ✅ TestRedirectChainStatusCodes - Verifies actual status codes (301, 302, etc.)
- ✅ TestSingleRedirectStatusCode - Single redirect with correct status
- ✅ TestRedirectChainAllURLsMarkedVisited - All URLs in chain marked
- ✅ TestRedirectChainVisitTracking - Visit tracking works correctly
- ✅ TestLongRedirectChain - Long chains handled properly
- ✅ TestNoRedirect - Non-redirect pages unaffected
- ✅ TestHttpBackendManualRedirect - HTTP backend correctly captures redirects

### ⏳ Stability Verification Pending

**Not Yet Verified:**
- Sequential crawl consistency (100% URL appearance rate)
- Real-world crawl stability with agentberlin.ai
- Previously unstable URLs now stable

**Action Required:**
1. Fix the 3 test failures (integration test build error + redirect filtering issues)
2. Run full sequential crawl test to completion
3. Analyze results to confirm 100% consistency for previously unstable URLs
4. Verify no new race conditions introduced

### 📝 Implementation Notes

**Key Design Decisions:**
1. **Final URL marking**: Final redirect destination is marked as visited in `setupRedirectHandler` because it's never separately queued through the discovery process
2. **Content-Type determination**: All redirects in a chain use the final destination's Content-Type to categorize as HTML or resource (since redirects themselves don't have content)
3. **CheckRedirect integration**: Manual redirect loop calls `CheckRedirect` with proper `via` chain to maintain URL filtering capabilities

**Files Modified:**
- `collector.go` - Modified CheckRedirect behavior
- `http_backend.go` - Implemented manual redirect loop
- `response.go` - Added RedirectResponse struct
- `crawler.go` - Updated setupRedirectHandler to process redirect chains
- `redirect_chain_test.go` - New comprehensive unit tests
- `redirect_visit_tracking_test.go` - Updated to expect new behavior
- `integration_tests/crawler_test.go` - Added redirect chain integration test
