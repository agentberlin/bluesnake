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
	"github.com/agentberlin/bluesnake/internal/types"
)

// GetCrawls returns all crawls for a project
func (a *App) GetCrawls(projectID uint) ([]types.CrawlInfo, error) {
	crawls, err := a.store.GetProjectCrawls(projectID)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlInfo for frontend
	crawlInfos := make([]types.CrawlInfo, len(crawls))
	for i, c := range crawls {
		crawlInfos[i] = types.CrawlInfo{
			ID:            c.ID,
			ProjectID:     c.ProjectID,
			CrawlDateTime: c.CrawlDateTime,
			CrawlDuration: c.CrawlDuration,
			PagesCrawled:  c.PagesCrawled,
		}
	}

	return crawlInfos, nil
}

// GetCrawlWithResults returns a specific crawl with all its results (both visited and unvisited URLs)
func (a *App) GetCrawlWithResults(crawlID uint) (*types.CrawlResultDetailed, error) {
	// Get crawl info
	crawl, err := a.store.GetCrawlByID(crawlID)
	if err != nil {
		return nil, err
	}

	// Get discovered URLs (both visited and unvisited)
	urls, err := a.store.GetCrawlResults(crawlID)
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
			ContentHash:     u.ContentHash,
			Indexable:       u.Indexable,
			ContentType:     u.ContentType,
			Error:           u.Error,
		}
	}

	return &types.CrawlResultDetailed{
		CrawlInfo: types.CrawlInfo{
			ID:            crawl.ID,
			ProjectID:     crawl.ProjectID,
			CrawlDateTime: crawl.CrawlDateTime,
			CrawlDuration: crawl.CrawlDuration,
			PagesCrawled:  crawl.PagesCrawled,
		},
		Results: results,
	}, nil
}

// DeleteCrawlByID deletes a specific crawl
func (a *App) DeleteCrawlByID(crawlID uint) error {
	return a.store.DeleteCrawl(crawlID)
}

// SearchCrawlResults searches and filters crawl results
func (a *App) SearchCrawlResults(crawlID uint, query string, contentTypeFilter string) ([]types.CrawlResult, error) {
	// Get filtered URLs from store
	urls, err := a.store.SearchCrawlResults(crawlID, query, contentTypeFilter)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlResult for frontend
	results := make([]types.CrawlResult, len(urls))
	for i, u := range urls {
		results[i] = types.CrawlResult{
			URL:             u.URL,
			Status:          u.Status,
			Title:           u.Title,
			MetaDescription: u.MetaDescription,
			ContentHash:     u.ContentHash,
			Indexable:       u.Indexable,
			ContentType:     u.ContentType,
			Error:           u.Error,
		}
	}

	return results, nil
}
