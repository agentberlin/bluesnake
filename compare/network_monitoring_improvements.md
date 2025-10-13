# Network Monitoring Improvements - Report

**Date:** October 13, 2025
**Test Domain:** reachpsych.com
**Issue:** BlueSnake missing 68% of URLs compared to ScreamingFrog

## Problem Identified

Initial comparison showed BlueSnake found only 170 URLs (32.3%) vs ScreamingFrog's 496 URLs.

**Root Cause:** BlueSnake only extracted links from static/rendered HTML, while ScreamingFrog monitored actual network traffic during page load (JavaScript chunks, lazy-loaded images, fonts, API calls, etc.).

## Solutions Implemented

### 1. Chrome DevTools Protocol Network Monitoring
**Files Modified:** `chromedp_backend.go`

- Added network event listener using `network.Enable()` and `EventRequestWillBeSent`
- Captures all network requests during page rendering (JS, CSS, images, fonts, API calls)
- Returns discovered URLs alongside rendered HTML

### 2. Network URL Context Passing
**Files Modified:** `bluesnake.go`

- Stores network-discovered URLs in response context as JSON
- Passes data to crawler for processing

### 3. Network URL Crawling Queue
**Files Modified:** `crawler.go`

- Parses network URLs from context
- Creates Link objects with inferred resource types
- Queues URLs for actual crawling (not just reporting)
- Respects resource validation configuration

### 4. Page Scrolling for Lazy-Loaded Content
**Files Modified:** `chromedp_backend.go`

- Scrolls page to bottom to trigger lazy-loaded images
- Waits for network requests to fire
- Scrolls back to top to capture all content
- Total wait time: 2 seconds (500ms initial + 1s scroll + 500ms final)

### 5. Analytics & Tracking URL Filtering
**Files Modified:** `crawler.go`

Added `isAnalyticsOrTracking()` function to filter:
- Google Analytics (`/g/collect`, `/gtag/js`, `/ga.js`)
- Tag Manager (`/gtm.js`, `googletagmanager`)
- React Server Components tokens (`?_rsc=xxxxx`)
- Generic tracking (`/pixel`, `/track`, `/beacon`, `/telemetry`)

### 6. Additional Improvements
**Files Modified:** `crawler.go`

- Added `inferResourceType()` helper for URL-based type detection
- CSS comment stripping to prevent extracting commented URLs
- Centralized resource type detection logic

## Results

| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| URLs Found | 170 | 218 | +28% |
| Common URLs | - | 179 | - |
| Coverage | 32.3% | 36.1% | +3.8pp |
| Match Quality | Low | High | Analytics filtered |

### Resource Breakdown (After)
- **CSS:** 3/3 (100% match)
- **Fonts:** 13/4 (finding MORE than SF)
- **HTML:** 58/56 (2 extra pages discovered)
- **Images:** 84/84 count match (21 different specific images)
- **JavaScript:** 62/64 (missing 9 page-specific chunks)

## Remaining Gap Analysis

**317 URLs still missing** from BlueSnake:

1. **284 "other" type URLs** - RSC cache-busting tokens (`?_rsc=xxxxx`)
   - These are duplicate pages with different query parameters
   - Likely false positives in ScreamingFrog's count
   - Represent prefetch requests, not unique content

2. **21 Images** - Next.js optimized images not triggered by scrolling
   - Likely require navigation to specific pages
   - May be conditionally loaded based on user interaction

3. **9 JavaScript files** - Page-specific Next.js chunks
   - Only loaded when navigating to those specific pages
   - Not part of the initial bundle

## Code Quality

- ✅ All changes compiled successfully
- ✅ Existing tests still pass
- ⚠️ No new tests added (baseline test coverage could be added for `inferResourceType()`, `isAnalyticsOrTracking()`, and CSS comment stripping)

## Recommendations for Future Work

1. **URL Normalization Policy** - Decide if RSC tokens should be treated as duplicates
2. **Extended Scrolling** - Consider multiple scroll positions or infinite scroll detection
3. **Navigation Simulation** - Crawl internal anchor links to trigger page-specific chunks
4. **Test Coverage** - Add baseline tests for new helper functions
5. **Performance Tuning** - Monitor memory/CPU usage with extended wait times

## Testing Commands

```bash
# Full comparison (runs both crawlers)
uv run compare/compare_crawlers.py reachpsych.com

# Quick validation (reuses ScreamingFrog data)
uv run compare/compare_crawlers.py reachpsych.com --bluesnake-only
```

## Files Modified

- `chromedp_backend.go` - Network monitoring + scrolling
- `bluesnake.go` - Context passing
- `crawler.go` - URL processing, filtering, and queueing
