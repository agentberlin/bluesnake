# bluesnake — Design Document

A modern, headless, CLI-first website crawler and SEO auditor in Go. Functional parity target: Screaming Frog SEO Spider's crawling/auditing core — **without** the UI and **without** third-party API integrations (GA4, GSC, PageSpeed/Lighthouse, link indexes, AI providers), and **without** opaque binary config files: everything is plain-text config + flags.

Status: living document — **all milestones M0–M14 implemented** (2026-06-10); **§8/§9 backlog cleared** (2026-06-11): SERP pixel widths, ISO-registry hreflang validation, LSH near-dup banding, `rendering.wait_strategy`, custom JS snippets via CDP, WARC archiving, the `serve` JSON API, crawl-path report, HTTP version capture, a 164-check catalogue (second tranche 2026-06-12, §9), and the catalogue-coverage meta-test. Remaining deliberate cuts are listed in §9. The feature inventories this design is derived from live in `docs/research/`:
- [01-crawl-configuration.md](research/01-crawl-configuration.md) — every SF config option
- [02-data-model-and-checks.md](research/02-data-model-and-checks.md) — per-URL data, tabs/filters, 300+ issues, crawl analysis, link model, reports
- [03-operations-cli-storage.md](research/03-operations-cli-storage.md) — storage modes, resume, modes, CLI, comparison

---

## 0. Engineering quality bar (the change standard)

bluesnake is an open-source tool used by a real community; we are responsible to
them for its correctness. Identifying a gap is cheap; **changing the engine is
not, and every production change clears a high bar.** Making one diff disappear is
never the goal — leaving the product *holistically* correct is. There are **no
ad-hoc, patch-it-and-move-on fixes.** This bar governs every engine change
(features, parity fixes, bug fixes) and is the operational contract behind Goal #9
(§1) and the testing strategy (§6).

1. **Architect first, code second.** Understand how the change fits the product as
   a whole and design the *right* shape before writing anything — not the smallest
   local patch that silences a symptom. If correctness means redesigning an
   internal, moving responsibilities between packages, or reshaping a data flow,
   do that. We carry **no legacy code or backward-compatibility debt for its own
   sake**: the right design wins over the smaller diff. A narrow hack that leaves
   the architecture worse is unacceptable even when it makes a number match.

2. **Ground the change in correct behaviour — not in matching a reference tool's
   output.** Our parity target is Screaming Frog (§1), but we cannot see its
   source, so every "what does SF do here" is a best-judgement inference. Anchor
   it in what crawling/auditing is actually *supposed* to do — HTTP / HTML / SEO
   semantics, the relevant spec, REP / Google docs — and research to confirm the
   correct behaviour when needed. **Matching SF's number is necessary but not
   sufficient:** a change that lines the number up without being grounded in
   correct behaviour is a liability that resurfaces as a new defect on the next
   site. Where SF is demonstrably legacy/ambiguous or simply wrong, we
   deliberately diverge and record why.

3. **Read the history first — and suspect past fixes.** Before touching anything,
   read the implementation-status deltas (§9 / §9.1) and the parity comparison
   decision log kept with the SF-comparison harness (every divergence ever ruled
   on, *including the ones we chose not to fix*). A new symptom is often a side
   effect of a previous best-guess fix; knowing the history is how we catch "the
   number matches now, but we quietly broke something we fixed before."

4. **Test-first, TDD and BDD — the suite is the regression net.** For each change,
   decide whether the behaviour is already covered by a unit/behavioural test,
   folds into one, or needs a new one. **Write or extend that test first and watch
   it fail**, then implement until it passes. These pinned tests exist precisely
   *because* each parity inference is a best guess — they must fail loudly when a
   future change accidentally undoes a past one. **No engine change lands without a
   test that pins the intended behaviour.**

5. **Every surface, not just the one you measured.** A change to crawler / parser
   / analysis logic must be correct and consistent across **all** surfaces — the
   CLI, the MCP server (`internal/mcp`), and the desktop UI (`desktop/`). A
   behaviour fixed in one path but wrong in another is not fixed.

6. **When in doubt, ask.** If the right design, the test boundary, the intended
   semantics, or even whether a gap is worth fixing is unclear, stop and ask
   rather than guess. A wrong change shipped to the community is far worse than a
   question asked.

Parity gaps themselves are discovered through the SF-comparison harness, whose
loop is *compare → rank → triage → **log every decision** (including won't-fix) →
record the domain*, so the same divergence is never investigated twice; §9.2
mirrors the current backlog distilled from that log.

---

## 1. Goals and non-goals

### Goals
1. **Crawl** any site (spider mode) or URL list (list mode) with full control: scope, limits, speed, include/exclude, URL rewriting, robots.txt handling, auth, custom headers/UA, proxy.
2. **Extract** the full Screaming Frog per-URL dataset: response data, indexability, on-page elements, directives, canonicals, pagination, hreflang, structured data, content metrics, security signals, link graph with rich edge data.
3. **Audit**: evaluate the full issues catalogue (issue/warning/opportunity × priority) that doesn't require external APIs.
4. **Persist**: disk-backed storage with continuous commit (crash-safe), pause/resume, resumable partial crawls, crawl IDs.
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
bluesnake crawls [ls|rm|export|info]   # manage stored crawls (by crawl ID)
bluesnake analyze <crawl-id>           # (re-)run post-crawl analysis
bluesnake export <crawl-id> ...        # tab/filter/bulk exports; --list to discover
bluesnake report <crawl-id> ...        # named reports; --list to discover
bluesnake issues <crawl-id>            # issues summary (and per-issue export)
bluesnake sitemap <crawl-id>           # generate XML sitemap(s) from a crawl
bluesnake compare <id-prev> <id-curr>  # crawl comparison (+ change detection)
bluesnake projects [ls|create|add|show|compare|diff]  # competitor-study layer (opt-in, own DB; §5.9)
bluesnake robots test <url...>         # robots.txt tester (live or --robots-file)
bluesnake config init|validate|show    # emit commented default config / validate / effective config
bluesnake serve                        # read-only localhost JSON API over the crawl store (--addr)
bluesnake mcp                          # MCP server for LLM agents over streamable HTTP (--addr, default 127.0.0.1:8473)
```

Global flags: `--config <file>`, `--store-dir <dir>` (default `~/.bluesnake`), `--output <dir>`, `--format csv|json|jsonl|xlsx`, `--timestamped-output`, `--overwrite`, `--quiet/--verbose`, `--log json|text`.

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
internal/structured/     JSON-LD/Microdata/RDFa parsing + Google rich-results validation;
                         embedded schema.org IS-A graph resolves subtypes to the most-specific
                         curated root (a Restaurant validates as a LocalBusiness)
internal/render/         chromedp session pool, rendered DOM, screenshots, console log, custom JS
internal/frontier/       dedup set + priority FIFO by depth, per-host queues, politeness,
                         limits enforcement, SQLite mirroring for resume
internal/crawler/        orchestrator: worker pool, pipeline (fetch→parse→evaluate→store→discover),
                         pause/resume, signal handling, progress events
internal/store/          SQLite schema + repositories (pages, links, frontier, sitemaps, issues,
                         analysis results, crawl meta), migrations, crawl manager (crawl IDs)
internal/indexability/   the indexability state machine (status + reason)
internal/issues/         rule engine: per-URL rules + aggregate rules; catalogue with id/severity/priority
internal/analyze/        post-crawl: link score, chains (redirect/canonical), near-dup minhash,
                         hreflang reciprocity, pagination sequence, sitemap set-ops, orphans,
                         inlink-derived flags
internal/export/         tab/filter datasets, bulk exports, writers (csv/json/jsonl/xlsx)
internal/report/         named reports (crawl overview, chains, insecure content, ...)
internal/sitemapgen/     XML sitemap + image sitemap generation w/ splitting + index
internal/compare/        crawl comparison, change detection, URL mapping
internal/project/        OPT-IN competitor-study overlay (Projects = main domain + competitors);
                         OWN projects.db, reads the registry/per-crawl DBs read-only, reuses
                         compare; zero changes to the crawl core — fully removable (§5.9)
internal/serpwidth/      text pixel-width measurement (bundled font metrics table)
internal/isocodes/       embedded ISO 639-1 + ISO 3166-1 registries (hreflang validation)
internal/warc/           minimal WARC/1.1 writer (extraction.store_warc archives)
internal/serve/          read-only localhost JSON API over stored crawls
internal/mcp/            MCP server (hand-rolled JSON-RPC 2.0 over the streamable-HTTP transport):
                         12 core tools (+5 from the removable project layer, §5.9) — crawl control (start/status/pause/resume/stop), config
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

`~/.bluesnake/crawls/<crawl-id>.db`, plus a tiny registry DB `~/.bluesnake/registry.db` (crawl id, seed, mode, started/finished, status, and two URL counts: `crawled` = fetched and `total` = encountered — Screaming Frog's "URLs Crawled" vs "URLs Encountered" split, where encountered also covers robots-blocked/errored URLs). `total` is the headline count shown across the CLI, desktop and MCP (`crawled` is reported alongside as the fetched subset); crawls finished before `total` existed are backfilled lazily from a `COUNT(*)` over `pages`. Crawl ID = `<yyyymmdd-hhmmss>-<short-rand>`.

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
  PRIMARY KEY (page_id, issue_id, detail)   -- detail is part of the key: one page
);                                          -- can trigger a check several times with
                                            -- different specifics (e.g. a Recipe missing
                                            -- two required properties); each distinct
                                            -- occurrence is its own row. Affected-URL
                                            -- counts use COUNT(DISTINCT page_id).
CREATE TABLE blobs (page_id INTEGER, kind TEXT, path TEXT);  -- stored html/rendered/pdf/screenshot file refs (filesystem-backed under <crawl-id>.assets/)
CREATE TABLE analysis_meta (key TEXT PRIMARY KEY, value TEXT);  -- which analyses ran, params (e.g. near-dup threshold)
```

Write strategy: workers push results to a single writer goroutine; batched transactions (N=200 pages or 500 ms, whichever first) → continuous commit with bounded fsync cost. WAL mode + `synchronous=NORMAL`.

Issue definitions (name, severity, priority, description, trigger doc) live in code (`internal/issues/catalogue.go`) as the single source of truth; `issues` table stores only occurrences.

#### Schema versioning & migrations (`internal/store`)

Crawl DBs and the registry DB are durable artifacts that outlive the binary, so the schema is **versioned, not patched ad hoc**. Each database carries its revision in SQLite's built-in `user_version` header slot (zero-cost to read, durable in the file header). On open, `store` runs the `CREATE TABLE IF NOT EXISTS` of the **latest** shape and then calls a single generic upgrader (`upgrade`):

- A **fresh** database (no tables yet → this open created it) is stamped straight to the top of its ladder; the migration steps never run.
- An **existing** database runs only the ladder steps whose version is above its stored revision, each applied in a transaction that bumps `user_version` atomically (a crash mid-step rolls back to the prior revision). The common case — already current — is one pragma read.

Migrations are an **append-only ladder** (`crawlMigrations`, `registryMigrations`): each step has a *stable* version number (never renumbered or reordered) and an idempotent `apply` func. Adding a schema change = append one step. The two `min*Version` floors are the removal lever (below).

> **Retiring a migration.** Stable version numbers + a floor are what make old step code *safely deletable* — without a durable revision marker you can never prove a DB on disk doesn't still need an old step. To drop support for ancient databases and delete their migration code:
> 1. Pick the new floor **F** — the oldest revision you still want to open.
> 2. Set `minCrawlVersion`/`minRegistryVersion = F`.
> 3. Delete every ladder step with `version <= F` (and any helper only it used). Leave the surviving steps' version numbers **unchanged** — never renumber.
> 4. Done: a database below F now fails to open with a clear *"schema vN predates the minimum supported vF — re-crawl or remove it"* error instead of running a half-complete ladder, and the retired step code is gone. Fresh databases (stamped at the current top) and any DB at ≥ F are unaffected.
>
> This is the project's deliberate stance on old data: we carry no backward-compat debt for its own sake (§0), so support for a schema era is dropped *explicitly* by raising the floor — never by silently keeping dead migration code forever.

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

`bluesnake compare <prev> <curr>`: attaches both DBs, applies URL mapping regexes to previous, computes per-filter membership deltas (Added/New/Removed/Missing per SF semantics) and element change detection (title/desc/h1/word-count/depth/link-metrics/content-similarity/structured-data-types), writes a comparison report (terminal summary + exportable CSV/JSON).

### 5.8 Rendering (phase 2)

`chromedp` pool (size = min(threads, cores-scaled cap: 2/4/8 tabs); per-page: navigate, wait until the page **settles**, snapshot rendered DOM, optional screenshot, console log capture, custom JS execution (action snippets then extraction snippets). Parse pipeline runs twice (raw + rendered) and diffs element sets → JavaScript tab data (`origin` on link edges, `*_rendered` facts). Resource blocking by robots reported as Blocked Resource.

**Shadow-DOM flattening** (`rendering.flatten_shadow_dom`, default on — R9b): `OuterHTML` does not serialize shadow roots, so links/headings/structured-data inside Web-Components shadow trees would be invisible. When on, the rendered snapshot is produced by a synchronous pass (right before serialization, after any screenshot) that moves each shadow host's children up into the host as light DOM and returns native `outerHTML`; nested roots are flattened a level per pass. Open roots are reached via `element.shadowRoot`; **closed** roots are reached via a document-start `attachShadow` shim that stashes them (mode left unchanged). The flattened HTML feeds the same `parse` + `structured` pipeline, so shadow links surface as `origin=rendered` — matching Screaming Frog, which pierces both open and closed shadow DOM. Residual: closed *declarative* shadow DOM (parser-created, no `attachShadow` call) is unreachable. (`rendering.flatten_iframes` remains unbuilt — see §10.)

**Settle detection** (`internal/render`): navigation does **not** wait for the browser `load` event (background media can hold it open for many seconds after the DOM is done); the anchor is `DOMContentLoaded`. After DCL, a page is settled when any of:
1. the countable network is fully idle for 500ms — media, websockets, EventSource, ping/beacon, prefetch and `blob:`/`data:` requests are excluded from the in-flight set (they routinely stay open forever);
2. the DOM node count holds steady across two 500ms probes with no script/stylesheet/XHR/fetch in flight (absorbs third-party widgets and analytics that chatter indefinitely);
3. the wire is completely silent for 1.5s (only permanently-open requests remain).

**None of those three fire while the page still has DOM work scheduled on an in-window timer** (R9a): a shim injected at document-start (`addScriptToEvaluateOnNewDocument`) wraps `setTimeout`/`clearTimeout` and exposes a live count of pending one-shot timers in `window.__bsPendingTimers`, which the settle loop reads each tick. Network-idle is otherwise the wrong sole signal for SPAs that inject content via `setTimeout` with no accompanying request — the wire goes quiet ~500ms after DCL, long before a `setTimeout(…, 1500)` fires (Screaming Frog catches these because it dwells its full AJAX timeout). The count is deliberately narrow so the latency lands only where waiting is correct: `setInterval` is never counted, a one-shot whose delay exceeds the cap is never counted (can't fire in-window), and a timer re-armed from inside another timer's callback is never counted (a self-rescheduling animation/poll loop would otherwise dwell to the cap — only its first top-level schedule counts). Residual deferred work the shim does not wait for (bounded, never a hang): string-code timers (`setTimeout("…", d)`, CSP-gated) and DOM injected via `requestAnimationFrame`/microtask/promise.

`rendering.ajax_timeout_sec` is the **hard cap** on the settle phase after DCL (not a fixed sleep) and bounds the timer wait too; `advanced.response_timeout_sec` caps the wait for DCL itself. Worst case therefore equals the old fixed-wait behaviour. Regression tests cover early settle, permanently-open streams/iframes, beacon chatter, in-window timer injection, and re-arming timer loops.

**Wait strategy knob** (`rendering.wait_strategy: adaptive | fixed`): adaptive is the settle detection above; `fixed` waits for the browser load event and then sleeps the *full* AJAX timeout before snapshotting — slower, but the snapshot moment is deterministic, which keeps `compare` runs stable on pages with flaky widgets.

**Custom JavaScript snippets** (`custom_js`): snippet files load at renderer construction (a missing file is a config error naming the snippet). After the page settles, `action` snippets run first (results discarded — they exist to mutate the page), then `extraction` snippets; values are stored in `custom_results` with kind `js` (JS strings verbatim, anything else compact JSON, `error: …` when a snippet throws). Each snippet is bounded by its `timeout_sec` (default 5); a `content_types` list restricts which pages a snippet's results are stored for.

### 5.9 Project layer (competitor study) — an opt-in, removable overlay

`internal/project` adds **Projects**: a *main domain plus its competitors*, for side-by-side benchmarking and per-competitor change-over-time. It is deliberately built as a **fully separable overlay** — like the desktop app (§1), a non-core extension that must never compromise the crawl engine. The contract (stated in the package `doc.go`, proven by the removal procedure below): the feature changes **no** crawl/store/compare/crawler logic, schema, or models. It owns its own database, reads everything else read-only through `store`'s public API, and reuses `internal/compare` unchanged. **Remove it** by deleting the package, its CLI/MCP/desktop registration lines (one `AddCommand`, the appended MCP `Tool` literals, the `ProjectApp` `Bind` entry, the frontend nav/router/`projectApi` additions), and `projects.db` — the rest of the product is byte-for-byte unchanged. No migrations to unwind.

Design decisions:
- **Own database.** A separate `~/.bluesnake/projects.db` (`projects`, `project_domains`), sibling to `registry.db`. The crawl registry and per-crawl DBs are never altered (so it adds no step to the §5.3 migration ladders).
- **Domain-keyed, derived membership.** You add a *domain* (not a crawl) to a project. A site's crawl history is resolved **live** from the registry by matching the seed, so a standalone crawl of a member domain auto-joins and a deleted crawl simply drops out — no crawl→project link is ever persisted, hence no dangling references.
- **Exact site identity, no folding.** A site key is the literal lowercased `host[:port]` of the seed. `example.com`, `www.example.com`, `a.example.com` and `example.com:8080` are **distinct** sites by design (it reuses none of the engine's `www`-stripping host derivers — it has its own `SiteKey`).
- **Associated vs comparable.** Every same-host crawl is *associated* and shown under the site; only a **finished, full-site spider crawl of the root that is not scope-narrowed** (`scope.include` empty) is *comparable* and feeds the numbers. Path crawls, list audits, running, and narrowed crawls are surfaced greyed-out with a reason — visible, but excluded from the math.
- **Dual-mode comparison.** Per-competitor *over time* reuses the pairwise `compare` engine verbatim (same domain ⇒ meaningful URL/issue deltas). *Cross-competitor* is a new read-only **metric scorecard** (`scorecard.go`): site size, indexable rate, status-code mix, issue counts by severity, link score, near-dups (+ optional avg word count / Flesch / schema.org coverage via SQLite JSON functions). Cross-domain URL comparison is **not** offered — disjoint URL sets make it degenerate. All metrics are single-pass SQL aggregates over each crawl DB; `LoadPages` is never used (it would reintroduce the per-crawl memory blow-up).
- **No project-level config; fairness is surfaced, not enforced.** Each site is crawled with its normal default config (per-site changes use the ordinary crawl flow, not the project). When competitors' latest crawls used materially different settings (rendering, depth, robots), the scorecard shows per-site config badges and a divergence banner rather than silently emitting an unfair number; the remedy is re-crawling.
- **Out of scope:** a scheduler. On-demand crawling of a project's sites is the building block a future scheduler would drive (§8).

Surfaces (engine-first, all three per §0): the CLI `bluesnake projects` subtree; five MCP tools (`list_projects`, `create_project`, `add_competitor`, `remove_competitor`, `project_comparison`); and a desktop **Projects** view (Overview + Comparison) bound through a *separate* `ProjectApp` Wails struct so the core `App` binding (and its generated `App.js`) stay untouched.

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
- Coverage gate: `make cover` fails when **aggregate** statement coverage across `internal/...` drops below **90%** (the `render` package's Chrome-dependent paths are build-tagged `chrome` and excluded from the default measured set). 90% is the project standard — new code lands with tests that keep the gate green, and meaningful behavioral/integration tests are strongly preferred over line-touching filler (the few statements left uncovered are deliberately the hard-to-reach I/O fault-injection branches, not feature logic). New modules should aim to clear 90% on their own so the aggregate has headroom.
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

Definition of done per milestone: feature file(s) green, unit coverage ≥ 90% for the module (the project standard; the aggregate gate in §6 enforces it across `internal/...`), `go vet`/`staticcheck` clean, design doc updated if reality diverged.

---

## 8. Open questions / future
- Spelling/grammar: candidate libs need evaluation; schema already reserves columns.
- Distributed crawling: out of scope; single-process concurrency is the design point.
- Windows support: nothing platform-specific except Chrome discovery; CI matrix later.
- **Settle thresholds are code constants, not config** (decided 2026-06-11): 500ms
  network-idle window, 1.5s wire-silence window, 2×500ms DOM-stability probes
  (`internal/render`) stay fixed — `rendering.wait_strategy: fixed` is the escape hatch
  when adaptive settling misbehaves, so per-threshold knobs would add config surface
  without a use case. (2026-06-25 — R9a: adaptive now *also* gates those three signals
  on a document-start shim's in-window `setTimeout` count, so it waits for timer-injected
  DOM that network-idle alone would miss; still no new config knob — the count is bounded
  by `ajax_timeout_sec` and the existing `wait_strategy: fixed` remains the escape hatch.
  This is the targeted form of the `MutationObserver` upgrade noted below, scoped to the
  scheduled-timer case rather than general mutation tracking.) If pages ever settle
  wrongly beyond this, the next precision upgrade is a `MutationObserver` injected at
  document start ("ms since last DOM mutation") instead of polling node counts.

- **Schema-floor refusal handling is deferred until a floor is actually raised**
  (decided 2026-06-19). The migration ladder's `min*Version` floors are `0`, so
  `upgrade()`'s "schema vN predates the minimum supported vF" error (§5.3) cannot
  fire yet; building surface handling for an error that can't occur would be
  speculative and untestable. When we first raise a floor, three things land
  together with it: (1) make the refusal a **typed sentinel** (e.g.
  `store.ErrSchemaTooOld`) so surfaces detect it with `errors.Is` instead of
  string-matching; (2) decide whether the **read-only open paths** that currently
  skip migrations (`mcp.openCrawlRO` → `query`/`issue_summary`, and
  `serve.open()`) should check `user_version` and refuse a sub-floor DB, or keep
  serving best-effort reads — today they'd silently query an unsupported schema;
  (3) give the **desktop** a real "this crawl predates this version — re-crawl or
  remove it" affordance, and let **serve** return that specific message (it
  currently masks every open error as a generic 404 by design). On the **CLI** and
  the MCP **`resume_crawl`** path the error already propagates verbatim, so those
  need nothing. Until then the floors stay at `0` (migrate everything) and the
  refusal branch is pinned only by `store.TestUpgradeLadder`.

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

**2026-06-17 — tokenization parity (word count / sentences / Flesch, was the
§9.2 #1 backlog row).** Reverse-engineered Screaming Frog v24.1's content
tokenization against ~100 controlled probe pages (crawled with SF headless) and
rebuilt it grounded in correct behaviour. The content text is now extracted as
**logical-line blocks** (`internal/parse.extractBlocks`) and the readability
metrics from a pure stats module (`internal/parse/readability.go`,
`computeStats`/`blockSentences`), replacing the old single-pass walker and the
guessed `floor(chars/85)` sentence heuristic. Fixes, each pinned by a probe
measured against SF (`content_parity_test.go`, `readability_test.go`):
table cells (`<td>/<th>`) separate words but the **row** (`<tr>`) is the
sentence/line; **list markers are rendered text** — `<ul>`→`•` (a word), `<ol>`→
`N.` (a number word **and** a terminating period), attached to the item's first
line even through wrapped block children, nested per level, `<dl>` none;
**sentence segmentation** = greedy ≤80-char run packing **plus** a split at every
`.!?` terminator (including mid-word, e.g. `3.14`), deduplicated against run
boundaries; literal newlines in text collapse to a word break, **except inside
`<pre>`** where a newline is a line/sentence boundary (HTML-correct). Word/
sentence parity is exact on **95/101 probes**; on a real 400-page site word-count
exact-match rose 42%→79%. The *readability-bucket* half of R5/G7 did **not**
materially improve, though (Flesch median |diff| only 2.66→2.40; bucket agreement
~flat at 89%→86%): with word and sentence counts now matching, the residual Flesch
error is dominated by the **syllable-estimation heuristic**, tracked as its own
§9.2 row and the R5/G7 readability-half FIX-LATER. The six residual
probes are documented SF artifacts or separate gaps (see §9.2): SF's `<main>`-
present content-area heuristic, SF counting a `<pre>` ASCII-art symbol line and a
bare `<hr>` as words, an SF off-by-one when a terminator lands exactly on the
80-char run edge, and Flesch divergence from the **syllable-estimation
heuristic** on vocabulary-dense pages (identical word+sentence counts, different
syllables) — the last is the main remaining readability-bucket limiter and needs
a pronunciation dictionary. The §9.2 backlog row is retired; these residuals are
tracked as their own rows.

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

**2026-06-19 — compare `content` + `structured_data` change detectors wired.**
`compare.change_detection` shipped with both entries enabled by default but
silently dropped them (`compare.content_change_threshold` was unread) — the
product advertised detectors it didn't run. `internal/compare.changeDetection`
now evaluates both. **Content** compares the two crawls' content-area text with
the *same* minhash similarity as near-duplicate analysis (new exported
`analyze.ContentSimilarity`, single source of truth) and reports a change only
when the content moved by more than `content_change_threshold` percent (SF's
">N% similarity change"); a footer/nav-only edit changes the body hash but not
the content area, so it is not reported, and an identical body short-circuits
the comparison. **Structured data** compares the unique, sorted schema.org type
set (SF's "Structured Data Unique Types"). Both flow through every surface that
reads `compare.Result.Changes` (CLI `bluesnake compare`, CSV/JSON/xlsx export,
the desktop comparison view) with no new config or UI. Pinned by
`internal/compare` unit tests, an `analyze.ContentSimilarity` test, and two
`features/compare.feature` scenarios. Retires the §9.1 "two dead
change-detection values" note and the §9.2 "Compare detectors" backlog row.

**2026-06-19 — rich-result matrix breadth: SoftwareApplication / Review /
AggregateRating (G5-matrix, partial).** The curated `internal/structured`
requirements table grew from 12 to 15 schema.org types, grounded in Google's
current rich-results docs (the "ground in correct behaviour, not the reference
tool" §0 anchor) rather than copied from SF: **SoftwareApplication** (+ the
`WebApplication` / `MobileApplication` subtypes Google validates identically —
matched on the leaf `@type`), **Review**, and **AggregateRating**. Two real
Google requirement shapes the old AND-only `required` list couldn't express are
now modelled: an `anyOf` group ("at least one of", e.g. AggregateRating needs
`ratingCount` **or** `reviewCount`; a Software App needs `aggregateRating` **or**
`review`) emits a single error when no member is present, and Review is
`trigger`-gated on `reviewRating` so a rating-less or nested review is not a
snippet candidate — the nesting-dependent `itemReviewed` is deliberately dropped
to avoid reproducing the R6 Organization-logo over-warning regression. **HowTo
is deliberately excluded**: Google deprecated HowTo rich results in Sep 2023, so
validating it would chase a stale feature. No new issue IDs (occurrences reuse
`structured_validation_error` / `_warning`), so every surface — `bluesnake
issues`, `crawl_overview`, the serve `/issues` endpoint, the MCP tools and the
desktop UI — picks them up unchanged, and the catalogue-coverage meta-test is
unaffected. Pinned by `internal/structured` table-driven tests (per-type
required/recommended/anyOf, the subtype aliases, the trigger gate, and an
explicit nested-rating no-over-warn guard). **Still open** (the row stays in
§9.2, narrowed): standalone `Offer` / merchant-listing depth, the LocalBusiness
subtype hierarchy (`Restaurant` et al., needs a schema.org IS-A map), nested
property checks (`offers.price`, `reviewRating.ratingValue` — the engine checks
top-level presence only), and the SF cross-check on trigger.dev / vellum.ai /
braintrust.dev / zenskar.com to verify counts and tune the error/warning split
(the established R6-style verification, which needs the Screaming Frog harness).

**2026-06-19 — schema.org subtype-hierarchy resolution (G5-matrix, the
LocalBusiness-subtree increment).** The previous note matched only literal
curated `@type`s, so `Restaurant`/`Hospital`/`TechArticle`/`Festival` got zero
validation. bluesnake now embeds the **objective schema.org IS-A graph**
(`internal/structured/schemaorg_hierarchy.txt`, 945 edges, generated
deterministically from schema.org's published types CSV by a checked-in
`go:generate` tool — `gen_hierarchy.go` — with **no LLM in the data path**) and
resolves any seen type to its **most-specific curated ancestor**
(`internal/structured/hierarchy.go`). `requirements` stays the single source of
truth (no per-subtype aliases); resolution is computed against it at load time.
**264 schema.org types now validate** (up from ~15): 150 LocalBusiness subtypes
(Restaurant, Bakery, Attorney, …), plus the Event (26), Organization (logo-gated,
safe), Article (recommended-only, safe), Product and Review subtrees. Design
points, each grounded and pinned: a directly-curated type keeps its own rules
(short-circuit, never falls through to a parent); the only incomparable tie in
the whole vocabulary is `ReviewNewsArticle`; per-node resolution collapses
redundant supertype roots so `["NewsArticle","Article"]` and
`["LocalBusiness","Organization"]` validate once, while `["VideoGame",
"SoftwareApplication"]` still validates as the explicit app co-type. **Validation
is scoped to the page's PRIMARY entity** — top-level / `@graph`-member JSON-LD
nodes and top-level microdata items — and nested reference stubs (a `Store` as
`offers.seller`, a `Restaurant` as `publisher`/`author`) record their types but
are **not** validated, matching how Google scopes a feature's required props and
avoiding an R6-class "missing address" false error (caught by an adversarial
review). JSON-LD `@type` is normalized through `shortType` (full-URL/prefixed),
and `data.Types` now records short forms (parity with SF and microdata).
**Grounded exclusions** (subtype routed to a different/retired Google feature, so
inheriting the parent over-flags): the Vehicle subtree ↛ Product (Vehicle-listing
deprecated 2025-06), `VideoGame`/`OperatingSystem`/`RuntimePlatform` ↛
SoftwareApplication, `ClaimReview`/`MediaReview`/`EmployerReview` ↛ Review (Fact
Check / media-authenticity / Employer features), `EmployerAggregateRating` ↛
AggregateRating, the `UserInteraction` telemetry family ↛ Event,
`ReviewNewsArticle` ↛ Review. Grounded and adversarially reviewed by two
background workflows (Google-feature semantics + over-warning/correctness/
cross-surface review); the sole confirmed blocker (nested-entity over-warning)
and a microdata multi-`itemtype` major were fixed before this note. Still open:
standalone `Offer`/merchant-listing depth, nested-property checks
(`offers.price`), and the SF count cross-check (needs the Screaming Frog harness).
A separate pre-existing limitation surfaced by the multi-property rules — the
`issues` table stored one detail per `(url, issue)`, so multiple missing-property
details collapsed to the last — **is fixed by the `(url, issue, detail)` key in
the next note**, so each missing property now persists as its own occurrence.

**2026-06-19 — issues store keeps every distinct occurrence.** The `issues`
table keyed on `(url, issue)`, so a page that triggered one check several times
with different details kept only the **last** detail (the write path is
`INSERT OR REPLACE`). Systemic across any multi-detail check; most visible after
the schema.org rich-result work above, where one page commonly emits several
`structured_validation_error`/`_warning` rows (one per missing required
property). The primary key is now `(url, issue, detail)`, so each distinct
occurrence is its own row while exact duplicates still collapse — which preserves
the `INSERT OR REPLACE` idempotency that re-analysis depends on (`finalize.Analyze`
clears via `SaveIssues` then re-adds, so no stale rows accumulate). A natural key
beats an autoincrement id here precisely for that idempotency. Affected-URL
tallies are unchanged: the count paths now use `COUNT(DISTINCT url)`
(`store.IssueCounts`/`IssueURLs`, the MCP `issue_summary` query), so the headline
numbers across CLI/serve/MCP/desktop stay per-URL while exports and the issues
table now list all details. *(The "clears via `SaveIssues` then re-adds"
mechanism referenced here was superseded 2026-07-02 by ownership-partitioned
replace semantics — see that note.)* Existing crawl DBs migrate on open
(a transactional `issues` table rebuild, detected via `PRAGMA table_info`,
idempotent) — required so re-analysing an old crawl doesn't keep collapsing on
the very path meant to fix it. Pinned, mutation-verified, by
`store.TestIssueMultiDetailPreserved`/`TestIssuesDetailPKMigration`,
`export.TestIssuesExportListsEveryDetail` (driven through the real
`issues.Evaluate` structured-validation path), and
`mcp.TestIssueSummaryCountsDistinctURLs`.

**2026-06-19 — schema versioning via `user_version` (migrations are now a
retirable ladder).** Folded the store's previously ad-hoc migrations (the two
`pages` `ADD COLUMN`s, the registry `crawls.total` column, and the issues-PK
rebuild above) into a single versioned mechanism in `internal/store`. Every
crawl/registry DB now records its revision in SQLite's `user_version`; `open`
CREATEs the latest shape then runs a generic `upgrade()` that stamps fresh DBs to
the top and runs only the above-revision steps on existing ones, each in a
`user_version`-bumping transaction. Migrations are an append-only ladder of
stably-numbered, idempotent steps; the per-ladder `min*Version` floor is the
removal lever — raising it (and deleting the sub-floor steps, never renumbering
the rest) makes too-old DBs fail to open with a clear re-crawl message instead of
half-migrating, so retired migration code is *safely* deletable. This is the
durable-revision marker the old detect-or-tolerate ALTERs lacked, which is why
they could never be removed. The §5.3 "Schema versioning & migrations" box
documents the mechanism and the retirement procedure. Pinned, mutation-verified,
by `store.TestUpgradeLadder` (stepwise apply, idempotent re-open, floor refusal,
fresh stamp) and `store.TestFreshDBSchemaVersion`.

**2026-06-21 — `advanced.ignore_paginated_for_duplicates` wired (was a §9.1
"behavioural flags not yet wired" row).** Screaming Frog's "Ignore Paginated URLs
for Duplicate Filters" now takes effect instead of parsing to a no-op. A URL is
"paginated" iff it declares a `rel="prev"` link (HTML or HTTP `Link` header) —
i.e. it is page 2+ of a sequence; page 1 carries only `rel="next"` and is
unaffected. That single rule lives in one place, `parse.Facts.IsPaginated()`, and
gates both duplicate-detection sites: the per-aggregate duplicate filters in
`issues.duplicates()` (`title_duplicate`, `description_duplicate`,
`h1_duplicate`, `h2_duplicate`, `content_exact_duplicate`) and the near-duplicate
candidate set in `analyze.nearDuplicates()` (`content_near_duplicate`) — matching
SF's documented filter list (Page Titles, Meta Description, H1, H2, Content
Exact/Near Duplicates). Excluded pages are neither flagged nor offered as a match
target, so a continuation page no longer makes page 1 (or its siblings) look
duplicated. Grounded in the rel=next/prev pagination signal (no longer a Google
indexing signal, but still SF's declared-pagination marker), not just an
output-match. **Default off, so default crawls are unchanged**; it flows through
every surface that reads the issues table / near-dup occurrences (CLI `bluesnake
issues`, `crawl_overview`, serve `/issues`, MCP `issue_summary`/`query`, desktop)
with no per-surface code, and the MCP knob catalogue now describes it rather than
flagging it "not yet wired". Pinned by
`issues.TestIgnorePaginatedForDuplicates` (off-vs-on across all five duplicate
filters, asserting page 1 stays in the filters) and
`analyze.TestIgnorePaginatedForNearDuplicates`.

**2026-06-21 — nested integral-object validation (G5-matrix, the `offers.price` /
`reviewRating.ratingValue` half).** bluesnake validated only a page's PRIMARY
entity, so an `Offer` with no price or a `reviewRating` with no `ratingValue`
slipped past silently (0 Rich-Result errors on otherwise-valid e-commerce
markup). The validator now recurses into a curated whitelist of INTEGRAL
sub-entities — `internal/structured.integralProps` (offers→Offer,
review/reviews→Review, reviewRating→Rating, aggregateRating→AggregateRating) —
and validates each against its own Google-required props, while every other
nested object (a `Store` as `offers.seller`, an `Organization` as
`publisher`/`author`, and their whole subtrees) stays a recorded-but-unvalidated
reference stub, preserving the R6 over-warn guard (the recursion is gated on the
node itself being validated, so a stub's nested rating is not validated either).
Three new `requirements` rows, grounded in Google's rich-results docs and
cross-checked against SF v24.1 controlled probes: `Offer` (price **and**
priceCurrency required, each an `anyOf` with `priceSpecification`),
`AggregateOffer` (a price RANGE keyed on `lowPrice`, not `price`, so it gets its
own rule and a valid range-offer isn't false-flagged), and `Rating`
(ratingValue). The schema.org IS-A `EmployerAggregateRating` (the Employer-Rating
feature) is excluded from the new `Rating` root in `hierarchy.go`, mirroring its
existing AggregateRating exclusion — else suppressing its AggregateRating edge
would let it fall back to `Rating[ratingValue]` and re-introduce the very false
error the exclusion prevents. **Parent-aware, to avoid an R6-class over-warn:**
Offer price/priceCurrency are required ONLY under a Product parent
(`offerStrictParents`) — probes proved Google's Offer requirements are
*per-feature*: an Event offer (a ticket `url` is enough) treats price/currency as
WARNINGS, and a Software-App offer requires price but NOT priceCurrency, so
validating those with the Product rule would over-error. Per the experiment
owner's decision the counting model is **single-count per page**: where SF
duplicates the SAME finding across two Google features (Product Snippet +
Merchant Listing — e.g. a missing price shows SF 2 / BS 1) bluesnake reports it
once on purpose (one finding per real problem). No new issue IDs (reuses
`structured_validation_error`/`_warning`), so CLI/serve/MCP/desktop pick it up
unchanged via the shared `internal/structured` path — JSON-LD and microdata
both. SF-cross-checked on probe pages: per-page Rich-Result **error** parity is
now EXACT on every Product-context page with ZERO over-errors (before the change
bluesnake reported 0 errors on all of them). Pinned by `internal/structured`
tests (`TestNestedOfferRequiredProperties`, `TestNestedRatingRequiredRatingValue`,
`TestNestedAggregateOfferNotMisvalidated`, `TestNestedOfferParentAware`,
`TestNestedReferenceStubNotValidated`, `TestMicrodataNestedOffer`). **Still open**
(the §9.2 row stays, narrowed again): per-feature Offer profiles (the Software-App
`price` error and Event recommended warnings bluesnake now scopes out), the
merchant-listing RECOMMENDED breadth (Offer `itemCondition`/`availability`,
Product `gtin`/`description` — bluesnake under-warns vs SF on Product-with-offers
pages by exactly these), SF's property-VALUE *type* checks ("address must be of
type PostalAddress" — a different validation dimension), and standalone-`Offer`
merchant depth.

**2026-07-02 — finalize/analyze staleness: ownership-partitioned replace
semantics + PageRank node-set pruning (#75).** The post-crawl write path had two
writers sharing persisted analysis state with mismatched replace scopes, and
PageRank's edge gate contradicted its own node-set predicate. Three pre-existing
bugs, one design: **every finalize writer atomically replaces exactly the state
it owns.** (1) The issues table is partitioned between its two writers by the
analysis-phase check set (`issues.AnalysisIDs`, declared next to the catalogue
and pinned against what each phase *actually emits* by
`analyze.TestIssuePartitionMatchesEmitters`): `store.SaveIssues` replaces every
row *except* that partition, so the cheap catalogue-only refresh (`bluesnake
issues`) no longer silently wipes a completed crawl's redirect-chain / near-dup /
hreflang / pagination / sitemap / llms.txt findings until the next full Analyze.
(2) `store.SaveAnalysis` — now a single transaction — resets the five
analysis-owned per-page columns (`link_score`, `unique_inlinks`,
`unique_outlinks`, `closest_similarity`, `near_dup_count`) to their schema
defaults before applying the new result maps, and replaces its own issue
partition (the append-only `AddIssues` is deleted): re-analysing with
near-duplicates off or different thresholds no longer leaves the previous run's
metrics or occurrence rows mixed into the new result. (3) PageRank computes over
the graph **induced on its documented node set** (internal ∧ crawled): an edge
to a never-crawled internal target (robots-blocked, errored, still-queued)
neither receives a rank share — it used to hold rank mass, shift the `v/max·100`
scaling, and appear in `LinkScores` — nor dilutes its source's out-degree; the
same "counts for link metrics, not for PageRank" treatment self-loops already
had, and the classic dangling-link removal from the original PageRank
formulation. Unique in/outlink metrics deliberately keep such edges (SF parity).
All three flow through every surface (CLI, MCP, desktop) via the shared
finalize/store/analyze path. Pinned by
`finalize.TestIssuesCmdPreservesAnalysisIssues` (full crawl → byte-identical
issues table across an `issues` refresh), `store.TestSaveIssuesPreservesAnalysisPartition`,
`store.TestReanalyzeClearsStaleScoreColumns`, and
`analyze.TestPageRank_NonCrawledDstsPruned` (bit-exact induced-subgraph scores
on both the CSR and Facts.Links paths).

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
- Structured data validation: curated Google rich-results requirement table
  (data-driven, `internal/structured.requirements`; ~18 curated roots incl. the
  nested Offer/AggregateOffer/Rating sub-entities, resolving 264 schema.org types
  via the embedded IS-A graph, with parent-aware nested-object validation); full
  Schema.org vocabulary validation not shipped.
- AMP: structural checks (canonical/viewport/charset/amp-script/reciprocity),
  not the full official AMP validator rule set.
- Sitemap orphan detection approximates "only discoverable via sitemap" as
  zero inlinks + sitemap-seeded.
- Concurrency: goroutine-per-URL behind a semaphore + sink-mirrored frontier.
  Correct and crash-safe, but RAM grows ~linearly with crawled pages and
  **explosively with the discovered frontier** — one parked goroutine per
  *admitted* URL, plus the full result set retained in memory. Measured peak
  **~1.7–1.9 GB at only ~4,000 crawled pages** on a faceted e-commerce site
  (frontier hit ~239k in 30 s), so it bites at *thousands*, not millions, of
  URLs. Full investigation, root-cause sink list, and a test-first fix plan
  (bounded worker pool + persistent SQLite frontier + stream-to-SQLite, sized
  for the future parallel multi-site mode): **[docs/MEMORY-SCALING.md](MEMORY-SCALING.md)**.

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
`rendering.flatten_iframes` (chromedp `OuterHTML` doesn't inline iframe
documents), `rendering.window` (the preset name is ignored;
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
`advanced.respect_canonical`, `advanced.respect_next_prev`, and
`analysis.canonicals` (canonical-chain analysis currently piggybacks on
`analysis.redirect_chains`).

**Storage knobs not yet wired:** `storage.dir` (the store path comes from
`--store-dir` or the app default, not this field) and `storage.retention_days`
(no pruning exists). When retention lands it will be an explicit
`bluesnake crawls prune` command (+ a desktop action), never an automatic
delete-on-startup.

**`limits.max_urls` on resume — cumulative, with one residual (2026-06-17):**
the crawl-total budget is a per-session fetch counter (`crawler.fetched`); a
resumed session now seeds it from the already-recorded pages
(`len(resumeProcessed)`), so a paused-then-resumed crawl honours the same total
budget as a straight crawl instead of being granted a fresh `max_urls` each
session (which previously let it fetch well past the cap). **TODO (minor):** the
seed counts *every* recorded page including robots-blocked ones, whereas the
live counter only increments for URLs that pass the robots gate — so a resumed
crawl that actually hits `max_urls` while having robots-blocked pages may fetch
a handful fewer than a straight crawl. Exact parity needs seeding from the
non-robots-blocked page count (`COUNT(*) WHERE state != 'blocked_robots'`),
threaded through the resume call sites. Negligible at the default cap (5M).

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
| Readability syllable precision (Flesch/buckets) | Low-Med | Med | Med | Residual of the 2026-06-17 tokenization fix: word & sentence counts now match SF, but the vowel-group syllable heuristic diverges on vocabulary-dense pages (identical words+sentences, Flesch off by enough to flip a readability bucket). The remaining readability-bucket gap. A pronunciation dictionary (CMUdict-style, embedded) is the real fix; large and overlaps the cut spelling/dictionary work |
| Content-area `<main>`/sectioning heuristic | Low-Med | Low-Med | Med | When a page has `<main>`, SF appears to drop some sibling sectioning content (`<header>`/`<aside>`) from the content area; bluesnake counts all non-nav/footer text. Surfaced as probe `d02`. Needs a dedicated content-area probe sweep before changing extraction |
| `<pre>` ASCII-art symbol word counting | Low | Near-zero | Low | Inside `<pre>`, SF counts box-drawing/symbol lines as fewer "words" than bluesnake (probe `pre03`). Newlines-as-lines already match; only pure-symbol token counting differs. Niche (ascii diagrams); quirky SF rule |
| Issues catalogue 164 → ~300 (residual tail) | Med | Med | Low-Med | 27 checks shipped 2026-06-12 (directives/pagination/hreflang/links complete for native data, §9). What remains needs new infrastructure per check — rendering-mode JS filters, Bad Content Type sniffing, Broken Bookmark (G28-entangled), HTTP Refresh header, sitemap >50MB — or is a11y/spelling/AMP-validator work tracked in its own rows |
| `respect_noindex/canonical/next_prev` wiring | Med | High | Med | Real SF workflow knobs ("crawl as Google indexes"). Today they parse and silently do nothing. Defaults off, so no default-behavior risk |
| SF-style elem paths `[n]`/`[@class]` (G10) | High | Med | Med | Kills ~12k cosmetic path diffs/site and unlocks SF's class-driven Navigation position matches. Exact qualifier rules need probes (SF emits [n] only for same-tag siblings, sometimes [@class] instead) |
| Accessibility (axe via CDP) | Med | High | Med | Most-requested real-world audit type; SF ships it. Needs Chrome + axe bundle injection. More "useful" than "parity" |
| Shadow-DOM/iframe flattening (`rendering.flatten_*`) | Med | Med-High | Med | Web-component sites currently lose rendered text/links that SF sees |
| Rich-result matrix breadth (G5 residual, narrowed again) | Low | Low | Low-Med | SoftwareApplication/Review/AggregateRating (2026-06-19), the full schema.org subtype hierarchy (264 types incl. all 150 LocalBusiness subtypes, 2026-06-19), AND nested integral-object validation (`offers.price`, `reviewRating.ratingValue`, parent-aware, single-count — 2026-06-21, SF-cross-checked, error parity EXACT in Product context) all landed; HowTo + deprecated/wrong-feature subtypes deliberately excluded. Residual: per-feature Offer profiles (Software-App `price`, Event recommended), merchant-listing RECOMMENDED breadth (Offer `itemCondition`/`availability`, Product `gtin`/`description`), SF property-VALUE *type* checks ("address must be PostalAddress"), standalone-`Offer` depth |
| Cookie collection (`url_details.cookies`) | Med | Med | Low-Med | Whole SF report we lack; GDPR/consent audits use it. Needs a cookies table + rendered-mode capture |
| Persistent-frontier worker pool (scale) | Indirect | High | Med-High | Gates large-site (e-commerce) crawls — and the future **parallel multi-site** mode. Goroutine-per-URL + in-memory visited-set/result map → **measured peak ~1.7–1.9 GB at only ~4k crawled pages** on a faceted site (frontier exploded to ~239k). This is **RAM** (distinct from on-disk stores). Full root-cause sink list, fix, and TDD plan: **[docs/MEMORY-SCALING.md](MEMORY-SCALING.md)** |
| ~~Fragment self-edges (G28)~~ → rendered remainder folded into R9 | — | — | — | RE-VERIFIED 2026-06-21: the static-HTML gap is GONE — current BS matches SF exactly on every fragment-anchor pattern (empty/named/dup/`#`/svg-icon permalinks; SF page set = BS page set). The SF dedup-semantics probe this row asked for confirmed SF keeps distinct empty self-frag edges with the fragment stripped, exactly as BS does; the prior KeepFragments + R3/R10 work resolved it. The original "44 vs 18" was a rendered-DOM discovery effect (greptile/artisan emit `href="#…"` only after JS render) → tracked under R9, not a static link-extraction bug |
| Schema.org vocabulary validation | Low-Med | Low | Med | SF's plain validation found zero issues across all 5 test domains — rich-results is what fires. Big build, small payoff |
| Store-flag enforcement (§9.1) | Low | Low-Med | Low | DB size on big crawls; SF-visible counts already match |
| PDF extraction (`extraction.pdf.*`) | Low-Med | Low | Med | Niche (gov/edu/docs sites). New parser dependency |
| AMP full validator | Low | Near-zero | Med | AMP is effectively dead. Skip unless a user asks |
| Spelling & grammar | Med | Low | High | Visible SF tab but noisy and rarely enabled; stays cut (§1 non-goals) |
| Orphan-detection exactness, retention/prune, cert dirs, window presets | Low | Low | Low | Housekeeping tier |
