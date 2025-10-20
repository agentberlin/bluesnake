# Race Condition Analysis Tools

This directory contains tools for verifying crawler stability and detecting race conditions in URL discovery and processing.

## Quick Verification

Run sequential crawls to verify consistency:

```bash
cd analysis/race_condition

# Run 50 sequential crawls (recommended for quick verification)
python3 sequential_crawl.py 50 https://agentberlin.ai

# Analyze results for a specific project
python3 analyze_crawls.py <project_id> 50
```

## Understanding Race Conditions

### Context Isolation Issue (Fixed)

The crawler uses Colly's Context object to pass metadata between callbacks. A race condition can occur if child URLs share their parent's Context:

**Problematic Pattern:**
```go
// BUGGY: Child URL shares parent's Context
cr.queueURL(URLDiscoveryRequest{
    Context: e.Request.Ctx,  // Dangerous! Concurrent access to shared Context
})
```

**What happens:**
1. Parent page sets `contentType="text/html"` in Context
2. Parent discovers child image, queues it with same Context
3. Child image processing sets `contentType="image/png"` in SAME Context
4. Parent reads Context → sees `contentType="image/png"` → thinks it's an image → skips HTML callback

**Solution:**
```go
// FIXED: Each URL gets fresh Context
cr.queueURL(URLDiscoveryRequest{
    Context: nil,  // Each URL gets isolated Context
})
```

**Important:** Manual navigation (`Request.Visit()`, `Request.Retry()`) intentionally preserves Context for session continuity.

### Redirect Chain Handling

The crawler manually follows redirects to capture all intermediate URLs:

- Each redirect (301, 302, 307, 308) is reported with actual status code
- Both intermediate and final URLs are marked as visited
- Proper HTTP semantics maintained (POST→GET conversion, Authorization header handling)

**Implementation:** See `http_backend.go` (manual redirect loop) and `crawler.go:setupRedirectHandler()`

## Testing Tools

### sequential_crawl.py

Runs crawls one at a time to detect consistency issues:

```bash
# Usage
python3 sequential_crawl.py [crawl_count] [target_url]

# Examples
python3 sequential_crawl.py                          # 200 crawls of agentberlin.ai
python3 sequential_crawl.py 50                       # 50 crawls of agentberlin.ai
python3 sequential_crawl.py 10 https://example.com   # 10 crawls of example.com
```

### analyze_crawls.py

Analyzes crawl results to identify URLs with inconsistent appearance rates:

```bash
# Usage
python3 analyze_crawls.py <project_id> [crawl_count]

# Examples
python3 analyze_crawls.py 5      # Analyze last 22 crawls
python3 analyze_crawls.py 5 50   # Analyze last 50 crawls
```

**What it checks:**
- URLs that don't appear in 100% of crawls (potential race condition)
- URLs that appear multiple times in a single crawl (potential duplicate reporting)
- Patterns by content type (HTML, JS, CSS, images, fonts)

## Interpreting Results

### Expected Behavior

✅ **100% consistency** - URL appears in every crawl
- All HTML pages should achieve this
- Critical navigation links should achieve this

⚠️ **98-99% consistency** - Acceptable for dynamic resources
- Code-split JavaScript chunks (Next.js `_next/static/chunks/*.js`)
- Query parameter URLs that may be conditionally loaded
- RSS feeds or optional resources

❌ **<95% consistency** - Investigate for race conditions
- Check if Context is being shared between requests
- Verify URL discovery logic
- Review concurrent access to shared data structures

### Key Files Modified

Files related to race condition fixes:

- `crawler.go` - URL discovery queuing with isolated Contexts
- `collector.go` - Context usage documentation
- `request.go` - Manual navigation Context handling
- `http_backend.go` - Manual redirect following
- `response.go` - RedirectResponse struct

## Running Unit Tests

```bash
# All tests
go test -v ./...

# Race condition specific tests
go test -v -run "TestRedirect"
go test -v -run "TestContext"
```

**Key test files:**
- `redirect_chain_test.go` - Redirect handling with status codes
- `redirect_visit_tracking_test.go` - Visit tracking correctness
- `http_backend_redirect_test.go` - HTTP backend redirect behavior
- `context_isolation_test.go` - Context isolation verification

## Troubleshooting

### URLs Missing from Some Crawls

1. **Check Context usage** - Are child URLs getting their own Context?
2. **Review discovery mechanism** - Spider, sitemap, or network discovery?
3. **Verify visit tracking** - Is the URL being marked visited correctly?

### Duplicate URLs in Single Crawl

1. **Check callback invocations** - Is OnPageCrawled being called multiple times?
2. **Review redirect chains** - Are intermediate redirects being reported correctly?
3. **Verify deduplication** - Is visit tracking preventing duplicates?

### Dynamic Resource Variations

Some variations are expected:
- JavaScript bundles may be code-split differently
- A/B testing may serve different resources
- CDN caching may affect resource discovery

If variations are consistent with a small set of resources and don't affect core HTML pages, this is typically acceptable.
