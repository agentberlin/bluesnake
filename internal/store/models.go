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

package store

import "encoding/json"

// Config represents the crawl configuration for a domain
type Config struct {
	ID                     uint     `gorm:"primaryKey"`
	ProjectID              uint     `gorm:"uniqueIndex;not null"`
	Domain                 string   `gorm:"not null"`
	JSRenderingEnabled     bool     `gorm:"default:false"`
	InitialWaitMs          int      `gorm:"default:1500"` // Initial wait after page load for JS frameworks to hydrate (in milliseconds)
	ScrollWaitMs           int      `gorm:"default:2000"` // Wait after scrolling to bottom for lazy-loaded content (in milliseconds)
	FinalWaitMs            int      `gorm:"default:1000"` // Final wait before capturing HTML (in milliseconds)
	Parallelism            int      `gorm:"default:5"`
	RequestTimeoutSecs     int      `gorm:"default:20"` // HTTP request timeout in seconds (matches ScreamingFrog default)
	UserAgent              string   `gorm:"type:text;default:'bluesnake/1.0 (+https://snake.blue)'"`
	IncludeSubdomains      bool     `gorm:"default:false"`                                // When true, crawl all subdomains of the project domain
	DiscoveryMechanisms    string   `gorm:"type:text;default:'[\"spider\",\"sitemap\"]'"` // JSON array
	SitemapURLs            string   `gorm:"type:text"`                                    // JSON array, nullable
	CheckExternalResources bool     `gorm:"default:true"`                                 // When true, validate external resources for broken links
	// Crawler directive configuration (robots.txt, nofollow, noindex, etc.)
	RobotsTxtMode            string `gorm:"default:'respect'"`  // "respect", "ignore", or "ignore-report"
	FollowInternalNofollow   bool   `gorm:"default:false"`      // When true, follow links with rel="nofollow" on same domain
	FollowExternalNofollow   bool   `gorm:"default:false"`      // When true, follow links with rel="nofollow" on external domains
	RespectMetaRobotsNoindex bool   `gorm:"default:true"`       // When true, respect <meta name="robots" content="noindex">
	RespectNoindex           bool   `gorm:"default:true"`       // When true, respect X-Robots-Tag: noindex headers
	// Incremental crawling configuration
	IncrementalCrawlingEnabled bool `gorm:"default:false"`      // When true, crawl in chunks and allow resume
	CrawlBudget                int  `gorm:"default:0"`          // Max URLs to crawl per session (0 = unlimited)
	Project                  *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt                int64  `gorm:"autoCreateTime"`
	UpdatedAt                int64  `gorm:"autoUpdateTime"`
}

// GetDiscoveryMechanismsArray deserializes the DiscoveryMechanisms JSON to []string
func (c *Config) GetDiscoveryMechanismsArray() []string {
	if c.DiscoveryMechanisms == "" {
		return []string{"spider"} // Default
	}
	var mechanisms []string
	if err := json.Unmarshal([]byte(c.DiscoveryMechanisms), &mechanisms); err != nil {
		return []string{"spider"} // Default on error
	}
	return mechanisms
}

// SetDiscoveryMechanismsArray serializes []string to JSON for DiscoveryMechanisms
func (c *Config) SetDiscoveryMechanismsArray(mechanisms []string) error {
	data, err := json.Marshal(mechanisms)
	if err != nil {
		return err
	}
	c.DiscoveryMechanisms = string(data)
	return nil
}

// GetSitemapURLsArray deserializes the SitemapURLs JSON to []string
// Returns nil if empty (which means use defaults)
func (c *Config) GetSitemapURLsArray() []string {
	if c.SitemapURLs == "" || c.SitemapURLs == "null" {
		return nil // nil means use defaults
	}
	var urls []string
	if err := json.Unmarshal([]byte(c.SitemapURLs), &urls); err != nil {
		return nil // Return nil on error
	}
	if len(urls) == 0 {
		return nil // Empty array = nil (use defaults)
	}
	return urls
}

// SetSitemapURLsArray serializes []string to JSON for SitemapURLs
func (c *Config) SetSitemapURLsArray(urls []string) error {
	if urls == nil || len(urls) == 0 {
		c.SitemapURLs = "" // Empty = use defaults
		return nil
	}
	data, err := json.Marshal(urls)
	if err != nil {
		return err
	}
	c.SitemapURLs = string(data)
	return nil
}

// Project represents a project (base URL) that can have multiple crawls
type Project struct {
	ID             uint    `gorm:"primaryKey"`
	URL            string  `gorm:"not null"`             // Normalized URL for the project
	Domain         string  `gorm:"uniqueIndex;not null"` // Domain identifier (includes subdomain)
	FaviconPath    string  `gorm:"type:text"`            // Path to cached favicon
	AICrawlerData  string  `gorm:"type:text"`            // JSON data for AI Crawler results
	SSRScreenshot  string  `gorm:"type:text"`            // Path to SSR screenshot
	JSScreenshot   string  `gorm:"type:text"`            // Path to JS-enabled screenshot
	NoJSScreenshot string  `gorm:"type:text"`            // Path to JS-disabled screenshot
	Crawls         []Crawl `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt      int64   `gorm:"autoCreateTime"`
	UpdatedAt      int64   `gorm:"autoUpdateTime"`
}

// Run state constants for IncrementalCrawlRun
const (
	RunStateInProgress = "in_progress" // A crawl is currently running
	RunStatePaused     = "paused"      // Budget hit, more URLs to crawl
	RunStateCompleted  = "completed"   // All URLs crawled or manually completed
)

// IncrementalCrawlRun groups multiple crawls in a single incremental run.
// When incremental crawling is enabled, each "run" can contain multiple crawl sessions
// that together form a complete crawl of the site.
type IncrementalCrawlRun struct {
	ID        uint     `gorm:"primaryKey"`
	ProjectID uint     `gorm:"not null;index"`
	State     string   `gorm:"not null;default:'in_progress'"` // in_progress, paused, completed
	Project   *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	Crawls    []Crawl  `gorm:"foreignKey:RunID"`
	CreatedAt int64    `gorm:"autoCreateTime"`
	UpdatedAt int64    `gorm:"autoUpdateTime"`
}

// Crawl state constants
// Note: CrawlStatePaused is deprecated - paused state now lives at the run level
const (
	CrawlStateInProgress = "in_progress"
	CrawlStatePaused     = "paused" // Deprecated: use RunStatePaused on IncrementalCrawlRun instead
	CrawlStateCompleted  = "completed"
	CrawlStateFailed     = "failed"
)

// Crawl represents a single crawl session for a project
type Crawl struct {
	ID             uint                 `gorm:"primaryKey"`
	ProjectID      uint                 `gorm:"not null;index"`
	RunID          *uint                `gorm:"index"` // nullable FK to IncrementalCrawlRun (null for non-incremental crawls)
	CrawlDateTime  int64                `gorm:"not null"`
	CrawlDuration  int64                `gorm:"not null"`
	PagesCrawled   int                  `gorm:"not null"`
	State          string               `gorm:"not null;default:'completed'"` // in_progress, completed, failed
	DiscoveredUrls []DiscoveredUrl      `gorm:"foreignKey:CrawlID;constraint:OnDelete:CASCADE"`
	Run            *IncrementalCrawlRun `gorm:"foreignKey:RunID"`
	CreatedAt      int64                `gorm:"autoCreateTime"`
	UpdatedAt      int64                `gorm:"autoUpdateTime"`
}

// DiscoveredUrl represents a single URL that was discovered during crawling
// This includes both URLs that were visited/crawled and URLs that were discovered but not visited (e.g., framework-specific URLs)
type DiscoveredUrl struct {
	ID              uint   `gorm:"primaryKey"`
	CrawlID         uint   `gorm:"not null;index"`
	URL             string `gorm:"not null"`
	Visited         bool   `gorm:"not null;default:false;index"` // true = URL was crawled/visited, false = discovered but not visited
	Status          int    `gorm:"not null"`
	Title           string `gorm:"type:text"`
	MetaDescription string `gorm:"type:text"`
	H1              string `gorm:"type:text"`
	H2              string `gorm:"type:text"`
	CanonicalURL    string `gorm:"type:text"`
	WordCount       int    `gorm:"default:0"`
	ContentHash     string `gorm:"type:text;index"`
	Indexable       string `gorm:"not null"`
	ContentType     string `gorm:"type:text"` // MIME type: text/html, image/jpeg, text/css, application/javascript, etc.
	Error           string `gorm:"type:text"`
	Depth           int    `gorm:"default:0"` // Crawl depth (0 = start URL, 1 = discovered from start, etc.)
	CreatedAt       int64  `gorm:"autoCreateTime"`
}

// PageLink represents a link between two pages
type PageLink struct {
	ID          uint   `gorm:"primaryKey"`
	CrawlID     uint   `gorm:"not null;index:idx_crawl_source;index:idx_crawl_target"`
	SourceURL   string `gorm:"not null;index:idx_crawl_source"` // Page containing the link
	TargetURL   string `gorm:"not null;index:idx_crawl_target"` // Page being linked to
	LinkType    string `gorm:"not null"`                        // "anchor", "image", "script", etc.
	LinkText    string `gorm:"type:text"`                       // anchor text or alt text
	LinkContext string `gorm:"type:text"`                       // surrounding text context
	IsInternal  bool   `gorm:"not null"`                        // internal vs external
	Status      int    `gorm:"default:0"`                       // HTTP status of target (0 if not crawled)
	Title       string `gorm:"type:text"`                       // Title of target page (if crawled)
	ContentType string `gorm:"type:text"`                       // Content type of target (if crawled)
	Position    string `gorm:"type:text"`                       // Position: "content", "navigation", "header", "footer", "sidebar", "breadcrumbs", "pagination", "unknown"
	DOMPath     string `gorm:"type:text"`                       // Simplified DOM path showing link's location in HTML structure
	URLAction   string `gorm:"type:text;index"`                 // Action: "crawl" (normal), "record" (framework-specific), "skip" (ignored)
	Follow      bool   `gorm:"not null"`                        // true if no nofollow/sponsored/ugc in rel attribute
	Rel         string `gorm:"type:text"`                       // Full rel attribute value (e.g., "nofollow", "noopener noreferrer")
	Target      string `gorm:"type:text"`                       // Target attribute (_blank, _self, etc.)
	PathType    string `gorm:"type:text"`                       // Path type: "Absolute", "Root-Relative", or "Relative"
	CreatedAt   int64  `gorm:"autoCreateTime"`
}

// PageLinkData is a simplified structure for passing link data
type PageLinkData struct {
	URL         string
	Type        string
	Text        string
	Context     string
	IsInternal  bool
	Status      int
	Title       string
	ContentType string
	Position    string
	DOMPath     string
	URLAction   string
	Follow      bool
	Rel         string
	Target      string
	PathType    string
}

// DomainFramework represents the detected web framework for a specific domain
type DomainFramework struct {
	ID          uint     `gorm:"primaryKey"`
	ProjectID   uint     `gorm:"not null;index:idx_project_domain"`
	Domain      string   `gorm:"not null;index:idx_project_domain;index:idx_domain"`
	Framework   string   `gorm:"not null"`
	ManuallySet bool     `gorm:"default:false"`
	Project     *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt   int64    `gorm:"autoCreateTime"`
	UpdatedAt   int64    `gorm:"autoUpdateTime"`
}

// Unique constraint on (ProjectID, Domain)
func (DomainFramework) TableName() string {
	return "domain_frameworks"
}

// CrawlQueueItem stores URLs discovered for a project across crawl sessions.
// This is the persistent queue that survives between crawl sessions for incremental crawling.
type CrawlQueueItem struct {
	ID        uint     `gorm:"primaryKey"`
	ProjectID uint     `gorm:"not null;index:idx_queue_project_visited"`
	URL       string   `gorm:"not null"`
	URLHash   int64    `gorm:"not null;index:idx_queue_hash"` // Stored as int64 for SQLite compatibility
	Source    string   `gorm:"not null"` // initial, spider, sitemap, network, resource
	Depth     int      `gorm:"not null;default:0"`
	Visited   bool     `gorm:"not null;default:false;index:idx_queue_project_visited"`
	Project   *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt int64    `gorm:"autoCreateTime"`
	UpdatedAt int64    `gorm:"autoUpdateTime"`
}

// TableName returns the table name for CrawlQueueItem
func (CrawlQueueItem) TableName() string {
	return "crawl_queue_items"
}
