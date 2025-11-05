# BlueSnake Enhancement Roadmap

This document outlines potential enhancements for BlueSnake, inspired by features from SEOnaut and other SEO crawling tools. Features are organized by implementation difficulty to help prioritize development efforts.

---

## 🟢 EASY - Quick Wins (1-3 days each)

### 1. Broken Link Detection & Reporting
**Status:** Not Implemented
**Priority:** High

**Current State:**
- BlueSnake already tracks status codes and errors
- Data is available in the database

**Enhancement:**
- Add dedicated "Broken Links" view filtering 404s and network errors
- Implement frontend filter + new tab in results view
- Show source pages linking to broken URLs

**Implementation:**
- Frontend: Add filter tab for broken links (status >= 400)
- Backend: Add query method in `internal/store/links.go`

**Value:** High - critical SEO issue identification

---

### 2. Redirect Chain Detection
**Status:** Not Implemented
**Priority:** High

**Current State:**
- Crawler tracks redirects via `OnRedirect` callback
- Redirect destinations are validated

**Enhancement:**
- Track full redirect chains (A → B → C)
- Identify redirect loops
- Flag permanent (301) vs temporary (302) redirects
- Report redirect chain length

**Implementation:**
- Enhance crawler callbacks to track redirect paths
- Add `redirect_chain` field to database
- Create redirect analysis view in UI

**Value:** High - identifies wasted crawl budget and potential issues

---

### 3. Missing Meta Tags Detection
**Status:** Partially Implemented
**Priority:** High

**Current State:**
- Title and meta description are extracted
- Stored in database

**Enhancement:**
- Flag pages missing title tags
- Flag pages missing meta descriptions
- Identify duplicate titles across pages
- Identify duplicate meta descriptions
- Report title/description length issues (too short/long)

**Implementation:**
- Add validation logic in `OnPageCrawled` callback
- Add `meta_issues` field to database
- Create meta tags report view

**Value:** High - basic SEO hygiene check

---

### 4. Issue Severity Categorization
**Status:** Not Implemented
**Priority:** Medium

**Current State:**
- Issues are tracked but not categorized

**Enhancement:**
- Categorize all issues as Critical/High/Low severity
- Visual severity badges in UI
- Filter results by severity
- SEO health score based on severity distribution

**Implementation:**
- Add `severity` field to database models
- Create severity calculation logic
- Add badge UI component (reuse design system)
- Update results view to show severity

**Value:** Medium - helps prioritize fixes

---

### 5. Duplicate Content Detection Enhancement
**Status:** Partially Implemented
**Priority:** High

**Current State:**
- Content hashing is implemented
- Duplicate detection works

**Enhancement:**
- Dedicated "Duplicate Content" report
- Show all pages sharing the same content hash
- Group duplicate pages together
- Suggest canonical URL for duplicate groups

**Implementation:**
- Query database for duplicate content hashes
- Create new duplicate content view
- Add grouping logic in backend

**Value:** High - identifies duplicate content issues

---

## 🟡 MEDIUM - Moderate Effort (3-7 days each)

### 6. Heading Structure Validation
**Status:** Not Implemented
**Priority:** High

**Current State:**
- HTML is parsed with GoQuery
- Heading extraction is possible

**Enhancement:**
- Validate H1-H6 structure
- Flag missing H1 tags
- Flag multiple H1 tags on same page
- Detect improper nesting (e.g., H1 → H3 skip)
- Report heading hierarchy issues

**Implementation:**
- Extract heading hierarchy in HTML parsing (`htmlelement.go`)
- Add heading validation logic
- Store heading structure in database
- Create heading structure report

**Value:** High - important for content hierarchy and SEO

---

### 7. Interactive SEO Dashboard with Charts
**Status:** Not Implemented
**Priority:** Medium

**Current State:**
- Basic stats display in footer
- Content type counts available

**Enhancement:**
- Visual charts showing:
  - Issue distribution (pie chart)
  - Status code breakdown (bar chart)
  - Content type distribution
  - Crawl progress over time
  - Historical trends
- Interactive data visualization

**Implementation:**
- Integrate chart library (Recharts for React)
- Create new dashboard view/section
- Add chart components to design system
- Create API endpoints for chart data

**Value:** Medium - better data visualization and insights

---

### 8. SEO Score/Health Rating
**Status:** Not Implemented
**Priority:** High

**Current State:**
- Various metrics are tracked
- No overall score

**Enhancement:**
- Calculate overall SEO health score (0-100)
- Score based on:
  - Broken links (critical)
  - Missing meta tags (high)
  - Redirect issues (medium)
  - Content duplicates (high)
  - Page speed (high)
- Display score on project cards
- Show score trend over time

**Implementation:**
- Create scoring algorithm in `internal/app/`
- Add score calculation to crawl completion
- Store score in database
- Display score prominently in UI

**Value:** High - quick project health overview

---

### 9. Image Alt Text Validation
**Status:** Not Implemented
**Priority:** Medium

**Current State:**
- Images are tracked as resources
- Image URLs are stored

**Enhancement:**
- Check for missing alt attributes on images
- Report empty alt attributes
- Identify decorative images (alt="")
- Calculate alt text quality score

**Implementation:**
- Parse `<img>` tags in HTML
- Extract and validate alt attributes
- Store image metadata in database
- Create image accessibility report

**Value:** Medium - accessibility and SEO improvement

---

### 10. Canonical Tag Analysis
**Status:** Not Implemented
**Priority:** High

**Current State:**
- HTML is parsed
- Meta tags are extracted

**Enhancement:**
- Extract canonical tags from pages
- Validate canonical URLs
- Identify canonical conflicts (multiple canonicals)
- Detect self-referencing canonicals
- Find canonical chains

**Implementation:**
- Add canonical extraction to HTML parsing
- Add canonical validation logic
- Store canonical data in database
- Create canonical analysis report

**Value:** High - prevents duplicate content issues

---

### 11. Robots Meta Tag Analysis
**Status:** Partially Implemented
**Priority:** Medium

**Current State:**
- Noindex directives are respected
- Basic robots.txt parsing

**Enhancement:**
- Extract all robots meta tags
- Report noindex, nofollow, noarchive directives
- Flag conflicting directives
- Compare robots.txt vs meta robots
- Identify indexation blockers

**Implementation:**
- Extract robots meta tags in HTML parsing
- Add robots analysis logic
- Store robots directives in database
- Create robots analysis report

**Value:** Medium - identifies indexation issues

---

## 🔴 HARD - Significant Effort (1-2 weeks each)

### 12. Historical Comparison & Change Tracking
**Status:** Not Implemented
**Priority:** Very High

**Current State:**
- Multiple crawls stored per project
- No comparison functionality

**Enhancement:**
- Compare two crawls side-by-side
- Highlight changes:
  - New pages added
  - Pages removed
  - Status code changes
  - Meta tag changes
  - Content changes
- Visual diff view
- Change alerts/notifications

**Implementation:**
- Create diff engine comparing two crawl results
- Build comparison UI component
- Add change detection algorithms
- Store change history

**Value:** Very High - track SEO changes over time

---

### 13. Scheduled Crawls
**Status:** Not Implemented
**Priority:** High

**Current State:**
- Manual crawl initiation only
- Desktop app architecture

**Enhancement:**
- Schedule recurring crawls (daily/weekly/monthly)
- Cron-like scheduling interface
- Background crawl execution
- Email notifications on completion
- Auto-compare with previous crawl

**Implementation:**
- Add job scheduler (cron-like)
- Persistent schedule storage in database
- Background task runner
- Notification system

**Value:** High - automate monitoring

**Note:** May require architectural changes for desktop app

---

### 14. Multi-Language SEO Analysis
**Status:** Not Implemented
**Priority:** Medium

**Current State:**
- Single-language crawling
- No language detection

**Enhancement:**
- Detect page language
- Extract hreflang tags
- Validate language alternates
- Identify missing translations
- Check hreflang reciprocity
- Report language coverage

**Implementation:**
- Parse hreflang attributes
- Add language detection logic
- Validate cross-referencing
- Create multi-language report

**Value:** Medium - critical for international sites

---

### 15. Page Speed & Performance Metrics
**Status:** Not Implemented
**Priority:** Very High

**Current State:**
- Basic crawl duration tracking
- Chromedp integration exists

**Enhancement:**
- Measure per-page performance:
  - Time to First Byte (TTFB)
  - First Contentful Paint (FCP)
  - Largest Contentful Paint (LCP)
  - Cumulative Layout Shift (CLS)
  - Time to Interactive (TTI)
- Core Web Vitals scoring
- Performance comparison over time

**Implementation:**
- Instrument chromedp to capture performance metrics
- Add performance data to database
- Create performance dashboard
- Performance scoring algorithm

**Value:** Very High - Core Web Vitals are ranking factors

---

### 16. Structured Data (Schema.org) Validation
**Status:** Not Implemented
**Priority:** High

**Current State:**
- HTML parsing only
- No structured data extraction

**Enhancement:**
- Extract JSON-LD structured data
- Parse Microdata format
- Parse RDFa format
- Validate against schema.org schemas
- Identify missing structured data
- Report structured data errors

**Implementation:**
- Parse structured data formats
- Integrate schema.org validation
- Store structured data in database
- Create structured data report

**Value:** High - enables rich snippets in search results

---

### 17. Sitemap Generation & Validation
**Status:** Partially Implemented
**Priority:** High

**Current State:**
- Sitemap parsing is implemented
- Discovery from sitemaps works

**Enhancement:**
- Generate optimized XML sitemaps from crawl results
- Validate existing sitemaps
- Identify sitemap errors:
  - URLs not in sitemap
  - Invalid URLs in sitemap
  - Blocked URLs in sitemap
- Image sitemaps
- Video sitemaps

**Implementation:**
- Build sitemap generator
- Add sitemap validation logic
- Create sitemap export functionality
- Sitemap comparison tool

**Value:** High - ensures search engines find all pages

---

### 18. Internal Link Analysis Dashboard
**Status:** Partially Implemented
**Priority:** Very High

**Current State:**
- Link graph data is collected
- Inlinks/outlinks are tracked
- LinksPanel component exists

**Enhancement:**
- Visualize internal link structure (graph view)
- Identify orphan pages (no inlinks)
- Calculate PageRank/authority scores
- Find broken link chains
- Suggest internal linking opportunities
- Link depth analysis

**Implementation:**
- Build graph visualization (D3.js or similar)
- Implement PageRank algorithm
- Create link analysis dashboard
- Add orphan page detection

**Value:** Very High - understand and optimize site architecture

---

## 🟣 ADVANCED - Major Features (2-4 weeks each)

### 19. Multi-User Support & Team Collaboration
**Status:** Not Implemented
**Priority:** Very High

**Current State:**
- Single-user desktop app
- SQLite local database

**Enhancement:**
- User accounts and authentication
- Project sharing between users
- Role-based access control (Admin, Editor, Viewer)
- Activity logs
- Commenting on issues
- Team notifications

**Implementation:**
- Add authentication system
- Multi-tenancy in database
- User management UI
- Project sharing logic
- Permission system

**Value:** Very High - enterprise feature

**Note:** Requires architectural shift from desktop to server-based architecture

---

### 20. Custom SEO Rules Engine
**Status:** Not Implemented
**Priority:** High

**Current State:**
- Fixed validation rules
- No customization

**Enhancement:**
- User-defined validation rules
- Rule builder UI
- Rule templates:
  - "Title length must be 30-60 chars"
  - "Meta description must be present"
  - "H1 count must equal 1"
- Custom severity levels
- Rule sharing/export

**Implementation:**
- Rule definition DSL
- Rule engine/interpreter
- Rule builder UI
- Rule storage in database

**Value:** High - customizable audits for different needs

---

### 21. Competitive Analysis
**Status:** Not Implemented
**Priority:** High

**Current State:**
- Single-site crawling
- No comparison features

**Enhancement:**
- Crawl multiple competitor sites
- Side-by-side metric comparison
- Competitive gap analysis
- Share of voice analysis
- Benchmark against competitors

**Implementation:**
- Multi-project comparison engine
- Anonymized crawling capabilities
- Comparison dashboard
- Benchmark calculations

**Value:** High - competitive intelligence

---

### 22. Export & Reporting System
**Status:** Not Implemented
**Priority:** Very High

**Current State:**
- Database-only storage
- No export functionality

**Enhancement:**
- Export formats:
  - PDF reports
  - CSV exports
  - Excel spreadsheets
  - JSON API responses
- Branded reports with logos
- Custom report templates
- Scheduled email reports
- Report sharing links

**Implementation:**
- Report templating engine
- Export libraries (pdf, csv, xlsx)
- Email integration
- Report customization UI

**Value:** Very High - client reporting and data portability

---

### 23. Integration with Google Search Console
**Status:** Not Implemented
**Priority:** Very High

**Current State:**
- Standalone crawler
- No external integrations

**Enhancement:**
- OAuth integration with Google Search Console
- Import GSC data:
  - Search queries
  - Impressions
  - Clicks
  - Average position
  - Coverage issues
- Correlate crawl data with GSC data
- Show real search performance alongside technical data

**Implementation:**
- OAuth flow for GSC authentication
- GSC API integration
- Data correlation logic
- Combined reporting dashboard

**Value:** Very High - combines crawl data with real search performance

---

### 24. AI-Powered SEO Recommendations
**Status:** Partially Implemented (MCP integration exists)
**Priority:** Very High

**Current State:**
- MCP server for AI integration
- No AI-powered recommendations

**Enhancement:**
- AI-generated SEO recommendations
- Context-aware suggestions using Claude/GPT
- Natural language explanations of issues
- Automated fix suggestions
- Content optimization recommendations
- Keyword opportunities

**Implementation:**
- Integrate LLM API (leverage existing MCP)
- Prompt engineering for SEO advice
- Recommendation UI components
- Context gathering from crawl data

**Value:** Very High - actionable insights with explanations

---

## 💡 Implementation Priority

### Phase 1: Foundation (Quick Wins)
**Timeline:** 1-2 weeks
1. Broken Link Detection
2. Redirect Chain Detection
3. Missing Meta Tags Detection
4. Issue Severity Categorization
5. Duplicate Content Report

**Rationale:** High value, low effort, builds on existing infrastructure

---

### Phase 2: Core SEO Features
**Timeline:** 3-4 weeks
6. Heading Structure Validation
7. SEO Score/Health Rating
8. Canonical Tag Analysis
9. Robots Meta Tag Analysis
10. Image Alt Text Validation

**Rationale:** Essential SEO audit features

---

### Phase 3: Advanced Analysis
**Timeline:** 6-8 weeks
11. Interactive SEO Dashboard with Charts
12. Historical Comparison & Change Tracking
13. Page Speed & Performance Metrics
14. Internal Link Analysis Dashboard

**Rationale:** High-value features that differentiate from competitors

---

### Phase 4: Professional Features
**Timeline:** 8-12 weeks
15. Structured Data Validation
16. Sitemap Generation & Validation
17. Export & Reporting System
18. Google Search Console Integration

**Rationale:** Professional/enterprise features

---

### Phase 5: Enterprise & AI
**Timeline:** 12+ weeks
19. Multi-User Support & Team Collaboration
20. Custom SEO Rules Engine
21. Scheduled Crawls
22. AI-Powered SEO Recommendations
23. Competitive Analysis
24. Multi-Language SEO Analysis

**Rationale:** Advanced features for enterprise use cases

---

## Architecture Considerations

### ✅ BlueSnake Strengths to Leverage

1. **Existing Crawler Callbacks**
   - `OnPageCrawled`, `OnURLDiscovered`, `OnResourceVisit`
   - Perfect extension points for new validations

2. **HTML Parsing Infrastructure**
   - GoQuery integration already in place
   - Easy to add new extraction logic

3. **Extensible Database Schema**
   - GORM models are easy to extend
   - Add new fields and tables as needed

4. **MCP Server Integration**
   - Natural fit for AI-powered features
   - Already accessible to Claude and other AI tools

5. **Multi-Transport Architecture**
   - Desktop app for individual users
   - MCP for AI integration
   - Easy to add HTTP API for team features

6. **Design System**
   - Reusable UI components in place
   - Consistent styling across features

### 🔧 Implementation Guidelines

1. **Add validation logic in crawler callbacks**
   - Keep crawler package pure
   - Add business logic in `internal/app/`

2. **Extend database models**
   - Add new tables in `internal/store/models.go`
   - Add CRUD operations in appropriate files

3. **Create new UI views**
   - Reuse design system components
   - Follow existing patterns (Config, LinksPanel, etc.)

4. **Maintain transport-agnostic business logic**
   - Keep `internal/app/` independent of UI
   - Support both desktop and MCP access

### 📊 Suggested Database Extensions

```go
// Example: New SEO Issues table
type SEOIssue struct {
    ID          uint
    CrawlID     uint
    URL         string
    IssueType   string  // "broken_link", "missing_meta", "duplicate_content"
    Severity    string  // "critical", "high", "low"
    Description string
    Details     string  // JSON for additional context
    CreatedAt   int64
}

// Example: Heading Structure
type HeadingStructure struct {
    ID             uint
    DiscoveredUrlID uint
    Level          int     // 1-6 for H1-H6
    Text           string
    Position       int     // Order on page
}

// Example: Redirect Chain
type RedirectChain struct {
    ID         uint
    CrawlID    uint
    SourceURL  string
    TargetURL  string
    StatusCode int
    ChainOrder int     // Position in chain
}
```

---

## Feature Dependencies

### Prerequisites for Advanced Features

**For Scheduled Crawls:**
- Background task runner
- Possibly shift from desktop-only to server mode

**For Multi-User Support:**
- Authentication system
- Server-based architecture (not just desktop)
- Database migration to PostgreSQL/MySQL

**For Team Collaboration:**
- Multi-user support must be implemented first
- Notification system

**For Competitive Analysis:**
- Multi-project comparison engine
- Enhanced crawl orchestration

---

## Metrics for Success

### Phase 1 Success Criteria
- [ ] Broken links are clearly identified and reportable
- [ ] Redirect chains are tracked and visualized
- [ ] Missing meta tags are flagged
- [ ] Issues have severity levels
- [ ] Duplicate content groups are shown

### Phase 2 Success Criteria
- [ ] Heading structure validation is accurate
- [ ] SEO score is calculated and displayed
- [ ] Canonical tags are analyzed
- [ ] Robots directives are reported

### Phase 3 Success Criteria
- [ ] Dashboard shows interactive charts
- [ ] Historical comparison works between crawls
- [ ] Performance metrics are collected
- [ ] Internal link graph is visualized

---

## Notes

- This roadmap is inspired by SEOnaut (https://github.com/StJudeWasHere/seonaut)
- Features are selected based on BlueSnake's existing architecture and capabilities
- Implementation estimates assume familiarity with the existing codebase
- Some enterprise features may require architectural changes
- All features maintain the "transport-agnostic" design principle

---

**Last Updated:** 2025-11-05
**Document Version:** 1.0
