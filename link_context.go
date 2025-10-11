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

	"github.com/PuerkitoBio/goquery"
)

// extractLinkContext extracts the contextual text surrounding a link element.
// It prioritizes semantic parent elements (p, li, td, h1-h6, etc.) and falls back
// to the immediate parent if it contains only inline content.
func extractLinkContext(elem *HTMLElement) string {
	selection := elem.DOM

	// Try to find a semantic parent element
	semanticParent := findSemanticParent(selection)
	if semanticParent != nil && semanticParent.Length() > 0 {
		return extractTextWithSpacing(semanticParent)
	}

	// Fallback: check if immediate parent has only inline content
	parent := selection.Parent()
	if parent.Length() > 0 && hasOnlyInlineContent(parent) {
		return extractTextWithSpacing(parent)
	}

	// Last resort: return empty if no suitable context found
	return ""
}

// findSemanticParent finds the closest semantic parent element that provides meaningful context.
// Semantic elements include: p, li, td, th, h1-h6, blockquote, figcaption.
func findSemanticParent(selection *goquery.Selection) *goquery.Selection {
	semanticTags := []string{"p", "li", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "figcaption"}

	current := selection.Parent()
	for current.Length() > 0 {
		nodeName := goquery.NodeName(current)
		for _, tag := range semanticTags {
			if nodeName == tag {
				return current
			}
		}
		current = current.Parent()
	}

	return nil
}

// hasOnlyInlineContent checks if an element contains only inline elements and text.
// Returns true if all children are inline elements (span, a, strong, em, etc.).
func hasOnlyInlineContent(selection *goquery.Selection) bool {
	inlineElements := map[string]bool{
		"a": true, "abbr": true, "b": true, "bdi": true, "bdo": true,
		"br": true, "cite": true, "code": true, "data": true, "dfn": true,
		"em": true, "i": true, "kbd": true, "mark": true, "q": true,
		"s": true, "samp": true, "small": true, "span": true, "strong": true,
		"sub": true, "sup": true, "time": true, "u": true, "var": true,
		"wbr": true, "#text": true,
	}

	allInline := true
	selection.Contents().Each(func(i int, child *goquery.Selection) {
		nodeName := goquery.NodeName(child)
		if !inlineElements[nodeName] {
			allInline = false
		}
	})

	return allInline
}

// extractTextWithSpacing extracts text from a selection with proper spacing between elements.
// Adds spacing between block elements to ensure readability.
func extractTextWithSpacing(selection *goquery.Selection) string {
	var textParts []string

	// Remove elements that shouldn't be included in context
	cloned := selection.Clone()
	cloned.Find("script, style, nav, header, footer").Remove()

	// Extract text with spacing
	var extractRecursive func(*goquery.Selection)
	extractRecursive = func(sel *goquery.Selection) {
		sel.Contents().Each(func(i int, child *goquery.Selection) {
			nodeName := goquery.NodeName(child)

			// Handle text nodes
			if nodeName == "#text" {
				text := child.Text()
				if trimmed := strings.TrimSpace(text); trimmed != "" {
					textParts = append(textParts, trimmed)
				}
				return
			}

			// Skip script and style elements
			if nodeName == "script" || nodeName == "style" {
				return
			}

			// Recursively extract from child elements
			if child.Length() > 0 {
				extractRecursive(child)

				// Add spacing after block elements
				if isBlockElement(nodeName) {
					textParts = append(textParts, " ")
				}
			}
		})
	}

	extractRecursive(cloned)

	// Join and normalize whitespace
	text := strings.Join(textParts, " ")
	return normalizeWhitespace(text)
}

// isBlockElement checks if an HTML element is a block-level element.
func isBlockElement(nodeName string) bool {
	blockElements := map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true,
		"details": true, "dialog": true, "dd": true, "div": true, "dl": true,
		"dt": true, "fieldset": true, "figcaption": true, "figure": true,
		"footer": true, "form": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "header": true, "hgroup": true,
		"hr": true, "li": true, "main": true, "nav": true, "ol": true,
		"p": true, "pre": true, "section": true, "table": true, "ul": true,
	}

	return blockElements[nodeName]
}
