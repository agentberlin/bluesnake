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

func TestCreateIncrementalRun(t *testing.T) {
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

	t.Run("CreateRun_Succeeds", func(t *testing.T) {
		run, err := store.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("CreateIncrementalRun() failed: %v", err)
		}

		if run.ID == 0 {
			t.Error("Expected run ID to be non-zero")
		}
		if run.ProjectID != project.ID {
			t.Errorf("Expected ProjectID = %d, got %d", project.ID, run.ProjectID)
		}
		if run.State != RunStateInProgress {
			t.Errorf("Expected State = %q, got %q", RunStateInProgress, run.State)
		}
	})
}

func TestRunStateManagement(t *testing.T) {
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

	t.Run("UpdateRunState_Works", func(t *testing.T) {
		run, err := store.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("CreateIncrementalRun() failed: %v", err)
		}

		// Update to paused
		err = store.UpdateRunState(run.ID, RunStatePaused)
		if err != nil {
			t.Fatalf("UpdateRunState() failed: %v", err)
		}

		// Verify
		updated, err := store.GetRunByID(run.ID)
		if err != nil {
			t.Fatalf("GetRunByID() failed: %v", err)
		}

		if updated.State != RunStatePaused {
			t.Errorf("Expected state = %q, got %q", RunStatePaused, updated.State)
		}
	})

	t.Run("GetPausedRun_ReturnsPausedRun", func(t *testing.T) {
		// Create a new project for isolation
		proj, _ := store.GetOrCreateProject("https://paused-run-test.com", "paused-run-test.com")

		// Create run and set to paused
		run, _ := store.CreateIncrementalRun(proj.ID)
		store.UpdateRunState(run.ID, RunStatePaused)

		// Should find the paused run
		paused, err := store.GetPausedRun(proj.ID)
		if err != nil {
			t.Fatalf("GetPausedRun() failed: %v", err)
		}

		if paused == nil {
			t.Fatal("Expected to find paused run, got nil")
		}

		if paused.ID != run.ID {
			t.Errorf("Expected run ID %d, got %d", run.ID, paused.ID)
		}
	})

	t.Run("GetPausedRun_ReturnsNilWhenNoPausedRuns", func(t *testing.T) {
		// Create a new project for isolation
		proj, _ := store.GetOrCreateProject("https://no-paused-run.com", "no-paused-run.com")

		// Create run but leave as in_progress
		store.CreateIncrementalRun(proj.ID)

		// Should not find any paused run
		paused, err := store.GetPausedRun(proj.ID)
		if err != nil {
			t.Fatalf("GetPausedRun() failed: %v", err)
		}

		if paused != nil {
			t.Errorf("Expected nil (no paused run), got run ID %d", paused.ID)
		}
	})

	t.Run("GetInProgressRun_ReturnsInProgressRun", func(t *testing.T) {
		// Create a new project for isolation
		proj, _ := store.GetOrCreateProject("https://in-progress-run.com", "in-progress-run.com")

		// Create run (starts as in_progress)
		run, _ := store.CreateIncrementalRun(proj.ID)

		// Should find the in-progress run
		inProgress, err := store.GetInProgressRun(proj.ID)
		if err != nil {
			t.Fatalf("GetInProgressRun() failed: %v", err)
		}

		if inProgress == nil {
			t.Fatal("Expected to find in-progress run, got nil")
		}

		if inProgress.ID != run.ID {
			t.Errorf("Expected run ID %d, got %d", run.ID, inProgress.ID)
		}
	})
}

func TestCreateCrawlWithRun(t *testing.T) {
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

	t.Run("CreateCrawlWithRun_Succeeds", func(t *testing.T) {
		run, err := store.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("CreateIncrementalRun() failed: %v", err)
		}

		crawl, err := store.CreateCrawlWithRun(project.ID, run.ID)
		if err != nil {
			t.Fatalf("CreateCrawlWithRun() failed: %v", err)
		}

		if crawl.ID == 0 {
			t.Error("Expected crawl ID to be non-zero")
		}
		if crawl.ProjectID != project.ID {
			t.Errorf("Expected ProjectID = %d, got %d", project.ID, crawl.ProjectID)
		}
		if crawl.RunID == nil || *crawl.RunID != run.ID {
			t.Errorf("Expected RunID = %d, got %v", run.ID, crawl.RunID)
		}
		if crawl.State != CrawlStateInProgress {
			t.Errorf("Expected State = %q, got %q", CrawlStateInProgress, crawl.State)
		}
	})
}

func TestGetRunCrawls(t *testing.T) {
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

	t.Run("GetRunCrawls_ReturnsAllCrawlsForRun", func(t *testing.T) {
		run, err := store.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("CreateIncrementalRun() failed: %v", err)
		}

		// Create multiple crawls under this run
		crawl1, _ := store.CreateCrawlWithRun(project.ID, run.ID)
		crawl2, _ := store.CreateCrawlWithRun(project.ID, run.ID)

		// Get all crawls for the run
		crawls, err := store.GetRunCrawls(run.ID)
		if err != nil {
			t.Fatalf("GetRunCrawls() failed: %v", err)
		}

		if len(crawls) != 2 {
			t.Errorf("Expected 2 crawls, got %d", len(crawls))
		}

		// Verify both crawls are in the list
		foundCrawl1, foundCrawl2 := false, false
		for _, c := range crawls {
			if c.ID == crawl1.ID {
				foundCrawl1 = true
			}
			if c.ID == crawl2.ID {
				foundCrawl2 = true
			}
		}

		if !foundCrawl1 || !foundCrawl2 {
			t.Error("Not all crawls found in GetRunCrawls result")
		}
	})
}

func TestRunWithCrawlsRelationship(t *testing.T) {
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

	t.Run("GetRunWithCrawls_PreloadsCrawls", func(t *testing.T) {
		run, err := store.CreateIncrementalRun(project.ID)
		if err != nil {
			t.Fatalf("CreateIncrementalRun() failed: %v", err)
		}

		// Create crawls under this run
		store.CreateCrawlWithRun(project.ID, run.ID)
		store.CreateCrawlWithRun(project.ID, run.ID)

		// Get run with crawls preloaded
		runWithCrawls, err := store.GetRunWithCrawls(run.ID)
		if err != nil {
			t.Fatalf("GetRunWithCrawls() failed: %v", err)
		}

		if runWithCrawls == nil {
			t.Fatal("Expected run, got nil")
		}

		if len(runWithCrawls.Crawls) != 2 {
			t.Errorf("Expected 2 preloaded crawls, got %d", len(runWithCrawls.Crawls))
		}
	})
}

func TestGetActiveOrPausedRun(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := newStoreWithPath(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Run("Returns_InProgressRun", func(t *testing.T) {
		proj, _ := store.GetOrCreateProject("https://active-test-1.com", "active-test-1.com")
		run, _ := store.CreateIncrementalRun(proj.ID)

		result, err := store.GetActiveOrPausedRun(proj.ID)
		if err != nil {
			t.Fatalf("GetActiveOrPausedRun() failed: %v", err)
		}
		if result == nil || result.ID != run.ID {
			t.Error("Expected to find in-progress run")
		}
	})

	t.Run("Returns_PausedRun", func(t *testing.T) {
		proj, _ := store.GetOrCreateProject("https://active-test-2.com", "active-test-2.com")
		run, _ := store.CreateIncrementalRun(proj.ID)
		store.UpdateRunState(run.ID, RunStatePaused)

		result, err := store.GetActiveOrPausedRun(proj.ID)
		if err != nil {
			t.Fatalf("GetActiveOrPausedRun() failed: %v", err)
		}
		if result == nil || result.ID != run.ID {
			t.Error("Expected to find paused run")
		}
	})

	t.Run("Returns_NilForCompletedRun", func(t *testing.T) {
		proj, _ := store.GetOrCreateProject("https://active-test-3.com", "active-test-3.com")
		run, _ := store.CreateIncrementalRun(proj.ID)
		store.UpdateRunState(run.ID, RunStateCompleted)

		result, err := store.GetActiveOrPausedRun(proj.ID)
		if err != nil {
			t.Fatalf("GetActiveOrPausedRun() failed: %v", err)
		}
		if result != nil {
			t.Error("Expected nil for completed run, got a run")
		}
	})
}
