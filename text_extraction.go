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
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// extractAllText extracts all visible text from HTML, removing all tags.
// This includes navigation, headers, footers, and all content areas.
// Normalizes whitespace (collapses multiple spaces/newlines).
func extractAllText(htmlBody []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlBody))
	if err != nil {
		return ""
	}

	// Remove script and style elements as they're not visible text
	doc.Find("script, style").Remove()

	// Get all text
	text := doc.Text()

	// Normalize whitespace: collapse multiple spaces/newlines to single space
	text = normalizeWhitespace(text)

	return strings.TrimSpace(text)
}

// extractMainContentText extracts text from the main content area only.
// Excludes navigation, headers, footers, and sidebars.
// Uses HTML5 semantic elements to identify content.
func extractMainContentText(htmlBody []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlBody))
	if err != nil {
		return ""
	}

	// Remove non-content elements
	doc.Find("script, style, nav, header, footer, aside, .sidebar, .navigation").Remove()

	// Try to find semantic content elements in order of preference
	var contentSelection *goquery.Selection

	// 1. Look for <article> tag
	if article := doc.Find("article").First(); article.Length() > 0 {
		contentSelection = article
	} else if main := doc.Find("main").First(); main.Length() > 0 {
		// 2. Look for <main> tag
		contentSelection = main
	} else if roleMain := doc.Find("[role='main']").First(); roleMain.Length() > 0 {
		// 3. Look for role="main" attribute
		contentSelection = roleMain
	} else {
		// 4. Fallback: use body (with nav/header/footer already removed)
		contentSelection = doc.Find("body")
	}

	if contentSelection == nil || contentSelection.Length() == 0 {
		return ""
	}

	// Use extractTextWithSpacing for proper spacing between block elements
	return extractTextWithSpacing(contentSelection)
}

// normalizeWhitespace collapses multiple consecutive whitespace characters
// (spaces, tabs, newlines) into a single space.
func normalizeWhitespace(text string) string {
	// Split by any whitespace and rejoin with single spaces
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}
