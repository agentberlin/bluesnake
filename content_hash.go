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
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/cespare/xxhash/v2"
)

// Regex patterns for stripping dynamic content
var (
	// Timestamp patterns (ISO8601, RFC3339, common formats)
	timestampPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`), // ISO8601/RFC3339
		regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`),                                  // Common timestamp
		regexp.MustCompile(`\d{1,2}/\d{1,2}/\d{4} \d{1,2}:\d{2}(?::\d{2})? (?:AM|PM)`),            // US format with time
		regexp.MustCompile(`(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2},?\s+\d{4}\s+\d{1,2}:\d{2}`), // Month DD, YYYY HH:MM
	}

	// Relative time patterns
	relativeTimePatterns = []*regexp.Regexp{
		regexp.MustCompile(`\d+\s+(?:second|minute|hour|day|week|month|year)s?\s+ago`),
		regexp.MustCompile(`(?:just\s+now|moments?\s+ago)`),
	}

	// Session/Request ID patterns
	sessionIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:session|request|trace)[-_]?id[:=]\s*["']?[a-f0-9-]{8,}["']?`),
		regexp.MustCompile(`(?i)csrf[-_]?token[:=]\s*["']?[a-zA-Z0-9+/=]{16,}["']?`),
		regexp.MustCompile(`(?i)_token["']?\s*[:=]\s*["']?[a-zA-Z0-9+/=]{16,}["']?`),
	}

	// Analytics patterns (Google Analytics, tracking pixels, etc.)
	analyticsPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)google-analytics\.com/(?:analytics|ga)\.js`),
		regexp.MustCompile(`(?i)googletagmanager\.com/gtag/js`),
		regexp.MustCompile(`(?i)www\.google-analytics\.com/collect\?[^\s<>"']+`),
		regexp.MustCompile(`(?i)gtag\s*\([^)]+\)`),
		regexp.MustCompile(`(?i)ga\s*\([^)]+\)`),
		regexp.MustCompile(`(?i)_gaq\.push\([^)]+\)`),
		regexp.MustCompile(`(?i)fbq\s*\([^)]+\)`), // Facebook Pixel
		regexp.MustCompile(`(?i)pixel\.gif\?[^\s<>"']+`),
	}

	// Cache-busting version parameters
	versionParamPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\?v=[a-f0-9]+`),
		regexp.MustCompile(`\?ver=[a-f0-9]+`),
		regexp.MustCompile(`\?_=[0-9]+`),
		regexp.MustCompile(`\?t=[0-9]+`),
	}

	// Whitespace normalization
	whitespacePattern = regexp.MustCompile(`\s+`)
)

// NormalizeContent normalizes HTML content based on the provided configuration
// to make content hashing more reliable by removing dynamic elements
func NormalizeContent(html []byte, config *ContentHashConfig) ([]byte, error) {
	if config == nil {
		config = &ContentHashConfig{
			ExcludeTags:        []string{"script", "style", "nav", "footer"},
			StripTimestamps:    true,
			StripAnalytics:     true,
			StripComments:      true,
			CollapseWhitespace: true,
		}
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	// If IncludeOnlyTags is specified, extract only those tags
	if len(config.IncludeOnlyTags) > 0 {
		doc = extractOnlyTags(doc, config.IncludeOnlyTags)
	}

	// Remove excluded tags
	if len(config.ExcludeTags) > 0 {
		for _, tag := range config.ExcludeTags {
			doc.Find(tag).Remove()
		}
	}

	// Get the HTML string
	content, err := doc.Html()
	if err != nil {
		return nil, fmt.Errorf("failed to render HTML: %w", err)
	}

	contentBytes := []byte(content)

	// Strip HTML comments if configured
	if config.StripComments {
		contentBytes = stripHTMLComments(contentBytes)
	}

	// Strip timestamps if configured
	if config.StripTimestamps {
		contentBytes = stripTimestamps(contentBytes)
	}

	// Strip analytics code if configured
	if config.StripAnalytics {
		contentBytes = stripAnalytics(contentBytes)
	}

	// Strip session IDs and CSRF tokens
	contentBytes = stripSessionIDs(contentBytes)

	// Strip cache-busting version parameters
	contentBytes = stripVersionParams(contentBytes)

	// Collapse whitespace if configured
	if config.CollapseWhitespace {
		contentBytes = collapseWhitespace(contentBytes)
	}

	return contentBytes, nil
}

// extractOnlyTags creates a new document containing only the specified tags
func extractOnlyTags(doc *goquery.Document, tags []string) *goquery.Document {
	selector := strings.Join(tags, ", ")
	extracted := doc.Find(selector)

	// Create a new document with only the extracted content
	newDoc, _ := goquery.NewDocumentFromReader(strings.NewReader("<html><body></body></html>"))
	body := newDoc.Find("body")

	extracted.Each(func(i int, s *goquery.Selection) {
		body.AppendSelection(s)
	})

	return newDoc
}

// stripHTMLComments removes HTML comments from content
func stripHTMLComments(content []byte) []byte {
	commentPattern := regexp.MustCompile(`<!--[\s\S]*?-->`)
	return commentPattern.ReplaceAll(content, []byte(""))
}

// stripTimestamps removes timestamp patterns from content
func stripTimestamps(content []byte) []byte {
	for _, pattern := range timestampPatterns {
		content = pattern.ReplaceAll(content, []byte("[TIMESTAMP]"))
	}
	for _, pattern := range relativeTimePatterns {
		content = pattern.ReplaceAll(content, []byte("[RELATIVE_TIME]"))
	}
	return content
}

// stripAnalytics removes analytics and tracking code from content
func stripAnalytics(content []byte) []byte {
	for _, pattern := range analyticsPatterns {
		content = pattern.ReplaceAll(content, []byte(""))
	}
	return content
}

// stripSessionIDs removes session IDs, request IDs, and CSRF tokens
func stripSessionIDs(content []byte) []byte {
	for _, pattern := range sessionIDPatterns {
		content = pattern.ReplaceAll(content, []byte(""))
	}
	return content
}

// stripVersionParams removes cache-busting version parameters from URLs
func stripVersionParams(content []byte) []byte {
	for _, pattern := range versionParamPatterns {
		content = pattern.ReplaceAll(content, []byte(""))
	}
	return content
}

// collapseWhitespace normalizes whitespace (multiple spaces/newlines to single space)
func collapseWhitespace(content []byte) []byte {
	return whitespacePattern.ReplaceAll(bytes.TrimSpace(content), []byte(" "))
}

// ComputeContentHash computes a hash of the normalized content using the specified algorithm
func ComputeContentHash(content []byte, algorithm string) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("content is empty")
	}

	switch strings.ToLower(algorithm) {
	case "xxhash", "":
		// xxHash is the fastest and default
		hash := xxhash.Sum64(content)
		return fmt.Sprintf("%016x", hash), nil

	case "md5":
		hash := md5.Sum(content)
		return hex.EncodeToString(hash[:]), nil

	case "sha256":
		hash := sha256.Sum256(content)
		return hex.EncodeToString(hash[:]), nil

	default:
		return "", fmt.Errorf("unsupported hash algorithm: %s (supported: xxhash, md5, sha256)", algorithm)
	}
}

// ComputeContentHashWithConfig is a convenience function that normalizes content and computes its hash
func ComputeContentHashWithConfig(html []byte, algorithm string, config *ContentHashConfig) (string, error) {
	normalized, err := NormalizeContent(html, config)
	if err != nil {
		return "", fmt.Errorf("failed to normalize content: %w", err)
	}

	hash, err := ComputeContentHash(normalized, algorithm)
	if err != nil {
		return "", fmt.Errorf("failed to compute hash: %w", err)
	}

	return hash, nil
}
