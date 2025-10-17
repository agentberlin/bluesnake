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
	"strings"
	"testing"
)

func TestLinkPositionClassification(t *testing.T) {
	tests := []struct {
		name             string
		html             string
		expectedPosition string
		linkHref         string
	}{
		{
			name: "link in main content",
			html: `<html><body><main><article><p>Check out <a href="/page1">this page</a></p></article></main></body></html>`,
			expectedPosition: "content",
			linkHref:         "/page1",
		},
		{
			name: "link in navigation",
			html: `<html><body><nav><ul><li><a href="/home">Home</a></li></ul></nav></body></html>`,
			expectedPosition: "navigation",
			linkHref:         "/home",
		},
		{
			name: "link in header",
			html: `<html><body><header><a href="/logo">Logo</a></header></body></html>`,
			expectedPosition: "header",
			linkHref:         "/logo",
		},
		{
			name: "link in footer",
			html: `<html><body><footer><a href="/privacy">Privacy Policy</a></footer></body></html>`,
			expectedPosition: "footer",
			linkHref:         "/privacy",
		},
		{
			name: "link in sidebar",
			html: `<html><body><aside><a href="/related">Related Content</a></aside></body></html>`,
			expectedPosition: "sidebar",
			linkHref:         "/related",
		},
		{
			name: "link in breadcrumbs",
			html: `<html><body><nav class="breadcrumb"><a href="/home">Home</a></nav></body></html>`,
			expectedPosition: "breadcrumbs",
			linkHref:         "/home",
		},
		{
			name: "link in pagination",
			html: `<html><body><div class="pagination"><a href="/page2">Next</a></div></body></html>`,
			expectedPosition: "pagination",
			linkHref:         "/page2",
		},
		{
			name: "link with role=main",
			html: `<html><body><div role="main"><p><a href="/content">Content Link</a></p></div></body></html>`,
			expectedPosition: "content",
			linkHref:         "/content",
		},
		{
			name: "link with role=navigation",
			html: `<html><body><div role="navigation"><a href="/menu">Menu</a></div></body></html>`,
			expectedPosition: "navigation",
			linkHref:         "/menu",
		},
		{
			name: "link in article tag",
			html: `<html><body><article><a href="/story">Read more</a></article></body></html>`,
			expectedPosition: "content",
			linkHref:         "/story",
		},
		{
			name: "link with unknown position",
			html: `<html><body><div><a href="/unknown">Unknown</a></div></body></html>`,
			expectedPosition: "unknown",
			linkHref:         "/unknown",
		},
		{
			name: "link in menu class",
			html: `<html><body><div class="menu"><a href="/products">Products</a></div></body></html>`,
			expectedPosition: "navigation",
			linkHref:         "/products",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock server
			mockTransport := NewMockTransport()
			mockTransport.RegisterResponse("http://example.com/", &MockResponse{
				StatusCode: 200,
				Body:       tt.html,
				Headers:    http.Header{"Content-Type": []string{"text/html"}},
			})

			// Create crawler with mock transport
			config := NewDefaultConfig()
			config.AllowedDomains = []string{"example.com"}

			crawler := NewCrawler(context.Background(), config)
			crawler.Collector.WithTransport(mockTransport)

			// Track collected links
			var collectedLinks []Link

			crawler.SetOnPageCrawled(func(result *PageResult) {
				if result.Links != nil {
					collectedLinks = append(collectedLinks, result.Links.Internal...)
					collectedLinks = append(collectedLinks, result.Links.External...)
				}
			})

			// Start crawling
			crawler.Start("http://example.com/")
			crawler.Wait()

			// Find the link we're testing
			found := false
			for _, link := range collectedLinks {
				if strings.HasSuffix(link.URL, tt.linkHref) {
					found = true
					if link.Position != tt.expectedPosition {
						t.Errorf("Expected position %q, got %q for link %s\nDOM Path: %s",
							tt.expectedPosition, link.Position, link.URL, link.DOMPath)
					}
					if link.DOMPath == "" {
						t.Errorf("DOMPath should not be empty for link %s", link.URL)
					}
					break
				}
			}

			if !found {
				t.Errorf("Link with href %q not found in collected links", tt.linkHref)
				for _, link := range collectedLinks {
					t.Logf("  Found link: %s (position: %s, path: %s)", link.URL, link.Position, link.DOMPath)
				}
			}
		})
	}
}

func TestLinkPositionHelpers(t *testing.T) {
	tests := []struct {
		name                     string
		position                 string
		expectedIsContent        bool
		expectedIsBoilerplate    bool
	}{
		{"content position", "content", true, false},
		{"navigation position", "navigation", false, true},
		{"header position", "header", false, true},
		{"footer position", "footer", false, true},
		{"sidebar position", "sidebar", false, true},
		{"breadcrumbs position", "breadcrumbs", false, true},
		{"pagination position", "pagination", false, true},
		{"unknown position", "unknown", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotContent := isContentLink(tt.position)
			if gotContent != tt.expectedIsContent {
				t.Errorf("isContentLink(%q) = %v, want %v", tt.position, gotContent, tt.expectedIsContent)
			}

			gotBoilerplate := isBoilerplateLink(tt.position)
			if gotBoilerplate != tt.expectedIsBoilerplate {
				t.Errorf("isBoilerplateLink(%q) = %v, want %v", tt.position, gotBoilerplate, tt.expectedIsBoilerplate)
			}
		})
	}
}
