# acrawler Desktop App — UI/UX Design Brief

This document is written for a designer with **no knowledge of the codebase**.
It contains everything the current product can do, every setting a user can
change, every piece of data the app can show, and every action a user can
trigger. The desktop app must expose **all of it** — today the product is a
command-line tool, and the goal is full feature parity with a graphical
interface.

Reference product: this is functionally a modern re-imagining of *Screaming
Frog SEO Spider* (a well-known desktop SEO crawler — worth looking at for
domain familiarity, **not** as a visual reference; its UI is dated and dense).

---

## 1. What the product does, in one minute

A user points the app at a website. The app **crawls** it — it downloads the
home page, finds every link on it, downloads those pages, finds their links,
and so on until it has visited the whole site. While doing this it records
everything an SEO professional cares about: page titles, descriptions,
headings, status codes (200 OK / 404 broken / redirects), how pages link to
each other, security problems, duplicate content, and a hundred other checks.

The output is a **crawl** — a saved, browsable database of every page found,
every link between pages, and every problem ("issue") detected. Users browse
it, filter it, export it to spreadsheets, generate XML sitemaps from it, and
compare it against an older crawl of the same site to see what changed.

**Primary persona:** SEO specialists and web developers auditing websites —
their own, clients', or competitors'. They are technical-adjacent: comfortable
with spreadsheets and URLs, not necessarily with code. Power users will live
in this app for hours; first-run users need to get a crawl going in under a
minute.

**The golden path:** enter a URL → press Start → watch progress → browse
results & issues → export. Everything else is configuration and power
features around that path.

---

## 2. Core concepts (glossary the UI must teach)

| Concept | Meaning |
|---|---|
| **Crawl** | One run of the spider against a site. Each crawl is saved with a unique ID, has a status (running / completed / interrupted), and is permanently browsable. |
| **Project** | An optional label to group crawls (e.g. all crawls of `client-a.com`). |
| **Spider mode** | Normal crawling: start from one URL, discover everything by following links. |
| **List mode** | The user supplies a fixed list of URLs (paste, file, or an XML sitemap URL) and the app audits exactly those, without wandering off. Used for migration checks and spot audits. |
| **Internal vs external** | Internal = pages on the site being crawled (same host). External = links pointing to other sites; the app checks they're alive but doesn't crawl into them. |
| **Indexability** | Whether Google could index a page. Every page is *Indexable* or *Non-Indexable* with a reason: Blocked by Robots.txt, No Response, Client Error, Server Error, Redirected, Noindex, or Canonicalised. |
| **Issue** | One detected problem on one URL. Every issue type has a **severity** — `issue` (definitely a problem), `warning` (needs review), `opportunity` (improvement potential) — and a **priority** (high / medium / low). |
| **Crawl analysis** | A second computation pass after crawling finishes (automatic by default): link scores, redirect chains, duplicate content, hreflang reciprocity, sitemap comparisons. Re-runnable without recrawling. |
| **robots.txt** | A file websites use to tell crawlers what not to visit. The app obeys it by default (like Google does), and reports which rule blocked a URL. |
| **Pause / resume** | A crawl can be interrupted at any time (or crash) and later resumed from exactly where it stopped — nothing is re-downloaded. |
| **Compare** | Diff two crawls of the same site: which pages and issues appeared, disappeared, or changed. |
| **JavaScript rendering** | Optional mode that loads each page in an embedded Chrome browser so content injected by JavaScript is seen — slower but matches what Google sees on JS-heavy sites. Requires Chrome installed on the machine. |

---

## 3. Top-level information architecture (suggested, not prescriptive)

The app needs surfaces for these areas; how they map to navigation is the
designer's call:

1. **New Crawl** — URL entry, mode choice (spider / list), configuration, Start.
2. **Crawl progress** — live view while running, with Pause/Stop.
3. **Crawl results** — the heart of the app: data tables ("tabs"), per-URL
   detail, issues browser. One results workspace per crawl.
4. **Crawl manager** — all stored crawls: status, project, date, page counts;
   open / resume / compare / delete.
5. **Compare view** — pick two crawls, see deltas.
6. **Exports & reports** — from any crawl: spreadsheets, reports, XML sitemaps.
7. **Settings / configuration profiles** — the full settings tree (section 5),
   savable as named profiles, attachable to a crawl.
8. **Tools** — robots.txt tester.

---

## 4. Every action the user can trigger

These all exist today as CLI commands; each needs a UI affordance.

| Action | What it does | Inputs | Outputs / states |
|---|---|---|---|
| **Start spider crawl** | Crawl a site from a start URL | URL, configuration (section 5), optional project name | A running crawl; live progress |
| **Start list audit** | Audit a fixed URL set | URL list via: paste, text/CSV file, or a sitemap URL; option "follow redirect chains to the end" | Same as crawl |
| **Pause / stop crawl** | Gracefully interrupt; everything so far is saved | — | Crawl marked *interrupted*, resumable |
| **Resume crawl** | Continue an interrupted crawl from its saved queue | The crawl; (advanced: override the saved settings — requires explicit confirmation, default is to reuse the exact settings the crawl started with) | Crawl continues; no URL fetched twice |
| **Re-run analysis** | Recompute link scores, chains, duplicates, hreflang, sitemap checks on an existing crawl (e.g. after changing the duplicate-similarity threshold) | The crawl | Updated metrics + issues |
| **Re-run issue evaluation** | Recompute all issue checks | The crawl | Updated issues |
| **Browse results** | Tables of pages, links, issues (section 6) | Filters | — |
| **Export dataset** | Any results table → file | Dataset name, optional issue filter, format: CSV / JSON / JSONL / XLSX, destination | File written |
| **Generate report** | Named summary reports (section 7) | Report name, format, destination | File written |
| **Generate XML sitemap** | Build `sitemap.xml` from the crawl (indexable HTML pages only; auto-splits with an index file above 49,999 URLs) | Include last-modified dates yes/no, destination folder | One or more XML files |
| **Compare crawls** | Diff two crawls | Previous crawl, current crawl, optional URL-mapping rules (regex find→replace applied to old URLs so a renamed site still lines up) | Comparison view + exportable file |
| **Manage crawls** | List / open / delete stored crawls | — | Delete needs confirmation (permanent) |
| **Test robots.txt** | Paste/load a robots.txt and test URLs against it | robots.txt content, a user-agent token, URLs | Per-URL verdict: ALLOWED, or BLOCKED with the exact rule line that matched |
| **Manage configuration profiles** | Create, edit, validate, save, load full settings profiles | — | Profiles are plain files a user can also share |

**Background behaviours the UI should surface:** after every crawl, issue
evaluation and analysis run automatically (configurable off). Crawls
auto-save continuously — there is no "Save" button and the app cannot lose a
crawl, even on crash; the design should communicate this safety.

---

## 5. The complete settings inventory

Every setting below must be reachable in the UI. They are grouped exactly as
the product groups them. Each entry: **Label** — control type — default —
explanation. "Pages count" style hints describe typical interactions, not
mandates.

Settings live in **profiles**: a user configures once, saves as a named
profile, and picks a profile when starting a crawl. The settings used are
**frozen into each crawl** — viewing an old crawl should show the settings it
ran with.

> Design challenge to embrace: this is ~120 settings. Nearly all users touch
> fewer than ten (URL, depth, speed, include/exclude). Progressive disclosure
> is essential — sensible defaults everywhere, an "everything" view for power
> users, search within settings, and per-setting reset-to-default.

### 5.1 Crawl scope

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Crawl all subdomains | toggle | off | Treat `blog.site.com`, `shop.site.com` etc. as part of the site. (Automatically on when the start URL is a bare domain like `site.com`.) |
| Crawl outside start folder | toggle | off | If the user starts at `site.com/blog/`, normally only `/blog/...` is explored. This lifts that restriction. |
| Check links outside start folder | toggle | on | With the folder restriction active, still fetch pages outside the folder once (to verify they work) without exploring further from them. |
| Follow internal "nofollow" links | toggle | off | Follow links marked `nofollow`/`sponsored`/`ugc` on the user's own site. |
| Follow external "nofollow" links | toggle | off | Same, for links to other sites. |
| Crawl invalid links | toggle | off | Attempt links that look malformed (typo'd protocols etc.) and report them as errors instead of silently skipping. |
| CDN domains | editable list of domains (optionally with a path, e.g. `cdn.example.net/assets/`) | empty | Extra domains to treat as part of the site (asset CDNs). |
| Include patterns | editable list of regex patterns | empty | If any are set, **only** URLs matching at least one are crawled. |
| Exclude patterns | editable list of regex patterns | empty | URLs matching any are never even requested. Exclude beats include. A live "test a URL against these patterns" helper would be valuable. |

### 5.2 What to store & crawl, per link type

For each of the following resource/link types there are **two independent
toggles**: *Store* (keep it in the results) and *Crawl* (actually request it).
A compact two-column matrix works well.

| Type | Store default | Crawl default |
|---|---|---|
| Images | on | on |
| Media (video/audio) | on | on |
| CSS | on | on |
| JavaScript files | on | on |
| SWF (legacy Flash) | on | on |
| Internal hyperlinks | on | on |
| External links | on | on |
| Canonicals | on | on |
| Pagination (rel next/prev) | off | off |
| Hreflang (language alternates) | on | off |
| AMP pages | off | off |
| Meta refresh targets | on | on |
| iframes | on | on |
| Mobile alternate links | off | off |
| Uncrawlable links (e.g. `javascript:` links, `href` on non-link elements) | off (store only — never crawled) | — |

### 5.3 XML sitemaps (as a discovery source)

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Crawl linked XML sitemaps | toggle | off | Also read the site's sitemap files and crawl the URLs listed in them (this is what enables "orphan page" detection). |
| Auto-discover sitemaps via robots.txt | toggle (child of above) | off | Find sitemap locations declared in robots.txt. |
| Sitemap URLs | editable list (child) | empty | Explicit sitemap addresses to read. |

### 5.4 Extraction (what data is captured per page)

Page details — all toggles, all **on** by default: Page titles, Meta
descriptions, Meta keywords, H1, H2, Indexability, Word count, Readability,
Text-to-code ratio, Page hash (powers exact-duplicate detection), Page size,
Forms.

URL details — toggles: Response time (on), Last-modified (on), HTTP headers
(on), Cookies (off).

Directives — toggles, on: Meta robots, X-Robots-Tag.

Structured data — toggles, all **off** by default: JSON-LD, Microdata, RDFa,
Schema.org validation, Google rich-results validation, Case-sensitive
validation. (The three formats are the masters; the validation toggles only
make sense when at least one format is on — disable/grey accordingly.)

HTML storage — toggles, off: Store raw HTML (saves every page's source to
disk for later viewing/export), Store rendered HTML *(future — accepted but
not yet functional)*, PDF storage and PDF property extraction *(future)*.

### 5.5 Limits

All numeric fields; "−1" / blank means *unlimited* — the UI should present
this as an "unlimited" state, not a magic number.

| Setting | Default | Meaning |
|---|---|---|
| Max URLs to crawl | 5,000,000 | Hard stop for the whole crawl. |
| Max crawl depth | unlimited | How many clicks away from the start URL to go. |
| Max URLs per depth level | unlimited | Cap per level. |
| Max folder depth | unlimited | How many subdirectories deep (`/a/b/c/`). |
| Max query-string parameters | unlimited | Skip URLs with more than N `?a=1&b=2` parameters. |
| Max URLs per subdomain | unlimited | Per-subdomain cap. |
| Max redirects to follow | 5 | Length of redirect chains to chase. |
| Max URL length | 10,000 chars | Skip absurdly long URLs. |
| Max links per page | 10,000 | Only the first N links on a page are processed. |
| Max page size | 50 MB | Bigger downloads are abandoned and the page is marked "skipped: too large". |
| Per-path limits | editable list of (URL pattern → max pages) | empty | e.g. crawl at most 100 pages under `/blog/`. |

### 5.6 Rendering (JavaScript)

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Rendering mode | choice: **Text only** / **JavaScript** | Text only | JavaScript mode loads every page in headless Chrome. Significantly slower; needed for JS-heavy sites. **Requires Chrome/Chromium installed** — the UI must detect availability and explain when missing. |
| AJAX timeout | seconds | 5 | How long scripts get to run before the page is snapshotted. |
| Window preset / size | preset ("Googlebot desktop", 1024×768) or custom width × height | preset | Viewport for rendering. |
| Capture screenshots | toggle | off | *(captured today, persistence in results is future)* |
| Report JavaScript console errors | toggle | off | Records JS errors per page as issues. |
| Flatten Shadow DOM / Flatten iframes | toggles | on | *(accepted, not yet functional — keep visible but marked)* |
| Chrome path | file picker | auto-detect | Manual override when Chrome isn't found automatically. |

### 5.7 Advanced crawl behaviour

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Cookie storage | choice: session / persistent / none | session | How cookies are handled across requests. |
| Ignore non-indexable URLs for issues | toggle | off | Don't flag content issues (titles, duplicates…) on pages Google wouldn't index anyway. |
| Ignore paginated URLs for duplicate checks | toggle | off | |
| Always follow redirects | toggle | off | (List mode) follow redirect chains to their final destination regardless of depth — the migration-audit switch. |
| Always follow canonicals | toggle | off | Same idea for canonical chains. |
| Respect noindex / canonical / next-prev | three toggles | off | Suppress reporting of pages excluded by these signals. |
| Respect HSTS | toggle | on | After a site says "HTTPS only", later `http://` URLs are treated as auto-upgraded (shown as a special 307). |
| Respect self-referencing meta refresh | toggle | on | A page that meta-refreshes to itself counts as non-indexable. |
| Extract images from srcset | toggle | off | Also pick up responsive-image variants. |
| Crawl fragment identifiers | toggle | off | Treat `page#section` as distinct from `page`. |
| Assume pages are HTML | toggle | off | When a server doesn't say what a file is, treat it as a web page. |
| Response timeout | seconds | 20 | |
| Retries on 5xx errors | number | 0 | Re-request server errors N times. |
| Percent-encoding case | choice: upper / lower | upper | Nerd knob for servers sensitive to `%C3` vs `%c3`. |

### 5.8 Thresholds ("what counts as a problem")

These numbers drive the issue checks. Grouped editor with reset-to-default.

| Threshold | Default |
|---|---|
| Page title length | min 30 / max 60 characters (pixel-width limits 200–561 exist in config for future use) |
| Meta description length | min 70 / max 155 characters (pixels 400–985, future) |
| Max URL length (for the "URL too long" flag) | 115 characters |
| Max H1 / H2 length | 70 characters each |
| Max image alt-text length | 100 characters |
| Max image file size | 100 KB |
| Low-content word count | 200 words |
| High crawl depth | 4 clicks |
| High internal outlinks per page | 1,000 |
| High external outlinks per page | 100 |
| Non-descriptive anchor texts | editable word list (defaults: "click here", "read more", "learn more", …) |
| Soft-404 phrases | editable list (defaults: "page not found", "404", …) — 200-OK pages containing these get flagged |

### 5.9 Content analysis

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Content area — exclude elements / classes / IDs | three editable lists | elements: `nav`, `footer` | Which parts of a page count as "content" for word counts and duplicate detection. |
| Content area — include elements / classes / IDs | three editable lists | empty | If set, *only* these regions count. |
| Near-duplicate detection | toggle | off | Find pages whose text is nearly identical. |
| Similarity threshold | percent slider | 90% | How similar counts as "near duplicate". Changing it only requires re-running analysis, not recrawling — surface that. |
| Only check indexable pages | toggle | on | |

### 5.10 robots.txt

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Mode | choice: **Respect** / **Ignore** / **Ignore but report** | Respect | Ignore = don't even download robots.txt. Ignore-but-report = download and show it, but crawl everything anyway. |
| Show blocked internal URLs | toggle | on | Blocked pages appear in results (marked, never fetched). |
| Show blocked external URLs | toggle | off | |
| Custom robots.txt | per-host list: hostname + a robots.txt file/text | empty | Test "what if robots.txt said this instead" — overrides the live file for the crawl only. Pairs naturally with the robots tester tool. |

### 5.11 URL rewriting

Applied to discovered URLs before crawling (never to the start URL).

| Setting | Control | Default |
|---|---|---|
| Remove query parameters | editable list of parameter names (e.g. `utm_source`, `sessionid`) | empty |
| Regex replacements | ordered list of (pattern → replacement) pairs; order matters; drag-to-reorder | empty |
| Lowercase all URLs | toggle | off |

A "try a URL" preview box showing before→after would prevent many mistakes.

### 5.12 Speed

| Setting | Control | Default | Meaning |
|---|---|---|---|
| Max threads | number | 5 | Parallel downloads. |
| Max URLs per second | decimal, 0 = unlimited | 0 | Politeness throttle — the recommended way to go easy on a server. |

### 5.13 HTTP / identity / authentication

| Setting | Control | Default | Meaning |
|---|---|---|---|
| User-agent | text (with presets worth designing: acrawler default, Googlebot, Chrome…) | `acrawler/1.0 (+github.com/hhsecond/acrawler)` | What the crawler calls itself to servers. |
| Robots user-agent token | text | `acrawler` | The name used when matching robots.txt rules (separate on purpose). |
| Custom HTTP headers | name/value list | empty | e.g. `Accept-Language: de`. |
| Proxy | text (`http://user:pass@host:port`) | empty | |
| Trusted certificate folders | list of folders | empty | For sites with private/internal TLS certificates. |
| Basic auth credentials | list of (URL prefix, username, password **or** environment-variable name for the password) | empty | Password-protected sites. Longest matching prefix wins. |
| Auth cookies | list of (name, value, optional domain) | empty | "Bring your own session" for sites with login forms — the user logs in with a browser and pastes the session cookie. |

### 5.14 Custom search

Up to 100 user-defined searches, each producing a column in the results.
Per search: **name**, **mode** (contains / does not contain), **pattern**,
**treat as regex** toggle, **scope** (whole HTML / visible text / a specific
element by CSS selector). "Contains" reports a match count per page; "does
not contain" reports true/false.

### 5.15 Custom extraction

Up to 100 user-defined extractors, each producing a column. Per extractor:
**name**, **type** (XPath / CSS selector / Regex), **expression**, optional
**attribute** (CSS type: pull an attribute instead of text), **return** (text
/ outer HTML / inner HTML / function value — XPath functions like `count(…)`).
Multiple matches on a page are joined with a separator. A "test against a
URL" preview is highly recommended; expression errors are caught at
configuration time and must be shown inline.

*(Custom JavaScript snippets also exist in the configuration schema — name,
extraction-vs-action type, script file, timeout, content-type filter — but
execution is a future feature. Design the slot; mark it "coming soon".)*

### 5.16 Link positions

Ordered rules classifying where on a page each link lives (used in link
data): (position name ← path fragment). Defaults: head, nav, header, sidebar
(`aside`), footer, and a final catch-all "content". Editable + reorderable;
the catch-all must stay last. Plus a master toggle "store link paths"
(default on).

### 5.17 List-mode behaviour

| Setting | Default | Meaning |
|---|---|---|
| Respect robots.txt in list mode | off | List audits ignore robots by default (the user explicitly chose these URLs). |
| List-mode crawl depth | 0 | 0 = only the listed URLs. Raise to also crawl what they link to. |

### 5.18 Analysis

Master toggle **Auto-analyse after crawl** (default on) plus per-analysis
toggles, all on: Link score, Redirect chains, Near-duplicates, Pagination,
Hreflang, Canonicals, Link metrics, Sitemaps.

### 5.19 Storage

| Setting | Default | Meaning |
|---|---|---|
| Storage folder | `~/.acrawler` | Where crawls live on disk. |
| Crawl retention | keep forever | Auto-delete crawls older than N days (0 = never). |

### 5.20 Compare

| Setting | Default | Meaning |
|---|---|---|
| Change-detection elements | titles, descriptions, H1, word count, crawl depth, links, structured data, content | Which per-page elements the comparison diffs. |
| Content-change threshold | 10% | |
| URL mapping rules | ordered regex (pattern → replacement) list | Align old URLs to new ones when a site was restructured (e.g. `staging.` → `www.`). With a test box. |

---

## 6. The results workspace (what the user browses after a crawl)

### 6.1 Per-page data (the master record)

Every crawled URL has: URL · internal/external · state (crawled / blocked by
robots [with the matching rule line number] / error [with the network error] /
skipped-too-large) · HTTP status code & text · content type · crawl depth ·
response time · size · redirect target & type (server redirect / HSTS upgrade
/ meta refresh) · indexability + reason · inlink count · unique inlinks ·
unique outlinks · link score (0–100, PageRank-like) · who discovered it first
· word count · readability score · text ratio · page hash · near-duplicate
similarity % & closest match · full response headers · language ·
outside-start-folder flag.

Plus, for HTML pages: titles (all of them + counts; first two matter most),
meta descriptions, meta keywords, H1s, H2s, heading order, meta robots,
X-Robots-Tag, meta refresh, canonicals (from HTML and from HTTP headers),
rel next/prev, hreflang entries (language → URL), AMP links, base href,
detected forms, head-validity problems, structured-data summary (formats,
types, validation errors/warnings), JavaScript-rendering diff (see 6.4),
custom search/extraction values.

### 6.2 Result tables ("tabs")

Thirteen exportable datasets; these are the natural tabs/views:

| Dataset | One row per | Key columns |
|---|---|---|
| Internal | internal URL | the master-record columns above |
| External | external URL | URL, status, who links to it |
| Response codes | URL | state, status, redirect target/type, error text |
| Page titles | HTML page | title, length, count |
| Meta descriptions | HTML page | description, length, count |
| H1 | HTML page | first H1, length, count |
| Canonicals | HTML page | canonical URL, count, indexability |
| Hreflang | annotation | page → language → target URL |
| Images | image file | type, size, references |
| Security | internal page | scheme, status, indexability |
| Links | individual link (page→page edge) | source, destination, type (hyperlink/image/css/canonical/…), anchor text, alt text, nofollow, rel, target, how the href was written, position on page (nav/content/footer…), origin (in raw HTML vs only after JavaScript ran) |
| Issues | issue occurrence | URL, issue name, severity, priority, category, detail |
| Custom | custom search/extraction result | URL, name, value |

Any table can be filtered to "only URLs affected by issue X" — that filter is
a first-class concept (it's also how exports are filtered).

Expect **tens of thousands to millions of rows**: tables need virtual
scrolling, column sorting, quick text filter, column show/hide, and copy.

### 6.3 Issues browser

The complete catalogue (~100 checks) grouped by category. For each issue
type: name, severity (issue / warning / opportunity), priority (high / medium
/ low), affected-URL count, and drill-down to the URL list (and from a URL,
back to all its issues). Severity needs instant visual encoding; "0 issues"
states matter as positive feedback.

Full catalogue by category — *(severity, priority)*:

- **Response codes:** Internal No Response (issue, high) · Internal 4xx
  (issue, high) · Internal 5xx (issue, high) · Internal Blocked by robots.txt
  (warning, high) · Internal Redirect 3xx (warning, low) · Internal Meta
  Refresh Redirect (warning, low) · External No Response / 4xx / 5xx
  (warnings, low) · Redirect Chain (warning, medium) · Redirect Loop (issue,
  high)
- **Security:** HTTP page (issue, high) · Mixed Content (issue, high) ·
  Insecure Form Action (issue, high) · Form on HTTP Page (issue, high) ·
  Unsafe Cross-Origin Link (warning, low) · Protocol-Relative Resource
  (warning, low) · Missing HSTS / CSP / X-Content-Type-Options /
  X-Frame-Options / Referrer-Policy headers (warnings, low)
- **URL:** Uppercase · Underscores · Non-ASCII · Parameters · GA tracking
  parameters · Repetitive path (warnings, low) · Multiple slashes · Contains
  space (issues, low) · Over 115 characters (opportunity, low)
- **Page titles:** Missing (issue, high) · Multiple (issue, high) · Outside
  head (issue, high) · Duplicate / Too long / Too short (opportunities,
  medium) · Same as H1 (opportunity, low)
- **Meta description:** Multiple / Outside head (issues, medium) · Missing /
  Duplicate / Too long / Too short (opportunities, low)
- **Meta keywords:** Multiple (warning, low)
- **H1:** Missing (issue, medium) · Multiple (warning, medium) ·
  Non-sequential (warning, low) · Duplicate / Too long (opportunities, low)
- **H2:** Missing / Multiple (warnings, low) · Too long (opportunity, low)
- **Content:** Exact Duplicates (issue, high) · Near Duplicates (warning,
  medium) · Soft 404 (warning, high) · Lorem Ipsum placeholder (warning,
  high) · Low word count (opportunity, medium) · Readability difficult / very
  difficult (opportunities, low)
- **Images:** Missing alt text (issue, low) · Alt text too long (opportunity,
  low) · File too large (opportunity, medium)
- **Canonicals:** Multiple Conflicting (issue, high) · Points to
  non-indexable page (issue, high) · Outside head (issue, high) · Canonical
  Chain (warning, medium) · Canonicalised (warning, high) · Missing (warning,
  medium) · Multiple / Relative (warnings, low)
- **Directives:** Noindex / Nofollow / None (warnings, high)
- **Links:** Outlinks to localhost (issue, high) · No internal outlinks
  (warning, high) · Nofollow internal outlinks (warning, low) · High
  internal/external outlink counts (warnings, low) · High crawl depth
  (opportunity, medium) · No anchor text / Non-descriptive anchor text
  (opportunities, low)
- **Hreflang:** Broken target (issue, high) · Missing return link (issue,
  high) · Invalid language code (issue, high) · Missing self-reference /
  Missing x-default (warnings, low)
- **Pagination:** Broken pagination target (issue, high) · Sequence error
  (issue, low)
- **Sitemaps:** Orphan URL — in sitemap but not linked (issue, medium) ·
  Non-indexable URL in sitemap (issue, medium) · Indexable page missing from
  sitemap (issue, medium) · In multiple sitemaps (warning, low)
- **Structured data:** Parse error (issue, high) · Validation error — missing
  required property (issue, high) · Validation warning — missing recommended
  property (opportunity, low)
- **JavaScript** (rendering mode only): Noindex only in raw HTML (issue,
  high) · Canonical changed by JS (issue, high) · Title updated by JS / H1
  updated by JS / Contains JS-only links (warnings, medium) · Console errors
  (warning, low)
- **HTML validation:** Missing/multiple head or body (issues, high) ·
  Body before html / Head not first / Invalid elements in head (warnings,
  high) · Document over 2 MB (issue, high)
- **AMP:** Missing canonical / viewport / charset / AMP script (issues, high)
  · Missing return link from AMP page (issue, high) · AMP page indexable
  (warning, high)

### 6.4 Per-URL detail (drill-down)

Selecting a URL should offer: full record (6.1), its inlinks and outlinks
(rows from the Links dataset), its issues, raw response headers, stored page
source (when "store HTML" was on), structured-data detail, and — in rendering
mode — the JavaScript diff: rendered vs raw word count, what JS changed
(title, H1, canonical, removed noindex), JS-only link count, console errors.

### 6.5 Crawl progress (live view)

Today's CLI shows an end-of-crawl summary; the app should show it live:
pages crawled, queue size, pages/second, elapsed time, status-code breakdown
(2xx/3xx/4xx/5xx/blocked/no-response), indexable vs non-indexable, and a
live error feed. Controls: Pause (graceful — everything saved, resumable) and
the reassurance that closing the app mid-crawl loses nothing. On finish:
summary + issue totals + a clear path to the results workspace. Interrupted
crawls must advertise **Resume** prominently in the crawl manager.

---

## 7. Reports & exports

**Reports** (each is a generated table, same format choices as exports):

| Report | Contents |
|---|---|
| Crawl overview | Every metric counted: totals, per-status, per-state, indexability, every issue with its count |
| Redirect chains | Source → every hop → final destination + status, loop flag |
| Canonical chains | Same shape for canonical hops |
| Insecure content | All security findings with details |
| Orphan pages | Pages found only via sitemaps |

**Export destinations & formats:** CSV, JSON, JSONL, Excel (XLSX); to file.
Any dataset, any report, sitemap files. The "filter by issue" option applies
to dataset exports.

---

## 8. States, errors, and edge cases the design must handle

- **Crawl states:** running → completed / interrupted (resumable). Deleted is
  permanent (confirm).
- **Settings validation:** every profile is validated before a crawl starts;
  errors are specific and name the exact setting (e.g. *"speed.max_threads:
  must be ≥ 1"*, *"scope.exclude[0]: invalid regex"*). Show inline at the
  offending field, not as a generic alert.
- **Chrome missing** (JavaScript mode): detect up front; offer Text-only
  fallback and a path picker.
- **Network problems are data, not app errors:** a site being down produces
  result rows ("No Response"), not failure dialogs. The app itself failing
  (disk full, storage folder unwritable) is a real error.
- **Resume with changed settings** is refused by default; an explicit
  override ("force") exists and deserves a strong confirmation explaining the
  results may become inconsistent.
- **Empty states:** no crawls yet (first-run), crawl with zero issues
  (celebrate), filter with no matches, comparison of identical crawls.
- **Scale:** a crawl can be 5 pages or 5 million. Progress, tables, and
  exports must communicate scale (row counts, export size hints).
- **Politeness:** crawling hammers other people's servers; the speed settings
  exist for ethics as much as performance. Consider surfacing rate in the
  start flow rather than burying it.

---

## 9. Explicitly out of scope for now (leave room, don't design flows)

Future features with config slots or known plans — the design can reserve
space but shouldn't build full flows: custom JavaScript snippet execution ·
rendered-HTML & screenshot persistence · pixel-width title/description checks
· spelling & grammar · accessibility audits · WARC site archiving · crawl
scheduling · per-URL "crawl path" report · visualisations (crawl tree /
force-directed graphs) · segments (user-defined colored groupings) ·
Screaming Frog's third-party integrations (Analytics, Search Console,
PageSpeed) — these are permanently out of scope.

---

## 10. Quick domain primer for the designer

- **Status codes:** 2xx = fine, 3xx = redirect, 4xx = broken (the classic
  404), 5xx = server failure. SEO people obsess over 404s and redirect
  chains.
- **Canonical:** a page's way of saying "the real version of me is over
  there". Pages that canonical elsewhere usually don't rank themselves.
- **Noindex:** a page's way of saying "don't put me in Google".
- **Hreflang:** annotations linking language versions of a page
  (`en` ↔ `de` ↔ `fr`). They must point at each other reciprocally — broken
  reciprocity is a top international-SEO bug, and this tool checks it.
- **Orphan page:** exists (it's in the sitemap) but nothing links to it, so
  users and Google rarely find it.
- **Link score:** 0–100 measure of how well a page is linked internally
  (same family of math as Google's original PageRank).
- **Anchor text:** the clickable words of a link. "Click here" tells Google
  nothing; descriptive anchors are better — hence the related checks.
