# Refactoring: Collector/Crawler Separation

## Overview

The `Crawler` currently accesses `Collector` internals in ways that break encapsulation. This document tracks the remaining work needed to achieve proper separation of concerns.

## Remaining Issue: Crawler Directives on Collector (Layer Violation)

### Problem

The Collector (HTTP client) implements crawler policy logic, violating separation of concerns:

| Field | Type | Location | Used By | Issue |
|-------|------|----------|---------|-------|
| `RobotsTxtMode` | string | Collector:277 | `checkRobots()` at collector.go:1052 | Policy, not HTTP |
| `FollowInternalNofollow` | bool | Collector:279 | Not used in Collector | Should be Crawler-only |
| `FollowExternalNofollow` | bool | Collector:281 | Not used in Collector | Should be Crawler-only |
| `RespectMetaRobotsNoindex` | bool | Collector:283 | Not used in Collector | Should be Crawler-only |
| `RespectNoindex` | bool | Collector:285 | Not used in Collector | Should be Crawler-only |
| `ResourceValidation` | *Config | Collector:275 | Not used in Collector | Should be Crawler-only |
| `robotsMap` | map | Collector:290 | `checkRobots()` cache | Should be Crawler-owned |

### Example of Layer Violation

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

### Why This Is Bad

- **Layer violation**: HTTP client (Collector) should not know about crawling policies
- **Poor separation**: Crawler policies (robots.txt, nofollow) implemented in wrong layer
- **Coupling**: Config exists in both CrawlerConfig (configuration) AND Collector (runtime)
- **Reusability**: Cannot use Collector standalone without crawler-specific config

### Current State

- ✅ Config separated (CrawlerConfig has crawler directives, HTTPConfig doesn't)
- ❌ Implementation NOT separated (Collector still has crawler directive fields + checkRobots logic)
- Comment in code says "should stay on Collector for now" (collector.go:635-640)

### Target Architecture

| Component | Responsibility | Methods |
|-----------|----------------|---------|
| **Collector** | HTTP client only | Fetch URLs, parse HTML, run callbacks |
| **Crawler** | Policy enforcement | Check robots.txt, enforce nofollow/noindex, filter URLs |

## Implementation Plan

**Estimated effort:** ~8 hours (BREAKING CHANGES)

### Step 1: Move robots.txt Logic to Crawler (4 hrs)

1. Add `robotsMap` field to Crawler struct (15 min)
2. Move `checkRobots()` method from Collector to Crawler (30 min)
3. Have Crawler check robots.txt in `isURLCrawlable()` BEFORE enqueueing (1 hr)
4. Remove `checkRobots()` call from `Collector.requestCheck()` (15 min)
5. Update Crawler to pass HTTP client to robots.txt checker (30 min)
6. Add unit tests for Crawler.checkRobots() (1 hr)
7. Update integration tests (30 min)

### Step 2: Remove Crawler Directive Fields from Collector (4 hrs)

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

### Breaking Changes

- Direct use of `Collector.checkRobots()` will break (migrate to Crawler)
- Setting crawler directives on Collector will no longer work
- Standalone Collector use won't check robots.txt (need to use Crawler)

## Testing Strategy

1. **Robots.txt tests** - Move/update tests from Collector to Crawler for checkRobots()
2. **Crawler directive tests** - Verify RobotsTxtMode, nofollow, noindex work from Crawler
3. **Regression tests** - Run existing test suite and fix breakages

## Next Steps

This is a significant architectural improvement but requires breaking changes. Should coordinate with any planned major version releases and consider user impact before proceeding.

---

**Document Last Updated:** 2025-10-19
**Status:** Ready for implementation (BREAKING change - awaiting approval)
