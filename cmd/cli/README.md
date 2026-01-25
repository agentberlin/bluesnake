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

Start a new crawl for a URL.

```bash
bluesnake crawl <url> [flags]
```

#### Core Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--parallelism` | `-p` | 5 | Number of concurrent requests |
| `--user-agent` | `-A` | bluesnake/1.0 | Custom User-Agent string |
| `--include-subdomains` | | false | Crawl all subdomains |
| `--max-urls` | | 0 | Maximum URLs to crawl (0 = unlimited) |

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
| `--export-links` | | false | Export outlinks |
| `--quiet` | `-q` | false | Suppress progress output |

#### Examples

```bash
# Basic crawl
bluesnake crawl https://example.com

# Crawl with custom settings
bluesnake crawl https://example.com \
  --parallelism 10 \
  --js-rendering \
  --include-subdomains \
  --format csv \
  --output ./results

# Crawl ignoring robots.txt
bluesnake crawl https://example.com --robots-txt ignore

# Quiet mode (no progress output)
bluesnake crawl https://example.com -q -o ./results
```

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
- `crawl_summary.json` - Crawl metadata and statistics

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

## Related

- [HTTP Server](../server/README.md) - REST API server
- [API Documentation](../../API.md) - Complete API reference
- [Architecture](../../ARCHITECTURE.md) - System architecture
