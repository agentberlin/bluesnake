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

	// Open database connection
	dbPath := filepath.Join(dbDir, "bluesnake.db")
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto migrate the schema
	if err := database.AutoMigrate(&Config{}, &Project{}, &Crawl{}, &CrawledUrl{}, &PageLink{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %v", err)
	}

	return &Store{db: database}, nil
}

// DB returns the underlying GORM database instance
func (s *Store) DB() *gorm.DB {
	return s.db
}
