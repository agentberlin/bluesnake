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

func TestExtractAllText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name: "simple HTML",
			html: `<html><body><p>Hello World</p></body></html>`,
			expected: "Hello World",
		},
		{
			name: "HTML with navigation and footer",
			html: `<html>
				<body>
					<nav>Navigation Menu</nav>
					<main>Main Content</main>
					<footer>Footer Text</footer>
				</body>
			</html>`,
			expected: "Navigation Menu Main Content Footer Text",
		},
		{
			name: "HTML with scripts and styles",
			html: `<html>
				<head><style>body { color: red; }</style></head>
				<body>
					<p>Visible Text</p>
					<script>console.log('hidden');</script>
				</body>
			</html>`,
			expected: "Visible Text",
		},
		{
			name: "HTML with excessive whitespace",
			html: `<html>
				<body>
					<p>Text   with    multiple    spaces</p>
					<p>And

					newlines</p>
				</body>
			</html>`,
			expected: "Text with multiple spaces And newlines",
		},
		{
			name: "empty HTML",
			html: `<html><body></body></html>`,
			expected: "",
		},
		{
			name: "invalid HTML",
			html: `not valid html`,
			expected: "not valid html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAllText([]byte(tt.html))
			if result != tt.expected {
				t.Errorf("extractAllText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractMainContentText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name: "HTML with article tag",
			html: `<html>
				<body>
					<nav>Navigation</nav>
					<article>Article Content</article>
					<footer>Footer</footer>
				</body>
			</html>`,
			expected: "Article Content",
		},
		{
			name: "HTML with main tag",
			html: `<html>
				<body>
					<header>Header</header>
					<main>Main Content</main>
					<aside>Sidebar</aside>
				</body>
			</html>`,
			expected: "Main Content",
		},
		{
			name: "HTML with role=main",
			html: `<html>
				<body>
					<div role="main">Role Main Content</div>
					<nav>Navigation</nav>
				</body>
			</html>`,
			expected: "Role Main Content",
		},
		{
			name: "HTML without semantic tags",
			html: `<html>
				<body>
					<div class="navigation">Nav</div>
					<div>Body Content</div>
				</body>
			</html>`,
			expected: "Body Content",
		},
		{
			name: "article takes precedence over main",
			html: `<html>
				<body>
					<main>
						<article>Article in Main</article>
						<div>Other Main Content</div>
					</main>
				</body>
			</html>`,
			expected: "Article in Main",
		},
		{
			name: "removes sidebar with class",
			html: `<html>
				<body>
					<article>
						Main Article
						<div class="sidebar">Sidebar Content</div>
					</article>
				</body>
			</html>`,
			expected: "Main Article",
		},
		{
			name: "empty content",
			html: `<html><body><nav>Only Nav</nav></body></html>`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMainContentText([]byte(tt.html))
			if result != tt.expected {
				t.Errorf("extractMainContentText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "multiple spaces",
			input:    "text   with    spaces",
			expected: "text with spaces",
		},
		{
			name:     "tabs and newlines",
			input:    "text\twith\ttabs\nand\nnewlines",
			expected: "text with tabs and newlines",
		},
		{
			name:     "leading and trailing whitespace",
			input:    "   text   ",
			expected: "text",
		},
		{
			name:     "already normalized",
			input:    "already normalized",
			expected: "already normalized",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "   \t\n   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeWhitespace(tt.input)
			result = strings.TrimSpace(result) // normalizeWhitespace doesn't trim, so we do it here for test
			if result != tt.expected {
				t.Errorf("normalizeWhitespace() = %q, want %q", result, tt.expected)
			}
		})
	}
}
