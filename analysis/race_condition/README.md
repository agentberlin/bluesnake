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

## Implementation Checklist

### Step 1: Modify HTTP Client Configuration
- [ ] Locate HTTP client setup in collector.go
- [ ] Override `CheckRedirect` to disable automatic redirect following
- [ ] Return error to prevent automatic redirect (e.g., `http.ErrUseLastResponse`)

### Step 2: Implement Manual Redirect Handling
- [ ] Create redirect chain tracking structure in collector or crawler
- [ ] Modify main request handling to catch redirect responses
- [ ] Implement redirect following loop:
  - [ ] Check if response is a redirect (3xx status)
  - [ ] Extract `Location` header for next URL
  - [ ] Store current response (status, headers, body if needed)
  - [ ] Create new request for redirect destination
  - [ ] Handle method/body conversion (301/302/303 → GET, 307/308 → preserve)
  - [ ] Drop `Authorization` header if host changes
  - [ ] Respect 10 redirect maximum
  - [ ] Handle cookies via client's Jar

### Step 3: Integrate with Callback System
- [ ] Store all intermediate responses in accessible structure
- [ ] Modify `setupRedirectHandler()` or create new handler for captured responses
- [ ] For each intermediate response:
  - [ ] Mark URL as visited in CrawlerStore
  - [ ] Store metadata with actual status code and Content-Type
  - [ ] Determine if HTML or resource based on Content-Type
  - [ ] Create appropriate result object (PageResult or ResourceResult)
  - [ ] Call appropriate callback (`OnPageCrawled` or `OnResourceVisit`)
  - [ ] Increment crawled pages counter (for HTML only)

### Step 4: Testing
- [ ] Add unit tests for redirect chain handling
- [ ] Test various redirect scenarios:
  - [ ] Single redirect (A→B)
  - [ ] Redirect chain (A→B→C)
  - [ ] Different redirect types (301, 302, 307, 308)
  - [ ] Mixed content types (HTML→HTML, image→image)
  - [ ] Host changes (auth header dropping)
  - [ ] Redirect loops (should respect 10 limit)
- [ ] Run sequential crawls with real websites
- [ ] Confirm all previously unstable URLs now appear in 100% of crawls
- [ ] Verify accurate status codes for redirect URLs in database
