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
	result := s.db.Preload("CrawledUrls").First(&crawl, id)
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

// GetCrawlResults gets all crawled URLs for a specific crawl
func (s *Store) GetCrawlResults(crawlID uint) ([]CrawledUrl, error) {
	var urls []CrawledUrl
	result := s.db.Where("crawl_id = ?", crawlID).Order("id ASC").Find(&urls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawl results: %v", result.Error)
	}
	return urls, nil
}

// SaveCrawledUrl saves a crawled URL result
func (s *Store) SaveCrawledUrl(crawlID uint, url string, status int, title string, metaDescription string, contentHash string, indexable string, contentType string, errorMsg string) error {
	crawledUrl := CrawledUrl{
		CrawlID:         crawlID,
		URL:             url,
		Status:          status,
		Title:           title,
		MetaDescription: metaDescription,
		ContentHash:     contentHash,
		Indexable:       indexable,
		ContentType:     contentType,
		Error:           errorMsg,
	}

	return s.db.Create(&crawledUrl).Error
}

// DeleteCrawl deletes a crawl and all its crawled URLs (cascade)
func (s *Store) DeleteCrawl(crawlID uint) error {
	return s.db.Delete(&Crawl{}, crawlID).Error
}

// SearchCrawlResults searches crawled URLs for a specific crawl with filtering
func (s *Store) SearchCrawlResults(crawlID uint, query string, contentTypeFilter string) ([]CrawledUrl, error) {
	var urls []CrawledUrl

	// Start with base query
	db := s.db.Where("crawl_id = ?", crawlID)

	// Apply content type filter if specified
	if contentTypeFilter != "" && contentTypeFilter != "all" {
		switch contentTypeFilter {
		case "html":
			db = db.Where("(content_type LIKE ? OR content_type LIKE ?)", "%text/html%", "%application/xhtml%")
		case "javascript":
			db = db.Where("(content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
				"%javascript%", "%application/x-javascript%", "%text/javascript%")
		case "css":
			db = db.Where("content_type LIKE ?", "%text/css%")
		case "image":
			db = db.Where("content_type LIKE ?", "%image/%")
		case "font":
			db = db.Where("(content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
				"%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		case "other":
			// Other = not html, not js, not css, not image, not font
			db = db.Where("content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ? AND content_type NOT LIKE ?",
				"%text/html%", "%application/xhtml%", "%javascript%", "%application/x-javascript%", "%text/javascript%", "%text/css%", "%image/%", "%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%")
		}
	}

	// Apply search query if specified
	if query != "" {
		searchPattern := "%" + query + "%"
		db = db.Where("(url LIKE ? OR title LIKE ? OR CAST(status AS TEXT) LIKE ? OR indexable LIKE ?)",
			searchPattern, searchPattern, searchPattern, searchPattern)
	}

	result := db.Order("id ASC").Find(&urls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to search crawl results: %v", result.Error)
	}

	return urls, nil
}
