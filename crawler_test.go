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

package bluesnake

import (
	"net/http"
	"sync"
	"testing"
)

// TestDiscoveredURLs tests that discovered URLs are correctly identified and marked as crawlable
func TestDiscoveredURLs(t *testing.T) {
	// Create a mock transport
	mock := NewMockTransport()

	// Register the home page with 3 links
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home Page</title></head>
		<body>
			<a href="https://example.com/page1">Page 1</a>
			<a href="https://example.com/page2">Page 2</a>
			<a href="https://example.com/page3">Page 3</a>
		</body>
	</html>`)

	// Register the 3 linked pages with 2-second delays
	for _, page := range []string{"/page1", "/page2", "/page3"} {
		mock.RegisterResponse("https://example.com"+page, &MockResponse{
			StatusCode: 200,
			Body:       `<html><head><title>` + page + `</title></head><body>Content</body></html>`,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		})
	}

	// Track callback invocations
	var mu sync.Mutex
	var homePageResult *PageResult

	// Create crawler with mock transport
	crawler := NewCrawler(
		AllowedDomains("example.com"),
		WithMockTransport(mock),
		Async(),
	)

	// Set callback to capture the home page result
	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()

		// Capture the home page result
		if result.URL == "https://example.com/" {
			resultCopy := *result
			resultCopy.DiscoveredURLs = make([]CrawledURL, len(result.DiscoveredURLs))
			copy(resultCopy.DiscoveredURLs, result.DiscoveredURLs)
			homePageResult = &resultCopy
		}
	})

	// Start crawling
	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	// Wait for completion
	crawler.Wait()

	// Verify the home page result
	mu.Lock()
	defer mu.Unlock()

	if homePageResult == nil {
		t.Fatal("Home page was not crawled")
	}

	// Check that we have exactly 3 discovered URLs
	if len(homePageResult.DiscoveredURLs) != 3 {
		t.Fatalf("Expected 3 discovered URLs, got %d", len(homePageResult.DiscoveredURLs))
	}

	// Verify all discovered URLs are crawlable
	for i, url := range homePageResult.DiscoveredURLs {
		if !url.IsCrawlable {
			t.Errorf("Expected URL %d (%s) to be crawlable, but IsCrawlable=false", i, url.URL)
		}
	}

	// Verify the expected URLs
	expectedURLs := map[string]bool{
		"https://example.com/page1": false,
		"https://example.com/page2": false,
		"https://example.com/page3": false,
	}

	for _, url := range homePageResult.DiscoveredURLs {
		if _, exists := expectedURLs[url.URL]; exists {
			expectedURLs[url.URL] = true
		} else {
			t.Errorf("Unexpected URL discovered: %s", url.URL)
		}
	}

	// Verify all expected URLs were found
	for url, found := range expectedURLs {
		if !found {
			t.Errorf("Expected to find URL %s in discovered URLs", url)
		}
	}

	t.Logf("Successfully discovered %d URLs, all marked as crawlable", len(homePageResult.DiscoveredURLs))
}
