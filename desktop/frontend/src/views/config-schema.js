/* ===========================================================================
   Shared crawl-config schema — the single source of truth for how config knobs
   are labelled, grouped, and typed. Bound to the real config schema (dotted
   yaml-tag keys in internal/config).

   Two views render from this:
     • Settings & Profiles (settings.jsx) — editable, per profile.
     • Crawl Setup (setup.jsx) — read-only, the config frozen into one crawl.
   Keeping the schema here is what keeps those two views in lockstep.
   =========================================================================== */

/* field builders: every `key` is a verified dotted yaml path in internal/config */
export const tg = (key, label, hint, adv) => ({ key, label, type: "toggle", hint, advanced: adv });
export const num = (key, label, hint, unit, adv) => ({ key, label, type: "number", hint, unit, advanced: adv });
export const ch = (key, label, options, hint, adv) => ({ key, label, type: "choice", options, hint, advanced: adv });
export const txt = (key, label, hint, adv) => ({ key, label, type: "text", hint, advanced: adv });
export const lst = (key, label, hint, adv) => ({ key, label, type: "list", hint, advanced: adv });

export const SECTIONS = [
  { id: "scope", label: "Crawl Scope", icon: "crosshair", fields: [
    tg("scope.crawl_all_subdomains", "Crawl all subdomains", "Treat blog.site.com, shop.site.com as part of the site."),
    tg("scope.crawl_outside_start_folder", "Crawl outside start folder", "Lift the /blog/ restriction when starting in a subfolder."),
    tg("scope.check_links_outside_start_folder", "Check links outside start folder", "Verify out-of-folder pages work without exploring them."),
    tg("scope.follow_internal_nofollow", "Follow internal nofollow links", null, true),
    tg("scope.follow_external_nofollow", "Follow external nofollow links", null, true),
    tg("scope.crawl_invalid_links", "Crawl invalid links", "Attempt malformed links and report them as errors.", true),
    lst("scope.cdns", "CDN domains", "Extra domains to treat as part of the site (asset CDNs).", true),
    lst("scope.include", "Include patterns (regex)", "Only URLs matching at least one are crawled."),
    lst("scope.exclude", "Exclude patterns (regex)", "URLs matching any are never requested. Exclude beats include."),
  ]},
  { id: "extraction", label: "Extraction", icon: "scan-line", fields: [
    // bluesnake always extracts the full per-URL dataset in one parse pass —
    // the per-field page_details/url_details/directives toggles SF uses to
    // save memory have no effect here, so they aren't shown (see DESIGN §9).
    tg("extraction.structured_data.jsonld", "JSON-LD", "Structured data master toggle."),
    tg("extraction.structured_data.microdata", "Microdata", null, true),
    tg("extraction.structured_data.rdfa", "RDFa", null, true),
    tg("extraction.store_html", "Store raw HTML", "Saves every page's source to disk for later viewing.", true),
    tg("extraction.store_rendered_html", "Store rendered HTML", "Saves the post-JavaScript DOM to disk (JavaScript rendering mode only).", true),
    tg("extraction.store_warc", "Archive responses as WARC", "Streams every fetched response into a standard .warc.gz archive next to the crawl database.", true),
  ]},
  { id: "limits", label: "Limits", icon: "gauge", fields: [
    num("limits.max_urls", "Max URLs to crawl", "Hard stop for the whole crawl."),
    num("limits.max_depth", "Max crawl depth", "Clicks from the start URL. −1 = unlimited."),
    num("limits.max_urls_per_depth", "Max URLs per depth level", null, null, true),
    num("limits.max_folder_depth", "Max folder depth", null, null, true),
    num("limits.max_query_strings", "Max query-string parameters", null, null, true),
    num("limits.max_redirects", "Max redirects to follow"),
    num("limits.max_url_length", "Max URL length", null, "chars", true),
    num("limits.max_links_per_page", "Max links per page", null, null, true),
    num("limits.max_page_size_kb", "Max page size", "Bigger downloads are abandoned.", "KB"),
  ]},
  { id: "rendering", label: "Rendering (JavaScript)", icon: "chrome", fields: [
    ch("rendering.mode", "Rendering mode", ["text", "javascript"], "JavaScript mode loads each page in headless Chrome. Requires Chrome installed."),
    ch("rendering.wait_strategy", "Wait strategy", ["adaptive", "fixed"], "Adaptive snapshots as soon as the page settles. Fixed waits the full AJAX timeout after load — slower but deterministic for crawl comparisons."),
    num("rendering.ajax_timeout_sec", "AJAX timeout", "Max wait for scripts/XHR to settle. Pages that go network-idle sooner snapshot immediately.", "s"),
    tg("rendering.screenshots", "Capture screenshots", "Saves a screenshot of each rendered page to disk.", true),
    tg("rendering.js_error_reporting", "Report JavaScript console errors", null, true),
    txt("rendering.chrome_path", "Chrome path", "Manual override when Chrome isn't found.", true),
  ]},
  { id: "thresholds", label: "Thresholds", icon: "sliders-horizontal", fields: [
    num("thresholds.title.min_chars", "Page title min length", null, "chars"),
    num("thresholds.title.max_chars", "Page title max length", null, "chars"),
    num("thresholds.title.min_px", "Page title min SERP width", "Measured with bundled Arial metrics at Google's title font size.", "px"),
    num("thresholds.title.max_px", "Page title max SERP width", "Titles wider than this truncate on the results page.", "px"),
    num("thresholds.description.min_chars", "Meta description min length", null, "chars"),
    num("thresholds.description.max_chars", "Meta description max length", null, "chars"),
    num("thresholds.description.min_px", "Meta description min SERP width", null, "px"),
    num("thresholds.description.max_px", "Meta description max SERP width", null, "px"),
    num("thresholds.url_max_chars", "Max URL length flag", null, "chars"),
    num("thresholds.h1_max_chars", "Max H1 length", null, "chars"),
    num("thresholds.h2_max_chars", "Max H2 length", null, "chars", true),
    num("thresholds.image_alt_max_chars", "Max image alt-text length", null, "chars"),
    num("thresholds.image_max_kb", "Max image file size", null, "KB"),
    num("thresholds.low_content_words", "Low-content word count", null, "words"),
    num("thresholds.high_crawl_depth", "High crawl depth", null, "clicks"),
    lst("thresholds.non_descriptive_anchors", "Non-descriptive anchor texts"),
    lst("thresholds.soft_404_patterns", "Soft-404 phrases"),
  ]},
  { id: "robots", label: "robots.txt", icon: "bot", fields: [
    ch("robots.mode", "Mode", ["respect", "ignore", "ignore-report"], "Respect obeys robots.txt like Google does."),
    tg("robots.show_blocked_internal", "Show blocked internal URLs"),
    tg("robots.show_blocked_external", "Show blocked external URLs"),
  ]},
  { id: "rewriting", label: "URL Rewriting", icon: "replace", fields: [
    lst("url_rewriting.remove_params", "Remove query parameters"),
    tg("url_rewriting.lowercase", "Lowercase all URLs", null, true),
  ]},
  { id: "speed", label: "Speed", icon: "zap", fields: [
    num("speed.max_threads", "Max threads", "Parallel downloads."),
    num("speed.max_urls_per_sec", "Max URLs per second", "Politeness throttle. 0 = unlimited.", "URL/s"),
  ]},
  { id: "http", label: "HTTP & Identity", icon: "fingerprint", fields: [
    txt("http.user_agent", "User-agent"),
    txt("http.robots_user_agent", "Robots user-agent token", "Used when matching robots.txt rules."),
    txt("http.proxy", "Proxy", "http://user:pass@host:port", true),
  ]},
  { id: "content", label: "Content Analysis", icon: "text-select", fields: [
    lst("content.area.exclude_elements", "Content area — exclude elements", "Which parts count as 'content' for word count & duplicates."),
    lst("content.area.include_elements", "Content area — include elements"),
    tg("content.near_duplicates.enabled", "Near-duplicate detection"),
    num("content.near_duplicates.threshold", "Similarity threshold", "Re-running analysis is enough — no recrawl needed.", "%"),
    tg("content.near_duplicates.indexable_only", "Only check indexable pages"),
  ]},
  { id: "analysis", label: "Analysis", icon: "git-compare", fields: [
    tg("analysis.auto", "Auto-analyse after crawl"),
    tg("analysis.link_score", "Link score"),
    tg("analysis.redirect_chains", "Redirect chains"),
    tg("analysis.near_duplicates", "Near-duplicates"),
    tg("analysis.pagination", "Pagination"),
    tg("analysis.hreflang", "Hreflang"),
    // analysis.canonicals omitted: canonical-chain analysis is gated by
    // "Redirect chains" today; the separate toggle is unwired (DESIGN §9).
    tg("analysis.sitemaps", "Sitemaps"),
  ]},
  // Storage section omitted: storage.dir and storage.retention_days are not
  // yet wired (the store path comes from --store-dir / the app default, and
  // retention pruning is unimplemented) — see DESIGN §9. The active storage
  // location is shown read-only on the home screen via GetStorageInfo.
];

/* every field across every section, tagged with its section (search + diff) */
export const ALL_FIELDS = SECTIONS.flatMap((s) => s.fields.map((f) => ({ ...f, section: s.label, sectionId: s.id })));

/* read a dotted yaml-tag path out of a parsed config object */
export const getPath = (obj, key) => key.split(".").reduce((o, k) => (o == null ? undefined : o[k]), obj);

/* encode a JS value as the YAML literal config.Set expects (settings editor) */
export const encodeVal = (f, v) => {
  if (f.type === "toggle") return v ? "true" : "false";
  if (f.type === "number") return String(v);
  return JSON.stringify(v); // strings and arrays — JSON is valid YAML
};
