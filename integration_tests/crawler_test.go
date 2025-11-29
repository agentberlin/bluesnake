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

package integration_tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/types"
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
	dbPath := tmpDir + "/test.db"

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
	coreApp := app.NewApp(st, emitter)
	ctx := context.Background()
	coreApp.Startup(ctx)

	t.Log("App initialized successfully")

	// This is what the UI does when user enters a URL and clicks "Start Crawl"
	// See: cmd/desktop/frontend/src/App.tsx:handleStartCrawl() -> StartCrawl(url)
	startURL := server.URL + "/"

	t.Logf("Starting crawl for URL: %s", startURL)

	_, err = coreApp.StartCrawl(startURL)
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

func (e *testEmitter) Emit(eventType app.EventType, data interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events[string(eventType)]++
}

// TestRedirectDestinationCrawled is a regression test for the redirect bug.
// This test ensures that when a URL redirects to another URL, the redirect
// destination is properly crawled and its links are extracted.
//
// Bug: setupRedirectHandler was marking redirect destinations as visited before
// processing their responses, causing OnHTML/OnScraped callbacks to be skipped.
// This resulted in links from redirect destinations never being discovered.
//
// Example: agentberlin.ai links to handbook.agentberlin.ai/ which redirects to
// handbook.agentberlin.ai/intro. The /intro page was marked as visited but never
// crawled, so its links (like /topic_first_seo) were never discovered.
func TestRedirectDestinationCrawled(t *testing.T) {
	// Create a test HTTP server that simulates the redirect scenario
	mux := http.NewServeMux()

	// Main page links to /redirect-me
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Main Page</title></head>
<body>
    <h1>Main Page</h1>
    <a href="/redirect-me">Link to redirect page</a>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	// /redirect-me redirects to /final-destination
	mux.HandleFunc("/redirect-me", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final-destination", http.StatusMovedPermanently)
	})

	// /final-destination contains important links
	mux.HandleFunc("/final-destination", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Final Destination</title></head>
<body>
    <h1>Final Destination</h1>
    <a href="/important-page">Important page linked from redirect destination</a>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	// /important-page should be discovered via /final-destination
	mux.HandleFunc("/important-page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Important Page</title></head>
<body><h1>Important Page</h1></body>
</html>`
		fmt.Fprint(w, html)
	})

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Create a temporary database for this test
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/redirect_test.db"

	testStore, err := store.NewStoreForTesting(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	// Create app with test emitter
	emitter := &testEmitter{events: make(map[string]int)}
	coreApp := app.NewApp(testStore, emitter)
	ctx := context.Background()
	coreApp.Startup(ctx)

	// Start the crawl
	_, err = coreApp.StartCrawl(testServer.URL)
	if err != nil {
		t.Fatalf("Failed to start crawl: %v", err)
	}

	// Wait for crawl to complete
	maxWaitTime := 10 * time.Second
	checkInterval := 100 * time.Millisecond
	timeout := time.After(maxWaitTime)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	crawlCompleted := false
	for !crawlCompleted {
		select {
		case <-timeout:
			t.Fatalf("Crawl did not complete within %v", maxWaitTime)
		case <-ticker.C:
			activeCrawls := coreApp.GetActiveCrawls()
			if len(activeCrawls) == 0 {
				// Check if we have a completed crawl
				projects, err := coreApp.GetProjects()
				if err == nil && len(projects) > 0 {
					crawls, err := coreApp.GetCrawls(projects[0].ID)
					if err == nil && len(crawls) > 0 {
						crawlCompleted = true
					}
				}
			}
		}
	}

	// Get the project and crawl results
	projects, err := coreApp.GetProjects()
	if err != nil || len(projects) == 0 {
		t.Fatalf("Failed to get projects: %v", err)
	}

	crawls, err := coreApp.GetCrawls(projects[0].ID)
	if err != nil || len(crawls) == 0 {
		t.Fatalf("Failed to get crawls: %v", err)
	}

	crawlID := crawls[0].ID

	// Get all crawled URLs
	results, err := coreApp.GetCrawlWithResultsPaginated(crawlID, 1000, 0, "all")
	if err != nil {
		t.Fatalf("Failed to get crawl results: %v", err)
	}

	// Verify that all expected pages were crawled
	expectedURLs := map[string]bool{
		testServer.URL + "/":                  false, // Main page
		testServer.URL + "/final-destination": false, // Redirect destination (CRITICAL)
		testServer.URL + "/important-page":    false, // Linked from redirect destination (CRITICAL)
	}

	for _, result := range results.Results {
		if _, exists := expectedURLs[result.URL]; exists {
			expectedURLs[result.URL] = true
			t.Logf("✓ Found: %s (Title: %s)", result.URL, result.Title)
		}
	}

	// Check for missing URLs
	missingCritical := false
	for url, found := range expectedURLs {
		if !found {
			t.Errorf("❌ CRITICAL: URL not crawled: %s", url)
			if url == testServer.URL+"/final-destination" {
				t.Error("   ↳ This is the redirect destination - it MUST be crawled!")
				missingCritical = true
			}
			if url == testServer.URL+"/important-page" {
				t.Error("   ↳ This page is linked from the redirect destination")
				t.Error("   ↳ If this is missing, it means the redirect destination wasn't crawled properly")
				missingCritical = true
			}
		}
	}

	if missingCritical {
		t.Fatal("REGRESSION: Redirect destination bug has returned! Check setupRedirectHandler in crawler.go")
	}

	t.Log("✓ Redirect destination regression test passed!")
}

// TestRedirectChainWithStatusCodes is an integration test for the redirect race condition fix.
// This test verifies that intermediate redirects in a chain are properly reported to the application
// with their actual redirect status codes (301, 302, 307, 308) instead of being silently marked as visited.
//
// This is the critical fix for the race condition where redirect destinations were marked as visited
// but never reported to OnPageCrawled callbacks, causing them to not appear in crawl results.
func TestRedirectChainWithStatusCodes(t *testing.T) {
	// Create a test HTTP server with a redirect chain
	mux := http.NewServeMux()

	// Main page links to /redirect-1
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Main Page</title></head>
<body>
    <h1>Main Page</h1>
    <a href="/redirect-1">Link to redirect chain</a>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	// /redirect-1 redirects to /redirect-2 with 301 (permanent)
	mux.HandleFunc("/redirect-1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirect-2", http.StatusMovedPermanently)
	})

	// /redirect-2 redirects to /final with 302 (temporary)
	mux.HandleFunc("/redirect-2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})

	// /final is the actual destination
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html>
<head><title>Final Page</title></head>
<body>
    <h1>Final Destination</h1>
    <p>You made it through the redirect chain!</p>
</body>
</html>`
		fmt.Fprint(w, html)
	})

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Create a temporary database for this test
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/redirect_chain_test.db"

	testStore, err := store.NewStoreForTesting(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	// Create app with test emitter
	emitter := &testEmitter{events: make(map[string]int)}
	coreApp := app.NewApp(testStore, emitter)
	ctx := context.Background()
	coreApp.Startup(ctx)

	// Start the crawl
	_, err = coreApp.StartCrawl(testServer.URL)
	if err != nil {
		t.Fatalf("Failed to start crawl: %v", err)
	}

	// Wait for crawl to complete
	maxWaitTime := 10 * time.Second
	checkInterval := 100 * time.Millisecond
	timeout := time.After(maxWaitTime)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	crawlCompleted := false
	for !crawlCompleted {
		select {
		case <-timeout:
			t.Fatalf("Crawl did not complete within %v", maxWaitTime)
		case <-ticker.C:
			activeCrawls := coreApp.GetActiveCrawls()
			if len(activeCrawls) == 0 {
				// Check if we have a completed crawl
				projects, err := coreApp.GetProjects()
				if err == nil && len(projects) > 0 {
					crawls, err := coreApp.GetCrawls(projects[0].ID)
					if err == nil && len(crawls) > 0 {
						crawlCompleted = true
					}
				}
			}
		}
	}

	// Get the project and crawl results
	projects, err := coreApp.GetProjects()
	if err != nil || len(projects) == 0 {
		t.Fatalf("Failed to get projects: %v", err)
	}

	crawls, err := coreApp.GetCrawls(projects[0].ID)
	if err != nil || len(crawls) == 0 {
		t.Fatalf("Failed to get crawls: %v", err)
	}

	crawlID := crawls[0].ID

	// Get all crawled URLs
	results, err := coreApp.GetCrawlWithResultsPaginated(crawlID, 1000, 0, "all")
	if err != nil {
		t.Fatalf("Failed to get crawl results: %v", err)
	}

	// Create a map of URL to result for easier verification
	urlResults := make(map[string]*types.CrawlResult)
	for i, result := range results.Results {
		urlResults[result.URL] = &results.Results[i]
	}

	// Verify that ALL URLs in the redirect chain were crawled and have correct status codes
	expectedURLs := map[string]int{
		testServer.URL + "/":           200, // Main page (200 OK)
		testServer.URL + "/redirect-1": 301, // First redirect (301 Moved Permanently)
		testServer.URL + "/redirect-2": 302, // Second redirect (302 Found)
		testServer.URL + "/final":      200, // Final destination (200 OK)
	}

	for url, expectedStatus := range expectedURLs {
		result, found := urlResults[url]
		if !found {
			t.Errorf("❌ CRITICAL: URL not found in crawl results: %s", url)
			t.Errorf("   ↳ This means the redirect was not reported to the application!")
			continue
		}

		if result.Status != expectedStatus {
			t.Errorf("❌ URL %s has incorrect status code: expected %d, got %d", url, expectedStatus, result.Status)
		} else {
			t.Logf("✓ Found: %s (Status: %d, Title: %s)", url, result.Status, result.Title)
		}

		// Verify redirect URLs have empty titles
		if expectedStatus >= 300 && expectedStatus < 400 {
			if result.Title != "" {
				t.Errorf("Redirect URL %s should have empty title, got '%s'", url, result.Title)
			}
		}

		// Verify final pages have titles
		if expectedStatus == 200 {
			if result.Title == "" {
				t.Errorf("Final page %s should have a title", url)
			}
		}
	}

	// Verify we have exactly the expected number of results (4: main page + 2 redirects + final page)
	if len(urlResults) != len(expectedURLs) {
		t.Errorf("Expected %d URLs in results, got %d", len(expectedURLs), len(urlResults))
		t.Log("URLs found:")
		for url, result := range urlResults {
			t.Logf("  - %s (Status: %d)", url, result.Status)
		}
	}

	t.Log("✓ Redirect chain integration test passed! All intermediate redirects reported with correct status codes.")
}

