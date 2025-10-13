# Framework Detection - Requirements Specification

## Overview
Detect the web framework/platform used by a website during crawl initialization to enable framework-specific optimizations and filtering rules.

## Goals
1. Automatically detect website framework/platform when creating a new project
2. Support detection of popular code and no-code website builders
3. Show detected framework in UI with ability for users to manually override
4. Use framework information to optimize crawling behavior (e.g., filter Next.js RSC URLs - this is the only reason we came to build this feature - for now)
5. Store framework detection in project metadata for future crawls

## Non-Goals (Phase 1)
- Detecting multiple frameworks on the same site (just detect primary)
- Version detection (e.g., Next.js 13 vs 14)
- Deep analysis of framework configuration
- Automatic re-detection on subsequent crawls (use cached value unless user changes it)

---

## Target Frameworks/Platforms

### JavaScript Frameworks (Code)
- **Next.js** - React framework with SSR/SSG capabilities
  - Detection signals: `/_next/`, `__next` div, `_rsc=` query params
  - Filtering rules: Filter `_rsc=` prefetch URLs

- **Nuxt.js** - Vue framework with SSR/SSG
  - Detection signals: `/_nuxt/`, `__nuxt` div
  - Filtering rules: Filter `/__nuxt_` prefetch URLs

- **Gatsby** - Static site generator for React
  - Detection signals: `/___gatsby`, `gatsby-` classes
  - Filtering rules: None needed

- **React (Client-side)** - Pure React without framework
  - Detection signals: `<div id="root">`, React DevTools markers
  - Filtering rules: None

- **Vue.js (Client-side)** - Pure Vue without framework
  - Detection signals: `v-` attributes, Vue DevTools markers
  - Filtering rules: None

- **Angular** - Google's SPA framework
  - Detection signals: `ng-` attributes, Angular DevTools
  - Filtering rules: None

- **Svelte/SvelteKit** - Compiler-based framework
  - Detection signals: `/\_app/`, `svelte-` classes
  - Filtering rules: TBD

- **Astro** - Multi-framework static site builder
  - Detection signals: `astro-` attributes
  - Filtering rules: None

### Static Site Generators
- **Jekyll** - Ruby-based SSG
  - Detection signals: Generator meta tag, typical folder structure
  - Filtering rules: None

- **Hugo** - Go-based SSG
  - Detection signals: Generator meta tag
  - Filtering rules: None

- **Eleventy (11ty)** - JavaScript SSG
  - Detection signals: Generator meta tag
  - Filtering rules: None

### CMS Platforms (Code)
- **WordPress** - PHP CMS
  - Detection signals: `/wp-content/`, `/wp-includes/`, generator meta
  - Filtering rules: Filter `?ver=` query params on assets

- **Drupal** - PHP CMS
  - Detection signals: `/sites/all/`, Drupal.settings JS object
  - Filtering rules: None

- **Joomla** - PHP CMS
  - Detection signals: `/media/joomla/`, generator meta
  - Filtering rules: None

### No-Code/Low-Code Platforms
- **Webflow** - Visual web design tool
  - Detection signals: `webflow.js`, `.w-` classes
  - Filtering rules: Filter Webflow's asset versioning URLs

- **Wix** - Website builder
  - Detection signals: `wix.com` in URLs, `_wix` attributes
  - Filtering rules: Filter Wix's internal tracking URLs

- **Squarespace** - Website builder
  - Detection signals: `squarespace.com` domains, specific class patterns
  - Filtering rules: None

- **Shopify** - E-commerce platform
  - Detection signals: `/cdn.shopify.com/`, Shopify.theme object
  - Filtering rules: Filter `?v=` query params on assets

- **Framer** - Design-to-code platform
  - Detection signals: `framer.com` in URLs, Framer runtime
  - Filtering rules: TBD

- **Bubble** - No-code web app builder
  - Detection signals: `bubble.io` domains
  - Filtering rules: TBD

### E-commerce
- **WooCommerce** (WordPress plugin)
  - Detection signals: `/wp-content/plugins/woocommerce/`
  - Filtering rules: Same as WordPress

- **Magento** - PHP e-commerce
  - Detection signals: `/static/version`, Magento_* classes
  - Filtering rules: Filter version query params

### Server Frameworks
- **Django** - Python framework
  - Detection signals: `/static/admin/`, `csrfmiddlewaretoken`
  - Filtering rules: None

- **Ruby on Rails** - Ruby framework
  - Detection signals: `csrf-token` meta, Rails asset pipeline
  - Filtering rules: Filter Rails asset digests

---

## Detection Heuristics

### Detection Order (First Match Wins)
1. **Meta Tags** - Check `<meta name="generator">` tag
2. **Asset URLs** - Check for framework-specific asset paths (/_next/, /_nuxt/, etc.)
3. **DOM Patterns** - Check for framework-specific div IDs/classes
4. **Script Sources** - Check for framework runtime scripts
5. **HTTP Headers** - Check X-Powered-By, Server headers
6. **HTML Comments** - Some platforms leave HTML comments

### Detection Timing
- **Initial Detection**: On first HTML page load (homepage)
- **Confidence Score**: Track confidence (high/medium/low) based on number of signals matched
- **Fallback**: If no framework detected, mark as "Unknown"

### Example Detection Logic for Next.js
```
Confidence Score = 0
If HTML contains "/_next/static/" → +3 points
If HTML contains '<div id="__next"' → +2 points
If Network requests include "_rsc=" → +2 points
If HTML contains 'next-data' script → +2 points

Score >= 5 → High confidence
Score 3-4 → Medium confidence
Score 1-2 → Low confidence
Score 0 → Unknown
```

---

## Data Model Changes

### Database Schema

**projects table** - Add columns:
```sql
ALTER TABLE projects ADD COLUMN framework VARCHAR(50);
ALTER TABLE projects ADD COLUMN framework_confidence VARCHAR(20); -- high/medium/low
ALTER TABLE projects ADD COLUMN framework_detected_at TIMESTAMP;
ALTER TABLE projects ADD COLUMN framework_override_by_user BOOLEAN DEFAULT FALSE;
```

**config table** - Add framework filtering rules:
```sql
CREATE TABLE framework_filters (
    id INTEGER PRIMARY KEY,
    framework VARCHAR(50) NOT NULL,
    filter_type VARCHAR(50) NOT NULL, -- url_pattern, query_param, etc.
    pattern TEXT NOT NULL,
    description TEXT,
    enabled BOOLEAN DEFAULT TRUE
);
```

### Go Structs

**internal/store/models.go**:
```go
type Project struct {
    // ... existing fields
    Framework              string    `json:"framework,omitempty"`
    FrameworkConfidence    string    `json:"frameworkConfidence,omitempty"` // high/medium/low
    FrameworkDetectedAt    time.Time `json:"frameworkDetectedAt,omitempty"`
    FrameworkOverrideByUser bool     `json:"frameworkOverrideByUser"`
}

type FrameworkFilter struct {
    ID          int    `json:"id"`
    Framework   string `json:"framework"`
    FilterType  string `json:"filterType"`
    Pattern     string `json:"pattern"`
    Description string `json:"description"`
    Enabled     bool   `json:"enabled"`
}
```

---

## API Changes

### New Endpoints

**GET /api/v1/projects/:id/framework**
- Returns detected framework info
- Response:
```json
{
  "framework": "nextjs",
  "confidence": "high",
  "detectedAt": "2025-10-13T14:00:00Z",
  "overrideByUser": false,
  "detectionSignals": [
    "Found /_next/static/ in HTML",
    "Found <div id=\"__next\">",
    "Found _rsc= query params in network requests"
  ]
}
```

**PUT /api/v1/projects/:id/framework**
- Manually set framework (user override)
- Request body:
```json
{
  "framework": "nextjs"
}
```

**GET /api/v1/frameworks**
- List all supported frameworks
- Response:
```json
{
  "frameworks": [
    {
      "id": "nextjs",
      "name": "Next.js",
      "category": "JavaScript Framework",
      "description": "React framework with SSR/SSG",
      "filteringRules": ["_rsc= prefetch URLs"]
    },
    // ... more frameworks
  ]
}
```

**GET /api/v1/frameworks/:id/filters**
- Get filtering rules for a specific framework
- Response:
```json
{
  "framework": "nextjs",
  "filters": [
    {
      "id": 1,
      "filterType": "url_pattern",
      "pattern": "_rsc=",
      "description": "React Server Components prefetch URLs",
      "enabled": true
    }
  ]
}
```

---

## Crawler Changes

### New File: `framework_detector.go`

```go
package bluesnake

type Framework string

const (
    FrameworkUnknown    Framework = "unknown"
    FrameworkNextJS     Framework = "nextjs"
    FrameworkNuxtJS     Framework = "nuxtjs"
    FrameworkGatsby     Framework = "gatsby"
    FrameworkReact      Framework = "react"
    FrameworkVue        Framework = "vue"
    FrameworkAngular    Framework = "angular"
    FrameworkWordPress  Framework = "wordpress"
    FrameworkWebflow    Framework = "webflow"
    FrameworkShopify    Framework = "shopify"
    // ... more frameworks
)

type FrameworkDetector struct {
    detectedFramework Framework
    confidence        string // high/medium/low
    signals           []string
}

func (fd *FrameworkDetector) Detect(html string, networkURLs []string) {
    // Detection logic here
}

func (fd *FrameworkDetector) GetFramework() Framework {
    return fd.detectedFramework
}

func (fd *FrameworkDetector) GetConfidence() string {
    return fd.confidence
}

func (fd *FrameworkDetector) GetSignals() []string {
    return fd.signals
}
```

### Crawler Integration

**crawler.go** - Add framework-aware filtering:

```go
type Crawler struct {
    // ... existing fields
    detectedFramework Framework
    frameworkDetector *FrameworkDetector
}

// On first HTML page, detect framework
cr.Collector.OnHTML("html", func(e *HTMLElement) {
    if cr.detectedFramework == FrameworkUnknown {
        networkURLs := extractNetworkURLsFromContext(e)
        cr.frameworkDetector.Detect(e.DOM.Html(), networkURLs)
        cr.detectedFramework = cr.frameworkDetector.GetFramework()

        // Store in database
        storeFrameworkDetection(cr.projectID, cr.detectedFramework, cr.frameworkDetector.GetConfidence())
    }

    // Continue with link extraction...
})

// Update isAnalyticsOrTracking to be framework-aware
func (cr *Crawler) isAnalyticsOrTracking(urlStr string) bool {
    urlLower := strings.ToLower(urlStr)

    // Framework-specific filters
    if cr.detectedFramework == FrameworkNextJS && strings.Contains(urlLower, "_rsc=") {
        return true
    }

    if cr.detectedFramework == FrameworkNuxtJS && strings.Contains(urlLower, "__nuxt_") {
        return true
    }

    // Generic analytics patterns (unchanged)
    // ...
}
```

---

## UI Changes

### Project Dashboard
Add a new section showing detected framework:

```
Project: reachpsych.com
Framework: Next.js (High Confidence) [Edit]
Detected: 2 hours ago
```

### Framework Edit Modal
When user clicks [Edit]:
```
┌─────────────────────────────────────────┐
│ Framework Detection                     │
├─────────────────────────────────────────┤
│                                         │
│ Detected Framework: Next.js             │
│ Confidence: High                        │
│                                         │
│ Detection Signals:                      │
│ ✓ Found /_next/static/ in HTML         │
│ ✓ Found <div id="__next">              │
│ ✓ Found _rsc= query params              │
│                                         │
│ Override Framework:                     │
│ [Dropdown: Next.js ▼]                   │
│                                         │
│ Active Filtering Rules:                 │
│ ☑ Filter _rsc= prefetch URLs            │
│                                         │
│ [Cancel]  [Save]                        │
└─────────────────────────────────────────┘
```

### Crawl Configuration Page
Show framework info and filtering rules before starting crawl:

```
┌─────────────────────────────────────────┐
│ Start New Crawl                         │
├─────────────────────────────────────────┤
│ URL: [https://example.com]              │
│                                         │
│ Framework: Auto-detect ▼                │
│   • Auto-detect (recommended)           │
│   • Next.js                             │
│   • WordPress                           │
│   • Unknown                             │
│   • ... more                            │
│                                         │
│ [Start Crawl]                           │
└─────────────────────────────────────────┘
```

---

## Comparison Script Changes

### compare_crawlers.py Updates

```python
class CrawlerComparison:
    def __init__(self, domain):
        self.domain = domain
        self.detected_framework = None  # Will be fetched from BlueSnake API

    def fetch_framework_info(self, project_id: int) -> Optional[str]:
        """Fetch detected framework from BlueSnake API"""
        response = requests.get(
            f"{self.server_url}/api/v1/projects/{project_id}/framework"
        )
        if response.status_code == 200:
            data = response.json()
            return data.get("framework")
        return None

    def should_filter_url(self, url: str) -> bool:
        """
        Check if a URL should be filtered based on detected framework
        """
        url_lower = url.lower()

        # Next.js specific filters
        if self.detected_framework == "nextjs":
            if "_rsc=" in url_lower:
                return True

        # Nuxt.js specific filters
        if self.detected_framework == "nuxtjs":
            if "__nuxt_" in url_lower:
                return True

        # WordPress specific filters
        if self.detected_framework == "wordpress":
            if "?ver=" in url_lower:
                return True

        # Generic analytics/tracking (framework-agnostic)
        analytics_patterns = [
            "/g/collect",
            "/gtm.js",
            "/gtag/js",
            "google-analytics",
            "googletagmanager",
        ]

        for pattern in analytics_patterns:
            if pattern in url_lower:
                return True

        return False
```

---

## Implementation Phases

### Phase 1: Core Detection
- [ ] Create `framework_detector.go` with detection logic
- [ ] Implement detection for top 5 frameworks (Next.js, Nuxt.js, WordPress, Webflow, React)
- [ ] Add database schema changes
- [ ] Store framework detection in projects table
- [ ] Add API endpoint to get/set framework

### Phase 2: Framework-Aware Filtering
- [ ] Update `isAnalyticsOrTracking()` to use framework info
- [ ] Create `framework_filters` table and seed with initial rules
- [ ] Implement framework-specific filtering logic
- [ ] Update comparison script to use framework info

### Phase 3: UI Integration
- [ ] Add framework display to project dashboard
- [ ] Create framework edit modal
- [ ] Add framework selection to crawl configuration
- [ ] Show framework filtering rules in UI

### Phase 4: Extended Framework Support
- [ ] Add remaining frameworks (20+ total)
- [ ] Implement confidence scoring
- [ ] Add framework-specific optimization hints
- [ ] Analytics on framework distribution across projects

---

## Testing Strategy

### Unit Tests
- Test framework detection logic with HTML fixtures
- Test each framework's detection heuristics independently
- Test filtering rules for each framework

### Integration Tests
- Test full crawl with Next.js site (should filter _rsc= URLs)
- Test full crawl with WordPress site (should filter ?ver= URLs)
- Test full crawl with unknown framework (should not apply framework filters)
- Test manual framework override

### Test Sites
- **Next.js**: reachpsych.com (existing test site)
- **WordPress**: wordpress.org/news
- **Webflow**: webflow.com (meta site)
- **React SPA**: reactjs.org
- **Unknown**: Simple static HTML site

---

## Success Metrics

1. **Detection Accuracy**: >90% correct framework detection on top 10 frameworks
2. **Coverage Improvement**: Achieve >85% URL coverage parity with ScreamingFrog after filtering
3. **User Overrides**: <10% of projects require manual framework override
4. **Performance**: Framework detection adds <500ms to initial crawl time

---

## Edge Cases & Considerations

### Multi-Framework Sites
- Some sites use Next.js for landing page but WordPress for blog
- **Solution**: Detect primary framework only, allow manual override

### Framework Transitions
- Site migrates from WordPress to Next.js between crawls
- **Solution**: Always use stored framework unless user manually re-detects

### Hybrid Rendering
- Next.js with SSR and CSR mixed
- **Solution**: Detection should work regardless (look for /_next/ assets)

### Custom Frameworks
- Companies with internal frameworks
- **Solution**: Allow "Unknown" and disable framework-specific filtering

---

## Future Enhancements

### Version Detection
- Detect framework versions (e.g., Next.js 13 vs 14)
- Version-specific filtering rules (App Router vs Pages Router)

### Framework Analytics
- Dashboard showing framework distribution
- "Sites like yours use Next.js 95% of the time"

### Auto-Configuration
- Suggest optimal crawl settings based on framework
- "Next.js detected: We recommend enabling JS rendering"

### Plugin System
- Allow users to define custom framework detection rules
- Community-contributed framework definitions

---

## Open Questions

1. Should we re-detect framework on every crawl or cache it?
   - **Recommendation**: Cache and allow manual re-detection

2. What if framework detection has low confidence?
   - **Recommendation**: Show warning in UI, ask user to verify

3. Should we detect framework on homepage only or multiple pages?
   - **Recommendation**: Homepage only for Phase 1, extend later

4. How to handle sites that intentionally hide framework signatures?
   - **Recommendation**: Fall back to "Unknown" and use generic filtering

---
