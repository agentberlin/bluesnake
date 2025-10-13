# Pending Improvements for URL Discovery

**Last Updated:** October 13, 2025
**Test Domain:** reachpsych.com

## Current Status

BlueSnake finds ~36% of URLs compared to ScreamingFrog (218 vs 496 URLs).

## Remaining Gap Analysis

**317 URLs still missing:**

1. **284 "other" type URLs** - RSC cache-busting tokens (`?_rsc=xxxxx`)
   - These are duplicate pages with different query parameters
   - Represent prefetch requests, not unique content

2. **21 Images** - Next.js optimized images not triggered by scrolling
   - Require navigation to specific pages to be discovered

3. **9 JavaScript files** - Page-specific Next.js chunks
   - Only loaded when navigating to those specific pages

## Pending Work

### 1. URL Normalization Policy
Decide if RSC tokens should be treated as duplicates to improve meaningful coverage metrics.

### 2. Extended Scrolling Strategy
Consider multiple scroll positions or infinite scroll detection to trigger more lazy-loaded content.

### 3. Navigation Simulation
Crawl internal anchor links to trigger page-specific chunks and conditionally-loaded resources.

### 4. Performance Tuning
Monitor memory/CPU usage with configurable wait times (now defaults to 4.5s total).

## Testing Commands

```bash
# Full comparison (runs both crawlers)
uv run compare/compare_crawlers.py reachpsych.com

# Quick validation (reuses ScreamingFrog data)
uv run compare/compare_crawlers.py reachpsych.com --bluesnake-only
```
