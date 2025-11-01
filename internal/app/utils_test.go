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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantURL     string
		wantDomain  string
		shouldError bool
	}{
		// Valid URLs
		{
			name:       "HTTPS URL",
			input:      "https://example.com",
			wantURL:    "https://example.com",
			wantDomain: "example.com",
		},
		{
			name:       "HTTP URL",
			input:      "http://example.com",
			wantURL:    "http://example.com",
			wantDomain: "example.com",
		},
		{
			name:       "Domain without protocol",
			input:      "example.com",
			wantURL:    "https://example.com",
			wantDomain: "example.com",
		},
		{
			name:       "URL with port",
			input:      "https://example.com:8080",
			wantURL:    "https://example.com:8080",
			wantDomain: "example.com:8080",
		},
		{
			name:       "URL with path (path should be stripped)",
			input:      "https://example.com/path",
			wantURL:    "https://example.com",
			wantDomain: "example.com",
		},
		{
			name:       "Subdomain",
			input:      "blog.example.com",
			wantURL:    "https://blog.example.com",
			wantDomain: "blog.example.com",
		},

		// Invalid URLs that should be rejected
		{
			name:        "Empty string",
			input:       "",
			shouldError: true,
		},
		{
			name:        "Just whitespace",
			input:       "   ",
			shouldError: true,
		},
		{
			name:        "Random string (no dots)",
			input:       "randomstring",
			shouldError: true, // This should fail!
		},
		{
			name:        "String with spaces",
			input:       "hello world",
			shouldError: true,
		},
		{
			name:        "Invalid characters",
			input:       "exa mple.com",
			shouldError: true,
		},
		{
			name:        "Just protocol",
			input:       "https://",
			shouldError: true,
		},
		{
			name:        "Invalid IP",
			input:       "999.999.999.999",
			shouldError: true,
		},

		// Edge cases that might be valid
		{
			name:       "Localhost",
			input:      "localhost",
			wantURL:    "https://localhost",
			wantDomain: "localhost",
		},
		{
			name:       "IP address",
			input:      "192.168.1.1",
			wantURL:    "https://192.168.1.1",
			wantDomain: "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotDomain, err := normalizeURL(tt.input)

			if tt.shouldError {
				assert.Error(t, err, "Expected error for input %q", tt.input)
			} else {
				require.NoError(t, err, "Unexpected error for input %q", tt.input)
				assert.Equal(t, tt.wantURL, gotURL, "URL mismatch")
				assert.Equal(t, tt.wantDomain, gotDomain, "Domain mismatch")
			}
		})
	}
}
