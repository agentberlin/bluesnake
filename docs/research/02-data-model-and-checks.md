# Screaming Frog SEO Spider — Data & Checks Inventory

Scope: native crawler features only. **Excluded as third-party API integrations:** Analytics (GA4), Search Console, PageSpeed (PSI/Lighthouse), Mobile (Lighthouse-based), Link Metrics (Majestic/Ahrefs/Moz), AI (embeddings/semantic search).

Sources: [Tabs user guide](https://www.screamingfrog.co.uk/seo-spider/user-guide/tabs/), [Issues library](https://www.screamingfrog.co.uk/seo-spider/issues/), [Configuration > Crawl Analysis](https://www.screamingfrog.co.uk/seo-spider/user-guide/configuration/), [General](https://www.screamingfrog.co.uk/seo-spider/user-guide/general/).

---

## 1. Core per-URL data model (Internal tab = superset of most tabs)

Every crawled URL stores:

**Identity / response**
- Address (URL); URL Encoded Address (the percent-encoded URL actually requested, RFC 3986)
- Content (content type); Status Code; Status (HTTP reason phrase)
- HTTP Version (HTTP/1.1 default; HTTP/2 only in JS rendering mode if server supports)
- Response Time (seconds to download); Last-Modified (from response header, empty if absent)
- Hash (MD5 of page — exact-duplicate detection)
- Size (bytes; from Content-Length, updated to uncompressed HTML size for HTML pages); Transferred (actual bytes transferred, i.e. compressed); Total Transferred (bytes incl. all resources, JS rendering mode only)

**Indexability**
- Indexability (Indexable / Non-Indexable)
- Indexability Status (reason: canonicalised, noindex, blocked by robots.txt, redirect, error, etc.)

**Redirects**
- Redirect URI (target); Redirect Type — one of: HTTP Redirect, HSTS Policy (turned around locally due to prior HSTS header), JavaScript Redirect (JS rendering only), Meta Refresh Redirect

**On-page elements** (SF extracts the **first two** instances of each; Occurrences column counts the total)
- Title 1/2, Title Length, Title Pixel Width
- Meta Description 1/2, Length, Pixel Width
- Meta Keywords 1/2, Length
- h1-1/2 + length; h2-1/2 + length
- Meta Robots 1/2; X-Robots-Tag 1/2; Meta Refresh 1
- Canonical Link Element 1/2; HTTP Canonical 1/2 (from Link header)
- rel="next" / rel="prev" (HTML link elements); HTTP rel="next" / rel="prev" (Link headers)

**Content metrics**
- Word Count (words in body, configurable content area; nav+footer excluded by default)
- Text Ratio (non-HTML chars in body ÷ total page chars, %)
- Average Words Per Sentence; Flesch Reading Ease Score (0–100); Readability classification
- Closest Similarity Match (%, near-duplicate, minhash, requires crawl analysis); No. Near Duplicates (requires crawl analysis)
- Spelling Errors, Grammar Errors, Total Language Errors (opt-in); Language (from HTML lang attribute or config)

**Graph / architecture**
- Crawl Depth (clicks from start URL; redirects count as a level); Folder Depth (path subfolder count)
- Link Score (0–100, PageRank-like, internal links; **requires crawl analysis**)
- Inlinks; Unique Inlinks; Unique JS Inlinks (links only in rendered HTML); % of Total (unique inlinks ÷ total internal 200 HTML pages)
- Outlinks; Unique Outlinks; Unique JS Outlinks
- External Outlinks; Unique External Outlinks; Unique External JS Outlinks

**Internal vs External:** Internal = same subdomain as start URL (expandable via crawl-all-subdomains / CDN config / list mode). External tab stores only: Address, Content, Status Code, Status, Crawl Depth, Inlinks.

---

## 2. Tabs: columns and filters

### 2.1 Internal / External
Columns: as above. Filters (both tabs) = content-type buckets: **HTML, JavaScript, CSS, Images, PDF, Flash (.swf), Other, Unknown**.

### 2.2 Security
Filters:
- **HTTP URLs**; **HTTPS URLs**
- **Mixed Content** — HTTPS HTML page loading HTTP resources (img/JS/CSS)
- **Form URL Insecure** — form action URL is HTTP
- **Form on HTTP URL** — any form on an HTTP page
- **Unsafe Cross-Origin Links** — external links with `target="_blank"` lacking `rel="noopener"`/`noreferrer`
- **Protocol-Relative Resource Links** — resources loaded via `//host/...`
- **Missing HSTS Header**; **Missing Content-Security-Policy Header**; **Missing X-Content-Type-Options Header** (must be `nosniff`); **Missing X-Frame-Options Header** (must be `DENY`/`SAMEORIGIN`); **Missing Secure Referrer-Policy Header** (must be one of no-referrer-when-downgrade, strict-origin-when-cross-origin, no-referrer, strict-origin)
- **Bad Content Type** — actual content type ≠ declared Content-Type header, or invalid MIME type

### 2.3 Response Codes
Filters (internal & external variants): **Blocked by Robots.txt; Blocked Resource** (JS rendering — resource blocked for rendering); **No Response** (malformed URL, timeout, connection refused/error); **Success (2XX); Redirection (3XX); Redirection (JavaScript); Redirection (Meta Refresh); Redirection (HTTP Refresh); Redirect Chain** (2+ hops); **Redirect Loop**; **Client Error (4XX); Server Error (5XX)**.

### 2.4 URL
Filters: **Non ASCII Characters; Underscores; Uppercase; Multiple Slashes** (`//` in path); **Repetitive Path** (repeated path segment); **Contains A Space; Internal Search** (URL looks like on-site search results); **Parameters** (`?`/`&`); **Broken Bookmark** (#fragment with no matching element ID on target page); **GA Tracking Parameters** (`utm=`, `_ga=`, `_gl=`); **Over 115 Characters**.

### 2.5 Page Titles
Filters: **Missing** (absent/empty/whitespace); **Duplicate; Over 60 Characters; Below 30 Characters; Over X Pixels** (default 561px); **Below X Pixels** (default 200px); **Same as h1; Multiple; Outside `<head>`**.

### 2.6 Meta Description
Filters: **Missing; Duplicate; Over 155 Characters; Below 70 Characters; Over X Pixels** (default 985px); **Below X Pixels** (default 400px); **Multiple; Outside `<head>`**.

### 2.7 Meta Keywords
Filters: **Missing; Duplicate; Multiple**.

### 2.8 h1 / h2
h1 filters: **Missing; Duplicate; Over 70 Characters; Multiple; Alt Text in h1** (h1 content comes from image alt); **Non-sequential** (h1 isn't the first heading on the page).
h2 filters: **Missing; Duplicate; Over 70 Characters; Multiple; Non-sequential**.

### 2.9 Content
Filters:
- **Exact Duplicates** — matching MD5 of full HTML
- **Near Duplicates** — minhash over body text (content area configurable), ≥ similarity threshold (default 90%); exact duplicates excluded; scores rounded (≥99.5% shows as 100%); **requires crawl analysis**
- **Low Content Pages** — word count below threshold (default 200, configurable)
- **Soft 404 Pages** — 200 status + error-page text patterns ("Page Not Found", configurable)
- **Spelling Errors; Grammar Errors** (opt-in)
- **Readability Difficult; Readability Very Difficult** (Flesch bands)
- **Lorem Ipsum Placeholder** — body contains "Lorem ipsum"

### 2.10 Images
Filters: **Over 100kb; Missing Alt Text** (has alt attribute, empty text, per referencing page); **Missing Alt Attribute; Alt Text Over 100 Characters; Background Images** (CSS/dynamically loaded — JS rendering + crawl analysis); **Missing Size Attributes** (no width/height in HTML → CLS); **Incorrectly Sized Images** (real dimensions ≠ display dimensions, flagged when est. saving ≥4KB — JS rendering + crawl analysis).

### 2.11 Canonicals
Filters: **Contains Canonical; Self Referencing; Canonicalised** (canonical ≠ self); **Missing; Multiple; Multiple Conflicting** (different URLs across instances/implementations); **Non-Indexable Canonical** (target blocked/no response/3XX/4XX/5XX/noindex); **Canonical Is Relative; Unlinked** (URL only discoverable via canonical — crawl analysis); **Invalid Attribute In Annotation** (hreflang/lang/media/type attribute on the canonical link); **Contains Fragment URL; Outside `<head>`**.

### 2.12 Pagination
Filters: **Contains Pagination; First Page** (only rel=next); **Paginated 2+ Pages** (has rel=prev); **Pagination URL Not In Anchor Tag**; **Non-200 Pagination URL; Unlinked Pagination URL** (crawl analysis); **Non-Indexable**; **Multiple Pagination URLs; Pagination Loop** (crawl analysis); **Sequence Error** (next/prev don't reciprocate).

### 2.13 Directives
Filters (each directive value): **Index; Noindex; Follow; Nofollow; None** (= noindex,nofollow); **NoArchive; NoSnippet; Max-Snippet; Max-Image-Preview; Max-Video-Preview; NoODP; NoYDIR; NoImageIndex; NoTranslate; Unavailable_After; Refresh; Outside `<head>`**.

### 2.14 Hreflang
(From HTML link element, HTTP Link header, and XML sitemaps. Hard limit: 500 annotations per page.)
Filters: **Contains Hreflang; Non-200 Hreflang URLs; Unlinked Hreflang URLs** (crawl analysis); **Missing Return Links** (reciprocity — crawl analysis); **Inconsistent Language & Region Return Links; Non-Canonical Return Links; Noindex Return Links; Incorrect Language & Region Codes** (validate ISO 639-1 + optional ISO 3166-1 Alpha-2); **Multiple Entries; Missing Self Reference; Not Using Canonical** (page's own hreflang ≠ its canonical); **Missing X-Default; Missing** (no hreflang at all); **Outside `<head>`**.

### 2.15 JavaScript (requires JS rendering mode)
Columns: HTML vs Rendered word counts and element comparisons.
Filters: **Pages with Blocked Resources; Contains JavaScript Links; Contains JavaScript Content; Noindex Only in Original HTML; Nofollow Only in Original HTML; Canonical Only in Rendered HTML; Canonical Mismatch; Page Title Only in Rendered HTML; Page Title Updated by JavaScript; Meta Description Only in Rendered HTML; Meta Description Updated by JavaScript; H1 Only in Rendered HTML; H1 Updated by JavaScript; Uses Old AJAX Crawling Scheme URLs** (`#!`); **Uses Old AJAX Crawling Scheme Meta Fragment Tag; Pages with JavaScript Errors**.

### 2.16 Links
Filters: **Pages With High Crawl Depth; Pages Without Internal Outlinks; Pages With Uncrawlable Internal Outlinks; Internal Nofollow Outlinks; Internal Outlinks With No Anchor Text; Non-Descriptive Anchor Text In Internal Outlinks; Pages With High External Outlinks; Pages With High Internal Outlinks; Follow & Nofollow Internal Inlinks To Page; Internal Nofollow Inlinks Only; Outlinks To Localhost; Non-Indexable Page Inlinks Only** (crawl analysis).

### 2.17 AMP
SEO filters: **Non-200 Response; Missing Non-AMP Return Link; Missing Canonical to Non-AMP; Non-Indexable Canonical; Indexable** (AMP with desktop equivalent should be non-indexable); **Non-Indexable**.
AMP validation filters: **Missing HTML AMP Tag; Missing/Invalid Doctype HTML Tag; Missing Head Tag; Missing Body Tag; Missing Canonical; Missing/Invalid Meta Charset Tag; Missing/Invalid Meta Viewport Tag; Missing/Invalid AMP Script; Missing/Invalid AMP Boilerplate; Contains Disallowed HTML; Other Validation Errors**.

### 2.18 Structured Data
Columns: Address, Errors, Warnings, Total Types, Unique Types, Type 1/2…
Filters: **Contains Structured Data; Missing Structured Data; Validation Errors; Validation Warnings; Parse Errors; Microdata URLs; JSON-LD URLs; RDFa URLs**.
Google rich-result features validated: Article, Book Actions, Breadcrumb, Carousel, Course list, Dataset, Employer Aggregate Rating, Estimated Salary, Event, Fact Check, FAQ, Home Activities, Image Metadata, Job Posting, Learning Video, Local Business, Logo, Math Solver, Movie Carousel, Practice Problem, Product, Q&A, Recipe, Review Snippet, Sitelinks Searchbox, Software App, Speakable, Subscription & Paywalled Content, Vehicle Listing, Video.

### 2.19 Sitemaps
(Requires "Crawl Linked XML Sitemaps"; most filters require crawl analysis.)
Filters: **URLs In Sitemap; URLs Not In Sitemap** (crawled indexable pages absent from sitemaps); **Orphan URLs** (in sitemap but not found by crawling links); **Non-Indexable URLs in Sitemap; URLs In Multiple Sitemaps; XML Sitemap With Over 50k URLs; XML Sitemap With Over 50mb** (uncompressed).

### 2.20 Validation (HTML parseability for search bots)
Filters: **Invalid HTML Elements In `<head>`** (only title/meta/link/script/style/base/noscript/template allowed; invalid element ends the head for Google); **`<body>` Element Preceding `<html>`; `<head>` Not First In `<html>` Element; Missing `<head>` Tag; Multiple `<head>` Tags; Missing `<body>` Tag; Multiple `<body>` Tags; HTML Document Over 2MB; Resource Over 2MB** (JS/CSS files); **High Carbon Rating**.

### 2.21 Accessibility (axe-core ruleset, run locally in JS rendering mode)
Filters: one per axe rule (~92 rules), grouped Best Practice / WCAG 2.0 A / AA / AAA / 2.1 AA / 2.2 AA.

### 2.22 Custom Search / Custom Extraction / Custom JavaScript
- **Custom Search**: up to 100 regex contains / does-not-contain filters. Columns: `Contains: [x]` (match count), `Does Not Contain: [y]`.
- **Custom Extraction**: up to 100 extractors (XPath, CSSPath, regex); one column per extractor.
- **Custom JavaScript**: up to 100 JS snippets (Extraction or Action) in rendering mode.

### 2.23 Change Detection (crawl comparison mode)
Filters (changed-since-last-crawl): **Indexability, Page Titles, Meta Description, H1, Word Count, Crawl Depth, Inlinks, Unique Inlinks, Internal Outlinks, Unique Internal Outlinks, External Outlinks, Unique External Outlinks, Structured Data Unique Types, Content** (>10% similarity change, configurable).

---

## 3. Issues library (name | type | priority)

SF classifies each check as **Issue** (error), **Warning** (needs review), or **Opportunity** (optimisation potential), each with **High/Medium/Low priority**.

### Response Codes
| Name | Type | Priority |
|---|---|---|
| Internal No Response | Issue | High |
| Internal Client Error (4XX) | Issue | High |
| Internal Server Error (5XX) | Issue | High |
| Internal Redirect Loop | Issue | High |
| Internal Blocked by Robots.txt | Warning | High |
| Internal Blocked Resource | Warning | High |
| Internal Redirect Chain | Warning | Medium |
| External Blocked Resource | Warning | Medium |
| Internal Redirection (3XX) | Warning | Low |
| Internal Redirection (Meta Refresh) | Warning | Low |
| Internal Redirection (HTTP Refresh) | Warning | Low |
| Internal Redirection (JavaScript) | Warning | Low |
| External No Response | Warning | Low |
| External Client Error (4XX) | Warning | Low |
| External Server Error (5XX) | Warning | Low |

### Security
HTTP URLs, Mixed Content, Form URL Insecure, Form On HTTP URL — **Issue/High**. Missing HSTS Header, Unsafe Cross Origin Links, Protocol-Relative Resource Links, Missing Content-Security-Policy Header, Missing X-Content-Type-Options Header, Missing X-Frames-Options Header, Missing Secure Referrer-Policy Header, Bad Content Type — **Warning/Low**.

### URL
Multiple Slashes, Contains A Space, Broken Bookmark — **Issue/Low**. Non ASCII Characters, Uppercase, Repetitive Path, Internal Search, Parameters, GA Tracking Parameters — **Warning/Low**. Underscores, Over 115 Characters — **Opportunity/Low**.

### Page Titles
Missing, Multiple, Outside `<head>` — **Issue/High**. Duplicate, Over 60 Characters, Below 30 Characters, Over 561 Pixels, Below 200 Pixels — **Opportunity/Medium**. Same as H1 — **Opportunity/Low**.

### Meta Description
Multiple, Outside `<head>` — **Issue/Medium**. Missing, Duplicate, Over 155 Characters, Below 70 Characters, Over 985 Pixels, Below 400 Pixels — **Opportunity/Low**.

### H1
Missing — **Issue/Medium**. Multiple — **Warning/Medium**. Alt Text in h1, Non-sequential — **Warning/Low**. Duplicate, Over 70 Characters — **Opportunity/Low**.

### H2
Missing, Multiple, Non-sequential — **Warning/Low**. Duplicate, Over 70 Characters — **Opportunity/Low**.

### Content
Exact Duplicates — **Issue/High**. Spelling Errors, Grammar Errors — **Issue/Medium**. Soft 404 Pages, Lorem Ipsum Placeholder — **Warning/High**. Near Duplicates — **Warning/Medium**. Low Content Pages — **Opportunity/Medium**. Readability Difficult, Readability Very Difficult — **Opportunity/Low**.

### Images
Missing Alt Text, Missing Alt Attribute — **Issue/Low**. Background Images — **Warning/Low**. Over 100 kb — **Opportunity/Medium**. Alt Text Over 100 Characters, Incorrectly Sized Images, Missing Size Attributes — **Opportunity/Low**.

### Canonicals
Multiple Conflicting, Non-Indexable Canonical, Invalid Attribute In Annotation, Contains Fragment URL, Outside `<head>` — **Issue/High**. Canonicalised — **Warning/High**. Missing, Unlinked — **Warning/Medium**. Multiple, Canonical Is Relative — **Warning/Low**.

### Pagination
Pagination URL Not In Anchor Tag, Non-200 Pagination URLs — **Issue/High**. Unlinked Pagination URLs — **Issue/Medium**. Multiple Pagination URLs, Pagination Loop, Sequence Error — **Issue/Low**. Non-Indexable — **Warning/High**.

### Directives
Outside `<head>` — **Issue/High**. NoImageIndex — **Issue/Low**. Noindex, Nofollow, None — **Warning/High**. Unavailable_After — **Warning/Medium**. NoSnippet, NoODP, NoYDIR, NoTranslate — **Warning/Low**.

### Hreflang
Non-200 Hreflang URLs, Missing Return Links, Inconsistent Language & Region Confirmation Links, Non-Canonical Return Links, Noindex Return Links, Incorrect Language & Region Codes, Multiple Entries, Not Using Canonical, Outside `<head>` — **Issue/High**. Unlinked Hreflang URLs — **Issue/Medium**. Missing Self Reference, Missing X-Default — **Warning/Low**.

### JavaScript
Noindex Only in Original HTML, Nofollow Only in Original HTML, Canonical Mismatch — **Issue/High**. Uses Old AJAX Crawling Scheme URLs / Meta Fragment Tag — **Issue/Medium**. Pages with Blocked Resources — **Warning/High**. Contains JavaScript Links/Content, Title/Description/H1 Only in Rendered HTML or Updated by JavaScript — **Warning/Medium**. Canonical Only in Rendered HTML, Pages With JavaScript Errors — **Warning/Low**.

### Links
Outlinks To Localhost — **Issue/High**. Pages With Uncrawlable Internal Outlinks, Pages Without Internal Outlinks, Non-Indexable Page Inlinks Only — **Warning/High**. Internal Nofollow Outlinks, Pages With High External/Internal Outlinks, Follow & Nofollow Internal Inlinks To Page, Internal Nofollow Inlinks Only — **Warning/Low**. Pages With High Crawl Depth — **Opportunity/Medium**. Internal Outlinks With No Anchor Text, Non-Descriptive Anchor Text In Internal Outlinks — **Opportunity/Low**.

### AMP
All validation/SEO checks — **Issue/High**. Indexable — **Warning/High**.

### Structured Data
Validation Errors, Rich Result Validation Errors, Parse Errors — **Issue/High**. Missing, Validation Warnings, Rich Result Validation Warnings — **Opportunity/Low**.

### Sitemaps
XML Sitemap With Over 50k URLs, XML Sitemap Over 50mb — **Issue/High**. URLs Not In Sitemap, Orphan URLs, Non-Indexable URLs In Sitemap — **Issue/Medium**. URLs In Multiple Sitemaps — **Warning/Low**.

### Validation
Missing `<head>` Tag, Multiple `<head>` Tags, Missing `<body>` Tag, Multiple `<body>` Tags, HTML Document Over 2MB, Resource Over 2MB — **Issue/High**. Invalid HTML Elements In `<head>`, `<body>` Element Preceding `<html>`, `<head>` Not First In `<html>` Element — **Warning/High**. High Carbon Rating — **Opportunity/Low**.

### Accessibility
~92 axe-core rules, all **Warning**, priority High/Medium/Low per rule (Best Practice / WCAG 2.0 A / AA / AAA / 2.1 AA / 2.2 AA groupings).

---

## 4. Crawl Analysis (post-crawl computation)

| Category | What crawl analysis computes |
|---|---|
| **Link Score** | 0–100 PageRank-like score for all internal URLs (iterative link graph computation) |
| **Response Codes** | Internal Redirect Chain, Internal Redirect Loop (chain traversal) |
| **Content** | Near Duplicates (minhash similarity clustering) |
| **Images** | Background Images, Incorrectly Sized Images |
| **Canonicals** | Unlinked (URL discoverable only via canonical) |
| **Pagination** | Unlinked Pagination URLs, Pagination Loop |
| **Hreflang** | Unlinked Hreflang URLs, Missing, reciprocity checks (missing/inconsistent/non-canonical/noindex return links) |
| **Links** | Pages With High Crawl Depth, Follow & Nofollow Internal Inlinks To Page, Internal Nofollow Inlinks Only, Non-Indexable Page Inlinks Only |
| **Sitemaps** | URLs in Sitemap, URLs not in Sitemap, Orphan URLs, Non-Indexable URLs in Sitemap, URLs in Multiple Sitemaps |

These are whole-crawl-graph or cross-URL set operations that need a separate post-crawl phase over the stored crawl DB; near-duplicate analysis can be re-run with new thresholds without re-crawling.

---

## 5. Link data model (per link edge)

Each stored link records:
- **Type** — Hyperlink, JavaScript, CSS, Image, Canonical, HTTP Redirect, Meta Refresh, rel next/prev, hreflang, amphtml, etc.
- **From** URL / **To** URL
- **Anchor Text**; **Alt Text** (for hyperlinked images)
- **Follow** — True/False (False if rel contains nofollow, ugc, or sponsored)
- **Target** — `_blank`, `_self`, `_parent`, …
- **Rel** — nofollow / sponsored / ugc (only these stored)
- **Status Code / Status** of the To URL (once crawled)
- **Path Type** — Absolute / Protocol-Relative / Root-Relative / Path-Relative
- **Link Path** — XPath of the link element within the page
- **Link Position** — Head / Nav / Content / Sidebar / Footer etc. (customisable rules)
- **Link Origin** — HTML (raw only) / Rendered HTML (JS-only) / HTML & Rendered HTML / Dynamically Loaded

Image references additionally store: Real Dimensions (rendered), Dimensions in Attributes, Display Dimensions, Potential Savings (bytes).

Derived per-page aggregates: inlinks/unique inlinks/JS-only inlinks, outlinks/unique/JS-only, external outlinks/unique/JS-only, % of Total.

---

## 6. Reports, Bulk Exports, and XML Sitemap generation

### Reports
- **Crawl Overview** — summary counts of every tab/filter, URLs encountered/blocked/crawled, content types, response code buckets
- **Redirects:** All Redirects; Redirect Chains (2+ hops: source, hop count, chain type, loop flag); Redirect & Canonical Chains; Redirects to Errors. List-mode variants report one row per uploaded URL through to final destination
- **Canonicals:** Canonical Chains; Non-Indexable Canonicals
- **Pagination:** Non-200 Pagination URLs; Unlinked Pagination URLs
- **Hreflang (7):** All Hreflang URLs; Non-200 Hreflang URLs; Unlinked Hreflang URLs; Missing Return Links; Inconsistent Language & Region Return Links; Non Canonical Return Links; Noindex Return Links
- **Insecure Content** — HTTPS pages with any HTTP elements
- **SERP Summary** — URL, title, meta description with character lengths and pixel widths
- **Orphan Pages** — URLs from XML Sitemap not matched in crawl, with `source` column
- **Structured Data:** Validation Errors & Warnings Summary; Validation Errors & Warnings (per-URL); Google Rich Results Features Summary; Google Rich Results Features
- **HTTP Header Summary** — every unique response header + count of URLs
- **Cookie Summary** — unique cookies (name, domain, expiry, secure, HttpOnly) + URL counts
- **Crawl Path Report** (per URL) — shortest discovery path from start URL

### Bulk Exports
- **Queued URLs** (discovered, not yet crawled)
- **Links:** All Inlinks; All Outlinks; All Anchor Text; External Links; Internal Nofollow Outlinks; Internal Outlinks With No Anchor Text; Non-Descriptive Anchor Text In Internal Outlinks; Follow & Nofollow Internal Inlinks To Page; Internal Nofollow Inlinks Only; Outlinks To Localhost
- **Web:** Screenshots; Archived Website; All Page Source (raw or rendered HTML); All Page Text; All PDF Documents; All PDF Content; All HTTP Request Headers; All HTTP Response Headers; All Cookies
- **Path Type** — links of a given path type with source pages
- **Per-tab inlink exports** — "all links to URLs in filter X" for: Security, Response Codes, Content, Images, Canonicals, Directives, JavaScript, AMP, Structured Data (RDF triples), Sitemaps, Custom Search, Custom Extraction, Accessibility
- **Issues:** every issue + inlinks variants as one spreadsheet per issue
- Export formats: CSV, Excel; Multi-export of selected tabs/filters

### XML Sitemap generation
- **XML Sitemap** (HTML 200 pages, optionally PDFs, images) and separate **Images Sitemap**
- Auto-split at >49,999 URLs with generated sitemap index file; conforms to sitemaps.org
- Inclusion controls (defaults: 200-only; exclude noindex, canonicalised, paginated rel=prev URLs, PDFs — each toggleable)
- **lastmod**: from server response header or custom date; optional
- **priority**: optional, configurable per depth
- **changefreq**: optional, computed from last-modified header (<24h → daily else monthly) or from crawl depth
- **images**: optional; include CDN/external images; threshold by number of IMG inlinks

---

## 7. Visualisations (data requirements; CLI may export the data as JSON/DOT)

| Visualisation | Data needed |
|---|---|
| **Force-Directed Crawl Diagram** / **Crawl Tree Graph** | Crawl discovery tree: each URL's *first* (shortest-path) inlink; crawl depth; indexability flag; optional node scaling metric |
| **Directory Tree Diagram/Graph** | URL path hierarchy (segments may be virtual non-URL path nodes), per-node URL data + indexability |
| **Inlink Anchor Text Word Cloud** | All anchor text (and image alt) of inlinks per URL |
| **Body Text Word Cloud** | Stored HTML body text |

---

## 8. Lower-window detail data (per-URL drill-down structures worth modelling)

- **URL Details**: snapshot of core fields
- **Inlinks / Outlinks / Resources**: link edges (§5)
- **Image Details**: image references + dimensions/savings
- **Duplicate Details**: per-URL list of exact + near duplicates with similarity %
- **SERP Snippet**: simulated Google snippet w/ pixel widths
- **Rendered Page** (screenshot), **View Source** (raw vs rendered HTML diff), **HTTP Headers**, **Cookies**
- **Structured Data Details**: per-URL validation issues
- **Spelling & Grammar Details**; **N-grams**: 1–6-gram phrase frequency
- **Chrome Console Log**: JS errors per page (rendering mode)

Key configurable thresholds (SF "Preferences"): title 30–60 chars / 200–561 px; description 70–155 chars / 400–985 px; h1/h2 max 70 chars; URL max 115 chars; image max 100KB; alt text max 100 chars; low-content word count (200); high crawl depth (4); high internal outlinks (1000) / external outlinks (100); non-descriptive anchor text terms; soft-404 text patterns; near-duplicate similarity (90%); content area include/exclude selectors.
