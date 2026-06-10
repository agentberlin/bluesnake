// Package structured extracts schema.org structured data — JSON-LD,
// Microdata and RDFa — and validates a curated subset of Google rich-result
// feature requirements (required properties → errors, recommended →
// warnings). The full Google feature matrix is much larger; the subset here
// covers the most common types and the validation engine is data-driven so
// extending it is a table edit (see requirements).
package structured

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hhsecond/acrawler/internal/config"
	"golang.org/x/net/html"
)

// PageData is the structured-data result for one page.
type PageData struct {
	Formats     []string `json:"formats,omitempty"` // jsonld | microdata | rdfa
	Types       []string `json:"types,omitempty"`
	ParseErrors []string `json:"parse_errors,omitempty"`
	Errors      []string `json:"errors,omitempty"`   // missing required properties
	Warnings    []string `json:"warnings,omitempty"` // missing recommended properties
}

// requirements: Google rich-results required/recommended properties for the
// curated feature subset.
var requirements = map[string]struct{ required, recommended []string }{
	"Product":        {[]string{"name"}, []string{"image", "offers", "review", "aggregateRating"}},
	"Article":        {[]string{"headline"}, []string{"image", "datePublished", "author"}},
	"NewsArticle":    {[]string{"headline"}, []string{"image", "datePublished", "author"}},
	"BlogPosting":    {[]string{"headline"}, []string{"image", "datePublished", "author"}},
	"BreadcrumbList": {[]string{"itemListElement"}, nil},
	"FAQPage":        {[]string{"mainEntity"}, nil},
	"Recipe":         {[]string{"name", "image"}, []string{"recipeIngredient", "recipeInstructions"}},
	"Event":          {[]string{"name", "startDate", "location"}, []string{"image", "offers"}},
	"JobPosting":     {[]string{"title", "datePosted", "description", "hiringOrganization", "jobLocation"}, nil},
	"LocalBusiness":  {[]string{"name", "address"}, []string{"telephone", "openingHours"}},
	"Organization":   {[]string{"name"}, []string{"logo", "url"}},
	"VideoObject":    {[]string{"name", "thumbnailUrl", "uploadDate"}, []string{"description"}},
}

// Extract parses the enabled formats and validates the curated requirements.
// Returns nil when no structured-data format is enabled or none is present.
func Extract(body []byte, cfg *config.Config) *PageData {
	sd := &cfg.Extraction.StructuredData
	if !sd.JSONLD && !sd.Microdata && !sd.RDFa {
		return nil
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	data := &PageData{}
	if sd.JSONLD {
		extractJSONLD(root, data)
	}
	if sd.Microdata {
		extractMicrodata(root, data)
	}
	if sd.RDFa {
		extractRDFa(root, data)
	}
	if len(data.Formats) == 0 && len(data.ParseErrors) == 0 {
		return nil
	}
	return data
}

func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func attrValue(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// --- JSON-LD ---

func extractJSONLD(root *html.Node, data *PageData) {
	found := false
	walk(root, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "script" {
			return
		}
		if typ, _ := attrValue(n, "type"); !strings.EqualFold(typ, "application/ld+json") {
			return
		}
		var raw strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			raw.WriteString(c.Data)
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw.String()), &parsed); err != nil {
			data.ParseErrors = append(data.ParseErrors, "jsonld: "+err.Error())
			return
		}
		found = true
		walkJSONLD(parsed, data)
	})
	if found {
		data.Formats = append(data.Formats, "jsonld")
	}
}

// walkJSONLD handles objects, arrays and @graph containers.
func walkJSONLD(v any, data *PageData) {
	switch node := v.(type) {
	case []any:
		for _, item := range node {
			walkJSONLD(item, data)
		}
	case map[string]any:
		if graph, ok := node["@graph"]; ok {
			walkJSONLD(graph, data)
		}
		types := typeList(node["@type"])
		for _, t := range types {
			data.Types = append(data.Types, t)
			validateProps(t, func(prop string) bool {
				_, ok := node[prop]
				return ok
			}, data)
		}
		// nested entities
		for key, child := range node {
			if strings.HasPrefix(key, "@") {
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				walkJSONLD(child, data)
			}
		}
	}
}

func typeList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// --- Microdata ---

func extractMicrodata(root *html.Node, data *PageData) {
	found := false
	walk(root, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if _, scoped := attrValue(n, "itemscope"); !scoped {
			return
		}
		itemtype, _ := attrValue(n, "itemtype")
		if itemtype == "" {
			return
		}
		found = true
		typ := shortType(itemtype)
		data.Types = append(data.Types, typ)
		props := map[string]bool{}
		collectItemprops(n, props, true)
		validateProps(typ, func(p string) bool { return props[p] }, data)
	})
	if found {
		data.Formats = append(data.Formats, "microdata")
	}
}

// collectItemprops gathers itemprop names within an itemscope (not
// descending into nested itemscopes except to record them as properties).
func collectItemprops(n *html.Node, props map[string]bool, isRoot bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if prop, ok := attrValue(c, "itemprop"); ok {
				props[prop] = true
			}
			if _, scoped := attrValue(c, "itemscope"); scoped {
				continue // nested item: its own validation pass
			}
			collectItemprops(c, props, false)
		}
	}
}

// --- RDFa (typeof/property extraction only) ---

func extractRDFa(root *html.Node, data *PageData) {
	found := false
	walk(root, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		typeof, ok := attrValue(n, "typeof")
		if !ok || typeof == "" {
			return
		}
		found = true
		for t := range strings.FieldsSeq(typeof) {
			data.Types = append(data.Types, shortType(t))
		}
	})
	if found {
		data.Formats = append(data.Formats, "rdfa")
	}
}

func shortType(t string) string {
	t = strings.TrimSuffix(t, "/")
	if i := strings.LastIndexAny(t, "/#:"); i >= 0 {
		return t[i+1:]
	}
	return t
}

func validateProps(typ string, has func(string) bool, data *PageData) {
	req, ok := requirements[typ]
	if !ok {
		return
	}
	for _, p := range req.required {
		if !has(p) {
			data.Errors = append(data.Errors, fmt.Sprintf("%s: missing required property %q", typ, p))
		}
	}
	for _, p := range req.recommended {
		if !has(p) {
			data.Warnings = append(data.Warnings, fmt.Sprintf("%s: missing recommended property %q", typ, p))
		}
	}
}
