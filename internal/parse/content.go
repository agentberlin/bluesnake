package parse

import (
	"regexp"
	"slices"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"golang.org/x/net/html"
)

// elements never contributing to text content
var nonTextElements = []string{"script", "style", "noscript", "template"}

// collectContentMetrics computes word count, readability and text ratio.
// Word count and readability use the configured content area (nav/footer
// excluded by default); text ratio uses the full body text vs page bytes.
func collectContentMetrics(root *html.Node, body []byte, cfg *config.Config, facts *Facts) {
	bodyNode := findElement(root, "body")
	if bodyNode == nil {
		return
	}

	area := &cfg.Content.Area
	contentText := collapseSpace(collectText(bodyNode, area, hasIncludeRules(area), false))
	fullText := collapseSpace(collectText(bodyNode, nil, false, false))

	facts.ContentText = contentText
	words := strings.Fields(contentText)
	facts.WordCount = len(words)
	if len(body) > 0 {
		facts.TextRatio = float64(len(fullText)) / float64(len(body)) * 100
	}

	sentences := countSentences(contentText)
	if len(words) > 0 {
		facts.AvgWordsPerSentence = float64(len(words)) / float64(sentences)
		facts.Flesch = fleschScore(words, sentences)
	}
}

func hasIncludeRules(area *config.ContentAreaConfig) bool {
	return len(area.IncludeElements)+len(area.IncludeClasses)+len(area.IncludeIDs) > 0
}

// collectText walks the subtree gathering text. With include rules, only
// subtrees rooted at a matching element contribute (inIncluded tracks that);
// exclude rules always prune.
func collectText(n *html.Node, area *config.ContentAreaConfig, includeMode, inIncluded bool) string {
	if n.Type == html.ElementNode {
		if slices.Contains(nonTextElements, n.Data) {
			return ""
		}
		if area != nil {
			if matchesRules(n, area.ExcludeElements, area.ExcludeClasses, area.ExcludeIDs) {
				return ""
			}
			if includeMode && !inIncluded &&
				matchesRules(n, area.IncludeElements, area.IncludeClasses, area.IncludeIDs) {
				inIncluded = true
			}
		}
	}
	var b strings.Builder
	if n.Type == html.TextNode && (!includeMode || inIncluded) {
		b.WriteString(n.Data)
		b.WriteString(" ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(collectText(c, area, includeMode, inIncluded))
	}
	return b.String()
}

func matchesRules(n *html.Node, elements, classes, ids []string) bool {
	if slices.Contains(elements, n.Data) {
		return true
	}
	if len(classes) > 0 {
		for cls := range strings.FieldsSeq(attr(n, "class")) {
			if slices.Contains(classes, cls) {
				return true
			}
		}
	}
	if len(ids) > 0 && slices.Contains(ids, attr(n, "id")) {
		return true
	}
	return false
}

func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

var sentenceEnd = regexp.MustCompile(`[.!?]+`)

func countSentences(text string) int {
	n := len(sentenceEnd.FindAllString(text, -1))
	if n == 0 {
		return 1
	}
	return n
}

// fleschScore computes the Flesch Reading Ease score with a vowel-group
// syllable approximation (the standard heuristic for automated scoring).
func fleschScore(words []string, sentences int) float64 {
	syllables := 0
	for _, w := range words {
		syllables += syllableEstimate(w)
	}
	wordCount := float64(len(words))
	return 206.835 - 1.015*(wordCount/float64(sentences)) - 84.6*(float64(syllables)/wordCount)
}

func syllableEstimate(word string) int {
	count := 0
	prevVowel := false
	for _, r := range strings.ToLower(word) {
		isVowel := strings.ContainsRune("aeiouy", r)
		if isVowel && !prevVowel {
			count++
		}
		prevVowel = isVowel
	}
	if count == 0 {
		return 1
	}
	return count
}
