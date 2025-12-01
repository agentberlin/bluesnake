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
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Store represents the database store
type Store struct {
	db *gorm.DB
}

// NewStore creates a new Store and initializes the database
func NewStore() (*Store, error) {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Create ~/.bluesnake directory if it doesn't exist
	dbDir := filepath.Join(homeDir, ".bluesnake")

	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %v", err)
	}

	// Verify directory was created
	if info, err := os.Stat(dbDir); err != nil {
		return nil, fmt.Errorf("database directory does not exist after creation: %v", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("database path exists but is not a directory: %s", dbDir)
	}

	// Open database connection
	dbPath := filepath.Join(dbDir, "bluesnake.db")
	return newStoreWithPath(dbPath)
}

// NewStoreForTesting creates a store with a custom database path (used for testing)
func NewStoreForTesting(dbPath string) (*Store, error) {
	return newStoreWithPath(dbPath)
}

// newStoreWithPath creates a store with a custom database path (used for testing)
func newStoreWithPath(dbPath string) (*Store, error) {
	// Check if parent directory exists
	dbDir := filepath.Dir(dbPath)
	if _, err := os.Stat(dbDir); err != nil {
		return nil, fmt.Errorf("database directory does not exist: %s, error: %v", dbDir, err)
	}

	// Configure SQLite with pragmas for better concurrency
	// WAL mode enables concurrent reads and writes
	// busy_timeout prevents immediate "database is locked" errors
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=1000000000", dbPath)

	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Get underlying SQL DB and configure connection pool
	sqlDB, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying SQL DB: %v", err)
	}

	// Set connection pool settings for better concurrency
	sqlDB.SetMaxOpenConns(25)         // Max number of open connections
	sqlDB.SetMaxIdleConns(5)          // Max number of idle connections
	sqlDB.SetConnMaxLifetime(0)       // Connections never expire (reuse them)
	sqlDB.SetConnMaxIdleTime(0)       // Idle connections never expire

	// Auto migrate the schema
	if err := database.AutoMigrate(&Config{}, &Project{}, &Crawl{}, &DiscoveredUrl{}, &PageLink{}, &DomainFramework{}, &CrawlQueueItem{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %v", err)
	}

	// Add unique constraint on (ProjectID, Domain) for domain_frameworks
	if err := database.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_project_domain_unique ON domain_frameworks(project_id, domain)").Error; err != nil {
		return nil, fmt.Errorf("failed to create unique index: %v", err)
	}

	// Add unique constraint on (ProjectID, URL) for crawl_queue_items
	if err := database.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_queue_project_url ON crawl_queue_items(project_id, url)").Error; err != nil {
		return nil, fmt.Errorf("failed to create crawl queue unique index: %v", err)
	}

	return &Store{db: database}, nil
}

// DB returns the underlying GORM database instance
func (s *Store) DB() *gorm.DB {
	return s.db
}
