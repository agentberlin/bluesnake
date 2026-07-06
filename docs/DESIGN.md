# bluesnake — Design Document

A modern, headless, CLI-first website crawler and SEO auditor in Go. Functional parity target: Screaming Frog SEO Spider's crawling/auditing core — **without** the UI and **without** third-party API integrations (GA4, GSC, PageSpeed/Lighthouse, link indexes, AI providers), and **without** opaque binary config files: everything is plain-text config + flags.

Status: living design document — it describes the product's intended shape and the standard every change is held to. It is **not** a changelog: what shipped and when lives in git history and PRs, and future/backlog work lives in the issue tracker. The feature inventories this design is derived from live in `docs/research/`:
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
   read the git history and the parity comparison
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
record the domain*, so the same divergence is never investigated twice.

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
bluesnake tools <tool> [args]          # standalone site testers (robots, sitemap, ...; `tools list`)
bluesnake config init|validate|show|profiles  # default config / validate / effective config / list saved profiles
bluesnake serve                        # read-only localhost JSON API over the crawl store (--addr)
bluesnake mcp                          # MCP server for LLM agents over streamable HTTP (--addr, default 127.0.0.1:8473)
```

Global flags: `--config <file>`, `--store-dir <dir>` (default `~/.bluesnake`), `--output <dir>`, `--format csv|json|jsonl|xlsx`, `--timestamped-output`, `--overwrite`, `--quiet/--verbose`, `--log json|text`.

Every config key is overridable as a flag using dotted names: `--set spider.limits.max_depth=3 --set speed.max_threads=10` plus dedicated shorthand flags for the common ones (`--depth`, `--threads`, `--rate`, `--include`, `--exclude`, `--user-agent`, ...).

Base setup (§5.11): `crawl` defaults to reusing the setup the seed's site last ran with; `--setup last|app|defaults` picks the base explicitly (`app` = the saved default profile, `defaults` = the pinned built-ins for CI), `--profile <name>`/`--config <file>` are the named bases — all mutually exclusive, with `--set`/shorthands overlaying whichever base wins. The resolved source is printed. `projects crawl-all` resolves per member by default (each site's own last setup) and treats `--profile`/`--config`/`--setup app|defaults` as override-all.

Named profiles (the configs the desktop app manages; the default one is presented there as "App settings") are readable and usable from the CLI — `config profiles` lists them, `config show --profile <name>` prints one, and `crawl`/`list`/`projects crawl-all` accept `--profile <name>` as the base config (mutually exclusive with `--config`; `--set` and shorthand flags apply on top). The CLI never creates or edits profiles. Every enqueue path — CLI, desktop, MCP — freezes the effective config into the job spec at enqueue time (`runner.FreezeSpec`), so a queued job is immune to profile edits made while it waits; the crawl then freezes its own copy into the crawl DB at start (`store.CreateCrawl`) as before.

Crawl UX (headless but informative): single-line progress (crawled/queued/errors/URLs-sec), `--progress none|line|live`; non-zero exit codes contract: `0` ok, `1` crawl error, `2` config error, `3` interrupted (resumable).

`Ctrl-C` = graceful pause (frontier + state committed; prints `bluesnake resume <id>` hint). Second `Ctrl-C` = hard stop (still safe by WAL).

---

## 4. Configuration

Config is one YAML file = one crawl profile; every key has a default, so an empty
file is a valid config. The authoritative, fully-commented reference is whatever
`bluesnake config init` emits — generated from the schema, never hand-maintained.
Rather than reproduce the full schema here (it would only drift), this section
describes the shape. The top-level groups:

- `mode` — spider | list (set implicitly by the CLI subcommand).
- `scope` — subdomain/folder boundaries, nofollow handling, CDNs treated as
  internal, include/exclude (RE2, matched against the URL-encoded address).
- `resources` / `links` — per-type `{store, crawl}` pairs (images, media, css,
  js, canonicals, hreflang, iframes, meta-refresh, …); defaults mirror our house
  SF profile (externals not checked, canonicals recorded but not fetched).
- `sitemaps` — crawl linked sitemaps, auto-discover via robots.txt, explicit URLs.
- `llms_txt` — /llms.txt + /llms-full.txt audit and curated-link admission (§5.6).
- `site_checks` — crawl-integrated site-level audits: robots, sitemap, AI-bot
  access, render diff (§5.10). All on by default except `render_diff`.
- `extraction` — per-URL data toggles, structured-data formats, and
  HTML/rendered/WARC/PDF storage. (Extraction is always full here — see the
  no-ops note below.)
- `limits` — max urls/depth/folder-depth/query-strings/redirects/URL-length/
  page-size, plus per-path and per-subdomain caps.
- `rendering` — text | javascript, wait strategy, AJAX timeout, window preset,
  screenshots, shadow-DOM/iframe flattening, global render slots (§5.8). The old
  AJAX-crawling scheme is deliberately dropped (deprecated by Google 2018).
- `advanced` — cookie storage, the `respect_*` directive knobs, HSTS, percent-
  encoding, retries, timeouts.
- `thresholds` — the SF "Preferences" numbers (title/description char + pixel
  bounds, low-content, high-depth, non-descriptive anchors, soft-404 patterns).
- `content` — content-area include/exclude selectors, near-duplicate settings.
- `robots` — respect | ignore | ignore-report, blocked-URL reporting, custom
  per-host robots files.
- `url_rewriting` — remove params, regex replace, lowercase.
- `speed` — max threads, max URLs/sec.
- `http` — user agent, robots UA, HTTP version, browser headers, custom headers,
  proxy, and basic/cookie auth (a supplied session cookie is the forms-auth
  replacement).
- `custom_search` / `custom_extraction` / `custom_js` — user-defined matchers,
  xpath/css/regex extraction, and CDP JS snippets.
- `link_positions` — ordered element-path → position-bucket rules.
- `list_mode` — robots + depth behaviour for `bluesnake list`.
- `analysis` — which post-crawl analyzers run (§5.6).
- `storage` / `compare` — store dir + retention; change-detection fields and URL
  mapping for `bluesnake compare`.

Validation: unknown keys are errors (with a "did you mean" suggestion); every regex is compile-checked at load; `config validate` runs the same code path.

#### Known no-ops (config parsed but not yet consumed)

Some keys validate but currently change nothing; they are listed so the schema
doesn't silently lie (the YAML stays valid for forward-compat):

- **Extraction toggles are inert by design.** bluesnake extracts the full
  per-URL dataset in one parse pass, so `extraction.page_details.*`,
  `extraction.url_details.*` and `extraction.directives.*` never gate anything
  (`url_details.cookies` is additionally not collected yet — no cookies table).
- **Reserved for unbuilt features:** `extraction.pdf.*`,
  `extraction.structured_data.{google_rich_results_validation,case_sensitive}`,
  `rendering.flatten_iframes`, the `rendering.window` preset name (explicit
  width/height are honoured), `advanced.html_validation`, `http.trusted_cert_dirs`.
- **Resource/link `store` flags are unenforced** — every parsed edge is stored
  regardless; the `crawl` half of each pair *is* enforced.
- **Not yet wired:** `advanced.{respect_noindex,respect_canonical,respect_next_prev}`,
  `analysis.canonicals` (piggybacks on redirect-chain analysis), `storage.dir`
  (the path comes from `--store-dir`/the app default) and `storage.retention_days`
  (no pruning; when it lands it will be an explicit `bluesnake crawls prune`,
  never auto-delete-on-startup).

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
CREATE TABLE site_checks   (kind TEXT, subject TEXT, report JSON, checked_at INT,    -- site-level audit reports (§5.10): robots|sitemap|ai_bots|render_diff,
                            PRIMARY KEY (kind, subject));                            -- findings re-derived in analyze via sitecheck.DecodeFindings
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

- A **fresh** database (no tables yet → this open created it) is stamped straight to the top of its ladder — `max(floor, highest step)`, so an empty/fully-retired ladder still stamps the current revision, not v0; the migration steps never run.
- An **existing** database runs only the ladder steps whose version is above its stored revision, each applied in a transaction that bumps `user_version` atomically (a crash mid-step rolls back to the prior revision). The common case — already current — is one pragma read.

Migrations are an **append-only ladder** (`crawlMigrations`, `registryMigrations`): each step has a *stable* version number (never renumbered or reordered) and an idempotent `apply` func. Adding a schema change = append one step. The two `min*Version` floors are the removal lever (below). Both ladders are **currently empty**: every step was retired once all installs reached the top (crawl v5, registry v2), so the floors now sit at those tops and the next schema change appends just above (v6 / v3), reusing the retained `addColumn`/`columnExists` helpers.

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

**Shadow-DOM flattening** (`rendering.flatten_shadow_dom`, default on): `OuterHTML` does not serialize shadow roots, so links/headings/structured-data inside Web-Components shadow trees would be invisible. When on, the rendered snapshot is produced by a synchronous pass (right before serialization, after any screenshot) that moves each shadow host's children up into the host as light DOM and returns native `outerHTML`; nested roots are flattened a level per pass. Open roots are reached via `element.shadowRoot`; **closed** roots are reached via a document-start `attachShadow` shim that stashes them (mode left unchanged). The flattened HTML feeds the same `parse` + `structured` pipeline, so shadow links surface as `origin=rendered` — matching Screaming Frog, which pierces both open and closed shadow DOM. Residual: closed *declarative* shadow DOM (parser-created, no `attachShadow` call) is unreachable. (`rendering.flatten_iframes` remains unbuilt.)

**Settle detection** (`internal/render`): navigation does **not** wait for the browser `load` event (background media can hold it open for many seconds after the DOM is done); the anchor is `DOMContentLoaded`. After DCL, a page is settled when any of:
1. the countable network is fully idle for 500ms — media, websockets, EventSource, ping/beacon, prefetch and `blob:`/`data:` requests are excluded from the in-flight set (they routinely stay open forever);
2. the DOM node count holds steady across two 500ms probes with no script/stylesheet/XHR/fetch in flight (absorbs third-party widgets and analytics that chatter indefinitely);
3. the wire is completely silent for 1.5s (only permanently-open requests remain).

**None of those three fire while the page still has DOM work scheduled on an in-window timer**: a shim injected at document-start (`addScriptToEvaluateOnNewDocument`) wraps `setTimeout`/`clearTimeout` and exposes a live count of pending one-shot timers in `window.__bsPendingTimers`, which the settle loop reads each tick. Network-idle is otherwise the wrong sole signal for SPAs that inject content via `setTimeout` with no accompanying request — the wire goes quiet ~500ms after DCL, long before a `setTimeout(…, 1500)` fires (Screaming Frog catches these because it dwells its full AJAX timeout). The count is deliberately narrow so the latency lands only where waiting is correct: `setInterval` is never counted, a one-shot whose delay exceeds the cap is never counted (can't fire in-window), and a timer re-armed from inside another timer's callback is never counted (a self-rescheduling animation/poll loop would otherwise dwell to the cap — only its first top-level schedule counts). Residual deferred work the shim does not wait for (bounded, never a hang): string-code timers (`setTimeout("…", d)`, CSP-gated) and DOM injected via `requestAnimationFrame`/microtask/promise.

`rendering.ajax_timeout_sec` is the **hard cap** on the settle phase after DCL (not a fixed sleep) and bounds the timer wait too; `advanced.response_timeout_sec` caps the wait for DCL itself. Worst case therefore equals the old fixed-wait behaviour. Regression tests cover early settle, permanently-open streams/iframes, beacon chatter, in-window timer injection, and re-arming timer loops.

**Wait strategy knob** (`rendering.wait_strategy: adaptive | fixed`): adaptive is the settle detection above; `fixed` waits for the browser load event and then sleeps the *full* AJAX timeout before snapshotting — slower, but the snapshot moment is deterministic, which keeps `compare` runs stable on pages with flaky widgets.

**Custom JavaScript snippets** (`custom_js`): snippet files load at renderer construction (a missing file is a config error naming the snippet). After the page settles, `action` snippets run first (results discarded — they exist to mutate the page), then `extraction` snippets; values are stored in `custom_results` with kind `js` (JS strings verbatim, anything else compact JSON, `error: …` when a snippet throws). Each snippet is bounded by its `timeout_sec` (default 5); a `content_types` list restricts which pages a snippet's results are stored for.

**Global render slots** (`rendering.max_global_renders`): a render re-fetches the page plus every subresource inside a Chrome tab (~100-300MB each), so renders are a first-class bounded resource under the process-wide limiter (`internal/limiter`), in a slot pool **separate** from the fetch cap — a render is not a fetch: different weight, different resource axis. A worker holds a render slot only for the Chrome round-trip (released the moment `Render` returns, panic-safe, mirroring the fetch-slot pattern) and never holds a fetch slot at the same time — nested acquires across M crawls would starve or deadlock the pools against each other. `0` (the default) resolves via `render.GlobalRenderCap` to the same cores-scaled tab ceiling that bounds a single crawl's pool (2/4/8 by CPU count), so single-crawl behaviour is unchanged while M parallel rendered crawls stay bounded out of the box. Renderer **instances** deliberately stay one-per-crawl — a renderer bakes per-crawl config into its Chrome allocator (UA, window size, custom JS snippets), so a shared instance cannot represent two configs; the Chrome *process* count is bounded by `speed.max_concurrent_crawls`, while the expensive axis — concurrently rendering tabs — is bounded process-wide by the render pool. Pause/stop interrupts an in-flight render (renders run under the crawl context): the interrupted item is left pending — recording it raw-only would be permanent, since resume never re-renders a processed page — and a resume re-fetches and re-renders it.

### 5.9 Project layer (competitor study) — an opt-in, removable overlay

`internal/project` adds **Projects**: a *main domain plus its competitors*, for side-by-side benchmarking and per-competitor change-over-time. It is deliberately built as a **fully separable overlay** — like the desktop app (§1), a non-core extension that must never compromise the crawl engine. The contract (stated in the package `doc.go`, proven by the removal procedure below): the feature changes **no** crawl/store/compare/crawler logic, schema, or models. It owns its own database, reads everything else read-only through `store`'s public API, and reuses `internal/compare` unchanged. **Remove it** by deleting the package, its CLI/MCP/desktop registration lines (one `AddCommand`, the appended MCP `Tool` literals, the `ProjectApp` `Bind` entry, the frontend nav/router/`projectApi` additions), and `projects.db` — the rest of the product is byte-for-byte unchanged. No migrations to unwind.

Design decisions:
- **Own database.** A separate `~/.bluesnake/projects.db` (`projects`, `project_domains`), sibling to `registry.db`. The crawl registry and per-crawl DBs are never altered (so it adds no step to the §5.3 migration ladders).
- **Domain-keyed, derived membership.** You add a *domain* (not a crawl) to a project. A site's crawl history is resolved **live** from the registry by matching the seed, so a standalone crawl of a member domain auto-joins and a deleted crawl simply drops out — no crawl→project link is ever persisted, hence no dangling references.
- **Exact site identity, no folding.** A site key is the literal lowercased `host[:port]` of the seed. `example.com`, `www.example.com`, `a.example.com` and `example.com:8080` are **distinct** sites by design (it reuses none of the engine's `www`-stripping host derivers — it has its own `SiteKey`).
- **Associated vs comparable.** Every same-host crawl is *associated* and shown under the site; only a **finished, full-site spider crawl of the root that is not scope-narrowed** (`scope.include` empty) is *comparable* and feeds the numbers. Path crawls, list audits, running, and narrowed crawls are surfaced greyed-out with a reason — visible, but excluded from the math.
- **Dual-mode comparison.** Per-competitor *over time* reuses the pairwise `compare` engine verbatim (same domain ⇒ meaningful URL/issue deltas). *Cross-competitor* is a new read-only **metric scorecard** (`scorecard.go`): site size, indexable rate, status-code mix, issue counts by severity, link score, near-dups (+ optional avg word count / Flesch / schema.org coverage via SQLite JSON functions). Cross-domain URL comparison is **not** offered — disjoint URL sets make it degenerate. All metrics are single-pass SQL aggregates over each crawl DB; `LoadPages` is never used (it would reintroduce the per-crawl memory blow-up).
- **Per-site setups belong to the domain, not the project; fairness is surfaced, not enforced.** A site remembers the setup its last crawl ran with (§5.11 — derived from the crawl registry at enqueue; *nothing* is stored in the project layer, which this feature leaves byte-for-byte untouched). "Crawl all" therefore defaults to **each site's saved setup** (per-member resolution shown in the dialog via `CrawlAllPlan`) with **"one setup for every site"** as the explicit override mode — the shared setup card (base picker + touched-only quick knobs, site-checks selector included), each job's effective config frozen at enqueue like any other crawl. A member's Crawl button opens New Crawl prefilled with the site, where the site's last setup is preselected by §5.11's default. When competitors' latest crawls used materially different settings (rendering, depth, robots), the scorecard shows per-site config badges and a divergence banner — worded to acknowledge the divergence may be deliberate per-site setup — and the strict-fairness remedy is "Crawl all" with one shared setup.
- **Out of scope:** a scheduler. On-demand crawling of a project's sites is the building block a future scheduler would drive.

Surfaces (engine-first, all three per §0): the CLI `bluesnake projects` subtree; five MCP tools (`list_projects`, `create_project`, `add_competitor`, `remove_competitor`, `project_comparison`); and a desktop **Projects** view (Overview + Comparison) bound through a *separate* `ProjectApp` Wails struct so the core `App` binding (and its generated `App.js`) stay untouched.

### 5.10 Site tools & site checks — one engine, three surfaces

`internal/sitecheck` implements **site-level checks** consumed two ways by
every surface: **standalone tools** (interactive testers a user points at any
URL — throwaway by design, results returned to the caller and never
persisted) and the **crawl-integrated site-check pass** (the same checks run
automatically at crawl start, reports persisted, findings emitted as ordinary
catalogue issues). The llms.txt integration (§5.6) is the
architectural template for the crawl half: out-of-band fetch at crawl start,
own table, issues derived in analyze, idempotent on resume.

**Reports vs findings.** Every check returns a JSON-serializable *report*
(the full picture tool UIs render) and derives *findings* —
`{IssueID, URL, Detail}` — from it. One derivation feeds both halves
(`Reporter.Findings()`; analyze re-derives from stored reports via
`sitecheck.DecodeFindings` without refetching), so a tester and a crawl can
never disagree. `sitecheck` imports neither `issues` nor `crawler` (findings
carry plain string IDs; `analyze` maps them) — that ordering is the import
cycle-breaker.

**The checks** (thresholds are code constants — published protocol limits,
not preferences): *robots.txt* (Google REP fetch semantics — 5-hop redirect
chain, 4xx ⇒ missing, 5xx ⇒ whole-site risk, 500 KiB cap; parse-level invalid
lines via `robots.File.Ignored`; blocks-all; sitemap directives); *XML
sitemaps* (discovery via robots directives ∪ `/sitemap.xml` conventions ∪
declared, index recursion, gzip-aware sizes — which unblocked the >50 MB
check — entry hygiene; robots-declared sitemaps are exempt from the
cross-host finding per sitemaps.org cross-submission); *AI-bot access* (an
embedded registry of ~16 crawlers — data, not code; robots verdicts per bot
plus optional live probes with each fetcher's real UA via `fetch.FetchWith`,
classified against a control fetch to catch edge/WAF blocks; token-only
entries are never probed; robots-ignoring fetchers carry an "only an edge
block works" note; all findings Warning — blocking can be policy); *JS render
diff* (one URL raw vs Chrome-rendered over `parse.Facts`; the full per-field
diff lives in the report, while findings are three site-level IDs of their
own — `js_dependent_content`, `js_dependent_links`,
`js_changed_robots_directives` — because issue ownership is per ID and
the per-page js_* checks are evaluate-owned); *llms.txt* (file-level
rules live here, `analyze` delegates); *structured data* and *SERP snippet
preview* (tool-only — crawls already measure these per page; serp is pure
unless given a URL).

**Crawl pass.** Gate: `site_checks.enabled: auto` (default) runs the pass iff
the crawl is a full-domain audit — spider mode, root seed, empty
`scope.include`; `always`/`never` override; `limits.max_urls` deliberately
does not gate (the checks are site-scoped and fixed-cost). One background
goroutine at crawl start, fetches serialized, completion barrier before the
crawl finishes; a failed check degrades, never fails the crawl. **One
robots.txt fetch per crawl**: `robotsMgr` retains the raw retrieval (shared
`sitecheck.FetchRobots`) and the audit reuses it; `robots.mode: ignore` still
audits (reads, never gates); custom robots overrides skip the live audit and
feed their `Sitemap:` directives to discovery. Storage: `site_checks (kind,
subject, report JSON, PRIMARY KEY(kind, subject))`, INSERT OR REPLACE —
resume re-runs idempotently. Config: `site_checks.{enabled, robots, sitemap,
ai_bots.{check, live_probe, bots, skip}, render_diff}`; everything defaults
on except `render_diff` (launches headless Chrome — a different cost class;
the desktop New Crawl form's "run all checks" toggle and
`site_checks.render_diff: true` opt in).

**Slot discipline.** `sitecheck.WithLimiter` injects the
process-wide `limiter.Limiter` into the Checker, which itself brackets every
check fetch with a global fetch slot and the render diff's Chrome render with
a render slot — never both at once (the limiter's lock-order rule: the raw
fetch completes and releases before the render acquires). One implementation,
every surface: the crawl pass injects the crawler's limiter; the desktop
Tools hub and MCP `run_tool` inject their `runner.ProcessWiring` limiter
(exposed as `mcp.Backend.ProcessLimiter`), so interactive tool runs count
against the same ceilings as the crawls they run beside; CLI `tools`
one-shots inject nothing — nothing runs beside them. robots.txt keeps its documented
serialized bypass via the robots manager's raw client.

**Surfaces** (engine-first per §0): CLI `bluesnake tools` — one command
group, so the top level stays flat regardless of tool count (`list`, then one
subcommand per registry entry with typed flags). MCP — exactly two functions:
`list_tools` (the registry with argument schemas) and `run_tool` (dispatch by
name through `sitecheck.RunTool`, strict args, report+findings JSON). Desktop
— a **Tools** nav entry opening a hub (one card per registry entry) with
per-tool sub-views, bound through a separate `ToolsApp` Wails struct
(`ListTools`/`RunTool`, the §5.9 pattern); the robots tester's editor view
runs on the same dispatcher (inline-body runs), site-check issue rows deep-
link back to the matching tool ("re-test after fix" without re-crawling).
The registry (`sitecheck.Tools()`) is the single catalogue all three
enumerate — adding a tool = one check + one registry entry + one desktop
sub-view.

### 5.11 Crawl setup sources — a site remembers its setup

Every crawl start resolves its **base config** from one of three sources, on
every surface:

1. **Last crawl setup** (the default): the frozen config of the site's most
   recent **spider** crawl. Derived at enqueue from two facts that already
   exist — the registry knows each crawl's seed, and every crawl freezes its
   effective config into its own DB at `CreateCrawl` — so per-site config
   divergence needs **no storage anywhere**: no per-member profile columns,
   no site→config table. Deleting a crawl forgets that setup; profile edits
   and deletes can never dangle (the original per-profile sketch's
   deleted-profile problem dissolves). Falls back to the app settings for a
   never-crawled site.
2. **App settings** — the saved default profile (built-in defaults when none
   is saved): `runner.LoadProfile("")`, the prior no-profile semantics.
3. **A named profile** (or, CLI-only, a config file / the pinned built-in
   defaults).

Design points:

- **Site identity** is the exact lowercased `host[:port]` of the seed —
  deliberately the same rule as the project layer's `SiteKey` (§5.9):
  scheme/path never matter, www/subdomains/ports never fold. The core keeps
  its own tiny host helper so it never imports the removable project package.
  List-mode crawls neither establish nor consume a "last" setup (their frozen
  config bakes in list-mode adjustments — depth 0, robots ignored — that
  would be wrong to inherit; and a list audit has no single site).
- **One resolution path.** `runner.ResolveBase(spec)` maps
  `queue.JobSpec.ConfigSource` (`"" | "last"`) + `Profile` to a base config
  with provenance; `FreezeSpec`/`BuildConfig` freeze through it at enqueue,
  and every preview surface (desktop `App.SetupPreview`, the CLI provenance
  line, the MCP `base_config` response field) reads through it — so what a
  user is shown and what a job runs can never disagree. Freeze-at-enqueue
  semantics are unchanged: "last" names a lookup rule resolved at enqueue,
  never a live reference; jobs already queued are immune to later crawls.
  An unreadable last-crawl config fails **loudly**, naming the crawl —
  silently sliding to an older setup would be spooky.
- **Surfaces.** Desktop: the setup card's picker gains "Last crawl setup —
  <date>" as the first option whenever the typed site has history
  (auto-selected by default; hidden in list mode), and the quick knobs are
  now **touched-only overrides initialized from the resolved base**
  (`SetupPreview`) — untouched knobs send no-override sentinels
  (`StartRequest.Rate: -1`), so the chosen base shows through exactly (this
  also fixed the latent gap where the card's fixed defaults silently stomped
  a profile's rendering mode). CLI: `--setup last|app|defaults` (§3). MCP:
  `start_crawl`'s `setup` param (`last` default for spider, `app_settings`),
  mutually exclusive with `profile`. Projects: crawl-all per-member default
  + override-all mode (§5.9); the member Crawl button needs no code — New
  Crawl prefilled resolves the member's last setup by construction, which
  also answers the "same domain from New Crawl directly" question: the setup
  belongs to the domain, so *every* start of that site preselects it,
  project page or not.
- **The CLI's bare-run stance:** a bare `bluesnake crawl` resolves last →
  app settings → built-ins, like every other surface, and always prints the
  source it resolved. Since the default is history-dependent either way,
  reproducibility is an explicit opt-in: `--setup defaults` (or
  `--config`/`--profile`) pins the base for CI.

### 5.12 Crawl queue & parallel width

Every surface starts crawls the same way: a job enqueued into the core crawl
queue (`internal/queue`), drained by a single dispatcher through the shared
executor — the interface never dictates how a crawl runs. The desktop backs
the queue with the registry DB (jobs survive restarts, a crash reconciles a
running job to interrupted with its partial crawl resumable); the CLI and
the standalone MCP server drain an in-memory store in-process.

Concurrency is **two user-facing knobs**: `speed.max_threads` ("Threads per
site" — parallel downloads within one crawl) × `speed.max_concurrent_crawls`
("Parallel crawls" — how many sites crawl at once). They bound different
resource axes: threads bound network concurrency, while each parallel crawl
carries its own fixed overhead (worker pool, SQLite handles, buffers,
frontier RAM), so the crawl count is the memory-axis bound. **0 = unlimited
and is the default** — every queued crawl starts immediately, nothing ever
waits; it matches the `<= 0` = unlimited convention of the other process
caps (`internal/limiter`), and a user who wants the memory-axis bound sets
n ≥ 1 (1 = one crawl at a time). Unlimited is a growth mode, not a giant
fixed pool: a drain loop that claims a job spawns its replacement first
(there is always a parked spare drainer), and idle loops converge back to
one when the queue empties. The width is
**live**: `Dispatcher.SetConcurrency` retargets it at any time — raising
(or going unlimited) spawns drain loops so already-queued jobs start
immediately; lowering
retires loops between jobs, never interrupting a running crawl. The desktop
applies the knob on every profile save, the MCP servers re-read it at every
start, and `projects crawl-all` resolves it at command start — no restart
anywhere, and deliberately no per-invocation override (one knob, one
meaning, every surface).

Because the width can rise at any time, every dispatcher-owning surface runs
under ONE process-wide limiter built at startup (`runner.ProcessWiring`,
returned unconditionally): the global fetch cap (`speed.max_global_threads`
— an advanced YAML-only safety valve, hidden from the settings UI; 0 = each
crawl bounded only by its own threads), one finalize pass at a time, and the
Chrome render pool (§5.8). All its caps are width-independent, so one
limiter stays valid across retargets; the executor's per-crawl fallback
limiter is sound only where the width is fixed at one — the CLI's one-shot
`crawl`/`list` commands. Capacity semantics differ by surface on purpose: an
MCP start beyond a bounded width is rejected naming the running crawls
(an agent's crawl is never silently queued behind other work; at the
unlimited default there is no capacity to exhaust, so every start is
admitted), while the desktop enqueues and shows the wait in its queue view.

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

