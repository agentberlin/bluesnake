# BlueSnake API Documentation

BlueSnake provides a REST API for programmatic access to web crawling and SEO analysis capabilities.

**Base URL:** `http://localhost:8080/api/v1`

## Authentication

Currently, the API does not require authentication. CORS is enabled for all origins.

---

## Endpoints

### Health & Version

#### GET /health
Returns server health status.

**Response:**
```json
{
  "status": "ok"
}
```

#### GET /version
Returns the application version.

**Response:**
```json
{
  "version": "1.0.0"
}
```

---

### Projects

Projects represent domains/websites that have been crawled.

#### GET /projects
List all projects.

**Response:**
```json
[
  {
    "id": 1,
    "url": "https://example.com",
    "domain": "example.com",
    "faviconPath": "/path/to/favicon.ico",
    "crawlDateTime": 1704067200,
    "crawlDuration": 120,
    "pagesCrawled": 150,
    "totalUrls": 500,
    "latestCrawlId": 3
  }
]
```

#### DELETE /projects/{id}
Delete a project and all its crawl data.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Project ID |

**Response:** `204 No Content`

#### GET /projects/{id}/crawls
Get all crawls for a project.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Project ID |

**Response:**
```json
[
  {
    "id": 1,
    "projectId": 1,
    "crawlDateTime": 1704067200,
    "crawlDuration": 120,
    "pagesCrawled": 150,
    "state": "completed"
  }
]
```

**Crawl States:** `in_progress`, `paused`, `completed`, `failed`

#### GET /projects/{id}/active-stats
Get real-time statistics for an active crawl.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Project ID |

**Response:**
```json
{
  "crawlId": 5,
  "total": 500,
  "crawled": 150,
  "queued": 350,
  "html": 100,
  "javascript": 20,
  "css": 15,
  "images": 50,
  "fonts": 5,
  "unvisited": 350,
  "others": 10
}
```

---

### Crawls

#### GET /crawls/{id}
Get crawl details with paginated results.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Crawl ID |
| limit | integer | query | Results per page (default: 100) |
| cursor | integer | query | Pagination cursor (default: 0) |
| type | string | query | Content type filter: `all`, `html`, `css`, `javascript`, `images`, `fonts`, `others` (default: `all`) |

**Response:**
```json
{
  "results": [
    {
      "url": "https://example.com/page",
      "status": 200,
      "title": "Page Title",
      "metaDescription": "Page description",
      "h1": "Main Heading",
      "h2": "Subheading",
      "canonicalUrl": "https://example.com/page",
      "wordCount": 1500,
      "contentHash": "abc123...",
      "indexable": "Yes",
      "contentType": "text/html",
      "error": "",
      "depth": 1
    }
  ],
  "nextCursor": 100,
  "hasMore": true
}
```

**Indexable Values:** `Yes`, `No, Noindex`, `No, Canonical`, `No, Redirect`, etc.

**Depth:** Crawl depth indicates how many links away the URL is from the start URL (0 = start URL, 1 = discovered from start, etc.)

#### DELETE /crawls/{id}
Delete a crawl and its results.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Crawl ID |

**Response:** `204 No Content`

#### GET /crawls/{id}/pages/{url}/links
Get inlinks and outlinks for a specific page.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Crawl ID |
| url | string | path | URL-encoded page URL |

**Response:**
```json
{
  "pageUrl": "https://example.com/page",
  "inlinks": [
    {
      "url": "https://example.com/",
      "linkType": "anchor",
      "anchorText": "Click here",
      "context": "surrounding text...",
      "isInternal": true,
      "status": 200,
      "position": "navigation",
      "domPath": "html > body > nav > a",
      "urlAction": "crawl",
      "follow": true,
      "rel": "",
      "target": "_self",
      "pathType": "Root-Relative"
    }
  ],
  "outlinks": [
    {
      "url": "https://example.com/other",
      "linkType": "anchor",
      "anchorText": "Other page",
      "isInternal": true,
      "status": 200,
      "position": "content",
      "urlAction": "crawl",
      "follow": false,
      "rel": "nofollow",
      "target": "_blank",
      "pathType": "Absolute"
    }
  ]
}
```

**Link Types:** `anchor`, `image`, `script`, `stylesheet`, etc.

**Positions:** `content`, `navigation`, `header`, `footer`, `sidebar`, `breadcrumbs`, `pagination`, `unknown`

**URL Actions:** `crawl` (normal), `record` (framework-specific URL), `skip` (ignored)

**Link Attributes:**
| Field | Type | Description |
|-------|------|-------------|
| follow | boolean | `true` if the link should be followed (no `nofollow`, `sponsored`, or `ugc` in rel attribute) |
| rel | string | Full `rel` attribute value (e.g., `"nofollow"`, `"noopener noreferrer"`) |
| target | string | Target attribute value (`_blank`, `_self`, etc.) |
| pathType | string | How the href is specified: `Absolute`, `Root-Relative`, or `Relative` |

#### GET /crawls/{id}/pages/{url}/content
Get the HTML content of a crawled page.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| id | integer | path | Crawl ID |
| url | string | path | URL-encoded page URL |

**Response:**
```json
{
  "content": "<!DOCTYPE html><html>...</html>"
}
```

---

### Crawl Operations

#### POST /crawl
Start a new crawl.

**Request Body:**
```json
{
  "url": "https://example.com"
}
```

**Response:** `202 Accepted`
```json
{
  "message": "Crawl started",
  "project": {
    "id": 1,
    "url": "https://example.com",
    "domain": "example.com",
    "faviconPath": "",
    "crawlDateTime": 1704067200,
    "crawlDuration": 0,
    "pagesCrawled": 0,
    "totalUrls": 0,
    "latestCrawlId": 5
  }
}
```

#### POST /stop-crawl/{projectId}
Stop an active crawl.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| projectId | integer | path | Project ID |

**Response:**
```json
{
  "message": "Crawl stopped"
}
```

#### POST /resume-crawl/{projectId}
Resume a stopped/paused crawl.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| projectId | integer | path | Project ID |

**Response:** `202 Accepted`
```json
{
  "message": "Crawl resumed",
  "project": {
    "id": 1,
    "url": "https://example.com",
    "domain": "example.com",
    "latestCrawlId": 6
  }
}
```

#### GET /active-crawls
Get list of all currently active crawls.

**Response:**
```json
[
  {
    "projectId": 1,
    "crawlId": 5,
    "domain": "example.com",
    "url": "https://example.com",
    "pagesCrawled": 50,
    "totalUrlsCrawled": 150,
    "totalDiscovered": 500,
    "discoveredUrls": ["https://example.com/pending1", "..."],
    "isCrawling": true
  }
]
```

---

### Search

#### GET /search/{crawlId}
Search crawl results by URL or content.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| crawlId | integer | path | Crawl ID |
| q | string | query | Search query (searches URL, title, meta description) |
| type | string | query | Content type filter (default: `all`) |
| limit | integer | query | Results per page (default: 100) |
| cursor | integer | query | Pagination cursor (default: 0) |

**Response:**
```json
{
  "results": [
    {
      "url": "https://example.com/search-result",
      "status": 200,
      "title": "Matching Page",
      "metaDescription": "Contains search term",
      "indexable": "Yes",
      "contentType": "text/html"
    }
  ],
  "nextCursor": 100,
  "hasMore": false
}
```

---

### Configuration

#### GET /config
Get crawl configuration for a domain.

**Parameters:**
| Name | Type | Location | Description |
|------|------|----------|-------------|
| url | string | query | Domain URL (required) |

**Response:**
```json
{
  "domain": "example.com",
  "jsRenderingEnabled": false,
  "initialWaitMs": 1500,
  "scrollWaitMs": 2000,
  "finalWaitMs": 1000,
  "parallelism": 5,
  "userAgent": "bluesnake/1.0 (+https://snake.blue)",
  "includeSubdomains": false,
  "discoveryMechanisms": ["spider", "sitemap"],
  "sitemapURLs": [],
  "checkExternalResources": true,
  "robotsTxtMode": "respect",
  "followInternalNofollow": false,
  "followExternalNofollow": false,
  "respectMetaRobotsNoindex": true,
  "respectNoindex": true,
  "incrementalCrawlingEnabled": false,
  "crawlBudget": 0
}
```

#### PUT /config
Update crawl configuration for a domain.

**Request Body:**
```json
{
  "url": "https://example.com",
  "jsRendering": true,
  "initialWaitMs": 2000,
  "scrollWaitMs": 3000,
  "finalWaitMs": 1500,
  "parallelism": 10,
  "userAgent": "CustomBot/1.0",
  "includeSubdomains": true,
  "spiderEnabled": true,
  "sitemapEnabled": true,
  "sitemapURLs": ["https://example.com/sitemap.xml"],
  "checkExternalResources": true,
  "robotsTxtMode": "respect",
  "followInternalNofollow": false,
  "followExternalNofollow": false,
  "respectMetaRobotsNoindex": true,
  "respectNoindex": true,
  "incrementalCrawlingEnabled": false,
  "crawlBudget": 1000
}
```

**Configuration Options:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| url | string | required | Domain URL to configure |
| jsRendering | boolean | false | Enable JavaScript rendering (headless browser) |
| initialWaitMs | integer | 1500 | Wait time after page load for JS hydration |
| scrollWaitMs | integer | 2000 | Wait time after scrolling for lazy content |
| finalWaitMs | integer | 1000 | Final wait before capturing HTML |
| parallelism | integer | 5 | Number of concurrent requests |
| userAgent | string | bluesnake/1.0 | User-Agent header |
| includeSubdomains | boolean | false | Crawl all subdomains |
| spiderEnabled | boolean | true | Discover URLs by following links |
| sitemapEnabled | boolean | true | Discover URLs from sitemaps |
| sitemapURLs | array | [] | Custom sitemap URLs (empty = auto-detect) |
| checkExternalResources | boolean | true | Validate external resources for broken links |
| robotsTxtMode | string | "respect" | How to handle robots.txt: `respect`, `ignore`, `ignore-report` |
| followInternalNofollow | boolean | false | Follow internal nofollow links |
| followExternalNofollow | boolean | false | Follow external nofollow links |
| respectMetaRobotsNoindex | boolean | true | Respect meta robots noindex |
| respectNoindex | boolean | true | Respect X-Robots-Tag noindex |
| incrementalCrawlingEnabled | boolean | false | Enable chunked/resumable crawling |
| crawlBudget | integer | 0 | Max URLs per session (0 = unlimited) |

**Response:**
```json
{
  "message": "Config updated"
}
```

---

## Error Responses

All endpoints return standard HTTP error codes:

| Code | Description |
|------|-------------|
| 400 | Bad Request - Invalid parameters |
| 404 | Not Found - Resource doesn't exist |
| 405 | Method Not Allowed - Wrong HTTP method |
| 500 | Internal Server Error - Server error |

Error response format:
```
Error message text
```

---

## Pagination

Endpoints that return lists support cursor-based pagination:

- `limit`: Number of results per page (default: 100)
- `cursor`: Starting position (default: 0)

The response includes:
- `nextCursor`: Use this value as `cursor` for the next page
- `hasMore`: Boolean indicating if more results exist

---

## Content Type Filters

When filtering by content type, use these values:

| Value | Description |
|-------|-------------|
| all | All content types |
| html | HTML pages (text/html) |
| css | CSS stylesheets |
| javascript | JavaScript files |
| images | Image files (jpeg, png, gif, svg, webp, etc.) |
| fonts | Font files (woff, woff2, ttf, otf, eot) |
| others | Other content types |
