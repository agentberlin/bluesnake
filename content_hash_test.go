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
	"strings"
	"testing"
)

// TestNormalizeContent_ExcludeTags tests that specified tags are properly excluded
func TestNormalizeContent_ExcludeTags(t *testing.T) {
	html := []byte(`
		<html>
			<head><title>Test</title></head>
			<body>
				<nav>Navigation</nav>
				<main>Main content</main>
				<footer>Footer content</footer>
				<script>console.log("test");</script>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		ExcludeTags:        []string{"script", "nav", "footer"},
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that excluded tags are not present
	if strings.Contains(normalizedStr, "Navigation") {
		t.Error("Expected nav content to be excluded")
	}
	if strings.Contains(normalizedStr, "Footer content") {
		t.Error("Expected footer content to be excluded")
	}
	if strings.Contains(normalizedStr, "console.log") {
		t.Error("Expected script content to be excluded")
	}

	// Check that main content is still present
	if !strings.Contains(normalizedStr, "Main content") {
		t.Error("Expected main content to be present")
	}
}

// TestNormalizeContent_IncludeOnlyTags tests that only specified tags are included
func TestNormalizeContent_IncludeOnlyTags(t *testing.T) {
	html := []byte(`
		<html>
			<head><title>Test</title></head>
			<body>
				<nav>Navigation</nav>
				<main>Main content</main>
				<article>Article content</article>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		IncludeOnlyTags:    []string{"main", "article"},
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that only specified tags are present
	if !strings.Contains(normalizedStr, "Main content") {
		t.Error("Expected main content to be present")
	}
	if !strings.Contains(normalizedStr, "Article content") {
		t.Error("Expected article content to be present")
	}

	// Navigation should not be present
	if strings.Contains(normalizedStr, "Navigation") {
		t.Error("Expected nav content to be excluded")
	}
}

// TestNormalizeContent_StripComments tests HTML comment removal
func TestNormalizeContent_StripComments(t *testing.T) {
	html := []byte(`
		<html>
			<body>
				<!-- This is a comment -->
				<p>Content</p>
				<!-- Another comment -->
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		StripComments:      true,
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that comments are removed
	if strings.Contains(normalizedStr, "This is a comment") {
		t.Error("Expected comments to be removed")
	}
	if strings.Contains(normalizedStr, "<!--") {
		t.Error("Expected comment markers to be removed")
	}

	// Content should still be present
	if !strings.Contains(normalizedStr, "Content") {
		t.Error("Expected content to be present")
	}
}

// TestNormalizeContent_StripTimestamps tests timestamp removal
func TestNormalizeContent_StripTimestamps(t *testing.T) {
	html := []byte(`
		<html>
			<body>
				<p>Posted on 2025-10-11T14:30:00Z</p>
				<p>Updated 5 minutes ago</p>
				<p>Date: Jan 15, 2025 3:45 PM</p>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		StripTimestamps:    true,
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that timestamps are replaced
	if strings.Contains(normalizedStr, "2025-10-11T14:30:00Z") {
		t.Error("Expected ISO timestamp to be replaced")
	}
	if strings.Contains(normalizedStr, "5 minutes ago") {
		t.Error("Expected relative time to be replaced")
	}
	if strings.Contains(normalizedStr, "Jan 15, 2025") {
		t.Error("Expected date to be replaced")
	}

	// Should contain placeholder or be removed
	// Static text should remain
	if !strings.Contains(normalizedStr, "Posted on") {
		t.Error("Expected 'Posted on' text to remain")
	}
}

// TestNormalizeContent_StripAnalytics tests analytics code removal
func TestNormalizeContent_StripAnalytics(t *testing.T) {
	html := []byte(`
		<html>
			<body>
				<p>Content</p>
				<script src="https://www.google-analytics.com/analytics.js"></script>
				<script>
					gtag('config', 'GA_TRACKING_ID');
					fbq('track', 'PageView');
				</script>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		ExcludeTags:        []string{"script"},
		StripAnalytics:     true,
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that analytics code is removed
	if strings.Contains(normalizedStr, "google-analytics") {
		t.Error("Expected Google Analytics to be removed")
	}
	if strings.Contains(normalizedStr, "gtag") {
		t.Error("Expected gtag calls to be removed")
	}
	if strings.Contains(normalizedStr, "fbq") {
		t.Error("Expected Facebook Pixel to be removed")
	}

	// Content should still be present
	if !strings.Contains(normalizedStr, "Content") {
		t.Error("Expected content to be present")
	}
}

// TestNormalizeContent_CollapseWhitespace tests whitespace normalization
func TestNormalizeContent_CollapseWhitespace(t *testing.T) {
	html := []byte(`
		<html>
			<body>
				<p>Text    with    multiple     spaces</p>
				<p>Text

				with

				newlines</p>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		CollapseWhitespace: true,
	}

	normalized, err := NormalizeContent(html, config)
	if err != nil {
		t.Fatalf("NormalizeContent failed: %v", err)
	}

	normalizedStr := string(normalized)

	// Check that multiple spaces are collapsed
	if strings.Contains(normalizedStr, "    ") {
		t.Error("Expected multiple spaces to be collapsed")
	}

	// Should contain single spaces
	if !strings.Contains(normalizedStr, "Text with multiple spaces") {
		t.Error("Expected spaces to be normalized to single space")
	}
}

// TestComputeContentHash_XXHash tests xxHash algorithm
func TestComputeContentHash_XXHash(t *testing.T) {
	content := []byte("Test content for hashing")

	hash1, err := ComputeContentHash(content, "xxhash")
	if err != nil {
		t.Fatalf("ComputeContentHash failed: %v", err)
	}

	hash2, err := ComputeContentHash(content, "xxhash")
	if err != nil {
		t.Fatalf("ComputeContentHash failed: %v", err)
	}

	// Same content should produce same hash
	if hash1 != hash2 {
		t.Errorf("Expected same hash for same content, got %s and %s", hash1, hash2)
	}

	// Different content should produce different hash
	differentContent := []byte("Different content")
	hash3, err := ComputeContentHash(differentContent, "xxhash")
	if err != nil {
		t.Fatalf("ComputeContentHash failed: %v", err)
	}

	if hash1 == hash3 {
		t.Error("Expected different hash for different content")
	}
}

// TestComputeContentHash_MD5 tests MD5 algorithm
func TestComputeContentHash_MD5(t *testing.T) {
	content := []byte("Test content for MD5")

	hash, err := ComputeContentHash(content, "md5")
	if err != nil {
		t.Fatalf("ComputeContentHash failed: %v", err)
	}

	// MD5 hash should be 32 hex characters
	if len(hash) != 32 {
		t.Errorf("Expected MD5 hash to be 32 characters, got %d", len(hash))
	}
}

// TestComputeContentHash_SHA256 tests SHA256 algorithm
func TestComputeContentHash_SHA256(t *testing.T) {
	content := []byte("Test content for SHA256")

	hash, err := ComputeContentHash(content, "sha256")
	if err != nil {
		t.Fatalf("ComputeContentHash failed: %v", err)
	}

	// SHA256 hash should be 64 hex characters
	if len(hash) != 64 {
		t.Errorf("Expected SHA256 hash to be 64 characters, got %d", len(hash))
	}
}

// TestComputeContentHash_UnsupportedAlgorithm tests error handling for unsupported algorithm
func TestComputeContentHash_UnsupportedAlgorithm(t *testing.T) {
	content := []byte("Test content")

	_, err := ComputeContentHash(content, "unsupported")
	if err == nil {
		t.Error("Expected error for unsupported algorithm")
	}
}

// TestComputeContentHash_EmptyContent tests error handling for empty content
func TestComputeContentHash_EmptyContent(t *testing.T) {
	_, err := ComputeContentHash([]byte{}, "xxhash")
	if err == nil {
		t.Error("Expected error for empty content")
	}
}

// TestComputeContentHashWithConfig tests the convenience function
func TestComputeContentHashWithConfig(t *testing.T) {
	html := []byte(`
		<html>
			<body>
				<nav>Navigation</nav>
				<main>Main content</main>
				<footer>Footer</footer>
				<script>analytics.track();</script>
			</body>
		</html>
	`)

	config := &ContentHashConfig{
		ExcludeTags:        []string{"script", "nav", "footer"},
		CollapseWhitespace: true,
	}

	hash1, err := ComputeContentHashWithConfig(html, "xxhash", config)
	if err != nil {
		t.Fatalf("ComputeContentHashWithConfig failed: %v", err)
	}

	// Same HTML should produce same hash
	hash2, err := ComputeContentHashWithConfig(html, "xxhash", config)
	if err != nil {
		t.Fatalf("ComputeContentHashWithConfig failed: %v", err)
	}

	if hash1 != hash2 {
		t.Error("Expected same hash for same HTML")
	}

	// HTML with only cosmetic differences (different nav text) should produce same hash
	htmlWithDifferentNav := []byte(`
		<html>
			<body>
				<nav>Different Navigation</nav>
				<main>Main content</main>
				<footer>Different Footer</footer>
				<script>analytics.track('different');</script>
			</body>
		</html>
	`)

	hash3, err := ComputeContentHashWithConfig(htmlWithDifferentNav, "xxhash", config)
	if err != nil {
		t.Fatalf("ComputeContentHashWithConfig failed: %v", err)
	}

	// Should be same because nav, footer, and script are excluded
	if hash1 != hash3 {
		t.Error("Expected same hash when excluded elements differ")
	}

	// HTML with different main content should produce different hash
	htmlWithDifferentMain := []byte(`
		<html>
			<body>
				<nav>Navigation</nav>
				<main>Different main content</main>
				<footer>Footer</footer>
			</body>
		</html>
	`)

	hash4, err := ComputeContentHashWithConfig(htmlWithDifferentMain, "xxhash", config)
	if err != nil {
		t.Fatalf("ComputeContentHashWithConfig failed: %v", err)
	}

	if hash1 == hash4 {
		t.Error("Expected different hash when main content differs")
	}
}

// TestStripSessionIDs tests session ID and CSRF token removal
func TestStripSessionIDs(t *testing.T) {
	content := []byte(`
		session-id: abc123def456
		csrf-token: "ZGVmNDU2YWJjMTIz"
		_token="aGVsbG93b3JsZA=="
	`)

	stripped := stripSessionIDs(content)
	strippedStr := string(stripped)

	// Session IDs and tokens should be removed
	if strings.Contains(strippedStr, "abc123def456") {
		t.Error("Expected session ID to be removed")
	}
	if strings.Contains(strippedStr, "ZGVmNDU2YWJjMTIz") {
		t.Error("Expected CSRF token to be removed")
	}
}

// TestStripVersionParams tests cache-busting parameter removal
func TestStripVersionParams(t *testing.T) {
	content := []byte(`
		<link rel="stylesheet" href="style.css?v=12345">
		<script src="app.js?ver=abcdef"></script>
		<img src="logo.png?_=1234567890">
	`)

	stripped := stripVersionParams(content)
	strippedStr := string(stripped)

	// Version parameters should be removed
	if strings.Contains(strippedStr, "?v=") {
		t.Error("Expected ?v= parameter to be removed")
	}
	if strings.Contains(strippedStr, "?ver=") {
		t.Error("Expected ?ver= parameter to be removed")
	}
	if strings.Contains(strippedStr, "?_=") {
		t.Error("Expected ?_= parameter to be removed")
	}

	// File names should remain
	if !strings.Contains(strippedStr, "style.css") {
		t.Error("Expected file name to remain")
	}
}
