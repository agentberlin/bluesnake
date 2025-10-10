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
	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}, Async: true})
	crawler.Collector.WithTransport(mock)

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

// TestDiscoveryMechanism_SpiderOnly tests that spider-only mode follows links but doesn't use sitemap
func TestDiscoveryMechanism_SpiderOnly(t *testing.T) {
	mock := NewMockTransport()

	// Register home page with 2 links
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home</title></head>
		<body>
			<a href="/page1">Page 1</a>
			<a href="/page2">Page 2</a>
		</body>
	</html>`)

	// Register linked pages
	mock.RegisterHTML("https://example.com/page1", `<html><head><title>Page 1</title></head><body>Content 1</body></html>`)
	mock.RegisterHTML("https://example.com/page2", `<html><head><title>Page 2</title></head><body>Content 2</body></html>`)

	// Register sitemap (should NOT be used in spider-only mode)
	mock.RegisterResponse("https://example.com/sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url><loc>https://example.com/sitemap-only-page</loc></url>
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with spider-only mode
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySpider},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should have crawled 3 pages (home + 2 linked pages)
	if len(crawledPages) != 3 {
		t.Errorf("Expected 3 pages crawled, got %d: %v", len(crawledPages), crawledPages)
	}

	// Verify sitemap-only page was NOT crawled
	for _, url := range crawledPages {
		if url == "https://example.com/sitemap-only-page" {
			t.Error("Sitemap page should not be crawled in spider-only mode")
		}
	}

	t.Logf("Spider-only mode: crawled %d pages via link following", len(crawledPages))
}

// TestDiscoveryMechanism_SitemapOnly tests that sitemap-only mode uses sitemap but doesn't follow links
func TestDiscoveryMechanism_SitemapOnly(t *testing.T) {
	mock := NewMockTransport()

	// Register home page with links (should NOT follow in sitemap-only mode)
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home</title></head>
		<body>
			<a href="/page1">Page 1</a>
			<a href="/page2">Page 2</a>
		</body>
	</html>`)

	// Register sitemap with additional pages
	mock.RegisterResponse("https://example.com/sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url><loc>https://example.com/sitemap-page1</loc></url>
	<url><loc>https://example.com/sitemap-page2</loc></url>
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	// Register sitemap pages
	mock.RegisterHTML("https://example.com/sitemap-page1", `<html><head><title>Sitemap Page 1</title></head><body>Content</body></html>`)
	mock.RegisterHTML("https://example.com/sitemap-page2", `<html><head><title>Sitemap Page 2</title></head><body>Content</body></html>`)

	// Register linked pages (should NOT be crawled)
	mock.RegisterHTML("https://example.com/page1", `<html><head><title>Linked Page 1</title></head><body>Content</body></html>`)
	mock.RegisterHTML("https://example.com/page2", `<html><head><title>Linked Page 2</title></head><body>Content</body></html>`)

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with sitemap-only mode
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySitemap},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should have crawled 3 pages (home + 2 sitemap pages)
	// Links on home page should NOT be followed
	if len(crawledPages) != 3 {
		t.Errorf("Expected 3 pages crawled, got %d: %v", len(crawledPages), crawledPages)
	}

	// Verify sitemap pages were crawled
	hasSitemapPage1 := false
	hasSitemapPage2 := false
	hasLinkedPage := false

	for _, url := range crawledPages {
		if url == "https://example.com/sitemap-page1" {
			hasSitemapPage1 = true
		}
		if url == "https://example.com/sitemap-page2" {
			hasSitemapPage2 = true
		}
		if url == "https://example.com/page1" || url == "https://example.com/page2" {
			hasLinkedPage = true
		}
	}

	if !hasSitemapPage1 || !hasSitemapPage2 {
		t.Error("Sitemap pages should be crawled in sitemap-only mode")
	}

	if hasLinkedPage {
		t.Error("Linked pages should NOT be crawled in sitemap-only mode")
	}

	t.Logf("Sitemap-only mode: crawled %d pages from sitemap without following links", len(crawledPages))
}

// TestDiscoveryMechanism_Both tests that both modes work together (additive)
func TestDiscoveryMechanism_Both(t *testing.T) {
	mock := NewMockTransport()

	// Register home page with links
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home</title></head>
		<body>
			<a href="/linked-page">Linked Page</a>
		</body>
	</html>`)

	// Register sitemap
	mock.RegisterResponse("https://example.com/sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url><loc>https://example.com/sitemap-page</loc></url>
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	// Register both types of pages
	mock.RegisterHTML("https://example.com/linked-page", `<html><head><title>Linked Page</title></head><body>Content</body></html>`)
	mock.RegisterHTML("https://example.com/sitemap-page", `<html><head><title>Sitemap Page</title></head><body>Content</body></html>`)

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with both mechanisms
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySpider, DiscoverySitemap},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should have crawled 3 pages (home + linked page + sitemap page)
	if len(crawledPages) != 3 {
		t.Errorf("Expected 3 pages crawled, got %d: %v", len(crawledPages), crawledPages)
	}

	// Verify both types of pages were crawled
	hasLinkedPage := false
	hasSitemapPage := false

	for _, url := range crawledPages {
		if url == "https://example.com/linked-page" {
			hasLinkedPage = true
		}
		if url == "https://example.com/sitemap-page" {
			hasSitemapPage = true
		}
	}

	if !hasLinkedPage {
		t.Error("Linked page should be crawled when both mechanisms are enabled")
	}

	if !hasSitemapPage {
		t.Error("Sitemap page should be crawled when both mechanisms are enabled")
	}

	t.Logf("Both mechanisms: crawled %d pages (sitemap + link following)", len(crawledPages))
}

// TestDiscoveryMechanism_CustomSitemapURL tests using custom sitemap URLs
func TestDiscoveryMechanism_CustomSitemapURL(t *testing.T) {
	mock := NewMockTransport()

	// Register home page
	mock.RegisterHTML("https://example.com/", `<html><head><title>Home</title></head><body>Content</body></html>`)

	// Register default sitemap location (should NOT be used)
	mock.RegisterResponse("https://example.com/sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url><loc>https://example.com/default-sitemap-page</loc></url>
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	// Register custom sitemap location
	mock.RegisterResponse("https://example.com/custom-sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url><loc>https://example.com/custom-sitemap-page</loc></url>
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	// Register pages
	mock.RegisterHTML("https://example.com/default-sitemap-page", `<html><head><title>Default</title></head><body>Content</body></html>`)
	mock.RegisterHTML("https://example.com/custom-sitemap-page", `<html><head><title>Custom</title></head><body>Content</body></html>`)

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with custom sitemap URL
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySitemap},
		SitemapURLs:         []string{"https://example.com/custom-sitemap.xml"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should use custom sitemap, not default
	hasCustomPage := false
	hasDefaultPage := false

	for _, url := range crawledPages {
		if url == "https://example.com/custom-sitemap-page" {
			hasCustomPage = true
		}
		if url == "https://example.com/default-sitemap-page" {
			hasDefaultPage = true
		}
	}

	if !hasCustomPage {
		t.Error("Custom sitemap page should be crawled when custom sitemap URL is provided")
	}

	if hasDefaultPage {
		t.Error("Default sitemap should NOT be used when custom sitemap URL is provided")
	}

	t.Logf("Custom sitemap: crawled %d pages from custom sitemap", len(crawledPages))
}

// TestDiscoveryMechanism_EmptySitemap tests handling of empty sitemap
func TestDiscoveryMechanism_EmptySitemap(t *testing.T) {
	mock := NewMockTransport()

	// Register home page
	mock.RegisterHTML("https://example.com/", `<html><head><title>Home</title></head><body>Content</body></html>`)

	// Register empty sitemap
	mock.RegisterResponse("https://example.com/sitemap.xml", &MockResponse{
		StatusCode: 200,
		Body: `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
</urlset>`,
		Headers: http.Header{"Content-Type": []string{"application/xml"}},
	})

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with sitemap mode
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySitemap},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should only crawl home page (empty sitemap should not cause errors)
	if len(crawledPages) != 1 {
		t.Errorf("Expected 1 page crawled (home only), got %d", len(crawledPages))
	}

	if crawledPages[0] != "https://example.com/" {
		t.Errorf("Expected home page to be crawled, got %s", crawledPages[0])
	}

	t.Logf("Empty sitemap handled gracefully: %d page crawled", len(crawledPages))
}

// TestDiscoveryMechanism_NoSitemap tests handling when sitemap doesn't exist
func TestDiscoveryMechanism_NoSitemap(t *testing.T) {
	mock := NewMockTransport()

	// Register home page
	mock.RegisterHTML("https://example.com/", `<html><head><title>Home</title></head><body>Content</body></html>`)

	// Don't register sitemap (404)

	var mu sync.Mutex
	crawledPages := []string{}

	// Create crawler with sitemap mode
	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySitemap},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages = append(crawledPages, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should only crawl home page (missing sitemap should not cause errors)
	if len(crawledPages) != 1 {
		t.Errorf("Expected 1 page crawled (home only), got %d", len(crawledPages))
	}

	t.Logf("Missing sitemap handled gracefully: %d page crawled", len(crawledPages))
}

// TestCrawlerUserAgent tests that the UserAgent configuration is correctly applied
func TestCrawlerUserAgent(t *testing.T) {
	const customUserAgent = "bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)"
	const defaultUserAgent = "bluesnake - https://github.com/agentberlin/bluesnake"

	tests := []struct {
		name              string
		configuredUA      string
		expectedUA        string
		shouldSetUA       bool
	}{
		{
			name:         "Default UserAgent",
			configuredUA: "",
			expectedUA:   defaultUserAgent,
			shouldSetUA:  false,
		},
		{
			name:         "Custom UserAgent",
			configuredUA: customUserAgent,
			expectedUA:   customUserAgent,
			shouldSetUA:  true,
		},
		{
			name:         "Another Custom UserAgent",
			configuredUA: "MyCustomCrawler/2.0",
			expectedUA:   "MyCustomCrawler/2.0",
			shouldSetUA:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockTransport()

			// Register a page that echoes back the User-Agent header
			mock.RegisterResponse("https://example.com/", &MockResponse{
				StatusCode: 200,
				BodyFunc: func(req *http.Request) string {
					return req.Header.Get("User-Agent")
				},
			})

			var mu sync.Mutex
			var receivedUserAgent string

			// Create crawler with optional custom user agent
			var crawler *Crawler
			if tt.shouldSetUA {
				crawler = NewCrawler(&CollectorConfig{
					AllowedDomains: []string{"example.com"},
					UserAgent:      tt.configuredUA,
				})
			} else {
				// Pass nil to use default config with default UserAgent
				crawler = NewCrawler(nil)
				// Set allowed domains manually since we're using nil config
				crawler.Collector.AllowedDomains = []string{"example.com"}
			}

			crawler.Collector.WithTransport(mock)

			// Capture the user agent from the response
			crawler.SetOnPageCrawled(func(result *PageResult) {
				mu.Lock()
				defer mu.Unlock()
				// The body contains the User-Agent header that was sent
				receivedUserAgent = result.Title // Title will be empty, but we can check the raw response
			})

			// Use OnResponse to get the actual response body
			crawler.Collector.OnResponse(func(r *Response) {
				mu.Lock()
				defer mu.Unlock()
				receivedUserAgent = string(r.Body)
			})

			err := crawler.Start("https://example.com/")
			if err != nil {
				t.Fatalf("Failed to start crawler: %v", err)
			}

			crawler.Wait()

			mu.Lock()
			defer mu.Unlock()

			if receivedUserAgent != tt.expectedUA {
				t.Errorf("Expected User-Agent %q, got %q", tt.expectedUA, receivedUserAgent)
			}

			t.Logf("✓ User-Agent correctly set to: %q", receivedUserAgent)
		})
	}
}
