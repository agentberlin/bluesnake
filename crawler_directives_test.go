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
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRobotsTxtMode tests the three modes of robots.txt handling via Crawler
func TestRobotsTxtMode(t *testing.T) {
	t.Run("RobotsTxtMode respect blocks disallowed URLs", func(t *testing.T) {
		mock := setupMockTransport()

		// Register robots.txt that disallows /disallowed
		mock.RegisterResponse(testBaseURL+"/robots.txt", &MockResponse{
			StatusCode: 200,
			Body:       "User-agent: *\nDisallow: /disallowed\n",
		})

		// Register allowed page
		mock.RegisterHTML(testBaseURL+"/allowed", "<html><body>Allowed Content</body></html>")

		// Create crawler with respect mode
		config := NewDefaultConfig()
		config.RobotsTxtMode = "respect"
		crawler := NewCrawler(context.Background(), config)
		crawler.Collector.WithTransport(mock)

		var visitedURLs []string
		var mu sync.Mutex

		crawler.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			visitedURLs = append(visitedURLs, result.URL)
		})

		// Start crawl with allowed URL
		if err := crawler.Start(testBaseURL + "/allowed"); err != nil {
			t.Fatalf("Failed to start crawler: %v", err)
		}
		crawler.Wait()

		// Verify allowed URL was crawled
		if len(visitedURLs) == 0 {
			t.Error("Should have visited allowed URL")
		}

		// Now test that disallowed URL is blocked
		// Create a new crawler instance for clean state
		crawler2 := NewCrawler(context.Background(), config)
		crawler2.Collector.WithTransport(mock)

		visitedURLs = nil
		crawler2.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			// If we get here with disallowed URL, robots.txt was not respected
			if strings.Contains(result.URL, "/disallowed") {
				t.Error("Should not have crawled disallowed URL")
			}
			visitedURLs = append(visitedURLs, result.URL)
		})

		// Start crawl with disallowed URL - should be blocked by robots.txt
		if err := crawler2.Start(testBaseURL + "/disallowed"); err != nil {
			t.Logf("Expected: Start returned with error: %v", err)
		}
		crawler2.Wait()

		// Verify disallowed URL was NOT crawled
		for _, url := range visitedURLs {
			if strings.Contains(url, "/disallowed") {
				t.Error("Should not have crawled disallowed URL in respect mode")
			}
		}
	})

	t.Run("RobotsTxtMode ignore bypasses robots.txt", func(t *testing.T) {
		mock := setupMockTransport()

		// Register robots.txt that disallows /disallowed
		mock.RegisterResponse(testBaseURL+"/robots.txt", &MockResponse{
			StatusCode: 200,
			Body:       "User-agent: *\nDisallow: /disallowed\n",
		})

		// Register HTML response for /disallowed
		mock.RegisterHTML(testBaseURL+"/disallowed", "<html><body>Disallowed Content</body></html>")

		// Create crawler with ignore mode
		config := NewDefaultConfig()
		config.RobotsTxtMode = "ignore"
		crawler := NewCrawler(context.Background(), config)
		crawler.Collector.WithTransport(mock)

		var visitedURLs []string
		var mu sync.Mutex

		crawler.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			visitedURLs = append(visitedURLs, result.URL)
		})

		// Start crawl with disallowed URL - should succeed
		if err := crawler.Start(testBaseURL + "/disallowed"); err != nil {
			t.Fatalf("Failed to start crawler: %v", err)
		}
		crawler.Wait()

		// Verify disallowed URL was crawled (robots.txt ignored)
		if len(visitedURLs) == 0 {
			t.Error("Should have visited disallowed URL in ignore mode")
		}

		// Verify IgnoreRobotsTxt is true in ignore mode
		if !crawler.Collector.IgnoreRobotsTxt {
			t.Error("Collector.IgnoreRobotsTxt should be true in ignore mode")
		}

		// Verify shouldIgnoreRobotsTxt returns true
		if !crawler.shouldIgnoreRobotsTxt() {
			t.Error("shouldIgnoreRobotsTxt() should return true in ignore mode")
		}
	})

	t.Run("RobotsTxtMode ignore-report logs but does not block", func(t *testing.T) {
		mock := setupMockTransport()

		// Register robots.txt that disallows /disallowed
		mock.RegisterResponse(testBaseURL+"/robots.txt", &MockResponse{
			StatusCode: 200,
			Body:       "User-agent: *\nDisallow: /disallowed\n",
		})

		// Register HTML response for /disallowed
		mock.RegisterHTML(testBaseURL+"/disallowed", "<html><body>Disallowed Content</body></html>")

		// Create crawler with ignore-report mode
		config := NewDefaultConfig()
		config.RobotsTxtMode = "ignore-report"
		crawler := NewCrawler(context.Background(), config)
		crawler.Collector.WithTransport(mock)

		var visitedURLs []string
		var mu sync.Mutex

		crawler.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			visitedURLs = append(visitedURLs, result.URL)
		})

		// Start crawl with disallowed URL - should succeed but log
		if err := crawler.Start(testBaseURL + "/disallowed"); err != nil {
			t.Fatalf("Failed to start crawler: %v", err)
		}
		crawler.Wait()

		// Verify disallowed URL was crawled (logged but not blocked)
		if len(visitedURLs) == 0 {
			t.Error("Should have visited disallowed URL in ignore-report mode")
		}

		// Verify IgnoreRobotsTxt is false in ignore-report mode
		// (it checks robots.txt but doesn't block)
		if crawler.Collector.IgnoreRobotsTxt {
			t.Error("Collector.IgnoreRobotsTxt should be false in ignore-report mode")
		}

		// Verify shouldIgnoreRobotsTxt returns false
		if crawler.shouldIgnoreRobotsTxt() {
			t.Error("shouldIgnoreRobotsTxt() should return false in ignore-report mode")
		}
	})

	t.Run("Default RobotsTxtMode is respect", func(t *testing.T) {
		config := NewDefaultConfig()

		if config.RobotsTxtMode != "respect" {
			t.Errorf("Default RobotsTxtMode should be 'respect', got %s", config.RobotsTxtMode)
		}
	})
}

// TestNofollowFiltering tests the nofollow link filtering logic via Crawler
func TestNofollowFiltering(t *testing.T) {
	t.Run("Internal nofollow links are filtered by default", func(t *testing.T) {
		mock := setupMockTransport()

		// Page with nofollow link
		mock.RegisterHTML(testBaseURL+"/page", `
			<html>
			<body>
				<a href="/target" rel="nofollow">Nofollow Link</a>
			</body>
			</html>
		`)
		mock.RegisterHTML(testBaseURL+"/target", `<html><body>Target</body></html>`)

		// Create crawler with default settings (FollowInternalNofollow = false)
		config := NewDefaultConfig()
		config.RobotsTxtMode = "ignore" // Ignore robots.txt for this test
		crawler := NewCrawler(context.Background(), config)
		crawler.Collector.WithTransport(mock)

		var visitedPages []string
		var mu sync.Mutex

		crawler.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			visitedPages = append(visitedPages, result.URL)
		})

		if err := crawler.Start(testBaseURL + "/page"); err != nil {
			t.Fatalf("Failed to start crawler: %v", err)
		}

		// Wait with timeout
		done := make(chan struct{})
		go func() {
			crawler.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Crawler did not finish in time")
		}

		// Should only visit the initial page, not the nofollow target
		if len(visitedPages) != 1 {
			t.Errorf("Expected to visit 1 page (nofollow should be ignored), got %d: %v", len(visitedPages), visitedPages)
		}

		// Verify FollowInternalNofollow is false by default
		if crawler.followInternalNofollow {
			t.Error("followInternalNofollow should be false by default")
		}
	})

	t.Run("FollowInternalNofollow allows following internal nofollow links", func(t *testing.T) {
		mock := setupMockTransport()

		// Page with nofollow link
		mock.RegisterHTML(testBaseURL+"/page", `
			<html>
			<body>
				<a href="/target" rel="nofollow">Nofollow Link</a>
			</body>
			</html>
		`)
		mock.RegisterHTML(testBaseURL+"/target", `<html><body>Target</body></html>`)

		// Create crawler with FollowInternalNofollow = true
		config := NewDefaultConfig()
		config.RobotsTxtMode = "ignore"
		config.FollowInternalNofollow = true
		crawler := NewCrawler(context.Background(), config)
		crawler.Collector.WithTransport(mock)

		var visitedPages []string
		var mu sync.Mutex

		crawler.SetOnPageCrawled(func(result *PageResult) {
			mu.Lock()
			defer mu.Unlock()
			visitedPages = append(visitedPages, result.URL)
		})

		if err := crawler.Start(testBaseURL + "/page"); err != nil {
			t.Fatalf("Failed to start crawler: %v", err)
		}

		// Wait with timeout
		done := make(chan struct{})
		go func() {
			crawler.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Crawler did not finish in time")
		}

		// Should visit both pages (initial and nofollow target)
		if len(visitedPages) < 2 {
			t.Errorf("Expected to visit at least 2 pages with FollowInternalNofollow=true, got %d: %v", len(visitedPages), visitedPages)
		}

		// Verify FollowInternalNofollow is set
		if !crawler.followInternalNofollow {
			t.Error("followInternalNofollow should be true")
		}
	})
}

// TestCrawlerDirectiveDefaults tests that all crawler directive defaults match expectations
func TestCrawlerDirectiveDefaults(t *testing.T) {
	config := NewDefaultConfig()

	tests := []struct {
		name  string
		got   interface{}
		want  interface{}
	}{
		{"RobotsTxtMode", config.RobotsTxtMode, "respect"},
		{"FollowInternalNofollow", config.FollowInternalNofollow, false},
		{"FollowExternalNofollow", config.FollowExternalNofollow, false},
		{"RespectMetaRobotsNoindex", config.RespectMetaRobotsNoindex, true},
		{"RespectNoindex", config.RespectNoindex, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Default %s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestRobotsTxtModeConfigurationPropagation tests that RobotsTxtMode is properly set from config
func TestRobotsTxtModeConfigurationPropagation(t *testing.T) {
	modes := []string{"respect", "ignore", "ignore-report"}
	expectedIgnoreRobotsTxt := []bool{false, true, false}

	for i, mode := range modes {
		t.Run("RobotsTxtMode "+mode, func(t *testing.T) {
			config := NewDefaultConfig()
			config.RobotsTxtMode = mode
			crawler := NewCrawler(context.Background(), config)

			if crawler.robotsTxtMode != mode {
				t.Errorf("Crawler.robotsTxtMode not set correctly: got %s, want %s", crawler.robotsTxtMode, mode)
			}

			if crawler.Collector.IgnoreRobotsTxt != expectedIgnoreRobotsTxt[i] {
				t.Errorf("Collector.IgnoreRobotsTxt for mode %s: got %v, want %v",
					mode, crawler.Collector.IgnoreRobotsTxt, expectedIgnoreRobotsTxt[i])
			}
		})
	}
}

// TestResourceValidation tests that resource validation config is properly set on Crawler
func TestResourceValidation(t *testing.T) {
	t.Run("ResourceValidation is set on Crawler", func(t *testing.T) {
		config := NewDefaultConfig()
		config.ResourceValidation = &ResourceValidationConfig{
			Enabled:       true,
			ResourceTypes: []string{"image", "script"},
			CheckExternal: false,
		}

		crawler := NewCrawler(context.Background(), config)

		if crawler.resourceValidation == nil {
			t.Fatal("ResourceValidation should be set on Crawler")
		}

		if !crawler.resourceValidation.Enabled {
			t.Error("ResourceValidation.Enabled should be true")
		}

		if len(crawler.resourceValidation.ResourceTypes) != 2 {
			t.Errorf("Expected 2 resource types, got %d", len(crawler.resourceValidation.ResourceTypes))
		}

		if crawler.resourceValidation.CheckExternal {
			t.Error("ResourceValidation.CheckExternal should be false")
		}
	})

	t.Run("Default ResourceValidation config", func(t *testing.T) {
		config := NewDefaultConfig()

		if config.ResourceValidation == nil {
			t.Fatal("Default config should have ResourceValidation")
		}

		if !config.ResourceValidation.Enabled {
			t.Error("Default ResourceValidation should be enabled")
		}

		if !config.ResourceValidation.CheckExternal {
			t.Error("Default ResourceValidation should check external resources")
		}
	})
}
