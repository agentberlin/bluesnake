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

// GetAllProjects returns all projects with their latest crawl info
func (s *Store) GetAllProjects() ([]Project, error) {
	var projects []Project

	// First, get all projects
	result := s.db.Order("id ASC").Find(&projects)
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
