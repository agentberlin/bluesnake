package parse

import (
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"golang.org/x/net/html"
)

// elements never contributing to text content (svg titles and select
// option labels are not page prose either)
var nonTextElements = []string{"script", "style", "noscript", "template",
	"svg", "select", "textarea"}

// collectContentMetrics computes word count, readability and text ratio.
// Word count and readability use the configured content area (nav/footer
// excluded by default); text ratio uses the full body text vs page bytes.
func collectContentMetrics(root *html.Node, body []byte, cfg *config.Config, facts *Facts) {
	bodyNode := findElement(root, "body")
	if bodyNode == nil {
		return
	}

	area := &cfg.Content.Area
	rawContent := collectText(bodyNode, area, hasIncludeRules(area), false)
	contentText := collapseSpace(rawContent)
	fullText := collapseSpace(collectText(bodyNode, nil, false, false))

	facts.ContentText = contentText
	words := strings.Fields(contentText)
	facts.WordCount = len(words)
	if len(body) > 0 {
		facts.TextRatio = float64(len(fullText)) / float64(len(body)) * 100
	}

	sentences := countSentences(rawContent)
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
// exclude rules always prune. Words join across inline elements and break
// at every other element boundary, matching rendered text.
func collectText(n *html.Node, area *config.ContentAreaConfig, includeMode, inIncluded bool) string {
	var b strings.Builder
	collectTextInto(&b, n, area, includeMode, inIncluded)
	return b.String()
}

func collectTextInto(b *strings.Builder, n *html.Node, area *config.ContentAreaConfig, includeMode, inIncluded bool) {
	if n.Type == html.ElementNode {
		if slices.Contains(nonTextElements, n.Data) {
			return
		}
		if area != nil {
			if matchesRules(n, area.ExcludeElements, area.ExcludeClasses, area.ExcludeIDs) {
				return
			}
			if includeMode && !inIncluded &&
				matchesRules(n, area.IncludeElements, area.IncludeClasses, area.IncludeIDs) {
				inIncluded = true
			}
		}
	}
	if n.Type == html.TextNode && (!includeMode || inIncluded) {
		b.WriteString(n.Data)
	}
	// the newline boundary marker doubles as a sentence break for
	// countSentences; collapseSpace turns it into a plain word break
	boundary := n.Type == html.ElementNode && !inlineElements[n.Data]
	if boundary {
		b.WriteString("\n")
	} else if sameTagAdjacent(n) {
		b.WriteString(" ") // word break without a sentence break
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectTextInto(b, c, area, includeMode, inIncluded)
	}
	if boundary {
		b.WriteString("\n")
	}
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

// forcedBreakChars splits very long unpunctuated prose into multiple
// sentences for readability scoring: each block contributes one extra
// sentence per this many characters (Screaming Frog behaviour, measured
// against controlled pages — see the parity notes).
const forcedBreakChars = 85

// countSentences counts sentences the way Screaming Frog does: block
// boundaries (the \n markers collectText emits) end a sentence, runs of
// [.!?] split within a block, and every 85 characters of block text force
// an extra break so punctuation-free pages still score sensibly.
func countSentences(raw string) int {
	n := 0
	for block := range strings.SplitSeq(raw, "\n") {
		text := collapseSpace(block)
		if text == "" {
			continue
		}
		for _, segment := range sentenceEnd.Split(text, -1) {
			if strings.TrimSpace(segment) != "" {
				n++
			}
		}
		n += len(text) / forcedBreakChars
	}
	if n == 0 {
		return 1
	}
	return n
}

// fleschScore computes the Flesch Reading Ease score with a vowel-group
// syllable approximation, clamped to [0, 100] (Screaming Frog parity).
func fleschScore(words []string, sentences int) float64 {
	syllables := 0
	for _, w := range words {
		syllables += syllableEstimate(w)
	}
	wordCount := float64(len(words))
	score := 206.835 - 1.015*(wordCount/float64(sentences)) - 84.6*(float64(syllables)/wordCount)
	return math.Min(100, math.Max(0, score))
}

// syllableEstimate counts vowel groups (y included), treating the silent
// endings -e, -ed and -es as non-syllabic — except -le/-les, where the e is
// sounded ("table"). Tokens keep whatever punctuation they were written
// with, and vowel-less tokens ("$49") count zero syllables.
func syllableEstimate(word string) int {
	w := strings.ToLower(word)
	count := 0
	prevVowel := false
	for _, r := range w {
		isVowel := strings.ContainsRune("aeiouy", r)
		if isVowel && !prevVowel {
			count++
		}
		prevVowel = isVowel
	}
	if count > 1 {
		switch {
		case strings.HasSuffix(w, "le") || strings.HasSuffix(w, "les"):
			// sounded e
		case strings.HasSuffix(w, "e") || strings.HasSuffix(w, "ed") || strings.HasSuffix(w, "es"):
			count--
		}
	}
	return count
}
