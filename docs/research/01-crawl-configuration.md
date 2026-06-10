# Screaming Frog SEO Spider — Crawl Configuration Inventory

Source: official user guide (`https://www.screamingfrog.co.uk/seo-spider/user-guide/configuration/` and `/user-guide/general/`), current as of SEO Spider v24.x (June 2026). Third-party API integrations (GA4, GSC, PageSpeed Insights, Majestic, Ahrefs, Moz, AI providers, Looker Studio) are **excluded** per scope. Defaults marked *(UI default, not stated in docs)* are well-known product defaults the documentation references indirectly.

---

## 1. Spider > Crawl

Resource and link types each have **two independent checkboxes**: `Store` (keep/report the URL in results) and `Crawl` (request it / use it for discovery).

### Resource Links
- **Images — Store / Crawl** — checkbox ×2 — default: both enabled — store and check response codes of files in `<img>` elements. Images linked via anchor tags are still crawled regardless; use Exclude/custom robots.txt to suppress those.
- **Media — Store / Crawl** — checkbox ×2 — default: both enabled — files in `<video>` and `<audio>` elements (incl. `<source src>`).
- **CSS — Store / Crawl** — checkbox ×2 — default: both enabled — stylesheets via `<link rel="stylesheet">`.
- **JavaScript — Store / Crawl** — checkbox ×2 — default: both enabled — `<script src>` files.
- **SWF — Store / Crawl** — checkbox ×2 — default: both enabled — Adobe Flash files via `<embed>`.

### Page Links
- **Internal Hyperlinks — Store / Crawl** — checkbox ×2 — default: both enabled — anchor-tag URLs on the same subdomain as the start URL; disabling both is useful in List mode (crawl only uploaded URLs plus selected link types, e.g. only hreflang or only images).
- **External Links — Store / Crawl** — checkbox ×2 — default: both enabled — URLs on other domains/subdomains; crawl = check response code only. Includes external images/CSS/JS/hreflang/canonicals.
- **Canonicals — Store / Crawl** — checkbox ×2 — default: both enabled — canonical link elements and HTTP-header canonicals; with Crawl off but Store on, they're reported but not used for URL discovery.
- **Pagination (rel next/prev) — Store / Crawl** — checkbox ×2 — default: both **disabled** — `rel="next"`/`rel="prev"` link elements.
- **Hreflang — Store / Crawl** — checkbox ×2 — default: Store enabled, Crawl **disabled** — hreflang attributes (language/region codes + URLs); with Crawl enabled, hreflang URLs are also extracted from XML sitemaps uploaded in List mode.
- **AMP — Store / Crawl** — checkbox ×2 — default: both **disabled** — `rel="amphtml"` link URLs; both recommended on for AMP auditing.
- **Meta Refresh — Store / Crawl** — checkbox ×2 — default: both enabled — URLs inside `<meta http-equiv="refresh">`.
- **iframes — Store / Crawl** — checkbox ×2 — default: both enabled — `<iframe src>` URLs.
- **Mobile Alternate — Store / Crawl** — checkbox ×2 — default: both **disabled** — `rel="alternate"` media link elements (e.g. `m.example.com`).
- **Uncrawlable Links — Store** — checkbox — default: disabled — store links not in `<a href>` form (`<span href>`, `onclick`, `javascript:` etc.); reported with Link Crawlability = "Uncrawlable".

### Crawl Behaviour
- **Check Links Outside of Start Folder** — checkbox — default: enabled — when starting in a subfolder, still request (one level) links pointing outside the folder; untick to fully confine the crawl.
- **Crawl Outside of Start Folder** — checkbox — default: disabled — start from a subfolder but crawl the whole site.
- **Crawl All Subdomains** — checkbox — default: disabled — treat all subdomains of the root domain as internal. Note: if the crawl starts at a bare root domain (no subdomain, e.g. `https://screamingfrog.co.uk`), all subdomains are crawled by default anyway.
- **Follow Internal "nofollow"** — checkbox — default: disabled — crawl internal links carrying `nofollow`/`sponsored`/`ugc` attributes, meta nofollow, or X-Robots-Tag nofollow.
- **Follow External "nofollow"** — checkbox — default: disabled — same for external links.
- **Crawl Invalid Links** — checkbox — default: disabled — parse and crawl syntactically invalid URLs (e.g. `https://example/`, `hppts://…`); reported under Response Codes > No Response.

### XML Sitemaps
- **Crawl Linked XML Sitemaps** — checkbox — default: disabled — master switch enabling sitemap crawling in Spider mode (populates Sitemaps tab; filters need post-crawl analysis). Sub-options when enabled:
  - **Auto Discover XML Sitemaps via robots.txt** — checkbox — default: disabled.
  - **Crawl These Sitemaps** — checkbox + URL list — default: disabled/empty — explicit list of sitemap URLs.

---

## 2. Spider > Extraction

All are checkboxes; disabling saves memory and empties the dependent tabs/filters.

### Page Details (default: all enabled)
- **Page Titles**, **Meta Descriptions**, **Meta Keywords**, **H1**, **H2**, **Indexability (& Indexability Status)**, **Word Count**, **Readability**, **Text to Code Ratio**, **Hash Value** (powers exact-duplicate URL detection), **Page Size**, **Forms**, **Accessibility** (AXE rule set; also requires JavaScript rendering mode).

### URL Details (default: all enabled)
- **Response Time** — seconds to download URL.
- **Last-Modified** — from HTTP response header.
- **HTTP Headers** — store full request+response headers.
- **Cookies** — store cookies per page (JS rendering needed for JS/pixel-set cookies).

### Directives (default: enabled)
- **Meta Robots**, **X-Robots-Tag**.

### Structured Data (default: all disabled)
- **JSON-LD**, **Microdata**, **RDFa** — extraction toggles per format.
- **Schema.org Validation** — validate types/properties against latest main+pending Schema vocabulary (also flags deprecated Data-Vocabulary.org); only available if ≥1 format enabled.
- **Google Rich Result Feature Validation** — validate against Google rich-result requirements (required → errors, recommended → warnings); only available if ≥1 format enabled.
- **Case-Sensitive (validation)** — checkbox — default: disabled.

### HTML / PDF Storage (default: all disabled)
- **Store HTML** — save raw static HTML of every URL to disk.
- **Store Rendered HTML** — save post-JavaScript DOM (requires JS rendering mode).
- **Store PDF** — save PDFs to disk.
- **Extract PDF Properties** — beyond default title+keywords, extract Subject, Author, Creation Date, Modification Date, Page Count, Word Count.
- **Extract Link Text (PDF)** — locate anchor text for links inside PDFs (can be slow/inaccurate/memory-heavy).

---

## 3. Spider > Limits

- **Limit Crawl Total** — integer — default: 5,000,000.
- **Limit Crawl Depth** — integer — default: off/unlimited — link hops from start URL. (List mode forces this to 0.)
- **Limit URLs Per Crawl Depth** — integer — default: off — max URLs crawled at each depth.
- **Limit Max Folder Depth** — integer — default: off — max subdirectory depth (trailing-slash path segments; `/seo-spider/user-guide/` = depth 2).
- **Limit Number of Query Strings** — integer — default: off — skip URLs with more than N query parameters.
- **Limit Crawl Total Per Subdomain** — integer — default: off — per-subdomain URL cap.
- **Max Redirects to Follow** — integer — default: off (UI default 5 when enabled) — redirect chain length to follow.
- **Limit Max URL Length to Crawl** — integer (chars) — default: 10,000 — skip longer URLs.
- **Max Links per URL to Crawl** — integer — default: 10,000 — hyperlinks processed per page.
- **Max Page Size (KB) to Crawl** — integer KB — default: 50 MB — skip/truncate larger HTML responses.
- **Limit by URL Path** — list of (URL pattern, max pages) pairs — default: empty — per-path crawl caps.

---

## 4. Spider > Rendering

- **Rendering Mode** — dropdown — values: `Text Only` | `Old AJAX Crawling Scheme` | `JavaScript` — default: **Text Only**.
  - Text Only: raw HTML only, ignores AJAX scheme and client-side JS.
  - Old AJAX Crawling Scheme: obeys Google's deprecated `?_escaped_fragment_` scheme if present, else behaves like Text Only.
  - JavaScript: headless Chromium render; extracts content/links from rendered HTML and also from raw HTML (mimics Google).

JavaScript-mode sub-options:
- **Rendered Page Screenshots** — checkbox — default: enabled (when JS mode selected).
- **AJAX Timeout (secs)** — number — default: 5 — how long JS may execute after page+resources load before snapshot.
- **Window Size** — dropdown of device presets — default: Googlebot Desktop (1024×768); Googlebot Smartphone is 411×731. For the two Googlebot presets the page is re-sized/stretched up to 8,192 px height; other device presets render at fixed viewport.
  - **Width / Height** — numbers — custom window size.
  - **Scaling Factor** — number — screenshot pixel-density emulation.
  - **Mobile** — checkbox — Chrome mobile-device flag.
  - **Touch Enabled** — checkbox — Chrome touch flag.
  - **Resize to Content** — checkbox — default: enabled — grow window to capture full page length (≤8,192 px).
  - **Window Resize Time** — number (secs) — delay between resize and screenshot.
- **JavaScript Error Reporting** — checkbox — default: disabled — capture Chrome console errors/warnings.
- **Flatten Shadow DOM** — checkbox — default: enabled — include Shadow DOM content in rendered HTML (mimics Google).
- **Flatten iframes** — checkbox — default: enabled — inline iframes into a div in rendered HTML when conditions allow.
- **Archive Website** — checkbox — default: disabled — download/store all HTML + resources locally; **Archive Format**: `Hierarchical URL Archive` | `WARC`.

---

## 5. Spider > Advanced

- **Cookie Storage** — dropdown — `Session Only` | `Persistent` | `Do Not Store` — default: **Session Only** (accept for page load then clear, like Googlebot). Persistent shares cookies across threads per crawl; cookies are reset at the start of each crawl and not saved with crawl files.
- **Ignore Non-Indexable URLs for Issues** — checkbox — default: disabled — only flag title/description/H1/H2/content/structured-data issues on indexable pages.
- **Ignore Paginated URLs for Duplicate Filters** — checkbox — default: disabled.
- **Always Follow Redirects** — checkbox — default: disabled — in List mode follow redirect chains to the final target, ignoring crawl depth (site migrations).
- **Always Follow Canonicals** — checkbox — default: disabled — in List mode follow canonical chains to final target.
- **Respect Noindex** — checkbox — default: disabled — noindex URLs still crawled and outlinks followed, but not reported.
- **Respect Canonical** — checkbox — default: disabled — canonicalised URLs not reported (still crawled).
- **Respect Next/Prev** — checkbox — default: disabled — rel="prev" sequence URLs not reported.
- **Respect HSTS Policy** — checkbox — default: **enabled** — honour HSTS (RFC 6797): subsequent requests forced to HTTPS, reported as status 307 "HSTS Policy".
- **Respect Self-Referencing Meta Refresh** — checkbox — default: **enabled** — treat self-referencing meta refresh as making the page non-indexable.
- **Extract Images from img srcset Attribute** — checkbox — default: disabled.
- **Crawl Fragment Identifiers** — checkbox — default: disabled — treat `#fragment` URLs as separate unique URLs (default strips fragments).
- **Perform HTML Validation** — checkbox — default: disabled — basic HTML error checks (Validation tab).
- **Green Hosting Carbon Calculation** — checkbox — default: disabled.
- **Assume Pages are HTML** — checkbox — default: disabled — treat responses without Content-Type as HTML.
- **Response Timeout (secs)** — number — default: **20**.
- **5XX Response Retries** — number — default: off — automatically re-request URLs returning 5xx.

---

## 6. Spider > Preferences

All numeric thresholds driving "Over/Below X" filters.

- **Page Title Width** — min/max characters and min/max pixels — defaults: 30–60 chars, 200–561 px.
- **Meta Description Width** — min/max characters and pixels — defaults: 70–155 chars, 400–985 px.
- **Links — High Internal Outlinks** — number — default: 1,000.
- **Links — High External Outlinks** — number — default: 100.
- **Links — High Crawl Depth** — number — default: 4.
- **Links — Non-Descriptive Anchor Text terms** — configurable anchor-text term list.
- **Other — Max URL Length** — characters — default: 115.
- **Other — Max H1 Length** — characters — default: 70.
- **Other — Max H2 Length** — characters — default: 70.
- **Other — Max Image Alt Text Length** — characters — default: 100.
- **Other — Max Image Size (KB)** — KB — default: 100.
- **Other — Low Content Word Count** — words — default: 200.

---

## 7. Content > Area

Defines the content region used for word count, near-duplicate analysis, and spelling/grammar (does **not** affect link discovery). Adjustable post-crawl.

- **Default scope** — `<body>` element only, with `<nav>` and `<footer>` elements excluded by default.
- **Exclude Elements / Classes / IDs** — text lists — default: `nav`, `footer`.
- **Include Elements / Classes / IDs** — text lists — default: empty — restrict analysis to listed elements/classes/IDs.

---

## 8. Content > Duplicates

- **Exact duplicates** — always on — hash-based identical page detection (depends on Hash Value extraction).
- **Enable Near Duplicates** — checkbox — default: disabled — store page content and detect near duplicates via **minhash**; requires post-crawl analysis.
- **Near Duplicate Similarity Threshold (%)** — number — default: **90** — adjustable post-crawl and re-runnable via crawl analysis without recrawling.
- **Only Check Indexable Pages** — checkbox — default: enabled.

---

## 9. Content > Spelling & Grammar

- **Enable Spell Check** / **Enable Grammar Check** — checkboxes — default: disabled.
- **Language** — auto-detect (via HTML `lang` attribute) or manual — ~40 languages.
- **Grammar Rules** — per-rule enable/disable list — default: all enabled.
- **Ignore (words)** — per-crawl ignore list. **Dictionary** — persistent per-language ignore words.
- HTML pages only; re-runnable post-crawl without recrawling.

---

## 10. Robots.txt

- **Mode** — `Respect robots.txt` | `Ignore robots.txt` | `Ignore robots.txt but report status` — default: **Respect**. "Ignore" doesn't even download robots.txt; "Ignore but report status" downloads/reports it but doesn't obey.
- **Show Internal URLs Blocked by Robots.txt** — checkbox — default: **enabled** — blocked internal URLs shown (Status Code 0, "Blocked by Robots.txt", with matched directive line).
- **Show External URLs Blocked by Robots.txt** — checkbox — default: **disabled**.
- **Custom Robots.txt** — per-subdomain editable robots.txt that overrides the live file for the crawl. Uses the configured robots user-agent for matching.
- List mode ignores robots.txt by default.

---

## 11. URL Rewriting

Applied only to URLs *discovered* during the crawl, not start/list URLs.

- **Remove Parameters** — list of parameter names — strip named query parameters (session IDs, `utm_*`, …).
- **Regex Replace** — ordered list of (regex, replace) pairs (`$1` backrefs, empty replace allowed) — rewrite each matching substring of every URL.
- **Lowercase Discovered URLs** — checkbox — default: disabled.
- **Percent Encoding Mode** — uppercase hex (`%C3%A9`, default) | lowercase hex.

---

## 12. CDNs

- **CDN List** — list of domains, optionally with subfolder — treat listed domains (or domain+subfolder scope) as **Internal**: included in Internal tab with full detail extraction.

---

## 13. Include / Exclude

- **Include** — list of partial regex patterns — crawl only matching URLs; matching is on the **URL-encoded** address; the start page must link to at least one matching URL or nothing crawls.
- **Exclude** — list of partial regex patterns — matching URLs are not requested at all (pages reachable only through them are never discovered); applies to newly discovered URLs only, not the initial URL(s); mid-crawl changes apply to pending+new URLs only; supports `\Q…\E` literal quoting.

---

## 14. Speed

- **Max Threads** — number — default: **5** — concurrent crawler threads.
- **Limit URL/s (Max URI/s)** — checkbox + decimal — default: off — cap on URL requests per second (preferred way to throttle).

---

## 15. User-Agent

- **Preset User-Agent** — dropdown — default: product UA string; presets include Googlebot (desktop/smartphone), Bingbot, browsers.
- **Custom — HTTP Request User-Agent** — the `User-Agent` header value sent with requests.
- **Custom — Robots User-Agent** — the agent token used when matching robots.txt directives (independent of HTTP header).

---

## 16. HTTP Header

- **Custom HTTP Headers** — list of name/value pairs — send arbitrary headers with every request (e.g. `Accept-Language`, `Cookie`, `Referer`).

---

## 17. Custom Search

- Up to **100** search filters; results in Custom Search tab.
- Per filter: **Name**; **Contains / Does Not Contain**; **Text / Regex** (dot matches newlines); **Scope** — raw HTML (default), page text, a specific element, or XPath.
- Searches raw HTML by default; JS rendering mode searches rendered HTML. "Contains" reports occurrence counts; "Does Not Contain" reports a boolean.

---

## 18. Custom Extraction

- Up to **100 extractors**, cap of **1,000 extractions across all extractors**; runs against internal HTML pages with **2xx** responses; static HTML by default, rendered HTML in JS mode.
- **Modes**: `XPath` (incl. attributes), `CSS Path` (+ optional attribute), `Regex`.
- **Return options** (XPath/CSS Path): `Extract HTML Element` | `Extract Inner HTML` | `Extract Text` | `Function Value` (e.g. `count(//h1)`).
- Results shown in Custom Extraction tab and as Internal-tab columns.

---

## 19. Custom Link Positions

- Classifies every link's position (navigation, content, sidebar, footer…) by matching substrings against each link's XPath "link path".
- **Rules** — ordered list of (position name, path substring); default set matches semantic HTML5 elements; `Content = "/"` catch-all must stay last.
- **Disable Link Positions** — stops storing link XPaths entirely (saves memory).

---

## 20. Custom JavaScript Snippets

- Runs user JS on each internal 200-OK URL crawled (except PDFs); requires JS rendering. Results in Custom JavaScript tab.
- **Snippet types**: **Extraction** (returns string/number or list → mapped to columns; supports Promises) and **Action** (no data; requires a timeout; e.g. scroll for infinite-scroll/lazy-load).
- **Content-type filter** per snippet. Multiple snippets allowed; all Actions run before Extractions.
- Snippet library import/export as JSON.

---

## 21. Authentication

- **Standards-Based (Basic & Digest)** — pre-configured via: **URL, username, password**.
- **Forms-Based (Web Forms / Cookies)** — log in via browser for a given site URL; session cookies then used for the crawl.
- **Authentication Profiles** — exportable encrypted auth config file for scheduling/CLI.

---

## 22. Segments

- Database-storage-mode only. Define segments from **any crawl data** (including post-crawl analysis) via rules; ordered by precedence; set up before/during/after crawl; saved with configuration.
- Output: Segments column on every tab, aggregated Segments view, per-segment XML sitemaps (+ sitemap index).

---

## 23. Crawl Analysis

- Post-crawl computation pass for **Link Score** and filters that can't populate at run-time — Sitemaps filters, Pagination, Hreflang, AMP, near-duplicate Content filters.
- **Configure** — tick-list of analysis items to compute.
- **Auto Analyse At End of Crawl** — checkbox — default: off — run automatically when crawl finishes; re-runnable to refine results (e.g. after changing similarity threshold or content area) without recrawling.

---

## 24. System: Storage Mode & Memory

- **Storage Mode** — `Database Storage` (default) | `Memory Storage`.
  - Database: disk-backed; auto-saves crawls; enables crawl comparison, change detection, segments.
  - Memory: all-RAM, fastest, suited to <500k URLs; manual saving.
- **Memory Allocation** — number (GB) — default: 2 GB.
- **Crawl Retention** — auto-delete stored crawls after a configurable period (DB mode); crawls can be **Locked** to exempt them.
- **Trusted Certificates** — directory of extra CA certificates (.pem/.crt).

---

## 25. System: Proxy

- **Use Proxy Server** — checkbox — default: off. **Address + Port** — single proxy; supports username/password credentials.

---

## 26. Mode

- **Spider** (default) — recursive crawl from a start URL.
- **List** — audit an uploaded/pasted URL set (.txt, .xls, .xlsx, .csv, .xml). Forces crawl depth 0, **ignores robots.txt by default**, pairs with Always Follow Redirects/Canonicals; export preserves original upload order.
- **SERP** — no crawling; upload URL/Title/Description rows to compute pixel widths & character lengths.
- **Compare** — diff two stored crawls; configurable change-detection elements; URL buckets: Added / New / Removed / Missing.

---

## Notes for the Go re-implementation

- The Store/Crawl two-flag pattern (per resource/link type) is the backbone of Spider > Crawl — model it as `store bool, crawl bool` per link class.
- Include/exclude/rewrite/search/extraction regexes are partial-match against the URL-encoded address for include/exclude; dot-matches-newline for search/extraction.
- Cross-cutting behaviours: List mode mutates defaults (depth 0, ignore robots.txt); robots "Ignore" skips download entirely; HSTS is emulated client-side as a synthetic 307; fragment stripping is on by default; near-duplicate = minhash at 90% over the configured content area; crawl-analysis is a distinct post-crawl phase some reports depend on.
