# bluesnake — Design Document

A modern, headless, CLI-first website crawler and SEO auditor in Go. Functional parity target: Screaming Frog SEO Spider's crawling/auditing core — **without** the UI and **without** third-party API integrations (GA4, GSC, PageSpeed/Lighthouse, link indexes, AI providers), and **without** opaque binary config files: everything is plain-text config + flags.

Status: living document — **all milestones M0–M14 implemented** (2026-06-10); **§8/§9 backlog cleared** (2026-06-11): SERP pixel widths, ISO-registry hreflang validation, LSH near-dup banding, `rendering.wait_strategy`, custom JS snippets via CDP, WARC archiving, the `serve` JSON API, crawl-path report, HTTP version capture, a 164-check catalogue (second tranche 2026-06-12, §9), and the catalogue-coverage meta-test. Remaining deliberate cuts are listed in §9. The feature inventories this design is derived from live in `docs/research/`:
- [01-crawl-configuration.md](research/01-crawl-configuration.md) — every SF config option
- [02-data-model-and-checks.md](research/02-data-model-and-checks.md) — per-URL data, tabs/filters, 300+ issues, crawl analysis, link model, reports
- [03-operations-cli-storage.md](research/03-operations-cli-storage.md) — storage modes, resume, modes, CLI, comparison

---

## 1. Goals and non-goals

### Goals
1. **Crawl** any site (spider mode) or URL list (list mode) with full control: scope, limits, speed, include/exclude, URL rewriting, robots.txt handling, auth, custom headers/UA, proxy.
2. **Extract** the full Screaming Frog per-URL dataset: response data, indexability, on-page elements, directives, canonicals, pagination, hreflang, structured data, content metrics, security signals, link graph with rich edge data.
3. **Audit**: evaluate the full issues catalogue (issue/warning/opportunity × priority) that doesn't require external APIs.
4. **Persist**: disk-backed storage with continuous commit (crash-safe), pause/resume, resumable partial crawls, named projects, crawl IDs.
5. **Analyze** post-crawl: link score, redirect/canonical chains, near-duplicates, hreflang reciprocity, sitemap set-operations, orphans.
6. **Report/Export**: all tab/filter exports, bulk exports, reports, XML sitemap generation — CSV/JSON(L)/xlsx.
7. **Compare** two crawls: filter deltas (Added/New/Removed/Missing), change detection, URL mapping.
8. **Plain-text everything**: one YAML config schema covering every knob; CLI flags override; `bluesnake config init` emits a fully-commented default.
9. **Very good test coverage, BDD-first**: Gherkin acceptance specs + exhaustive table-driven unit tests written before each module's implementation.

### Non-goals
- GUI of any kind. *(Superseded 2026-06: a Wails desktop app now lives in `desktop/` as a thin shell over the same internal engine and `~/.bluesnake` store. The engine remains headless-first; every feature must land in the CLI and the engine before/alongside any UI surface.)*
- Third-party API integrations (GA4, Search Console, PSI/Lighthouse, Majestic/Ahrefs/Moz, AI embeddings/LLM features, Google Sheets/Drive/Looker).
- SERP mode pixel-perfect Google snippet simulation (we keep pixel-width calculation since title/description pixel filters depend on it, using a bundled font metrics table — but no interactive snippet editor).
- Built-in scheduler (cron exists; our CLI is fully scriptable; we document recipes).
- Spelling & grammar checking (v1: out; revisit — requires large dictionaries/language rules; the hook point is left in the data model: `spelling_errors`, `grammar_errors` columns nullable).

### Deliberate improvements over Screaming Frog
- Plain-text config (YAML) with JSON-schema validation, instead of `.seospiderconfig` binaries.
- First-class JSON/JSONL output for piping (SF is spreadsheet-centric).
- Single static binary; storage is embedded (SQLite), no JVM/memory allocation tuning.
- Discoverable exports: `bluesnake export --list` enumerates every exportable dataset.
- Go regex (RE2) everywhere — documented difference from SF's Java regex (no backtracking/lookahead; predictable performance).

---

## 2. Technology decisions

| Concern | Decision | Rationale |
|---|---|---|
| Language / min version | Go ≥ 1.25 | per environment |
| CLI framework | `spf13/cobra` | subcommand-rich CLI, completions, generated help |
| Config | YAML via `gopkg.in/yaml.v3` + defaults/validation layer | human-writable, commentable |
| Storage | SQLite via `modernc.org/sqlite` (pure Go, no cgo) — WAL mode | crash-safe continuous commit, queryable exports, single-file crawl DBs, no native deps |
| HTML parsing | `golang.org/x/net/html` (tokenizer/tree) + `github.com/PuerkitoBio/goquery` (CSS selectors) + `github.com/antchfx/htmlquery`/`xpath` (XPath custom extraction) | battle-tested; tolerant parsing like browsers/Google |
| Robots.txt | own implementation in `internal/robots` (Google REP: RFC 9309 + Google extensions) | exact UA-precedence/longest-match/allow-tie semantics + custom robots override + matched-line reporting need internals |
| JS rendering | `chromedp` (CDP, headless Chrome) — optional at runtime, feature-gated | parity with SF's Chromium rendering; binary stays pure-Go when not rendering |
| Crawl frontier | own implementation (in-memory queue mirrored to SQLite `frontier` table) | resume semantics + per-host politeness need custom structure |
| Near-duplicates | own minhash (shingling + 128 perms + LSH banding) | small, well-understood; SF parity |
| Link score | power-iteration PageRank over SQLite-loaded edge list | standard |
| xlsx export | `xuri/excelize/v2` | only writer needed |
| WARC archiving | `slyrz/warc` or own minimal writer (later phase) | |
| BDD | `cucumber/godog` Gherkin features + stdlib `testing` table-driven unit tests | explicit user requirement |
| Lint/CI | `gofmt`, `go vet`, `staticcheck`; GitHub Actions later | |

Module path: `github.com/agentberlin/bluesnake`.

---

## 3. CLI surface

```
bluesnake crawl <url>                  # spider mode crawl
bluesnake list <file|->                # list mode (file, stdin, or --sitemap <url>)
bluesnake resume <crawl-id>            # resume a paused/interrupted crawl
bluesnake crawls [ls|rm|export|info]   # manage stored crawls (IDs, projects)
bluesnake analyze <crawl-id>           # (re-)run post-crawl analysis
bluesnake export <crawl-id> ...        # tab/filter/bulk exports; --list to discover
bluesnake report <crawl-id> ...        # named reports; --list to discover
bluesnake issues <crawl-id>            # issues summary (and per-issue export)
bluesnake sitemap <crawl-id>           # generate XML sitemap(s) from a crawl
bluesnake compare <id-prev> <id-curr>  # crawl comparison (+ change detection)
bluesnake robots test <url...>         # robots.txt tester (live or --robots-file)
bluesnake config init|validate|show    # emit commented default config / validate / effective config
bluesnake serve                        # read-only localhost JSON API over the crawl store (--addr)
bluesnake mcp                          # MCP server for LLM agents over streamable HTTP (--addr, default 127.0.0.1:8473)
```

Global flags: `--config <file>`, `--store-dir <dir>` (default `~/.bluesnake`), `--project <name>`, `--task <name>`, `--output <dir>`, `--format csv|json|jsonl|xlsx`, `--timestamped-output`, `--overwrite`, `--quiet/--verbose`, `--log json|text`.

Every config key is overridable as a flag using dotted names: `--set spider.limits.max_depth=3 --set speed.max_threads=10` plus dedicated shorthand flags for the common ones (`--depth`, `--threads`, `--rate`, `--include`, `--exclude`, `--user-agent`, ...).

Crawl UX (headless but informative): single-line progress (crawled/queued/errors/URLs-sec), `--progress none|line|live`; non-zero exit codes contract: `0` ok, `1` crawl error, `2` config error, `3` interrupted (resumable).

`Ctrl-C` = graceful pause (frontier + state committed; prints `bluesnake resume <id>` hint). Second `Ctrl-C` = hard stop (still safe by WAL).

---

## 4. Configuration schema (YAML)

One file = one crawl profile. Everything has a default; an empty file is a valid config. Full schema (abridged here; canonical commented version is emitted by `bluesnake config init` and kept in `docs/examples/bluesnake.yaml`):

```yaml
mode: spider            # spider | list  (set implicitly by CLI subcommand)

scope:
  crawl_all_subdomains: false
  crawl_outside_start_folder: false
  check_links_outside_start_folder: true
  follow_internal_nofollow: false
  follow_external_nofollow: false
  crawl_invalid_links: false
  cdns: []              # domains (optionally domain/path) treated as internal
  include: []           # RE2 partial-match patterns vs URL-encoded address
  exclude: []

resources:              # store/crawl pairs (SF Spider > Crawl); off by default,
  images:        {store: false, crawl: false}   # matching our house SF profile
  media:         {store: false, crawl: false}
  css:           {store: false, crawl: false}
  javascript:    {store: false, crawl: false}
  swf:           {store: false, crawl: false}
links:
  internal:      {store: true,  crawl: true}
  external:      {store: false, crawl: false}   # house SF profile: externals not checked
  canonicals:    {store: true,  crawl: false}   # recorded but not fetched (house SF profile)
  pagination:    {store: false, crawl: false}
  hreflang:      {store: true,  crawl: false}
  amp:           {store: false, crawl: false}
  meta_refresh:  {store: true,  crawl: true}
  iframes:       {store: true,  crawl: true}
  mobile_alternate: {store: false, crawl: false}
  uncrawlable:   {store: false}

sitemaps:
  crawl_linked: true             # house SF profile: sitemaps crawled,
  auto_discover_via_robots: true # discovered via robots.txt Sitemap: lines
  urls: []

llms_txt:                        # /llms.txt audit (llmstxt.org); site-level file
  check: true                    # fetch & structurally validate /llms.txt per host
  fetch_full: true               # also fetch /llms-full.txt
  crawl_linked: true             # admit the curated links into the frontier
                                 # (analysis.llms_txt gates the link cross-check)

extraction:
  page_details: {titles: true, meta_descriptions: true, meta_keywords: true,
                 h1: true, h2: true, indexability: true, word_count: true,
                 readability: true, text_to_code_ratio: true, hash: true,
                 page_size: true, forms: true}
  url_details:  {response_time: true, last_modified: true, http_headers: true, cookies: false}
  directives:   {meta_robots: true, x_robots_tag: true}
  structured_data: {jsonld: false, microdata: false, rdfa: false,
                    schema_org_validation: false, google_rich_results_validation: false,
                    case_sensitive: false}
  store_html: false
  store_rendered_html: false
  store_warc: false      # archive every fetched response as WARC/1.1 (any status, incl. redirects/errors)
  pdf: {store: false, extract_properties: false, extract_link_text: false}

limits:
  max_urls: 5000000
  max_depth: -1          # -1 = unlimited
  max_urls_per_depth: -1
  max_folder_depth: -1
  max_query_strings: -1
  max_per_subdomain: -1
  max_redirects: 5
  max_url_length: 10000
  max_links_per_page: 10000
  max_page_size_kb: 51200
  by_path: []            # [{pattern: "/blog/", max: 100}]

rendering:
  mode: text             # text | javascript   (old AJAX scheme intentionally dropped: deprecated by Google 2018)
  wait_strategy: adaptive # adaptive (settle detection) | fixed (load event + full AJAX sleep; compare-stable)
  ajax_timeout_sec: 5
  window: googlebot-desktop   # preset or {width: , height: }
  screenshots: false
  js_error_reporting: false
  flatten_shadow_dom: true
  flatten_iframes: true
  chrome_path: ""        # auto-detect

advanced:
  cookie_storage: session        # session | persistent | none
  ignore_non_indexable_for_issues: false
  ignore_paginated_for_duplicates: false
  always_follow_redirects: false
  always_follow_canonicals: false
  respect_noindex: false
  respect_canonical: false
  respect_next_prev: false
  respect_hsts: true
  respect_self_referencing_meta_refresh: true
  extract_srcset: false
  crawl_fragments: false
  html_validation: false
  assume_pages_are_html: false
  response_timeout_sec: 20
  retry_5xx: 0                   # number of retries
  percent_encoding: upper        # upper | lower

thresholds:                      # SF "Preferences"
  title:        {min_chars: 30, max_chars: 60, min_px: 200, max_px: 561}
  description:  {min_chars: 70, max_chars: 155, min_px: 400, max_px: 985}
  url_max_chars: 115
  h1_max_chars: 70
  h2_max_chars: 70
  image_alt_max_chars: 100
  image_max_kb: 100
  low_content_words: 200
  high_crawl_depth: 4
  high_internal_outlinks: 1000
  high_external_outlinks: 100
  non_descriptive_anchors: ["click here", "click", "here", "read more", "more", "learn more", "go", "this page", "start", "right here"]
  soft_404_patterns: ["page not found", "404", "not be found"]

content:
  area: {include_elements: [], include_classes: [], include_ids: [],
         exclude_elements: [nav, footer], exclude_classes: [], exclude_ids: []}
  near_duplicates: {enabled: false, threshold: 90, indexable_only: true}

robots:
  mode: respect          # respect | ignore | ignore-report
  show_blocked_internal: true
  show_blocked_external: false
  custom: []             # [{host: "example.com", file: "./custom-robots.txt"}]

url_rewriting:
  remove_params: []      # ["utm_source", "sessionid"]
  regex_replace: []      # [{pattern: "", replace: ""}]
  lowercase: false

speed:
  max_threads: 5
  max_urls_per_sec: 0    # 0 = unlimited

http:
  user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 12_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.4 Safari/605.1.15"  # house SF profile UA
  robots_user_agent: "bluesnake"
  version: ""            # "" = negotiate (prefer HTTP/2) | "1.1" force HTTP/1.1 | "2"
  browser_headers: true  # send SF's default request profile: Accept text/html + Cache-Control/Pragma no-cache
  headers: {}            # name: value (override the browser defaults above; e.g. add Accept-Language)
  proxy: ""              # http://user:pass@host:port
  trusted_cert_dirs: []
  auth:
    basic: []            # [{url_prefix: "", username: "", password: "", password_env: ""}]
    cookies: []          # [{name: , value: , domain: }]  (forms-auth replacement: bring your own session cookie)

custom_search: []        # [{name:, mode: contains|not_contains, pattern:, regex: false, scope: html|text|element:<sel>}]
custom_extraction: []    # [{name:, type: xpath|css|regex, expression:, attribute:, return: text|html|inner_html|function}]
custom_js: []            # [{name:, type: extraction|action, file: snippet.js, timeout_sec: 5, content_types: [text/html]}]

link_positions:          # ordered; first match wins; substring match on link XPath/element chain
  - {name: head,    match: "/head/"}   # SF's decoded default search terms: the trailing
  - {name: nav,     match: "nav"}      # slash keeps <header> links out of the head bucket,
  - {name: header,  match: "header"}   # and the bare terms also match class names once
  - {name: sidebar, match: "aside"}    # element paths carry attribute qualifiers
  - {name: footer,  match: "footer"}
  - {name: content, match: "/"}
store_link_paths: true

list_mode:               # only used by `bluesnake list`
  respect_robots: false  # SF: list mode ignores robots by default
  crawl_depth: 0

analysis:
  auto: true             # run crawl analysis at end of crawl
  link_score: true
  redirect_chains: true
  near_duplicates: true
  pagination: true
  hreflang: true
  canonicals: true
  links: true
  sitemaps: true
  llms_txt: true         # cross-check /llms.txt curated links against the crawl

storage:
  dir: ~/.bluesnake       # crawl DBs live here, one SQLite file per crawl
  retention_days: 0      # 0 = keep forever
compare:
  change_detection: [titles, descriptions, h1, word_count, crawl_depth, links, structured_data, content]
  content_change_threshold: 10
  url_mapping: []        # [{pattern: "^https://staging\\.", replace: "https://www."}]
```

Validation: unknown keys are errors (with a "did you mean" suggestion); every regex is compile-checked at load; `config validate` runs the same code path.

---

## 5. Architecture

### 5.1 Package layout

```
cmd/bluesnake/            main; cobra commands only (thin: parse flags → call internal)
internal/config/         schema structs, defaults, YAML load/merge/validate, flag --set overlay
internal/urlutil/        normalization, resolution, rewriting, include/exclude, classification
                         (internal/external, folder depth, path type), fragment & encoding rules
internal/robots/         REP parser/matcher (Google semantics), per-host cache, custom overrides,
                         matched-line reporting, sitemap discovery
internal/llmstxt/        /llms.txt parser/validator (llmstxt.org): H1 title, blockquote summary,
                         H2 section link lists; pure (fetch/admit/issues live in crawler/analyze)
internal/fetch/          HTTP client: timeouts, retries, HSTS emulation, auth, headers, UA,
                         proxy, cookies, TLS, redirects-as-data (never auto-follow), rate metering
internal/parse/          HTML tokenization → PageFacts: elements, directives, links (typed edges),
                         forms, security signals, head-validity, content area text, word count,
                         readability, hash, structured-data raw blocks
internal/extract/        custom search, custom extraction (xpath/css/regex) over parsed docs
internal/structured/     JSON-LD/Microdata/RDFa parsing + schema.org validation (+ rich results later)
internal/render/         chromedp session pool, rendered DOM, screenshots, console log, custom JS
internal/frontier/       dedup set + priority FIFO by depth, per-host queues, politeness,
                         limits enforcement, SQLite mirroring for resume
internal/crawler/        orchestrator: worker pool, pipeline (fetch→parse→evaluate→store→discover),
                         pause/resume, signal handling, progress events
internal/store/          SQLite schema + repositories (pages, links, frontier, sitemaps, issues,
                         analysis results, crawl meta), migrations, crawl manager (IDs/projects)
internal/indexability/   the indexability state machine (status + reason)
internal/issues/         rule engine: per-URL rules + aggregate rules; catalogue with id/severity/priority
internal/analyze/        post-crawl: link score, chains (redirect/canonical), near-dup minhash,
                         hreflang reciprocity, pagination sequence, sitemap set-ops, orphans,
                         inlink-derived flags
internal/export/         tab/filter datasets, bulk exports, writers (csv/json/jsonl/xlsx)
internal/report/         named reports (crawl overview, chains, insecure content, ...)
internal/sitemapgen/     XML sitemap + image sitemap generation w/ splitting + index
internal/compare/        crawl comparison, change detection, URL mapping
internal/serpwidth/      text pixel-width measurement (bundled font metrics table)
internal/isocodes/       embedded ISO 639-1 + ISO 3166-1 registries (hreflang validation)
internal/warc/           minimal WARC/1.1 writer (extraction.store_warc archives)
internal/serve/          read-only localhost JSON API over stored crawls
internal/mcp/            MCP server (hand-rolled JSON-RPC 2.0 over the streamable-HTTP transport):
                         12 tools — crawl control (start/status/pause/resume/stop), config
                         introspection (knob catalogue via reflection over the schema, profiles),
                         and read-only SQL over the per-crawl SQLite DBs. Crawl control runs
                         against a Backend interface: the CLI uses the built-in Runner; the
                         desktop app adapts its session manager so agent-started crawls stream
                         live into the UI (Settings ▸ MCP Server toggle, persisted in desktop.json)
internal/version/
features/                Gherkin .feature files (BDD acceptance specs)
test/                    godog step definitions, fixture site builder (httptest), golden files
docs/
```

### 5.2 Crawl pipeline

```
                    ┌────────────┐
   seeds ─────────► │  frontier  │ ◄───────────── discovered URLs (post rewrite/include/exclude/limits/robots)
                    └─────┬──────┘
                          │ next(host-politeness, rate limit)
                    ┌─────▼──────┐
                    │  worker ×N │  fetch (or render)  ── HSTS/redirect/timeout/retry handled here
                    └─────┬──────┘
                          │ Response
                    ┌─────▼──────┐
                    │   parse    │  PageFacts + typed link edges (+ custom search/extraction)
                    └─────┬──────┘
                          │
                    ┌─────▼──────┐
                    │  evaluate  │  indexability, per-URL issues, security checks
                    └─────┬──────┘
                          │
                    ┌─────▼──────┐
                    │   store    │  continuous commit (SQLite WAL, batched tx)
                    └─────┬──────┘
                          │ outlinks
                          └──────────► discovery filter chain → frontier
```

- Workers = `speed.max_threads`; a global token-bucket enforces `max_urls_per_sec`; per-host serialization (one in-flight request per host by default) prevents hammering a single origin while still saturating multi-host crawls (externals).
- The **discovery filter chain** (pure function, heavily unit-tested): resolve → rewrite (remove params, regex, lowercase, encoding) → fragment strip (unless `crawl_fragments`) → scheme check → invalid-URL policy → scope classification (internal/external/CDN) → store/crawl flags per link type → include/exclude → robots (mode-aware) → limits (depth, folder depth, query strings, URL length, per-path, per-subdomain, total) → dedup → frontier.
- Redirects are **data, not transport**: the client never auto-follows; a 3xx page is stored with its `Location`, and the target re-enters discovery (depth+1), bounded by `limits.max_redirects` chain length (chains reconstructed in analysis). HSTS is emulated: after seeing a valid `Strict-Transport-Security`, subsequent http:// requests to that host (+subdomains if `includeSubDomains`) are turned around locally as synthetic `307 HSTS Policy`.
- **Browser-like requests** (`http.browser_headers`, on by default): the fetcher mirrors Screaming Frog v24.1's measured default request profile — a navigational `Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8` (byte-identical to SF's) plus `Cache-Control: no-cache` and `Pragma: no-cache`; configured `http.headers` override any of them. SF sends no `Accept-Language`, so neither do we by default (add one via `http.headers`); `Accept-Encoding` is left unset so the transport sends `gzip` exactly as SF does and keeps transparent decompression. Go's `net/http` sends no `Accept` at all by default, which trips bot-protection layers that gate on it: on scale.jobs, Clerk/Vercel returns `403` to a request with a missing or `*/*` Accept and `307 → accounts.sign-in` once `text/html` is present — independent of the UA string and the HTTP version, so this (not the transport) was the cause of the parity gap on ~61 protected pages. `http.version` (`""` = prefer HTTP/2, `"1.1"` forces HTTP/1.1 by clearing `TLSNextProto`, `"2"`) is a separate anti-fingerprinting/SF-parity knob (SF ran HTTP/1.1); empirically it does not affect the scale.jobs response on its own.
- **Pause/resume**: frontier and visited-set live in SQLite alongside results; resume = reload frontier + config snapshot (config is frozen into the crawl DB at start; resume refuses a changed config unless `--force`).

### 5.3 Storage schema (SQLite, one DB file per crawl)

`~/.bluesnake/crawls/<crawl-id>.db`, plus a tiny registry DB `~/.bluesnake/registry.db` (crawl id, project, task name, seed, mode, started/finished, status, and two URL counts: `crawled` = fetched and `total` = encountered — Screaming Frog's "URLs Crawled" vs "URLs Encountered" split, where encountered also covers robots-blocked/errored URLs). `total` is the headline count shown across the CLI, desktop and MCP (`crawled` is reported alongside as the fetched subset); crawls finished before `total` existed are backfilled lazily from a `COUNT(*)` over `pages`. Crawl ID = `<yyyymmdd-hhmmss>-<short-rand>`.

```sql
-- meta
CREATE TABLE crawl_meta (key TEXT PRIMARY KEY, value TEXT);          -- config_json, seed, mode, version, started_at...

-- one row per unique URL encountered (crawled or not)
CREATE TABLE pages (
  id INTEGER PRIMARY KEY,
  url TEXT NOT NULL UNIQUE,            -- normalized display URL
  url_encoded TEXT NOT NULL,           -- RFC3986 form actually requested
  scope TEXT NOT NULL,                 -- internal | external
  content_type TEXT, status_code INTEGER, status TEXT,
  http_version TEXT,
  crawl_state TEXT NOT NULL,           -- queued|crawled|blocked_robots|error|skipped_<reason>
  fetch_error TEXT,
  matched_robots_line INTEGER,         -- when blocked
  response_time_ms INTEGER, last_modified TEXT,
  size_bytes INTEGER, transferred_bytes INTEGER,
  hash TEXT,                           -- md5 of body
  crawl_depth INTEGER, folder_depth INTEGER,
  discovered_at INTEGER, crawled_at INTEGER,
  redirect_url TEXT, redirect_type TEXT,   -- http|hsts|meta_refresh|javascript
  indexability TEXT, indexability_status TEXT,
  -- on-page elements (first two instances + counts)
  title1 TEXT, title2 TEXT, title_count INTEGER, title1_px INTEGER,
  desc1 TEXT, desc2 TEXT, desc_count INTEGER, desc1_px INTEGER,
  keywords1 TEXT, keywords2 TEXT, keywords_count INTEGER,
  h1_1 TEXT, h1_2 TEXT, h1_count INTEGER,
  h2_1 TEXT, h2_2 TEXT, h2_count INTEGER,
  meta_robots1 TEXT, meta_robots2 TEXT, x_robots1 TEXT, x_robots2 TEXT,
  meta_refresh TEXT,
  canonical_html1 TEXT, canonical_html2 TEXT, canonical_http1 TEXT, canonical_http2 TEXT,
  rel_next_html TEXT, rel_prev_html TEXT, rel_next_http TEXT, rel_prev_http TEXT,
  -- content metrics
  word_count INTEGER, text_ratio REAL, avg_words_per_sentence REAL,
  flesch_score REAL, readability TEXT, language TEXT,
  -- flags packed as JSON for long-tail booleans (head validity, security signals, AMP, forms...)
  facts JSON,
  -- analysis outputs (filled by analyze phase)
  link_score REAL, inlinks INTEGER, unique_inlinks INTEGER,
  outlinks INTEGER, unique_outlinks INTEGER,
  ext_outlinks INTEGER, unique_ext_outlinks INTEGER,
  closest_similarity REAL, near_dup_count INTEGER
);
CREATE INDEX idx_pages_state ON pages(crawl_state);
CREATE INDEX idx_pages_status ON pages(status_code);

-- typed link edges
CREATE TABLE links (
  id INTEGER PRIMARY KEY,
  src INTEGER NOT NULL REFERENCES pages(id),
  dst INTEGER NOT NULL REFERENCES pages(id),
  type TEXT NOT NULL,        -- hyperlink|image|css|js|media|iframe|canonical|hreflang|next|prev|amp|meta_refresh|http_redirect|form_action|mobile_alternate|uncrawlable
  anchor TEXT, alt TEXT,
  follow INTEGER NOT NULL DEFAULT 1,
  rel TEXT, target TEXT,
  path_type TEXT,            -- absolute|protocol_relative|root_relative|path_relative
  link_path TEXT,            -- element XPath
  position TEXT,             -- head|nav|content|sidebar|footer|...
  origin TEXT NOT NULL DEFAULT 'html',  -- html|rendered|both
  attrs JSON                 -- hreflang code, img dimensions, etc.
);
CREATE INDEX idx_links_src ON links(src); CREATE INDEX idx_links_dst ON links(dst);

CREATE TABLE frontier (      -- pending queue (deleted as crawled) → resume support
  page_id INTEGER PRIMARY KEY REFERENCES pages(id),
  depth INTEGER NOT NULL, enqueued_at INTEGER NOT NULL
);

CREATE TABLE headers   (page_id INTEGER, dir TEXT, name TEXT, value TEXT);  -- dir: req|resp
CREATE TABLE cookies   (page_id INTEGER, name TEXT, value TEXT, domain TEXT, expiry TEXT, secure INTEGER, httponly INTEGER, source TEXT);
CREATE TABLE hreflang  (page_id INTEGER, source TEXT, lang TEXT, url TEXT, valid_code INTEGER);  -- source: html|http|sitemap
CREATE TABLE structured_data (page_id INTEGER, format TEXT, raw JSON, types JSON, errors JSON, warnings JSON);
CREATE TABLE custom_results (page_id INTEGER, kind TEXT, name TEXT, value TEXT);  -- kind: search|extraction|js
CREATE TABLE sitemap_entries (sitemap_url TEXT, url TEXT, lastmod TEXT, attrs JSON);
CREATE TABLE llmstxt       (url TEXT PRIMARY KEY, kind TEXT, status INT, found INT,  -- one row per /llms.txt + /llms-full.txt
                            title TEXT, summary TEXT, malformed INT, content TEXT);  -- (structural validation outcome)
CREATE TABLE llmstxt_links (src TEXT, url TEXT, section TEXT, anchor TEXT);          -- curated links (provenance, cross-checked in analysis)
CREATE TABLE issues (
  page_id INTEGER NOT NULL REFERENCES pages(id),
  issue_id TEXT NOT NULL,    -- stable snake_case id, e.g. title_missing
  detail TEXT,
  PRIMARY KEY (page_id, issue_id)
);
CREATE TABLE blobs (page_id INTEGER, kind TEXT, path TEXT);  -- stored html/rendered/pdf/screenshot file refs (filesystem-backed under <crawl-id>.assets/)
CREATE TABLE analysis_meta (key TEXT PRIMARY KEY, value TEXT);  -- which analyses ran, params (e.g. near-dup threshold)
```

Write strategy: workers push results to a single writer goroutine; batched transactions (N=200 pages or 500 ms, whichever first) → continuous commit with bounded fsync cost. WAL mode + `synchronous=NORMAL`.

Issue definitions (name, severity, priority, description, trigger doc) live in code (`internal/issues/catalogue.go`) as the single source of truth; `issues` table stores only occurrences.

### 5.4 Indexability state machine

A URL is **Non-Indexable** with the first matching reason:
`Blocked by robots.txt` → `No Response / Connection Error` → `Client Error (4xx)` / `Server Error (5xx)` → `Redirected` (3xx/meta-refresh/JS) → `noindex` (meta robots or X-Robots-Tag, robots-UA-scoped) → `Canonicalised` (canonical present and ≠ self) → else **Indexable**. Non-HTML 200s (images/css/js/pdf) are Indexable unless header directives say otherwise. `respect_*` advanced options change crawl/report behaviour, not the state machine.

### 5.5 Issues engine

Two rule classes:
1. **Per-page rules** — pure functions `func(page *PageFacts, cfg *Config) []Issue`, evaluated in the pipeline (e.g. `title_missing`, `url_uppercase`, `security_missing_hsts`). Cheap, streaming.
2. **Aggregate rules** — need cross-URL state, evaluated either incrementally with small indexes (duplicate titles via `hash(title)→count` map) or in the analysis phase (near-dups, hreflang reciprocity, orphans, chains).

Catalogue: every issue has `ID` (stable, snake_case), `Tab`, `Name`, `Severity` (issue|warning|opportunity), `Priority` (high|med|low), `Description`, `HowToFix`. The full SF catalogue from research doc 02 is encoded; integration-only issues omitted.

### 5.6 Post-crawl analysis (`bluesnake analyze`, auto by default)

Each analyzer reads SQLite, writes back columns/tables; all are idempotent and re-runnable (e.g. after changing near-dup threshold — mirrors SF):
- **link_score**: PageRank (d=0.85, 40 iters or ε<1e-6) over followed internal hyperlinks; scaled 0–100.
- **chains**: follow redirect/canonical edges → `redirect_chains` result table (source, hops, loop flag, final status, chain type incl. mixed).
- **near_duplicates**: 5-word shingles of content-area text → minhash(128) → LSH candidate pairs → exact Jaccard verify ≥ threshold.
- **hreflang**: reciprocity matrix, return-link checks, code validation (ISO 639-1 / 3166-1), x-default/self-reference, canonical consistency.
- **pagination**: sequence reciprocity, loops, unlinked pagination URLs.
- **canonicals/links**: unlinked-canonical detection, inlink-only-nofollow / non-indexable-inlinks-only flags, aggregates (unique in/outlinks, % of total).
- **sitemaps**: set ops between sitemap entries and crawled URLs → in/not-in/orphans/non-indexable-in-sitemap/multiple.
- **llms_txt**: structural validation of each fetched `/llms.txt` (missing / no-H1 / no-summary / malformed list / missing `/llms-full.txt`, keyed on the file URL) plus cross-checking every curated link against the crawl graph: broken (non-200), non-indexable, or unverified (not reached — e.g. external with externals off). The file is fetched out-of-band for the seed host at crawl start (like robots.txt); curated links are admitted to the frontier through the normal discovery filter chain (external links obey the external-crawl gate, unlike sitemap entries) unless `llms_txt.crawl_linked` is off, with provenance recorded in `llmstxt_links` independently of the link graph.

### 5.7 Compare

`bluesnake compare <prev> <curr>`: attaches both DBs, applies URL mapping regexes to previous, computes per-filter membership deltas (Added/New/Removed/Missing per SF semantics) and element change detection (title/desc/h1/word-count/depth/link-metrics/content-similarity), writes a comparison report (terminal summary + exportable CSV/JSON).

### 5.8 Rendering (phase 2)

`chromedp` pool (size = min(threads, cores-scaled cap: 2/4/8 tabs); per-page: navigate, wait until the page **settles**, snapshot rendered DOM, optional screenshot, console log capture, custom JS execution (action snippets then extraction snippets). Parse pipeline runs twice (raw + rendered) and diffs element sets → JavaScript tab data (`origin` on link edges, `*_rendered` facts). Resource blocking by robots reported as Blocked Resource.

**Settle detection** (`internal/render`): navigation does **not** wait for the browser `load` event (background media can hold it open for many seconds after the DOM is done); the anchor is `DOMContentLoaded`. After DCL, a page is settled when any of:
1. the countable network is fully idle for 500ms — media, websockets, EventSource, ping/beacon, prefetch and `blob:`/`data:` requests are excluded from the in-flight set (they routinely stay open forever);
2. the DOM node count holds steady across two 500ms probes with no script/stylesheet/XHR/fetch in flight (absorbs third-party widgets and analytics that chatter indefinitely);
3. the wire is completely silent for 1.5s (only permanently-open requests remain).

`rendering.ajax_timeout_sec` is the **hard cap** on the settle phase after DCL (not a fixed sleep); `advanced.response_timeout_sec` caps the wait for DCL itself. Worst case therefore equals the old fixed-wait behaviour. Regression tests cover early settle, permanently-open streams/iframes, and beacon chatter.

**Wait strategy knob** (`rendering.wait_strategy: adaptive | fixed`): adaptive is the settle detection above; `fixed` waits for the browser load event and then sleeps the *full* AJAX timeout before snapshotting — slower, but the snapshot moment is deterministic, which keeps `compare` runs stable on pages with flaky widgets.

**Custom JavaScript snippets** (`custom_js`): snippet files load at renderer construction (a missing file is a config error naming the snippet). After the page settles, `action` snippets run first (results discarded — they exist to mutate the page), then `extraction` snippets; values are stored in `custom_results` with kind `js` (JS strings verbatim, anything else compact JSON, `error: …` when a snippet throws). Each snippet is bounded by its `timeout_sec` (default 5); a `content_types` list restricts which pages a snippet's results are stored for.

---

## 6. Testing strategy (BDD)

### Layers
1. **Gherkin acceptance specs** (`features/*.feature`, run by godog via `go test ./test/...`): behaviour of the whole CLI/crawler against fixture sites served by `httptest`. Written **first**, before implementation; scenarios for unimplemented modules are tagged `@pending` (skipped, counted) and un-tagged as modules land.
2. **Module unit tests** (`internal/<pkg>/*_test.go`): exhaustive table-driven tests, written before the module's implementation (red → green). Pure-function bias makes this cheap (discovery filter chain, indexability, issue rules, robots matcher, URL ops are all pure).
3. **Integration tests** (`test/integration/`): crawler against rich fixture sites (mini-site with every pathology: redirect chains/loops, robots cases, hreflang clusters, dup content, broken links, security header matrix...), assert stored DB contents and export outputs via golden files.
4. **Property/fuzz tests** where parsing is involved: `urlutil` normalization (idempotence, round-trip), robots matcher (vs reference cases from RFC 9309 + Google's published test suite), HTML parser resilience.

### Fixture site builder
`test/fixture` provides a declarative builder: `site.Page("/a").Title("x").LinksTo("/b", "/c").Noindex()...` → handlers on `httptest.Server`. One canonical "kitchen-sink site" exercises every issue in the catalogue; per-feature focused sites keep scenarios readable.

### Conventions
- Every issue in the catalogue must have at least one fixture page that triggers it and one that doesn't — enforced by the catalogue-coverage meta-test (`internal/analyze/coverage_test.go`): a kitchen-sink page set must trigger every catalogue ID, and a fully healthy two-page fixture must trigger zero occurrences. Adding a check without a fixture fails the suite.
- Golden files under `test/golden/`; regenerate with `-update` flag.
- Coverage gate: `make test` fails under 85% for `internal/...` (excluding `render`, which needs Chrome and is build-tagged `chrome`).
- `@chrome`-tagged features are excluded from the default acceptance run; on a machine with Chrome run them with `BLUESNAKE_FEATURE_TAGS="@chrome" go test ./test/`. Chrome-dependent Go tests skip themselves when no Chrome is found.

---

## 7. Milestones (test-first, module by module)

| # | Milestone | Modules | Acceptance feature files |
|---|---|---|---|
| M0 | Scaffold + config | `config`, `cmd` skeleton | `config.feature` |
| M1 | URL handling | `urlutil` | `url_normalization.feature`, `include_exclude.feature`, `url_rewriting.feature` |
| M2 | Robots | `robots` | `robots.feature` (incl. tester subcommand) |
| M3 | Fetching | `fetch` | `fetch.feature` (timeouts, retries, HSTS, auth, headers, redirects-as-data) |
| M4 | Parsing | `parse`, `indexability` | `parse_elements.feature`, `links.feature`, `indexability.feature`, `security.feature` |
| M5 | Crawl engine | `frontier`, `crawler` | `spider_crawl.feature`, `limits.feature`, `speed.feature` |
| M6 | Storage + resume | `store` | `storage.feature`, `pause_resume.feature`, `crawls_management.feature` |
| M7 | Issues engine | `issues` | `issues_*.feature` (per tab group) |
| M8 | Analysis | `analyze` | `analysis_linkscore.feature`, `chains.feature`, `near_duplicates.feature`, `hreflang.feature`, `sitemaps_analysis.feature` |
| M9 | Exports/reports/sitemap | `export`, `report`, `sitemapgen`, `serpwidth` | `export.feature`, `reports.feature`, `sitemap_generation.feature` |
| M10 | List mode + compare | `compare`, list mode in `crawler` | `list_mode.feature`, `compare.feature` |
| M11 | Custom search/extraction | `extract` | `custom_search.feature`, `custom_extraction.feature` |
| M12 | Structured data | `structured` | `structured_data.feature` |
| M13 | Rendering (Chrome) | `render`, custom JS | `rendering.feature` (build-tagged) |
| M14 | Long tail | AMP validation, HTML validation tab, archive/WARC, accessibility (axe via CDP) | respective features |

Definition of done per milestone: feature file(s) green, unit coverage ≥ 85% for the module, `go vet`/`staticcheck` clean, design doc updated if reality diverged.

---

## 8. Open questions / future
- Spelling/grammar: candidate libs need evaluation; schema already reserves columns.
- Distributed crawling: out of scope; single-process concurrency is the design point.
- Windows support: nothing platform-specific except Chrome discovery; CI matrix later.
- **Settle thresholds are code constants, not config** (decided 2026-06-11): 500ms
  network-idle window, 1.5s wire-silence window, 2×500ms DOM-stability probes
  (`internal/render`) stay fixed — `rendering.wait_strategy: fixed` is the escape hatch
  when adaptive settling misbehaves, so per-threshold knobs would add config surface
  without a use case. If pages ever settle wrongly, the precision upgrade is a
  `MutationObserver` injected at document start ("ms since last DOM mutation") instead
  of polling node counts.

Resolved (2026-06-11): `serve` subcommand shipped (`internal/serve`, read-only JSON API
over the export layer); `rendering.wait_strategy: adaptive | fixed` shipped (§5.8).

## 9. Implementation status & deltas (2026-06-11)

All milestones M0–M14 are implemented, and the previously-tracked gaps have
landed: `internal/serpwidth` (bundled Arial metrics; title/description Over/
Below X Pixels checks + pixel_width export columns), hreflang validation
against the embedded ISO 639-1 / 3166-1 registries (`internal/isocodes`),
LSH banding in front of the exact near-duplicate verification (band width
adapts to the threshold; exact-equivalent at the default 90%), custom
JavaScript snippets executed via CDP (action → extraction ordering, results
in `custom_results` kind `js`), WARC/1.1 archiving with an own writer
(`internal/warc`, `extraction.store_warc`), the `serve` JSON API, the
`crawl_paths` report (per-URL discovery path, also in the desktop URL
drawer), per-page HTTP protocol version capture (stored, exported, shown in
the UI), and a catalogue of **164 checks** whose fixture coverage is enforced
by a meta-test (§6).

**2026-06-12 — second catalogue tranche (+27 → 164).** Completed the
issues-library entries for the directives, pagination, hreflang and links
tabs that are computable on existing data, plus internal-search URLs,
canonical fragment/invalid-attribute, CSS/JS resource >2MB, sitemap >50k,
JS-updated description, the Missing Alt Attribute vs Alt Text split, and Alt
Text in h1. Carried by three new parse facts: `Link.NoAltAttr`,
`Facts.H1AltText`, `Facts.CanonicalInvalidAttrs`.

**2026-06-17 — llms.txt as a first-class audit (+8 checks, new `llms_txt`
tab).** A new `internal/llmstxt` parser/validator and an `llms_txt:` config
block (`check`/`fetch_full`/`crawl_linked`, plus `analysis.llms_txt`). The seed
host's `/llms.txt` (and `/llms-full.txt`) is fetched out-of-band at crawl start
like robots.txt (re-fetched on resume, idempotent), structurally validated, and
stored in the `llmstxt` table; its curated links are recorded in `llmstxt_links`
(provenance independent of the link graph) and admitted to the frontier through
the normal discovery chain — internal links crawled, external links gated by the
external-crawl flag — unless `crawl_linked` is off. The analyze phase emits the
file-level checks (`llms_txt_missing`, `llms_txt_invalid_format`,
`llms_txt_missing_summary`, `llms_txt_malformed_link_list`,
`llms_full_txt_missing`) and the curated-link checks resolved against the crawl
graph (`llms_txt_broken_link`, `llms_txt_link_non_indexable`,
`llms_txt_link_unverified` — the last for links the crawl never reached). All
surface automatically through `bluesnake issues`, `crawl_overview`, the serve
`/issues` endpoint and the MCP `issue_summary`/`query` tools. (A bounded
out-of-band probe of unreached curated links, a dedicated report/export, and a
`bluesnake llms test` CLI are deliberate follow-ups, not yet built.)

**Implemented but scoped down (extension points exist):**
- Issues catalogue: **164 = the full issues library computable on the current
  data model** (the no-new-infrastructure boundary, not an arbitrary stop).
  The gap to SF's ~300 is not flat: ~92 are accessibility (axe — own row
  §9.2), the rest spelling (cut), AMP-validator and integration checks; strip
  those and the priority-classified non-a11y ceiling is ~200. The remaining
  ~36 each cross an infra boundary, deferred per check: rendering-mode JS
  filters (new `JSDiff` fields + Chrome), Bad Content Type (body sniffing),
  Broken Bookmark (fragment edges — G28, probe SF first), HTTP Refresh
  redirect type, sitemap >50MB (response sizes uncaptured), Background/
  Incorrectly-Sized Images (render+analysis), High Carbon Rating. Catalogue
  is a data table; the coverage meta-test forces a fixture per entry.
- Structured data validation: curated 12-type Google rich-results requirement
  table (data-driven, `internal/structured.requirements`); full Schema.org
  vocabulary validation not shipped.
- AMP: structural checks (canonical/viewport/charset/amp-script/reciprocity),
  not the full official AMP validator rule set.
- Sitemap orphan detection approximates "only discoverable via sitemap" as
  zero inlinks + sitemap-seeded.
- Concurrency: goroutine-per-URL behind a semaphore + sink-mirrored frontier.
  Correct and crash-safe; a persistent-frontier worker pool would reduce
  memory at millions-of-URLs scale.

**Not implemented (documented cuts):**
- Spelling & grammar (planned cut, §1 non-goals).
- Accessibility (axe-core) — needs an axe bundle injected via CDP.
- Old AJAX crawling scheme (deliberately dropped — deprecated by Google).
- SERP mode's interactive snippet editor, segments, visualisations, built-in
  scheduling (cron + CLI; pixel-width *measurement* is in, per §1).
- Forms-based auth recorder (bring-your-own session cookie supported).

### 9.1 Known config no-ops (2026-06-11 audit)

A full audit of every config field found a set that is parsed, defaulted and
validated but **not yet consumed** — flipping them changes nothing. They are
listed here so the schema doesn't silently lie; the misleading ones that were
surfaced in the desktop Settings UI have been removed from it (the YAML keys
remain valid for forward-compat). Wiring them is tracked future work.

**Extraction is always full (these toggles are inert by design here).** Unlike
Screaming Frog — which uses per-field switches to save memory — bluesnake
extracts the entire per-URL dataset in one cheap parse pass, so these never
gate anything: `extraction.page_details.*` (titles, meta_descriptions,
meta_keywords, h1, h2, indexability, word_count, readability,
text_to_code_ratio, hash, page_size, forms), `extraction.url_details.*`
(response_time, last_modified, http_headers, cookies),
`extraction.directives.{meta_robots,x_robots_tag}`. (Note: `cookies` is also
not *collected* yet — there is no cookies table.)

**Reserved for unbuilt features:** `extraction.pdf.*` (no PDF parsing),
`extraction.structured_data.{google_rich_results_validation,case_sensitive}`
(the curated rich-results check ignores both flags),
`rendering.flatten_shadow_dom` / `rendering.flatten_iframes` (chromedp
`OuterHTML` doesn't flatten), `rendering.window` (the preset name is ignored;
`window_width`/`window_height` are honoured), `advanced.html_validation` (the
Validation-tab checks always run regardless), `http.trusted_cert_dirs` (no
custom CA pool is built — only the documented insecure-TLS test hook exists).

**Resource/link `store` flags are unenforced.** `resources.{images,media,css,
javascript,swf}.store` and `links.{internal,external,canonicals,pagination,
hreflang,amp,meta_refresh,iframes,mobile_alternate}.store` are read by
`crawler.typeFlags` but the caller uses only the `crawl` half; every parsed
edge is stored regardless. (The `crawl` flags *are* enforced — discovery is
correctly gated.)

**Behavioural flags not yet wired:** `advanced.respect_noindex`,
`advanced.respect_canonical`, `advanced.respect_next_prev`,
`advanced.ignore_paginated_for_duplicates`, and `analysis.canonicals`
(canonical-chain analysis currently piggybacks on `analysis.redirect_chains`).

**Storage knobs not yet wired:** `storage.dir` (the store path comes from
`--store-dir` or the app default, not this field) and `storage.retention_days`
(no pruning exists). When retention lands it will be an explicit
`bluesnake crawls prune` command (+ a desktop action), never an automatic
delete-on-startup.

**Compare deltas — two dead change-detection values:** `compare.change_detection`
is honoured, but its `content` and `structured_data` entries have no detector,
and `compare.content_change_threshold` is unread (it pairs with the unbuilt
`content` similarity delta).

### 9.2 Backlog prioritization matrix (2026-06-12)

Pending items ranked after the 5-domain Screaming Frog comparison
(`~/crawl_comparison_experiment/runs/2026-06-12-yc5/{GAPLOG,FINDINGS}.md` —
G-numbers below refer to its gap log). Columns: **parity gain** = how much
closer to SF's actual output/behavior, **real-world use** = whether a working
SEO would feel it, **risk** = regression blast radius + implementation
uncertainty.

<!-- MAINTENANCE: when an item ships, REMOVE its row from this table and
     record the change in §9 (and §9.1 if it wires up a config no-op). -->

| Item | Parity gain | Real-world use | Risk | Notes |
|---|---|---|---|---|
| Tokenization parity (G7/G21) — word count, sentences, Flesch | High | High | Med | Largest remaining numeric divergence (hundreds of word-count diffs per site); readability buckets flip issue lists. Pin SF's rules with probe pages first (td/tr sentence semantics, inline joins without spaces) |
| Issues catalogue 164 → ~300 (residual tail) | Med | Med | Low-Med | 27 checks shipped 2026-06-12 (directives/pagination/hreflang/links complete for native data, §9). What remains needs new infrastructure per check — rendering-mode JS filters, Bad Content Type sniffing, Broken Bookmark (G28-entangled), HTTP Refresh header, sitemap >50MB — or is a11y/spelling/AMP-validator work tracked in its own rows |
| `respect_noindex/canonical/next_prev` wiring | Med | High | Med | Real SF workflow knobs ("crawl as Google indexes"). Today they parse and silently do nothing. Defaults off, so no default-behavior risk |
| SF-style elem paths `[n]`/`[@class]` (G10) | High | Med | Med | Kills ~12k cosmetic path diffs/site and unlocks SF's class-driven Navigation position matches. Exact qualifier rules need probes (SF emits [n] only for same-tag siblings, sometimes [@class] instead) |
| Accessibility (axe via CDP) | Med | High | Med | Most-requested real-world audit type; SF ships it. Needs Chrome + axe bundle injection. More "useful" than "parity" |
| Shadow-DOM/iframe flattening (`rendering.flatten_*`) | Med | Med-High | Med | Web-component sites currently lose rendered text/links that SF sees |
| Rich-result matrix breadth (G5 residual) | Med | Med-High | Low | Extend `structured.requirements` (SoftwareApplication, Review, HowTo, AggregateRating) with probes against SF |
| Cookie collection (`url_details.cookies`) | Med | Med | Low-Med | Whole SF report we lack; GDPR/consent audits use it. Needs a cookies table + rendered-mode capture |
| Persistent-frontier worker pool (scale) | Indirect | High | Med-High | Gates large-site (e-commerce) crawls; rendered stores already hit ~1GB on mid-size sites |
| Fragment self-edges (G28) | Med | Low | Med | SF keeps `<a href="#x">` as empty-anchor self-edges (feeds its no-anchor-text counts). Changes outlink counts everywhere — probe SF's dedup semantics first |
| Schema.org vocabulary validation | Low-Med | Low | Med | SF's plain validation found zero issues across all 5 test domains — rich-results is what fires. Big build, small payoff |
| Compare detectors (`content`/`structured_data`) | Low-Med | Med | Low | Real workflow, but only for repeat-crawl users |
| Store-flag enforcement (§9.1) | Low | Low-Med | Low | DB size on big crawls; SF-visible counts already match |
| PDF extraction (`extraction.pdf.*`) | Low-Med | Low | Med | Niche (gov/edu/docs sites). New parser dependency |
| AMP full validator | Low | Near-zero | Med | AMP is effectively dead. Skip unless a user asks |
| Spelling & grammar | Med | Low | High | Visible SF tab but noisy and rarely enabled; stays cut (§1 non-goals) |
| Orphan-detection exactness, retention/prune, cert dirs, window presets | Low | Low | Low | Housekeeping tier |
