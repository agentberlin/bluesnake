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

// Default filter chain for content extraction
// Uses GoOse-inspired filters for noise removal
var defaultContentFilters = NewFilterChain(
	NewNoisePatternFilter(),
	NewNavigationTextFilter(),
	NewLinkDensityFilter(),
)

// Default stopwords scorer for content detection
var defaultStopwordsScorer = NewStopwordsScorer()

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
//
// Strategy:
// 1. Remove script/style/noscript
// 2. Apply GoOse-inspired filters (noise patterns, nav text, link density)
// 3. Try HTML5 semantic elements (article, main, [role='main'])
// 4. If no semantic elements, use stopwords-based scoring to find best content node
// 5. Fall back to body if nothing else works
func extractMainContentText(htmlBody []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlBody))
	if err != nil {
		return ""
	}

	// Step 1: Remove non-visible elements
	doc.Find("script, style, noscript").Remove()

	// Step 2: Apply GoOse-inspired filters to clean noise
	doc = defaultContentFilters.Apply(doc)

	// Step 3: Try semantic elements first (most reliable when present)
	var contentSelection *goquery.Selection

	if article := doc.Find("article").First(); article.Length() > 0 {
		contentSelection = article
	} else if main := doc.Find("main").First(); main.Length() > 0 {
		contentSelection = main
	} else if roleMain := doc.Find("[role='main']").First(); roleMain.Length() > 0 {
		contentSelection = roleMain
	}

	// Step 4: If no semantic elements, use stopwords-based scoring
	if contentSelection == nil {
		contentSelection = findBestContentNode(doc)
	}

	// Step 5: Fall back to body
	if contentSelection == nil || contentSelection.Length() == 0 {
		contentSelection = doc.Find("body")
	}

	if contentSelection == nil || contentSelection.Length() == 0 {
		return ""
	}

	// Use extractTextWithSpacing for proper spacing between block elements
	return extractTextWithSpacing(contentSelection)
}

// findBestContentNode finds the DOM node most likely to contain main content
// using stopwords-based scoring (ported from GoOse's CalculateBestNode)
func findBestContentNode(doc *goquery.Document) *goquery.Selection {
	parentScores := make(map[*goquery.Selection]int)
	linkDensityFilter := NewLinkDensityFilter()

	// Score all paragraphs and propagate scores to parents
	doc.Find("p, pre, td").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		stopwords := defaultStopwordsScorer.CountStopwords(text)

		// Skip if not enough stopwords (not real content)
		if stopwords < 2 {
			return
		}

		// Skip if high link density (likely navigation)
		if linkDensityFilter.isHighLinkDensity(s) {
			return
		}

		// Calculate score based on stopwords + text length
		score := defaultStopwordsScorer.ScoreText(text)

		// Add score to parent (full score)
		parent := s.Parent()
		if parent.Length() > 0 {
			parentScores[parent] += score
		}

		// Add score to grandparent (half score) - gravity scoring from GoOse
		grandparent := parent.Parent()
		if grandparent.Length() > 0 {
			parentScores[grandparent] += score / 2
		}
	})

	// Find the highest scoring node
	var bestNode *goquery.Selection
	bestScore := 0
	for node, score := range parentScores {
		if score > bestScore {
			bestScore = score
			bestNode = node
		}
	}

	return bestNode
}

// normalizeWhitespace collapses multiple consecutive whitespace characters
// (spaces, tabs, newlines) into a single space.
func normalizeWhitespace(text string) string {
	// Split by any whitespace and rejoin with single spaces
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}
