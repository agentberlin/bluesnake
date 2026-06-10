# Screaming Frog SEO Spider — Operational Capability Inventory

Sources: official user guide (General, Configuration), official tutorials (How To Compare Crawls, How To Use List Mode, Robots.txt Tester).

---

## 1. Storage Modes

### Database Storage Mode (default)
- Crawl data written to disk (hybrid engine: disk + RAM working set). Enables crawling millions of URLs.
- **Crawls are automatically saved and committed to the database during the crawl** — clearing a crawl, typing a new URL, or shutting down auto-stores the crawl. Survives crash/power cut.
- Crawls open via a crawl manager (fast open, no load step), with project-folder organization, rename, duplicate, export, bulk delete.
- SSD strongly recommended. Rough capacity: SSD + 4 GB RAM ≈ 2 million URLs. Default soft crawl limit 5M URLs.
- Database location configurable.
- Required for crawl comparison.

### Memory Storage Mode (legacy)
- All crawl data in RAM. Manual save; no auto-save/crash recovery.
- On memory ceiling: "high memory usage" warning; save crawl, switch to DB storage, then **open and resume** the crawl.

---

## 2. Saving / Loading / Resuming Crawls ("Partial Crawling")

- **Pause/resume mid-crawl**: pause at any point; resume later. In DB mode the crawl is continuously committed; reopening allows resuming where it stopped.
- **Crash recovery**: DB mode — automatic.
- **File formats**: portable crawl file (openable in both storage modes, slower) and raw database export (faster, DB-mode only).
- Each crawl in DB mode has a **database crawl ID**, usable from the CLI (`--list-crawls`, `--load-crawl <id>`, compare by IDs).

---

## 3. Modes

### Spider mode (default)
- Crawls the entered subdomain, treating other subdomains as external. Scope: Subdomain / All Subdomains / Subfolder.
- Subfolder crawl requires trailing slash; combinable with subdomain (`http://de.example.com/uk/`).

### List mode
- Input methods: file, manual entry, paste, **download XML sitemap** (from URL or file, incl. sitemap indexes). Accepted files: .txt, .csv, .xls, .xlsx, .xml — scans for `http(s)://`-prefixed strings.
- Normalization on upload: fragments stripped, duplicates de-duped; reports submitted vs unique counts.
- Default: crawl depth 0 (only uploaded URLs). Unchecking the depth limit crawls list + everything linked. Granular auditing via store/crawl link-type toggles.
- **Always Follow Redirects** / **Always Follow Canonicals**: follow chains to final target ignoring depth, bounded by Max Redirects — for migration auditing with the "All Redirects" report.
- Export preserves original upload order with `Original URL` and `Address` columns.
- Ignores robots.txt by default.

### SERP mode
- No crawling. Upload URL + title + description rows; compute character counts and pixel widths; preview/edit snippets; export.

### Compare mode — see §7.

---

## 4. Scheduling

- One-off or recurring scheduled crawls. Per task: name, project folder, date/interval; mode (Spider/List), start URL or list, saved config profile, auth config, headless toggle; export settings (output folder, timestamped vs overwrite, format, chosen tab/filter exports + bulk exports + reports, sitemap creation); email notification on completion.
- Each scheduled crawl runs as a new instance; overlapping schedules run concurrently.
- Task history with end time and error column.
- For the Go CLI: scheduling delegated to cron/systemd timers + a fully scriptable CLI; config profiles make this work.

---

## 5. Command Line Interface (Screaming Frog reference)

| Option | Arguments | Description |
|---|---|---|
| `--crawl` | URL | Start crawling the URL (Spider mode). |
| `--crawl-list` | list file | Crawl URLs in List mode. |
| `--load-crawl` | crawl file \| database crawl ID | Load a saved crawl. |
| `--list-crawls` | — | Print database crawl IDs. |
| `--crawl-comparison` | id1 id2 | Compare two crawls (crawl date determines current vs previous). |
| `--project-crawl-comparison` | "true" | Auto-compare last two crawls sharing a project name. |
| `--config` | config file | Saved configuration file (escape hatch for every setting lacking a flag). |
| `--auth-config` | auth file | Saved authentication configuration. |
| `--headless` | — | Run without UI. |
| `--save-crawl` | — | Save the completed crawl. |
| `--output-folder` | path | Where exports go (default cwd). |
| `--overwrite` | — | Overwrite files in output dir. |
| `--project-name` / `--task-name` | name | DB-mode organization. |
| `--timestamped-output` | — | Timestamped folder per run. |
| `--export-tabs` | "tab:filter,..." | Tab+filter exports (names match UI labels; `X` literal for preference-driven filters). |
| `--bulk-export` | "submenu:export,..." | Bulk export menu names. |
| `--save-report` | "submenu:report,..." | Report menu names. |
| `--create-sitemap` / `--create-images-sitemap` | — | Build XML sitemap(s) from completed crawl. |
| `--export-format` | csv \| xls \| xlsx | Export format. |
| `--email-on-complete` | email | Notify on completion. |
| `--help` | [export-tabs\|bulk-export\|save-report] | Scoped help enumerating legal export names. |

Headless quirks worth avoiding in our design: exports fail unless output dir exists and is empty or `--timestamped-output` used; most config only reachable via opaque binary `.seospiderconfig` files (the thing we're explicitly fixing with plain-text config).

---

## 6. Exporting

- **Formats**: CSV, xlsx (and xls legacy).
- **Three export surfaces**: tab export (respects active filter), per-URL drill-down exports (inlinks/outlinks/etc.), Bulk Export menu.
- **Multi-Export / Presets**: any combination of tabs, bulk exports, reports in one action; saveable as named presets reusable in scheduling and CLI.
- **Non-spreadsheet exports**: screenshots, archived website, page source, page text, PDFs, XML sitemaps, saved crawls.

---

## 7. Crawl Comparison (Compare mode)

- Requires database storage. Select two crawls; crawl dates determine current vs previous.
- **What's compared**: every tab/filter count with change deltas; site structure directory tree with added/missing URL counts per directory; per-URL side-by-side details.
- **Filter change semantics** (per tab/filter): **Added** = in both crawls, newly entered the filter; **New** = only in current crawl and in filter; **Removed** = in both crawls, left the filter; **Missing** = in previous crawl's filter, absent from current crawl.
- **Change Detection** (opt-in per element): page titles, meta descriptions, H1s, word count, crawl depth, internal linking metrics, structured data, page content (minhash similarity, >10% change threshold, requires stored HTML). Produces a Change Detection view with per-element filters.
- **URL Mapping**: regex map of previous URLs → current URLs so different structures compare as the same page (staging vs production, path migrations); with pattern testing.
- Comparison results exportable; comparison stored as its own small artifact.

---

## 8. Robots.txt, Redirect Chains, AMP, Sitemap Crawling

### Robots.txt compliance & tester
- Obeys robots.txt like Google: UA precedence own-token → `Googlebot` → `*`; only one UA group honored; longest-match wins, allow wins ties; wildcard `*` and `$` end-anchor supported (Google spec).
- Modes: Respect (default), Ignore (file not downloaded), Ignore but report status.
- Blocked URLs surface with a **Matched Robots.txt Line** column (line number + path); inlinks to blocked URLs exportable. Blocked pages never fetched.
- **Custom robots.txt tester**: per-subdomain override robots files used for the crawl; bulk URL testing; never touches the live site.

### Redirect chains
- Always Follow Redirects + Always Follow Canonicals (list mode, ignore depth, capped by Max Redirects). Chain reporting via Redirect Chains / Redirect & Canonical Chains reports.

### XML sitemap crawling
- Off by default in Spider mode; enable + auto-discover via robots.txt or explicit list. Sitemap filters need post-crawl analysis. List mode supports sitemap download as upload source.

---

## 9. Limits & Performance

- Threads default 5; optional max URLs/second throttle (preferred rate control).
- Limits: total URLs, depth, folder depth, URLs/depth, query strings, max redirects, URL length (10k), links/page (10k), page size (50MB), per-subdomain.
- DB mode + SSD for big crawls; continuous commit = crash safety.

### Implications for the Go re-implementation
- SF's CLI is not self-describing (binary config files) — we fix this with plain-text config (full schema) + flags.
- Export naming is label-coupled with `--help` vocabularies — replicate as discoverable subcommand help / `list` subcommands.
- DB-mode invariants worth copying: continuous commit (crash-safe), crawl IDs, project/task naming, list-crawls, compare-by-ID, timestamped-vs-overwrite output contract.
