# BlueSnake Reports Design Specification

## Overview

This document outlines the design specification for the **Reports** section in BlueSnake, a new feature that provides comprehensive SEO analysis and issue reporting capabilities.

---

## Report-Worthy Items from ENHANCEMENTS.md

### High Priority Report Items (Phase 1)

These items should be included in the initial report implementation:

#### 1. SEO Score/Health Rating (Enhancement #8)
- Overall SEO health score (0-100)
- Score breakdown by category:
  - Technical SEO score
  - Content quality score
  - Link health score
- Score trend over time (when comparing multiple crawls)
- Visual gauge/chart representation

**Priority:** Very High
**Implementation Effort:** Medium (requires scoring algorithms)

#### 2. Broken Link Detection (Enhancement #1)
- Identify all 404 and network errors
- Show source pages linking to broken URLs
- Count of broken links per source page
- Grouping by error type (404, timeout, DNS failure, etc.)

**Priority:** High
**Severity:** Critical
**Implementation Effort:** Easy (data already available in database)

#### 3. Redirect Chain Detection (Enhancement #2)
- Display full redirect chains (A → B → C)
- Identify redirect loops
- Distinguish permanent (301) vs temporary (302) redirects
- Report redirect chain length
- Flag excessive redirects (>3 hops)

**Priority:** High
**Severity:** High
**Implementation Effort:** Easy (requires redirect tracking enhancement)

#### 4. Missing Meta Tags (Enhancement #3)
- Pages missing title tags
- Pages missing meta descriptions
- Duplicate titles across pages
- Duplicate meta descriptions
- Title/description length issues:
  - Title too short (<30 chars) or too long (>60 chars)
  - Description too short (<50 chars) or too long (>160 chars)

**Priority:** High
**Severity:** High
**Implementation Effort:** Easy (basic validation on existing data)

#### 5. Duplicate Content Detection (Enhancement #5)
- Pages sharing the same content hash
- Group duplicate pages together
- Show all URLs in each duplicate group
- Suggest canonical URL for duplicate groups (based on URL patterns, inlinks)

**Priority:** High
**Severity:** High
**Implementation Effort:** Easy (content hashing already implemented)

#### 6. Heading Structure Validation (Enhancement #6)
- Pages missing H1 tags
- Pages with multiple H1 tags
- Improper heading nesting (e.g., H1 → H3 skip)
- Heading hierarchy visualization
- Empty headings

**Priority:** High
**Severity:** High
**Implementation Effort:** Medium (requires heading extraction)

#### 7. Image Alt Text Validation (Enhancement #9)
- Images missing alt attributes
- Images with empty alt attributes (`alt=""`)
- Identify decorative images
- Alt text quality scoring (length, keyword stuffing detection)

**Priority:** Medium
**Severity:** Medium
**Implementation Effort:** Medium (requires image parsing)

#### 8. Canonical Tag Analysis (Enhancement #10)
- Pages missing canonical tags
- Canonical conflicts (multiple canonical tags on same page)
- Self-referencing canonical issues
- Canonical chains (A → B → C)
- Non-indexable pages with canonicals

**Priority:** High
**Severity:** High
**Implementation Effort:** Medium (requires canonical extraction)

#### 9. Robots Meta Tag Analysis (Enhancement #11)
- Extract and report noindex/nofollow directives
- Identify conflicting directives (robots.txt vs meta robots)
- Flag pages with noindex but included in sitemap
- Report noarchive/noimageindex directives

**Priority:** Medium
**Severity:** Medium
**Implementation Effort:** Medium (requires robots meta extraction)

---

### Secondary Report Items (Phase 2+)

These items can be added in future phases:

#### 10. Page Speed & Performance Metrics (Enhancement #15)
- Core Web Vitals:
  - Largest Contentful Paint (LCP)
  - First Input Delay (FID)
  - Cumulative Layout Shift (CLS)
- Additional metrics:
  - Time to First Byte (TTFB)
  - First Contentful Paint (FCP)
  - Time to Interactive (TTI)
- Performance scoring (0-100)
- Performance comparison over time

**Priority:** Very High
**Implementation Effort:** Hard (requires chromedp instrumentation)

#### 11. Internal Link Analysis (Enhancement #18)
- Identify orphan pages (pages with no inlinks)
- Calculate PageRank/authority scores
- Link depth analysis (clicks from homepage)
- Broken link chains
- Internal linking opportunities

**Priority:** Very High
**Implementation Effort:** Hard (requires graph algorithms)

#### 12. Structured Data Validation (Enhancement #16)
- Extract JSON-LD structured data
- Parse Microdata and RDFa formats
- Validate against schema.org schemas
- Identify missing structured data opportunities
- Report structured data errors

**Priority:** High
**Implementation Effort:** Hard (requires schema validation)

#### 13. Sitemap Issues (Enhancement #17)
- URLs crawled but not in sitemap
- Invalid URLs in sitemap (404s, redirects)
- Blocked URLs in sitemap (robots.txt conflicts)
- Sitemap size/count validation
- Image/video sitemap analysis

**Priority:** High
**Implementation Effort:** Medium (requires sitemap comparison)

---

## UI Design Specification

### Section Placement

Add a new **"Reports"** navigation item to the sidebar between "Crawl Results" and "Configuration":

```
Sidebar Navigation:
├── Home
├── Crawl Results
├── Reports ← NEW SECTION
├── Configuration
└── AI Crawlers
```

### Report Dashboard Architecture

The reports section follows a **three-level progressive disclosure** pattern:

1. **Level 1: Overview Dashboard** - High-level summary with key metrics
2. **Level 2: Category Detail View** - Specific issue category with all affected items
3. **Level 3: Issue Detail View** - Individual issue with full context and recommendations

---

### Level 1: Overview Dashboard (Landing View)

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Reports Overview - example.com                                          │
│ Crawled on Dec 15, 2024 at 3:45 PM               [Select Crawl ▼]      │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  SEO Health Score                                                  │ │
│  │                                                                     │ │
│  │         ┌──────────────┐                                           │ │
│  │         │              │                                           │ │
│  │         │      78      │  ← Circular gauge visualization          │ │
│  │         │              │                                           │ │
│  │         └──────────────┘                                           │ │
│  │                                                                     │ │
│  │  Score Breakdown:                                                  │ │
│  │  ● Technical SEO: 85/100                                           │ │
│  │  ● Content Quality: 72/100                                         │ │
│  │  ● Link Health: 65/100                                             │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌─────────────────────────┐  ┌───────────────────────────────────┐   │
│  │ Issues Summary          │  │  Issue Severity Distribution      │   │
│  │                         │  │                                   │   │
│  │ 🔴 Critical: 12         │  │  ┌─────────────────────────────┐ │   │
│  │ 🟡 High: 34             │  │  │                             │ │   │
│  │ 🟢 Medium: 56           │  │  │     [Pie Chart Visual]      │ │   │
│  │ ⚪ Low: 23              │  │  │                             │ │   │
│  │                         │  │  │  🔴 Critical: 12 (10%)      │ │   │
│  │ Total Issues: 125       │  │  │  🟡 High: 34 (27%)          │ │   │
│  │                         │  │  │  🟢 Medium: 56 (45%)        │ │   │
│  └─────────────────────────┘  │  │  ⚪ Low: 23 (18%)           │ │   │
│                                │  │                             │ │   │
│                                │  └─────────────────────────────┘ │   │
│                                └───────────────────────────────────┘   │
│                                                                         │
│  Issue Categories                                                      │
│  Click on any category to view detailed breakdown                      │
│                                                                         │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │ 🔴 Broken Links                            12 issues      [→]  │   │
│  │    Pages with 404 errors or network failures                   │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟡 Redirect Chains                          8 issues      [→]  │   │
│  │    URLs with multiple redirects                                │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟡 Missing Meta Tags                       45 issues      [→]  │   │
│  │    Pages missing titles or descriptions                        │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟡 Duplicate Content                       12 groups     [→]  │   │
│  │    Pages with identical content hashes                         │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟡 Heading Structure Issues                23 issues      [→]  │   │
│  │    Invalid H1-H6 hierarchy                                     │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟢 Image Alt Text Issues                   18 issues      [→]  │   │
│  │    Images missing or with empty alt text                       │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟡 Canonical Tag Issues                     7 issues      [→]  │   │
│  │    Canonical tag conflicts or chains                           │   │
│  ├────────────────────────────────────────────────────────────────┤   │
│  │ 🟢 Robots Meta Tag Issues                   3 issues      [→]  │   │
│  │    Indexation directive conflicts                              │   │
│  └────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌────────────────────────────────────────────────────────────────┐   │
│  │ Compare with Previous Crawl                                     │   │
│  │                                                                  │   │
│  │ New Issues: +5    Fixed Issues: -12    Total Change: -7       │   │
│  │                                                                  │   │
│  │ [View Detailed Comparison]                                      │   │
│  └────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  [Export Full Report as PDF] [Export as CSV] [Share Report Link]       │
└─────────────────────────────────────────────────────────────────────────┘
```

#### Overview Dashboard Components

**Header Section:**
- Domain name/URL
- Crawl date/time
- Dropdown to select different crawls for comparison

**SEO Health Score Card:**
- Large circular gauge showing overall score (0-100)
- Color-coded:
  - 0-40: Red (Critical)
  - 41-60: Orange (Needs Improvement)
  - 61-80: Yellow (Good)
  - 81-100: Green (Excellent)
- Breakdown scores for:
  - Technical SEO (crawlability, indexability, redirects)
  - Content Quality (meta tags, headings, duplicates)
  - Link Health (broken links, internal linking)

**Issues Summary Card:**
- Count of issues by severity
- Total issues count
- Visual severity indicators (colored dots/badges)

**Severity Distribution Chart:**
- Pie chart showing distribution of issues
- Percentages for each severity level
- Click to filter by severity

**Issue Categories List:**
- Expandable cards for each issue type
- Each card shows:
  - Severity indicator (colored dot)
  - Category name
  - Issue count
  - Brief description
  - Arrow/chevron to navigate to detail view
- Sorted by severity (Critical → Low)

**Comparison Section (if previous crawl exists):**
- Shows changes from previous crawl
- New issues count (red +5)
- Fixed issues count (green -12)
- Net change
- Link to detailed comparison view

**Action Buttons:**
- Export full report as PDF (formatted, branded report)
- Export as CSV (raw data for analysis)
- Share report link (future feature)

---

### Level 2: Category Detail View

Example: **Broken Links Category**

```
┌─────────────────────────────────────────────────────────────────────────┐
│ ← Back to Reports Overview                                              │
│                                                                         │
│ Broken Links Report                                      🔴 Critical   │
│ 12 issues found                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ What are Broken Links?                                             │ │
│  │                                                                     │ │
│  │ Broken links are URLs that return error status codes (404 Not     │ │
│  │ Found, 500 Server Error, etc.) or network failures. They hurt     │ │
│  │ user experience and can negatively impact SEO rankings.           │ │
│  │                                                                     │ │
│  │ Impact: Critical - Fix immediately                                │ │
│  │ Effort: Easy - Update or remove links                             │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ 🔍 Search broken links...                          [Filters ▼]    │ │
│  │                                                                     │ │
│  │ Filter by: [ All Errors ▼ ] [ All Sources ▼ ]                     │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Broken URL             Error Type    Status  Sources (Inlinks)    │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /old-page-404          Not Found     404     5 pages        [→]  │ │
│  │ Link Text: "View details", "Read more"                            │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /missing-image.jpg     Not Found     404     12 pages       [→]  │ │
│  │ Link Text: [Image]                                                │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /broken-link           Network Error Error    2 pages        [→]  │ │
│  │ Link Text: "Click here"                                           │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /timeout-url           Timeout       Error    1 page         [→]  │ │
│  │ Link Text: "Learn more"                                           │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /server-error          Server Error  500      3 pages        [→]  │ │
│  │ Link Text: "Contact us"                                           │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  Showing 5 of 12 broken links                        [Load More ▼]     │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ 💡 Recommendations                                                 │ │
│  │                                                                     │ │
│  │ 1. Review each broken link and determine if the page should be   │ │
│  │    restored or if the links should be updated/removed            │ │
│  │ 2. Set up 301 redirects for important pages that have moved      │ │
│  │ 3. Update internal links to point to valid URLs                  │ │
│  │ 4. Remove or replace external broken links                       │ │
│  │ 5. Monitor your site regularly for new broken links              │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  [Export This Category as CSV] [Mark All as Reviewed]                  │
└─────────────────────────────────────────────────────────────────────────┘
```

#### Category Detail Components

**Header:**
- Back navigation to Overview
- Category name
- Severity badge
- Issue count

**Description Card:**
- Explanation of what this issue type is
- Why it matters (impact on SEO/UX)
- Impact level (Critical/High/Medium/Low)
- Effort to fix (Easy/Medium/Hard)

**Search & Filter Bar:**
- Search input for filtering results
- Filters dropdown:
  - By error type (404, 500, timeout, etc.)
  - By source count (pages with most broken links)
  - By URL pattern

**Issue Table:**
- Columns:
  - Broken URL (clickable to detail view)
  - Error Type (human-readable)
  - Status code
  - Sources count (number of pages linking to it)
  - Action arrow to detail view
- Additional info row:
  - Preview of anchor text used in links
  - Link type (anchor, image, script, etc.)
- Sortable columns
- Pagination or infinite scroll

**Recommendations Card:**
- Actionable steps to fix this type of issue
- Best practices
- Prioritization guidance

**Action Buttons:**
- Export this category as CSV
- Mark all as reviewed/acknowledged (future feature)

---

### Level 3: Issue Detail View

Example: **Individual Broken Link Detail**

```
┌─────────────────────────────────────────────────────────────────────────┐
│ ← Back to Broken Links                                                  │
│                                                                         │
│ /old-page-404                                          🔴 Critical     │
│ 404 Not Found                                                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Issue Details                                                      │ │
│  │                                                                     │ │
│  │ URL:              https://example.com/old-page-404                │ │
│  │ Status Code:      404 Not Found                                   │ │
│  │ Error Message:    The requested resource was not found            │ │
│  │ First Discovered: Dec 10, 2024 at 2:15 PM                         │ │
│  │ Last Checked:     Dec 15, 2024 at 3:45 PM                         │ │
│  │ Content Type:     text/html                                       │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ 💡 Recommendation                                                  │ │
│  │                                                                     │ │
│  │ This page is linked from 5 other pages on your site. We           │ │
│  │ recommend one of the following actions:                           │ │
│  │                                                                     │ │
│  │ 1. Restore the page at this URL                                   │ │
│  │ 2. Set up a 301 redirect to a relevant replacement page          │ │
│  │ 3. Update all 5 source pages to remove or replace the link       │ │
│  │                                                                     │ │
│  │ Priority: High - This affects multiple pages                      │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Source Pages (5 inlinks)                                           │ │
│  │                                                                     │ │
│  │ These pages link to this broken URL:                              │ │
│  │                                                                     │ │
│  │ ┌─────────────────────────────────────────────────────────────┐  │ │
│  │ │ Source URL                    Anchor Text       Position    │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /blog/article-1              "Read more"        Content     │  │ │
│  │ │ Status: 200 | Type: HTML                        [View →]    │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /products/category-a         "View product"     Content     │  │ │
│  │ │ Status: 200 | Type: HTML                        [View →]    │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /homepage                    "Old page"         Navigation  │  │ │
│  │ │ Status: 200 | Type: HTML                        [View →]    │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /sitemap.html                "Archive"          Content     │  │ │
│  │ │ Status: 200 | Type: HTML                        [View →]    │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /footer-links                "Resources"        Navigation  │  │ │
│  │ │ Status: 200 | Type: HTML                        [View →]    │  │ │
│  │ └─────────────────────────────────────────────────────────────┘  │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Historical Data                                                    │ │
│  │                                                                     │ │
│  │ This URL has been checked in 3 previous crawls:                   │ │
│  │                                                                     │ │
│  │ • Dec 15, 2024: 404 Not Found                                     │ │
│  │ • Dec 10, 2024: 404 Not Found                                     │ │
│  │ • Dec 5, 2024: 200 OK (Page was accessible)                       │ │
│  │                                                                     │ │
│  │ This page became unavailable between Dec 5 and Dec 10, 2024       │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  [Open URL in Browser] [View in Crawl Results] [Mark as Resolved]      │
└─────────────────────────────────────────────────────────────────────────┘
```

#### Issue Detail Components

**Header:**
- Back navigation to Category view
- Issue URL
- Status code and error type
- Severity badge

**Issue Details Card:**
- Full URL
- Status code
- Error message
- First discovered date
- Last checked date
- Content type
- Any other relevant metadata

**Recommendation Card:**
- Specific, actionable recommendation for this issue
- Multiple options if applicable
- Priority indication based on impact

**Source Pages Table:**
- List of all pages linking to this broken URL
- For each source:
  - Source URL (clickable)
  - Anchor text used
  - Link position (content, navigation, footer, etc.)
  - Source page status
  - View button to see source page in Crawl Results
- Sortable and searchable

**Historical Data Card (if available):**
- Status history across multiple crawls
- When the issue first appeared
- Whether it was previously fixed and reappeared
- Trend analysis

**Action Buttons:**
- Open URL in browser (to verify current state)
- View in Crawl Results (see full page data)
- Mark as resolved (for tracking fixes)

---

## Category-Specific Layouts

### Missing Meta Tags Detail

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Missing Meta Tags Report                              🟡 High          │
│ 45 issues found                                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Sub-Categories:                                                        │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ [ All Issues ▼ ]                                                   │ │
│  │                                                                     │ │
│  │ • Missing Title Tag (12 pages)                                    │ │
│  │ • Missing Meta Description (15 pages)                             │ │
│  │ • Duplicate Titles (8 pages)                                      │ │
│  │ • Duplicate Descriptions (6 pages)                                │ │
│  │ • Title Too Short (3 pages)                                       │ │
│  │ • Description Too Long (1 page)                                   │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ URL                     Issue Type        Current Value  Status   │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /product/item-123      Missing Title      (empty)        [Fix]   │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /blog/post-456         Title Too Short    "Blog"         [Fix]   │ │
│  │ Current: 4 chars | Recommended: 30-60 chars                       │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ /about                 Duplicate Title    "About Us"     [Fix]   │ │
│  │ Also used by: /about-us, /company                                 │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

**Features:**
- Sub-category filtering
- Issue-specific details (character counts, duplicates)
- Inline recommendations

### Duplicate Content Detail

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Duplicate Content Report                              🟡 High          │
│ 12 groups of duplicate pages (36 total pages)                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Group 1 of 12                        Content Hash: a3f2b1...      │ │
│  │ 4 pages with identical content                                    │ │
│  │                                                                     │ │
│  │ Suggested Canonical: /products/widget (most inlinks)              │ │
│  │                                                                     │ │
│  │ URLs in this group:                                               │ │
│  │ ┌─────────────────────────────────────────────────────────────┐  │ │
│  │ │ URL                    Inlinks  Canonical Tag    Action     │  │ │
│  │ ├─────────────────────────────────────────────────────────────┤  │ │
│  │ │ /products/widget      15        self             [Keep]     │  │ │
│  │ │ /shop/widget          3         none             [Fix]      │  │ │
│  │ │ /items/widget-123     1         none             [Fix]      │  │ │
│  │ │ /catalog/widget       0         none             [Fix]      │  │ │
│  │ └─────────────────────────────────────────────────────────────┘  │ │
│  │                                                                     │ │
│  │ Recommendation: Set canonical to /products/widget on all pages   │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  [Previous Group] [Next Group] [View All Groups]                       │
└─────────────────────────────────────────────────────────────────────────┘
```

**Features:**
- Group-based navigation
- Canonical tag suggestions
- Inlink count for prioritization
- Recommended actions

### Heading Structure Detail

```
┌─────────────────────────────────────────────────────────────────────────┐
│ Heading Structure Issues                              🟡 High          │
│ 23 pages with heading problems                                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ URL: /blog/article-123                            [View Page →]  │ │
│  │                                                                     │ │
│  │ Issues Found:                                                      │ │
│  │ ✗ Multiple H1 tags (2 found)                                      │ │
│  │ ✗ Heading skip: H2 → H4                                           │ │
│  │                                                                     │ │
│  │ Current Heading Structure:                                        │ │
│  │                                                                     │ │
│  │ H1: "Welcome to Our Blog"                                         │ │
│  │ H1: "Article Title"                          ← Duplicate H1       │ │
│  │   H2: "Introduction"                                              │ │
│  │     H4: "Key Points"                         ← Skipped H3         │ │
│  │     H4: "Summary"                                                 │ │
│  │   H2: "Conclusion"                                                │ │
│  │                                                                     │ │
│  │ Recommended Structure:                                            │ │
│  │                                                                     │ │
│  │ H1: "Article Title"                          ← Single H1          │ │
│  │   H2: "Introduction"                                              │ │
│  │     H3: "Key Points"                         ← Proper nesting     │ │
│  │     H3: "Summary"                                                 │ │
│  │   H2: "Conclusion"                                                │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

**Features:**
- Visual heading hierarchy
- Side-by-side current vs recommended
- Specific issue highlighting

---

## Design Principles

### 1. Progressive Disclosure
Start with high-level overview, allow users to drill down to increasingly specific details:
- Overview → Category → Individual Issue
- Each level reveals more context and actionable data

### 2. Visual Hierarchy & Color Coding
Use consistent color coding throughout:
- 🔴 **Red/Critical**: Immediate action required (broken links, major errors)
- 🟡 **Orange/High**: Important issues affecting SEO (missing meta tags, duplicates)
- 🟢 **Yellow/Medium**: Should be fixed but not urgent (alt text, minor issues)
- ⚪ **Gray/Low**: Nice to have improvements

### 3. Actionable Insights
Every issue includes:
- Clear description of what the issue is
- Why it matters (impact on SEO/UX)
- How to fix it (specific recommendations)
- Priority/severity indication
- Effort estimation (Easy/Medium/Hard)

### 4. Context & Evidence
Provide full context for every issue:
- Which pages are affected
- How many instances
- When it was first detected
- Historical trends
- Related issues

### 5. Search & Filter
Essential for large sites:
- Search within each category
- Filter by severity
- Filter by status (new/existing/fixed)
- Filter by URL pattern
- Sort by various criteria

### 6. Export & Share
Enable different workflows:
- **PDF Export**: Formatted client reports with branding, charts, executive summary
- **CSV Export**: Raw data for spreadsheet analysis
- **Share Link**: Shareable report URLs (future feature)
- **Per-Category Export**: Export specific issue types

### 7. Consistency with Existing UI
Maintain BlueSnake's design language:
- Reuse existing design system components
- Match color scheme and typography
- Follow established interaction patterns
- Use familiar icons and layouts

### 8. Comparison Over Time
Track progress across crawls:
- Compare current crawl to previous
- Show new issues vs fixed issues
- Trend analysis (improving/declining)
- Historical charts
- Change detection

### 9. Performance Considerations
Handle large datasets efficiently:
- Pagination for large issue lists
- Lazy loading of details
- Progressive rendering
- Efficient database queries
- Caching of computed scores

### 10. Mobile-Friendly (Future)
While desktop-first, consider responsive design:
- Collapsible cards
- Stacked layouts on small screens
- Touch-friendly interactions

---

## Data Requirements

### Database Schema Extensions

#### 1. New Tables

**`SEOIssue` Table:**
```sql
CREATE TABLE seo_issues (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    crawl_id        INTEGER NOT NULL,
    issue_type      VARCHAR(50) NOT NULL,  -- 'broken_link', 'missing_meta', etc.
    severity        VARCHAR(20) NOT NULL,   -- 'critical', 'high', 'medium', 'low'
    url             TEXT NOT NULL,
    title           TEXT,
    description     TEXT,
    details         TEXT,                   -- JSON for issue-specific data
    status          VARCHAR(20),            -- 'new', 'existing', 'fixed'
    first_seen      INTEGER,                -- Unix timestamp
    last_seen       INTEGER,                -- Unix timestamp
    created_at      INTEGER NOT NULL,

    FOREIGN KEY (crawl_id) REFERENCES crawls(id) ON DELETE CASCADE,
    INDEX idx_crawl_issue (crawl_id, issue_type),
    INDEX idx_severity (severity),
    INDEX idx_status (status)
);
```

**Issue Types (issue_type values):**
- `broken_link`
- `redirect_chain`
- `missing_title`
- `missing_description`
- `duplicate_title`
- `duplicate_description`
- `title_too_short`
- `title_too_long`
- `description_too_short`
- `description_too_long`
- `duplicate_content`
- `missing_h1`
- `multiple_h1`
- `heading_skip`
- `empty_heading`
- `missing_alt_text`
- `empty_alt_text`
- `missing_canonical`
- `canonical_conflict`
- `canonical_chain`
- `robots_conflict`
- `noindex_in_sitemap`

**Details JSON Structure Examples:**

Broken Link:
```json
{
  "error_type": "404",
  "error_message": "Not Found",
  "source_urls": [
    {
      "url": "/blog/article-1",
      "anchor_text": "Read more"
    }
  ],
  "source_count": 5
}
```

Duplicate Content:
```json
{
  "content_hash": "a3f2b1c5d4e6...",
  "duplicate_urls": [
    "/products/widget",
    "/shop/widget",
    "/catalog/widget"
  ],
  "suggested_canonical": "/products/widget",
  "group_size": 3
}
```

Heading Structure:
```json
{
  "issues": ["multiple_h1", "heading_skip"],
  "current_structure": [
    {"level": 1, "text": "Welcome"},
    {"level": 1, "text": "Title"},
    {"level": 2, "text": "Intro"},
    {"level": 4, "text": "Points"}
  ],
  "recommended_structure": [
    {"level": 1, "text": "Title"},
    {"level": 2, "text": "Intro"},
    {"level": 3, "text": "Points"}
  ]
}
```

**`SEOScore` Table:**
```sql
CREATE TABLE seo_scores (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    crawl_id            INTEGER NOT NULL,
    overall_score       INTEGER NOT NULL,      -- 0-100
    technical_score     INTEGER NOT NULL,      -- 0-100
    content_score       INTEGER NOT NULL,      -- 0-100
    link_score          INTEGER NOT NULL,      -- 0-100
    total_issues        INTEGER DEFAULT 0,
    critical_issues     INTEGER DEFAULT 0,
    high_issues         INTEGER DEFAULT 0,
    medium_issues       INTEGER DEFAULT 0,
    low_issues          INTEGER DEFAULT 0,
    created_at          INTEGER NOT NULL,

    FOREIGN KEY (crawl_id) REFERENCES crawls(id) ON DELETE CASCADE,
    UNIQUE(crawl_id)
);
```

**`IssueCategory` Table (optional - for categorization):**
```sql
CREATE TABLE issue_categories (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    category_name   VARCHAR(50) NOT NULL,
    display_name    VARCHAR(100) NOT NULL,
    description     TEXT,
    severity        VARCHAR(20) NOT NULL,
    icon            VARCHAR(50),
    sort_order      INTEGER DEFAULT 0,

    UNIQUE(category_name)
);
```

#### 2. Extended Existing Tables

**Extend `discovered_urls` table:**
```sql
-- Add columns for heading validation
ALTER TABLE discovered_urls ADD COLUMN has_h1 BOOLEAN DEFAULT NULL;
ALTER TABLE discovered_urls ADD COLUMN h1_count INTEGER DEFAULT NULL;
ALTER TABLE discovered_urls ADD COLUMN heading_structure TEXT; -- JSON

-- Add columns for canonical analysis
ALTER TABLE discovered_urls ADD COLUMN canonical_url TEXT;
ALTER TABLE discovered_urls ADD COLUMN has_canonical_conflict BOOLEAN DEFAULT FALSE;

-- Add columns for robots analysis
ALTER TABLE discovered_urls ADD COLUMN robots_meta TEXT; -- JSON: ["noindex", "nofollow"]
ALTER TABLE discovered_urls ADD COLUMN has_robots_conflict BOOLEAN DEFAULT FALSE;
```

**Extend `page_links` table:**
```sql
-- Add column for alt text (for images)
ALTER TABLE page_links ADD COLUMN alt_text TEXT;
ALTER TABLE page_links ADD COLUMN has_alt_text BOOLEAN DEFAULT NULL;
```

---

### Backend Implementation

#### 1. Analysis Engine (`internal/app/seo_analysis.go`)

**Core Functions:**

```go
// AnalyzeCrawlForIssues performs full SEO analysis on a completed crawl
func (a *App) AnalyzeCrawlForIssues(crawlID uint) error

// DetectBrokenLinks finds all 404s and network errors
func (a *App) DetectBrokenLinks(crawlID uint) ([]SEOIssue, error)

// DetectRedirectChains finds redirect chains and loops
func (a *App) DetectRedirectChains(crawlID uint) ([]SEOIssue, error)

// DetectMissingMetaTags finds pages with missing/duplicate meta tags
func (a *App) DetectMissingMetaTags(crawlID uint) ([]SEOIssue, error)

// DetectDuplicateContent groups pages by content hash
func (a *App) DetectDuplicateContent(crawlID uint) ([]SEOIssue, error)

// DetectHeadingIssues validates H1-H6 structure
func (a *App) DetectHeadingIssues(crawlID uint) ([]SEOIssue, error)

// CalculateSEOScore computes overall score based on issues
func (a *App) CalculateSEOScore(crawlID uint) (*SEOScore, error)
```

**Scoring Algorithm:**

```go
func CalculateSEOScore(issues []SEOIssue, crawlStats CrawlStats) SEOScore {
    // Base score: 100
    score := 100.0

    // Deduct points based on severity and proportion
    criticalPenalty := float64(countBySeverity(issues, "critical")) / float64(crawlStats.TotalPages) * 40
    highPenalty := float64(countBySeverity(issues, "high")) / float64(crawlStats.TotalPages) * 30
    mediumPenalty := float64(countBySeverity(issues, "medium")) / float64(crawlStats.TotalPages) * 20
    lowPenalty := float64(countBySeverity(issues, "low")) / float64(crawlStats.TotalPages) * 10

    score -= criticalPenalty + highPenalty + mediumPenalty + lowPenalty

    // Ensure score doesn't go below 0
    if score < 0 {
        score = 0
    }

    return SEOScore{
        OverallScore: int(score),
        // ... calculate subscores similarly
    }
}
```

#### 2. Report API Methods (`internal/app/reports.go`)

```go
// GetReportOverview returns summary data for report dashboard
func (a *App) GetReportOverview(crawlID uint) (*ReportOverview, error)

// GetIssuesByCategory returns all issues for a specific category
func (a *App) GetIssuesByCategory(crawlID uint, category string, filters IssueFilters) ([]SEOIssue, error)

// GetIssueDetail returns full details for a specific issue
func (a *App) GetIssueDetail(issueID uint) (*IssueDetail, error)

// CompareReports compares two crawls and shows differences
func (a *App) CompareReports(crawlID1, crawlID2 uint) (*ReportComparison, error)

// ExportReportPDF generates PDF report
func (a *App) ExportReportPDF(crawlID uint, options ExportOptions) ([]byte, error)

// ExportReportCSV generates CSV export
func (a *App) ExportReportCSV(crawlID uint, options ExportOptions) ([]byte, error)
```

**Type Definitions:**

```go
type ReportOverview struct {
    CrawlID          uint
    Domain           string
    CrawlDate        int64
    SEOScore         SEOScore
    IssueSummary     IssueSummary
    IssueCategories  []IssueCategory
    PreviousCrawl    *CrawlComparison  // optional
}

type IssueSummary struct {
    TotalIssues     int
    CriticalCount   int
    HighCount       int
    MediumCount     int
    LowCount        int
}

type IssueCategory struct {
    CategoryName    string
    DisplayName     string
    Description     string
    Severity        string
    IssueCount      int
    Icon            string
}

type IssueDetail struct {
    Issue           SEOIssue
    SourcePages     []PageLink
    HistoricalData  []IssueHistory
    Recommendations []string
}

type ReportComparison struct {
    NewIssues       int
    FixedIssues     int
    ExistingIssues  int
    ScoreChange     int
    Details         []IssueChange
}
```

#### 3. Crawler Integration

Update crawler callbacks to extract additional data:

**In `internal/app/crawler.go` - `OnPageCrawled` callback:**

```go
// Extract headings
headings := extractHeadings(result)
headingStructure, _ := json.Marshal(headings)
discoveredUrl.HeadingStructure = string(headingStructure)
discoveredUrl.HasH1 = hasH1(headings)
discoveredUrl.H1Count = countH1(headings)

// Extract canonical
canonical := extractCanonical(result)
discoveredUrl.CanonicalURL = canonical

// Extract robots meta
robotsMeta := extractRobotsMeta(result)
robotsJSON, _ := json.Marshal(robotsMeta)
discoveredUrl.RobotsMeta = string(robotsJSON)
```

**Helper functions:**

```go
func extractHeadings(result *PageResult) []Heading {
    // Parse HTML and extract H1-H6 tags
}

func extractCanonical(result *PageResult) string {
    // Parse <link rel="canonical"> tag
}

func extractRobotsMeta(result *PageResult) []string {
    // Parse <meta name="robots" content="...">
}
```

---

### Frontend Implementation

#### 1. New Components

**Report Components:**

```
cmd/desktop/frontend/src/
├── Reports/
│   ├── ReportsOverview.tsx          # Level 1: Main dashboard
│   ├── CategoryDetail.tsx           # Level 2: Category view
│   ├── IssueDetail.tsx              # Level 3: Individual issue
│   ├── components/
│   │   ├── SEOScoreGauge.tsx        # Circular score gauge
│   │   ├── IssueSummaryCard.tsx     # Issues summary widget
│   │   ├── SeverityChart.tsx        # Pie chart for severity
│   │   ├── IssueCategoryCard.tsx    # Clickable category card
│   │   ├── IssueTable.tsx           # Reusable table for issues
│   │   ├── ComparisonWidget.tsx     # Crawl comparison view
│   │   ├── RecommendationCard.tsx   # Recommendation display
│   │   └── ExportButtons.tsx        # PDF/CSV export buttons
│   ├── utils/
│   │   ├── scoreCalculations.ts     # Score calculation helpers
│   │   ├── issueFilters.ts          # Filter/search logic
│   │   └── formatters.ts            # Data formatting
│   └── Reports.css                  # Styling
```

#### 2. API Integration

**TypeScript Interfaces:**

```typescript
interface ReportOverview {
  crawlId: number;
  domain: string;
  crawlDate: number;
  seoScore: SEOScore;
  issueSummary: IssueSummary;
  issueCategories: IssueCategory[];
  previousCrawl?: CrawlComparison;
}

interface SEOScore {
  overallScore: number;
  technicalScore: number;
  contentScore: number;
  linkScore: number;
}

interface IssueSummary {
  totalIssues: number;
  criticalCount: number;
  highCount: number;
  mediumCount: number;
  lowCount: number;
}

interface IssueCategory {
  categoryName: string;
  displayName: string;
  description: string;
  severity: string;
  issueCount: number;
  icon: string;
}

interface SEOIssue {
  id: number;
  crawlId: number;
  issueType: string;
  severity: string;
  url: string;
  title: string;
  description: string;
  details: any; // JSON object
  status: string;
  firstSeen: number;
  lastSeen: number;
}
```

**API Calls (via Wails):**

```typescript
import {
  GetReportOverview,
  GetIssuesByCategory,
  GetIssueDetail,
  CompareReports,
  ExportReportPDF,
  ExportReportCSV
} from "../../wailsjs/go/main/DesktopApp";

// Get report overview
const overview = await GetReportOverview(crawlId);

// Get issues for a category
const issues = await GetIssuesByCategory(crawlId, "broken_link", filters);

// Get issue detail
const detail = await GetIssueDetail(issueId);

// Compare reports
const comparison = await CompareReports(crawlId1, crawlId2);

// Export as PDF
const pdfData = await ExportReportPDF(crawlId, options);

// Export as CSV
const csvData = await ExportReportCSV(crawlId, options);
```

#### 3. Chart Libraries

Use **Recharts** for React visualizations:

```bash
npm install recharts
```

**Example: SEO Score Gauge Component**

```typescript
import { CircularProgressbar, buildStyles } from 'react-circular-progressbar';
import 'react-circular-progressbar/dist/styles.css';

function SEOScoreGauge({ score }: { score: number }) {
  const getColor = (score: number) => {
    if (score >= 81) return '#10b981'; // Green
    if (score >= 61) return '#f59e0b'; // Yellow
    if (score >= 41) return '#f97316'; // Orange
    return '#ef4444'; // Red
  };

  return (
    <CircularProgressbar
      value={score}
      text={`${score}`}
      styles={buildStyles({
        pathColor: getColor(score),
        textColor: getColor(score),
        textSize: '24px',
      })}
    />
  );
}
```

**Example: Severity Pie Chart**

```typescript
import { PieChart, Pie, Cell, ResponsiveContainer, Legend } from 'recharts';

function SeverityChart({ summary }: { summary: IssueSummary }) {
  const data = [
    { name: 'Critical', value: summary.criticalCount, color: '#ef4444' },
    { name: 'High', value: summary.highCount, color: '#f97316' },
    { name: 'Medium', value: summary.mediumCount, color: '#f59e0b' },
    { name: 'Low', value: summary.lowCount, color: '#94a3b8' },
  ];

  return (
    <ResponsiveContainer width="100%" height={200}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          nameKey="name"
          cx="50%"
          cy="50%"
          outerRadius={60}
          label
        >
          {data.map((entry, index) => (
            <Cell key={`cell-${index}`} fill={entry.color} />
          ))}
        </Pie>
        <Legend />
      </PieChart>
    </ResponsiveContainer>
  );
}
```

---

## Implementation Phases

### Phase 1: Foundation (Week 1-2)
- Database schema creation
- Basic SEO analysis engine
- Broken links detection
- Missing meta tags detection
- Report overview UI (without charts)

**Deliverable:** Basic functional report showing broken links and meta issues

### Phase 2: Core Features (Week 3-4)
- SEO scoring algorithm
- Duplicate content detection
- Redirect chain detection
- Category detail views
- Issue detail views
- Charts integration (score gauge, pie chart)

**Deliverable:** Full report UI with major issue categories

### Phase 3: Advanced Analysis (Week 5-6)
- Heading structure validation
- Image alt text validation
- Canonical tag analysis
- Robots meta tag analysis
- Search and filter functionality
- Export to CSV

**Deliverable:** Complete SEO analysis with all categories

### Phase 4: Polish & Extras (Week 7-8)
- PDF export with branding
- Historical comparison (crawl vs crawl)
- Recommendations engine (AI-powered if MCP available)
- Performance optimizations
- UI polish and animations

**Deliverable:** Production-ready reports feature

---

## Success Metrics

### User Engagement
- Percentage of users who view reports after crawl
- Time spent in reports section
- Most viewed issue categories
- Export usage (PDF vs CSV)

### Value Delivered
- Number of issues detected per crawl
- Issue severity distribution
- Score improvements over time
- Issues fixed (tracked via status field)

### Performance
- Report generation time (target: <2 seconds)
- Page load time for overview
- Database query performance
- Export generation time

---

## Future Enhancements

### Phase 5+: Advanced Features

1. **AI-Powered Recommendations** (Enhancement #24)
   - Use MCP integration to generate context-aware fix suggestions
   - Natural language explanations of issues
   - Automated fix proposals

2. **Page Speed Integration** (Enhancement #15)
   - Core Web Vitals scoring
   - Performance recommendations
   - Speed trend tracking

3. **Internal Link Graph Visualization** (Enhancement #18)
   - Visual link graph
   - Orphan page detection
   - PageRank calculation

4. **Scheduled Reports** (Enhancement #13 + Reports)
   - Automated weekly/monthly reports
   - Email delivery
   - Automatic comparison with previous period

5. **Custom Rules Engine** (Enhancement #20)
   - User-defined validation rules
   - Custom severity levels
   - Domain-specific checks

6. **Google Search Console Integration** (Enhancement #23)
   - Import GSC data
   - Correlate crawl issues with search performance
   - Combined reporting

7. **Multi-Language Support** (Enhancement #14)
   - Hreflang validation
   - Language-specific issues
   - Translation coverage reports

---

## Conclusion

The Reports section will transform BlueSnake from a crawler into a comprehensive SEO audit tool. By providing actionable insights, clear visualizations, and detailed issue tracking, it will enable users to:

- **Identify SEO issues** quickly and comprehensively
- **Prioritize fixes** based on severity and impact
- **Track progress** over time with historical comparison
- **Export insights** for clients and stakeholders
- **Take action** with specific, actionable recommendations

The phased implementation approach ensures that core value is delivered early while allowing for iterative improvements and advanced features in later phases.
