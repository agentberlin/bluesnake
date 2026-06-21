package mcp

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
)

// ConfigOption is one crawl knob in the list_config_options catalogue.
type ConfigOption struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Default     any      `json:"default"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// Catalog walks config.Default() by reflection (yaml tags become dotted
// paths), so the option list can never drift from the schema; descriptions,
// enums, and wiring notes are curated below.
func Catalog() []ConfigOption {
	var out []ConfigOption
	walkConfig(reflect.ValueOf(config.Default()).Elem(), "", &out)
	return out
}

// Subtrees that exist in the YAML schema but are accepted-and-ignored by the
// engine (DESIGN.md §9) — hidden so the catalogue never suggests dead knobs.
var skipPrefixes = []string{
	"extraction.page_details", "extraction.url_details", "extraction.pdf",
	"storage",
}

var enums = map[string][]string{
	"mode":                      {"spider", "list"},
	"robots.mode":               {"respect", "ignore", "ignore-report"},
	"rendering.mode":            {"text", "javascript"},
	"rendering.wait_strategy":   {"adaptive", "fixed"},
	"advanced.cookie_storage":   {"session", "persistent", "none"},
	"advanced.percent_encoding": {"upper", "lower"},
	"http.version":              {"", "1.1", "2"},
}

// Knobs parsed but not (fully) wired into the engine yet.
var notes = map[string]string{
	"advanced.respect_noindex":   "not yet wired — noindex already drives indexability reporting",
	"advanced.respect_canonical": "not yet wired — canonicals already drive indexability reporting",
	"advanced.respect_next_prev": "not yet wired",
	"advanced.html_validation":   "not yet wired",
	"analysis.canonicals":        "canonical chains are gated by analysis.redirect_chains today",
}

var descriptions = map[string]string{
	"mode": "Crawl mode. spider follows links from the seed URL; list audits a fixed URL set (start_crawl sets this automatically).",

	"scope.crawl_all_subdomains":             "Treat every subdomain (blog.site.com, shop.site.com) as internal and crawl it.",
	"scope.crawl_outside_start_folder":       "When the seed is a subfolder (site.com/blog/), also crawl URLs outside that folder.",
	"scope.check_links_outside_start_folder": "Request out-of-folder pages to verify they work, without exploring their links.",
	"scope.follow_internal_nofollow":         "Follow internal links marked rel=nofollow.",
	"scope.follow_external_nofollow":         "Follow external links marked rel=nofollow.",
	"scope.crawl_invalid_links":              "Attempt malformed links and report them as errors instead of skipping them.",
	"scope.cdns":                             "Extra domains treated as part of the site (asset CDNs).",
	"scope.include":                          "Regex allowlist — only matching URLs are crawled (empty = everything).",
	"scope.exclude":                          "Regex blocklist — matching URLs are never requested. Exclude beats include.",

	"resources.images.store":     "Report image URLs in results.",
	"resources.images.crawl":     "Request images (status, size, alt audits need this).",
	"resources.media.store":      "Report audio/video URLs in results.",
	"resources.media.crawl":      "Request audio/video files.",
	"resources.css.store":        "Report stylesheet URLs in results.",
	"resources.css.crawl":        "Request stylesheets.",
	"resources.javascript.store": "Report script URLs in results.",
	"resources.javascript.crawl": "Request scripts.",
	"resources.swf.store":        "Report Flash URLs in results.",
	"resources.swf.crawl":        "Request Flash files.",

	"links.internal.store":         "Report internal hyperlinks.",
	"links.internal.crawl":         "Follow internal hyperlinks for discovery.",
	"links.external.store":         "Report external hyperlinks.",
	"links.external.crawl":         "Request external links to check their status.",
	"links.canonicals.store":       "Report canonical link elements.",
	"links.canonicals.crawl":       "Crawl canonical targets.",
	"links.pagination.store":       "Report rel=next/prev links.",
	"links.pagination.crawl":       "Crawl rel=next/prev targets.",
	"links.hreflang.store":         "Report hreflang alternates.",
	"links.hreflang.crawl":         "Crawl hreflang targets.",
	"links.amp.store":              "Report AMP links.",
	"links.amp.crawl":              "Crawl AMP versions.",
	"links.meta_refresh.store":     "Report meta-refresh targets.",
	"links.meta_refresh.crawl":     "Follow meta-refresh redirects.",
	"links.iframes.store":          "Report iframe sources.",
	"links.iframes.crawl":          "Crawl iframe sources.",
	"links.mobile_alternate.store": "Report rel=alternate mobile links.",
	"links.mobile_alternate.crawl": "Crawl mobile alternate URLs.",
	"links.uncrawlable.store":      "Report links that cannot be crawled (bad scheme, malformed).",

	"sitemaps.crawl_linked":             "Crawl XML sitemaps referenced by the site.",
	"sitemaps.auto_discover_via_robots": "Discover sitemaps from robots.txt Sitemap: lines and crawl them.",
	"sitemaps.urls":                     "Explicit sitemap URLs to crawl in addition to discovered ones.",

	"extraction.directives.meta_robots":                         "Extract meta robots directives.",
	"extraction.directives.x_robots_tag":                        "Extract X-Robots-Tag response headers.",
	"extraction.structured_data.jsonld":                         "Parse JSON-LD structured data.",
	"extraction.structured_data.microdata":                      "Parse Microdata structured data.",
	"extraction.structured_data.rdfa":                           "Parse RDFa structured data.",
	"extraction.structured_data.schema_org_validation":          "Validate structured data against schema.org.",
	"extraction.structured_data.google_rich_results_validation": "Validate against Google rich-result requirements.",
	"extraction.structured_data.case_sensitive":                 "Case-sensitive schema.org type/property matching.",
	"extraction.store_html":                                     "Save every page's raw source to disk (pages.facts stays in SQLite either way).",
	"extraction.store_rendered_html":                            "Save the post-JavaScript DOM to disk (JavaScript rendering mode only).",
	"extraction.store_warc":                                     "Stream every fetched response into a .warc.gz archive next to the crawl database.",

	"limits.max_urls":           "Hard stop for the whole crawl.",
	"limits.max_depth":          "Max clicks from the start URL. -1 = unlimited.",
	"limits.max_urls_per_depth": "Max URLs crawled per depth level. -1 = unlimited.",
	"limits.max_folder_depth":   "Max URL folder depth. -1 = unlimited.",
	"limits.max_query_strings":  "Max query-string parameters a crawlable URL may have. -1 = unlimited.",
	"limits.max_per_subdomain":  "Max URLs per subdomain. -1 = unlimited.",
	"limits.max_redirects":      "Max redirect hops to follow.",
	"limits.max_url_length":     "URLs longer than this (chars) are not crawled.",
	"limits.max_links_per_page": "Max links extracted per page.",
	"limits.max_page_size_kb":   "Bigger downloads are abandoned (KB).",
	"limits.by_path":            "Per-path-pattern URL caps. Items: {pattern (regex), max}.",

	"rendering.mode":               "text parses static HTML; javascript renders each page in headless Chrome first (needs Chrome installed).",
	"rendering.wait_strategy":      "adaptive snapshots when the page settles (network idle + stable DOM); fixed always waits the full AJAX timeout — slower but deterministic for crawl comparisons.",
	"rendering.ajax_timeout_sec":   "Max seconds to wait for scripts/XHR to settle after load.",
	"rendering.window":             "Viewport preset name for rendering.",
	"rendering.window_width":       "Viewport width in px.",
	"rendering.window_height":      "Viewport height in px.",
	"rendering.screenshots":        "Save a screenshot of each rendered page to disk.",
	"rendering.js_error_reporting": "Record JavaScript console errors per page.",
	"rendering.flatten_shadow_dom": "Include shadow-DOM content in the rendered HTML.",
	"rendering.flatten_iframes":    "Inline iframe content into the rendered HTML.",
	"rendering.chrome_path":        "Manual Chrome binary path when auto-detection fails.",

	"advanced.cookie_storage":                        "Cookie jar behaviour during the crawl.",
	"advanced.ignore_non_indexable_for_issues":       "Skip non-indexable pages when evaluating content issues.",
	"advanced.ignore_paginated_for_duplicates":       "Exclude paginated URLs (those declaring rel=prev) from the duplicate filters: page titles, meta descriptions, H1, H2, and exact/near-duplicate content.",
	"advanced.always_follow_redirects":               "Follow redirects beyond crawl scope until a final target.",
	"advanced.always_follow_canonicals":              "Follow canonical targets beyond crawl scope.",
	"advanced.respect_hsts":                          "After seeing Strict-Transport-Security, treat http:// requests to that host as 307s to https (matches browsers).",
	"advanced.respect_self_referencing_meta_refresh": "Count a meta refresh pointing at the page itself as a refresh (affects indexability).",
	"advanced.extract_srcset":                        "Extract image URLs from srcset attributes.",
	"advanced.crawl_fragments":                       "Keep #fragment when deduplicating URLs (crawl /page#a and /page#b separately).",
	"advanced.assume_pages_are_html":                 "Parse responses with no Content-Type as HTML.",
	"advanced.response_timeout_sec":                  "Per-request timeout in seconds.",
	"advanced.retry_5xx":                             "How many times to retry 5xx responses.",
	"advanced.percent_encoding":                      "Case used when percent-encoding URLs.",

	"thresholds.title.min_chars":         "Titles shorter than this raise 'Title Below X Characters'.",
	"thresholds.title.max_chars":         "Titles longer than this raise 'Title Over X Characters'.",
	"thresholds.title.min_px":            "Min SERP pixel width for titles (bundled Arial metrics).",
	"thresholds.title.max_px":            "Titles wider than this truncate on Google's results page.",
	"thresholds.description.min_chars":   "Meta descriptions shorter than this are flagged.",
	"thresholds.description.max_chars":   "Meta descriptions longer than this are flagged.",
	"thresholds.description.min_px":      "Min SERP pixel width for descriptions.",
	"thresholds.description.max_px":      "Max SERP pixel width before truncation.",
	"thresholds.url_max_chars":           "URLs longer than this are flagged.",
	"thresholds.h1_max_chars":            "H1s longer than this are flagged.",
	"thresholds.h2_max_chars":            "H2s longer than this are flagged.",
	"thresholds.image_alt_max_chars":     "Alt texts longer than this are flagged.",
	"thresholds.image_max_kb":            "Images heavier than this are flagged.",
	"thresholds.low_content_words":       "Pages with fewer words are flagged as low content.",
	"thresholds.high_crawl_depth":        "Pages deeper than this many clicks are flagged.",
	"thresholds.high_internal_outlinks":  "Pages with more internal outlinks are flagged.",
	"thresholds.high_external_outlinks":  "Pages with more external outlinks are flagged.",
	"thresholds.non_descriptive_anchors": "Anchor texts considered non-descriptive ('click here').",
	"thresholds.soft_404_patterns":       "Body phrases that mark a 200 page as a soft 404.",

	"content.area.include_elements":          "Elements that count as 'content' for word count and duplicate detection.",
	"content.area.include_classes":           "CSS classes included in the content area.",
	"content.area.include_ids":               "Element IDs included in the content area.",
	"content.area.exclude_elements":          "Elements excluded from the content area (nav, footer...).",
	"content.area.exclude_classes":           "CSS classes excluded from the content area.",
	"content.area.exclude_ids":               "Element IDs excluded from the content area.",
	"content.near_duplicates.enabled":        "Detect near-duplicate pages (minhash similarity).",
	"content.near_duplicates.threshold":      "Similarity percentage (0-100) above which pages count as near-duplicates.",
	"content.near_duplicates.indexable_only": "Only consider indexable pages for duplicate detection.",

	"robots.mode":                  "respect obeys robots.txt like Google; ignore crawls everything; ignore-report crawls everything but still reports what would be blocked.",
	"robots.show_blocked_internal": "Report internal URLs blocked by robots.txt as results.",
	"robots.show_blocked_external": "Report external URLs blocked by robots.txt.",
	"robots.custom":                "Per-host robots.txt overrides. Items: {host, file}.",

	"url_rewriting.remove_params": "Query parameters stripped from every discovered URL (session ids, UTM tags).",
	"url_rewriting.regex_replace": "Regex rewrites applied to discovered URLs. Items: {pattern, replace}.",
	"url_rewriting.lowercase":     "Lowercase every discovered URL.",

	"speed.max_threads":      "Parallel download workers.",
	"speed.max_urls_per_sec": "Politeness throttle across all workers. 0 = unlimited.",

	"http.user_agent":        "HTTP User-Agent header sent with every request.",
	"http.robots_user_agent": "Token used when matching robots.txt rules.",
	"http.version":           "Empty = negotiate (prefer HTTP/2); '1.1' forces HTTP/1.1; '2' forces HTTP/2.",
	"http.browser_headers":   "Send browser-like Accept/Accept-Language/Cache-Control defaults (Screaming Frog parity).",
	"http.headers":           "Extra request headers, name -> value.",
	"http.proxy":             "Proxy URL (http://user:pass@host:port).",
	"http.trusted_cert_dirs": "Directories of extra trusted CA certificates.",
	"http.auth.basic":        "HTTP Basic credentials per URL prefix. Items: {url_prefix, username, password, password_env}.",
	"http.auth.cookies":      "Auth cookies sent with matching requests. Items: {name, value, domain}.",

	"custom_search":     "Find pages containing (or missing) a pattern. Items: {name, mode contains|not_contains, pattern, regex, scope html|text|element:<selector>}. Results land in the custom_results table (kind='search').",
	"custom_extraction": "Scrape arbitrary values from every page. Items: {name, type xpath|css|regex, expression, attribute, return text|html|inner_html|function}. Results land in custom_results (kind='extraction').",
	"custom_js":         "Run JavaScript snippets in the rendered page (rendering.mode=javascript). Items: {name, type extraction|action, file, timeout_sec, content_types}.",
	"link_positions":    "Classify links by where they sit in the page. Items: {name, match CSS selector}.",
	"store_link_paths":  "Store each link's DOM path (elem_path column in links table).",

	"list_mode.respect_robots": "Obey robots.txt in list mode.",
	"list_mode.crawl_depth":    "How many clicks beyond the list to crawl (0 = just the list).",

	"analysis.auto":            "Run issue evaluation and graph analyses automatically when a crawl completes.",
	"analysis.link_score":      "PageRank-style internal link score per URL (pages.link_score).",
	"analysis.redirect_chains": "Follow redirect/canonical edges into chains and loops.",
	"analysis.near_duplicates": "Near-duplicate clustering (pages.closest_similarity, near_dup_count).",
	"analysis.pagination":      "rel=next/prev sequence checks.",
	"analysis.hreflang":        "Hreflang reciprocity and validity checks.",
	"analysis.links":           "Unique in/outlink counts per URL.",
	"analysis.sitemaps":        "Sitemap set operations (in sitemap vs crawled, orphans).",

	"compare.change_detection":         "Which page elements crawl comparison diffs.",
	"compare.content_change_threshold": "Percent body-content change that counts as 'changed'.",
	"compare.url_mapping":              "Regex URL mappings for comparing across migrations. Items: {pattern, replace}.",
}

func skipKey(key string) bool {
	for _, p := range skipPrefixes {
		if key == p || strings.HasPrefix(key, p+".") {
			return true
		}
	}
	return false
}

func walkConfig(v reflect.Value, prefix string, out *[]ConfigOption) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(ft.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		if skipKey(key) {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Struct:
			walkConfig(fv, key, out)
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Struct {
				*out = append(*out, option(key, "list of objects ("+elemFields(fv.Type().Elem())+")", []any{}))
			} else {
				*out = append(*out, option(key, "list of strings", fv.Interface()))
			}
		case reflect.Map:
			*out = append(*out, option(key, "map of string to string", fv.Interface()))
		case reflect.Bool:
			*out = append(*out, option(key, "bool", fv.Bool()))
		case reflect.Int, reflect.Int64:
			*out = append(*out, option(key, "int", fv.Int()))
		case reflect.Float64:
			*out = append(*out, option(key, "float", fv.Float()))
		case reflect.String:
			*out = append(*out, option(key, "string", fv.String()))
		default:
			*out = append(*out, option(key, fv.Kind().String(), fmt.Sprintf("%v", fv.Interface())))
		}
	}
}

func elemFields(t reflect.Type) string {
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if tag != "" && tag != "-" && t.Field(i).IsExported() {
			fields = append(fields, tag)
		}
	}
	return strings.Join(fields, ", ")
}

func option(key, typ string, def any) ConfigOption {
	return ConfigOption{
		Key: key, Type: typ, Default: def,
		Enum:        enums[key],
		Description: descriptions[key],
		Note:        notes[key],
	}
}
