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

// content_filters.go provides modular content extraction filters inspired by GoOse.
// These filters are designed to be composable and can be enabled/disabled independently.
// The default text extraction (extractMainContentText) remains unchanged.

package bluesnake

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ContentFilter defines the interface for all content filters.
// Filters modify the document in place and return it for chaining.
type ContentFilter interface {
	// Filter applies the filter to the document and returns the modified document
	Filter(doc *goquery.Document) *goquery.Document
	// Name returns the filter name for debugging
	Name() string
}

// FilterChain applies multiple filters in sequence
type FilterChain struct {
	filters []ContentFilter
}

// NewFilterChain creates a new filter chain with the given filters
func NewFilterChain(filters ...ContentFilter) *FilterChain {
	return &FilterChain{filters: filters}
}

// Apply applies all filters in the chain to the document
func (fc *FilterChain) Apply(doc *goquery.Document) *goquery.Document {
	for _, f := range fc.filters {
		doc = f.Filter(doc)
	}
	return doc
}

// Add adds a filter to the chain
func (fc *FilterChain) Add(f ContentFilter) *FilterChain {
	fc.filters = append(fc.filters, f)
	return fc
}

// =============================================================================
// NoisePatternFilter - Removes elements matching known non-content patterns
// Ported from GoOse's cleaner.go removeNodesRegEx
// =============================================================================

// noisePatterns is ported from GoOse's cleaner.go
// These patterns match class/id attributes of elements that are typically not content
var noisePatterns = regexp.MustCompile(`(?i)` +
	`[Cc]omentario|` +
	`[Ff]ooter|` +
	`^side$|` +
	`^side_|` +
	`^widget$|` +
	`[_-]ads?[_-]?|` +
	`^ad[s]?[ _-]|` +
	`^banner|` +
	`breadcrumbs|` +
	`byline|` +
	`^caption$|` +
	`carousel|` +
	`comment|` +
	`contact|` +
	`cookie|` +
	`^date$|` +
	`facebook|` +
	`figcaption|` +
	`footnote|` +
	`foot|` +
	`footer|` +
	`header|` +
	`hidden|` +
	`menu|` +
	`menucontainer|` +
	`[Nn]avigation|` +
	`navbar|` +
	`^nav[_-]|` +
	`popup|` +
	`recommend|` +
	`related|` +
	`retweet|` +
	`rss|` +
	`search[_-]|` +
	`share[_-]|` +
	`sidebar|` +
	`social|` +
	`sponsor|` +
	`subscribe|` +
	`subscription|` +
	`tags|` +
	`teaser|` +
	`timestamp|` +
	`tools|` +
	`tooltip|` +
	`twitter|` +
	`newsletter|` +
	`follow|` +
	`signin|` +
	`sign-in|` +
	`account|` +
	`settings`)

// keepPatterns protects elements that should not be removed
var keepPatterns = regexp.MustCompile(`(?i)` +
	`\barticle\b|` +
	`\bcontent\b|` +
	`\bstory\b|` +
	`\bpost\b|` +
	`\bentry\b|` +
	`\bmain\b|` +
	`\bbody\b`)

// NoisePatternFilter removes elements that match known non-content patterns
type NoisePatternFilter struct{}

// NewNoisePatternFilter creates a new NoisePatternFilter
func NewNoisePatternFilter() *NoisePatternFilter {
	return &NoisePatternFilter{}
}

// Name returns the filter name
func (f *NoisePatternFilter) Name() string {
	return "NoisePatternFilter"
}

// Filter removes elements matching noise patterns
func (f *NoisePatternFilter) Filter(doc *goquery.Document) *goquery.Document {
	// Remove elements by noise patterns in class and id attributes
	doc.Find("*").Each(func(i int, s *goquery.Selection) {
		class, hasClass := s.Attr("class")
		id, hasID := s.Attr("id")

		// Check if element should be kept
		if hasClass && keepPatterns.MatchString(class) {
			return
		}
		if hasID && keepPatterns.MatchString(id) {
			return
		}

		// Remove if matches noise pattern
		if hasClass && noisePatterns.MatchString(class) {
			s.Remove()
			return
		}
		if hasID && noisePatterns.MatchString(id) {
			s.Remove()
			return
		}
	})

	return doc
}

// =============================================================================
// NavigationTextFilter - Removes elements with navigation-like text
// Ported from GoOse's cleaner.go removeNavigationElements
// =============================================================================

// navigationTextPatterns are common navigation text phrases
var navigationTextPatterns = []string{
	"sign in", "sign out", "subscribe", "newsletter", "my account",
	"settings", "topics you follow", "edition", "follow cnn",
	"watch", "listen", "live tv", "more", "about",
	"terms of use", "privacy policy", "ad choices", "help center",
	"profiles", "leadership", "work for", "newsletters",
	"close icon", "close", "submit", "cancel", "feedback",
	"tweet", "email", "link copied", "see all topics",
	"updated", "published", "min read",
}

// NavigationTextFilter removes elements with navigation-like text content
type NavigationTextFilter struct {
	maxTextLength int
}

// NewNavigationTextFilter creates a new NavigationTextFilter
func NewNavigationTextFilter() *NavigationTextFilter {
	return &NavigationTextFilter{
		maxTextLength: 100, // Only check elements with text shorter than this
	}
}

// Name returns the filter name
func (f *NavigationTextFilter) Name() string {
	return "NavigationTextFilter"
}

// Filter removes elements with navigation-like text
func (f *NavigationTextFilter) Filter(doc *goquery.Document) *goquery.Document {
	doc.Find("div, span, li, ul").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if len(text) == 0 || len(text) >= f.maxTextLength {
			return
		}

		lowerText := strings.ToLower(text)
		for _, pattern := range navigationTextPatterns {
			if strings.Contains(lowerText, pattern) {
				s.Remove()
				return
			}
		}
	})

	return doc
}

// =============================================================================
// LinkDensityFilter - Removes elements with high link-to-text ratio
// Ported from GoOse's extractor.go isHighLinkDensity
// =============================================================================

// LinkDensityFilter removes elements where most text is within links
type LinkDensityFilter struct {
	// MaxLinkRatio is the maximum ratio of link words to total words (default 0.5)
	MaxLinkRatio float64
	// MinLinks is the minimum number of links before considering link density (default 3)
	MinLinks int
}

// NewLinkDensityFilter creates a new LinkDensityFilter with default settings
func NewLinkDensityFilter() *LinkDensityFilter {
	return &LinkDensityFilter{
		MaxLinkRatio: 0.5,
		MinLinks:     3,
	}
}

// Name returns the filter name
func (f *LinkDensityFilter) Name() string {
	return "LinkDensityFilter"
}

// Filter removes elements with high link density
func (f *LinkDensityFilter) Filter(doc *goquery.Document) *goquery.Document {
	doc.Find("div, section, aside, ul, ol").Each(func(i int, s *goquery.Selection) {
		if f.isHighLinkDensity(s) {
			s.Remove()
		}
	})

	return doc
}

// isHighLinkDensity checks if an element has high link density
// Ported from GoOse's extractor.go
func (f *LinkDensityFilter) isHighLinkDensity(node *goquery.Selection) bool {
	links := node.Find("a")
	if links.Length() < f.MinLinks {
		return false
	}

	text := node.Text()
	words := strings.Fields(text)
	nwords := len(words)
	if nwords == 0 {
		return true
	}

	// Calculate words in links
	var linkText strings.Builder
	links.Each(func(i int, s *goquery.Selection) {
		linkText.WriteString(s.Text())
		linkText.WriteString(" ")
	})
	linkWords := len(strings.Fields(linkText.String()))

	// Calculate link density
	linkRatio := float64(linkWords) / float64(nwords)

	// High link density check from GoOse
	if linkRatio > f.MaxLinkRatio {
		return true
	}

	// Additional check: many links with moderate density
	if links.Length() > 5 && linkRatio > 0.3 {
		return true
	}

	return false
}

// =============================================================================
// StopwordsScorer - Scores content based on stopword presence
// Ported from GoOse's stopwords.go
// =============================================================================

// englishStopwords is a subset of the most common English stopwords
// Ported from GoOse's stopwords.go
var englishStopwords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true,
	"against": true, "all": true, "also": true, "am": true, "an": true,
	"and": true, "another": true, "any": true, "are": true, "as": true,
	"at": true, "be": true, "because": true, "been": true, "before": true,
	"being": true, "below": true, "between": true, "both": true, "but": true,
	"by": true, "can": true, "could": true, "did": true, "do": true,
	"does": true, "doing": true, "down": true, "during": true, "each": true,
	"even": true, "few": true, "for": true, "from": true, "further": true,
	"get": true, "had": true, "has": true, "have": true, "having": true,
	"he": true, "her": true, "here": true, "hers": true, "herself": true,
	"him": true, "himself": true, "his": true, "how": true, "i": true,
	"if": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "itself": true, "just": true, "like": true, "make": true,
	"many": true, "me": true, "might": true, "more": true, "most": true,
	"much": true, "must": true, "my": true, "myself": true, "never": true,
	"no": true, "nor": true, "not": true, "now": true, "of": true,
	"off": true, "on": true, "once": true, "only": true, "or": true,
	"other": true, "our": true, "ours": true, "ourselves": true, "out": true,
	"over": true, "own": true, "said": true, "same": true, "she": true,
	"should": true, "so": true, "some": true, "still": true, "such": true,
	"than": true, "that": true, "the": true, "their": true, "theirs": true,
	"them": true, "themselves": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "to": true,
	"too": true, "under": true, "until": true, "up": true, "upon": true,
	"us": true, "very": true, "was": true, "we": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true,
	"who": true, "whom": true, "why": true, "will": true, "with": true,
	"would": true, "you": true, "your": true, "yours": true, "yourself": true,
	"yourselves": true,
}

// StopwordsScorer provides methods for scoring text based on stopwords
type StopwordsScorer struct {
	stopwords map[string]bool
}

// NewStopwordsScorer creates a new StopwordsScorer with English stopwords
func NewStopwordsScorer() *StopwordsScorer {
	return &StopwordsScorer{
		stopwords: englishStopwords,
	}
}

// CountStopwords returns the number of stopwords in the given text
// Ported from GoOse's stopwords.go stopWordsCount
func (s *StopwordsScorer) CountStopwords(text string) int {
	if text == "" {
		return 0
	}

	text = strings.ToLower(text)
	words := strings.Fields(text)
	count := 0
	for _, word := range words {
		// Remove punctuation from word
		word = strings.Trim(word, ".,!?;:\"'()[]{}—–-")
		if s.stopwords[word] {
			count++
		}
	}
	return count
}

// ScoreText returns a score for the text based on stopwords and length
// Higher scores indicate more likely content
func (s *StopwordsScorer) ScoreText(text string) int {
	stopwordCount := s.CountStopwords(text)
	textLen := len(text)

	// Score based on stopwords + length bonus
	// Ported from GoOse's scoring logic in CalculateBestNode
	score := stopwordCount
	if textLen > 100 {
		score += textLen / 100
	}
	return score
}

// =============================================================================
// ContentNodeFinder - Finds the best content node using stopword scoring
// Ported from GoOse's extractor.go CalculateBestNode
// =============================================================================

// ContentNodeFinder identifies the main content area using stopword scoring
type ContentNodeFinder struct {
	scorer           *StopwordsScorer
	linkDensityCheck *LinkDensityFilter
	// MinStopwords is the minimum stopwords count for a paragraph to be considered content
	MinStopwords int
}

// NewContentNodeFinder creates a new ContentNodeFinder
func NewContentNodeFinder() *ContentNodeFinder {
	return &ContentNodeFinder{
		scorer:           NewStopwordsScorer(),
		linkDensityCheck: NewLinkDensityFilter(),
		MinStopwords:     2,
	}
}

// FindBestNode finds the DOM node most likely to contain main content
// Returns the selection and its score
func (f *ContentNodeFinder) FindBestNode(doc *goquery.Document) (*goquery.Selection, int) {
	// First try semantic elements (article, main, etc.)
	if semanticNode := f.trySemanticElements(doc); semanticNode != nil {
		score := f.scorer.ScoreText(semanticNode.Text())
		if score > 10 { // Has substantial content
			return semanticNode, score
		}
	}

	// Fall back to stopwords-based scoring
	parentScores := make(map[*goquery.Selection]int)

	// Find all paragraphs and score their parents
	doc.Find("p, pre, td").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		stopwords := f.scorer.CountStopwords(text)

		// Skip if not enough stopwords (not real content)
		if stopwords < f.MinStopwords {
			return
		}

		// Skip if high link density
		if f.linkDensityCheck.isHighLinkDensity(s) {
			return
		}

		// Calculate score
		score := f.scorer.ScoreText(text)

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

	return bestNode, bestScore
}

// trySemanticElements checks for HTML5 semantic elements
func (f *ContentNodeFinder) trySemanticElements(doc *goquery.Document) *goquery.Selection {
	// Priority order for semantic elements
	selectors := []string{
		"article",
		"main",
		"[role='main']",
		".article-content",
		".post-content",
		".entry-content",
		".content-body",
		"#content",
	}

	for _, selector := range selectors {
		selection := doc.Find(selector).First()
		if selection.Length() > 0 {
			// Validate it has substantial content
			text := selection.Text()
			if len(strings.TrimSpace(text)) > 200 {
				// Also check paragraph count
				paragraphs := selection.Find("p")
				if paragraphs.Length() >= 2 {
					return selection
				}
			}
		}
	}

	return nil
}

// =============================================================================
// EnhancedContentExtractor - Combines all filters for enhanced extraction
// =============================================================================

// EnhancedContentExtractor applies GoOse-inspired filters to improve extraction
type EnhancedContentExtractor struct {
	filterChain   *FilterChain
	nodeFinder    *ContentNodeFinder
	useNodeFinder bool
}

// NewEnhancedContentExtractor creates an extractor with default filters
func NewEnhancedContentExtractor() *EnhancedContentExtractor {
	return &EnhancedContentExtractor{
		filterChain: NewFilterChain(
			NewNoisePatternFilter(),
			NewNavigationTextFilter(),
			NewLinkDensityFilter(),
		),
		nodeFinder:    NewContentNodeFinder(),
		useNodeFinder: true,
	}
}

// WithFilters sets custom filters, replacing the default chain
func (e *EnhancedContentExtractor) WithFilters(filters ...ContentFilter) *EnhancedContentExtractor {
	e.filterChain = NewFilterChain(filters...)
	return e
}

// WithoutNodeFinder disables the content node finder
func (e *EnhancedContentExtractor) WithoutNodeFinder() *EnhancedContentExtractor {
	e.useNodeFinder = false
	return e
}

// ExtractText extracts main content text with enhanced filtering
func (e *EnhancedContentExtractor) ExtractText(htmlBody []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlBody))
	if err != nil {
		return ""
	}

	// Remove script and style first
	doc.Find("script, style, noscript").Remove()

	// Apply filter chain
	doc = e.filterChain.Apply(doc)

	// Find best content node if enabled
	var contentSelection *goquery.Selection
	if e.useNodeFinder {
		bestNode, score := e.nodeFinder.FindBestNode(doc)
		if bestNode != nil && score > 10 {
			contentSelection = bestNode
		}
	}

	// Fall back to semantic elements if node finder didn't find good content
	if contentSelection == nil {
		if article := doc.Find("article").First(); article.Length() > 0 {
			contentSelection = article
		} else if main := doc.Find("main").First(); main.Length() > 0 {
			contentSelection = main
		} else if roleMain := doc.Find("[role='main']").First(); roleMain.Length() > 0 {
			contentSelection = roleMain
		} else {
			contentSelection = doc.Find("body")
		}
	}

	if contentSelection == nil || contentSelection.Length() == 0 {
		return ""
	}

	text := contentSelection.Text()
	text = normalizeWhitespace(text)
	return strings.TrimSpace(text)
}

// =============================================================================
// Helper function for easy extraction with enhanced filters
// =============================================================================

// extractEnhancedContentText extracts text using GoOse-inspired filters
// This is an alternative to extractMainContentText with additional filtering
func extractEnhancedContentText(htmlBody []byte) string {
	extractor := NewEnhancedContentExtractor()
	return extractor.ExtractText(htmlBody)
}
