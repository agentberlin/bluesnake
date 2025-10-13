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
	"strings"
	"sync"
	"testing"
)

// TestDiscoveredURLs tests that discovered URLs are correctly identified as internal outbound links
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

	// Register the 3 linked pages
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
			homePageResult = result
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

	if homePageResult.Links == nil {
		t.Fatal("Links should not be nil")
	}

	// Check that we have exactly 3 internal anchor links
	internalLinks := homePageResult.Links.Internal
	anchorLinks := []Link{}
	for _, link := range internalLinks {
		if link.Type == "anchor" {
			anchorLinks = append(anchorLinks, link)
		}
	}

	if len(anchorLinks) != 3 {
		t.Fatalf("Expected 3 anchor links, got %d", len(anchorLinks))
	}

	// Verify all discovered links are internal
	for i, link := range anchorLinks {
		if !link.IsInternal {
			t.Errorf("Expected link %d (%s) to be internal, but IsInternal=false", i, link.URL)
		}
	}

	// Verify the expected URLs
	expectedURLs := map[string]bool{
		"https://example.com/page1": false,
		"https://example.com/page2": false,
		"https://example.com/page3": false,
	}

	for _, link := range anchorLinks {
		if _, exists := expectedURLs[link.URL]; exists {
			expectedURLs[link.URL] = true
		} else {
			t.Errorf("Unexpected URL discovered: %s", link.URL)
		}
	}

	// Verify all expected URLs were found
	for url, found := range expectedURLs {
		if !found {
			t.Errorf("Expected to find URL %s in discovered links", url)
		}
	}

	t.Logf("Successfully discovered %d internal links", len(anchorLinks))
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
				Headers: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
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

			// Capture the user agent from the response body using GetHTML()
			crawler.SetOnPageCrawled(func(result *PageResult) {
				mu.Lock()
				defer mu.Unlock()
				// Use GetHTML() to get the response body (which contains the User-Agent)
				receivedUserAgent = result.GetHTML()
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

// TestPageResult_GetHTML tests the GetHTML() method
func TestPageResult_GetHTML(t *testing.T) {
	mock := NewMockTransport()

	expectedHTML := `<html><head><title>Test Page</title><meta name="description" content="Test description"></head><body><main>Main content here</main></body></html>`
	mock.RegisterHTML("https://example.com/", expectedHTML)

	var mu sync.Mutex
	var pageResult *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		pageResult = result
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if pageResult == nil {
		t.Fatal("Page was not crawled")
	}

	html := pageResult.GetHTML()
	if html != expectedHTML {
		t.Errorf("GetHTML() returned incorrect HTML.\nExpected: %q\nGot: %q", expectedHTML, html)
	}

	t.Logf("GetHTML() correctly returned %d bytes of HTML", len(html))
}

// TestPageResult_GetTextFull tests the GetTextFull() method
func TestPageResult_GetTextFull(t *testing.T) {
	mock := NewMockTransport()

	html := `<html>
		<head><title>Test Page</title></head>
		<body>
			<nav>Navigation Menu</nav>
			<header>Header Content</header>
			<main>Main Content</main>
			<aside>Sidebar Content</aside>
			<footer>Footer Content</footer>
		</body>
	</html>`

	mock.RegisterHTML("https://example.com/", html)

	var mu sync.Mutex
	var pageResult *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		pageResult = result
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if pageResult == nil {
		t.Fatal("Page was not crawled")
	}

	text := pageResult.GetTextFull()

	// Should include all text from all sections
	expectedTexts := []string{"Test Page", "Navigation Menu", "Header Content", "Main Content", "Sidebar Content", "Footer Content"}
	for _, expected := range expectedTexts {
		if !strings.Contains(text, expected) {
			t.Errorf("GetTextFull() missing expected text: %q\nFull text: %q", expected, text)
		}
	}

	t.Logf("GetTextFull() correctly extracted %d characters including all page sections", len(text))
}

// TestPageResult_GetTextContent tests the GetTextContent() method
func TestPageResult_GetTextContent(t *testing.T) {
	tests := []struct {
		name            string
		html            string
		expectedInclude []string
		expectedExclude []string
	}{
		{
			name: "article tag",
			html: `<html><body>
				<nav>Navigation</nav>
				<article>Article Content</article>
				<footer>Footer</footer>
			</body></html>`,
			expectedInclude: []string{"Article Content"},
			expectedExclude: []string{"Navigation", "Footer"},
		},
		{
			name: "main tag",
			html: `<html><body>
				<header>Header</header>
				<main>Main Content</main>
				<aside>Sidebar</aside>
			</body></html>`,
			expectedInclude: []string{"Main Content"},
			expectedExclude: []string{"Header", "Sidebar"},
		},
		{
			name: "role=main attribute",
			html: `<html><body>
				<div role="main">Role Main Content</div>
				<nav>Navigation</nav>
			</body></html>`,
			expectedInclude: []string{"Role Main Content"},
			expectedExclude: []string{"Navigation"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockTransport()
			mock.RegisterHTML("https://example.com/", tt.html)

			var mu sync.Mutex
			var pageResult *PageResult

			crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
			crawler.Collector.WithTransport(mock)

			crawler.SetOnPageCrawled(func(result *PageResult) {
				mu.Lock()
				defer mu.Unlock()
				pageResult = result
			})

			err := crawler.Start("https://example.com/")
			if err != nil {
				t.Fatalf("Failed to start crawler: %v", err)
			}

			crawler.Wait()

			mu.Lock()
			defer mu.Unlock()

			if pageResult == nil {
				t.Fatal("Page was not crawled")
			}

			text := pageResult.GetTextContent()

			// Check that expected content is included
			for _, expected := range tt.expectedInclude {
				if !strings.Contains(text, expected) {
					t.Errorf("GetTextContent() missing expected text: %q\nFull text: %q", expected, text)
				}
			}

			// Check that unwanted content is excluded
			for _, excluded := range tt.expectedExclude {
				if strings.Contains(text, excluded) {
					t.Errorf("GetTextContent() should not include: %q\nFull text: %q", excluded, text)
				}
			}

			t.Logf("GetTextContent() correctly extracted %d characters from main content area", len(text))
		})
	}
}

// TestResourceResult_NonHTMLRouting tests that non-HTML resources are routed to OnResourceVisit callback
func TestResourceResult_NonHTMLRouting(t *testing.T) {
	mock := NewMockTransport()

	// Register a JSON response
	mock.RegisterResponse("https://example.com/data.json", &MockResponse{
		StatusCode: 200,
		Body:       `{"key": "value"}`,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
	})

	var mu sync.Mutex
	var resourceResult *ResourceResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
	crawler.Collector.WithTransport(mock)

	// Non-HTML content should go to OnResourceVisit, not OnPageCrawled
	crawler.SetOnResourceVisit(func(result *ResourceResult) {
		mu.Lock()
		defer mu.Unlock()
		resourceResult = result
	})

	err := crawler.Start("https://example.com/data.json")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if resourceResult == nil {
		t.Fatal("Resource was not visited")
	}

	// Verify ResourceResult fields
	if resourceResult.URL != "https://example.com/data.json" {
		t.Errorf("URL = %q, want %q", resourceResult.URL, "https://example.com/data.json")
	}

	if resourceResult.Status != 200 {
		t.Errorf("Status = %d, want 200", resourceResult.Status)
	}

	if resourceResult.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want %q", resourceResult.ContentType, "application/json")
	}

	if resourceResult.Error != "" {
		t.Errorf("Error should be empty, got: %q", resourceResult.Error)
	}

	t.Log("Non-HTML resources correctly routed to OnResourceVisit")
}

// TestResourceValidation_ConfiguredTypes tests that resource validation respects configuration
func TestResourceValidation_ConfiguredTypes(t *testing.T) {
	tests := []struct {
		name                  string
		resourceTypes         []string
		checkExternal         bool
		expectedInternalImage bool
		expectedExternalImage bool
		expectedScript        bool
	}{
		{
			name:                  "Validate all resource types",
			resourceTypes:         nil, // nil = all types
			checkExternal:         true,
			expectedInternalImage: true,
			expectedExternalImage: true,
			expectedScript:        true,
		},
		{
			name:                  "Validate only images",
			resourceTypes:         []string{"image"},
			checkExternal:         true,
			expectedInternalImage: true,
			expectedExternalImage: true,
			expectedScript:        false, // scripts not in list
		},
		{
			name:                  "Skip external resources",
			resourceTypes:         []string{"image", "script"},
			checkExternal:         false,
			expectedInternalImage: true,
			expectedExternalImage: false, // external check disabled
			expectedScript:        true,
		},
		{
			name:                  "Disabled validation",
			resourceTypes:         []string{"image"},
			checkExternal:         false,
			expectedInternalImage: false, // Will be set by disabling validation below
			expectedExternalImage: false,
			expectedScript:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockTransport()

			// Register HTML page with various resource links
			mock.RegisterHTML("https://example.com/", `<html>
				<head><title>Test Page</title></head>
				<body>
					<img src="https://example.com/internal.png" alt="Internal Image">
					<img src="https://external.com/external.png" alt="External Image">
					<script src="https://example.com/script.js"></script>
				</body>
			</html>`)

			// Register resources
			mock.RegisterResponse("https://example.com/internal.png", &MockResponse{
				StatusCode: 200,
				Body:       "fake-image-data",
				Headers:    http.Header{"Content-Type": []string{"image/png"}},
			})
			mock.RegisterResponse("https://external.com/external.png", &MockResponse{
				StatusCode: 200,
				Body:       "fake-image-data",
				Headers:    http.Header{"Content-Type": []string{"image/png"}},
			})
			mock.RegisterResponse("https://example.com/script.js", &MockResponse{
				StatusCode: 200,
				Body:       "console.log('test');",
				Headers:    http.Header{"Content-Type": []string{"application/javascript"}},
			})

			var mu sync.Mutex
			visitedResources := make(map[string]bool)

			// Configure resource validation
			resourceValidation := &ResourceValidationConfig{
				Enabled:       tt.name != "Disabled validation", // Disable for last test
				ResourceTypes: tt.resourceTypes,
				CheckExternal: tt.checkExternal,
			}

			crawler := NewCrawler(&CollectorConfig{
				AllowedDomains:     []string{"example.com", "external.com"}, // Need to allow external domain
				Async:              true,
				ResourceValidation: resourceValidation,
			})
			crawler.Collector.WithTransport(mock)

			// Track resource visits
			crawler.SetOnResourceVisit(func(result *ResourceResult) {
				mu.Lock()
				defer mu.Unlock()
				visitedResources[result.URL] = true
			})

			err := crawler.Start("https://example.com/")
			if err != nil {
				t.Fatalf("Failed to start crawler: %v", err)
			}

			crawler.Wait()

			mu.Lock()
			defer mu.Unlock()

			// Check internal image
			if tt.expectedInternalImage {
				if !visitedResources["https://example.com/internal.png"] {
					t.Error("Internal image should be validated")
				}
			} else {
				if visitedResources["https://example.com/internal.png"] {
					t.Error("Internal image should NOT be validated")
				}
			}

			// Check external image
			if tt.expectedExternalImage {
				if !visitedResources["https://external.com/external.png"] {
					t.Error("External image should be validated")
				}
			} else {
				if visitedResources["https://external.com/external.png"] {
					t.Error("External image should NOT be validated")
				}
			}

			// Check script
			if tt.expectedScript {
				if !visitedResources["https://example.com/script.js"] {
					t.Error("Script should be validated")
				}
			} else {
				if visitedResources["https://example.com/script.js"] {
					t.Error("Script should NOT be validated")
				}
			}

			t.Logf("Resource validation config correctly applied: %d resources visited", len(visitedResources))
		})
	}
}

// TestPageResult_MetaDescription tests that meta description is extracted
func TestPageResult_MetaDescription(t *testing.T) {
	mock := NewMockTransport()

	html := `<html>
		<head>
			<title>Test Page</title>
			<meta name="description" content="This is a test description">
		</head>
		<body>Content</body>
	</html>`

	mock.RegisterHTML("https://example.com/", html)

	var mu sync.Mutex
	var pageResult *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		pageResult = result
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if pageResult == nil {
		t.Fatal("Page was not crawled")
	}

	expectedDesc := "This is a test description"
	if pageResult.MetaDescription != expectedDesc {
		t.Errorf("MetaDescription = %q, want %q", pageResult.MetaDescription, expectedDesc)
	}

	t.Logf("Meta description correctly extracted: %q", pageResult.MetaDescription)
}

// TestResourceHintExtraction tests extraction of <link rel="preload">, <link rel="modulepreload">, and <link rel="prefetch">
func TestResourceHintExtraction(t *testing.T) {
	mock := NewMockTransport()

	// Create a mock HTML page with resource hints
	mockHTML := `<!DOCTYPE html>
<html>
<head>
	<title>Test Page</title>
	<!-- Preload hints -->
	<link rel="preload" as="script" href="/js/app.js">
	<link rel="preload" as="style" href="/css/main.css">
	<link rel="preload" as="font" href="/fonts/Inter.woff2">
	<link rel="preload" as="image" href="/images/hero.jpg">

	<!-- Module preload -->
	<link rel="modulepreload" href="/js/module.js">

	<!-- Prefetch -->
	<link rel="prefetch" as="script" href="/js/future.js">
	<link rel="prefetch" as="document" href="/next-page.html">

	<!-- Regular script (for comparison) -->
	<script src="/js/regular.js"></script>
	<link rel="stylesheet" href="/css/regular.css">
</head>
<body>
	<h1>Test</h1>
</body>
</html>`

	mock.RegisterHTML("https://example.com/", mockHTML)

	// Track discovered links
	var mu sync.Mutex
	var discoveredLinks []Link

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.Links != nil {
			discoveredLinks = append(discoveredLinks, result.Links.Internal...)
			discoveredLinks = append(discoveredLinks, result.Links.External...)
		}
	})

	// Start crawl
	if err := crawler.Start("https://example.com/"); err != nil {
		t.Fatalf("Failed to start crawl: %v", err)
	}
	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify we discovered the expected links
	expectedLinks := map[string]string{
		"https://example.com/js/app.js":          "script",
		"https://example.com/css/main.css":       "stylesheet",
		"https://example.com/fonts/Inter.woff2":  "font",
		"https://example.com/images/hero.jpg":    "image",
		"https://example.com/js/module.js":       "script",
		"https://example.com/js/future.js":       "script",
		"https://example.com/next-page.html":     "anchor",
		"https://example.com/js/regular.js":      "script",
		"https://example.com/css/regular.css":    "stylesheet",
	}

	foundLinks := make(map[string]string)
	for _, link := range discoveredLinks {
		foundLinks[link.URL] = link.Type
	}

	// Check each expected link
	missingCount := 0
	wrongTypeCount := 0
	for expectedURL, expectedType := range expectedLinks {
		foundType, found := foundLinks[expectedURL]
		if !found {
			t.Errorf("Expected to find link %s but it was not discovered", expectedURL)
			missingCount++
		} else if foundType != expectedType {
			t.Errorf("Link %s has type %s, expected %s", expectedURL, foundType, expectedType)
			wrongTypeCount++
		} else {
			t.Logf("✓ Found %s with correct type: %s", expectedURL, expectedType)
		}
	}

	if missingCount == 0 && wrongTypeCount == 0 {
		t.Logf("Successfully extracted all %d resource hints with correct types", len(expectedLinks))
	}
}
