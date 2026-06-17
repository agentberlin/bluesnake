// Package llmstxt parses and structurally validates the /llms.txt standard
// (https://llmstxt.org): a markdown file at a site root that curates the site
// for LLMs. It is a site-level artifact like robots.txt — fetched once per host,
// out-of-band — and like sitemaps it carries a link list bluesnake cross-checks
// against the crawl. This package is a pure parser/validator; fetching,
// frontier admission and issue emission live in the crawler/analyze layers.
//
// Format (only the H1 title is required):
//
//	# Project Name
//
//	> One-line summary of the project (blockquote).
//
//	Optional free prose with more detail.
//
//	## Section
//
//	- [Name](https://example.com/page): optional description
//	- [Another](https://example.com/other)
//
// Sections are delimited by H2 headers; each holds a markdown list whose items
// are links. A bullet under a section that is not a well-formed link marks the
// file malformed.
package llmstxt

import (
	"regexp"
	"strings"
)

// Link is one curated entry from a section list.
type Link struct {
	Section string // the H2 section it appeared under ("" if before any section)
	Name    string // the link text
	URL     string // the (possibly relative) href, verbatim
	Desc    string // optional trailing description
}

// File is a parsed llms.txt document. Title is the only field the spec requires;
// an empty Title means the file is structurally invalid.
type File struct {
	Title     string // first H1 text ("" when absent)
	Summary   string // blockquote summary text ("" when absent)
	Links     []Link // curated links, in document order
	Malformed bool   // a bullet under a section was not a well-formed link
}

var (
	h1Re     = regexp.MustCompile(`^#\s+(.+?)\s*$`)
	h2Re     = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	quoteRe  = regexp.MustCompile(`^>\s?(.*)$`)
	bulletRe = regexp.MustCompile(`^[-*]\s+`)
	// a list item: [name](url) with an optional ": description" tail
	itemRe = regexp.MustCompile(`^[-*]\s+\[([^\]]*)\]\(\s*([^)\s]+)\s*\)\s*(?::\s*(.*?))?\s*$`)
)

// Valid reports whether the file meets the one hard requirement of the spec:
// an H1 title.
func (f *File) Valid() bool { return f.Title != "" }

// Parse reads an llms.txt document. It never returns nil; an empty or
// unparseable body yields a File whose Valid() is false.
func Parse(data []byte) *File {
	f := &File{}
	section := ""
	var summary []string
	finalizeSummary := func() {
		if len(summary) > 0 && f.Summary == "" {
			f.Summary = strings.TrimSpace(strings.Join(summary, " "))
		}
	}

	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		// H2 opens a new section; collected summary ends at the first heading.
		if m := h2Re.FindStringSubmatch(line); m != nil {
			finalizeSummary()
			section = m[1]
			continue
		}
		// H1: first one wins as the title.
		if m := h1Re.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "##") {
			if f.Title == "" {
				f.Title = m[1]
			}
			continue
		}
		// Blockquote summary: the first run of '>' lines in the header area
		// (before any section), captured even when the H1 is absent.
		if m := quoteRe.FindStringSubmatch(line); m != nil {
			if section == "" && f.Summary == "" {
				summary = append(summary, strings.TrimSpace(m[1]))
			}
			continue
		}
		if trimmed == "" {
			finalizeSummary()
			continue
		}

		// Bullets only carry meaning inside a section; free-prose lists before
		// the first H2 are allowed and never flagged.
		if bulletRe.MatchString(trimmed) {
			if section == "" {
				continue
			}
			if m := itemRe.FindStringSubmatch(trimmed); m != nil {
				f.Links = append(f.Links, Link{
					Section: section,
					Name:    strings.TrimSpace(m[1]),
					URL:     strings.TrimSpace(m[2]),
					Desc:    strings.TrimSpace(m[3]),
				})
			} else {
				f.Malformed = true
			}
		}
	}
	finalizeSummary()
	return f
}
