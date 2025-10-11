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
	ID                  uint     `gorm:"primaryKey"`
	ProjectID           uint     `gorm:"uniqueIndex;not null"`
	Domain              string   `gorm:"not null"`
	JSRenderingEnabled  bool     `gorm:"default:false"`
	Parallelism         int      `gorm:"default:5"`
	UserAgent           string   `gorm:"type:text;default:'bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)'"`
	IncludeSubdomains   bool     `gorm:"default:false"` // When true, crawl all subdomains of the project domain
	DiscoveryMechanisms string   `gorm:"type:text;default:'[\"spider\"]'"` // JSON array
	SitemapURLs         string   `gorm:"type:text"`                        // JSON array, nullable
	Project             *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt           int64    `gorm:"autoCreateTime"`
	UpdatedAt           int64    `gorm:"autoUpdateTime"`
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
	ID          uint     `gorm:"primaryKey"`
	URL         string   `gorm:"not null"`                 // Normalized URL for the project
	Domain      string   `gorm:"uniqueIndex;not null"`     // Domain identifier (includes subdomain)
	FaviconPath string   `gorm:"type:text"`                // Path to cached favicon
	Crawls      []Crawl  `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt   int64    `gorm:"autoCreateTime"`
	UpdatedAt   int64    `gorm:"autoUpdateTime"`
}

// Crawl represents a single crawl session for a project
type Crawl struct {
	ID            uint         `gorm:"primaryKey"`
	ProjectID     uint         `gorm:"not null;index"`
	CrawlDateTime int64        `gorm:"not null"` // Unix timestamp
	CrawlDuration int64        `gorm:"not null"` // Duration in milliseconds
	PagesCrawled  int          `gorm:"not null"`
	CrawledUrls   []CrawledUrl `gorm:"foreignKey:CrawlID;constraint:OnDelete:CASCADE"`
	CreatedAt     int64        `gorm:"autoCreateTime"`
	UpdatedAt     int64        `gorm:"autoUpdateTime"`
}

// CrawledUrl represents a single URL that was crawled
type CrawledUrl struct {
	ID              uint   `gorm:"primaryKey"`
	CrawlID         uint   `gorm:"not null;index"`
	URL             string `gorm:"not null"`
	Status          int    `gorm:"not null"`
	Title           string `gorm:"type:text"`
	MetaDescription string `gorm:"type:text"`
	ContentHash     string `gorm:"type:text;index"`
	Indexable       string `gorm:"not null"`
	Error           string `gorm:"type:text"`
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
}
