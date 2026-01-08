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
		mapDiscoveredCount := 0

		if ac.stats.discoveredURLs != nil {
			ac.stats.discoveredURLs.Range(func(key, value interface{}) bool {
				urlStr := key.(string)
				mapDiscoveredCount++

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

		// Use stats.totalDiscovered if set (for resumed crawls with queue offset),
		// otherwise use the map count (for fresh crawls)
		totalDiscovered := ac.stats.totalDiscovered
		if mapDiscoveredCount > totalDiscovered {
			totalDiscovered = mapDiscoveredCount
		}

		progress = append(progress, types.CrawlProgress{
			ProjectID:        ac.projectID,
			CrawlID:          ac.crawlID,
			Domain:           ac.domain,
			URL:              ac.url,
			PagesCrawled:     ac.stats.pagesCrawled,
			TotalURLsCrawled: ac.stats.totalURLsCrawled,
			TotalDiscovered:  totalDiscovered,
			DiscoveredURLs:   discoveredURLs,
			IsCrawling:       true,
		})
		ac.statusMutex.RUnlock()
	}

	return progress
}

// GetActiveCrawlStats returns statistics for an active crawl (no URL list, just counts).
// For incremental crawling, stats are aggregated across all crawls in the run.
func (a *App) GetActiveCrawlStats(projectID uint) (*types.ActiveCrawlStats, error) {
	a.crawlsMutex.RLock()
	ac, exists := a.activeCrawls[projectID]
	a.crawlsMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active crawl found for project %d", projectID)
	}

	// Get total/crawled/queued from in-memory state (accurate for both incremental and non-incremental)
	ac.statusMutex.RLock()
	crawlID := ac.crawlID
	mapDiscoveredCount := 0
	if ac.stats.discoveredURLs != nil {
		ac.stats.discoveredURLs.Range(func(_, _ interface{}) bool {
			mapDiscoveredCount++
			return true
		})
	}
	totalDiscovered := ac.stats.totalDiscovered
	if mapDiscoveredCount > totalDiscovered {
		totalDiscovered = mapDiscoveredCount
	}
	totalCrawled := ac.stats.totalURLsCrawled
	ac.statusMutex.RUnlock()

	// Get content-type breakdowns from database
	stats, err := a.store.GetActiveCrawlStatsAggregated(crawlID)
	if err != nil {
		return nil, err
	}

	return &types.ActiveCrawlStats{
		CrawlID:    crawlID,
		Total:      totalDiscovered,
		Crawled:    totalCrawled,
		Queued:     totalDiscovered - totalCrawled,
		HTML:       stats["html"],
		JavaScript: stats["javascript"],
		CSS:        stats["css"],
		Images:     stats["images"],
		Fonts:      stats["fonts"],
		Unvisited:  stats["unvisited"],
		Others:     stats["others"],
	}, nil
}
