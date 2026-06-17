package parse

import (
	"slices"
	"strconv"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"golang.org/x/net/html"
)

// elements never contributing to text content (svg titles and select
// option labels are not page prose either)
var nonTextElements = []string{"script", "style", "noscript", "template",
	"svg", "select", "textarea"}

// wordSepElements separate words but do NOT start a new line/sentence: a table
// cell joins its row's text with a space, while the row (<tr>) is the logical
// line. Screaming Frog counts a two-cell row as one sentence whose cells are
// space-joined words; only <tr> (and other block elements) end a sentence.
var wordSepElements = map[string]bool{"td": true, "th": true}

// collectContentMetrics computes word count, readability and text ratio.
// Word count and readability use the configured content area (nav/footer
// excluded by default); text ratio uses the full body text vs page bytes.
func collectContentMetrics(root *html.Node, body []byte, cfg *config.Config, facts *Facts) {
	bodyNode := findElement(root, "body")
	if bodyNode == nil {
		return
	}

	area := &cfg.Content.Area
	blocks := extractBlocks(bodyNode, area, hasIncludeRules(area), true)
	facts.ContentText = strings.Join(blocks, " ")

	stats := computeStats(blocks)
	facts.WordCount = stats.words
	facts.AvgWordsPerSentence = stats.avgWordsPerSentence
	facts.Flesch = stats.flesch

	if len(body) > 0 {
		// Text ratio is whole-page visible text vs total bytes — no content-area
		// exclusion and no synthetic list markers.
		fullText := strings.Join(extractBlocks(bodyNode, nil, false, false), " ")
		facts.TextRatio = float64(len(fullText)) / float64(len(body)) * 100
	}
}

func hasIncludeRules(area *config.ContentAreaConfig) bool {
	return len(area.IncludeElements)+len(area.IncludeClasses)+len(area.IncludeIDs) > 0
}

// extractBlocks walks the subtree and returns the visible text split into blocks
// (logical lines). The split is structural, not textual: every non-inline
// element except table cells starts a new block, and so does <br> — but literal
// whitespace inside a text node (including newlines) is just a word break, never
// a sentence break. Inline elements join their text with no space (except an
// immediately same-tag-adjacent sibling, which breaks a word); table cells
// (<td>/<th>) insert a word space without ending the block. With include rules,
// only subtrees rooted at a matching element contribute (inIncluded tracks
// that); exclude rules always prune. List markers are synthesised when
// withMarkers is set (off for the text-ratio pass).
func extractBlocks(root *html.Node, area *config.ContentAreaConfig, includeMode, withMarkers bool) []string {
	e := &blockExtractor{area: area, includeMode: includeMode, withMarkers: withMarkers}
	e.walk(root, false, false)
	e.flush()
	return e.blocks
}

type blockExtractor struct {
	area          *config.ContentAreaConfig
	includeMode   bool
	withMarkers   bool
	blocks        []string
	cur           strings.Builder
	pendingMarker string // list marker waiting to prefix the item's first line
}

func (e *blockExtractor) flush() {
	if t := collapseSpace(e.cur.String()); t != "" {
		// A list marker renders on the same line as the item's first content,
		// even when that content is wrapped in block elements
		// (<li><div>text</div></li> reads "• text", not "•" then "text").
		if e.pendingMarker != "" {
			t = e.pendingMarker + " " + t
			e.pendingMarker = ""
		}
		e.blocks = append(e.blocks, t)
	}
	e.cur.Reset()
}

func (e *blockExtractor) walk(n *html.Node, inIncluded, inPre bool) {
	if n.Type == html.ElementNode {
		if slices.Contains(nonTextElements, n.Data) {
			return
		}
		if e.area != nil {
			if matchesRules(n, e.area.ExcludeElements, e.area.ExcludeClasses, e.area.ExcludeIDs) {
				return
			}
			if e.includeMode && !inIncluded &&
				matchesRules(n, e.area.IncludeElements, e.area.IncludeClasses, e.area.IncludeIDs) {
				inIncluded = true
			}
		}
		if n.Data == "pre" {
			inPre = true
		}
	}
	if n.Type == html.TextNode && (!e.includeMode || inIncluded) {
		e.writeText(n.Data, inPre)
	}

	isBlock := n.Type == html.ElementNode && !inlineElements[n.Data] && !wordSepElements[n.Data]
	isWordSep := n.Type == html.ElementNode && wordSepElements[n.Data]
	switch {
	case isBlock:
		e.flush()
		if e.withMarkers && n.Data == "li" {
			e.pendingMarker = listMarker(n)
		}
	case isWordSep:
		e.cur.WriteByte(' ')
	case sameTagAdjacent(n):
		e.cur.WriteByte(' ') // word break without a sentence break
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		e.walk(c, inIncluded, inPre)
	}

	if isWordSep {
		e.cur.WriteByte(' ')
	}
	if isBlock {
		e.flush()
		if n.Data == "li" {
			e.pendingMarker = "" // drop an unconsumed marker (empty item)
		}
	}
}

// writeText appends a text node's data to the current block. Inside <pre>,
// whitespace is significant: a newline is a line break, so it ends the block
// (a preformatted code/ascii line is its own line, the way browsers and
// Screaming Frog treat it). Elsewhere, all whitespace — including source
// newlines — collapses to a single word break at flush time.
func (e *blockExtractor) writeText(s string, inPre bool) {
	if !inPre || !strings.ContainsRune(s, '\n') {
		e.cur.WriteString(s)
		return
	}
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			e.flush()
		}
		e.cur.WriteString(line)
	}
}

// listMarker returns the rendered marker for a list item: a bullet for <ul>
// items and the ordinal "N." for <ol> items (honouring the list's start
// attribute), matching the visible text Screaming Frog counts. The bullet is a
// single word; "N." is a number word that also ends a sentence. Items whose
// parent is not a list, and definition lists, have no marker.
func listMarker(li *html.Node) string {
	p := li.Parent
	if p == nil || p.Type != html.ElementNode {
		return ""
	}
	switch p.Data {
	case "ul":
		return "•"
	case "ol":
		idx := 1
		if s := strings.TrimSpace(attr(p, "start")); s != "" {
			if v, err := strconv.Atoi(s); err == nil {
				idx = v
			}
		}
		for sib := p.FirstChild; sib != nil && sib != li; sib = sib.NextSibling {
			if sib.Type == html.ElementNode && sib.Data == "li" {
				idx++
			}
		}
		return strconv.Itoa(idx) + "."
	}
	return ""
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
