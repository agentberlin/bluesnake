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
	ProjectID       uint     `json:"projectId"`
	CrawlID         uint     `json:"crawlId"`
	Domain          string   `json:"domain"`
	URL             string   `json:"url"`
	PagesCrawled    int      `json:"pagesCrawled"`
	TotalDiscovered int      `json:"totalDiscovered"` // Total URLs discovered (both crawled and queued)
	DiscoveredURLs  []string `json:"discoveredUrls"`  // URLs discovered but not yet crawled
	IsCrawling      bool     `json:"isCrawling"`
}

// CrawlResult represents a single crawl result
type CrawlResult struct {
	URL             string `json:"url"`
	Status          int    `json:"status"`
	Title           string `json:"title"`
	MetaDescription string `json:"metaDescription,omitempty"`
	ContentHash     string `json:"contentHash,omitempty"`
	Indexable       string `json:"indexable"`
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
	PagesCrawled  int    `json:"pagesCrawled"`
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

// CrawlResultDetailed represents a crawl with all its URLs
type CrawlResultDetailed struct {
	CrawlInfo CrawlInfo     `json:"crawlInfo"`
	Results   []CrawlResult `json:"results"`
}

// ConfigResponse represents the configuration response for the frontend
type ConfigResponse struct {
	Domain              string   `json:"domain"`
	JSRenderingEnabled  bool     `json:"jsRenderingEnabled"`
	Parallelism         int      `json:"parallelism"`
	UserAgent           string   `json:"userAgent"`
	DiscoveryMechanisms []string `json:"discoveryMechanisms"`
	SitemapURLs         []string `json:"sitemapURLs"`
}

// UpdateInfo contains information about available updates
type UpdateInfo struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
}

// LinkInfo represents link information for the frontend
type LinkInfo struct {
	URL        string `json:"url"`
	AnchorText string `json:"anchorText"`
	Context    string `json:"context,omitempty"`
	IsInternal bool   `json:"isInternal"`
	Status     *int   `json:"status,omitempty"`
	Position   string `json:"position,omitempty"` // Position in page: "content", "navigation", "header", "footer", "sidebar", "breadcrumbs", "pagination", "unknown"
	DOMPath    string `json:"domPath,omitempty"`  // Simplified DOM path showing link's location in HTML structure
}

// PageLinksResponse represents the response for page links
type PageLinksResponse struct {
	PageURL  string     `json:"pageUrl"`
	Inlinks  []LinkInfo `json:"inlinks"`
	Outlinks []LinkInfo `json:"outlinks"`
}
