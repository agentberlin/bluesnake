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
	"fmt"

	"github.com/agentberlin/bluesnake/internal/types"
)

// GetActiveCrawls returns the progress of all active crawls
func (a *App) GetActiveCrawls() []types.CrawlProgress {
	a.crawlsMutex.RLock()
	defer a.crawlsMutex.RUnlock()

	progress := make([]types.CrawlProgress, 0, len(a.activeCrawls))
	for _, ac := range a.activeCrawls {
		ac.statusMutex.RLock()

		// Get discovered URLs that haven't been crawled yet (from bluesnake callbacks)
		discoveredURLs := []string{}
		totalDiscovered := 0

		if ac.stats.discoveredURLs != nil {
			ac.stats.discoveredURLs.Range(func(key, value interface{}) bool {
				urlStr := key.(string)
				totalDiscovered++

				// Check if this URL has been crawled
				if ac.stats.crawledURLs != nil {
					if _, crawled := ac.stats.crawledURLs.Load(urlStr); !crawled {
						// URL discovered but not yet crawled
						discoveredURLs = append(discoveredURLs, urlStr)
					}
				} else {
					discoveredURLs = append(discoveredURLs, urlStr)
				}
				return true
			})
		}

		progress = append(progress, types.CrawlProgress{
			ProjectID:       ac.projectID,
			CrawlID:         ac.crawlID,
			Domain:          ac.domain,
			URL:             ac.url,
			PagesCrawled:    ac.stats.pagesCrawled,
			TotalDiscovered: totalDiscovered,
			DiscoveredURLs:  discoveredURLs,
			IsCrawling:      true,
		})
		ac.statusMutex.RUnlock()
	}

	return progress
}

// GetActiveCrawlData returns the data for an active crawl from database
func (a *App) GetActiveCrawlData(projectID uint) (*types.CrawlResultDetailed, error) {
	a.crawlsMutex.RLock()
	ac, exists := a.activeCrawls[projectID]
	a.crawlsMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active crawl found for project %d", projectID)
	}

	// Read stats
	ac.statusMutex.RLock()
	pagesCrawled := ac.stats.pagesCrawled
	crawlID := ac.crawlID
	ac.statusMutex.RUnlock()

	// Fetch crawled results from database
	urls, err := a.store.GetCrawlResults(crawlID)
	if err != nil {
		return nil, err
	}

	// Convert crawled URLs to CrawlResult for frontend
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
			ContentHash:     u.ContentHash,
			Indexable:       u.Indexable,
			ContentType:     u.ContentType,
			Error:           u.Error,
		}
	}

	return &types.CrawlResultDetailed{
		CrawlInfo: types.CrawlInfo{
			ID:            crawlID,
			ProjectID:     ac.projectID,
			CrawlDateTime: 0, // Not applicable for active crawl
			CrawlDuration: 0, // Not applicable for active crawl
			PagesCrawled:  pagesCrawled,
		},
		Results: results,
	}, nil
}
