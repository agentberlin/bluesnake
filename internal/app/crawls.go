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

package app

import (
	"log"

	"github.com/agentberlin/bluesnake/internal/types"
)

// GetCrawls returns deduplicated crawl history for a project.
// For incremental crawling, runs are aggregated into single entries.
// For standalone crawls, each crawl is returned as-is.
func (a *App) GetCrawls(projectID uint) ([]types.CrawlInfo, error) {
	entries, err := a.store.GetCrawlHistory(projectID)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlInfo for frontend
	crawlInfos := make([]types.CrawlInfo, len(entries))
	for i, e := range entries {
		crawlInfos[i] = types.CrawlInfo{
			ID:            e.ID,
			ProjectID:     e.ProjectID,
			CrawlDateTime: e.CrawlDateTime,
			CrawlDuration: e.CrawlDuration,
			PagesCrawled:  e.PagesCrawled,
			State:         e.State,
		}
	}

	return crawlInfos, nil
}

// DeleteCrawlByID deletes a specific crawl
func (a *App) DeleteCrawlByID(crawlID uint) error {
	return a.store.DeleteCrawl(crawlID)
}

// GetCrawlWithResultsPaginated returns a specific crawl with paginated results.
// For incremental crawling, results are aggregated across all crawls in the run.
func (a *App) GetCrawlWithResultsPaginated(crawlID uint, limit int, cursor uint, contentTypeFilter string) (*types.CrawlResultPaginated, error) {
	// Get paginated URLs from store (aggregated if part of a run)
	urls, nextCursor, hasMore, err := a.store.GetCrawlResultsPaginatedAggregated(crawlID, limit, cursor, contentTypeFilter)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlResult for frontend
	results := make([]types.CrawlResult, len(urls))
	for i, u := range urls {
		// For unvisited URLs, set a descriptive title
		title := u.Title
		if !u.Visited {
			title = "Unvisited URL"
		}

		results[i] = types.CrawlResult{
			URL:             u.URL,
			Status:          u.Status,
			Title:           title,
			MetaDescription: u.MetaDescription,
			H1:              u.H1,
			H2:              u.H2,
			CanonicalURL:    u.CanonicalURL,
			WordCount:       u.WordCount,
			ContentHash:     u.ContentHash,
			Indexable:       u.Indexable,
			ContentType:     u.ContentType,
			Error:           u.Error,
		}
	}

	return &types.CrawlResultPaginated{
		Results:    results,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// SearchCrawlResultsPaginated searches and filters crawl results with pagination.
// For incremental crawling, results are aggregated across all crawls in the run.
func (a *App) SearchCrawlResultsPaginated(crawlID uint, query string, contentTypeFilter string, limit int, cursor uint) (*types.CrawlResultPaginated, error) {
	log.Printf("=== DEBUG: SearchCrawlResultsPaginated START - crawlID=%d, query='%s', filter='%s', limit=%d, cursor=%d ===",
		crawlID, query, contentTypeFilter, limit, cursor)

	// Get paginated filtered URLs from store (aggregated if part of a run)
	urls, nextCursor, hasMore, err := a.store.SearchCrawlResultsPaginatedAggregated(crawlID, query, contentTypeFilter, limit, cursor)
	if err != nil {
		log.Printf("=== DEBUG: SearchCrawlResultsPaginated ERROR - error=%v ===", err)
		return nil, err
	}

	log.Printf("DEBUG: Store returned %d URLs, nextCursor=%d, hasMore=%v", len(urls), nextCursor, hasMore)

	// Convert to CrawlResult for frontend
	results := make([]types.CrawlResult, len(urls))
	for i, u := range urls {
		// For unvisited URLs, set a descriptive title
		title := u.Title
		if !u.Visited {
			title = "Unvisited URL"
		}

		results[i] = types.CrawlResult{
			URL:             u.URL,
			Status:          u.Status,
			Title:           title,
			MetaDescription: u.MetaDescription,
			H1:              u.H1,
			H2:              u.H2,
			CanonicalURL:    u.CanonicalURL,
			WordCount:       u.WordCount,
			ContentHash:     u.ContentHash,
			Indexable:       u.Indexable,
			ContentType:     u.ContentType,
			Error:           u.Error,
		}
	}

	log.Printf("=== DEBUG: SearchCrawlResultsPaginated END - returning %d results, nextCursor=%d, hasMore=%v ===",
		len(results), nextCursor, hasMore)

	return &types.CrawlResultPaginated{
		Results:    results,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// GetCrawlStats returns statistics for any crawl (active or completed).
// For incremental crawling, stats are aggregated across all crawls in the run.
func (a *App) GetCrawlStats(crawlID uint) (*types.ActiveCrawlStats, error) {
	// Get stats from store (aggregated if part of a run)
	stats, err := a.store.GetActiveCrawlStatsAggregated(crawlID)
	if err != nil {
		return nil, err
	}

	return &types.ActiveCrawlStats{
		CrawlID:    crawlID,
		Total:      stats["total"],
		Crawled:    stats["crawled"],
		Queued:     stats["queued"],
		HTML:       stats["html"],
		JavaScript: stats["javascript"],
		CSS:        stats["css"],
		Images:     stats["images"],
		Fonts:      stats["fonts"],
		Unvisited:  stats["unvisited"],
		Others:     stats["others"],
	}, nil
}
