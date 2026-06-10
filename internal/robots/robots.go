// Package robots parses and evaluates robots.txt with Google REP semantics
// (RFC 9309 + Google extensions): case-insensitive prefix-based user-agent
// group selection with fallback to *, longest-path-match rule precedence with
// allow winning ties, * wildcards and $ end anchors, sitemap discovery, and
// matched-line reporting for the "Blocked by Robots.txt" crawl state.
package robots

import (
	"bufio"
	"bytes"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Rule is one Allow/Disallow line.
type Rule struct {
	Allow bool
	Path  string
	Line  int    // 1-based line number in the file
	Raw   string // canonical "Disallow: /path" text for reporting

	re *regexp.Regexp
}

// Group is one user-agent group (consecutive User-agent lines + their rules).
type Group struct {
	Agents []string // lowercased tokens
	Rules  []Rule
}

// File is a parsed robots.txt.
type File struct {
	Groups   []Group
	Sitemaps []string
}

// Verdict is the result of testing a URL.
type Verdict struct {
	Allowed bool
	Rule    *Rule // the deciding rule; nil when nothing matched
}

// Parse never fails: malformed lines are ignored, an empty file allows all.
func Parse(data []byte) *File {
	f := &File{}
	var current *Group
	lastWasAgent := false
	lineNo := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if !lastWasAgent {
				f.Groups = append(f.Groups, Group{})
				current = &f.Groups[len(f.Groups)-1]
			}
			current.Agents = append(current.Agents, strings.ToLower(value))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if current == nil || value == "" {
				continue // rules before any group, and empty paths, have no effect
			}
			raw := "Disallow: " + value
			if key == "allow" {
				raw = "Allow: " + value
			}
			current.Rules = append(current.Rules, Rule{
				Allow: key == "allow",
				Path:  value,
				Line:  lineNo,
				Raw:   raw,
				re:    compileRulePath(value),
			})
		case "sitemap":
			lastWasAgent = false
			if value != "" {
				f.Sitemaps = append(f.Sitemaps, value)
			}
		default:
			lastWasAgent = false
		}
	}
	return f
}

// compileRulePath turns a rule path into an anchored matcher: * matches any
// run of characters, a trailing $ anchors the end.
func compileRulePath(path string) *regexp.Regexp {
	anchored := strings.HasSuffix(path, "$")
	if anchored {
		path = strings.TrimSuffix(path, "$")
	}
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(path), `\*`, `.*`)
	if anchored {
		expr += "$"
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	return re
}

// Verdict tests a URL for a robots user-agent token. Group selection: the
// longest group token that is a case-insensitive prefix of the user-agent
// wins; with no match the * groups apply. All groups sharing the winning
// token are merged (RFC 9309 §2.2.1).
func (f *File) Verdict(userAgent, rawURL string) Verdict {
	path := pathFor(rawURL)
	rules := f.rulesFor(strings.ToLower(userAgent))

	var best *Rule
	for i := range rules {
		r := &rules[i]
		if r.re == nil || !r.re.MatchString(path) {
			continue
		}
		switch {
		case best == nil,
			len(r.Path) > len(best.Path),
			len(r.Path) == len(best.Path) && r.Allow && !best.Allow:
			best = r
		}
	}
	if best == nil {
		return Verdict{Allowed: true}
	}
	return Verdict{Allowed: best.Allow, Rule: best}
}

func (f *File) rulesFor(uaLower string) []Rule {
	bestToken := ""
	for _, g := range f.Groups {
		for _, a := range g.Agents {
			if a != "*" && strings.HasPrefix(uaLower, a) && len(a) > len(bestToken) {
				bestToken = a
			}
		}
	}
	match := func(a string) bool { return a == bestToken }
	if bestToken == "" {
		match = func(a string) bool { return a == "*" }
	}
	var rules []Rule
	for _, g := range f.Groups {
		if slices.ContainsFunc(g.Agents, match) {
			rules = append(rules, g.Rules...)
		}
	}
	return rules
}

// pathFor extracts the matchable part of a URL: escaped path plus query.
func pathFor(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}
