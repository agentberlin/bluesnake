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
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// hostnameRegex matches valid hostnames and IP addresses
// Valid hostname components: alphanumeric, hyphens (not at start/end)
// Must have at least one dot OR be "localhost" OR be an IP address
var hostnameRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

// isValidHostname validates a hostname or IP address
func isValidHostname(hostname string) bool {
	// Check if it's "localhost"
	if hostname == "localhost" {
		return true
	}

	// Check if it's a valid IPv4 address
	if ip := net.ParseIP(hostname); ip != nil && ip.To4() != nil {
		return true
	}

	// Check if it's a valid IPv6 address
	if ip := net.ParseIP(hostname); ip != nil && ip.To16() != nil {
		return true
	}

	// Check if it matches valid hostname pattern (must have at least one dot)
	// This rejects single words like "randomstring" but allows "example.com"
	if !strings.Contains(hostname, ".") && hostname != "localhost" {
		return false
	}

	// Additional validation: hostname must match standard DNS naming rules
	// or be localhost or an IP address
	return hostnameRegex.MatchString(hostname)
}

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

	// Validate hostname format
	if !isValidHostname(hostname) {
		return "", "", fmt.Errorf("invalid hostname: %s (must be a valid domain name, IP address, or 'localhost')", hostname)
	}

	// Build normalized URL
	// Keep port if it's non-standard (not 80 for http, not 443 for https)
	// Preserve the original scheme (http or https)
	scheme := parsedURL.Scheme
	normalizedURL := scheme + "://" + hostname
	if parsedURL.Port() != "" {
		port := parsedURL.Port()
		// Only keep port if it's not the default for the scheme
		isDefaultPort := (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
		if !isDefaultPort {
			normalizedURL = scheme + "://" + hostname + ":" + port
			// Include port in domain identifier for non-standard ports
			hostname = hostname + ":" + port
		}
	}

	return normalizedURL, hostname, nil
}

// resolveURL follows redirects and returns the final destination URL.
// This is used to resolve cases like amahahealth.com -> www.amahahealth.com
// so that the project is created with the canonical domain.
//
// Returns the final URL after following redirects, or the input URL if:
// - The request fails (network error, timeout, etc.)
// - The URL is already the final destination (no redirects)
func resolveURL(inputURL string) string {
	// Ensure URL has a scheme
	if !strings.HasPrefix(inputURL, "http://") && !strings.HasPrefix(inputURL, "https://") {
		inputURL = "https://" + inputURL
	}

	// Create HTTP client with redirect tracking
	client := &http.Client{
		Timeout: 5 * time.Second,
		// Default behavior follows redirects and we can get final URL from response
	}

	// Use HEAD request to minimize data transfer
	req, err := http.NewRequest("HEAD", inputURL, nil)
	if err != nil {
		return inputURL // Fall back to input URL on error
	}

	// Set a reasonable user agent
	req.Header.Set("User-Agent", "bluesnake/1.0 (+https://snake.blue)")

	resp, err := client.Do(req)
	if err != nil {
		return inputURL // Fall back to input URL on error
	}
	defer resp.Body.Close()

	// Get the final URL after all redirects
	finalURL := resp.Request.URL.String()

	return finalURL
}
