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
	"context"
	"sync"
	"testing"
)

// TestRedirectChainStatusCodes tests that intermediate redirects are reported with their actual status codes
func TestRedirectChainStatusCodes(t *testing.T) {
	mock := NewMockTransport()

	// Register redirect chain: A→B→C with different redirect types
	mock.RegisterRedirect("https://example.com/page-a", "https://example.com/page-b", 301) // Permanent redirect
	mock.RegisterRedirect("https://example.com/page-b", "https://example.com/page-c", 302) // Temporary redirect

	// Register the final destination
	mock.RegisterHTML("https://example.com/page-c", `<html>
		<head><title>Page C</title></head>
		<body><h1>Final destination</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify page-a was reported with status 301
	if result, ok := crawledPages["https://example.com/page-a"]; !ok {
		t.Error("Expected page-a to be crawled and reported")
	} else {
		if result.Status != 301 {
			t.Errorf("Expected page-a to have status 301, got %d", result.Status)
		}
		if result.Title != "" {
			t.Errorf("Expected page-a to have empty title (redirect), got %s", result.Title)
		}
	}

	// Verify page-b was reported with status 302
	if result, ok := crawledPages["https://example.com/page-b"]; !ok {
		t.Error("Expected page-b to be crawled and reported")
	} else {
		if result.Status != 302 {
			t.Errorf("Expected page-b to have status 302, got %d", result.Status)
		}
		if result.Title != "" {
			t.Errorf("Expected page-b to have empty title (redirect), got %s", result.Title)
		}
	}

	// Verify page-c was reported with status 200 and has content
	if result, ok := crawledPages["https://example.com/page-c"]; !ok {
		t.Error("Expected page-c to be crawled and reported")
	} else {
		if result.Status != 200 {
			t.Errorf("Expected page-c to have status 200, got %d", result.Status)
		}
		if result.Title != "Page C" {
			t.Errorf("Expected page-c to have title 'Page C', got '%s'", result.Title)
		}
	}

	// Verify all 3 URLs were reported
	if len(crawledPages) != 3 {
		t.Errorf("Expected 3 pages to be crawled, got %d: %v", len(crawledPages), crawledPages)
	}

	t.Logf("✓ Redirect chain: All 3 URLs reported with correct status codes (301, 302, 200)")
}

// TestRedirect307And308PreserveMethod tests that 307/308 redirects preserve method and body
func TestRedirect307And308PreserveMethod(t *testing.T) {
	mock := NewMockTransport()

	// Register redirect chain with 307 (temporary, preserve method)
	mock.RegisterRedirect("https://example.com/page-a", "https://example.com/page-b", 307)
	mock.RegisterRedirect("https://example.com/page-b", "https://example.com/page-c", 308) // Permanent, preserve method

	// Register the final destination
	mock.RegisterHTML("https://example.com/page-c", `<html>
		<head><title>Page C</title></head>
		<body><h1>Final destination</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify page-a was reported with status 307
	if result, ok := crawledPages["https://example.com/page-a"]; !ok {
		t.Error("Expected page-a to be crawled and reported")
	} else {
		if result.Status != 307 {
			t.Errorf("Expected page-a to have status 307, got %d", result.Status)
		}
	}

	// Verify page-b was reported with status 308
	if result, ok := crawledPages["https://example.com/page-b"]; !ok {
		t.Error("Expected page-b to be crawled and reported")
	} else {
		if result.Status != 308 {
			t.Errorf("Expected page-b to have status 308, got %d", result.Status)
		}
	}

	// Verify page-c was reported with status 200
	if result, ok := crawledPages["https://example.com/page-c"]; !ok {
		t.Error("Expected page-c to be crawled and reported")
	} else {
		if result.Status != 200 {
			t.Errorf("Expected page-c to have status 200, got %d", result.Status)
		}
	}

	t.Logf("✓ Redirect chain with 307/308: All URLs reported with correct status codes")
}

// TestSingleRedirectStatusCode tests that a single redirect is reported correctly
func TestSingleRedirectStatusCode(t *testing.T) {
	mock := NewMockTransport()

	// Register single redirect: A→B
	mock.RegisterRedirect("https://example.com/page-a", "https://example.com/page-b", 301)

	// Register the final destination
	mock.RegisterHTML("https://example.com/page-b", `<html>
		<head><title>Page B</title></head>
		<body><h1>This is page B</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify page-a was reported with status 301
	if result, ok := crawledPages["https://example.com/page-a"]; !ok {
		t.Error("Expected page-a to be crawled and reported")
	} else {
		if result.Status != 301 {
			t.Errorf("Expected page-a to have status 301, got %d", result.Status)
		}
		if result.Title != "" {
			t.Errorf("Expected page-a to have empty title (redirect), got %s", result.Title)
		}
		if len(result.Links.Internal) != 0 || len(result.Links.External) != 0 {
			t.Error("Expected page-a to have no links (redirect)")
		}
	}

	// Verify page-b was reported with status 200 and has content
	if result, ok := crawledPages["https://example.com/page-b"]; !ok {
		t.Error("Expected page-b to be crawled and reported")
	} else {
		if result.Status != 200 {
			t.Errorf("Expected page-b to have status 200, got %d", result.Status)
		}
		if result.Title != "Page B" {
			t.Errorf("Expected page-b to have title 'Page B', got '%s'", result.Title)
		}
	}

	// Verify both URLs were reported
	if len(crawledPages) != 2 {
		t.Errorf("Expected 2 pages to be crawled, got %d", len(crawledPages))
	}

	t.Logf("✓ Single redirect: Both URLs reported with correct status codes (301, 200)")
}

// TestRedirectChainAllURLsMarkedVisited tests that all URLs in redirect chain are marked as visited
func TestRedirectChainAllURLsMarkedVisited(t *testing.T) {
	mock := NewMockTransport()

	// Register redirect chain: A→B→C
	mock.RegisterRedirect("https://example.com/page-a", "https://example.com/page-b", 301)
	mock.RegisterRedirect("https://example.com/page-b", "https://example.com/page-c", 302)

	// Register the final destination
	mock.RegisterHTML("https://example.com/page-c", `<html>
		<head><title>Page C</title></head>
		<body><h1>Final destination</h1></body>
	</html>`)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	// Verify ALL three URLs in the chain are marked as visited
	urls := []string{
		"https://example.com/page-a",
		"https://example.com/page-b",
		"https://example.com/page-c",
	}

	for _, url := range urls {
		hash := requestHash(url, nil)
		visited, err := crawler.store.IsVisited(hash)
		if err != nil {
			t.Fatalf("Error checking if %s is visited: %v", url, err)
		}
		if !visited {
			t.Errorf("Expected %s to be marked as visited in redirect chain", url)
		}
	}

	t.Logf("✓ Redirect chain: All 3 URLs marked as visited correctly")
}

// TestLongRedirectChain tests a long redirect chain (close to max limit)
func TestLongRedirectChain(t *testing.T) {
	mock := NewMockTransport()

	// Register a chain of 8 redirects (well within the 10 limit)
	for i := 0; i < 8; i++ {
		fromURL := "https://example.com/page-" + string(rune('a'+i))
		toURL := "https://example.com/page-" + string(rune('a'+i+1))
		statusCode := 301
		if i%2 == 1 {
			statusCode = 302 // Alternate between 301 and 302
		}
		mock.RegisterRedirect(fromURL, toURL, statusCode)
	}

	// Register the final destination (page-i)
	mock.RegisterHTML("https://example.com/page-i", `<html>
		<head><title>Page I</title></head>
		<body><h1>Final destination after 8 redirects</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify all 9 pages (8 redirects + 1 final) were reported
	expectedCount := 9
	if len(crawledPages) != expectedCount {
		t.Errorf("Expected %d pages to be crawled, got %d", expectedCount, len(crawledPages))
	}

	// Verify the final page has status 200
	if result, ok := crawledPages["https://example.com/page-i"]; !ok {
		t.Error("Expected final page (page-i) to be crawled and reported")
	} else {
		if result.Status != 200 {
			t.Errorf("Expected page-i to have status 200, got %d", result.Status)
		}
		if result.Title != "Page I" {
			t.Errorf("Expected page-i to have title 'Page I', got '%s'", result.Title)
		}
	}

	// Verify intermediate redirects have redirect status codes
	for i := 0; i < 8; i++ {
		url := "https://example.com/page-" + string(rune('a'+i))
		if result, ok := crawledPages[url]; !ok {
			t.Errorf("Expected %s to be crawled and reported", url)
		} else {
			expectedStatus := 301
			if i%2 == 1 {
				expectedStatus = 302
			}
			if result.Status != expectedStatus {
				t.Errorf("Expected %s to have status %d, got %d", url, expectedStatus, result.Status)
			}
		}
	}

	t.Logf("✓ Long redirect chain: All 9 URLs reported with correct status codes")
}

// TestNoRedirect tests that pages without redirects work correctly
func TestNoRedirect(t *testing.T) {
	mock := NewMockTransport()

	// Register a single page (no redirects)
	mock.RegisterHTML("https://example.com/page-a", `<html>
		<head><title>Page A</title></head>
		<body><h1>Direct page</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Verify only one page was reported
	if len(crawledPages) != 1 {
		t.Errorf("Expected 1 page to be crawled, got %d", len(crawledPages))
	}

	// Verify page-a has status 200 and content
	if result, ok := crawledPages["https://example.com/page-a"]; !ok {
		t.Error("Expected page-a to be crawled and reported")
	} else {
		if result.Status != 200 {
			t.Errorf("Expected page-a to have status 200, got %d", result.Status)
		}
		if result.Title != "Page A" {
			t.Errorf("Expected page-a to have title 'Page A', got '%s'", result.Title)
		}
	}

	t.Logf("✓ No redirect: Single page reported correctly with status 200")
}
