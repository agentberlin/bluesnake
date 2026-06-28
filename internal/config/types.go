// Package config defines bluesnake's complete plain-text configuration schema:
// one YAML document covering every crawl knob (the Screaming Frog feature set),
// strict parsing (unknown keys are errors), defaults for everything, dotted-path
// overrides, and validation with key-path error messages.
package config

import "regexp"

type Config struct {
	Mode string `yaml:"mode"` // spider | list

	Scope            ScopeConfig        `yaml:"scope"`
	Resources        ResourcesConfig    `yaml:"resources"`
	Links            LinksConfig        `yaml:"links"`
	Sitemaps         SitemapsConfig     `yaml:"sitemaps"`
	LlmsTxt          LlmsTxtConfig      `yaml:"llms_txt"`
	Extraction       ExtractionConfig   `yaml:"extraction"`
	Limits           LimitsConfig       `yaml:"limits"`
	Rendering        RenderingConfig    `yaml:"rendering"`
	Advanced         AdvancedConfig     `yaml:"advanced"`
	Thresholds       ThresholdsConfig   `yaml:"thresholds"`
	Content          ContentConfig      `yaml:"content"`
	Robots           RobotsConfig       `yaml:"robots"`
	URLRewriting     URLRewritingConfig `yaml:"url_rewriting"`
	Speed            SpeedConfig        `yaml:"speed"`
	HTTP             HTTPConfig         `yaml:"http"`
	CustomSearch     []CustomSearch     `yaml:"custom_search"`
	CustomExtraction []CustomExtraction `yaml:"custom_extraction"`
	CustomJS         []CustomJS         `yaml:"custom_js"`
	LinkPositions    []LinkPosition     `yaml:"link_positions"`
	StoreLinkPaths   bool               `yaml:"store_link_paths"`
	ListMode         ListModeConfig     `yaml:"list_mode"`
	Analysis         AnalysisConfig     `yaml:"analysis"`
	Storage          StorageConfig      `yaml:"storage"`
	Compare          CompareConfig      `yaml:"compare"`
}

type ScopeConfig struct {
	CrawlAllSubdomains           bool     `yaml:"crawl_all_subdomains"`
	CrawlOutsideStartFolder      bool     `yaml:"crawl_outside_start_folder"`
	CheckLinksOutsideStartFolder bool     `yaml:"check_links_outside_start_folder"`
	FollowInternalNofollow       bool     `yaml:"follow_internal_nofollow"`
	FollowExternalNofollow       bool     `yaml:"follow_external_nofollow"`
	CrawlInvalidLinks            bool     `yaml:"crawl_invalid_links"`
	CDNs                         []string `yaml:"cdns"`
	Include                      []string `yaml:"include"`
	Exclude                      []string `yaml:"exclude"`

	includeRE []*regexp.Regexp
	excludeRE []*regexp.Regexp
}

// IncludeRE returns the compiled include patterns (compiled during Validate).
func (s *ScopeConfig) IncludeRE() []*regexp.Regexp { return s.includeRE }

// ExcludeRE returns the compiled exclude patterns (compiled during Validate).
func (s *ScopeConfig) ExcludeRE() []*regexp.Regexp { return s.excludeRE }

// StoreCrawl is the Screaming Frog two-flag pattern: Store = keep/report the
// URL in results, Crawl = request it / use it for discovery.
type StoreCrawl struct {
	Store bool `yaml:"store"`
	Crawl bool `yaml:"crawl"`
}

type ResourcesConfig struct {
	Images     StoreCrawl `yaml:"images"`
	Media      StoreCrawl `yaml:"media"`
	CSS        StoreCrawl `yaml:"css"`
	JavaScript StoreCrawl `yaml:"javascript"`
	SWF        StoreCrawl `yaml:"swf"`
}

type LinksConfig struct {
	Internal        StoreCrawl `yaml:"internal"`
	External        StoreCrawl `yaml:"external"`
	Canonicals      StoreCrawl `yaml:"canonicals"`
	Pagination      StoreCrawl `yaml:"pagination"`
	Hreflang        StoreCrawl `yaml:"hreflang"`
	AMP             StoreCrawl `yaml:"amp"`
	MetaRefresh     StoreCrawl `yaml:"meta_refresh"`
	IFrames         StoreCrawl `yaml:"iframes"`
	MobileAlternate StoreCrawl `yaml:"mobile_alternate"`
	Uncrawlable     struct {
		Store bool `yaml:"store"`
	} `yaml:"uncrawlable"`
}

type SitemapsConfig struct {
	CrawlLinked           bool     `yaml:"crawl_linked"`
	AutoDiscoverViaRobots bool     `yaml:"auto_discover_via_robots"`
	URLs                  []string `yaml:"urls"`
}

// LlmsTxtConfig controls the /llms.txt site audit (llmstxt.org). The file is
// fetched once per crawled host, out-of-band like robots.txt; its curated links
// are cross-checked against the crawl during analysis.
type LlmsTxtConfig struct {
	Check       bool `yaml:"check"`        // fetch & validate /llms.txt at all
	FetchFull   bool `yaml:"fetch_full"`   // also fetch /llms-full.txt
	CrawlLinked bool `yaml:"crawl_linked"` // admit the curated links into the frontier
}

type PageDetailsConfig struct {
	Titles           bool `yaml:"titles"`
	MetaDescriptions bool `yaml:"meta_descriptions"`
	MetaKeywords     bool `yaml:"meta_keywords"`
	H1               bool `yaml:"h1"`
	H2               bool `yaml:"h2"`
	Indexability     bool `yaml:"indexability"`
	WordCount        bool `yaml:"word_count"`
	Readability      bool `yaml:"readability"`
	TextToCodeRatio  bool `yaml:"text_to_code_ratio"`
	Hash             bool `yaml:"hash"`
	PageSize         bool `yaml:"page_size"`
	Forms            bool `yaml:"forms"`
}

type URLDetailsConfig struct {
	ResponseTime bool `yaml:"response_time"`
	LastModified bool `yaml:"last_modified"`
	HTTPHeaders  bool `yaml:"http_headers"`
	Cookies      bool `yaml:"cookies"`
}

type DirectivesConfig struct {
	MetaRobots bool `yaml:"meta_robots"`
	XRobotsTag bool `yaml:"x_robots_tag"`
}

type StructuredDataConfig struct {
	JSONLD                      bool `yaml:"jsonld"`
	Microdata                   bool `yaml:"microdata"`
	RDFa                        bool `yaml:"rdfa"`
	SchemaOrgValidation         bool `yaml:"schema_org_validation"`
	GoogleRichResultsValidation bool `yaml:"google_rich_results_validation"`
	CaseSensitive               bool `yaml:"case_sensitive"`
}

type PDFConfig struct {
	Store             bool `yaml:"store"`
	ExtractProperties bool `yaml:"extract_properties"`
	ExtractLinkText   bool `yaml:"extract_link_text"`
}

type ExtractionConfig struct {
	PageDetails       PageDetailsConfig    `yaml:"page_details"`
	URLDetails        URLDetailsConfig     `yaml:"url_details"`
	Directives        DirectivesConfig     `yaml:"directives"`
	StructuredData    StructuredDataConfig `yaml:"structured_data"`
	StoreHTML         bool                 `yaml:"store_html"`
	StoreRenderedHTML bool                 `yaml:"store_rendered_html"`
	StoreWARC         bool                 `yaml:"store_warc"`
	PDF               PDFConfig            `yaml:"pdf"`
}

type PathLimit struct {
	Pattern string `yaml:"pattern"`
	Max     int    `yaml:"max"`
}

type LimitsConfig struct {
	MaxURLs         int         `yaml:"max_urls"`
	MaxDepth        int         `yaml:"max_depth"` // -1 = unlimited
	MaxURLsPerDepth int         `yaml:"max_urls_per_depth"`
	MaxFolderDepth  int         `yaml:"max_folder_depth"`
	MaxQueryStrings int         `yaml:"max_query_strings"`
	MaxPerSubdomain int         `yaml:"max_per_subdomain"`
	MaxRedirects    int         `yaml:"max_redirects"`
	MaxURLLength    int         `yaml:"max_url_length"`
	MaxLinksPerPage int         `yaml:"max_links_per_page"`
	MaxPageSizeKB   int         `yaml:"max_page_size_kb"`
	ByPath          []PathLimit `yaml:"by_path"`
}

type RenderingConfig struct {
	Mode             string `yaml:"mode"`          // text | javascript
	WaitStrategy     string `yaml:"wait_strategy"` // adaptive | fixed (DESIGN.md §8: fixed = load event + full AJAX sleep, compare-stable snapshots)
	AjaxTimeoutSec   int    `yaml:"ajax_timeout_sec"`
	Window           string `yaml:"window"` // preset name
	WindowWidth      int    `yaml:"window_width"`
	WindowHeight     int    `yaml:"window_height"`
	Screenshots      bool   `yaml:"screenshots"`
	JSErrorReporting bool   `yaml:"js_error_reporting"`
	FlattenShadowDOM bool   `yaml:"flatten_shadow_dom"`
	FlattenIFrames   bool   `yaml:"flatten_iframes"`
	ChromePath       string `yaml:"chrome_path"`
}

type AdvancedConfig struct {
	CookieStorage                     string `yaml:"cookie_storage"` // session | persistent | none
	IgnoreNonIndexableForIssues       bool   `yaml:"ignore_non_indexable_for_issues"`
	IgnorePaginatedForDuplicates      bool   `yaml:"ignore_paginated_for_duplicates"`
	AlwaysFollowRedirects             bool   `yaml:"always_follow_redirects"`
	AlwaysFollowCanonicals            bool   `yaml:"always_follow_canonicals"`
	RespectNoindex                    bool   `yaml:"respect_noindex"`
	RespectCanonical                  bool   `yaml:"respect_canonical"`
	RespectNextPrev                   bool   `yaml:"respect_next_prev"`
	RespectHSTS                       bool   `yaml:"respect_hsts"`
	RespectSelfReferencingMetaRefresh bool   `yaml:"respect_self_referencing_meta_refresh"`
	ExtractSrcset                     bool   `yaml:"extract_srcset"`
	CrawlFragments                    bool   `yaml:"crawl_fragments"`
	HTMLValidation                    bool   `yaml:"html_validation"`
	AssumePagesAreHTML                bool   `yaml:"assume_pages_are_html"`
	// SkipIdenticalContentLinks: when a fetched page's RAW body is byte-identical
	// (same content hash) to one already crawled, record it but do not render or
	// expand its outlinks. Stops client-routed SPA shells and query-string twins
	// from ballooning the frontier (Screaming Frog parity; see R8 / sweetgreen
	// order.*). Only full byte identity short-circuits, never a near-duplicate.
	SkipIdenticalContentLinks bool   `yaml:"skip_identical_content_links"`
	ResponseTimeoutSec        int    `yaml:"response_timeout_sec"`
	Retry5xx                  int    `yaml:"retry_5xx"`
	PercentEncoding           string `yaml:"percent_encoding"` // upper | lower
}

type WidthThreshold struct {
	MinChars int `yaml:"min_chars"`
	MaxChars int `yaml:"max_chars"`
	MinPx    int `yaml:"min_px"`
	MaxPx    int `yaml:"max_px"`
}

type ThresholdsConfig struct {
	Title                 WidthThreshold `yaml:"title"`
	Description           WidthThreshold `yaml:"description"`
	URLMaxChars           int            `yaml:"url_max_chars"`
	H1MaxChars            int            `yaml:"h1_max_chars"`
	H2MaxChars            int            `yaml:"h2_max_chars"`
	ImageAltMaxChars      int            `yaml:"image_alt_max_chars"`
	ImageMaxKB            int            `yaml:"image_max_kb"`
	LowContentWords       int            `yaml:"low_content_words"`
	HighCrawlDepth        int            `yaml:"high_crawl_depth"`
	HighInternalOutlinks  int            `yaml:"high_internal_outlinks"`
	HighExternalOutlinks  int            `yaml:"high_external_outlinks"`
	NonDescriptiveAnchors []string       `yaml:"non_descriptive_anchors"`
	Soft404Patterns       []string       `yaml:"soft_404_patterns"`
}

type ContentAreaConfig struct {
	IncludeElements []string `yaml:"include_elements"`
	IncludeClasses  []string `yaml:"include_classes"`
	IncludeIDs      []string `yaml:"include_ids"`
	ExcludeElements []string `yaml:"exclude_elements"`
	ExcludeClasses  []string `yaml:"exclude_classes"`
	ExcludeIDs      []string `yaml:"exclude_ids"`
}

type NearDuplicatesConfig struct {
	Enabled       bool `yaml:"enabled"`
	Threshold     int  `yaml:"threshold"` // percent 0-100
	IndexableOnly bool `yaml:"indexable_only"`
}

type ContentConfig struct {
	Area           ContentAreaConfig    `yaml:"area"`
	NearDuplicates NearDuplicatesConfig `yaml:"near_duplicates"`
}

type CustomRobots struct {
	Host string `yaml:"host"`
	File string `yaml:"file"`
}

type RobotsConfig struct {
	Mode                string         `yaml:"mode"` // respect | ignore | ignore-report
	ShowBlockedInternal bool           `yaml:"show_blocked_internal"`
	ShowBlockedExternal bool           `yaml:"show_blocked_external"`
	Custom              []CustomRobots `yaml:"custom"`
}

type RegexReplace struct {
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace"`
}

type URLRewritingConfig struct {
	RemoveParams []string       `yaml:"remove_params"`
	RegexReplace []RegexReplace `yaml:"regex_replace"`
	Lowercase    bool           `yaml:"lowercase"`
}

type SpeedConfig struct {
	MaxThreads    int     `yaml:"max_threads"`
	MaxURLsPerSec float64 `yaml:"max_urls_per_sec"` // 0 = unlimited
	// MaxGlobalThreads caps total concurrent fetches across ALL running crawls
	// in this process (parallel multi-crawl). 0 = unlimited — a single crawl then
	// behaves exactly as before, bounded only by MaxThreads (MEMORY-SCALING.md §5.6).
	MaxGlobalThreads int `yaml:"max_global_threads"`
	// MaxConcurrentCrawls caps how many crawls the dispatcher runs in parallel
	// (each with its own worker pool/DB/buffers — a distinct overhead axis from
	// the fetch cap, GL-18). 0/1 = one crawl at a time, the default.
	MaxConcurrentCrawls int `yaml:"max_concurrent_crawls"`
}

type BasicAuth struct {
	URLPrefix   string `yaml:"url_prefix"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	PasswordEnv string `yaml:"password_env"`
}

type AuthCookie struct {
	Name   string `yaml:"name"`
	Value  string `yaml:"value"`
	Domain string `yaml:"domain"`
}

type AuthConfig struct {
	Basic   []BasicAuth  `yaml:"basic"`
	Cookies []AuthCookie `yaml:"cookies"`
}

type HTTPConfig struct {
	UserAgent       string            `yaml:"user_agent"`
	RobotsUserAgent string            `yaml:"robots_user_agent"`
	Version         string            `yaml:"version"`         // "" (negotiate, prefer HTTP/2) | "1.1" (force HTTP/1.1) | "2"
	BrowserHeaders  bool              `yaml:"browser_headers"` // send browser-like Accept/Accept-Language defaults
	Headers         map[string]string `yaml:"headers"`
	Proxy           string            `yaml:"proxy"`
	TrustedCertDirs []string          `yaml:"trusted_cert_dirs"`
	Auth            AuthConfig        `yaml:"auth"`
}

type CustomSearch struct {
	Name    string `yaml:"name"`
	Mode    string `yaml:"mode"` // contains | not_contains
	Pattern string `yaml:"pattern"`
	Regex   bool   `yaml:"regex"`
	Scope   string `yaml:"scope"` // html | text | element:<selector>
}

type CustomExtraction struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"` // xpath | css | regex
	Expression string `yaml:"expression"`
	Attribute  string `yaml:"attribute"`
	Return     string `yaml:"return"` // text | html | inner_html | function
}

type CustomJS struct {
	Name         string   `yaml:"name"`
	Type         string   `yaml:"type"` // extraction | action
	File         string   `yaml:"file"`
	TimeoutSec   int      `yaml:"timeout_sec"`
	ContentTypes []string `yaml:"content_types"`
}

type LinkPosition struct {
	Name  string `yaml:"name"`
	Match string `yaml:"match"`
}

type ListModeConfig struct {
	RespectRobots bool `yaml:"respect_robots"`
	CrawlDepth    int  `yaml:"crawl_depth"`
}

type AnalysisConfig struct {
	Auto           bool `yaml:"auto"`
	LinkScore      bool `yaml:"link_score"`
	RedirectChains bool `yaml:"redirect_chains"`
	NearDuplicates bool `yaml:"near_duplicates"`
	Pagination     bool `yaml:"pagination"`
	Hreflang       bool `yaml:"hreflang"`
	Canonicals     bool `yaml:"canonicals"`
	Links          bool `yaml:"links"`
	Sitemaps       bool `yaml:"sitemaps"`
	LlmsTxt        bool `yaml:"llms_txt"`
}

type StorageConfig struct {
	Dir           string `yaml:"dir"`
	RetentionDays int    `yaml:"retention_days"`
}

type URLMapping struct {
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace"`
}

type CompareConfig struct {
	ChangeDetection        []string     `yaml:"change_detection"`
	ContentChangeThreshold int          `yaml:"content_change_threshold"`
	URLMapping             []URLMapping `yaml:"url_mapping"`
}
