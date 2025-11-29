// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package types

// CrawlProgress represents the progress of an active crawl
type CrawlProgress struct {
	ProjectID        uint     `json:"projectId"`
	CrawlID          uint     `json:"crawlId"`
	Domain           string   `json:"domain"`
	URL              string   `json:"url"`
	PagesCrawled     int      `json:"pagesCrawled"`     // HTML pages crawled successfully
	TotalURLsCrawled int      `json:"totalUrlsCrawled"` // Total URLs crawled (including resources)
	TotalDiscovered  int      `json:"totalDiscovered"`  // Total URLs discovered (both crawled and queued)
	DiscoveredURLs   []string `json:"discoveredUrls"`   // URLs discovered but not yet crawled
	IsCrawling       bool     `json:"isCrawling"`
}

// CrawlResult represents a single crawl result
type CrawlResult struct {
	URL             string `json:"url"`
	Status          int    `json:"status"`
	Title           string `json:"title"`
	MetaDescription string `json:"metaDescription,omitempty"`
	ContentHash     string `json:"contentHash,omitempty"`
	Indexable       string `json:"indexable"`
	ContentType     string `json:"contentType,omitempty"` // MIME type: text/html, image/jpeg, text/css, application/javascript, etc.
	Error           string `json:"error,omitempty"`
}

// ProjectInfo represents project information for the frontend
type ProjectInfo struct {
	ID            uint   `json:"id"`
	URL           string `json:"url"`
	Domain        string `json:"domain"`
	FaviconPath   string `json:"faviconPath"`
	CrawlDateTime int64  `json:"crawlDateTime"`
	CrawlDuration int64  `json:"crawlDuration"`
	PagesCrawled  int    `json:"pagesCrawled"` // Number of HTML pages crawled
	TotalURLs     int    `json:"totalUrls"`    // Total number of URLs discovered (including all resources)
	LatestCrawlID uint   `json:"latestCrawlId"`
}

// CrawlInfo represents crawl information for the frontend
type CrawlInfo struct {
	ID            uint  `json:"id"`
	ProjectID     uint  `json:"projectId"`
	CrawlDateTime int64 `json:"crawlDateTime"`
	CrawlDuration int64 `json:"crawlDuration"`
	PagesCrawled  int   `json:"pagesCrawled"`
}

// ConfigResponse represents the configuration response for the frontend
type ConfigResponse struct {
	Domain                   string   `json:"domain"`
	JSRenderingEnabled       bool     `json:"jsRenderingEnabled"`
	InitialWaitMs            int      `json:"initialWaitMs"`
	ScrollWaitMs             int      `json:"scrollWaitMs"`
	FinalWaitMs              int      `json:"finalWaitMs"`
	Parallelism              int      `json:"parallelism"`
	UserAgent                string   `json:"userAgent"`
	IncludeSubdomains        bool     `json:"includeSubdomains"`
	DiscoveryMechanisms      []string `json:"discoveryMechanisms"`
	SitemapURLs              []string `json:"sitemapURLs"`
	CheckExternalResources   bool     `json:"checkExternalResources"`
	RobotsTxtMode            string   `json:"robotsTxtMode"`            // "respect", "ignore", or "ignore-report"
	FollowInternalNofollow   bool     `json:"followInternalNofollow"`   // Follow internal links with rel="nofollow"
	FollowExternalNofollow   bool     `json:"followExternalNofollow"`   // Follow external links with rel="nofollow"
	RespectMetaRobotsNoindex bool     `json:"respectMetaRobotsNoindex"` // Respect <meta name="robots" content="noindex">
	RespectNoindex           bool     `json:"respectNoindex"`           // Respect X-Robots-Tag: noindex headers
}

// VersionRule represents a version-specific rule (warning or block)
type VersionRule struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

// VersionManifest represents the version.json structure from R2
type VersionManifest struct {
	LatestVersion    string        `json:"latestVersion"`
	WarnBelow        string        `json:"warnBelow,omitempty"`
	WarnBelowReason  string        `json:"warnBelowReason,omitempty"`
	BlockBelow       string        `json:"blockBelow,omitempty"`
	BlockBelowReason string        `json:"blockBelowReason,omitempty"`
	WarnVersions     []VersionRule `json:"warnVersions,omitempty"`
	BlockVersions    []VersionRule `json:"blockVersions,omitempty"`
}

// UpdateInfo contains information about available updates
type UpdateInfo struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
	ShouldWarn      bool   `json:"shouldWarn"`
	ShouldBlock     bool   `json:"shouldBlock"`
	DisplayReason   string `json:"displayReason,omitempty"`
}

// LinkInfo represents link information for the frontend
type LinkInfo struct {
	URL        string `json:"url"`
	LinkType   string `json:"linkType"`           // Type: "anchor", "image", "script", "stylesheet", etc.
	AnchorText string `json:"anchorText"`
	Context    string `json:"context,omitempty"`
	IsInternal bool   `json:"isInternal"`
	Status     *int   `json:"status,omitempty"`
	Position   string `json:"position,omitempty"` // Position in page: "content", "navigation", "header", "footer", "sidebar", "breadcrumbs", "pagination", "unknown"
	DOMPath    string `json:"domPath,omitempty"`  // Simplified DOM path showing link's location in HTML structure
	URLAction  string `json:"urlAction"`          // Action: "crawl" (normal), "record" (framework-specific), "skip" (ignored)
}

// PageLinksResponse represents the response for page links
type PageLinksResponse struct {
	PageURL  string     `json:"pageUrl"`
	Inlinks  []LinkInfo `json:"inlinks"`
	Outlinks []LinkInfo `json:"outlinks"`
}

// AICrawlerData represents AI crawler check results
type AICrawlerData struct {
	ContentVisibility *ContentVisibilityResult `json:"contentVisibility,omitempty"`
	RobotsTxt         map[string]BotAccess     `json:"robotsTxt,omitempty"`
	HTTPCheck         map[string]BotAccess     `json:"httpCheck,omitempty"`
	CheckedAt         int64                    `json:"checkedAt"` // Unix timestamp
}

// ContentVisibilityResult represents SSR check results
type ContentVisibilityResult struct {
	Score      float64 `json:"score"`
	StatusCode int     `json:"statusCode"`
	IsError    bool    `json:"isError"`
}

// BotAccess represents bot access information
type BotAccess struct {
	Allowed bool   `json:"allowed"`
	Domain  string `json:"domain"`
	Message string `json:"message,omitempty"`
}

// AICrawlerResponse represents the complete AI crawler response for frontend
type AICrawlerResponse struct {
	Data           *AICrawlerData `json:"data"`
	SSRScreenshot  string         `json:"ssrScreenshot,omitempty"`  // Base64 or path
	JSScreenshot   string         `json:"jsScreenshot,omitempty"`   // Base64 or path
	NoJSScreenshot string         `json:"noJSScreenshot,omitempty"` // Base64 or path
}

// CrawlResultPaginated represents a paginated crawl result response
type CrawlResultPaginated struct {
	Results    []CrawlResult `json:"results"`
	NextCursor uint          `json:"nextCursor"`
	HasMore    bool          `json:"hasMore"`
}

// ActiveCrawlStats represents statistics for an active crawl (no URL list, just counts)
type ActiveCrawlStats struct {
	CrawlID        uint `json:"crawlId"`
	Total          int  `json:"total"`          // Total URLs (crawled + queued)
	Crawled        int  `json:"crawled"`        // URLs that have been crawled
	Queued         int  `json:"queued"`         // URLs discovered but not yet crawled
	HTML           int  `json:"html"`           // Count of HTML pages
	JavaScript     int  `json:"javascript"`     // Count of JS files
	CSS            int  `json:"css"`            // Count of CSS files
	Images         int  `json:"images"`         // Count of images
	Fonts          int  `json:"fonts"`          // Count of fonts
	Unvisited      int  `json:"unvisited"`      // Count of unvisited URLs
	Others         int  `json:"others"`         // Count of other content types
}

// SystemHealthCheck represents the result of a system health check
type SystemHealthCheck struct {
	IsHealthy   bool   `json:"isHealthy"`   // Overall health status
	ErrorTitle  string `json:"errorTitle"`  // Error title if not healthy
	ErrorMsg    string `json:"errorMsg"`    // Detailed error message
	Suggestion  string `json:"suggestion"`  // Suggestion to fix the issue
}
