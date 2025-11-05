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
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
)

// GetOrCreateProject gets or creates a project by domain
func (s *Store) GetOrCreateProject(urlStr string, domain string) (*Project, error) {
	var project Project
	result := s.db.Where("domain = ?", domain).First(&project)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new project
		project = Project{
			URL:    urlStr,
			Domain: domain,
		}
		if err := s.db.Create(&project).Error; err != nil {
			return nil, fmt.Errorf("failed to create project: %v", err)
		}

		// Fetch and save favicon asynchronously (don't fail if this fails)
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				// Update project with favicon path
				s.db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)

		return &project, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get project: %v", result.Error)
	}

	// Update URL if it has changed (should be the normalized URL)
	if project.URL != urlStr {
		project.URL = urlStr
		s.db.Save(&project)
	}

	// If favicon doesn't exist, try to fetch it
	if project.FaviconPath == "" {
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				s.db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)
	}

	return &project, nil
}

// GetAllProjects returns all projects (excluding competitors) with their latest crawl info
func (s *Store) GetAllProjects() ([]Project, error) {
	var projects []Project

	// Get all projects where IsCompetitor = false (exclude competitors from home page)
	result := s.db.Where("is_competitor = ?", false).Order("id ASC").Find(&projects)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get projects: %v", result.Error)
	}

	// For each project, manually fetch the latest crawl
	for i := range projects {
		var latestCrawl Crawl
		err := s.db.Where("project_id = ?", projects[i].ID).
			Order("crawl_date_time DESC").
			First(&latestCrawl).Error

		if err == nil {
			// Found a crawl, add it to the project
			projects[i].Crawls = []Crawl{latestCrawl}
		} else if err != gorm.ErrRecordNotFound {
			// An actual error occurred (not just "no records")
			return nil, fmt.Errorf("failed to get latest crawl for project %d: %v", projects[i].ID, err)
		}
		// If err == gorm.ErrRecordNotFound, Crawls will remain empty slice
	}

	return projects, nil
}

// GetProjectByID gets a project by ID
func (s *Store) GetProjectByID(id uint) (*Project, error) {
	var project Project
	result := s.db.First(&project, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get project: %v", result.Error)
	}
	return &project, nil
}

// DeleteProject deletes a project and all its crawls (cascade)
func (s *Store) DeleteProject(projectID uint) error {
	result := s.db.Delete(&Project{}, projectID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("project with ID %d not found", projectID)
	}
	return nil
}

// GetProjectByDomain gets a project by domain
func (s *Store) GetProjectByDomain(domain string) (*Project, error) {
	var project Project
	result := s.db.Where("domain = ?", domain).First(&project)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get project: %v", result.Error)
	}
	return &project, nil
}

// UpdateProject updates a project with given fields
func (s *Store) UpdateProject(projectID uint, updates map[string]interface{}) error {
	result := s.db.Model(&Project{}).Where("id = ?", projectID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("failed to update project: %v", result.Error)
	}
	return nil
}

// fetchAndSaveFavicon fetches the favicon for a domain and saves it locally
func fetchAndSaveFavicon(projectID uint, domain string) (string, error) {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Create project directory
	projectDir := filepath.Join(homeDir, ".bluesnake", "projects", fmt.Sprintf("%d", projectID))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory: %v", err)
	}

	// Extract base domain (remove port if present for Google favicon API)
	baseDomain := domain
	if strings.Contains(domain, ":") {
		if parsedURL, err := url.Parse("http://" + domain); err == nil {
			baseDomain = parsedURL.Hostname()
		}
	}

	// Fetch favicon from Google's favicon service
	faviconURL := fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=128", url.QueryEscape(baseDomain))
	resp, err := http.Get(faviconURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch favicon: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch favicon: status %d", resp.StatusCode)
	}

	// Save favicon to file
	faviconPath := filepath.Join(projectDir, "favicon.png")
	file, err := os.Create(faviconPath)
	if err != nil {
		return "", fmt.Errorf("failed to create favicon file: %v", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", fmt.Errorf("failed to save favicon: %v", err)
	}

	return faviconPath, nil
}

// GetTotalURLsForCrawl returns the total number of discovered URLs for a crawl
func (s *Store) GetTotalURLsForCrawl(crawlID uint) (int, error) {
	var count int64
	result := s.db.Model(&DiscoveredUrl{}).Where("crawl_id = ?", crawlID).Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count URLs for crawl %d: %v", crawlID, result.Error)
	}
	return int(count), nil
}

// ============================================================================
// Competitor Management Methods
// ============================================================================

// GetOrCreateCompetitor gets or creates a competitor project (marked with IsCompetitor = true)
func (s *Store) GetOrCreateCompetitor(urlStr string, domain string) (*Project, error) {
	var project Project
	result := s.db.Where("domain = ?", domain).First(&project)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new competitor project
		project = Project{
			URL:          urlStr,
			Domain:       domain,
			IsCompetitor: true,
		}
		if err := s.db.Create(&project).Error; err != nil {
			return nil, fmt.Errorf("failed to create competitor: %v", err)
		}

		// Fetch and save favicon asynchronously
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				s.db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)

		return &project, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get competitor: %v", result.Error)
	}

	// If project exists but is not marked as competitor, update it
	if !project.IsCompetitor {
		project.IsCompetitor = true
		if err := s.db.Save(&project).Error; err != nil {
			return nil, fmt.Errorf("failed to update project as competitor: %v", err)
		}
	}

	// Update URL if it has changed
	if project.URL != urlStr {
		project.URL = urlStr
		s.db.Save(&project)
	}

	// If favicon doesn't exist, try to fetch it
	if project.FaviconPath == "" {
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				s.db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)
	}

	return &project, nil
}

// GetAllCompetitors returns all projects marked as competitors with their latest crawl info
func (s *Store) GetAllCompetitors() ([]Project, error) {
	var competitors []Project

	// Get all projects where IsCompetitor = true
	result := s.db.Where("is_competitor = ?", true).Order("id ASC").Find(&competitors)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get competitors: %v", result.Error)
	}

	// For each competitor, manually fetch the latest crawl
	for i := range competitors {
		var latestCrawl Crawl
		err := s.db.Where("project_id = ?", competitors[i].ID).
			Order("crawl_date_time DESC").
			First(&latestCrawl).Error

		if err == nil {
			competitors[i].Crawls = []Crawl{latestCrawl}
		} else if err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to get latest crawl for competitor %d: %v", competitors[i].ID, err)
		}
	}

	return competitors, nil
}

// GetCompetitorsForProject returns all competitors linked to a specific project
func (s *Store) GetCompetitorsForProject(projectID uint) ([]Project, error) {
	var competitors []Project

	// Join through ProjectCompetitor table
	result := s.db.
		Joins("JOIN project_competitors ON project_competitors.competitor_id = projects.id").
		Where("project_competitors.project_id = ?", projectID).
		Find(&competitors)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get competitors for project %d: %v", projectID, result.Error)
	}

	// For each competitor, fetch the latest crawl
	for i := range competitors {
		var latestCrawl Crawl
		err := s.db.Where("project_id = ?", competitors[i].ID).
			Order("crawl_date_time DESC").
			First(&latestCrawl).Error

		if err == nil {
			competitors[i].Crawls = []Crawl{latestCrawl}
		} else if err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("failed to get latest crawl for competitor %d: %v", competitors[i].ID, err)
		}
	}

	return competitors, nil
}

// AddCompetitorToProject links a competitor to a project
func (s *Store) AddCompetitorToProject(projectID uint, competitorID uint) error {
	// Check if the relationship already exists
	var existing ProjectCompetitor
	result := s.db.Where("project_id = ? AND competitor_id = ?", projectID, competitorID).First(&existing)

	if result.Error == nil {
		// Relationship already exists
		return nil
	}

	if result.Error != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to check existing relationship: %v", result.Error)
	}

	// Create the relationship
	relationship := ProjectCompetitor{
		ProjectID:    projectID,
		CompetitorID: competitorID,
	}

	if err := s.db.Create(&relationship).Error; err != nil {
		return fmt.Errorf("failed to link competitor to project: %v", err)
	}

	return nil
}

// RemoveCompetitorFromProject unlinks a competitor from a project
func (s *Store) RemoveCompetitorFromProject(projectID uint, competitorID uint) error {
	result := s.db.Where("project_id = ? AND competitor_id = ?", projectID, competitorID).
		Delete(&ProjectCompetitor{})

	if result.Error != nil {
		return fmt.Errorf("failed to unlink competitor from project: %v", result.Error)
	}

	return nil
}

// SetProjectAsCompetitor sets or unsets the IsCompetitor flag for a project
func (s *Store) SetProjectAsCompetitor(projectID uint, isCompetitor bool) error {
	result := s.db.Model(&Project{}).Where("id = ?", projectID).Update("is_competitor", isCompetitor)
	if result.Error != nil {
		return fmt.Errorf("failed to update project competitor status: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("project with ID %d not found", projectID)
	}
	return nil
}
