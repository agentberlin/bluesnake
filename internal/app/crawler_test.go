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
	"testing"
)

// TestBuildDomainFilter tests the domain filter regex generation
func TestBuildDomainFilter(t *testing.T) {
	tests := []struct {
		name              string
		domain            string
		includeSubdomains bool
		shouldMatch       []string
		shouldNotMatch    []string
	}{
		{
			name:              "Exact domain only",
			domain:            "example.com",
			includeSubdomains: false,
			shouldMatch: []string{
				"https://example.com/",
				"https://example.com/page",
				"https://example.com/path/to/page",
				"http://example.com/",
			},
			shouldNotMatch: []string{
				"https://blog.example.com/",
				"https://api.example.com/page",
				"https://sub.example.com/",
				"https://example.org/",
				"https://notexample.com/",
			},
		},
		{
			name:              "Include subdomains",
			domain:            "example.com",
			includeSubdomains: true,
			shouldMatch: []string{
				"https://example.com/",
				"https://example.com/page",
				"https://blog.example.com/",
				"https://api.example.com/page",
				"https://deep.sub.example.com/",
				"http://example.com/",
				"http://blog.example.com/",
			},
			shouldNotMatch: []string{
				"https://example.org/",
				"https://notexample.com/",
				"https://examplecom.com/",
			},
		},
		{
			name:              "Domain with port - exact",
			domain:            "example.com:8080",
			includeSubdomains: false,
			shouldMatch: []string{
				"https://example.com:8080/",
				"https://example.com:8080/page",
				"http://example.com:8080/",
			},
			shouldNotMatch: []string{
				"https://example.com/", // different port
				"https://blog.example.com:8080/",
			},
		},
		{
			name:              "Domain with port - include subdomains",
			domain:            "example.com:8080",
			includeSubdomains: true,
			shouldMatch: []string{
				"https://example.com/",
				"https://blog.example.com/",
				"https://api.example.com/page",
			},
			shouldNotMatch: []string{
				"https://example.org/",
				"https://notexample.com/",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := buildDomainFilter(tt.domain, tt.includeSubdomains)
			if err != nil {
				t.Fatalf("buildDomainFilter() error = %v", err)
			}

			// Test URLs that should match
			for _, url := range tt.shouldMatch {
				if !filter.MatchString(url) {
					t.Errorf("Expected %q to match domain %q (includeSubdomains=%v), but it didn't",
						url, tt.domain, tt.includeSubdomains)
				}
			}

			// Test URLs that should not match
			for _, url := range tt.shouldNotMatch {
				if filter.MatchString(url) {
					t.Errorf("Expected %q NOT to match domain %q (includeSubdomains=%v), but it did",
						url, tt.domain, tt.includeSubdomains)
				}
			}
		})
	}
}
