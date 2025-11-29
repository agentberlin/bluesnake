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
	"os"
	"path/filepath"
	"testing"
)

// TestGetOrCreateConfigDefaults tests that GetOrCreateConfig returns correct default values
func TestGetOrCreateConfigDefaults(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	projectID := uint(1)
	domain := "example.com"
	config, err := store.GetOrCreateConfig(projectID, domain)
	if err != nil {
		t.Fatalf("GetOrCreateConfig() error = %v", err)
	}

	// Verify default values
	if config.ProjectID != projectID {
		t.Errorf("Expected ProjectID = %d, got %d", projectID, config.ProjectID)
	}
	if config.Domain != domain {
		t.Errorf("Expected Domain = %s, got %s", domain, config.Domain)
	}
	if config.JSRenderingEnabled != false {
		t.Errorf("Expected JSRenderingEnabled = false, got %v", config.JSRenderingEnabled)
	}
	if config.Parallelism != 5 {
		t.Errorf("Expected Parallelism = 5, got %d", config.Parallelism)
	}
	if config.IncludeSubdomains != true {
		t.Errorf("Expected IncludeSubdomains = true, got %v", config.IncludeSubdomains)
	}
	if config.CheckExternalResources != true {
		t.Errorf("Expected CheckExternalResources = true, got %v", config.CheckExternalResources)
	}
	mechanisms := config.GetDiscoveryMechanismsArray()
	if len(mechanisms) != 2 || mechanisms[0] != "spider" || mechanisms[1] != "sitemap" {
		t.Errorf("Expected DiscoveryMechanisms = [spider sitemap], got %v", mechanisms)
	}
}

// TestConfigPersistence tests that config changes are persisted to database
func TestConfigPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	projectID := uint(1)
	domain := "example.com"

	// Create store and set config
	{
		store, err := newStoreWithPath(dbPath)
		if err != nil {
			t.Fatalf("Failed to create store: %v", err)
		}

		_, err = store.GetOrCreateConfig(projectID, domain)
		if err != nil {
			t.Fatalf("GetOrCreateConfig() error = %v", err)
		}

		err = store.UpdateConfig(projectID, true, 1500, 2000, 1000, 20, "test", false, []string{"spider"}, []string{}, false, "respect", false, false, true, true)
		if err != nil {
			t.Fatalf("UpdateConfig() error = %v", err)
		}
	}

	// Reopen store and verify config is persisted
	{
		store, err := newStoreWithPath(dbPath)
		if err != nil {
			t.Fatalf("Failed to reopen store: %v", err)
		}

		config, err := store.GetOrCreateConfig(projectID, domain)
		if err != nil {
			t.Fatalf("GetOrCreateConfig() error = %v", err)
		}

		if !config.JSRenderingEnabled {
			t.Errorf("Expected JSRenderingEnabled = true after reopening, got false")
		}
		if config.Parallelism != 20 {
			t.Errorf("Expected Parallelism = 20 after reopening, got %d", config.Parallelism)
		}
	}

	// Clean up
	os.RemoveAll(tmpDir)
}
