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

	"github.com/PuerkitoBio/goquery"
)

func docFromHTML(html string) *goquery.Document {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	return doc
}

func TestNoisePatternFilter(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		shouldContain  string
		shouldNotContain string
	}{
		{
			name: "removes sidebar",
			html: `<html><body>
				<article>Main content here</article>
				<div class="sidebar">Sidebar stuff</div>
			</body></html>`,
			shouldContain:    "Main content here",
			shouldNotContain: "Sidebar stuff",
		},
		{
			name: "removes footer",
			html: `<html><body>
				<article>Article content</article>
				<div id="footer">Footer links</div>
			</body></html>`,
			shouldContain:    "Article content",
			shouldNotContain: "Footer links",
		},
		{
			name: "removes navigation",
			html: `<html><body>
				<div class="navigation">Nav links</div>
				<main>Main stuff</main>
			</body></html>`,
			shouldContain:    "Main stuff",
			shouldNotContain: "Nav links",
		},
		{
			name: "preserves article content",
			html: `<html><body>
				<div class="article-content">This is the article</div>
				<div class="sidebar">Side content</div>
			</body></html>`,
			shouldContain:    "This is the article",
			shouldNotContain: "Side content",
		},
		{
			name: "removes social sharing",
			html: `<html><body>
				<article>Article text</article>
				<div class="social-share">Share on Twitter</div>
			</body></html>`,
			shouldContain:    "Article text",
			shouldNotContain: "Share on Twitter",
		},
		{
			name: "removes ads",
			html: `<html><body>
				<article>Content here</article>
				<div class="ad-container">Advertisement</div>
			</body></html>`,
			shouldContain:    "Content here",
			shouldNotContain: "Advertisement",
		},
	}

	filter := NewNoisePatternFilter()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := docFromHTML(tt.html)
			doc = filter.Filter(doc)
			result := doc.Text()

			if !strings.Contains(result, tt.shouldContain) {
				t.Errorf("Result should contain %q but got: %s", tt.shouldContain, result)
			}
			if tt.shouldNotContain != "" && strings.Contains(result, tt.shouldNotContain) {
				t.Errorf("Result should NOT contain %q but got: %s", tt.shouldNotContain, result)
			}
		})
	}
}

func TestNavigationTextFilter(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		shouldContain  string
		shouldNotContain string
	}{
		{
			name: "removes sign in text",
			html: `<html><body>
				<article>Article content</article>
				<div>Sign in</div>
			</body></html>`,
			shouldContain:    "Article content",
			shouldNotContain: "Sign in",
		},
		{
			name: "removes subscribe text",
			html: `<html><body>
				<article>Main story</article>
				<span>Subscribe now</span>
			</body></html>`,
			shouldContain:    "Main story",
			shouldNotContain: "Subscribe now",
		},
		{
			name: "keeps long content",
			html: `<html><body>
				<div>This is a longer piece of content that should definitely not be removed because it contains more than 100 characters and is likely real content not navigation.</div>
			</body></html>`,
			shouldContain: "This is a longer piece",
		},
		{
			name: "removes privacy policy link",
			html: `<html><body>
				<article>News article</article>
				<li>Privacy Policy</li>
			</body></html>`,
			shouldContain:    "News article",
			shouldNotContain: "Privacy Policy",
		},
	}

	filter := NewNavigationTextFilter()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := docFromHTML(tt.html)
			doc = filter.Filter(doc)
			result := doc.Text()

			if !strings.Contains(result, tt.shouldContain) {
				t.Errorf("Result should contain %q but got: %s", tt.shouldContain, result)
			}
			if tt.shouldNotContain != "" && strings.Contains(result, tt.shouldNotContain) {
				t.Errorf("Result should NOT contain %q but got: %s", tt.shouldNotContain, result)
			}
		})
	}
}

func TestLinkDensityFilter(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		shouldContain  string
		shouldNotContain string
	}{
		{
			name: "removes navigation menu with many links",
			html: `<html><body>
				<article><p>This is the main article content with several sentences of text.</p></article>
				<div>
					<a href="#">Link 1</a>
					<a href="#">Link 2</a>
					<a href="#">Link 3</a>
					<a href="#">Link 4</a>
					<a href="#">Link 5</a>
					<a href="#">Link 6</a>
				</div>
			</body></html>`,
			shouldContain:    "main article content",
			shouldNotContain: "Link 6",
		},
		{
			name: "keeps content with few links",
			html: `<html><body>
				<article>
					<p>This is a paragraph with some text and a <a href="#">single link</a> inside it.</p>
				</article>
			</body></html>`,
			shouldContain: "single link",
		},
		{
			name: "removes list of links",
			html: `<html><body>
				<article><p>Article content here</p></article>
				<ul>
					<li><a href="#">Nav 1</a></li>
					<li><a href="#">Nav 2</a></li>
					<li><a href="#">Nav 3</a></li>
					<li><a href="#">Nav 4</a></li>
				</ul>
			</body></html>`,
			shouldContain: "Article content",
		},
	}

	filter := NewLinkDensityFilter()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := docFromHTML(tt.html)
			doc = filter.Filter(doc)
			result := doc.Text()

			if !strings.Contains(result, tt.shouldContain) {
				t.Errorf("Result should contain %q but got: %s", tt.shouldContain, result)
			}
			if tt.shouldNotContain != "" && strings.Contains(result, tt.shouldNotContain) {
				t.Errorf("Result should NOT contain %q but got: %s", tt.shouldNotContain, result)
			}
		})
	}
}

func TestStopwordsScorer(t *testing.T) {
	scorer := NewStopwordsScorer()

	tests := []struct {
		name         string
		text         string
		minStopwords int
		maxStopwords int
	}{
		{
			name:         "empty text",
			text:         "",
			minStopwords: 0,
			maxStopwords: 0,
		},
		{
			name:         "simple sentence",
			text:         "The quick brown fox jumps over the lazy dog",
			minStopwords: 2, // "the" appears twice, "over" is also a stopword
			maxStopwords: 5,
		},
		{
			name:         "many stopwords",
			text:         "I am going to the store and I will be there for a while",
			minStopwords: 8, // i, am, to, the, and, i, will, be, there, for, a
			maxStopwords: 15,
		},
		{
			name:         "no stopwords",
			text:         "Python JavaScript Ruby TypeScript",
			minStopwords: 0,
			maxStopwords: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := scorer.CountStopwords(tt.text)
			if count < tt.minStopwords {
				t.Errorf("CountStopwords(%q) = %d, want >= %d", tt.text, count, tt.minStopwords)
			}
			if count > tt.maxStopwords {
				t.Errorf("CountStopwords(%q) = %d, want <= %d", tt.text, count, tt.maxStopwords)
			}
		})
	}
}

func TestContentNodeFinder(t *testing.T) {
	finder := NewContentNodeFinder()

	tests := []struct {
		name           string
		html           string
		shouldContain  string
	}{
		{
			name: "finds article element",
			html: `<html><body>
				<nav>Navigation</nav>
				<article>
					<p>This is a paragraph with lots of text and many words that should be found as content.</p>
					<p>Another paragraph with more text to ensure this is substantial content.</p>
				</article>
				<footer>Footer</footer>
			</body></html>`,
			shouldContain: "This is a paragraph",
		},
		{
			name: "finds main element",
			html: `<html><body>
				<header>Header</header>
				<main>
					<p>Main content paragraph one with lots of text and words.</p>
					<p>Main content paragraph two with more substantial text.</p>
				</main>
				<aside>Sidebar</aside>
			</body></html>`,
			shouldContain: "Main content paragraph",
		},
		{
			name: "finds content by stopwords scoring",
			html: `<html><body>
				<div class="nav"><a href="#">Link</a><a href="#">Link</a></div>
				<div class="wrapper">
					<p>The article discusses how the new technology will change the way we live and work in the future. It is an important development that many people are excited about.</p>
					<p>Furthermore, the implications of this change are significant for the industry and will affect how we approach problems in the coming years.</p>
				</div>
			</body></html>`,
			shouldContain: "article discusses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := docFromHTML(tt.html)
			node, score := finder.FindBestNode(doc)

			if node == nil {
				t.Error("FindBestNode returned nil")
				return
			}

			if score <= 0 {
				t.Errorf("Score should be > 0, got %d", score)
			}

			text := node.Text()
			if !strings.Contains(text, tt.shouldContain) {
				t.Errorf("Best node should contain %q but got: %s", tt.shouldContain, text)
			}
		})
	}
}

func TestFilterChain(t *testing.T) {
	html := `<html><body>
		<div class="sidebar">Sidebar content</div>
		<article>
			<p>This is the main article with substantial content about an important topic.</p>
		</article>
		<div class="footer">Footer stuff</div>
		<span>Sign in</span>
	</body></html>`

	chain := NewFilterChain(
		NewNoisePatternFilter(),
		NewNavigationTextFilter(),
	)

	doc := docFromHTML(html)
	doc = chain.Apply(doc)
	result := doc.Text()

	if !strings.Contains(result, "main article") {
		t.Error("Result should contain 'main article'")
	}
	if strings.Contains(result, "Sidebar") {
		t.Error("Result should NOT contain 'Sidebar'")
	}
	if strings.Contains(result, "Sign in") {
		t.Error("Result should NOT contain 'Sign in'")
	}
}

func TestEnhancedContentExtractor(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		shouldContain  string
		shouldNotContain string
	}{
		{
			name: "extracts article content",
			html: `<html><body>
				<nav>Navigation menu</nav>
				<article>
					<p>This is a substantial article paragraph with lots of words and content that should be extracted as the main content.</p>
					<p>Another paragraph continues the article with more important information.</p>
				</article>
				<div class="sidebar">Related links</div>
				<footer>Copyright</footer>
			</body></html>`,
			shouldContain:    "substantial article paragraph",
			shouldNotContain: "Navigation menu",
		},
		{
			name: "handles pages without semantic elements",
			html: `<html><body>
				<div class="header"><a href="#">Home</a><a href="#">About</a></div>
				<div class="content">
					<p>The main content of the page discusses important topics that are relevant to the reader.</p>
					<p>It continues with more paragraphs that provide detailed information about the subject.</p>
				</div>
				<div class="footer">Footer links</div>
			</body></html>`,
			shouldContain: "main content of the page",
		},
	}

	extractor := NewEnhancedContentExtractor()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractText([]byte(tt.html))

			if !strings.Contains(result, tt.shouldContain) {
				t.Errorf("Result should contain %q but got: %s", tt.shouldContain, result)
			}
			if tt.shouldNotContain != "" && strings.Contains(result, tt.shouldNotContain) {
				t.Errorf("Result should NOT contain %q but got: %s", tt.shouldNotContain, result)
			}
		})
	}
}

func TestExtractEnhancedContentText(t *testing.T) {
	html := `<html><body>
		<nav>Menu items</nav>
		<article>
			<p>This is the main content that should be extracted with all the important information.</p>
		</article>
		<aside>Side content</aside>
	</body></html>`

	result := extractEnhancedContentText([]byte(html))

	if !strings.Contains(result, "main content") {
		t.Errorf("extractEnhancedContentText should contain 'main content' but got: %s", result)
	}
}

func TestOriginalExtractionUnchanged(t *testing.T) {
	// Test that the original extractMainContentText still works exactly as before
	html := `<html>
		<body>
			<nav>Navigation</nav>
			<article>Article Content</article>
			<footer>Footer</footer>
		</body>
	</html>`

	result := extractMainContentText([]byte(html))
	if result != "Article Content" {
		t.Errorf("extractMainContentText should return 'Article Content' but got: %s", result)
	}
}
