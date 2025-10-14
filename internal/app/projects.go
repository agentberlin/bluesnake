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
