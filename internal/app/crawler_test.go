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

package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/store"
)

// TestBuildDomainFilter tests the domain filter regex generation
func TestBuildDomainFilter(t *testing.T) {
	tests := []struct {
		name              string
		domain            string
		includeSubdomains bool
		shouldMatch       []string
		shouldNotMatch    []string
	}{
		{
			name:              "Exact domain only",
			domain:            "example.com",
			includeSubdomains: false,
			shouldMatch: []string{
				"https://example.com/",
				"https://example.com/page",
				"https://example.com/path/to/page",
				"http://example.com/",
			},
			shouldNotMatch: []string{
				"https://blog.example.com/",
				"https://api.example.com/page",
				"https://sub.example.com/",
				"https://example.org/",
				"https://notexample.com/",
			},
		},
		{
			name:              "Include subdomains",
			domain:            "example.com",
			includeSubdomains: true,
			shouldMatch: []string{
				"https://example.com/",
				"https://example.com/page",
				"https://blog.example.com/",
				"https://api.example.com/page",
				"https://deep.sub.example.com/",
				"http://example.com/",
				"http://blog.example.com/",
			},
			shouldNotMatch: []string{
				"https://example.org/",
				"https://notexample.com/",
				"https://examplecom.com/",
			},
		},
		{
			name:              "Domain with port - exact",
			domain:            "example.com:8080",
			includeSubdomains: false,
			shouldMatch: []string{
				"https://example.com:8080/",
				"https://example.com:8080/page",
				"http://example.com:8080/",
			},
			shouldNotMatch: []string{
				"https://example.com/", // different port
				"https://blog.example.com:8080/",
			},
		},
		{
			name:              "Domain with port - include subdomains (port must match)",
			domain:            "example.com:8080",
			includeSubdomains: true,
			shouldMatch: []string{
				"https://example.com:8080/",
				"https://blog.example.com:8080/",
				"https://api.example.com:8080/page",
				"http://example.com:8080/",
			},
			shouldNotMatch: []string{
				"https://example.com/",          // different port (default 443)
				"https://blog.example.com/",     // different port (default 443)
				"https://example.com:9000/",     // different port number
				"https://example.org:8080/",     // different domain
				"https://notexample.com:8080/",  // different domain
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := buildDomainFilter(tt.domain, tt.includeSubdomains)
			if err != nil {
				t.Fatalf("buildDomainFilter() error = %v", err)
			}

			// Test URLs that should match
			for _, url := range tt.shouldMatch {
				if !filter.MatchString(url) {
					t.Errorf("Expected %q to match domain %q (includeSubdomains=%v), but it didn't",
						url, tt.domain, tt.includeSubdomains)
				}
			}

			// Test URLs that should not match
			for _, url := range tt.shouldNotMatch {
				if filter.MatchString(url) {
					t.Errorf("Expected %q NOT to match domain %q (includeSubdomains=%v), but it did",
						url, tt.domain, tt.includeSubdomains)
				}
			}
		})
	}
}

// TestGetQueueStatus_CanResume tests that GetQueueStatus returns canResume=true
// when there's a paused run with pending URLs in the queue
func TestGetQueueStatus_CanResume(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := store.NewStoreForTesting(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	emitter := &NoOpEmitter{}
	app := NewApp(st, emitter)
	app.Startup(context.Background())

	t.Run("CanResume_TrueWhenPausedRunWithPendingURLs", func(t *testing.T) {
		// Create project
		project, err := st.GetOrCreateProject("https://resume-test.com", "resume-test.com")
		if err != nil {
			t.Fatalf("Failed to create project: %v", err)
		}

		// Create a run and set it to paused
		run, err := st.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("Failed to create run: %v", err)
		}
		if err := st.UpdateRunState(run.ID, store.RunStatePaused); err != nil {
			t.Fatalf("Failed to update run state: %v", err)
		}

		// Add pending URLs to the queue
		queueItems := []store.CrawlQueueItem{
			{ProjectID: project.ID, URL: "https://resume-test.com/page1", URLHash: 12345, Visited: false},
			{ProjectID: project.ID, URL: "https://resume-test.com/page2", URLHash: 67890, Visited: false},
		}
		if err := st.AddToQueue(project.ID, queueItems); err != nil {
			t.Fatalf("Failed to add to queue: %v", err)
		}

		// Get queue status
		status, err := app.GetQueueStatus(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStatus() failed: %v", err)
		}

		// Verify canResume is true
		if !status.CanResume {
			t.Error("Expected CanResume to be true when paused run has pending URLs")
		}
		if status.Pending != 2 {
			t.Errorf("Expected 2 pending URLs, got %d", status.Pending)
		}
	})

	t.Run("CanResume_FalseWhenNoPausedRun", func(t *testing.T) {
		// Create project with no paused run
		project, err := st.GetOrCreateProject("https://no-pause-test.com", "no-pause-test.com")
		if err != nil {
			t.Fatalf("Failed to create project: %v", err)
		}

		// Create a run but leave it in_progress (not paused)
		_, err = st.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("Failed to create run: %v", err)
		}

		// Add pending URLs to the queue
		queueItems := []store.CrawlQueueItem{
			{ProjectID: project.ID, URL: "https://no-pause-test.com/page1", URLHash: 11111, Visited: false},
		}
		if err := st.AddToQueue(project.ID, queueItems); err != nil {
			t.Fatalf("Failed to add to queue: %v", err)
		}

		// Get queue status
		status, err := app.GetQueueStatus(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStatus() failed: %v", err)
		}

		// Verify canResume is false (run is in_progress, not paused)
		if status.CanResume {
			t.Error("Expected CanResume to be false when run is in_progress (not paused)")
		}
	})

	t.Run("CanResume_FalseWhenNoPendingURLs", func(t *testing.T) {
		// Create project
		project, err := st.GetOrCreateProject("https://no-pending-test.com", "no-pending-test.com")
		if err != nil {
			t.Fatalf("Failed to create project: %v", err)
		}

		// Create a run and set it to paused
		run, err := st.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("Failed to create run: %v", err)
		}
		if err := st.UpdateRunState(run.ID, store.RunStatePaused); err != nil {
			t.Fatalf("Failed to update run state: %v", err)
		}

		// Don't add any URLs to the queue

		// Get queue status
		status, err := app.GetQueueStatus(project.ID)
		if err != nil {
			t.Fatalf("GetQueueStatus() failed: %v", err)
		}

		// Verify canResume is false (no pending URLs)
		if status.CanResume {
			t.Error("Expected CanResume to be false when no pending URLs in queue")
		}
	})
}
