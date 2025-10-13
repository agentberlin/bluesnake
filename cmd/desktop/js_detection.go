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

package main

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// detectJSRenderingNeed performs heuristic-based detection to determine if a website
// needs JavaScript rendering. It focuses on detecting CLIENT-SIDE rendering by analyzing
// actual content presence, not just framework usage (since Next.js/Nuxt can do SSR).
func detectJSRenderingNeed(url string) (bool, error) {
	// Normalize URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow redirects
			return nil
		},
	}

	// Fetch the HTML
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}

	// Set a realistic user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		// If we can't fetch the page, default to no JS rendering
		return false, err
	}
	defer resp.Body.Close()

	// Only analyze HTML responses
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return false, nil
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	html := string(body)
	htmlLower := strings.ToLower(html)

	// Extract body content
	bodyStartIdx := strings.Index(htmlLower, "<body")
	bodyEndIdx := strings.LastIndex(htmlLower, "</body>")

	if bodyStartIdx < 0 || bodyEndIdx <= bodyStartIdx {
		// No body tag found, likely not HTML or malformed
		return false, nil
	}

	// Move past the opening <body> tag
	bodyOpenEnd := strings.Index(htmlLower[bodyStartIdx:], ">")
	if bodyOpenEnd < 0 {
		return false, nil
	}
	bodyStartIdx = bodyStartIdx + bodyOpenEnd + 1

	bodyContent := html[bodyStartIdx:bodyEndIdx]
	bodyContentLower := strings.ToLower(bodyContent)

	// Calculate evidence score (higher = more likely needs JS rendering)
	score := 0

	// 1. Extract visible text content (remove scripts, styles, and tags)
	visibleContent := extractVisibleText(bodyContent)
	visibleContentLength := len(strings.TrimSpace(visibleContent))

	// 2. Count meaningful content
	// If body has very little actual text content (< 200 chars), strong indicator of CSR
	if visibleContentLength < 200 {
		score += 5 // Strong indicator
	} else if visibleContentLength < 500 {
		score += 2 // Moderate indicator
	}

	// 3. Check for empty/near-empty container patterns typical of CSR
	emptyContainerPatterns := []string{
		`<div id="root"></div>`,
		`<div id="root" ></div>`,
		`<div id="app"></div>`,
		`<div id="app" ></div>`,
		`<div id="__next"></div>`,
		`<div id="__next" ></div>`,
		`<div id="___gatsby"></div>`,
		`<div id="___gatsby" ></div>`,
	}

	for _, pattern := range emptyContainerPatterns {
		if strings.Contains(bodyContentLower, strings.ToLower(pattern)) {
			score += 4 // Very strong indicator
			break
		}
	}

	// 4. Check for "loading" indicators (common in CSR before JS loads)
	loadingIndicators := []string{
		"loading...",
		"please wait",
		"loading content",
		"initializing",
		"loading application",
	}

	for _, indicator := range loadingIndicators {
		if strings.Contains(strings.ToLower(visibleContent), indicator) {
			score += 3
			break
		}
	}

	// 5. Check script-to-content ratio
	// Count <script> tags in body
	scriptCount := strings.Count(bodyContentLower, "<script")

	// High script count + low content = likely CSR
	if scriptCount > 5 && visibleContentLength < 500 {
		score += 2
	} else if scriptCount > 10 && visibleContentLength < 1000 {
		score += 2
	}

	// 6. Check for SPA app shells (container with classes/ids but no content)
	// Look for divs with app-related IDs/classes that have no substantial content
	appContainerRegex := regexp.MustCompile(`<div[^>]*(id|class)=["']?(root|app|main|__next|___gatsby|__nuxt)[^>]*>`)
	if appContainerRegex.MatchString(bodyContentLower) {
		// Found an app container - check if it's mostly empty
		if visibleContentLength < 300 {
			score += 3
		}
	}

	// 7. Check for heavy JavaScript with minimal content
	// Look for large script bundles typical of SPAs
	bundleIndicators := 0
	if strings.Contains(bodyContentLower, "bundle.js") {
		bundleIndicators++
	}
	if strings.Contains(bodyContentLower, "vendor.js") || strings.Contains(bodyContentLower, "vendors.js") {
		bundleIndicators++
	}
	if strings.Contains(bodyContentLower, "runtime.js") || strings.Contains(bodyContentLower, "runtime-main.js") {
		bundleIndicators++
	}
	if strings.Contains(bodyContentLower, "chunk.js") {
		bundleIndicators++
	}

	// Multiple bundle files + sparse content = likely CSR
	if bundleIndicators >= 2 && visibleContentLength < 500 {
		score += 2
	}

	// 8. Check for noscript warnings (indicates page needs JS)
	noscriptContent := extractNoscriptContent(bodyContent)
	noscriptIndicators := []string{
		"enable javascript",
		"requires javascript",
		"javascript is disabled",
		"javascript to run",
		"without javascript",
	}

	for _, indicator := range noscriptIndicators {
		if strings.Contains(strings.ToLower(noscriptContent), indicator) {
			score += 3
			break
		}
	}

	// Decision threshold: if score >= 7, enable JS rendering
	// This is conservative - we want clear evidence of CSR before enabling JS
	needsJSRendering := score >= 7

	return needsJSRendering, nil
}

// extractVisibleText removes script, style, and HTML tags to get approximate visible text
func extractVisibleText(html string) string {
	content := html

	// Remove script tags and their content
	for {
		scriptStart := strings.Index(strings.ToLower(content), "<script")
		if scriptStart == -1 {
			break
		}
		scriptEnd := strings.Index(strings.ToLower(content[scriptStart:]), "</script>")
		if scriptEnd == -1 {
			// Unclosed script tag, remove from start to end
			content = content[:scriptStart]
			break
		}
		content = content[:scriptStart] + content[scriptStart+scriptEnd+9:]
	}

	// Remove style tags and their content
	for {
		styleStart := strings.Index(strings.ToLower(content), "<style")
		if styleStart == -1 {
			break
		}
		styleEnd := strings.Index(strings.ToLower(content[styleStart:]), "</style>")
		if styleEnd == -1 {
			// Unclosed style tag, remove from start to end
			content = content[:styleStart]
			break
		}
		content = content[:styleStart] + content[styleStart+styleEnd+8:]
	}

	// Remove noscript tags (but not their content - we check that separately)
	content = regexp.MustCompile(`(?i)<noscript[^>]*>`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?i)</noscript>`).ReplaceAllString(content, "")

	// Remove HTML comments
	content = regexp.MustCompile(`<!--.*?-->`).ReplaceAllString(content, "")

	// Remove all remaining HTML tags
	content = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(content, " ")

	// Decode common HTML entities
	content = strings.ReplaceAll(content, "&nbsp;", " ")
	content = strings.ReplaceAll(content, "&lt;", "<")
	content = strings.ReplaceAll(content, "&gt;", ">")
	content = strings.ReplaceAll(content, "&amp;", "&")
	content = strings.ReplaceAll(content, "&quot;", "\"")

	// Normalize whitespace
	content = regexp.MustCompile(`\s+`).ReplaceAllString(content, " ")

	return strings.TrimSpace(content)
}

// extractNoscriptContent extracts content from noscript tags
func extractNoscriptContent(html string) string {
	noscriptRegex := regexp.MustCompile(`(?i)<noscript[^>]*>(.*?)</noscript>`)
	matches := noscriptRegex.FindAllStringSubmatch(html, -1)

	var content strings.Builder
	for _, match := range matches {
		if len(match) > 1 {
			content.WriteString(match[1])
			content.WriteString(" ")
		}
	}

	return content.String()
}
