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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

// Config represents the crawl configuration for a domain
type Config struct {
	ID                  uint     `gorm:"primaryKey"`
	ProjectID           uint     `gorm:"uniqueIndex;not null"`
	Domain              string   `gorm:"not null"`
	JSRenderingEnabled  bool     `gorm:"default:false"`
	Parallelism         int      `gorm:"default:5"`
	DiscoveryMechanisms string   `gorm:"type:text;default:'[\"spider\"]'"` // JSON array
	SitemapURLs         string   `gorm:"type:text"`                        // JSON array, nullable
	Project             *Project `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt           int64    `gorm:"autoCreateTime"`
	UpdatedAt           int64    `gorm:"autoUpdateTime"`
}

// GetDiscoveryMechanismsArray deserializes the DiscoveryMechanisms JSON to []string
func (c *Config) GetDiscoveryMechanismsArray() []string {
	if c.DiscoveryMechanisms == "" {
		return []string{"spider"} // Default
	}
	var mechanisms []string
	if err := json.Unmarshal([]byte(c.DiscoveryMechanisms), &mechanisms); err != nil {
		return []string{"spider"} // Default on error
	}
	return mechanisms
}

// SetDiscoveryMechanismsArray serializes []string to JSON for DiscoveryMechanisms
func (c *Config) SetDiscoveryMechanismsArray(mechanisms []string) error {
	data, err := json.Marshal(mechanisms)
	if err != nil {
		return err
	}
	c.DiscoveryMechanisms = string(data)
	return nil
}

// GetSitemapURLsArray deserializes the SitemapURLs JSON to []string
// Returns nil if empty (which means use defaults)
func (c *Config) GetSitemapURLsArray() []string {
	if c.SitemapURLs == "" || c.SitemapURLs == "null" {
		return nil // nil means use defaults
	}
	var urls []string
	if err := json.Unmarshal([]byte(c.SitemapURLs), &urls); err != nil {
		return nil // Return nil on error
	}
	if len(urls) == 0 {
		return nil // Empty array = nil (use defaults)
	}
	return urls
}

// SetSitemapURLsArray serializes []string to JSON for SitemapURLs
func (c *Config) SetSitemapURLsArray(urls []string) error {
	if urls == nil || len(urls) == 0 {
		c.SitemapURLs = "" // Empty = use defaults
		return nil
	}
	data, err := json.Marshal(urls)
	if err != nil {
		return err
	}
	c.SitemapURLs = string(data)
	return nil
}

// Project represents a project (base URL) that can have multiple crawls
type Project struct {
	ID          uint     `gorm:"primaryKey"`
	URL         string   `gorm:"not null"` // Normalized URL for the project
	Domain      string   `gorm:"uniqueIndex;not null"` // Domain identifier (includes subdomain)
	FaviconPath string   `gorm:"type:text"` // Path to cached favicon
	Crawls      []Crawl  `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	CreatedAt   int64    `gorm:"autoCreateTime"`
	UpdatedAt   int64    `gorm:"autoUpdateTime"`
}

// Crawl represents a single crawl session for a project
type Crawl struct {
	ID            uint          `gorm:"primaryKey"`
	ProjectID     uint          `gorm:"not null;index"`
	CrawlDateTime int64         `gorm:"not null"` // Unix timestamp
	CrawlDuration int64         `gorm:"not null"` // Duration in milliseconds
	PagesCrawled  int           `gorm:"not null"`
	CrawledUrls   []CrawledUrl  `gorm:"foreignKey:CrawlID;constraint:OnDelete:CASCADE"`
	CreatedAt     int64         `gorm:"autoCreateTime"`
	UpdatedAt     int64         `gorm:"autoUpdateTime"`
}

// CrawledUrl represents a single URL that was crawled
type CrawledUrl struct {
	ID        uint   `gorm:"primaryKey"`
	CrawlID   uint   `gorm:"not null;index"`
	URL       string `gorm:"not null"`
	Status    int    `gorm:"not null"`
	Title     string `gorm:"type:text"`
	Indexable string `gorm:"not null"`
	Error     string `gorm:"type:text"`
	CreatedAt int64  `gorm:"autoCreateTime"`
}

// InitDB initializes the database connection and creates tables
func InitDB() error {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Create ~/.bluesnake directory if it doesn't exist
	dbDir := filepath.Join(homeDir, ".bluesnake")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %v", err)
	}

	// Open database connection
	dbPath := filepath.Join(dbDir, "bluesnake.db")
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %v", err)
	}

	db = database

	// Auto migrate the schema
	if err := db.AutoMigrate(&Config{}, &Project{}, &Crawl{}, &CrawledUrl{}); err != nil {
		return fmt.Errorf("failed to migrate database: %v", err)
	}

	return nil
}

// GetOrCreateConfig retrieves the config for a project or creates one with defaults
func GetOrCreateConfig(projectID uint, domain string) (*Config, error) {
	var config Config

	result := db.Where("project_id = ?", projectID).First(&config)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new config with defaults
		config = Config{
			ProjectID:           projectID,
			Domain:              domain,
			JSRenderingEnabled:  false,
			Parallelism:         5,
			DiscoveryMechanisms: "[\"spider\"]", // Default to spider mode
			SitemapURLs:         "",             // Empty = use defaults when sitemap enabled
		}

		if err := db.Create(&config).Error; err != nil {
			return nil, fmt.Errorf("failed to create config: %v", err)
		}

		return &config, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get config: %v", result.Error)
	}

	return &config, nil
}

// UpdateConfig updates the configuration for a project
func UpdateConfig(projectID uint, jsRendering bool, parallelism int, discoveryMechanisms []string, sitemapURLs []string) error {
	var config Config

	result := db.Where("project_id = ?", projectID).First(&config)

	if result.Error != nil {
		return fmt.Errorf("failed to get config: %v", result.Error)
	}

	// Update existing config
	config.JSRenderingEnabled = jsRendering
	config.Parallelism = parallelism

	// Update discovery mechanisms
	if err := config.SetDiscoveryMechanismsArray(discoveryMechanisms); err != nil {
		return fmt.Errorf("failed to set discovery mechanisms: %v", err)
	}

	// Update sitemap URLs
	if err := config.SetSitemapURLsArray(sitemapURLs); err != nil {
		return fmt.Errorf("failed to set sitemap URLs: %v", err)
	}

	return db.Save(&config).Error
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

// GetOrCreateProject gets or creates a project by domain
func GetOrCreateProject(url string, domain string) (*Project, error) {
	var project Project
	result := db.Where("domain = ?", domain).First(&project)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new project
		project = Project{
			URL:    url,
			Domain: domain,
		}
		if err := db.Create(&project).Error; err != nil {
			return nil, fmt.Errorf("failed to create project: %v", err)
		}

		// Fetch and save favicon asynchronously (don't fail if this fails)
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				// Update project with favicon path
				db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)

		return &project, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get project: %v", result.Error)
	}

	// Update URL if it has changed (should be the normalized URL)
	if project.URL != url {
		project.URL = url
		db.Save(&project)
	}

	// If favicon doesn't exist, try to fetch it
	if project.FaviconPath == "" {
		go func(projectID uint, domain string) {
			if faviconPath, err := fetchAndSaveFavicon(projectID, domain); err == nil {
				db.Model(&Project{}).Where("id = ?", projectID).Update("favicon_path", faviconPath)
			}
		}(project.ID, domain)
	}

	return &project, nil
}

// CreateCrawl creates a new crawl for a project
func CreateCrawl(projectID uint, crawlDateTime int64, crawlDuration int64, pagesCrawled int) (*Crawl, error) {
	crawl := Crawl{
		ProjectID:     projectID,
		CrawlDateTime: crawlDateTime,
		CrawlDuration: crawlDuration,
		PagesCrawled:  pagesCrawled,
	}

	if err := db.Create(&crawl).Error; err != nil {
		return nil, fmt.Errorf("failed to create crawl: %v", err)
	}

	return &crawl, nil
}

// SaveCrawledUrl saves a crawled URL result
func SaveCrawledUrl(crawlID uint, url string, status int, title string, indexable string, errorMsg string) error {
	crawledUrl := CrawledUrl{
		CrawlID:   crawlID,
		URL:       url,
		Status:    status,
		Title:     title,
		Indexable: indexable,
		Error:     errorMsg,
	}

	return db.Create(&crawledUrl).Error
}

// UpdateCrawlStats updates the crawl statistics
func UpdateCrawlStats(crawlID uint, crawlDuration int64, pagesCrawled int) error {
	return db.Model(&Crawl{}).Where("id = ?", crawlID).Updates(map[string]interface{}{
		"crawl_duration": crawlDuration,
		"pages_crawled":  pagesCrawled,
	}).Error
}

// GetAllProjects returns all projects with their latest crawl info
func GetAllProjects() ([]Project, error) {
	var projects []Project

	// First, get all projects
	result := db.Order("id ASC").Find(&projects)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get projects: %v", result.Error)
	}

	// For each project, manually fetch the latest crawl
	for i := range projects {
		var latestCrawl Crawl
		err := db.Where("project_id = ?", projects[i].ID).
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
func GetProjectByID(id uint) (*Project, error) {
	var project Project
	result := db.First(&project, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get project: %v", result.Error)
	}
	return &project, nil
}

// GetProjectCrawls returns all crawls for a project ordered by date
func GetProjectCrawls(projectID uint) ([]Crawl, error) {
	var crawls []Crawl
	result := db.Where("project_id = ?", projectID).Order("crawl_date_time DESC").Find(&crawls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawls: %v", result.Error)
	}
	return crawls, nil
}

// GetLatestCrawl gets the most recent crawl for a project
func GetLatestCrawl(projectID uint) (*Crawl, error) {
	var crawl Crawl
	result := db.Where("project_id = ?", projectID).Order("crawl_date_time DESC").First(&crawl)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get latest crawl: %v", result.Error)
	}
	return &crawl, nil
}

// GetCrawlResults gets all crawled URLs for a specific crawl
func GetCrawlResults(crawlID uint) ([]CrawledUrl, error) {
	var urls []CrawledUrl
	result := db.Where("crawl_id = ?", crawlID).Order("id ASC").Find(&urls)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawl results: %v", result.Error)
	}
	return urls, nil
}

// GetCrawlByID gets a crawl by ID
func GetCrawlByID(id uint) (*Crawl, error) {
	var crawl Crawl
	result := db.Preload("CrawledUrls").First(&crawl, id)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get crawl: %v", result.Error)
	}
	return &crawl, nil
}

// DeleteCrawl deletes a crawl and all its crawled URLs (cascade)
func DeleteCrawl(crawlID uint) error {
	return db.Delete(&Crawl{}, crawlID).Error
}

// DeleteProject deletes a project and all its crawls (cascade)
func DeleteProject(projectID uint) error {
	return db.Delete(&Project{}, projectID).Error
}
