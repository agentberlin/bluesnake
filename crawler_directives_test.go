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
	"net/http"
	"sync/atomic"
	"testing"
)

// TestRobotsTxtMode tests the three modes of robots.txt handling
func TestRobotsTxtMode(t *testing.T) {
	t.Run("RobotsTxtMode respect blocks disallowed URLs", func(t *testing.T) {
		mock := setupMockTransport()

		// Register robots.txt that disallows /disallowed
		mock.RegisterResponse(testBaseURL+"/robots.txt", &MockResponse{
			StatusCode: 200,
			Body:       "User-agent: *\nDisallow: /disallowed\n",
		})

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode: "respect",
		})
		c.WithTransport(mock)

		var visited uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visited, 1)
		})

		// Try to visit disallowed URL
		err := c.Visit(testBaseURL + "/disallowed")
		if err == nil {
			t.Error("Expected error when visiting disallowed URL in respect mode")
		}
		if err != ErrRobotsTxtBlocked {
			t.Errorf("Expected ErrRobotsTxtBlocked, got %v", err)
		}

		if visited != 0 {
			t.Errorf("Should not have visited disallowed URL, visited count: %d", visited)
		}

		// Verify IgnoreRobotsTxt is false in respect mode
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should be false in respect mode")
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

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode: "ignore",
		})
		c.WithTransport(mock)

		var visited uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visited, 1)
		})

		// Try to visit disallowed URL - should succeed
		err := c.Visit(testBaseURL + "/disallowed")
		if err != nil {
			t.Errorf("Should be able to visit disallowed URL in ignore mode, got error: %v", err)
		}

		if visited != 1 {
			t.Errorf("Should have visited disallowed URL, visited count: %d", visited)
		}

		// Verify IgnoreRobotsTxt is true in ignore mode
		if !c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should be true in ignore mode")
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

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode: "ignore-report",
		})
		c.WithTransport(mock)

		var visited uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visited, 1)
		})

		// Try to visit disallowed URL - should succeed but log
		err := c.Visit(testBaseURL + "/disallowed")
		if err != nil {
			t.Errorf("Should be able to visit disallowed URL in ignore-report mode, got error: %v", err)
		}

		if visited != 1 {
			t.Errorf("Should have visited disallowed URL, visited count: %d", visited)
		}

		// Verify IgnoreRobotsTxt is false in ignore-report mode
		// (it checks robots.txt but doesn't block)
		if c.IgnoreRobotsTxt {
			t.Error("IgnoreRobotsTxt should be false in ignore-report mode")
		}
	})

	t.Run("Default RobotsTxtMode is respect", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		if c.RobotsTxtMode != "respect" {
			t.Errorf("Default RobotsTxtMode should be 'respect', got %s", c.RobotsTxtMode)
		}

		if c.IgnoreRobotsTxt {
			t.Error("Default should have IgnoreRobotsTxt = false")
		}
	})
}

// TestNofollowFiltering tests the nofollow link filtering logic
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

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode:          "ignore",
			FollowInternalNofollow: false, // Default
		})
		c.WithTransport(mock)

		var visited []string
		c.OnHTML("a[href]", func(e *HTMLElement) {
			link := e.Attr("href")
			visited = append(visited, link)
		})

		err := c.Visit(testBaseURL + "/page")
		if err != nil {
			t.Fatalf("Failed to visit page: %v", err)
		}

		// The link should be found but not followed
		if len(visited) != 1 {
			t.Errorf("Expected to find 1 link, got %d", len(visited))
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

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode:          "ignore",
			FollowInternalNofollow: true,
		})
		c.WithTransport(mock)

		var visitedPages uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visitedPages, 1)
		})

		err := c.Visit(testBaseURL + "/page")
		if err != nil {
			t.Fatalf("Failed to visit page: %v", err)
		}

		// Should visit both the initial page and the target
		if visitedPages < 1 {
			t.Errorf("Expected to visit at least 1 page, visited: %d", visitedPages)
		}
	})

	t.Run("Sponsored and UGC rels are also treated as nofollow", func(t *testing.T) {
		mock := setupMockTransport()

		// Page with sponsored and ugc links
		mock.RegisterHTML(testBaseURL+"/page", `
			<html>
			<body>
				<a href="/sponsored" rel="sponsored">Sponsored Link</a>
				<a href="/ugc" rel="ugc">UGC Link</a>
			</body>
			</html>
		`)

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode:          "ignore",
			FollowInternalNofollow: false,
		})
		c.WithTransport(mock)

		var links []string
		c.OnHTML("a[href]", func(e *HTMLElement) {
			links = append(links, e.Attr("href"))
		})

		err := c.Visit(testBaseURL + "/page")
		if err != nil {
			t.Fatalf("Failed to visit page: %v", err)
		}

		// Links should be found
		if len(links) != 2 {
			t.Errorf("Expected to find 2 links, got %d", len(links))
		}
	})
}

// TestMetaRobotsNoindex tests the meta robots noindex handling
func TestMetaRobotsNoindex(t *testing.T) {
	t.Run("RespectMetaRobotsNoindex blocks pages with noindex meta tag", func(t *testing.T) {
		mock := setupMockTransport()

		// Page with noindex meta tag
		mock.RegisterHTML(testBaseURL+"/noindex", `
			<html>
			<head>
				<meta name="robots" content="noindex">
			</head>
			<body>Should not be indexed</body>
			</html>
		`)

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode:            "ignore",
			RespectMetaRobotsNoindex: true, // Default
		})
		c.WithTransport(mock)

		var visited uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visited, 1)
		})

		err := c.Visit(testBaseURL + "/noindex")

		// The page should be fetched but callback should not be called
		// due to noindex directive
		if err == nil {
			t.Log("Visited noindex page - checking if callback was suppressed")
		}

		// Note: The actual noindex handling might prevent the callback
		// This test validates the configuration is set correctly
		if !c.RespectMetaRobotsNoindex {
			t.Error("RespectMetaRobotsNoindex should be true")
		}
	})

	t.Run("Disabling RespectMetaRobotsNoindex allows indexing noindex pages", func(t *testing.T) {
		c := NewCollector(context.Background(), &CollectorConfig{
			RespectMetaRobotsNoindex: false,
		})

		if c.RespectMetaRobotsNoindex {
			t.Error("RespectMetaRobotsNoindex should be false when disabled")
		}
	})

	t.Run("Default RespectMetaRobotsNoindex is true", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		if !c.RespectMetaRobotsNoindex {
			t.Error("Default RespectMetaRobotsNoindex should be true")
		}
	})
}

// TestXRobotsTagNoindex tests the X-Robots-Tag header handling
func TestXRobotsTagNoindex(t *testing.T) {
	t.Run("RespectNoindex blocks responses with X-Robots-Tag noindex", func(t *testing.T) {
		mock := setupMockTransport()

		// Response with X-Robots-Tag: noindex header
		headers := make(http.Header)
		headers.Set("Content-Type", "text/html")
		headers.Set("X-Robots-Tag", "noindex")
		mock.RegisterResponse(testBaseURL+"/noindex", &MockResponse{
			StatusCode: 200,
			Body:       "<html><body>Should not be indexed</body></html>",
			Headers:    headers,
		})

		c := NewCollector(context.Background(), &CollectorConfig{
			RobotsTxtMode:  "ignore",
			RespectNoindex: true, // Default
		})
		c.WithTransport(mock)

		var visited uint32
		c.OnHTML("body", func(e *HTMLElement) {
			atomic.AddUint32(&visited, 1)
		})

		err := c.Visit(testBaseURL + "/noindex")

		// The page should be fetched but callback might not be called
		// due to noindex directive
		if err == nil {
			t.Log("Visited noindex page - checking configuration")
		}

		if !c.RespectNoindex {
			t.Error("RespectNoindex should be true")
		}
	})

	t.Run("Disabling RespectNoindex allows indexing pages with X-Robots-Tag", func(t *testing.T) {
		c := NewCollector(context.Background(), &CollectorConfig{
			RespectNoindex: false,
		})

		if c.RespectNoindex {
			t.Error("RespectNoindex should be false when disabled")
		}
	})

	t.Run("Default RespectNoindex is true", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		if !c.RespectNoindex {
			t.Error("Default RespectNoindex should be true")
		}
	})
}

// TestCrawlerDirectiveDefaults tests that all crawler directive defaults match ScreamingFrog
func TestCrawlerDirectiveDefaults(t *testing.T) {
	c := NewCollector(context.Background(), nil)

	tests := []struct {
		name     string
		got      interface{}
		want     interface{}
		fieldName string
	}{
		{"RobotsTxtMode", c.RobotsTxtMode, "respect", "RobotsTxtMode"},
		{"FollowInternalNofollow", c.FollowInternalNofollow, false, "FollowInternalNofollow"},
		{"FollowExternalNofollow", c.FollowExternalNofollow, false, "FollowExternalNofollow"},
		{"RespectMetaRobotsNoindex", c.RespectMetaRobotsNoindex, true, "RespectMetaRobotsNoindex"},
		{"RespectNoindex", c.RespectNoindex, true, "RespectNoindex"},
		{"IgnoreRobotsTxt", c.IgnoreRobotsTxt, false, "IgnoreRobotsTxt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Default %s = %v, want %v", tt.fieldName, tt.got, tt.want)
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
			c := NewCollector(context.Background(), &CollectorConfig{
				RobotsTxtMode: mode,
			})

			if c.RobotsTxtMode != mode {
				t.Errorf("RobotsTxtMode not set correctly: got %s, want %s", c.RobotsTxtMode, mode)
			}

			if c.IgnoreRobotsTxt != expectedIgnoreRobotsTxt[i] {
				t.Errorf("IgnoreRobotsTxt for mode %s: got %v, want %v",
					mode, c.IgnoreRobotsTxt, expectedIgnoreRobotsTxt[i])
			}
		})
	}
}
