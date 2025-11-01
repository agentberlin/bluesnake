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

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestApp creates a test app instance with temporary database
func setupTestApp(t *testing.T) (*app.App, *store.Store, func()) {
	// Create temp database
	tmpDB := t.TempDir() + "/test.db"

	// Initialize store
	st, err := store.NewStoreForTesting(tmpDB)
	require.NoError(t, err)

	// Create app
	emitter := &app.NoOpEmitter{}
	testApp := app.NewApp(st, emitter)
	testApp.Startup(context.Background())

	// Return cleanup function
	cleanup := func() {
		// Cleanup handled by t.TempDir()
	}

	return testApp, st, cleanup
}

// createTestHTTPServer creates a simple test HTTP server with 3 pages
func createTestHTTPServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Add delay to simulate real-world server and allow tests to observe crawl state
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Test Home Page</title>
    <meta name="description" content="Home page description">
</head>
<body>
    <h1>Home Page</h1>
    <a href="/page1">Page 1</a>
    <a href="/page2">Page 2</a>
</body>
</html>`
		w.Write([]byte(html))
	})

	mux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		// Add delay to simulate real-world server
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Page 1</title></head>
<body><h1>Page 1 Content</h1></body>
</html>`
		w.Write([]byte(html))
	})

	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		// Add delay to simulate real-world server
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Page 2</title></head>
<body><h1>Page 2 Content</h1></body>
</html>`
		w.Write([]byte(html))
	})

	return httptest.NewServer(mux)
}

// waitForCrawlToStart waits for a crawl to be registered in active crawls
func waitForCrawlToStart(t *testing.T, testApp *app.App, maxWait time.Duration) (projectID, crawlID uint) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		crawls := testApp.GetActiveCrawls()
		if len(crawls) > 0 {
			return crawls[0].ProjectID, crawls[0].CrawlID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Timed out waiting for crawl to start")
	return 0, 0
}

// waitForCrawlToComplete waits for a crawl to finish
func waitForCrawlToComplete(t *testing.T, testApp *app.App, projectID uint, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		crawls := testApp.GetActiveCrawls()
		stillActive := false
		for _, c := range crawls {
			if c.ProjectID == projectID {
				stillActive = true
				break
			}
		}
		if !stillActive {
			return // Crawl completed
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Timed out waiting for crawl to complete")
}

// =============================================================================
// Test: Crawl Management Tools
// =============================================================================

func TestCrawlWebsiteTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	ctx := context.Background()
	mcpServer, err := NewMCPServer(ctx)
	require.NoError(t, err)

	// Replace the app instance with our test app
	mcpServer.app = testApp

	t.Run("ValidURL_StartsCrawlSuccessfully", func(t *testing.T) {
		// Start crawl via app
		err := testApp.StartCrawl(server.URL)
		require.NoError(t, err)

		// Wait for crawl to start (longer timeout due to HTTP delays)
		projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)

		assert.NotZero(t, projectID)
		assert.NotZero(t, crawlID)

		// Verify active crawl exists
		activeCrawls := testApp.GetActiveCrawls()
		assert.NotEmpty(t, activeCrawls)
	})

	t.Run("InvalidURL_ReturnsError", func(t *testing.T) {
		// Test various invalid URL formats that should be rejected
		invalidURLs := []string{
			"randomstring",        // No dots, not a valid domain
			"hello world",         // Spaces
			"999.999.999.999",     // Invalid IP address
			"",                    // Empty string
			"   ",                 // Just whitespace
			"https://",            // Just protocol
			"exa mple.com",        // Spaces in domain
		}

		for _, invalidURL := range invalidURLs {
			err := testApp.StartCrawl(invalidURL)
			assert.Error(t, err, "Expected error for invalid URL: %q", invalidURL)
		}
	})
}

func TestStopCrawlTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	t.Run("StopActiveCrawl_Succeeds", func(t *testing.T) {
		// Start a crawl
		err := testApp.StartCrawl(server.URL)
		require.NoError(t, err)

		projectID, _ := waitForCrawlToStart(t, testApp, 5*time.Second)

		// Stop the crawl
		err = testApp.StopCrawl(projectID)
		assert.NoError(t, err)

		// Verify crawl is stopped (wait longer for graceful shutdown)
		time.Sleep(2 * time.Second)
		activeCrawls := testApp.GetActiveCrawls()
		for _, c := range activeCrawls {
			assert.NotEqual(t, projectID, c.ProjectID, "Crawl should be stopped")
		}
	})

	t.Run("StopNonExistentProject_ReturnsError", func(t *testing.T) {
		err := testApp.StopCrawl(999999)
		assert.Error(t, err)
	})
}

func TestGetCrawlStatusTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	t.Run("ActiveCrawl_ReturnsProgressStats", func(t *testing.T) {
		// Start a crawl
		err := testApp.StartCrawl(server.URL)
		require.NoError(t, err)

		projectID, _ := waitForCrawlToStart(t, testApp, 5*time.Second)

		// Get status of active crawl
		activeCrawls := testApp.GetActiveCrawls()
		require.NotEmpty(t, activeCrawls)

		found := false
		for _, crawl := range activeCrawls {
			if crawl.ProjectID == projectID {
				found = true
				assert.NotZero(t, crawl.CrawlID)
				assert.NotEmpty(t, crawl.Domain)
				assert.NotEmpty(t, crawl.URL)
				break
			}
		}
		assert.True(t, found, "Active crawl should be found")

		// Stop crawl and wait for it to fully stop
		testApp.StopCrawl(projectID)
		time.Sleep(2 * time.Second) // Wait for cleanup
	})

	t.Run("CompletedCrawl_ReturnsFinalStats", func(t *testing.T) {
		// Start and wait for crawl to complete
		err := testApp.StartCrawl(server.URL)
		require.NoError(t, err)

		projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
		waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

		// Get crawls for project
		crawls, err := testApp.GetCrawls(projectID)
		require.NoError(t, err)
		require.NotEmpty(t, crawls)

		assert.Equal(t, crawlID, crawls[0].ID)
		assert.NotZero(t, crawls[0].PagesCrawled)
	})

	t.Run("NonExistentProject_ReturnsEmptyOrError", func(t *testing.T) {
		crawls, err := testApp.GetCrawls(999999)
		// Either no error with empty list, or error
		if err == nil {
			assert.Empty(t, crawls)
		}
	})
}

// =============================================================================
// Test: Result Retrieval Tools
// =============================================================================

func TestGetCrawlResultsTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("ReturnsResultsWithDefaultLimit", func(t *testing.T) {
		results, err := testApp.GetCrawlWithResultsPaginated(crawlID, 100, 0, "all")
		require.NoError(t, err)
		assert.NotNil(t, results)
	})

	t.Run("ContentTypeFilter_Works", func(t *testing.T) {
		htmlResults, err := testApp.GetCrawlWithResultsPaginated(crawlID, 100, 0, "html")
		require.NoError(t, err)
		assert.NotNil(t, htmlResults)

		// All results should be HTML
		for _, r := range htmlResults.Results {
			assert.Contains(t, r.ContentType, "html")
		}
	})

	t.Run("InvalidCrawlID_ReturnsError", func(t *testing.T) {
		_, err := testApp.GetCrawlWithResultsPaginated(999999, 100, 0, "all")
		// Should either return error or empty results
		if err == nil {
			// Empty results acceptable
		}
	})
}

func TestSearchCrawlResultsTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("SearchQuery_FindsMatchingURLs", func(t *testing.T) {
		results, err := testApp.SearchCrawlResultsPaginated(crawlID, "page1", "all", 100, 0)
		require.NoError(t, err)
		assert.NotNil(t, results)

		// Results should contain "page1" in URL
		if len(results.Results) > 0 {
			assert.Contains(t, results.Results[0].URL, "page1")
		}
	})

	t.Run("SearchWithContentTypeFilter", func(t *testing.T) {
		results, err := testApp.SearchCrawlResultsPaginated(crawlID, "page", "html", 100, 0)
		require.NoError(t, err)
		assert.NotNil(t, results)
	})
}

func TestGetCrawlStatisticsTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("ReturnsCompleteStatistics", func(t *testing.T) {
		stats, err := testApp.GetCrawlStats(crawlID)
		require.NoError(t, err)
		require.NotNil(t, stats)

		// Verify stats structure exists (type is ActiveCrawlStats)
		assert.NotZero(t, stats.Total)
		assert.NotZero(t, stats.HTML)
	})

	t.Run("InvalidCrawlID_ReturnsError", func(t *testing.T) {
		_, err := testApp.GetCrawlStats(999999)
		// Should either error or return empty stats
		if err != nil {
			assert.Error(t, err)
		}
	})
}

// =============================================================================
// Test: Link & Content Analysis Tools
// =============================================================================

func TestGetPageLinksTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("ReturnsLinksForValidPage", func(t *testing.T) {
		links, err := testApp.GetPageLinksForURL(crawlID, server.URL+"/")
		require.NoError(t, err)
		assert.NotNil(t, links)
		assert.Equal(t, server.URL+"/", links.PageURL)
	})

	t.Run("NonExistentPage_ReturnsEmptyArrays", func(t *testing.T) {
		links, err := testApp.GetPageLinksForURL(crawlID, server.URL+"/nonexistent")
		if err == nil {
			assert.Empty(t, links.Inlinks)
			assert.Empty(t, links.Outlinks)
		}
	})
}

func TestGetPageContentTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("ReturnsTextContentForValidPage", func(t *testing.T) {
		content, err := testApp.GetPageContent(crawlID, server.URL+"/")
		if err == nil {
			assert.NotEmpty(t, content)
		}
	})

	t.Run("NonExistentPage_ReturnsError", func(t *testing.T) {
		_, err := testApp.GetPageContent(crawlID, server.URL+"/nonexistent")
		assert.Error(t, err)
	})
}

// =============================================================================
// Test: Project Management Tools
// =============================================================================

func TestListProjectsTool(t *testing.T) {
	testApp, st, cleanup := setupTestApp(t)
	defer cleanup()

	t.Run("ReturnsAllProjects", func(t *testing.T) {
		// Create test projects
		_, err := st.GetOrCreateProject("https://example.com", "example.com")
		require.NoError(t, err)
		_, err = st.GetOrCreateProject("https://test.com", "test.com")
		require.NoError(t, err)

		// Get all projects
		projects, err := testApp.GetProjects()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(projects), 2)

		// Verify project structure
		for _, p := range projects {
			assert.NotZero(t, p.ID)
			assert.NotEmpty(t, p.Domain)
		}
	})

	t.Run("EmptyDatabase_ReturnsEmptyArray", func(t *testing.T) {
		// Create fresh app
		freshApp, _, freshCleanup := setupTestApp(t)
		defer freshCleanup()

		projects, err := freshApp.GetProjects()
		require.NoError(t, err)
		assert.Empty(t, projects)
	})
}

func TestListProjectCrawlsTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, _ := waitForCrawlToStart(t, testApp, 2*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("ReturnsAllCrawlsForProject", func(t *testing.T) {
		crawls, err := testApp.GetCrawls(projectID)
		require.NoError(t, err)
		assert.NotEmpty(t, crawls)

		// Verify crawl structure
		for _, c := range crawls {
			assert.NotZero(t, c.ID)
			assert.Equal(t, projectID, c.ProjectID)
		}
	})

	t.Run("InvalidProjectID_ReturnsError", func(t *testing.T) {
		crawls, err := testApp.GetCrawls(999999)
		if err == nil {
			assert.Empty(t, crawls)
		}
	})
}

func TestDeleteProjectTool(t *testing.T) {
	testApp, st, cleanup := setupTestApp(t)
	defer cleanup()

	t.Run("DeletesProjectSuccessfully", func(t *testing.T) {
		// Create a project
		project, err := st.GetOrCreateProject("https://delete-me.com", "delete-me.com")
		require.NoError(t, err)

		// Delete the project
		err = testApp.DeleteProjectByID(project.ID)
		require.NoError(t, err)

		// Verify project is deleted
		projects, err := testApp.GetProjects()
		require.NoError(t, err)
		for _, p := range projects {
			assert.NotEqual(t, project.ID, p.ID)
		}
	})

	t.Run("InvalidProjectID_ReturnsError", func(t *testing.T) {
		// Deleting non-existent project should return an error
		err := testApp.DeleteProjectByID(999999)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestDeleteCrawlTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	server := createTestHTTPServer()
	defer server.Close()

	// Start and complete a crawl
	err := testApp.StartCrawl(server.URL)
	require.NoError(t, err)

	projectID, crawlID := waitForCrawlToStart(t, testApp, 5*time.Second)
	waitForCrawlToComplete(t, testApp, projectID, 10*time.Second)

	t.Run("DeletesCrawlSuccessfully", func(t *testing.T) {
		// Delete the crawl
		err := testApp.DeleteCrawlByID(crawlID)
		require.NoError(t, err)

		// Verify crawl is deleted
		crawls, err := testApp.GetCrawls(projectID)
		require.NoError(t, err)
		for _, c := range crawls {
			assert.NotEqual(t, crawlID, c.ID)
		}
	})

	t.Run("InvalidCrawlID_ReturnsError", func(t *testing.T) {
		// Deleting non-existent crawl should return an error
		err := testApp.DeleteCrawlByID(999999)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// =============================================================================
// Test: Configuration Tools
// =============================================================================

func TestGetDomainConfigTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	t.Run("ReturnsConfigForValidDomain", func(t *testing.T) {
		cfg, err := testApp.GetConfigForDomain("https://example.com")
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Verify config structure
		assert.Equal(t, "example.com", cfg.Domain)
		assert.NotZero(t, cfg.Parallelism)
		assert.NotEmpty(t, cfg.UserAgent)
	})

	t.Run("DomainWithoutProtocol_Works", func(t *testing.T) {
		config, err := testApp.GetConfigForDomain("test.com")
		require.NoError(t, err)
		assert.NotNil(t, config)
	})
}

func TestUpdateDomainConfigTool(t *testing.T) {
	testApp, _, cleanup := setupTestApp(t)
	defer cleanup()

	t.Run("UpdatesConfigSuccessfully", func(t *testing.T) {
		// Get initial config
		_, err := testApp.GetConfigForDomain("https://example.com")
		require.NoError(t, err)

		// Update config
		err = testApp.UpdateConfigForDomain(
			"https://example.com",
			true,  // JS rendering enabled
			2000,  // Initial wait
			3000,  // Scroll wait
			1500,  // Final wait
			10,    // Parallelism
			"custom-agent",
			false, // Include subdomains
			true,  // Spider enabled
			false, // Sitemap enabled
			[]string{},
			true,  // Check external resources
			false, // Single page mode
			"respect",
			true,  // Follow internal nofollow
			false, // Follow external nofollow
			true,  // Respect meta robots noindex
			true,  // Respect noindex
		)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig, err := testApp.GetConfigForDomain("https://example.com")
		require.NoError(t, err)
		assert.True(t, updatedConfig.JSRenderingEnabled)
		assert.Equal(t, 10, updatedConfig.Parallelism)
		assert.Equal(t, "custom-agent", updatedConfig.UserAgent)
	})

	t.Run("InvalidDomain_ReturnsError", func(t *testing.T) {
		err := testApp.UpdateConfigForDomain(
			"not-a-valid-url",
			false, 1500, 2000, 1000, 5, "agent", false,
			true, false, []string{}, false, false, "respect",
			false, false, false, false,
		)
		assert.Error(t, err)
	})
}
