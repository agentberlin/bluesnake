# BlueSnake CLI

Command-line interface for BlueSnake web crawler. Provides programmatic access to crawling, exporting, and managing crawl data.

## Quick Start

```bash
# Build the CLI
go build -o bluesnake ./cmd/cli

# Crawl a website
./bluesnake crawl https://example.com

# Export results
./bluesnake export --crawl-id 1 --format csv -o ./export
```

## Commands

### crawl

Start a new crawl for a URL, or resume a paused crawl.

```bash
bluesnake crawl <url> [flags]
bluesnake crawl --resume --project-id <id> [flags]
```

#### Resume Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--resume` | `-r` | false | Resume a paused crawl instead of starting new |
| `--project-id` | | 0 | Project ID to resume (required with --resume) |

#### Core Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--parallelism` | `-p` | 5 | Number of concurrent requests |
| `--response-timeout` | `-T` | 20 | Timeout in seconds waiting for server response |
| `--user-agent` | `-A` | bluesnake/1.0 | Custom User-Agent string |
| `--include-subdomains` | | false | Crawl all subdomains |
| `--max-urls` | | 0 | Maximum URLs to crawl (0 = unlimited, pauses when reached) |

#### JavaScript Rendering

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--js-rendering` | `-j` | false | Enable JavaScript rendering |
| `--initial-wait` | | 1500 | Initial wait after page load (ms) |
| `--scroll-wait` | | 2000 | Wait after scrolling (ms) |
| `--final-wait` | | 1000 | Final wait before capture (ms) |

#### Discovery Options

| Flag | Default | Description |
|------|---------|-------------|
| `--spider` | true | Enable link discovery by spidering |
| `--sitemap` | true | Enable sitemap URL discovery |
| `--sitemap-url` | | Custom sitemap URL |
| `--check-external` | true | Validate external resources |

#### Robots/Directives

| Flag | Default | Description |
|------|---------|-------------|
| `--robots-txt` | respect | Mode: respect, ignore, ignore-report |
| `--follow-nofollow-internal` | false | Follow internal nofollow links |
| `--follow-nofollow-external` | false | Follow external nofollow links |
| `--respect-noindex` | true | Respect X-Robots-Tag noindex |
| `--respect-meta-noindex` | true | Respect meta robots noindex |

#### Output Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | . | Output directory |
| `--format` | `-f` | json | Output format: json, csv |
| `--export` | `-e` | true | Export results to files after crawl |
| `--export-links` | | false | Export outlinks |
| `--export-content` | | false | Export text content of HTML pages |
| `--quiet` | `-q` | false | Suppress progress output |

#### Examples

```bash
# Basic crawl
bluesnake crawl https://example.com

# Crawl with a URL limit (pauses when reached)
bluesnake crawl https://example.com --max-urls 100

# Resume a paused crawl
bluesnake crawl --resume --project-id 1

# Resume with additional URL budget
bluesnake crawl --resume --project-id 1 --max-urls 50

# Crawl with custom settings
bluesnake crawl https://example.com \
  --parallelism 10 \
  --js-rendering \
  --include-subdomains \
  --format csv \
  --output ./results

# Crawl ignoring robots.txt
bluesnake crawl https://example.com --robots-txt ignore

# Crawl slow site with longer response timeout
bluesnake crawl https://slow-site.com --response-timeout 60

# Quiet mode (no progress output)
bluesnake crawl https://example.com -q -o ./results
```

#### Pause and Resume Workflow

When using `--max-urls`, the crawler will pause after reaching the limit and save pending URLs for later resumption:

```bash
# Start crawl with 100 URL limit
bluesnake crawl https://example.com --max-urls 100
# Output: "Crawl paused (budget reached)"
# Output: "Resume with: bluesnake crawl --resume --project-id 1"

# Check project status
bluesnake list projects

# Resume and crawl 50 more URLs
bluesnake crawl --resume --project-id 1 --max-urls 50

# Resume without limit (crawl all remaining URLs)
bluesnake crawl --resume --project-id 1
```

You can also pause manually with Ctrl+C, which saves the current state for later resumption.

### export

Export results from a completed crawl.

```bash
bluesnake export [flags]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--crawl-id` | `-c` | required | Crawl ID to export |
| `--output` | `-o` | . | Output directory |
| `--format` | `-f` | json | Output format: json, csv |
| `--export-links` | | false | Export outlinks |
| `--export-content` | | false | Export text content of HTML pages |
| `--type` | `-t` | all | Content type filter |

#### Examples

```bash
# Export as JSON
bluesnake export --crawl-id 123 -o ./export

# Export as CSV with links
bluesnake export --crawl-id 123 --format csv --export-links -o ./export
```

#### Output Files

- `internal_all.json` or `internal_all.csv` - All discovered URLs
- `all_outlinks.json` or `all_outlinks.csv` - All page links (if --export-links)
- `content/` - Directory containing text content files (if --export-content)
- `crawl_summary.json` - Crawl metadata and statistics

## Export Data Format

### internal_all.json

Contains all crawled URLs with their metadata. JSON format:

```json
{
  "crawlId": 1,
  "domain": "example.com",
  "crawlDateTime": "2025-01-26T12:00:00Z",
  "totalUrls": 150,
  "results": [
    {
      "url": "https://example.com/page",
      "status": 200,
      "title": "Page Title",
      "metaDescription": "Page description text",
      "h1": "Main Heading",
      "h2": "Subheading",
      "canonicalUrl": "https://example.com/page",
      "wordCount": 500,
      "contentHash": "abc123...",
      "indexable": "Indexable",
      "contentType": "text/html",
      "error": "",
      "depth": 1
    }
  ]
}
```

**Field descriptions:**

| Field | Type | Description |
|-------|------|-------------|
| `url` | string | Full URL of the page |
| `status` | int | HTTP status code (200, 301, 404, etc.) |
| `title` | string | Content of the `<title>` tag |
| `metaDescription` | string | Content of the meta description tag |
| `h1` | string | First H1 heading on the page |
| `h2` | string | First H2 heading on the page |
| `canonicalUrl` | string | Canonical URL if specified |
| `wordCount` | int | Word count of visible text content |
| `contentHash` | string | Hash of the page content (for duplicate detection) |
| `indexable` | string | "Indexable" or reason for non-indexability |
| `contentType` | string | MIME type (text/html, image/jpeg, etc.) |
| `error` | string | Error message if crawl failed |
| `depth` | int | Crawl depth (0 = start URL) |

### internal_all.csv

CSV format with ScreamingFrog-compatible headers:

| Column | Description |
|--------|-------------|
| Address | Full URL |
| Status Code | HTTP status code |
| Content Type | MIME type |
| Title 1 | Page title |
| Meta Description 1 | Meta description |
| H1-1 | First H1 heading |
| H2-1 | First H2 heading |
| Canonical Link Element 1 | Canonical URL |
| Word Count | Word count |
| Indexability | Indexable status |
| Crawl Depth | Depth from start URL |
| Content Hash | Content hash |

### all_outlinks.json

Contains all links discovered during the crawl. Exported when using `--export-links`. JSON format:

```json
{
  "crawlId": 1,
  "links": [
    {
      "sourceUrl": "https://example.com/page-a",
      "targetUrl": "https://example.com/page-b",
      "linkType": "anchor",
      "linkText": "Click here",
      "follow": true,
      "rel": "",
      "target": "_blank",
      "pathType": "Absolute",
      "position": "content"
    }
  ]
}
```

**Field descriptions:**

| Field | Type | Description |
|-------|------|-------------|
| `sourceUrl` | string | Page containing the link |
| `targetUrl` | string | Page being linked to |
| `linkType` | string | Type: "anchor", "image", "script", "stylesheet" |
| `linkText` | string | Anchor text or alt text for images |
| `follow` | bool | true if link should be followed (no nofollow/sponsored/ugc) |
| `rel` | string | Full rel attribute value |
| `target` | string | Target attribute (_blank, _self, etc.) |
| `pathType` | string | "Absolute", "Root-Relative", or "Relative" |
| `position` | string | Location: "content", "navigation", "header", "footer", "sidebar", "breadcrumbs", "pagination", "unknown" |

### all_outlinks.csv

CSV format with headers:

| Column | Description |
|--------|-------------|
| Source | Source URL |
| Destination | Target URL |
| Type | Link type |
| Anchor | Anchor/alt text |
| Follow | true/false |
| Rel | Rel attribute |
| Target | Target attribute |
| Path Type | URL path type |
| Link Position | Position in page |

### crawl_summary.json

Contains crawl metadata and statistics:

```json
{
  "crawlId": 1,
  "projectId": 1,
  "domain": "example.com",
  "exportedAt": "2025-01-26T12:30:00Z",
  "totalUrls": 150,
  "htmlPages": 75,
  "images": 40,
  "javascript": 15,
  "css": 10,
  "fonts": 5,
  "others": 5
}
```

**Field descriptions:**

| Field | Type | Description |
|-------|------|-------------|
| `crawlId` | int | Unique crawl identifier |
| `projectId` | int | Project identifier |
| `domain` | string | Crawled domain |
| `exportedAt` | string | ISO 8601 timestamp of export |
| `totalUrls` | int | Total URLs discovered |
| `htmlPages` | int | Count of HTML pages |
| `images` | int | Count of images |
| `javascript` | int | Count of JavaScript files |
| `css` | int | Count of CSS files |
| `fonts` | int | Count of font files |
| `others` | int | Count of other resource types |

### content/ (Text Content Export)

When using `--export-content`, extracted text content from HTML pages is exported to the `content/` subdirectory. Each HTML page gets its own `.txt` file containing the main content text (navigation, headers, footers, and boilerplate removed).

**Directory structure:**
```
output/
├── internal_all.json
├── crawl_summary.json
└── content/
    ├── index.txt
    ├── about_us.txt
    ├── blog_post-1.txt
    └── contact.txt
```

**File naming:**
- URLs are sanitized to create valid filenames
- Path separators (`/`) become underscores (`_`)
- Special characters are replaced with underscores
- All files have `.txt` extension

**Content extraction:**
- Main content is intelligently extracted using semantic HTML5 elements (`<article>`, `<main>`, `[role='main']`)
- Navigation, headers, footers, and sidebars are filtered out
- Script and style content is removed
- Whitespace is normalized

**Example content file:**
```
Welcome to Example Company

We provide innovative solutions for modern businesses.

Our Services

Web Development
We build responsive, modern websites using the latest technologies.

Mobile Apps
Native and cross-platform mobile applications for iOS and Android.

Contact us today to learn more about how we can help your business grow.
```

This export is useful for:
- Building search indexes
- Content analysis and auditing
- Training machine learning models
- SEO content review
- Accessibility analysis

### list

List projects or crawls.

```bash
# List all projects
bluesnake list projects

# List crawls for a project
bluesnake list crawls --project-id 1
```

### version

Show version information.

```bash
bluesnake version
```

## Building

```bash
# Build for current platform
go build -o bluesnake ./cmd/cli

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -o bluesnake-linux ./cmd/cli

# Cross-compile for Windows
GOOS=windows GOARCH=amd64 go build -o bluesnake.exe ./cmd/cli
```

## Data Storage

The CLI uses the same SQLite database as the Desktop app and HTTP server:
- Location: `~/.bluesnake/bluesnake.db`
- Text content: `~/.bluesnake/<domain>/<crawl-id>/`

## Comparison with ScreamingFrog CLI

| ScreamingFrog | BlueSnake | Notes |
|---------------|-----------|-------|
| `--headless` | (default) | CLI is always headless |
| `--crawl <url>` | `crawl <url>` | Same concept |
| `--export-tabs` | `--format csv` | Similar output |
| `--bulk-export` | `--export-links` | Modular export |
| `--output-folder` | `--output` | Same purpose |
| `--config <file>` | Individual flags | More granular |
| Response Timeout (20s) | `--response-timeout` (20s) | Same default |

## Related

- [HTTP Server](../server/README.md) - REST API server
- [API Documentation](../../API.md) - Complete API reference
- [Architecture](../../ARCHITECTURE.md) - System architecture
