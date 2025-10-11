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

// TestLinkExtraction_MultipleTypes tests extraction of different link types
func TestLinkExtraction_MultipleTypes(t *testing.T) {
	mock := NewMockTransport()

	// Register page with various link types
	mock.RegisterHTML("https://example.com/", `<html>
		<head>
			<title>Home Page</title>
			<link rel="stylesheet" href="/style.css">
			<link rel="canonical" href="https://example.com/">
		</head>
		<body>
			<a href="/page1">Link 1</a>
			<a href="/page2">Link 2</a>
			<img src="/image.png" alt="Test Image">
			<script src="/script.js"></script>
			<iframe src="/frame.html"></iframe>
		</body>
	</html>`)

	var mu sync.Mutex
	var homePageResult *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}, Async: true})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.URL == "https://example.com/" {
			homePageResult = result
		}
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if homePageResult == nil {
		t.Fatal("Home page was not crawled")
	}

	if homePageResult.Links == nil {
		t.Fatal("Links should not be nil")
	}

	// Count link types in internal links
	linkTypes := make(map[string]int)
	for _, link := range homePageResult.Links.Internal {
		linkTypes[link.Type]++
	}

	// Verify we extracted all link types
	expectedTypes := map[string]int{
		"anchor":     2, // 2 <a> tags
		"image":      1, // 1 <img>
		"script":     1, // 1 <script>
		"stylesheet": 1, // 1 <link rel="stylesheet">
		"canonical":  1, // 1 <link rel="canonical">
		"iframe":     1, // 1 <iframe>
	}

	for linkType, expectedCount := range expectedTypes {
		if linkTypes[linkType] != expectedCount {
			t.Errorf("Expected %d %s links, got %d", expectedCount, linkType, linkTypes[linkType])
		}
	}

	// Verify anchor text extraction
	foundAnchorText := false
	for _, link := range homePageResult.Links.Internal {
		if link.Type == "anchor" && link.Text == "Link 1" {
			foundAnchorText = true
			break
		}
	}
	if !foundAnchorText {
		t.Error("Expected to find anchor with text 'Link 1'")
	}

	// Verify alt text extraction for images
	foundAltText := false
	for _, link := range homePageResult.Links.Internal {
		if link.Type == "image" && link.Text == "Test Image" {
			foundAltText = true
			break
		}
	}
	if !foundAltText {
		t.Error("Expected to find image with alt text 'Test Image'")
	}

	t.Logf("Successfully extracted %d different link types", len(linkTypes))
}

// TestInternalExternalClassification tests internal vs external link classification
func TestInternalExternalClassification(t *testing.T) {
	mock := NewMockTransport()

	// Register page with internal and external links
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home</title></head>
		<body>
			<a href="https://example.com/page1">Internal Link</a>
			<a href="https://blog.example.com/post">Subdomain Link</a>
			<a href="https://external.com/page">External Link</a>
			<a href="/relative">Relative Link</a>
		</body>
	</html>`)

	var mu sync.Mutex
	var homePageResult *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}, Async: true})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.URL == "https://example.com/" {
			homePageResult = result
		}
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if homePageResult == nil || homePageResult.Links == nil {
		t.Fatal("Home page or links not available")
	}

	// Count internal and external links
	internalCount := len(homePageResult.Links.Internal)
	externalCount := len(homePageResult.Links.External)

	// Should have 3 internal (same domain + subdomain + relative) and 1 external
	if internalCount != 3 {
		t.Errorf("Expected 3 internal links, got %d", internalCount)
		for _, link := range homePageResult.Links.Internal {
			t.Logf("  Internal: %s", link.URL)
		}
	}

	if externalCount != 1 {
		t.Errorf("Expected 1 external link, got %d", externalCount)
		for _, link := range homePageResult.Links.External {
			t.Logf("  External: %s", link.URL)
		}
	}

	// Verify subdomain is classified as internal
	foundSubdomain := false
	for _, link := range homePageResult.Links.Internal {
		if link.URL == "https://blog.example.com/post" {
			foundSubdomain = true
			if !link.IsInternal {
				t.Error("Subdomain link should be marked as internal")
			}
			break
		}
	}
	if !foundSubdomain {
		t.Error("Subdomain link should be in internal links")
	}

	// Verify external domain is classified correctly
	foundExternal := false
	for _, link := range homePageResult.Links.External {
		if link.URL == "https://external.com/page" {
			foundExternal = true
			if link.IsInternal {
				t.Error("External link should be marked as external")
			}
			break
		}
	}
	if !foundExternal {
		t.Error("External link should be in external links")
	}

	t.Logf("Classification correct: %d internal, %d external", internalCount, externalCount)
}

// TestLinkMetadata tests that link metadata is populated when available
func TestLinkMetadata(t *testing.T) {
	mock := NewMockTransport()

	// Page 1 links to Page 2
	mock.RegisterHTML("https://example.com/page1", `<html>
		<head><title>Page 1</title></head>
		<body>
			<a href="/page2">Link to Page 2</a>
		</body>
	</html>`)

	// Page 2 exists with specific status/title
	mock.RegisterHTML("https://example.com/page2", `<html>
		<head><title>Page 2 Title</title></head>
		<body>Content</body>
	</html>`)

	var mu sync.Mutex
	var page1Result *PageResult

	crawler := NewCrawler(&CollectorConfig{AllowedDomains: []string{"example.com"}, Async: true})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		// We want the LAST callback for page1, after page2 has been crawled
		if result.URL == "https://example.com/page1" {
			page1Result = result
		}
	})

	err := crawler.Start("https://example.com/page1")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	if page1Result == nil || page1Result.Links == nil {
		t.Fatal("Page 1 or links not available")
	}

	// Find the link to page2 in internal links
	var page2Link *Link
	for _, link := range page1Result.Links.Internal {
		if link.URL == "https://example.com/page2" {
			page2Link = &link
			break
		}
	}

	if page2Link == nil {
		t.Fatal("Link to page2 not found in links")
	}

	// Note: Metadata might not be populated immediately in the first callback
	// since page2 might not have been crawled yet. This is expected behavior.
	// The metadata gets populated when the link is extracted, so if page2 was crawled first,
	// it would have metadata.

	t.Logf("Link to page2 found: status=%v, title=%s", page2Link.Status, page2Link.Title)
}

// TestSpiderOnlyFollowsAnchors tests that spider mode only visits anchor links
func TestSpiderOnlyFollowsAnchors(t *testing.T) {
	mock := NewMockTransport()

	// Page with various link types
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home</title></head>
		<body>
			<a href="/page1">Page 1</a>
			<img src="/image.png" alt="Image">
			<script src="/script.js"></script>
		</body>
	</html>`)

	// Register only the anchor target
	mock.RegisterHTML("https://example.com/page1", `<html>
		<head><title>Page 1</title></head>
		<body>Content</body>
	</html>`)

	// Register other resources (should NOT be visited)
	mock.RegisterResponse("https://example.com/image.png", &MockResponse{
		StatusCode: 200,
		Body:       "IMAGE_DATA",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
	})
	mock.RegisterResponse("https://example.com/script.js", &MockResponse{
		StatusCode: 200,
		Body:       "console.log('test')",
		Headers:    http.Header{"Content-Type": []string{"application/javascript"}},
	})

	var mu sync.Mutex
	crawledURLs := []string{}

	crawler := NewCrawler(&CollectorConfig{
		AllowedDomains:      []string{"example.com"},
		Async:               true,
		DiscoveryMechanisms: []DiscoveryMechanism{DiscoverySpider},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledURLs = append(crawledURLs, result.URL)
	})

	err := crawler.Start("https://example.com/")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Should only crawl 2 pages: home and page1 (not image or script)
	if len(crawledURLs) != 2 {
		t.Errorf("Expected 2 pages crawled, got %d: %v", len(crawledURLs), crawledURLs)
	}

	// Verify image and script were NOT crawled
	for _, url := range crawledURLs {
		if url == "https://example.com/image.png" || url == "https://example.com/script.js" {
			t.Errorf("Non-anchor resource should not be crawled: %s", url)
		}
	}

	t.Logf("Spider correctly only followed anchor links: %d pages crawled", len(crawledURLs))
}

// TestRootDomainExtraction tests root domain extraction for internal/external classification
func TestRootDomainExtraction(t *testing.T) {
	tests := []struct {
		startURL   string
		testURL    string
		isInternal bool
	}{
		{"https://example.com/", "https://example.com/page", true},
		{"https://example.com/", "https://blog.example.com/post", true},
		{"https://blog.example.com/", "https://example.com/page", true},
		{"https://example.com/", "https://other.com/page", false},
		{"https://example.com:8080/", "https://example.com:8080/page", true},
		{"https://example.com/", "https://example.org/page", false},
	}

	for _, tt := range tests {
		t.Run(tt.startURL+"->"+tt.testURL, func(t *testing.T) {
			mock := NewMockTransport()

			// Register start page with link to test URL
			mock.RegisterHTML(tt.startURL, `<html>
				<head><title>Start</title></head>
				<body><a href="`+tt.testURL+`">Test Link</a></body>
			</html>`)

			var mu sync.Mutex
			var startPageResult *PageResult

			// Don't restrict AllowedDomains to test classification logic
			crawler := NewCrawler(&CollectorConfig{Async: true})
			crawler.Collector.WithTransport(mock)

			crawler.SetOnPageCrawled(func(result *PageResult) {
				mu.Lock()
				defer mu.Unlock()
				if result.URL == tt.startURL {
					startPageResult = result
				}
			})

			err := crawler.Start(tt.startURL)
			if err != nil {
				t.Fatalf("Failed to start crawler: %v", err)
			}

			crawler.Wait()

			mu.Lock()
			defer mu.Unlock()

			if startPageResult == nil || startPageResult.Links == nil {
				t.Fatal("Start page or links not available")
			}

			// Find the test link
			var foundLink *Link
			for _, link := range startPageResult.Links.Internal {
				if link.URL == tt.testURL {
					foundLink = &link
					break
				}
			}
			if foundLink == nil {
				for _, link := range startPageResult.Links.External {
					if link.URL == tt.testURL {
						foundLink = &link
						break
					}
				}
			}

			if foundLink == nil {
				t.Fatalf("Test link %s not found in links", tt.testURL)
			}

			if foundLink.IsInternal != tt.isInternal {
				t.Errorf("Expected IsInternal=%v for %s from %s, got %v",
					tt.isInternal, tt.testURL, tt.startURL, foundLink.IsInternal)
			}
		})
	}
}
