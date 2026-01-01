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
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestExtractLinkContext(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name: "context from paragraph",
			html: `<p>This is some text with <a href="/link">a link</a> in the middle.</p>`,
			expected: "This is some text with a link in the middle.",
		},
		{
			name: "context from list item",
			html: `<ul><li>Item with <a href="/link">link</a> inside</li></ul>`,
			expected: "Item with link inside",
		},
		{
			name: "context from table cell",
			html: `<table><tr><td>Cell with <a href="/link">link</a> content</td></tr></table>`,
			expected: "Cell with link content",
		},
		{
			name: "context from heading",
			html: `<h2>Heading with <a href="/link">link</a> text</h2>`,
			expected: "Heading with link text",
		},
		{
			name: "context from blockquote",
			html: `<blockquote>Quote with <a href="/link">link</a> inside</blockquote>`,
			expected: "Quote with link inside",
		},
		{
			name: "context from figcaption",
			html: `<figure><img src="img.jpg"><figcaption>Caption with <a href="/link">link</a></figcaption></figure>`,
			expected: "Caption with link",
		},
		{
			name: "fallback to inline parent",
			html: `<div><span>Span with <a href="/link">link</a> text</span></div>`,
			expected: "Span with link text",
		},
		{
			name: "nested semantic parent",
			html: `<div><div><p>Nested paragraph with <a href="/link">link</a> content</p></div></div>`,
			expected: "Nested paragraph with link content",
		},
		{
			name: "multiple whitespace normalization",
			html: `<p>Text   with    multiple    <a href="/link">spaces</a>    here</p>`,
			expected: "Text with multiple spaces here",
		},
		{
			name: "context excludes script tags",
			html: `<p>Text before <script>console.log('hidden');</script> <a href="/link">link</a> after</p>`,
			expected: "Text before link after",
		},
		{
			name: "context excludes style tags",
			html: `<p>Text <style>body { color: red; }</style> with <a href="/link">link</a> here</p>`,
			expected: "Text with link here",
		},
		{
			name: "link in complex nested structure",
			html: `<div class="wrapper"><p>Paragraph with <strong>bold and <a href="/link">link</a></strong> inside</p></div>`,
			expected: "Paragraph with bold and link inside",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock HTMLElement from the HTML string
			doc, err := goquery.NewDocumentFromReader(bytes.NewBufferString(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			// Find the link element
			linkSelection := doc.Find("a[href]").First()
			if linkSelection.Length() == 0 {
				t.Fatal("No link found in HTML")
			}

			// Create HTMLElement
			req := &Request{
				URL: &url.URL{Scheme: "http", Host: "example.com"},
			}
			elem := &HTMLElement{
				DOM:     linkSelection,
				Request: req,
			}

			// Extract context
			context := extractLinkContext(elem)

			// Normalize whitespace for comparison
			context = strings.TrimSpace(context)
			expected := strings.TrimSpace(tt.expected)

			if context != expected {
				t.Errorf("extractLinkContext() = %q, want %q", context, expected)
			}
		})
	}
}

func TestFindSemanticParent(t *testing.T) {
	tests := []struct {
		name          string
		html          string
		expectedTag   string
		shouldBeFound bool
	}{
		{
			name:          "finds paragraph",
			html:          `<div><p><a href="/link">link</a></p></div>`,
			expectedTag:   "p",
			shouldBeFound: true,
		},
		{
			name:          "finds list item",
			html:          `<ul><li><a href="/link">link</a></li></ul>`,
			expectedTag:   "li",
			shouldBeFound: true,
		},
		{
			name:          "finds heading",
			html:          `<h1><a href="/link">link</a></h1>`,
			expectedTag:   "h1",
			shouldBeFound: true,
		},
		{
			name:          "finds table cell",
			html:          `<table><tr><td><a href="/link">link</a></td></tr></table>`,
			expectedTag:   "td",
			shouldBeFound: true,
		},
		{
			name:          "no semantic parent",
			html:          `<div><span><a href="/link">link</a></span></div>`,
			expectedTag:   "",
			shouldBeFound: false,
		},
		{
			name:          "prefers closest semantic parent",
			html:          `<div><blockquote><p><a href="/link">link</a></p></blockquote></div>`,
			expectedTag:   "p",
			shouldBeFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(bytes.NewBufferString(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			linkSelection := doc.Find("a[href]").First()
			if linkSelection.Length() == 0 {
				t.Fatal("No link found in HTML")
			}

			parent := findSemanticParent(linkSelection)

			if tt.shouldBeFound {
				if parent == nil || parent.Length() == 0 {
					t.Error("Expected to find semantic parent, but none found")
					return
				}
				actualTag := goquery.NodeName(parent)
				if actualTag != tt.expectedTag {
					t.Errorf("findSemanticParent() found tag %q, want %q", actualTag, tt.expectedTag)
				}
			} else {
				if parent != nil && parent.Length() > 0 {
					actualTag := goquery.NodeName(parent)
					t.Errorf("Expected no semantic parent, but found %q", actualTag)
				}
			}
		})
	}
}

func TestHasOnlyInlineContent(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected bool
	}{
		{
			name:     "only inline elements",
			html:     `<span>Text with <strong>bold</strong> and <em>italic</em></span>`,
			expected: true,
		},
		{
			name:     "contains block element",
			html:     `<div>Text with <p>paragraph</p></div>`,
			expected: false,
		},
		{
			name:     "only text",
			html:     `<span>Just text</span>`,
			expected: true,
		},
		{
			name:     "contains div",
			html:     `<span>Text <div>block</div></span>`,
			expected: false,
		},
		{
			name:     "mixed inline elements",
			html:     `<span><a href="#">link</a> <code>code</code> <abbr>abbr</abbr></span>`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(bytes.NewBufferString(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			// Get the first element (which should be the outer container)
			selection := doc.Find("body").Children().First()
			if selection.Length() == 0 {
				t.Fatal("No element found in HTML")
			}

			result := hasOnlyInlineContent(selection)
			if result != tt.expected {
				t.Errorf("hasOnlyInlineContent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractTextWithSpacing(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "simple text",
			html:     `<p>Simple text</p>`,
			expected: "Simple text",
		},
		{
			name:     "text with inline elements",
			html:     `<p>Text with <strong>bold</strong> and <em>italic</em></p>`,
			expected: "Text with bold and italic",
		},
		{
			name:     "text with block elements",
			html:     `<div><p>First paragraph</p><p>Second paragraph</p></div>`,
			expected: "First paragraph\n\nSecond paragraph",
		},
		{
			name:     "removes extra whitespace",
			html:     `<p>Text   with    extra    spaces</p>`,
			expected: "Text with extra spaces",
		},
		{
			name:     "excludes script tags",
			html:     `<div>Text <script>alert('hi');</script> more text</div>`,
			expected: "Text more text",
		},
		{
			name:     "excludes style tags",
			html:     `<div>Text <style>body { color: red; }</style> more text</div>`,
			expected: "Text more text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(bytes.NewBufferString(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			selection := doc.Find("body").Children().First()
			if selection.Length() == 0 {
				t.Fatal("No element found in HTML")
			}

			result := extractTextWithSpacing(selection)
			result = strings.TrimSpace(result)
			expected := strings.TrimSpace(tt.expected)

			if result != expected {
				t.Errorf("extractTextWithSpacing() = %q, want %q", result, expected)
			}
		})
	}
}
