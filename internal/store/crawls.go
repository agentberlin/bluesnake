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
	"time"

	"gorm.io/gorm"
)

// CreateCrawl creates a new crawl for a project
func (s *Store) CreateCrawl(projectID uint, crawlDateTime int64, crawlDuration int64, pagesCrawled int) (*Crawl, error) {
	crawl := Crawl{
		ProjectID:     projectID,
		CrawlDateTime: crawlDateTime,
		CrawlDuration: crawlDuration,
		PagesCrawled:  pagesCrawled,
		State:         CrawlStateInProgress,
	}

	if err := s.db.Create(&crawl).Error; err != nil {
		return nil, fmt.Errorf("failed to create crawl: %v", err)
	}

	return &crawl, nil
}

// CreateCrawlWithState creates a new crawl for a project with a specified initial state
func (s *Store) CreateCrawlWithState(projectID uint, crawlDateTime int64, crawlDuration int64, pagesCrawled int, state string) (*Crawl, error) {
	crawl := Crawl{
		ProjectID:     projectID,
		CrawlDateTime: crawlDateTime,
		CrawlDuration: crawlDuration,
		PagesCrawled:  pagesCrawled,
		State:         state,
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

// UpdateCrawlState updates the state of a crawl
func (s *Store) UpdateCrawlState(crawlID uint, state string) error {
	return s.db.Model(&Crawl{}).Where("id = ?", crawlID).Update("state", state).Error
}

// UpdateCrawlStatsAndState updates crawl statistics and state in one operation
func (s *Store) UpdateCrawlStatsAndState(crawlID uint, crawlDuration int64, pagesCrawled int, state string) error {
	return s.db.Model(&Crawl{}).Where("id = ?", crawlID).Updates(map[string]interface{}{
		"crawl_duration": crawlDuration,
		"pages_crawled":  pagesCrawled,
		"state":          state,
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
func (s *Store) SaveDiscoveredUrl(crawlID uint, url string, visited bool, status int, title string, metaDescription string, h1 string, h2 string, canonicalURL string, wordCount int, contentHash string, indexable string, contentType string, errorMsg string, depth int) error {
	discoveredUrl := DiscoveredUrl{
		CrawlID:         crawlID,
		URL:             url,
		Visited:         visited,
		Status:          status,
		Title:           title,
		MetaDescription: metaDescription,
		H1:              h1,
		H2:              h2,
		CanonicalURL:    canonicalURL,
		WordCount:       wordCount,
		ContentHash:     contentHash,
		Indexable:       indexable,
		ContentType:     contentType,
		Error:           errorMsg,
		Depth:           depth,
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


// ============================================================================
// IncrementalCrawlRun Functions
// ============================================================================

// CreateIncrementalRun creates a new incremental crawl run for a project
func (s *Store) CreateIncrementalRun(projectID uint) (*IncrementalCrawlRun, error) {
	run := IncrementalCrawlRun{
		ProjectID: projectID,
		State:     RunStateInProgress,
	}
	if err := s.db.Create(&run).Error; err != nil {
		return nil, fmt.Errorf("failed to create incremental run: %v", err)
	}
	return &run, nil
}

// GetPausedRun returns the most recent paused run for a project, if any
func (s *Store) GetPausedRun(projectID uint) (*IncrementalCrawlRun, error) {
	var run IncrementalCrawlRun
	result := s.db.Where("project_id = ? AND state = ?", projectID, RunStatePaused).
		Order("created_at DESC").First(&run)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get paused run: %v", result.Error)
	}
	return &run, nil
}

// GetInProgressRun returns the in-progress run for a project, if any
func (s *Store) GetInProgressRun(projectID uint) (*IncrementalCrawlRun, error) {
	var run IncrementalCrawlRun
	result := s.db.Where("project_id = ? AND state = ?", projectID, RunStateInProgress).First(&run)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get in-progress run: %v", result.Error)
	}
	return &run, nil
}

// UpdateRunState updates the state of an incremental run
func (s *Store) UpdateRunState(runID uint, state string) error {
	return s.db.Model(&IncrementalCrawlRun{}).Where("id = ?", runID).Update("state", state).Error
}

// CreateCrawlWithRun creates a new crawl associated with an incremental run
func (s *Store) CreateCrawlWithRun(projectID uint, runID uint) (*Crawl, error) {
	crawl := Crawl{
		ProjectID:     projectID,
		RunID:         &runID,
		CrawlDateTime: time.Now().Unix(),
		CrawlDuration: 0,
		PagesCrawled:  0,
		State:         CrawlStateInProgress,
	}
	if err := s.db.Create(&crawl).Error; err != nil {
		return nil, fmt.Errorf("failed to create crawl with run: %v", err)
	}
	return &crawl, nil
}

// GetRunCrawls returns all crawls for a run ordered by date
func (s *Store) GetRunCrawls(runID uint) ([]Crawl, error) {
	var crawls []Crawl
	result := s.db.Where("run_id = ?", runID).Order("crawl_date_time ASC").Find(&crawls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get run crawls: %v", result.Error)
	}
	return crawls, nil
}

// GetRunWithCrawls returns a run with all its crawls preloaded
func (s *Store) GetRunWithCrawls(runID uint) (*IncrementalCrawlRun, error) {
	var run IncrementalCrawlRun
	result := s.db.Preload("Crawls").First(&run, runID)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get run with crawls: %v", result.Error)
	}
	return &run, nil
}

// GetRunByID returns a run by its ID
func (s *Store) GetRunByID(runID uint) (*IncrementalCrawlRun, error) {
	var run IncrementalCrawlRun
	result := s.db.First(&run, runID)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get run: %v", result.Error)
	}
	return &run, nil
}

// GetActiveOrPausedRun returns the most recent in-progress or paused run for a project
func (s *Store) GetActiveOrPausedRun(projectID uint) (*IncrementalCrawlRun, error) {
	var run IncrementalCrawlRun
	result := s.db.Where("project_id = ? AND state IN (?, ?)", projectID, RunStateInProgress, RunStatePaused).
		Order("created_at DESC").First(&run)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get active or paused run: %v", result.Error)
	}
	return &run, nil
}

// ============================================================================
// Crawl History & Aggregation Functions
// ============================================================================

// CrawlHistoryEntry represents a unified crawl history entry for the frontend.
// For runs, this aggregates multiple crawl sessions into one entry.
// For standalone crawls, this represents a single crawl.
type CrawlHistoryEntry struct {
	ID            uint   // The crawl ID to use for fetching results (latest crawl in run, or the crawl itself)
	ProjectID     uint
	CrawlDateTime int64  // When the crawl/run started
	CrawlDuration int64  // Total duration across all sessions
	PagesCrawled  int    // Total pages across all sessions
	State         string // Current state (from run if incremental, from crawl if standalone)
}

// GetCrawlHistory returns a deduplicated crawl history for a project.
// Runs are aggregated into single entries, standalone crawls are returned as-is.
func (s *Store) GetCrawlHistory(projectID uint) ([]CrawlHistoryEntry, error) {
	var entries []CrawlHistoryEntry

	// First, get all runs for this project with their aggregated stats
	type runStats struct {
		RunID         uint
		State         string
		FirstCrawlAt  int64
		TotalDuration int64
		TotalPages    int
		LatestCrawlID uint
	}
	var runs []runStats
	if err := s.db.Model(&Crawl{}).
		Select(`
			run_id,
			MIN(crawl_date_time) as first_crawl_at,
			SUM(crawl_duration) as total_duration,
			SUM(pages_crawled) as total_pages,
			MAX(id) as latest_crawl_id
		`).
		Where("project_id = ? AND run_id IS NOT NULL", projectID).
		Group("run_id").
		Find(&runs).Error; err != nil {
		return nil, fmt.Errorf("failed to get run stats: %v", err)
	}

	// Get run states
	runStates := make(map[uint]string)
	if len(runs) > 0 {
		var runIDs []uint
		for _, r := range runs {
			runIDs = append(runIDs, r.RunID)
		}
		var runRecords []IncrementalCrawlRun
		if err := s.db.Where("id IN ?", runIDs).Find(&runRecords).Error; err != nil {
			return nil, fmt.Errorf("failed to get run states: %v", err)
		}
		for _, r := range runRecords {
			runStates[r.ID] = r.State
		}
	}

	// Add run entries
	for _, r := range runs {
		entries = append(entries, CrawlHistoryEntry{
			ID:            r.LatestCrawlID,
			ProjectID:     projectID,
			CrawlDateTime: r.FirstCrawlAt,
			CrawlDuration: r.TotalDuration,
			PagesCrawled:  r.TotalPages,
			State:         runStates[r.RunID],
		})
	}

	// Get standalone crawls (run_id IS NULL)
	var standaloneCrawls []Crawl
	if err := s.db.Where("project_id = ? AND run_id IS NULL", projectID).
		Order("crawl_date_time DESC").
		Find(&standaloneCrawls).Error; err != nil {
		return nil, fmt.Errorf("failed to get standalone crawls: %v", err)
	}

	// Add standalone entries
	for _, c := range standaloneCrawls {
		entries = append(entries, CrawlHistoryEntry{
			ID:            c.ID,
			ProjectID:     projectID,
			CrawlDateTime: c.CrawlDateTime,
			CrawlDuration: c.CrawlDuration,
			PagesCrawled:  c.PagesCrawled,
			State:         c.State,
		})
	}

	// Sort by CrawlDateTime descending (most recent first)
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].CrawlDateTime > entries[i].CrawlDateTime {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	return entries, nil
}

// getCrawlIDsForAggregation returns all crawl IDs that should be aggregated for a given crawl.
// If the crawl is part of a run, returns all crawl IDs in that run.
// Otherwise, returns just the given crawl ID.
func (s *Store) getCrawlIDsForAggregation(crawlID uint) ([]uint, error) {
	// First, get the crawl to check if it's part of a run
	var crawl Crawl
	if err := s.db.First(&crawl, crawlID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return []uint{crawlID}, nil
		}
		return nil, err
	}

	// If not part of a run, return just this crawl ID
	if crawl.RunID == nil {
		return []uint{crawlID}, nil
	}

	// Get all crawl IDs in the run
	var crawlIDs []uint
	if err := s.db.Model(&Crawl{}).
		Where("run_id = ?", *crawl.RunID).
		Pluck("id", &crawlIDs).Error; err != nil {
		return nil, err
	}

	return crawlIDs, nil
}

// GetCrawlResultsPaginatedAggregated gets paginated discovered URLs, aggregating across a run if applicable
func (s *Store) GetCrawlResultsPaginatedAggregated(crawlID uint, limit int, cursor uint, contentTypeFilter string) ([]DiscoveredUrl, uint, bool, error) {
	// Get all crawl IDs to aggregate
	crawlIDs, err := s.getCrawlIDsForAggregation(crawlID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("failed to get crawl IDs: %v", err)
	}

	var urls []DiscoveredUrl

	// Start with base query using IN clause for multiple crawl IDs
	db := s.db.Where("crawl_id IN ?", crawlIDs)

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

	// Fetch limit + 1 to check if there are more results
	result := db.Order("id ASC").Limit(limit + 1).Find(&urls)
	if result.Error != nil {
		return nil, 0, false, fmt.Errorf("failed to get paginated crawl results: %v", result.Error)
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

// GetActiveCrawlStatsAggregated gets statistics aggregated across a run if applicable
func (s *Store) GetActiveCrawlStatsAggregated(crawlID uint) (map[string]int, error) {
	// Get all crawl IDs to aggregate
	crawlIDs, err := s.getCrawlIDsForAggregation(crawlID)
	if err != nil {
		return nil, fmt.Errorf("failed to get crawl IDs: %v", err)
	}

	stats := make(map[string]int)

	// Get total count
	var total int64
	if err := s.db.Model(&DiscoveredUrl{}).Where("crawl_id IN ?", crawlIDs).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count total URLs: %v", err)
	}
	stats["total"] = int(total)

	// Get crawled count (visited = true)
	var crawled int64
	if err := s.db.Model(&DiscoveredUrl{}).Where("crawl_id IN ? AND visited = ?", crawlIDs, true).Count(&crawled).Error; err != nil {
		return nil, fmt.Errorf("failed to count crawled URLs: %v", err)
	}
	stats["crawled"] = int(crawled)

	// Queued = total - crawled
	stats["queued"] = stats["total"] - stats["crawled"]

	// Get HTML count
	var html int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ?)",
			crawlIDs, true, "%text/html%", "%application/xhtml%").
		Count(&html).Error; err != nil {
		return nil, fmt.Errorf("failed to count HTML URLs: %v", err)
	}
	stats["html"] = int(html)

	// Get JavaScript count
	var javascript int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
			crawlIDs, true, "%javascript%", "%application/x-javascript%", "%text/javascript%").
		Count(&javascript).Error; err != nil {
		return nil, fmt.Errorf("failed to count JavaScript URLs: %v", err)
	}
	stats["javascript"] = int(javascript)

	// Get CSS count
	var css int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ? AND content_type LIKE ?",
			crawlIDs, true, "%text/css%").
		Count(&css).Error; err != nil {
		return nil, fmt.Errorf("failed to count CSS URLs: %v", err)
	}
	stats["css"] = int(css)

	// Get images count
	var images int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ? AND content_type LIKE ?",
			crawlIDs, true, "%image/%").
		Count(&images).Error; err != nil {
		return nil, fmt.Errorf("failed to count image URLs: %v", err)
	}
	stats["images"] = int(images)

	// Get fonts count
	var fonts int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ? AND (content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ? OR content_type LIKE ?)",
			crawlIDs, true, "%font/%", "%application/font%", "%woff%", "%ttf%", "%eot%", "%otf%").
		Count(&fonts).Error; err != nil {
		return nil, fmt.Errorf("failed to count font URLs: %v", err)
	}
	stats["fonts"] = int(fonts)

	// Get unvisited count
	var unvisited int64
	if err := s.db.Model(&DiscoveredUrl{}).
		Where("crawl_id IN ? AND visited = ?", crawlIDs, false).
		Count(&unvisited).Error; err != nil {
		return nil, fmt.Errorf("failed to count unvisited URLs: %v", err)
	}
	stats["unvisited"] = int(unvisited)

	// Calculate "others" = visited - (html + js + css + images + fonts)
	stats["others"] = stats["crawled"] - (stats["html"] + stats["javascript"] + stats["css"] + stats["images"] + stats["fonts"])

	return stats, nil
}

// SearchCrawlResultsPaginatedAggregated searches discovered URLs with pagination, aggregating across a run if applicable
func (s *Store) SearchCrawlResultsPaginatedAggregated(crawlID uint, query string, contentTypeFilter string, limit int, cursor uint) ([]DiscoveredUrl, uint, bool, error) {
	// Get all crawl IDs to aggregate
	crawlIDs, err := s.getCrawlIDsForAggregation(crawlID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("failed to get crawl IDs: %v", err)
	}

	var urls []DiscoveredUrl

	// Start with base query using IN clause
	db := s.db.Where("crawl_id IN ?", crawlIDs)

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
