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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetPendingURLs returns all unvisited URLs from the crawl queue for a project.
// These are URLs that were discovered but not yet crawled.
func (s *Store) GetPendingURLs(projectID uint) ([]CrawlQueueItem, error) {
	var items []CrawlQueueItem
	if err := s.db.Where("project_id = ? AND visited = ?", projectID, false).
		Order("id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// GetVisitedURLHashes returns the hashes of all visited URLs for a project.
// Used when resuming a crawl to pre-populate the visited set.
// Returns int64 because SQLite stores hashes as signed integers.
func (s *Store) GetVisitedURLHashes(projectID uint) ([]int64, error) {
	var hashes []int64
	if err := s.db.Model(&CrawlQueueItem{}).
		Where("project_id = ? AND visited = ?", projectID, true).
		Pluck("url_hash", &hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}

// MarkURLsVisited marks the given URLs as visited in the queue.
// Uses URL matching to find and update the records.
func (s *Store) MarkURLsVisited(projectID uint, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	return s.db.Model(&CrawlQueueItem{}).
		Where("project_id = ? AND url IN ?", projectID, urls).
		Update("visited", true).Error
}

// AddAndMarkVisited adds a URL to the queue and marks it as visited in one operation.
// Uses upsert: if URL exists, marks it visited; if not, creates it with visited=true.
// This ensures ALL crawled URLs are tracked in the queue, regardless of discovery source
// (spider, sitemap, redirects, etc.).
func (s *Store) AddAndMarkVisited(projectID uint, url string, urlHash int64, source string) error {
	item := CrawlQueueItem{
		ProjectID: projectID,
		URL:       url,
		URLHash:   urlHash,
		Source:    source,
		Depth:     0,
		Visited:   true,
	}

	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "url"}},
		DoUpdates: clause.AssignmentColumns([]string{"visited", "updated_at"}),
	}).Create(&item).Error
}

// AddToQueue adds URLs to the crawl queue for a project.
// Uses upsert to handle duplicates - existing URLs are not modified.
// Batches inserts to avoid SQLite "too many SQL variables" error.
func (s *Store) AddToQueue(projectID uint, items []CrawlQueueItem) error {
	if len(items) == 0 {
		return nil
	}

	// Ensure projectID is set on all items
	for i := range items {
		items[i].ProjectID = projectID
	}

	// SQLite has a limit on SQL variables (typically 999).
	// CrawlQueueItem has ~9 columns, so batch size of 100 is safe.
	const batchSize = 100

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[i:end]

		if err := s.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "project_id"}, {Name: "url"}},
			DoNothing: true,
		}).Create(&batch).Error; err != nil {
			return err
		}
	}

	return nil
}

// AddSingleToQueue adds a single URL to the crawl queue.
// Uses upsert to handle duplicates.
func (s *Store) AddSingleToQueue(projectID uint, url string, urlHash int64, source string, depth int) error {
	item := CrawlQueueItem{
		ProjectID: projectID,
		URL:       url,
		URLHash:   urlHash,
		Source:    source,
		Depth:     depth,
		Visited:   false,
	}

	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "url"}},
		DoNothing: true,
	}).Create(&item).Error
}

// ClearQueue removes all URLs from the crawl queue for a project.
// Used when starting a fresh crawl or disabling incremental crawling.
func (s *Store) ClearQueue(projectID uint) error {
	return s.db.Where("project_id = ?", projectID).Delete(&CrawlQueueItem{}).Error
}

// QueueStats contains statistics about the crawl queue.
type QueueStats struct {
	Visited int64
	Pending int64
	Total   int64
}

// GetQueueStats returns statistics about the crawl queue for a project.
func (s *Store) GetQueueStats(projectID uint) (*QueueStats, error) {
	var visited, pending int64

	// Count visited URLs
	if err := s.db.Model(&CrawlQueueItem{}).
		Where("project_id = ? AND visited = ?", projectID, true).
		Count(&visited).Error; err != nil {
		return nil, err
	}

	// Count pending URLs
	if err := s.db.Model(&CrawlQueueItem{}).
		Where("project_id = ? AND visited = ?", projectID, false).
		Count(&pending).Error; err != nil {
		return nil, err
	}

	return &QueueStats{
		Visited: visited,
		Pending: pending,
		Total:   visited + pending,
	}, nil
}

// HasPendingURLs checks if there are any unvisited URLs in the queue.
func (s *Store) HasPendingURLs(projectID uint) (bool, error) {
	var count int64
	if err := s.db.Model(&CrawlQueueItem{}).
		Where("project_id = ? AND visited = ?", projectID, false).
		Limit(1).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetQueueItemByURL retrieves a queue item by its URL.
func (s *Store) GetQueueItemByURL(projectID uint, url string) (*CrawlQueueItem, error) {
	var item CrawlQueueItem
	if err := s.db.Where("project_id = ? AND url = ?", projectID, url).
		First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}
