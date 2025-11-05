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
	"net/url"

	"github.com/agentberlin/bluesnake/internal/types"
)

// GetProjects returns all projects from the database with their latest crawl info
func (a *App) GetProjects() ([]types.ProjectInfo, error) {
	projects, err := a.store.GetAllProjects()
	if err != nil {
		return nil, err
	}

	// Convert to ProjectInfo for frontend
	projectInfos := make([]types.ProjectInfo, 0, len(projects))
	for _, p := range projects {
		projectInfo := types.ProjectInfo{
			ID:          p.ID,
			URL:         p.URL,
			Domain:      p.Domain,
			FaviconPath: p.FaviconPath,
		}

		// Get the latest crawl for this project if it exists
		if len(p.Crawls) > 0 {
			latestCrawl := p.Crawls[0]
			projectInfo.CrawlDateTime = latestCrawl.CrawlDateTime
			projectInfo.CrawlDuration = latestCrawl.CrawlDuration
			projectInfo.PagesCrawled = latestCrawl.PagesCrawled
			projectInfo.LatestCrawlID = latestCrawl.ID

			// Get total URLs count for this crawl
			totalURLs, err := a.store.GetTotalURLsForCrawl(latestCrawl.ID)
			if err == nil {
				projectInfo.TotalURLs = totalURLs
			}
		}
		// If no crawls, fields will be zero values (0 for int64/uint)

		projectInfos = append(projectInfos, projectInfo)
	}

	return projectInfos, nil
}

// DeleteProjectByID deletes a project and all its crawls
func (a *App) DeleteProjectByID(projectID uint) error {
	return a.store.DeleteProject(projectID)
}

// ============================================================================
// Competitor Management Methods
// ============================================================================

// GetCompetitors returns all competitor projects for a specific parent project with their latest crawl info
func (a *App) GetCompetitors(parentProjectID uint) ([]types.CompetitorInfo, error) {
	// Get competitors linked to this specific parent project
	competitors, err := a.store.GetCompetitorsForProject(parentProjectID)
	if err != nil {
		return nil, err
	}

	// Get active crawls to check if any competitor is currently being crawled
	activeCrawls := a.GetActiveCrawls()
	activeCrawlMap := make(map[uint]bool)
	for _, ac := range activeCrawls {
		activeCrawlMap[ac.ProjectID] = true
	}

	// Convert to CompetitorInfo for frontend
	competitorInfos := make([]types.CompetitorInfo, 0, len(competitors))
	for _, c := range competitors {
		competitorInfo := types.CompetitorInfo{
			ID:          c.ID,
			URL:         c.URL,
			Domain:      c.Domain,
			FaviconPath: c.FaviconPath,
			IsCrawling:  activeCrawlMap[c.ID],
		}

		// Get the latest crawl for this competitor if it exists
		if len(c.Crawls) > 0 {
			latestCrawl := c.Crawls[0]
			competitorInfo.CrawlDateTime = latestCrawl.CrawlDateTime
			competitorInfo.CrawlDuration = latestCrawl.CrawlDuration
			competitorInfo.PagesCrawled = latestCrawl.PagesCrawled
			competitorInfo.LatestCrawlID = latestCrawl.ID

			// Get total URLs count for this crawl
			totalURLs, err := a.store.GetTotalURLsForCrawl(latestCrawl.ID)
			if err == nil {
				competitorInfo.TotalURLs = totalURLs
			}
		}

		competitorInfos = append(competitorInfos, competitorInfo)
	}

	return competitorInfos, nil
}

// GetCompetitorStats returns aggregate statistics for all competitors
func (a *App) GetCompetitorStats() (*types.CompetitorStats, error) {
	competitors, err := a.store.GetAllCompetitors()
	if err != nil {
		return nil, err
	}

	stats := &types.CompetitorStats{
		TotalCompetitors: len(competitors),
	}

	// Get active crawls to count how many competitors are currently being crawled
	activeCrawls := a.GetActiveCrawls()
	activeCrawlMap := make(map[uint]bool)
	for _, ac := range activeCrawls {
		activeCrawlMap[ac.ProjectID] = true
	}

	// Calculate aggregate stats
	var lastCrawlTime int64
	var totalPages int

	for _, c := range competitors {
		// Check if this competitor is being crawled
		if activeCrawlMap[c.ID] {
			stats.ActiveCrawls++
		}

		// Get latest crawl data
		if len(c.Crawls) > 0 {
			latestCrawl := c.Crawls[0]
			totalPages += latestCrawl.PagesCrawled

			// Track the most recent crawl time across all competitors
			if latestCrawl.CrawlDateTime > lastCrawlTime {
				lastCrawlTime = latestCrawl.CrawlDateTime
			}
		}
	}

	stats.TotalPages = totalPages
	stats.LastCrawlTime = lastCrawlTime

	return stats, nil
}

// StartCompetitorCrawl starts a crawl for a competitor domain and links it to a parent project
func (a *App) StartCompetitorCrawl(urlStr string, parentProjectID uint) error {
	// Normalize the URL
	normalizedURL, domain, err := normalizeURL(urlStr)
	if err != nil {
		return err
	}

	// Parse the normalized URL
	parsedURL, err := url.Parse(normalizedURL)
	if err != nil {
		return err
	}

	// Get or create competitor project
	project, err := a.store.GetOrCreateCompetitor(normalizedURL, domain)
	if err != nil {
		return err
	}

	// Link competitor to parent project
	if err := a.store.AddCompetitorToProject(parentProjectID, project.ID); err != nil {
		return fmt.Errorf("failed to link competitor to project: %v", err)
	}

	// Check if already crawling
	a.crawlsMutex.RLock()
	_, alreadyCrawling := a.activeCrawls[project.ID]
	a.crawlsMutex.RUnlock()

	if alreadyCrawling {
		return fmt.Errorf("crawl already in progress for this competitor")
	}

	// Start the crawl using the existing crawler logic
	go a.runCrawler(parsedURL, normalizedURL, domain, project.ID)

	return nil
}

// DeleteCompetitor deletes a competitor and all its crawls
func (a *App) DeleteCompetitor(competitorID uint) error {
	return a.store.DeleteProject(competitorID)
}

// AddCompetitorToProject links a competitor to a project
func (a *App) AddCompetitorToProject(projectID uint, competitorID uint) error {
	return a.store.AddCompetitorToProject(projectID, competitorID)
}

// RemoveCompetitorFromProject unlinks a competitor from a project
func (a *App) RemoveCompetitorFromProject(projectID uint, competitorID uint) error {
	return a.store.RemoveCompetitorFromProject(projectID, competitorID)
}
