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
	"time"
)

func TestDeleteCrawl(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a project first (needed for creating crawls)
	project, err := store.GetOrCreateProject("https://example.com", "example.com")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	t.Run("DeleteExistingCrawl_Succeeds", func(t *testing.T) {
		// Create a crawl
		crawl, err := store.CreateCrawl(project.ID, time.Now().Unix(), 1000, 5)
		if err != nil {
			t.Fatalf("Failed to create crawl: %v", err)
		}

		// Delete the crawl
		err = store.DeleteCrawl(crawl.ID)
		if err != nil {
			t.Errorf("DeleteCrawl() failed for existing crawl: %v", err)
		}

		// Verify crawl is deleted
		crawls, err := store.GetProjectCrawls(project.ID)
		if err != nil {
			t.Fatalf("GetProjectCrawls() failed: %v", err)
		}

		for _, c := range crawls {
			if c.ID == crawl.ID {
				t.Errorf("Crawl %d should have been deleted but still exists", crawl.ID)
			}
		}
	})

	t.Run("DeleteNonExistentCrawl_ReturnsError", func(t *testing.T) {
		// Attempt to delete a crawl that doesn't exist
		nonExistentID := uint(999999)
		err := store.DeleteCrawl(nonExistentID)

		if err == nil {
			t.Error("DeleteCrawl() should return error for non-existent crawl, but got nil")
		}

		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected error message to contain 'not found', got: %v", err)
		}
	})

	t.Run("DeleteCrawl_WithDiscoveredUrls", func(t *testing.T) {
		// Create a crawl
		crawl, err := store.CreateCrawl(project.ID, time.Now().Unix(), 1000, 5)
		if err != nil {
			t.Fatalf("Failed to create crawl: %v", err)
		}

		// Add a discovered URL to the crawl
		err = store.SaveDiscoveredUrl(crawl.ID, "https://example.com/page1", true, 200, "Page 1", "Description", "H1 Heading", "H2 Heading", "https://example.com/page1", 100, "hash123", "yes", "text/html", "")
		if err != nil {
			t.Fatalf("Failed to save discovered URL: %v", err)
		}

		// Delete the crawl (should cascade delete discovered URLs due to OnDelete:CASCADE in model)
		err = store.DeleteCrawl(crawl.ID)
		if err != nil {
			t.Errorf("DeleteCrawl() failed: %v", err)
		}

		// Note: CASCADE behavior is tested via database constraints in models.go
		// The Crawl model has: `gorm:"foreignKey:CrawlID;constraint:OnDelete:CASCADE"`
	})
}

func TestCreateCrawl(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a project first
	project, err := store.GetOrCreateProject("https://example.com", "example.com")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	t.Run("CreateCrawl_Succeeds", func(t *testing.T) {
		crawlDateTime := time.Now().Unix()
		crawlDuration := int64(5000)
		pagesCrawled := 10

		crawl, err := store.CreateCrawl(project.ID, crawlDateTime, crawlDuration, pagesCrawled)
		if err != nil {
			t.Fatalf("CreateCrawl() failed: %v", err)
		}

		if crawl.ProjectID != project.ID {
			t.Errorf("Expected ProjectID = %d, got %d", project.ID, crawl.ProjectID)
		}
		if crawl.CrawlDateTime != crawlDateTime {
			t.Errorf("Expected CrawlDateTime = %d, got %d", crawlDateTime, crawl.CrawlDateTime)
		}
		if crawl.CrawlDuration != crawlDuration {
			t.Errorf("Expected CrawlDuration = %d, got %d", crawlDuration, crawl.CrawlDuration)
		}
		if crawl.PagesCrawled != pagesCrawled {
			t.Errorf("Expected PagesCrawled = %d, got %d", pagesCrawled, crawl.PagesCrawled)
		}
		if crawl.ID == 0 {
			t.Error("Expected crawl ID to be non-zero")
		}
	})
}

func TestCrawlStateManagement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	project, err := store.GetOrCreateProject("https://example.com", "example.com")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	t.Run("NewCrawl_HasInProgressState", func(t *testing.T) {
		crawl, err := store.CreateCrawl(project.ID, time.Now().Unix(), 0, 0)
		if err != nil {
			t.Fatalf("CreateCrawl() failed: %v", err)
		}

		if crawl.State != CrawlStateInProgress {
			t.Errorf("Expected new crawl state = %q, got %q", CrawlStateInProgress, crawl.State)
		}
	})

	t.Run("UpdateCrawlState_Works", func(t *testing.T) {
		crawl, err := store.CreateCrawl(project.ID, time.Now().Unix(), 0, 0)
		if err != nil {
			t.Fatalf("CreateCrawl() failed: %v", err)
		}

		// Update to paused
		err = store.UpdateCrawlState(crawl.ID, CrawlStatePaused)
		if err != nil {
			t.Fatalf("UpdateCrawlState() failed: %v", err)
		}

		// Verify
		updated, err := store.GetCrawlByID(crawl.ID)
		if err != nil {
			t.Fatalf("GetCrawlByID() failed: %v", err)
		}

		if updated.State != CrawlStatePaused {
			t.Errorf("Expected state = %q, got %q", CrawlStatePaused, updated.State)
		}
	})

}

func TestGetProjectCrawls(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create a project
	project, err := store.GetOrCreateProject("https://example.com", "example.com")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	t.Run("GetCrawls_ReturnsMultipleCrawls", func(t *testing.T) {
		// Create multiple crawls
		crawl1, err := store.CreateCrawl(project.ID, time.Now().Unix(), 1000, 5)
		if err != nil {
			t.Fatalf("Failed to create crawl1: %v", err)
		}

		time.Sleep(10 * time.Millisecond) // Ensure different timestamps

		crawl2, err := store.CreateCrawl(project.ID, time.Now().Unix(), 2000, 10)
		if err != nil {
			t.Fatalf("Failed to create crawl2: %v", err)
		}

		// Get all crawls for project
		crawls, err := store.GetProjectCrawls(project.ID)
		if err != nil {
			t.Fatalf("GetProjectCrawls() failed: %v", err)
		}

		if len(crawls) < 2 {
			t.Errorf("Expected at least 2 crawls, got %d", len(crawls))
		}

		// Verify our crawls are in the list
		foundCrawl1 := false
		foundCrawl2 := false
		for _, c := range crawls {
			if c.ID == crawl1.ID {
				foundCrawl1 = true
			}
			if c.ID == crawl2.ID {
				foundCrawl2 = true
			}
		}

		if !foundCrawl1 {
			t.Error("Crawl1 not found in results")
		}
		if !foundCrawl2 {
			t.Error("Crawl2 not found in results")
		}
	})
}
