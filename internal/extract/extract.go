// Package extract implements custom search (contains / does-not-contain over
// raw HTML, page text, or an element scope) and custom extraction (XPath,
// CSS selectors, regex with text/html/function returns) — Screaming Frog's
// Custom Search and Custom Extraction, config-driven and run against
// internal 2xx HTML pages.
package extract

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xpath"
	"golang.org/x/net/html"
)

// Result is one custom search/extraction value for a page.
type Result struct {
	Kind  string // search | extraction
	Name  string
	Value string
}

type search struct {
	cfg config.CustomSearch
	re  *regexp.Regexp // nil for plain-text search
}

type extractor struct {
	cfg  config.CustomExtraction
	expr *xpath.Expr    // xpath mode
	re   *regexp.Regexp // regex mode
}

// Engine holds compiled searches and extractors.
type Engine struct {
	searches   []search
	extractors []extractor
}

// New compiles the configured searches and extractors. Returns nil when
// nothing is configured (the crawler skips the stage entirely).
func New(cfg *config.Config) (*Engine, error) {
	if len(cfg.CustomSearch)+len(cfg.CustomExtraction) == 0 {
		return nil, nil
	}
	e := &Engine{}
	for _, cs := range cfg.CustomSearch {
		s := search{cfg: cs}
		if cs.Regex {
			re, err := regexp.Compile("(?s)" + cs.Pattern)
			if err != nil {
				return nil, fmt.Errorf("custom_search %q: %w", cs.Name, err)
			}
			s.re = re
		}
		e.searches = append(e.searches, s)
	}
	for _, ce := range cfg.CustomExtraction {
		x := extractor{cfg: ce}
		switch ce.Type {
		case "xpath":
			expr, err := xpath.Compile(ce.Expression)
			if err != nil {
				return nil, fmt.Errorf("custom_extraction %q: %w", ce.Name, err)
			}
			x.expr = expr
		case "regex":
			re, err := regexp.Compile("(?s)" + ce.Expression)
			if err != nil {
				return nil, fmt.Errorf("custom_extraction %q: %w", ce.Name, err)
			}
			x.re = re
		}
		e.extractors = append(e.extractors, x)
	}
	return e, nil
}

// Run evaluates everything against one page.
func (e *Engine) Run(body []byte, contentText string) []Result {
	var results []Result
	var doc *html.Node
	var gq *goquery.Document
	getDoc := func() *html.Node {
		if doc == nil {
			doc, _ = html.Parse(bytes.NewReader(body))
		}
		return doc
	}
	getGQ := func() *goquery.Document {
		if gq == nil {
			if d := getDoc(); d != nil {
				gq = goquery.NewDocumentFromNode(d)
			}
		}
		return gq
	}

	for _, s := range e.searches {
		haystack := string(body)
		switch {
		case s.cfg.Scope == "text":
			haystack = contentText
		case strings.HasPrefix(s.cfg.Scope, "element:"):
			sel := strings.TrimPrefix(s.cfg.Scope, "element:")
			if g := getGQ(); g != nil {
				var parts []string
				g.Find(sel).Each(func(_ int, el *goquery.Selection) {
					h, _ := goquery.OuterHtml(el)
					parts = append(parts, h)
				})
				haystack = strings.Join(parts, "\n")
			}
		}
		count := 0
		if s.re != nil {
			count = len(s.re.FindAllStringIndex(haystack, -1))
		} else {
			count = strings.Count(haystack, s.cfg.Pattern)
		}
		value := fmt.Sprintf("%d", count)
		if s.cfg.Mode == "not_contains" {
			value = fmt.Sprintf("%t", count == 0)
		}
		results = append(results, Result{Kind: "search", Name: s.cfg.Name, Value: value})
	}

	for _, x := range e.extractors {
		var values []string
		switch x.cfg.Type {
		case "regex":
			for _, m := range x.re.FindAllStringSubmatch(string(body), -1) {
				if len(m) > 1 {
					values = append(values, m[1])
				} else {
					values = append(values, m[0])
				}
			}
		case "xpath":
			d := getDoc()
			if d == nil {
				break
			}
			if x.cfg.Return == "function" {
				values = append(values, fmt.Sprintf("%v", x.expr.Evaluate(htmlquery.CreateXPathNavigator(d))))
				break
			}
			for _, n := range htmlquery.QuerySelectorAll(d, x.expr) {
				values = append(values, nodeValue(n, x.cfg.Return))
			}
		case "css":
			g := getGQ()
			if g == nil {
				break
			}
			g.Find(x.cfg.Expression).Each(func(_ int, el *goquery.Selection) {
				switch {
				case x.cfg.Attribute != "":
					if v, ok := el.Attr(x.cfg.Attribute); ok {
						values = append(values, v)
					}
				case x.cfg.Return == "html":
					h, _ := goquery.OuterHtml(el)
					values = append(values, h)
				case x.cfg.Return == "inner_html":
					h, _ := el.Html()
					values = append(values, h)
				default:
					values = append(values, strings.TrimSpace(el.Text()))
				}
			})
		}
		results = append(results, Result{Kind: "extraction", Name: x.cfg.Name,
			Value: strings.Join(values, " | ")})
	}
	return results
}

func nodeValue(n *html.Node, ret string) string {
	switch ret {
	case "html":
		return htmlquery.OutputHTML(n, true)
	case "inner_html":
		return htmlquery.OutputHTML(n, false)
	default:
		return strings.TrimSpace(htmlquery.InnerText(n))
	}
}
