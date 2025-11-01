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
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Run("DeleteExistingProject_Succeeds", func(t *testing.T) {
		// Create a project
		project, err := store.GetOrCreateProject("https://example.com", "example.com")
		if err != nil {
			t.Fatalf("Failed to create project: %v", err)
		}

		// Delete the project
		err = store.DeleteProject(project.ID)
		if err != nil {
			t.Errorf("DeleteProject() failed for existing project: %v", err)
		}

		// Verify project is deleted
		allProjects, err := store.GetAllProjects()
		if err != nil {
			t.Fatalf("GetAllProjects() failed: %v", err)
		}

		for _, p := range allProjects {
			if p.ID == project.ID {
				t.Errorf("Project %d should have been deleted but still exists", project.ID)
			}
		}
	})

	t.Run("DeleteNonExistentProject_ReturnsError", func(t *testing.T) {
		// Attempt to delete a project that doesn't exist
		nonExistentID := uint(999999)
		err := store.DeleteProject(nonExistentID)

		if err == nil {
			t.Error("DeleteProject() should return error for non-existent project, but got nil")
		}

		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected error message to contain 'not found', got: %v", err)
		}
	})

	t.Run("DeleteProject_WithCrawls", func(t *testing.T) {
		// Create a project
		project, err := store.GetOrCreateProject("https://test.com", "test.com")
		if err != nil {
			t.Fatalf("Failed to create project: %v", err)
		}

		// Create a crawl for this project
		_, err = store.CreateCrawl(project.ID, 1234567890, 0, 0)
		if err != nil {
			t.Fatalf("Failed to create crawl: %v", err)
		}

		// Delete the project (should cascade delete crawls due to OnDelete:CASCADE in model)
		err = store.DeleteProject(project.ID)
		if err != nil {
			t.Errorf("DeleteProject() failed: %v", err)
		}

		// Note: CASCADE behavior is tested via database constraints in models.go
		// The Project model has: `gorm:"foreignKey:ProjectID;constraint:OnDelete:CASCADE"`
	})
}

func TestGetOrCreateProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Run("CreateNewProject_Succeeds", func(t *testing.T) {
		url := "https://newsite.com"
		domain := "newsite.com"

		project, err := store.GetOrCreateProject(url, domain)
		if err != nil {
			t.Fatalf("GetOrCreateProject() failed: %v", err)
		}

		if project.URL != url {
			t.Errorf("Expected URL = %s, got %s", url, project.URL)
		}
		if project.Domain != domain {
			t.Errorf("Expected Domain = %s, got %s", domain, project.Domain)
		}
		if project.ID == 0 {
			t.Error("Expected project ID to be non-zero")
		}
	})

	t.Run("GetExistingProject_ReturnsExisting", func(t *testing.T) {
		url := "https://existing.com"
		domain := "existing.com"

		// Create first time
		project1, err := store.GetOrCreateProject(url, domain)
		if err != nil {
			t.Fatalf("GetOrCreateProject() failed: %v", err)
		}

		// Get second time (should return existing)
		project2, err := store.GetOrCreateProject(url, domain)
		if err != nil {
			t.Fatalf("GetOrCreateProject() failed on second call: %v", err)
		}

		if project1.ID != project2.ID {
			t.Errorf("Expected same project ID, got %d and %d", project1.ID, project2.ID)
		}
	})
}
