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
	"fmt"
	"net/url"
	"strings"
)

// normalizeURL normalizes a URL input and extracts the domain identifier
// Returns: (normalizedURL, domain, error)
func normalizeURL(input string) (string, string, error) {
	// Trim whitespace
	input = strings.TrimSpace(input)

	if input == "" {
		return "", "", fmt.Errorf("empty URL")
	}

	// Add https:// if no protocol is present
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse the URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %v", err)
	}

	// Extract hostname (includes subdomain, excludes port)
	hostname := parsedURL.Hostname()
	if hostname == "" {
		return "", "", fmt.Errorf("no hostname in URL")
	}

	// Convert hostname to lowercase for case-insensitive comparison
	hostname = strings.ToLower(hostname)

	// Build normalized URL
	// Keep port if it's non-standard (not 80 for http, not 443 for https)
	normalizedURL := "https://" + hostname
	if parsedURL.Port() != "" {
		port := parsedURL.Port()
		// Only keep port if it's not the default for https (443)
		if port != "443" {
			normalizedURL = "https://" + hostname + ":" + port
			// Include port in domain identifier for non-standard ports
			hostname = hostname + ":" + port
		}
	}

	return normalizedURL, hostname, nil
}
