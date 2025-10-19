# Race Condition Analysis & Debugging Guide

## Problem Overview

The crawler produces inconsistent results when crawling the same website multiple times. For example, crawling `agentberlin.ai` yields between 139-145 total URLs per crawl, with certain URLs appearing in only 78-99.5% of crawls instead of 100%.

**Key characteristic**: The race condition occurs even in sequential (non-parallel) crawls, indicating timing-dependent behavior rather than concurrency issues.

---

## Fixed Issues

### ✅ Redirect Destination Race Condition

**Symptom**: URLs that are redirect destinations (e.g., `handbook.agentberlin.ai/intro` which redirects from `handbook.agentberlin.ai/`) appeared in only 78% of crawls.

**Root Cause**: The `setupRedirectHandler()` function in `crawler.go` was marking redirect destinations as "visited" BEFORE their responses were processed. This caused the Collector to reject the redirect destination response as "already visited", preventing `OnHTML`/`OnScraped` callbacks from being called.

**Non-deterministic behavior**:
- If a page linking directly to `/intro` is crawled first → `/intro` gets properly queued and crawled ✓
- If the redirect to `/intro` happens first → `/intro` gets marked visited but skipped ✗

**Fix Applied**: Modified `setupRedirectHandler()` to track redirect chains and mark redirect URLs as visited AFTER response processing:

1. **OnRedirect callback**: Stores intermediate URLs in redirect chains
   - For chain A→B→C: creates entries `{"B": ["B"]}` and `{"C": ["B", "C"]}`

2. **OnResponse callback**: Marks all redirect URLs as visited after processing
   - Ensures callbacks are called and links are extracted
   - Cleans up redirect entries to prevent memory leaks

**Files Modified**:
- `crawler.go:302-346` - `setupRedirectHandler()` function
- `storage/crawler_store.go` - Added `redirectDestinations` map and helper methods
- `integration_tests/crawler_test.go` - Added `TestRedirectDestinationCrawled` regression test

**Verification**: After fix, `handbook.agentberlin.ai/intro` now appears in 100% of crawls (was 78%).

---

## Known Remaining Issues

### ⚠️ Remaining Unstable URLs (93-99.5% appearance rate)

These URLs still exhibit inconsistent crawling behavior:

1. `agentberlin.ai/refund-policy` - 93.0%
2. `agentberlin.ai/privacy-policy` - 96.0%
3. `handbook.agentberlin.ai/topic_first_seo` - 97.5%
4. `workspace.agentberlin.ai/login?next=%2F` - 98.0%
5. `agentberlin.ai/terms-of-service` - 98.0%
6. `workspace.agentberlin.ai/login?next=%2Fcheckout%3Fplan%3Dscale` - 98.0%
7. `agentberlin.ai/blog` - 98.5%
8. `workspace.agentberlin.ai/login?next=%2Fcheckout%3Fplan%3Ddominate` - 99.0%
9. `agentberlin.ai/newsletter` - 99.0%
10. `agentberlin.ai/pricing` - 99.5%

**Possible Causes**:
- JavaScript rendering timing issues
- Conditional link rendering (A/B testing, session state)
- Link discovery/extraction race conditions
- URL queue processing timing

---

## Debugging Tools & Scripts

### Sequential Crawl Test

Run multiple sequential crawls to establish baseline stability:

```bash
cd analysis/race_condition
python3 sequential_crawl.py
```

**Parameters** (edit in script):
- `TARGET_CRAWLS`: Number of crawls to run (default: 200)
- `TARGET_URL`: Website to crawl
- `PROJECT_ID`: Project ID in database

**Output**: Identifies which URLs are unstable and their appearance rates.

### Mass Parallel Crawl Test

Run many parallel crawls for statistical analysis:

```bash
python3 mass_crawl.py
```

**Parameters** (edit in script):
- `TARGET_CRAWLS`: Number of parallel crawls (default: 1000)
- `MAX_WAIT_MINUTES`: Timeout for crawl completion (default: 10)

**Use case**: Test for concurrency-related race conditions.

### Comprehensive Link Analysis

Analyze crawl results to identify the source of inconsistencies:

```bash
python3 analyze_mass_crawls.py | tee full_report.txt
```

**What it does**:
- Identifies unstable URLs across all crawls
- Analyzes inbound links to unstable URLs
- Determines if source pages exist in crawls where target is missing
- Checks if source pages have links when target URL is missing

**Key Questions Answered**:
1. Do source pages exist in crawls where target is missing?
   - If NO → Race is in discovering the source page
   - If YES → Proceed to question 2
2. Do source pages still contain links to target when it's missing?
   - If NO → Links are conditionally rendered
   - If YES → Race is in link following/queue processing

### Full Analysis Pipeline

Run all analysis steps in sequence:

```bash
bash run_full_analysis.sh
```

**What it does**:
1. Runs mass crawls
2. Analyzes results with link analysis
3. Generates comprehensive report

---

## Debugging Workflow

### 1. Reproduce the Issue

```bash
# Verify server is running
curl http://localhost:8080/api/v1/health

# Run sequential crawls to identify unstable URLs
python3 analysis/race_condition/sequential_crawl.py
```

### 2. Identify the Pattern

Look for:
- Which URLs are unstable?
- What do they have in common? (subdomain, link type, page depth)
- Is there mutual exclusivity between certain URLs?

### 3. Run Link Analysis

```bash
python3 analysis/race_condition/analyze_mass_crawls.py
```

Determine if the race is in:
- **Link discovery**: Source pages don't exist in some crawls
- **Link extraction**: Links are conditionally rendered
- **Link following**: Source pages and links exist, but aren't followed

### 4. Investigate Code Paths

Based on link analysis results:

- **If source pages are missing**: Check initial page discovery in `crawler.go`
- **If links are missing**: Check JavaScript rendering timing (headless browser integration)
- **If links exist but aren't followed**: Check URL queue/deduplication logic in `storage/crawler_store.go`

### 5. Test the Fix

```bash
# Run sequential crawls to verify fix
python3 analysis/race_condition/sequential_crawl.py

# Run parallel crawls to test for concurrency issues
python3 analysis/race_condition/mass_crawl.py
```

---

## Common Root Causes

### Timing-Dependent Issues
- JavaScript rendering timing varies
- Network latency affects page load order
- Async resource loading (fonts, images, scripts)

### Conditional Rendering
- A/B testing variations
- Session state simulation
- Client-side random logic
- Authentication state differences

### URL Processing Issues
- Deduplication logic bugs
- URL normalization inconsistencies
- Queue processing race conditions
- Visit tracking errors

---

## Code Locations to Investigate

- `crawler.go:302-346` - Redirect handling
- `crawler.go` - Main crawler logic, URL queue processing
- `storage/crawler_store.go` - Visit tracking, URL deduplication
- JavaScript rendering integration (if applicable)

---

## Server Considerations

Before running large crawl batches:

1. **Monitor resource usage** - CPU, memory, database connections
2. **Check for memory leaks** - Especially in long-running crawls
3. **Verify database connection pooling** - Ensure proper configuration
4. **Consider rate limiting** - On crawl start endpoint if needed
5. **Run in batches** - Start with 10-20 crawls, then scale up
