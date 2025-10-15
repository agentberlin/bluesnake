# Test Failures Documentation

This document tracks test failures identified on 2025-10-15 and their root causes.

## Summary

Total failing tests: 7
- 5 Collector visit tracking tests (collector_test.go, bluesnake_server_test.go)
- 2 Crawler link extraction tests (crawler_links_test.go)

## Category 1: Collector Visit Tracking Tests (5 failures)

### Root Cause
Visit tracking logic was **intentionally removed** from the Collector's `requestCheck()` function and moved to the Crawler's `processDiscoveredURL()` function. This was done to eliminate race conditions by centralizing visit tracking in a single-threaded processor goroutine.

**Code Location**: `bluesnake.go:1067-1097`
- Lines 1090-1096 contain comments explaining the removal
- Visit checking now happens in `crawler.go:368` via `cr.Collector.store.VisitIfNotVisited(uHash)`

### Failing Tests

#### 1. TestCollectorURLRevisit
**File**: `collector_test.go:222-249`
**What it tests**: Collector should prevent revisiting the same URL unless `AllowURLRevisit=true`
**Failure**: URL is revisited even when `AllowURLRevisit=false` because the Collector no longer checks visited status
**Error**:
```
collector_test.go:238: URL revisited
collector_test.go:247: URL not revisited
```

#### 2. TestCollectorPostRevisit
**File**: `collector_test.go:251-289`
**What it tests**: POST requests to the same URL with same data should not be revisited
**Failure**: POST requests are revisited because visit checking was removed from Collector
**Error**: Similar to TestCollectorURLRevisit

#### 3. TestCollectorPostURLRevisitCheck
**File**: `collector_test.go:291-346`
**What it tests**: `HasPosted()` method should correctly report if a POST URL+data combination has been visited
**Failure**: `HasPosted()` returns false after POST because visit tracking happens in Crawler, not Collector
**Error**:
```
collector_test.go:321: Expected URL to have been visited
collector_test.go:344: Expected URL to have been visited
```

#### 4. TestCollectorPostRawRevisit
**File**: `collector_test.go:183-216`
**What it tests**: PostRaw requests should not be revisited unless `AllowURLRevisit=true`
**Failure**: Same as other revisit tests - Collector doesn't track visits anymore

#### 5. TestCollectorURLRevisitCheck
**File**: `bluesnake_server_test.go:313-372`
**What it tests**:
- `HasVisited()` should return false before visiting
- `HasVisited()` should return true after visiting
- Revisiting should return `AlreadyVisitedError`
**Failure**: Visit status is not tracked by Collector, so `HasVisited()` always returns false
**Error**:
```
bluesnake_server_test.go:338: Expected URL to have been visited
bluesnake_server_test.go:364: err=%!q(<nil>) returned when trying to revisit, expected AlreadyVisitedError
```

### Resolution Options

1. **Remove these tests** - Since visit tracking was intentionally moved to Crawler, these Collector-level tests are no longer valid
2. **Move to Crawler tests** - Rewrite these tests to use the Crawler API instead of Collector directly
3. **Restore minimal visit tracking** - Add back visit tracking to Collector for backward compatibility, but this reintroduces the race conditions that were fixed

**Recommendation**: Option 1 or 2. The visit tracking was moved to Crawler for good architectural reasons (eliminating race conditions).

## Category 2: Crawler Link Extraction Tests (2 failures)

### Root Cause
Race condition between channel closure and goroutine attempting to send to channel.

**The Problem**:
1. `Wait()` calls `cr.Collector.Wait()` which blocks until all HTTP requests complete (line 193)
2. `Wait()` then closes `cr.discoveryChannel` (line 201)
3. BUT: OnHTML callbacks run asynchronously (line 938 in bluesnake.go: `go c.fetch(...)`)
4. These async callbacks can still be executing and trying to queue URLs (line 328-334 in crawler.go)
5. When they call `queueURL()` after the channel is closed, it panics: **"panic: send on closed channel"**

**Code Locations**:
- Channel closed: `crawler.go:201` in `Wait()`
- Send attempted: `crawler.go:303` in `queueURL()`
- Panic stack trace shows: `crawler.go:302` → `crawler.go:622` (OnHTML callback)

### Failing Tests

#### 1. TestLinkExtraction_MultipleTypes
**File**: `crawler_links_test.go:24-122`
**What it tests**: Crawler should extract all link types (anchors, images, scripts, stylesheets, etc.)
**Failure**: Panic when OnHTML callback tries to queue discovered URLs after channel is closed
**Error**:
```
panic: send on closed channel
goroutine 42 [running]:
github.com/agentberlin/bluesnake.(*Crawler).queueURL(0x1400016ba40, ...)
	/Users/hhsecond/asgard/bluesnake/crawler.go:302 +0x120
github.com/agentberlin/bluesnake.(*Crawler).setupCallbacks.func1(0x14000724420)
	/Users/hhsecond/asgard/bluesnake/crawler.go:622 +0x92c
```

#### 2. TestInternalExternalClassification
**File**: `crawler_links_test.go:125-194`
**What it tests**: Links should be correctly classified as internal vs external
**Failure**: Same panic as TestLinkExtraction_MultipleTypes
**Error**: Same panic: send on closed channel

### Resolution Options

1. **Add closed channel check in queueURL()** - Use defer/recover or check if channel is closed before sending
2. **Wait for all async work** - Ensure all OnHTML callbacks complete before closing discovery channel
3. **Use sync.WaitGroup for callbacks** - Track when all callbacks finish, not just when HTTP requests finish
4. **Make queueURL non-blocking and silent on closed channel** - Accept that late discoveries after shutdown are dropped

**Recommendation**: Option 1 or 4. Add protection in `queueURL()` to gracefully handle closed channel (either via recover() or by checking a shutdown flag). This is a common pattern in Go for graceful shutdown.

### Detailed Timing Analysis

The race happens because:
```
Time 0: Worker fetches page and calls fetch()
Time 1: fetch() completes, WaitGroup.Done() called
Time 2: Collector.Wait() returns (WaitGroup = 0)
Time 3: Wait() closes discoveryChannel
Time 4: OnHTML callback (running async in goroutine) tries to queueURL()
Time 5: PANIC - channel is closed
```

The fix needs to ensure either:
- OnHTML callbacks complete before closing channel, OR
- queueURL() gracefully handles closed channel

## Test Execution Command

To reproduce all failures:
```bash
go test -v ./... 2>&1 | grep -E "(FAIL|PASS|--- FAIL|--- PASS)"
```

To reproduce specific failures:
```bash
# Collector visit tracking tests
go test -v -run "TestCollectorURLRevisit" ./...

# Crawler link extraction tests
go test -v -run "TestLinkExtraction_MultipleTypes|TestInternalExternalClassification" ./...
```

## Priority

**High Priority**: Category 2 (Crawler tests) - These are crashes/panics that need to be fixed

**Medium Priority**: Category 1 (Collector tests) - These are testing deprecated functionality and should be cleaned up

## Next Steps

1. Fix Category 2 (race condition in queueURL/Wait)
2. Decide on Category 1 (remove tests or port to Crawler tests)
3. Re-run full test suite to verify fixes
