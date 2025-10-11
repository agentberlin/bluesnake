# Incremental Crawling Specification for BlueSnake

**Author**: Research & Analysis
**Date**: 2025-10-11
**Status**: Draft Specification
**Version**: 1.0

## Table of Contents
1. [Executive Summary](#executive-summary)
2. [Problem Statement](#problem-statement)
3. [Research Findings](#research-findings)
4. [Proposed Solution](#proposed-solution)
5. [Technical Design](#technical-design)
6. [Implementation Phases](#implementation-phases)
7. [API Changes](#api-changes)
8. [Database Schema Changes](#database-schema-changes)

---

## Executive Summary

This specification outlines the implementation of incremental crawling for BlueSnake, allowing the crawler to efficiently detect and crawl only changed content instead of re-crawling entire websites. The solution follows industry best practices from Google, Bing, and other major crawlers, using HTTP conditional requests, content hashing, and intelligent scheduling.

**Key Benefits:**
- **Bandwidth Reduction**: 60-80% reduction in data transfer using 304 Not Modified responses
- **Faster Crawls**: Skip unchanged pages, focus on new/modified content
- **Better Change Tracking**: Historical data on when and how often pages change
- **Server-Friendly**: Reduced load on target servers through conditional requests

---

## Problem Statement

### The Chicken-and-Egg Problem

When implementing incremental crawling, we face a fundamental challenge:

1. **To detect changes**, we need to check if content has changed
2. **To check content**, we need to crawl it (fetch the content hash)
3. **But crawling defeats the purpose** of incremental crawling (avoiding unnecessary downloads)

### Current State

BlueSnake currently has:
- ✅ Content hashing system (`content_hash.go`)
- ✅ Content hash storage in `Storage` interface
- ✅ Database with `Project`, `Crawl`, `CrawledUrl` tables
- ✅ Discovery mechanisms (spider, sitemap)
- ✅ `CheckHead` option for HEAD requests

But lacks:
- ❌ Storage of HTTP caching headers (ETag, Last-Modified)
- ❌ Per-URL crawl history and timestamps
- ❌ Conditional request support (If-None-Match, If-Modified-Since)
- ❌ Change frequency tracking
- ❌ Recrawl scheduling logic
- ❌ Crawl mode selection (full vs incremental)

---

## Research Findings

### How Popular Crawlers Solve This Problem

#### 1. HTTP Conditional Requests (Primary Strategy)

**ETag + If-None-Match:**
- Server generates unique identifier (ETag) for content
- Crawler stores ETag from previous crawl
- On next crawl, sends `If-None-Match: <etag>` header
- Server responds with:
  - `304 Not Modified` if content unchanged (no body sent)
  - `200 OK` with full content if changed
- **Googlebot's preferred method**

**Last-Modified + If-Modified-Since:**
- Server sends `Last-Modified` timestamp
- Crawler stores timestamp
- On next crawl, sends `If-Modified-Since: <timestamp>` header
- Server responds with 304 if not modified since that date
- **Fallback when ETag not available**

**Cache-Control max-age:**
- Server tells crawlers how long content stays fresh
- Crawler can skip URLs within freshness window

**Industry Implementation:**
- Google: Uses ETag preferentially, falls back to Last-Modified
- Microsoft SharePoint: Recommends conditional requests for crawl efficiency
- Ahrefs, Screaming Frog: Support If-Modified-Since headers

#### 2. Change Frequency Analysis

**Pattern Detection:**
- Crawlers analyze historical crawl data
- Cluster pages by change frequency patterns
- Pages that change frequently → crawl more often
- Static pages → crawl less frequently

**Googlebot's Approach:**
- Ignores sitemap's `changefreq` tag (too unreliable)
- Uses actual change history instead
- Factors: freshness signals, update patterns, page importance

#### 3. Lightweight Discovery Methods

**Sitemap lastmod Dates:**
- Parse `<lastmod>` from sitemap.xml
- Compare with last crawl date
- Prioritize URLs with recent lastmod

**HEAD Requests:**
- Issue HEAD request first (no body download)
- Check `Last-Modified` and `ETag` headers
- Decide whether to follow with GET

**Tiered Discovery:**
- Separate URL discovery phase from content fetching
- Discover all URLs first (from sitemaps, links)
- Then prioritize which ones to actually fetch

#### 4. Crawl Scheduling Algorithms

**URL Frontier Architecture:**
- **Front Queues**: Implement prioritization
- **Back Queues**: Implement politeness (one queue per host)

**Prioritization Factors:**
- Time since last crawl
- Historical change frequency
- Page importance (depth, inlinks, authority)
- Content type and quality

**Common Algorithms:**
- **OPIC** (Online Page Importance Computation): Distribute "cash" among pages
- **Best-First**: Select most valuable URLs from frontier
- **Clustering-Based**: Sample clusters, recrawl if significant changes detected

---

## Proposed Solution

### Multi-Level Change Detection Strategy

Our solution uses **multiple layers of change detection**, from most efficient to most thorough:

#### Level 1: HTTP Conditional Requests (Most Efficient)
- Use `If-None-Match` (ETag) and `If-Modified-Since` (Last-Modified) headers
- Server returns `304 Not Modified` → Skip processing, mark as "unchanged"
- Server returns `200 OK` → Download and process content
- **Bandwidth Saved**: ~70-80% for unchanged content

#### Level 2: Content Hashing (Already Implemented)
- For downloaded pages, compute content hash
- Compare with stored hash from previous crawl
- Detect changes even if server doesn't support conditional requests
- **Catches**: Changes in content that don't update Last-Modified

#### Level 3: Sitemap Intelligence
- Parse `<lastmod>` dates from sitemap.xml
- Compare with `LastCrawledAt` timestamp
- Prioritize URLs with recent lastmod dates
- **Use Case**: Quick initial prioritization before making requests

#### Level 4: Change Frequency Scoring
- Track how often each URL changes over time
- Calculate change frequency score (changes per time period)
- Prioritize frequently-changing URLs for more frequent crawls
- **Benefit**: Adaptive scheduling based on actual behavior

---

## Technical Design

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Incremental Crawler                       │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌────────────────┐      ┌──────────────────┐              │
│  │ URL Prioritizer│──────▶│ Conditional HTTP │              │
│  │                │      │   Request Layer  │              │
│  │ - Change Score │      │                  │              │
│  │ - Last Crawled │      │ - If-None-Match  │              │
│  │ - Sitemap Data │      │ - If-Mod-Since   │              │
│  └────────────────┘      └──────────────────┘              │
│         │                         │                          │
│         │                         ▼                          │
│         │              ┌──────────────────┐                 │
│         │              │ Response Handler │                 │
│         │              │                  │                 │
│         │              │ - 304 Handler    │                 │
│         │              │ - Hash Comparator│                 │
│         │              │ - Change Tracker │                 │
│         │              └──────────────────┘                 │
│         │                         │                          │
│         ▼                         ▼                          │
│  ┌──────────────────────────────────────┐                  │
│  │        URL Metadata Storage           │                  │
│  │                                        │                  │
│  │ - LastCrawledAt                        │                  │
│  │ - ETag / Last-Modified                 │                  │
│  │ - ContentHash                          │                  │
│  │ - ChangeFrequency Score                │                  │
│  │ - ChangesDetected Count                │                  │
│  └──────────────────────────────────────┘                  │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

### Crawl Modes

#### 1. Full Crawl Mode (Default)
- Crawls all discovered URLs regardless of previous state
- Ignores conditional requests
- Use case: First crawl, or when you want complete fresh data

#### 2. Incremental Crawl Mode
- Uses conditional requests for known URLs
- Skips unchanged content (304 responses)
- Only processes changed or new content
- Use case: Regular updates, monitoring changes

#### 3. Smart Crawl Mode (Advanced)
- Samples URLs to detect change patterns
- If >X% of sample changed, switches to full crawl
- Otherwise proceeds with incremental approach
- Use case: Intelligent adaptive crawling

---

## Implementation Phases

### Phase 1: Database Schema Extensions

**Add to `CrawledUrl` table:**

```go
type CrawledUrl struct {
    ID              uint   `gorm:"primaryKey"`
    CrawlID         uint   `gorm:"not null;index"`
    URL             string `gorm:"not null;index"` // Add index for lookups
    Status          int    `gorm:"not null"`
    Title           string `gorm:"type:text"`
    Indexable       string `gorm:"not null"`
    Error           string `gorm:"type:text"`

    // NEW FIELDS for Incremental Crawling
    LastCrawledAt   int64  `gorm:"index"`           // Unix timestamp
    ETag            string `gorm:"type:text"`       // ETag from response
    LastModified    string `gorm:"type:text"`       // Last-Modified header
    ContentHash     string `gorm:"index"`           // Content hash
    ChangeFrequency float64 `gorm:"default:0"`      // Changes per day
    ChangesDetected int    `gorm:"default:0"`       // Total changes seen

    CreatedAt       int64  `gorm:"autoCreateTime"`
}
```

**Add to `Config` table:**

```go
type Config struct {
    // ... existing fields ...

    // NEW FIELD
    CrawlMode       string `gorm:"default:'full'"` // "full", "incremental", "smart"
}
```

**Migration Strategy:**
- Add new columns with default values
- Existing data remains valid
- First incremental crawl will populate new fields

### Phase 2: Storage Interface Extensions

**Extend `storage/storage.go`:**

```go
type Storage interface {
    // ... existing methods ...

    // URL Metadata Management
    GetURLMetadata(url string) (*URLMetadata, error)
    SetURLMetadata(url string, metadata *URLMetadata) error

    // Change Tracking
    RecordURLChange(url string, changeDetected bool, contentHash string) error
    GetChangeFrequency(url string) (float64, error)
}

type URLMetadata struct {
    URL             string
    LastCrawledAt   int64   // Unix timestamp
    ETag            string
    LastModified    string
    ContentHash     string
    ChangeFrequency float64
    ChangesDetected int
}
```

**Implementation in `InMemoryStorage`:**

```go
type InMemoryStorage struct {
    // ... existing fields ...

    urlMetadata map[string]*URLMetadata
    metadataLock *sync.RWMutex
}
```

### Phase 3: HTTP Conditional Request Support

**Modify `bluesnake.go` collector:**

```go
type CollectorConfig struct {
    // ... existing fields ...

    // NEW FIELDS
    EnableIncrementalCrawl bool
    CrawlMode              string // "full", "incremental", "smart"
}

type Collector struct {
    // ... existing fields ...

    EnableIncrementalCrawl bool
    CrawlMode              string
}
```

**Modify request preparation in `scrape()` method:**

```go
func (c *Collector) scrape(u, method string, ...) error {
    // ... existing code ...

    // NEW: Add conditional request headers if incremental crawl enabled
    if c.EnableIncrementalCrawl && c.CrawlMode != "full" {
        if metadata, err := c.store.GetURLMetadata(u); err == nil && metadata != nil {
            if metadata.ETag != "" {
                hdr.Set("If-None-Match", metadata.ETag)
            }
            if metadata.LastModified != "" {
                hdr.Set("If-Modified-Since", metadata.LastModified)
            }
        }
    }

    // ... continue with request ...
}
```

**Modify response handling in `fetch()` method:**

```go
func (c *Collector) fetch(...) error {
    // ... existing request code ...

    response, err := c.backend.Cache(req, ...)

    // NEW: Handle 304 Not Modified
    if response.StatusCode == 304 {
        // Content unchanged, record this but don't process
        c.handleNotModified(request, response)
        return nil
    }

    // ... existing response processing ...

    // NEW: Store ETag and Last-Modified for future crawls
    if c.EnableIncrementalCrawl {
        metadata := &storage.URLMetadata{
            URL:           request.URL.String(),
            LastCrawledAt: time.Now().Unix(),
            ETag:          response.Headers.Get("ETag"),
            LastModified:  response.Headers.Get("Last-Modified"),
            ContentHash:   contentHash,
        }

        // Check if content actually changed
        oldMetadata, _ := c.store.GetURLMetadata(request.URL.String())
        changeDetected := oldMetadata == nil ||
                         oldMetadata.ContentHash != contentHash

        // Update metadata and change tracking
        c.store.SetURLMetadata(request.URL.String(), metadata)
        c.store.RecordURLChange(request.URL.String(), changeDetected, contentHash)
    }

    // ... continue processing ...
}
```

**Add new handler for 304 responses:**

```go
func (c *Collector) handleNotModified(request *Request, response *Response) {
    // Update LastCrawledAt but keep existing metadata
    metadata, err := c.store.GetURLMetadata(request.URL.String())
    if err != nil || metadata == nil {
        return
    }

    metadata.LastCrawledAt = time.Now().Unix()
    c.store.SetURLMetadata(request.URL.String(), metadata)

    // Record that no change was detected
    c.store.RecordURLChange(request.URL.String(), false, metadata.ContentHash)

    // Call callback with special "not modified" result
    result := &PageResult{
        URL:            request.URL.String(),
        Status:         304,
        Title:          "(Not Modified)",
        ContentType:    "",
        Indexable:      "-",
        ContentHash:    metadata.ContentHash,
        DiscoveredURLs: []CrawledURL{},
    }

    c.callOnPageCrawled(result)
}
```

### Phase 4: Change Frequency Tracking

**Algorithm for calculating change frequency:**

```go
// RecordURLChange updates change tracking statistics
func (s *InMemoryStorage) RecordURLChange(url string, changeDetected bool, contentHash string) error {
    s.metadataLock.Lock()
    defer s.metadataLock.Unlock()

    metadata, exists := s.urlMetadata[url]
    if !exists {
        metadata = &URLMetadata{
            URL:             url,
            ContentHash:     contentHash,
            LastCrawledAt:   time.Now().Unix(),
            ChangesDetected: 0,
            ChangeFrequency: 0,
        }
        s.urlMetadata[url] = metadata
    }

    if changeDetected {
        metadata.ChangesDetected++

        // Calculate change frequency (changes per day)
        if metadata.LastCrawledAt > 0 {
            daysSinceLastCrawl := float64(time.Now().Unix() - metadata.LastCrawledAt) / 86400.0
            if daysSinceLastCrawl > 0 {
                // Exponential moving average for change frequency
                alpha := 0.3 // Weight for new observation
                observed := 1.0 / daysSinceLastCrawl // Changes per day for this period
                metadata.ChangeFrequency = alpha*observed + (1-alpha)*metadata.ChangeFrequency
            }
        }
    }

    metadata.ContentHash = contentHash
    metadata.LastCrawledAt = time.Now().Unix()

    return nil
}
```

### Phase 5: URL Prioritization

**Priority scoring algorithm:**

```go
type URLPriority struct {
    URL             string
    Priority        float64
    LastCrawledAt   int64
    ChangeFrequency float64
    Depth           int
}

// CalculatePriority returns a priority score for a URL
// Higher score = should crawl sooner
func CalculatePriority(metadata *storage.URLMetadata, depth int) float64 {
    now := time.Now().Unix()

    // Factor 1: Time since last crawl (higher = more urgent)
    daysSinceLastCrawl := float64(now - metadata.LastCrawledAt) / 86400.0
    timeScore := daysSinceLastCrawl

    // Factor 2: Change frequency (higher = more urgent)
    // ChangeFrequency is in changes per day
    freqScore := metadata.ChangeFrequency * 10 // Scale up

    // Factor 3: Depth (lower depth = higher priority)
    depthScore := 1.0 / (float64(depth) + 1)

    // Factor 4: Never crawled before = highest priority
    neverCrawledBonus := 0.0
    if metadata.LastCrawledAt == 0 {
        neverCrawledBonus = 100.0
    }

    // Combined priority score
    priority := (timeScore * 0.4) +
                (freqScore * 0.4) +
                (depthScore * 0.2) +
                neverCrawledBonus

    return priority
}
```

### Phase 6: Sitemap Intelligence

**Parse and use sitemap lastmod:**

```go
// EnhancedSitemapURL extends sitemap URL with lastmod
type EnhancedSitemapURL struct {
    URL     string
    LastMod time.Time
    Priority float64 // From sitemap
}

// FetchSitemapWithMetadata returns URLs with metadata
func FetchSitemapWithMetadata(sitemapURL string) ([]EnhancedSitemapURL, error) {
    // Use existing sitemap.go but extract <lastmod> and <priority>
    // Return enhanced structure
}

// ShouldCrawlBasedOnSitemap decides if URL needs crawling
func ShouldCrawlBasedOnSitemap(url string, lastmod time.Time, metadata *storage.URLMetadata) bool {
    // If never crawled, definitely crawl
    if metadata == nil || metadata.LastCrawledAt == 0 {
        return true
    }

    // If sitemap says modified after last crawl, definitely crawl
    lastCrawledTime := time.Unix(metadata.LastCrawledAt, 0)
    if lastmod.After(lastCrawledTime) {
        return true
    }

    // Otherwise, use other factors (change frequency, time elapsed)
    return false
}
```

### Phase 7: Smart Crawl Mode

**Adaptive crawling strategy:**

```go
type SmartCrawlStrategy struct {
    SampleSize      int     // Number of URLs to sample
    ChangeThreshold float64 // If X% changed, switch to full crawl
}

func (s *SmartCrawlStrategy) DetermineCrawlMode(urls []string, store storage.Storage) string {
    // Sample random URLs
    sampleSize := min(s.SampleSize, len(urls))
    sample := randomSample(urls, sampleSize)

    // Do conditional requests on sample
    changedCount := 0
    for _, url := range sample {
        if isContentChanged(url, store) {
            changedCount++
        }
    }

    changeRatio := float64(changedCount) / float64(len(sample))

    // If many changes detected, do full crawl
    if changeRatio > s.ChangeThreshold {
        return "full"
    }

    // Otherwise, incremental crawl
    return "incremental"
}
```

---

## API Changes

### Crawler Configuration

**New configuration options:**

```go
config := &bluesnake.CollectorConfig{
    // ... existing config ...

    // NEW OPTIONS
    EnableIncrementalCrawl: true,
    CrawlMode:              "incremental", // "full", "incremental", "smart"
}

crawler := bluesnake.NewCrawler(config)
```

### PageResult Extensions

**Add fields to track incremental crawl status:**

```go
type PageResult struct {
    URL                string
    Status             int
    Title              string
    Indexable          string
    ContentType        string
    Error              string
    DiscoveredURLs     []CrawledURL
    ContentHash        string
    IsDuplicateContent bool

    // NEW FIELDS
    IsModified         bool   // false if 304 Not Modified
    ETag               string // ETag from response
    LastModified       string // Last-Modified from response
    ChangeDetected     bool   // true if content hash changed
    ChangeFrequency    float64 // Current change frequency score
}
```

### Callback Enhancements

**OnPageCrawled callback receives incremental status:**

```go
crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    if !result.IsModified {
        fmt.Printf("⏭️  Skipped (304): %s\n", result.URL)
    } else if result.ChangeDetected {
        fmt.Printf("🔄 Changed: %s\n", result.URL)
    } else {
        fmt.Printf("✓ New: %s\n", result.URL)
    }
})
```

### Crawl Statistics

**Add incremental crawl metrics:**

```go
type CrawlStats struct {
    TotalURLs          int
    NewURLs            int
    ChangedURLs        int
    UnchangedURLs      int
    SkippedURLs        int // 304 responses
    BandwidthSaved     int64 // Bytes saved by 304s
    AverageChangeFreq  float64
}
```

---

## Database Schema Changes

### Migration SQL

```sql
-- Add new columns to crawled_url table
ALTER TABLE crawled_url ADD COLUMN last_crawled_at INTEGER DEFAULT 0;
ALTER TABLE crawled_url ADD COLUMN etag TEXT DEFAULT '';
ALTER TABLE crawled_url ADD COLUMN last_modified TEXT DEFAULT '';
ALTER TABLE crawled_url ADD COLUMN content_hash TEXT DEFAULT '';
ALTER TABLE crawled_url ADD COLUMN change_frequency REAL DEFAULT 0.0;
ALTER TABLE crawled_url ADD COLUMN changes_detected INTEGER DEFAULT 0;

-- Add indexes for performance
CREATE INDEX idx_crawled_url_url ON crawled_url(url);
CREATE INDEX idx_crawled_url_last_crawled ON crawled_url(last_crawled_at);
CREATE INDEX idx_crawled_url_content_hash ON crawled_url(content_hash);

-- Add crawl_mode to config table
ALTER TABLE config ADD COLUMN crawl_mode TEXT DEFAULT 'full';
```

### GORM AutoMigrate

The changes to the Go structs will be automatically migrated by GORM:

```go
func InitDB() error {
    // ... existing code ...

    // Auto migrate will add new columns
    if err := db.AutoMigrate(&Config{}, &Project{}, &Crawl{}, &CrawledUrl{}); err != nil {
        return fmt.Errorf("failed to migrate database: %v", err)
    }

    return nil
}
```

---

## Usage Examples

### Example 1: Basic Incremental Crawl

```go
// First crawl (full)
config := bluesnake.NewDefaultConfig()
config.EnableIncrementalCrawl = true
config.CrawlMode = "full" // First time, do full crawl

crawler := bluesnake.NewCrawler(config)
crawler.Start("https://example.com")
crawler.Wait()

// Later crawl (incremental)
config2 := bluesnake.NewDefaultConfig()
config2.EnableIncrementalCrawl = true
config2.CrawlMode = "incremental" // Only crawl changed content

crawler2 := bluesnake.NewCrawler(config2)
crawler2.Start("https://example.com")
crawler2.Wait()
```

### Example 2: Smart Crawl with Adaptive Strategy

```go
config := bluesnake.NewDefaultConfig()
config.EnableIncrementalCrawl = true
config.CrawlMode = "smart" // Automatically decide

crawler := bluesnake.NewCrawler(config)

crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
    if result.Status == 304 {
        log.Printf("Unchanged: %s (saved bandwidth)", result.URL)
    } else if result.ChangeDetected {
        log.Printf("Changed: %s (freq: %.2f changes/day)",
                  result.URL, result.ChangeFrequency)
    }
})

crawler.Start("https://example.com")
crawler.Wait()
```

### Example 3: Query Change History

```go
// Get URLs that change frequently
func GetFrequentlyChangingPages(projectID uint) ([]CrawledUrl, error) {
    var urls []CrawledUrl
    err := db.Where("project_id = ? AND change_frequency > ?", projectID, 1.0).
        Order("change_frequency DESC").
        Limit(100).
        Find(&urls).Error
    return urls, err
}

// Get pages not crawled in X days
func GetStalePages(projectID uint, days int) ([]CrawledUrl, error) {
    cutoff := time.Now().AddDate(0, 0, -days).Unix()
    var urls []CrawledUrl
    err := db.Where("project_id = ? AND last_crawled_at < ?", projectID, cutoff).
        Find(&urls).Error
    return urls, err
}
```

---

## Performance Considerations

### Bandwidth Savings

**Expected savings with incremental crawling:**
- Small sites (100-1000 pages): 40-60% bandwidth reduction
- Medium sites (1000-10000 pages): 60-75% bandwidth reduction
- Large sites (10000+ pages): 70-85% bandwidth reduction

**Factors affecting savings:**
- Site update frequency
- Server ETag/Last-Modified support
- Content type distribution

### Memory Overhead

**Per-URL metadata storage:**
- URL string: ~100 bytes
- Metadata: ~200 bytes (timestamps, ETags, scores)
- Total: ~300 bytes per URL

**For 100,000 URLs:**
- Memory: ~30 MB (acceptable)
- Database: ~50 MB (minimal)

### Crawl Speed Improvements

**304 Not Modified responses:**
- Network time: Same (TCP + headers)
- Transfer time: ~95% reduction (no body)
- Processing time: ~99% reduction (no parsing)

**Overall speedup:**
- If 70% unchanged: ~3x faster
- If 50% unchanged: ~2x faster

---

## Testing Strategy

### Unit Tests

1. **Conditional Request Headers**
   - Test If-None-Match header generation
   - Test If-Modified-Since header generation
   - Test 304 response handling

2. **Change Detection**
   - Test content hash comparison
   - Test change frequency calculation
   - Test priority scoring

3. **Metadata Storage**
   - Test URLMetadata storage/retrieval
   - Test concurrent access
   - Test migration from existing data

### Integration Tests

1. **Mock Server Tests**
   - Server supporting ETag
   - Server supporting Last-Modified
   - Server returning 304 responses
   - Server with changing content

2. **Real Website Tests**
   - Test against Wikipedia (good ETag support)
   - Test against news sites (frequent changes)
   - Test against static sites (rare changes)

### Performance Tests

1. **Benchmark crawl speed** with/without incremental
2. **Measure bandwidth usage** with/without incremental
3. **Test memory usage** with large URL sets
4. **Test database performance** with millions of URLs

---

## Security Considerations

### ETag Validation

ETags can potentially leak information:
- Ensure ETags are stored securely
- Don't expose ETags in logs
- Validate ETag format before using

### Last-Modified Manipulation

Servers could manipulate Last-Modified headers:
- Always verify with content hash as backup
- Log discrepancies (304 but hash changed)
- Allow fallback to full crawl if suspicious

### Privacy

Crawl history reveals access patterns:
- Store crawl metadata securely
- Consider data retention policies
- Support clearing old metadata

---

## Future Enhancements

### Phase 2 Features (Post-MVP)

1. **Machine Learning for Change Prediction**
   - Train model on historical change patterns
   - Predict which URLs likely changed
   - Adaptive scheduling based on predictions

2. **Distributed Crawling**
   - Share URL metadata across crawlers
   - Coordinate incremental crawls
   - Deduplicate efforts

3. **Real-Time Change Detection**
   - WebSocket/SSE support for live updates
   - RSS/Atom feed monitoring
   - Webhook notifications from sites

4. **Advanced Analytics**
   - Change pattern visualization
   - Bandwidth savings reports
   - Content freshness heatmaps

5. **Crawl Budget Optimization**
   - Limit crawl to N requests per day
   - Prioritize most valuable URLs
   - Time-based scheduling

---

## References

### Industry Standards
- RFC 7232: HTTP/1.1 Conditional Requests
- RFC 9110: HTTP Semantics (ETag, Last-Modified)
- Sitemap Protocol 0.9

### Research Papers
- "Clustering-based incremental web crawling" (ACM TOIS)
- "Optimal Freshness Crawl Scheduling" (Microsoft Research)
- "Neural Prioritisation for Web Crawling" (arXiv 2024)

### Industry Implementations
- Google Search Central: Crawling and Indexing Documentation
- Bing Webmaster Guidelines
- Screaming Frog SEO Spider
- Ahrefs Site Audit

### Blog Posts & Articles
- "Crawling December: HTTP caching" (Google Search Central)
- "Decoding crawl frequency" (OnCrawl)
- "How Googlebot behavior reflects site health" (SALT.agency)

---

## Appendix A: HTTP Status Code Handling

| Status Code | Meaning | Action |
|------------|---------|--------|
| 200 OK | Content returned | Process normally |
| 304 Not Modified | Content unchanged | Skip processing, update LastCrawledAt |
| 404 Not Found | Page removed | Mark as deleted, consider removing |
| 410 Gone | Permanently removed | Remove from future crawls |
| 301/302 Redirect | Page moved | Follow redirect, update URL |
| 429 Too Many Requests | Rate limited | Back off, respect Retry-After |
| 503 Service Unavailable | Temporary error | Retry later |

---

## Appendix B: Change Frequency Interpretation

| Frequency (changes/day) | Interpretation | Suggested Crawl Interval |
|-------------------------|----------------|--------------------------|
| 0.0 - 0.1 | Static/Rarely changes | Weekly or monthly |
| 0.1 - 0.5 | Occasionally changes | Every 2-7 days |
| 0.5 - 2.0 | Regularly updated | Daily |
| 2.0 - 10.0 | Frequently updated | Multiple times per day |
| 10.0+ | Real-time/Very dynamic | Hourly |

---

## Appendix C: Configuration Examples

### Conservative (Low Server Load)
```go
config := &bluesnake.CollectorConfig{
    EnableIncrementalCrawl: true,
    CrawlMode:              "incremental",
    MaxDepth:               3,
    CheckHead:              true, // Use HEAD first
    Async:                  true,
    // Rate limiting recommended
}
```

### Aggressive (Maximum Freshness)
```go
config := &bluesnake.CollectorConfig{
    EnableIncrementalCrawl: true,
    CrawlMode:              "smart",
    MaxDepth:               0, // Unlimited
    CheckHead:              false, // Skip HEAD, go straight to GET
    Async:                  true,
    // Higher parallelism
}
```

### Balanced (Recommended)
```go
config := &bluesnake.CollectorConfig{
    EnableIncrementalCrawl: true,
    CrawlMode:              "incremental",
    MaxDepth:               5,
    CheckHead:              false, // Conditional GET is efficient enough
    Async:                  true,
    EnableContentHash:      true,
}
```

---

## Conclusion

This specification provides a comprehensive roadmap for implementing incremental crawling in BlueSnake, following industry best practices and solving the "chicken-and-egg" problem through HTTP conditional requests, content hashing, and intelligent scheduling.

**Key Takeaways:**
1. Use HTTP conditional requests (ETag/Last-Modified) as primary change detection
2. Layer content hashing as backup verification
3. Track change frequency for intelligent scheduling
4. Support multiple crawl modes (full, incremental, smart)
5. Optimize for both bandwidth and crawl speed

**Implementation Priority:**
1. Phase 1-2: Database and storage (foundation)
2. Phase 3: HTTP conditional requests (core feature)
3. Phase 4-5: Change tracking and prioritization (optimization)
4. Phase 6-7: Advanced features (sitemap, smart mode)

The system is designed to be backward-compatible, with incremental crawling as an opt-in feature that doesn't affect existing functionality.
