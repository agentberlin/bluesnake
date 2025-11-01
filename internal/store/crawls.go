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

import (
	"fmt"

	"gorm.io/gorm"
)

// CreateCrawl creates a new crawl for a project
func (s *Store) CreateCrawl(projectID uint, crawlDateTime int64, crawlDuration int64, pagesCrawled int) (*Crawl, error) {
	crawl := Crawl{
		ProjectID:     projectID,
		CrawlDateTime: crawlDateTime,
		CrawlDuration: crawlDuration,
		PagesCrawled:  pagesCrawled,
	}

	if err := s.db.Create(&crawl).Error; err != nil {
		return nil, fmt.Errorf("failed to create crawl: %v", err)
	}

	return &crawl, nil
}

// UpdateCrawlStats updates the crawl statistics
func (s *Store) UpdateCrawlStats(crawlID uint, crawlDuration int64, pagesCrawled int) error {
	return s.db.Model(&Crawl{}).Where("id = ?", crawlID).Updates(map[string]interface{}{
		"crawl_duration": crawlDuration,
		"pages_crawled":  pagesCrawled,
	}).Error
}

// GetCrawlByID gets a crawl by ID
func (s *Store) GetCrawlByID(id uint) (*Crawl, error) {
	var crawl Crawl
	result := s.db.Preload("DiscoveredUrls").First(&crawl, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawl: %v", result.Error)
	}
	return &crawl, nil
}

// GetProjectCrawls returns all crawls for a project ordered by date
func (s *Store) GetProjectCrawls(projectID uint) ([]Crawl, error) {
	var crawls []Crawl
	result := s.db.Where("project_id = ?", projectID).Order("crawl_date_time DESC").Find(&crawls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawls: %v", result.Error)
	}
	return crawls, nil
}

// GetLatestCrawl gets the most recent crawl for a project
func (s *Store) GetLatestCrawl(projectID uint) (*Crawl, error) {
	var crawl Crawl
	result := s.db.Where("project_id = ?", projectID).Order("crawl_date_time DESC").First(&crawl)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest crawl: %v", result.Error)
	}
	return &crawl, nil
}

// SaveDiscoveredUrl saves a discovered URL (whether visited or not)
func (s *Store) SaveDiscoveredUrl(crawlID uint, url string, visited bool, status int, title string, metaDescription string, contentHash string, indexable string, contentType string, errorMsg string) error {
	discoveredUrl := DiscoveredUrl{
		CrawlID:         crawlID,
		URL:             url,
		Visited:         visited,
		Status:          status,
		Title:           title,
		MetaDescription: metaDescription,
		ContentHash:     contentHash,
		Indexable:       indexable,
		ContentType:     contentType,
		Error:           errorMsg,
	}

	return s.db.Create(&discoveredUrl).Error
}

// DeleteCrawl deletes a crawl and all its crawled URLs (cascade)
func (s *Store) DeleteCrawl(crawlID uint) error {
	result := s.db.Delete(&Crawl{}, crawlID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("crawl with ID %d not found", crawlID)
	}
	return nil
}

// GetCrawlResultsPaginated gets paginated discovered URLs for a specific crawl with cursor-based pagination
func (s *Store) GetCrawlResultsPaginated(crawlID uint, limit int, cursor uint, contentTypeFilter string) ([]DiscoveredUrl, uint, bool, error) {
	var urls []DiscoveredUrl

	// Start with base query
	db := s.db.Where("crawl_id = ?", crawlID)

	// Apply cursor for pagination (fetch records with id > cursor)
	if cursor > 0 {
		db = db.Where("id > ?", cursor)
	}

	// Apply content type filter if specified
	if contentTypeFilter != "" && contentTypeFilter != "all" {
		switch contentTypeFilter {
		case "html":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ?)", true, "%text/html%", "%application/xhtml%")
		case "javascript":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)", true,
				"%javascript%", "%application/x-javascript%", "%text/javascript%")
		case "css":
			db = db.Where("visited = ? AND content_type LIKE ?", true, "%text/css%")
		case "image":
			db = db.Where("visited = ? AND content_type LIKE ?", true, "%image/%")
		case "font":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)", true,
				"%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		case "unvisited":
			db = db.Where("visited = ?", false)
		case "other":
			db = db.Where("visited = ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ?", true,
				"%text/html%", "%application/xhtml%", "%javascript%", "%application/x-javascript%", "%text/javascript%", "%text/css%", "%image/%", "%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		}
	}

	// Fetch limit + 1 to check if there are more results
	result := db.Order("id ASC").Limit(limit + 1).Find(&urls)
	if result.Error != nil {
		return nil, 0, false, fmt.Errorf("failed to get paginated crawl results: %v", result.Error)
	}

	// Check if there are more results
	hasMore := len(urls) > limit
	if hasMore {
		urls = urls[:limit] // Trim to requested limit
	}

	// Calculate next cursor (ID of last item)
	var nextCursor uint
	if len(urls) > 0 {
		nextCursor = urls[len(urls)-1].ID
	}

	return urls, nextCursor, hasMore, nil
}

// GetActiveCrawlStats gets statistics for an active crawl without fetching all URLs
func (s *Store) GetActiveCrawlStats(crawlID uint) (map[string]int, error) {
	stats := make(map[string]int)

	// Get total count
	var total int64
	if err := s.db.Model(&DiscoveredUrl{}).Where("crawl_id = ?", crawlID).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count total URLs: %v", err)
	}
	stats["total"] = int(total)

	// Get crawled count (visited = true)
	var crawled int64
	if err := s.db.Model(&DiscoveredUrl{}).Where("crawl_id = ? AND visited = ?", crawlID, true).Count(&crawled).Error; err != nil {
		return nil, fmt.Errorf("failed to count crawled URLs: %v", err)
	}
	stats["crawled"] = int(crawled)

	// Queued = total - crawled
	stats["queued"] = stats["total"] - stats["crawled"]

	// Get HTML count
	var html int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ?)",
			crawlID, true, "%text/html%", "%application/xhtml%").
		Count(&html).Error; err != nil {
		return nil, fmt.Errorf("failed to count HTML URLs: %v", err)
	}
	stats["html"] = int(html)

	// Get JavaScript count
	var javascript int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
			crawlID, true, "%javascript%", "%application/x-javascript%", "%text/javascript%").
		Count(&javascript).Error; err != nil {
		return nil, fmt.Errorf("failed to count JavaScript URLs: %v", err)
	}
	stats["javascript"] = int(javascript)

	// Get CSS count
	var css int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ? AND content_type LIKE ?",
			crawlID, true, "%text/css%").
		Count(&css).Error; err != nil {
		return nil, fmt.Errorf("failed to count CSS URLs: %v", err)
	}
	stats["css"] = int(css)

	// Get images count
	var images int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ? AND content_type LIKE ?",
			crawlID, true, "%image/%").
		Count(&images).Error; err != nil {
		return nil, fmt.Errorf("failed to count image URLs: %v", err)
	}
	stats["images"] = int(images)

	// Get fonts count
	var fonts int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
			crawlID, true, "%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%").
		Count(&fonts).Error; err != nil {
		return nil, fmt.Errorf("failed to count font URLs: %v", err)
	}
	stats["fonts"] = int(fonts)

	// Get unvisited count
	var unvisited int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id = ? AND visited = ?", crawlID, false).
		Count(&unvisited).Error; err != nil {
		return nil, fmt.Errorf("failed to count unvisited URLs: %v", err)
	}
	stats["unvisited"] = int(unvisited)

	// Calculate "others" = visited - (html + js + css + images + fonts)
	stats["others"] = stats["crawled"] - (stats["html"] + stats["javascript"] + stats["css"] + stats["images"] + stats["fonts"])

	return stats, nil
}

// SearchCrawlResultsPaginated searches discovered URLs with pagination
func (s *Store) SearchCrawlResultsPaginated(crawlID uint, query string, contentTypeFilter string, limit int, cursor uint) ([]DiscoveredUrl, uint, bool, error) {
	var urls []DiscoveredUrl

	// Start with base query
	db := s.db.Where("crawl_id = ?", crawlID)

	// Apply cursor for pagination
	if cursor > 0 {
		db = db.Where("id > ?", cursor)
	}

	// Apply content type filter if specified
	if contentTypeFilter != "" && contentTypeFilter != "all" {
		switch contentTypeFilter {
		case "html":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ?)", true, "%text/html%", "%application/xhtml%")
		case "javascript":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)", true,
				"%javascript%", "%application/x-javascript%", "%text/javascript%")
		case "css":
			db = db.Where("visited = ? AND content_type LIKE ?", true, "%text/css%")
		case "image":
			db = db.Where("visited = ? AND content_type LIKE ?", true, "%image/%")
		case "font":
			db = db.Where("visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)", true,
				"%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		case "unvisited":
			db = db.Where("visited = ?", false)
		case "other":
			db = db.Where("visited = ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ?", true,
				"%text/html%", "%application/xhtml%", "%javascript%", "%application/x-javascript%", "%text/javascript%", "%text/css%", "%image/%", "%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		}
	}

	// Apply search query if specified
	if query != "" {
		searchPattern := "%" + query + "%"
		db = db.Where("(url LIKE ? OR title LIKE ? OR CAST(status AS TEXT) LIKE ? OR indexable LIKE ?)",
			searchPattern, searchPattern, searchPattern, searchPattern)
	}

	// Fetch limit + 1 to check if there are more results
	result := db.Order("id ASC").Limit(limit + 1).Find(&urls)
	if result.Error != nil {
		return nil, 0, false, fmt.Errorf("failed to search paginated crawl results: %v", result.Error)
	}

	// Check if there are more results
	hasMore := len(urls) > limit
	if hasMore {
		urls = urls[:limit]
	}

	// Calculate next cursor
	var nextCursor uint
	if len(urls) > 0 {
		nextCursor = urls[len(urls)-1].ID
	}

	return urls, nextCursor, hasMore, nil
}
