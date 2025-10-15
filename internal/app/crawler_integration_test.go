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
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// TestCrawlerIntegration tests the full crawling flow from UI perspective:
// 1. User enters a URL in the UI
// 2. UI calls StartCrawl(url) via Wails binding
// 3. Crawler discovers and crawls pages
// 4. Results are saved to database
func TestCrawlerIntegration(t *testing.T) {
	// Create a test HTTP server with 3 pages:
	// - index page (/) that links to page1 and page2
	// - page1 (/page1)
	// - page2 (/page2)
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Test Home Page</title>
    <meta name="description" content="Home page description">
</head>
<body>
    <h1>Welcome to Test Site</h1>
    <nav>
        <a href="/page1">Go to Page 1</a>
        <a href="/page2">Go to Page 2</a>
    </nav>
    <p>This is the home page content.</p>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	mux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Test Page 1</title>
    <meta name="description" content="Page 1 description">
</head>
<body>
    <h1>Page 1</h1>
    <p>This is page 1 content.</p>
    <a href="/">Back to Home</a>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head>
    <title>Test Page 2</title>
    <meta name="description" content="Page 2 description">
</head>
<body>
    <h1>Page 2</h1>
    <p>This is page 2 content.</p>
    <a href="/">Back to Home</a>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	// Start the test HTTP server
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Logf("Test server started at: %s", server.URL)

	// Create a temporary database for testing
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize the store (database) with test database path
	// We call the internal function directly since we're in the same module
	st, err := store.NewStoreForTesting(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	t.Log("Database initialized successfully")

	// Create a simple event emitter for testing
	emitter := &testEmitter{
		events: make(map[string]int),
	}

	// Create the app instance (mimicking what happens in cmd/desktop/main.go)
	coreApp := NewApp(st, emitter)
	ctx := context.Background()
	coreApp.Startup(ctx)

	t.Log("App initialized successfully")

	// This is what the UI does when user enters a URL and clicks "Start Crawl"
	// See: cmd/desktop/frontend/src/App.tsx:handleStartCrawl() -> StartCrawl(url)
	startURL := server.URL + "/"

	t.Logf("Starting crawl for URL: %s", startURL)

	err = coreApp.StartCrawl(startURL)
	if err != nil {
		t.Fatalf("Failed to start crawl: %v", err)
	}

	t.Log("Crawl started successfully")

	// Wait for the crawl to complete
	// In the real UI, this is handled by polling GetActiveCrawls() and GetActiveCrawlStats()
	// For the test, we'll wait up to 30 seconds and check periodically
	maxWaitTime := 30 * time.Second
	checkInterval := 100 * time.Millisecond // Use shorter interval to catch fast crawls
	startTime := time.Now()

	var crawlCompleted bool

	for time.Since(startTime) < maxWaitTime {
		// Check if crawl exists in database (more reliable than checking active crawls)
		projects, err := coreApp.GetProjects()
		if err == nil && len(projects) > 0 {
			crawls, err := coreApp.GetCrawls(projects[0].ID)
			if err == nil && len(crawls) > 0 {
				// Crawl record exists in database
				activeCrawls := coreApp.GetActiveCrawls()
				if len(activeCrawls) == 0 {
					// Crawl is in database but not active = completed
					crawlCompleted = true
					t.Log("Crawl completed (found in database, not in active crawls)")
					break
				} else {
					// Still actively crawling
					t.Logf("Crawl progress: %d/%d URLs crawled", activeCrawls[0].TotalURLsCrawled, activeCrawls[0].TotalDiscovered)
				}
			}
		}

		time.Sleep(checkInterval)
	}

	if !crawlCompleted {
		t.Fatalf("Crawl did not complete within %v", maxWaitTime)
	}

	t.Log("Crawl completed, verifying results...")

	// Get the project that was created
	projects, err := coreApp.GetProjects()
	if err != nil {
		t.Fatalf("Failed to get projects: %v", err)
	}

	if len(projects) != 1 {
		t.Fatalf("Expected 1 project, got %d", len(projects))
	}

	project := projects[0]
	t.Logf("Project created: ID=%d, URL=%s, Domain=%s", project.ID, project.URL, project.Domain)

	// Get the crawls for this project
	crawls, err := coreApp.GetCrawls(project.ID)
	if err != nil {
		t.Fatalf("Failed to get crawls: %v", err)
	}

	if len(crawls) != 1 {
		t.Fatalf("Expected 1 crawl, got %d", len(crawls))
	}

	crawl := crawls[0]
	t.Logf("Crawl info: ID=%d, Pages=%d, Duration=%dms", crawl.ID, crawl.PagesCrawled, crawl.CrawlDuration)

	// Verify that we crawled all 3 pages
	if crawl.PagesCrawled < 3 {
		t.Errorf("Expected at least 3 pages crawled, got %d", crawl.PagesCrawled)
	}

	// Get crawl stats to verify URLs were discovered
	stats, err := coreApp.GetCrawlStats(crawl.ID)
	if err != nil {
		t.Fatalf("Failed to get crawl stats: %v", err)
	}

	t.Logf("Crawl stats: Total=%d, Crawled=%d, HTML=%d", stats.Total, stats.Crawled, stats.HTML)

	// Verify we have at least 3 HTML pages
	if stats.HTML < 3 {
		t.Errorf("Expected at least 3 HTML pages, got %d", stats.HTML)
	}

	// Get the actual crawl results to verify page titles and content
	results, err := coreApp.GetCrawlWithResultsPaginated(crawl.ID, 100, 0, "html")
	if err != nil {
		t.Fatalf("Failed to get crawl results: %v", err)
	}

	if len(results.Results) < 3 {
		t.Errorf("Expected at least 3 results, got %d", len(results.Results))
	}

	// Verify page titles are correct
	expectedTitles := map[string]bool{
		"Test Home Page": false,
		"Test Page 1":    false,
		"Test Page 2":    false,
	}

	for _, result := range results.Results {
		t.Logf("Result: URL=%s, Status=%d, Title=%s", result.URL, result.Status, result.Title)

		// Check status code
		if result.Status != 200 {
			t.Errorf("Expected status 200 for %s, got %d", result.URL, result.Status)
		}

		// Check title
		if _, ok := expectedTitles[result.Title]; ok {
			expectedTitles[result.Title] = true
		}
	}

	// Verify all expected titles were found
	for title, found := range expectedTitles {
		if !found {
			t.Errorf("Expected to find page with title '%s', but it was not crawled", title)
		}
	}

	// Verify links were saved correctly
	// Get links for the home page
	homeURL := server.URL + "/"
	linksResponse, err := coreApp.GetPageLinksForURL(crawl.ID, homeURL)
	if err != nil {
		t.Fatalf("Failed to get page links: %v", err)
	}

	t.Logf("Home page has %d outlinks and %d inlinks", len(linksResponse.Outlinks), len(linksResponse.Inlinks))

	// Verify home page has outlinks to page1 and page2
	expectedOutlinks := map[string]bool{
		server.URL + "/page1": false,
		server.URL + "/page2": false,
	}

	for _, link := range linksResponse.Outlinks {
		if _, ok := expectedOutlinks[link.URL]; ok {
			expectedOutlinks[link.URL] = true
		}
	}

	for url, found := range expectedOutlinks {
		if !found {
			t.Errorf("Expected home page to link to '%s', but link was not found", url)
		}
	}

	// Verify event emissions
	if emitter.events["crawl:started"] != 1 {
		t.Errorf("Expected 1 crawl:started event, got %d", emitter.events["crawl:started"])
	}

	if emitter.events["crawl:completed"] != 1 {
		t.Errorf("Expected 1 crawl:completed event, got %d", emitter.events["crawl:completed"])
	}

	t.Log("✓ All integration test checks passed!")
}

// testEmitter implements EventEmitter for testing
type testEmitter struct {
	events map[string]int
	mu     sync.Mutex
}

func (e *testEmitter) Emit(eventType EventType, data interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events[string(eventType)]++
}
