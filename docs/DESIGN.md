# acrawler — Design Document

A modern, headless, CLI-first website crawler and SEO auditor in Go. Functional parity target: Screaming Frog SEO Spider's crawling/auditing core — **without** the UI and **without** third-party API integrations (GA4, GSC, PageSpeed/Lighthouse, link indexes, AI providers), and **without** opaque binary config files: everything is plain-text config + flags.

Status: living document — **all milestones M0–M14 implemented** (2026-06-10). Implementation deltas from this design are tracked in §9. The feature inventories this design is derived from live in `docs/research/`:
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
8. **Plain-text everything**: one YAML config schema covering every knob; CLI flags override; `acrawler config init` emits a fully-commented default.
9. **Very good test coverage, BDD-first**: Gherkin acceptance specs + exhaustive table-driven unit tests written before each module's implementation.

### Non-goals
- GUI of any kind.
- Third-party API integrations (GA4, Search Console, PSI/Lighthouse, Majestic/Ahrefs/Moz, AI embeddings/LLM features, Google Sheets/Drive/Looker).
- SERP mode pixel-perfect Google snippet simulation (we keep pixel-width calculation since title/description pixel filters depend on it, using a bundled font metrics table — but no interactive snippet editor).
- Built-in scheduler (cron exists; our CLI is fully scriptable; we document recipes).
- Spelling & grammar checking (v1: out; revisit — requires large dictionaries/language rules; the hook point is left in the data model: `spelling_errors`, `grammar_errors` columns nullable).

### Deliberate improvements over Screaming Frog
- Plain-text config (YAML) with JSON-schema validation, instead of `.seospiderconfig` binaries.
- First-class JSON/JSONL output for piping (SF is spreadsheet-centric).
- Single static binary; storage is embedded (SQLite), no JVM/memory allocation tuning.
- Discoverable exports: `acrawler export --list` enumerates every exportable dataset.
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

Module path: `github.com/hhsecond/acrawler`.

---

## 3. CLI surface

```
acrawler crawl <url>                  # spider mode crawl
acrawler list <file|->                # list mode (file, stdin, or --sitemap <url>)
acrawler resume <crawl-id>            # resume a paused/interrupted crawl
acrawler crawls [ls|rm|export|info]   # manage stored crawls (IDs, projects)
acrawler analyze <crawl-id>           # (re-)run post-crawl analysis
acrawler export <crawl-id> ...        # tab/filter/bulk exports; --list to discover
acrawler report <crawl-id> ...        # named reports; --list to discover
acrawler issues <crawl-id>            # issues summary (and per-issue export)
acrawler sitemap <crawl-id>           # generate XML sitemap(s) from a crawl
acrawler compare <id-prev> <id-curr>  # crawl comparison (+ change detection)
acrawler robots test <url...>         # robots.txt tester (live or --robots-file)
acrawler config init|validate|show    # emit commented default config / validate / effective config
acrawler serve <crawl-id>             # (later) localhost JSON API over a crawl DB
```

Global flags: `--config <file>`, `--store-dir <dir>` (default `~/.acrawler`), `--project <name>`, `--task <name>`, `--output <dir>`, `--format csv|json|jsonl|xlsx`, `--timestamped-output`, `--overwrite`, `--quiet/--verbose`, `--log json|text`.

Every config key is overridable as a flag using dotted names: `--set spider.limits.max_depth=3 --set speed.max_threads=10` plus dedicated shorthand flags for the common ones (`--depth`, `--threads`, `--rate`, `--include`, `--exclude`, `--user-agent`, ...).

Crawl UX (headless but informative): single-line progress (crawled/queued/errors/URLs-sec), `--progress none|line|live`; non-zero exit codes contract: `0` ok, `1` crawl error, `2` config error, `3` interrupted (resumable).

`Ctrl-C` = graceful pause (frontier + state committed; prints `acrawler resume <id>` hint). Second `Ctrl-C` = hard stop (still safe by WAL).

---

## 4. Configuration schema (YAML)

One file = one crawl profile. Everything has a default; an empty file is a valid config. Full schema (abridged here; canonical commented version is emitted by `acrawler config init` and kept in `docs/examples/acrawler.yaml`):

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

resources:              # store/crawl pairs (SF Spider > Crawl)
  images:        {store: true,  crawl: true}
  media:         {store: true,  crawl: true}
  css:           {store: true,  crawl: true}
  javascript:    {store: true,  crawl: true}
  swf:           {store: true,  crawl: true}
links:
  internal:      {store: true,  crawl: true}
  external:      {store: true,  crawl: true}
  canonicals:    {store: true,  crawl: true}
  pagination:    {store: false, crawl: false}
  hreflang:      {store: true,  crawl: false}
  amp:           {store: false, crawl: false}
  meta_refresh:  {store: true,  crawl: true}
  iframes:       {store: true,  crawl: true}
  mobile_alternate: {store: false, crawl: false}
  uncrawlable:   {store: false}

sitemaps:
  crawl_linked: false
  auto_discover_via_robots: false
  urls: []

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
  user_agent: "acrawler/1.0 (+https://github.com/hhsecond/acrawler)"
  robots_user_agent: "acrawler"
  headers: {}            # name: value
  proxy: ""              # http://user:pass@host:port
  trusted_cert_dirs: []
  auth:
    basic: []            # [{url_prefix: "", username: "", password: "", password_env: ""}]
    cookies: []          # [{name: , value: , domain: }]  (forms-auth replacement: bring your own session cookie)

custom_search: []        # [{name:, mode: contains|not_contains, pattern:, regex: false, scope: html|text|element:<sel>}]
custom_extraction: []    # [{name:, type: xpath|css|regex, expression:, attribute:, return: text|html|inner_html|function}]
custom_js: []            # [{name:, type: extraction|action, file: snippet.js, timeout_sec: 5, content_types: [text/html]}]

link_positions:          # ordered; first match wins; substring match on link XPath/element chain
  - {name: head,    match: "/head"}
  - {name: nav,     match: "/nav"}
  - {name: header,  match: "/header"}
  - {name: sidebar, match: "/aside"}
  - {name: footer,  match: "/footer"}
  - {name: content, match: "/"}
store_link_paths: true

list_mode:               # only used by `acrawler list`
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

storage:
  dir: ~/.acrawler       # crawl DBs live here, one SQLite file per crawl
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
cmd/acrawler/            main; cobra commands only (thin: parse flags → call internal)
internal/config/         schema structs, defaults, YAML load/merge/validate, flag --set overlay
internal/urlutil/        normalization, resolution, rewriting, include/exclude, classification
                         (internal/external, folder depth, path type), fragment & encoding rules
internal/robots/         REP parser/matcher (Google semantics), per-host cache, custom overrides,
                         matched-line reporting, sitemap discovery
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
- **Pause/resume**: frontier and visited-set live in SQLite alongside results; resume = reload frontier + config snapshot (config is frozen into the crawl DB at start; resume refuses a changed config unless `--force`).

### 5.3 Storage schema (SQLite, one DB file per crawl)

`~/.acrawler/crawls/<crawl-id>.db`, plus a tiny registry DB `~/.acrawler/registry.db` (crawl id, project, task name, seed, mode, started/finished, status, counts). Crawl ID = `<yyyymmdd-hhmmss>-<short-rand>`.

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

### 5.6 Post-crawl analysis (`acrawler analyze`, auto by default)

Each analyzer reads SQLite, writes back columns/tables; all are idempotent and re-runnable (e.g. after changing near-dup threshold — mirrors SF):
- **link_score**: PageRank (d=0.85, 40 iters or ε<1e-6) over followed internal hyperlinks; scaled 0–100.
- **chains**: follow redirect/canonical edges → `redirect_chains` result table (source, hops, loop flag, final status, chain type incl. mixed).
- **near_duplicates**: 5-word shingles of content-area text → minhash(128) → LSH candidate pairs → exact Jaccard verify ≥ threshold.
- **hreflang**: reciprocity matrix, return-link checks, code validation (ISO 639-1 / 3166-1), x-default/self-reference, canonical consistency.
- **pagination**: sequence reciprocity, loops, unlinked pagination URLs.
- **canonicals/links**: unlinked-canonical detection, inlink-only-nofollow / non-indexable-inlinks-only flags, aggregates (unique in/outlinks, % of total).
- **sitemaps**: set ops between sitemap entries and crawled URLs → in/not-in/orphans/non-indexable-in-sitemap/multiple.

### 5.7 Compare

`acrawler compare <prev> <curr>`: attaches both DBs, applies URL mapping regexes to previous, computes per-filter membership deltas (Added/New/Removed/Missing per SF semantics) and element change detection (title/desc/h1/word-count/depth/link-metrics/content-similarity), writes a comparison report (terminal summary + exportable CSV/JSON).

### 5.8 Rendering (phase 2)

`chromedp` pool (size = min(threads, cores-scaled cap: 2/4/8 tabs); per-page: navigate, wait until the page **settles**, snapshot rendered DOM, optional screenshot, console log capture, custom JS execution (action snippets then extraction snippets). Parse pipeline runs twice (raw + rendered) and diffs element sets → JavaScript tab data (`origin` on link edges, `*_rendered` facts). Resource blocking by robots reported as Blocked Resource.

**Settle detection** (`internal/render`): navigation does **not** wait for the browser `load` event (background media can hold it open for many seconds after the DOM is done); the anchor is `DOMContentLoaded`. After DCL, a page is settled when any of:
1. the countable network is fully idle for 500ms — media, websockets, EventSource, ping/beacon, prefetch and `blob:`/`data:` requests are excluded from the in-flight set (they routinely stay open forever);
2. the DOM node count holds steady across two 500ms probes with no script/stylesheet/XHR/fetch in flight (absorbs third-party widgets and analytics that chatter indefinitely);
3. the wire is completely silent for 1.5s (only permanently-open requests remain).

`rendering.ajax_timeout_sec` is the **hard cap** on the settle phase after DCL (not a fixed sleep); `advanced.response_timeout_sec` caps the wait for DCL itself. Worst case therefore equals the old fixed-wait behaviour. Regression tests cover early settle, permanently-open streams/iframes, and beacon chatter.

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
- Every issue in the catalogue must have at least one fixture page that triggers it and one that doesn't (enforced by a meta-test iterating the catalogue against the kitchen-sink crawl).
- Golden files under `test/golden/`; regenerate with `-update` flag.
- Coverage gate: `make test` fails under 85% for `internal/...` (excluding `render`, which needs Chrome and is build-tagged `chrome`).

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
- `serve` subcommand (JSON API over crawl DB) — would make a future UI trivial; deferred.
- Spelling/grammar: candidate libs need evaluation; schema already reserves columns.
- Distributed crawling: out of scope; single-process concurrency is the design point.
- Windows support: nothing platform-specific except Chrome discovery; CI matrix later.
- **Renderer wait strategy knob** (`rendering.wait_strategy: adaptive | fixed`): adaptive settle
  detection (§5.8) trades snapshot determinism for speed — two crawls of the same page may
  snapshot at slightly different moments, which can surface as phantom diffs in `compare` on
  pages with flaky widgets. A `fixed` mode (old behaviour: load event + full AJAX sleep) should
  be offered for compare-stable auditing. Not yet implemented; today only adaptive exists.
- **Settle thresholds are code constants, not config**: 500ms network-idle window, 1.5s
  wire-silence window, 2×500ms DOM-stability probes (`internal/render`). Decide whether to
  expose them under `rendering.` or document them as fixed. If pages ever settle wrongly, the
  precision upgrade is a `MutationObserver` injected at document start ("ms since last DOM
  mutation") instead of polling node counts.

## 9. Implementation status & deltas (2026-06-10)

All milestones M0–M14 are implemented. Where the implementation deviates from
or subsets this design:

**Implemented but scoped down (extension points exist):**
- Issues catalogue: ~100 checks implemented (per-page + cross-page + analysis
  + structured data + JS + validation + AMP) of SF's ~300; the catalogue in
  `internal/issues` is a data table — adding checks is incremental.
- Structured data validation: curated 12-type Google rich-results requirement
  table (data-driven, `internal/structured.requirements`); full Schema.org
  vocabulary validation not shipped.
- AMP: structural checks (canonical/viewport/charset/amp-script/reciprocity),
  not the full official AMP validator rule set.
- Title/description thresholds: character-based only. SERP **pixel-width**
  checks need a bundled font-metrics table (`internal/serpwidth` not built).
- Hreflang code validation is structural (ISO-shaped), not full ISO registry.
- Near-duplicates: minhash signatures compared all-pairs (fine to ~10k pages);
  LSH banding is the next step for large crawls.
- Sitemap orphan detection approximates "only discoverable via sitemap" as
  zero inlinks + sitemap-seeded.
- Concurrency: goroutine-per-URL behind a semaphore + sink-mirrored frontier.
  Correct and crash-safe; a persistent-frontier worker pool would reduce
  memory at millions-of-URLs scale.

**Not implemented (documented cuts):**
- Spelling & grammar (planned cut, §1 non-goals).
- Accessibility (axe-core) — needs an axe bundle injected via CDP.
- WARC / full website archiving (raw HTML blob storage exists:
  `extraction.store_html` → per-crawl assets directory).
- Custom JavaScript snippets (config schema exists; execution via CDP not wired).
- Old AJAX crawling scheme (deliberately dropped — deprecated by Google).
- SERP mode, segments, visualisations, built-in scheduling (cron + CLI).
- Forms-based auth recorder (bring-your-own session cookie supported).
- HTTP/2 fetch metrics, per-URL crawl-path report.
