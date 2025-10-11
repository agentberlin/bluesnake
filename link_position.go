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

// extractLinkPosition determines the semantic position of a link in the page structure.
// It analyzes the DOM hierarchy and element attributes to classify the link's location.
// Returns both a position classification and the DOM path for debugging.
func extractLinkPosition(elem *HTMLElement) (position string, domPath string) {
	domPath = buildDOMPath(elem.DOM)
	position = classifyLinkPosition(elem.DOM, domPath)
	return position, domPath
}

// buildDOMPath constructs a simplified DOM path from the link element up to the body.
// Returns a path like "body > main > article > p > a"
// Includes important attributes like id, class, and role for better classification.
func buildDOMPath(selection *goquery.Selection) string {
	var pathParts []string

	current := selection
	for current.Length() > 0 {
		nodeName := goquery.NodeName(current)

		// Stop at html or body tag to keep paths manageable
		if nodeName == "html" {
			break
		}

		// Build element descriptor with tag name and important attributes
		descriptor := nodeName

		// Add role attribute if present (important for semantic identification)
		if role, exists := current.Attr("role"); exists && role != "" {
			descriptor += `[role="` + role + `"]`
		}

		// Add id if present (useful for debugging)
		if id, exists := current.Attr("id"); exists && id != "" {
			descriptor += "#" + id
		}

		// Add first class if present (helps identify navigation, menus, etc.)
		if class, exists := current.Attr("class"); exists && class != "" {
			classes := strings.Fields(class)
			if len(classes) > 0 {
				descriptor += "." + classes[0]
			}
		}

		pathParts = append([]string{descriptor}, pathParts...)

		current = current.Parent()
	}

	return strings.Join(pathParts, " > ")
}

// classifyLinkPosition classifies a link's position based on its DOM path and parent elements.
// This implements a heuristic approach that checks for semantic HTML5 elements,
// ARIA roles, and common class/id patterns.
func classifyLinkPosition(selection *goquery.Selection, domPath string) string {
	domPathLower := strings.ToLower(domPath)

	// Priority 1: Check ancestors for semantic HTML5 elements and ARIA roles
	// Walk up the DOM tree to find semantic containers
	current := selection.Parent()
	for current.Length() > 0 {
		nodeName := goquery.NodeName(current)
		role, _ := current.Attr("role")
		class, _ := current.Attr("class")
		id, _ := current.Attr("id")

		// Combine attributes for pattern matching
		attributes := strings.ToLower(nodeName + " " + role + " " + class + " " + id)

		// Check for content areas (highest priority)
		if nodeName == "main" || nodeName == "article" || role == "main" || role == "article" {
			return "content"
		}

		// Check for breadcrumbs BEFORE navigation (more specific)
		if strings.Contains(attributes, "breadcrumb") {
			return "breadcrumbs"
		}

		// Check for pagination BEFORE navigation (more specific)
		if strings.Contains(attributes, "pagination") || strings.Contains(attributes, "pager") ||
			strings.Contains(attributes, "page-number") {
			return "pagination"
		}

		// Check for navigation
		if nodeName == "nav" || role == "navigation" || strings.Contains(attributes, "nav") ||
			strings.Contains(attributes, "menu") || strings.Contains(attributes, "navbar") ||
			strings.Contains(attributes, "megamenu") {
			return "navigation"
		}

		// Check for header
		if nodeName == "header" || role == "banner" || strings.Contains(attributes, "header") ||
			strings.Contains(attributes, "masthead") || strings.Contains(attributes, "topbar") {
			return "header"
		}

		// Check for footer
		if nodeName == "footer" || role == "contentinfo" || strings.Contains(attributes, "footer") {
			return "footer"
		}

		// Check for sidebar/aside
		if nodeName == "aside" || role == "complementary" || strings.Contains(attributes, "sidebar") ||
			strings.Contains(attributes, "aside") {
			return "sidebar"
		}

		current = current.Parent()
	}

	// Priority 2: Fallback to DOM path analysis if no semantic parent found
	// Check domPath for common patterns

	// Breadcrumbs patterns
	if strings.Contains(domPathLower, "breadcrumb") {
		return "breadcrumbs"
	}

	// Pagination patterns
	if strings.Contains(domPathLower, "pagination") || strings.Contains(domPathLower, "pager") ||
		strings.Contains(domPathLower, "page-number") {
		return "pagination"
	}

	// Navigation patterns
	if strings.Contains(domPathLower, "nav") || strings.Contains(domPathLower, "menu") {
		return "navigation"
	}

	// Header patterns
	if strings.Contains(domPathLower, "header") || strings.Contains(domPathLower, "masthead") ||
		strings.Contains(domPathLower, "topbar") {
		return "header"
	}

	// Footer patterns
	if strings.Contains(domPathLower, "footer") {
		return "footer"
	}

	// Sidebar patterns
	if strings.Contains(domPathLower, "sidebar") || strings.Contains(domPathLower, "aside") {
		return "sidebar"
	}

	// Content patterns (main, article)
	if strings.Contains(domPathLower, "main") || strings.Contains(domPathLower, "article") ||
		strings.Contains(domPathLower, `role="main"`) {
		return "content"
	}

	// Default: unknown
	return "unknown"
}

// isContentLink returns true if the link is classified as being in a content area.
// This is a helper function for filtering content-only links.
func isContentLink(position string) bool {
	return position == "content"
}

// isBoilerplateLink returns true if the link is classified as being in a boilerplate area.
// This is a helper function for filtering boilerplate links.
func isBoilerplateLink(position string) bool {
	boilerplatePositions := []string{
		"navigation",
		"header",
		"footer",
		"sidebar",
		"breadcrumbs",
		"pagination",
	}

	for _, bp := range boilerplatePositions {
		if position == bp {
			return true
		}
	}
	return false
}
