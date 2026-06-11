package config

// Default returns the full default configuration. Values mirror the
// documented Screaming Frog default where one exists (see docs/research/01),
// except where our house SF setup deliberately deviates — those follow the
// scale.jobs parity audit baseline instead: resources (images/CSS/JS) and
// external links are not fetched, canonical targets are recorded but not
// crawled, XML sitemaps are auto-discovered and crawled, and the user agent
// is the Safari string our SF profile crawls with.
func Default() *Config {
	return &Config{
		Mode: "spider",
		Scope: ScopeConfig{
			CheckLinksOutsideStartFolder: true,
		},
		Resources: ResourcesConfig{
			Images:     StoreCrawl{false, false},
			Media:      StoreCrawl{false, false},
			CSS:        StoreCrawl{false, false},
			JavaScript: StoreCrawl{false, false},
			SWF:        StoreCrawl{false, false},
		},
		Links: LinksConfig{
			Internal:    StoreCrawl{true, true},
			External:    StoreCrawl{false, false},
			Canonicals:  StoreCrawl{true, false},
			Pagination:  StoreCrawl{false, false},
			Hreflang:    StoreCrawl{true, false},
			AMP:         StoreCrawl{false, false},
			MetaRefresh: StoreCrawl{true, true},
			IFrames:     StoreCrawl{true, true},
		},
		Sitemaps: SitemapsConfig{
			CrawlLinked:           true,
			AutoDiscoverViaRobots: true,
		},
		Extraction: ExtractionConfig{
			PageDetails: PageDetailsConfig{
				Titles: true, MetaDescriptions: true, MetaKeywords: true,
				H1: true, H2: true, Indexability: true, WordCount: true,
				Readability: true, TextToCodeRatio: true, Hash: true,
				PageSize: true, Forms: true,
			},
			URLDetails: URLDetailsConfig{
				ResponseTime: true, LastModified: true, HTTPHeaders: true,
			},
			Directives: DirectivesConfig{MetaRobots: true, XRobotsTag: true},
		},
		Limits: LimitsConfig{
			MaxURLs:         5000000,
			MaxDepth:        -1,
			MaxURLsPerDepth: -1,
			MaxFolderDepth:  -1,
			MaxQueryStrings: -1,
			MaxPerSubdomain: -1,
			MaxRedirects:    5,
			MaxURLLength:    10000,
			MaxLinksPerPage: 10000,
			MaxPageSizeKB:   51200,
		},
		Rendering: RenderingConfig{
			Mode:             "text",
			WaitStrategy:     "adaptive",
			AjaxTimeoutSec:   5,
			Window:           "googlebot-desktop",
			FlattenShadowDOM: true,
			FlattenIFrames:   true,
		},
		Advanced: AdvancedConfig{
			CookieStorage:                     "session",
			RespectHSTS:                       true,
			RespectSelfReferencingMetaRefresh: true,
			ResponseTimeoutSec:                20,
			PercentEncoding:                   "upper",
		},
		Thresholds: ThresholdsConfig{
			Title:                WidthThreshold{MinChars: 30, MaxChars: 60, MinPx: 200, MaxPx: 561},
			Description:          WidthThreshold{MinChars: 70, MaxChars: 155, MinPx: 400, MaxPx: 985},
			URLMaxChars:          115,
			H1MaxChars:           70,
			H2MaxChars:           70,
			ImageAltMaxChars:     100,
			ImageMaxKB:           100,
			LowContentWords:      200,
			HighCrawlDepth:       4,
			HighInternalOutlinks: 1000,
			HighExternalOutlinks: 100,
			NonDescriptiveAnchors: []string{
				"click here", "click", "here", "read more", "more",
				"learn more", "go", "this page", "start", "right here",
			},
			Soft404Patterns: []string{"page not found", "404", "not be found"},
		},
		Content: ContentConfig{
			Area: ContentAreaConfig{
				ExcludeElements: []string{"nav", "footer"},
			},
			NearDuplicates: NearDuplicatesConfig{
				Threshold:     90,
				IndexableOnly: true,
			},
		},
		Robots: RobotsConfig{
			Mode:                "respect",
			ShowBlockedInternal: true,
		},
		Speed: SpeedConfig{MaxThreads: 5},
		HTTP: HTTPConfig{
			UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 12_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.4 Safari/605.1.15",
			RobotsUserAgent: "acrawler",
			// Mirror Screaming Frog's default request headers (Accept text/html +
			// no-cache). Some bot-protection layers (Clerk/Vercel) 403 requests
			// that lack a navigational Accept header even when the UA looks like a
			// browser; see internal/fetch for the measured SF profile.
			BrowserHeaders: true,
		},
		LinkPositions: []LinkPosition{
			{Name: "head", Match: "/head"},
			{Name: "nav", Match: "/nav"},
			{Name: "header", Match: "/header"},
			{Name: "sidebar", Match: "/aside"},
			{Name: "footer", Match: "/footer"},
			{Name: "content", Match: "/"},
		},
		StoreLinkPaths: true,
		ListMode:       ListModeConfig{RespectRobots: false, CrawlDepth: 0},
		Analysis: AnalysisConfig{
			Auto: true, LinkScore: true, RedirectChains: true,
			NearDuplicates: true, Pagination: true, Hreflang: true,
			Canonicals: true, Links: true, Sitemaps: true,
		},
		Storage: StorageConfig{Dir: "~/.acrawler"},
		Compare: CompareConfig{
			ChangeDetection: []string{
				"titles", "descriptions", "h1", "word_count",
				"crawl_depth", "links", "structured_data", "content",
			},
			ContentChangeThreshold: 10,
		},
	}
}
