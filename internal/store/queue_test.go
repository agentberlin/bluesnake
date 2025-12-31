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
	"testing"
)

func TestAddAndMarkVisited(t *testing.T) {
	// This test describes the expected behavior of the fix.

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

	t.Run("AddAndMarkVisited_NewURL_CreatesWithVisitedTrue", func(t *testing.T) {
		url := "https://example.com/sitemap-page"
		urlHash := int64(11111)

		// This function should create the URL in queue with visited=true
		err := store.AddAndMarkVisited(project.ID, url, urlHash, "sitemap")
		if err != nil {
			t.Fatalf("AddAndMarkVisited() failed: %v", err)
		}

		// Verify URL is in queue with visited=true
		stats, err := store.GetQueueStats(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStats() failed: %v", err)
		}

		if stats.Visited != 1 {
			t.Errorf("Expected 1 visited URL, got %d", stats.Visited)
		}
		if stats.Total != 1 {
			t.Errorf("Expected 1 total URL, got %d", stats.Total)
		}
	})

	t.Run("AddAndMarkVisited_ExistingURL_UpdatesToVisited", func(t *testing.T) {
		url := "https://example.com/spider-page"
		urlHash := int64(22222)

		// First, add URL to queue with visited=false (simulating spider discovery)
		err := store.AddSingleToQueue(project.ID, url, urlHash, "spider", 0)
		if err != nil {
			t.Fatalf("AddSingleToQueue() failed: %v", err)
		}

		// Verify it's pending (visited=false)
		stats, err := store.GetQueueStats(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStats() failed: %v", err)
		}
		initialPending := stats.Pending

		// Now call AddAndMarkVisited - should update existing to visited=true
		err = store.AddAndMarkVisited(project.ID, url, urlHash, "crawled")
		if err != nil {
			t.Fatalf("AddAndMarkVisited() failed: %v", err)
		}

		// Verify it's now visited
		stats, err = store.GetQueueStats(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStats() failed: %v", err)
		}

		// Pending should decrease by 1, Visited should increase by 1
		if stats.Pending != initialPending-1 {
			t.Errorf("Expected pending to decrease by 1, was %d now %d", initialPending, stats.Pending)
		}
	})

	t.Run("AddAndMarkVisited_Idempotent", func(t *testing.T) {
		url := "https://example.com/idempotent-page"
		urlHash := int64(33333)

		// Call AddAndMarkVisited twice with same URL
		err := store.AddAndMarkVisited(project.ID, url, urlHash, "crawled")
		if err != nil {
			t.Fatalf("First AddAndMarkVisited() failed: %v", err)
		}

		err = store.AddAndMarkVisited(project.ID, url, urlHash, "crawled")
		if err != nil {
			t.Fatalf("Second AddAndMarkVisited() failed: %v", err)
		}

		// Should still have just one entry
		item, err := store.GetQueueItemByURL(project.ID, url)
		if err != nil {
			t.Fatalf("GetQueueItemByURL() failed: %v", err)
		}

		if item == nil {
			t.Fatal("Expected to find queue item, got nil")
		}
		if !item.Visited {
			t.Error("Expected item to be visited")
		}
	})
}

func TestIncrementalCrawlResume_AllURLsTracked(t *testing.T) {
	// This integration test simulates the incremental crawl scenario
	// and verifies all crawled URLs are properly tracked in the queue.

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

	t.Run("AllCrawledURLsAreTracked", func(t *testing.T) {
		// Simulate crawling URLs from different sources:
		// 1. Initial URL (added before crawl)
		// 2. Spider-discovered URL (added to queue, then crawled)
		// 3. Sitemap URL (NOT in queue before crawl - this is the bug case)
		// 4. Redirect destination (NOT in queue before crawl)

		// 1. Initial URL - add to queue first
		initialURL := "https://example.com/"
		initialHash := int64(1001)
		store.AddSingleToQueue(project.ID, initialURL, initialHash, "initial", 0)

		// 2. Spider URL - add to queue first
		spiderURL := "https://example.com/page1"
		spiderHash := int64(1002)
		store.AddSingleToQueue(project.ID, spiderURL, spiderHash, "spider", 1)

		// 3. Sitemap URL - NOT added to queue (simulates current bug)
		sitemapURL := "https://example.com/sitemap-page"
		sitemapHash := int64(1003)

		// 4. Redirect destination - NOT added to queue (simulates current bug)
		redirectURL := "https://example.com/redirected"
		redirectHash := int64(1004)

		// Simulate crawling all 4 URLs using AddAndMarkVisited
		// (This is what the fix should do)
		urls := []struct {
			url    string
			hash   int64
			source string
		}{
			{initialURL, initialHash, "crawled"},
			{spiderURL, spiderHash, "crawled"},
			{sitemapURL, sitemapHash, "crawled"},   // Not in queue before!
			{redirectURL, redirectHash, "crawled"}, // Not in queue before!
		}

		for _, u := range urls {
			err := store.AddAndMarkVisited(project.ID, u.url, u.hash, u.source)
			if err != nil {
				t.Fatalf("AddAndMarkVisited(%s) failed: %v", u.url, err)
			}
		}

		// Verify ALL 4 URLs are tracked as visited
		stats, err := store.GetQueueStats(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStats() failed: %v", err)
		}

		if stats.Visited != 4 {
			t.Errorf("Expected 4 visited URLs (all crawled URLs tracked), got %d", stats.Visited)
		}
		if stats.Pending != 0 {
			t.Errorf("Expected 0 pending URLs, got %d", stats.Pending)
		}

		// Verify each URL is retrievable
		for _, u := range urls {
			item, err := store.GetQueueItemByURL(project.ID, u.url)
			if err != nil {
				t.Fatalf("GetQueueItemByURL(%s) failed: %v", u.url, err)
			}
			if item == nil {
				t.Errorf("URL %s not found in queue", u.url)
			} else if !item.Visited {
				t.Errorf("URL %s should be visited", u.url)
			}
		}
	})
}
