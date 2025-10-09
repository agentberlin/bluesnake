package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

// Project represents a crawled project with its metadata
type Project struct {
	ID            uint   `gorm:"primaryKey"`
	URL           string `gorm:"uniqueIndex;not null"`
	Domain        string `gorm:"not null"`
	CrawlDateTime int64  `gorm:"not null"` // Unix timestamp
	CrawlDuration int64  `gorm:"not null"` // Duration in milliseconds
	PagesCrawled  int    `gorm:"not null"`
	CreatedAt     int64  `gorm:"autoCreateTime"`
	UpdatedAt     int64  `gorm:"autoUpdateTime"`
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
	if err := db.AutoMigrate(&Project{}); err != nil {
		return fmt.Errorf("failed to migrate database: %v", err)
	}

	return nil
}

// SaveProject saves or updates a project in the database
func SaveProject(url string, domain string, crawlDateTime int64, crawlDuration int64, pagesCrawled int) error {
	project := Project{
		URL:           url,
		Domain:        domain,
		CrawlDateTime: crawlDateTime,
		CrawlDuration: crawlDuration,
		PagesCrawled:  pagesCrawled,
	}

	// Check if project exists
	var existing Project
	result := db.Where("url = ?", url).First(&existing)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new project
		return db.Create(&project).Error
	}

	// Update existing project
	existing.CrawlDateTime = crawlDateTime
	existing.CrawlDuration = crawlDuration
	existing.PagesCrawled = pagesCrawled
	return db.Save(&existing).Error
}

// GetAllProjects returns all projects ordered by most recent crawl
func GetAllProjects() ([]Project, error) {
	var projects []Project
	result := db.Order("crawl_date_time DESC").Find(&projects)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get projects: %v", result.Error)
	}
	return projects, nil
}

// DeleteProject deletes a project by URL
func DeleteProject(url string) error {
	return db.Where("url = ?", url).Delete(&Project{}).Error
}
