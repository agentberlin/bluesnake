package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

// Config represents the crawl configuration for a domain
type Config struct {
	ID                  uint   `gorm:"primaryKey"`
	Domain              string `gorm:"uniqueIndex;not null"`
	JSRenderingEnabled  bool   `gorm:"default:false"`
	Parallelism         int    `gorm:"default:5"`
	CreatedAt           int64  `gorm:"autoCreateTime"`
	UpdatedAt           int64  `gorm:"autoUpdateTime"`
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
	if err := db.AutoMigrate(&Config{}); err != nil {
		return fmt.Errorf("failed to migrate database: %v", err)
	}

	return nil
}

// GetOrCreateConfig retrieves the config for a domain or creates one with defaults
func GetOrCreateConfig(domain string) (*Config, error) {
	var config Config

	result := db.Where("domain = ?", domain).First(&config)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new config with defaults
		config = Config{
			Domain:             domain,
			JSRenderingEnabled: false,
			Parallelism:        5,
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

// UpdateConfig updates the configuration for a domain
func UpdateConfig(domain string, jsRendering bool, parallelism int) error {
	var config Config

	result := db.Where("domain = ?", domain).First(&config)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new config
		config = Config{
			Domain:             domain,
			JSRenderingEnabled: jsRendering,
			Parallelism:        parallelism,
		}
		return db.Create(&config).Error
	}

	if result.Error != nil {
		return fmt.Errorf("failed to get config: %v", result.Error)
	}

	// Update existing config
	config.JSRenderingEnabled = jsRendering
	config.Parallelism = parallelism

	return db.Save(&config).Error
}

// GetConfig returns the configuration for a domain (for frontend)
func GetConfig(domain string) (*Config, error) {
	return GetOrCreateConfig(domain)
}
