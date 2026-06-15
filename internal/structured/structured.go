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

	"github.com/agentberlin/bluesnake/internal/config"
	"golang.org/x/net/html"
)

// PageData is the structured-data result for one page.
type PageData struct {
	Formats     []string `json:"formats,omitempty"` // jsonld | microdata | rdfa
	Types       []string `json:"types,omitempty"`
	ParseErrors []string `json:"parse_errors,omitempty"`
	// Recovered notes blocks that were syntactically invalid but salvaged by a
	// lenient retry (e.g. raw control chars escaped). The data IS extracted, but
	// the source is technically invalid — Google/SF tolerate this silently;
	// bluesnake surfaces it so the owner can fix the source.
	Recovered []string `json:"recovered,omitempty"`
	Errors    []string `json:"errors,omitempty"`   // missing required properties
	Warnings  []string `json:"warnings,omitempty"` // missing recommended properties
}

// requirements: Google rich-results required/recommended properties for the
// curated feature subset. `trigger` props gate feature eligibility: Google's
// Rich Results Test only validates a type for a feature when the feature's
// trigger properties are present (e.g. Organization is a "Logo" candidate only
// when it carries a logo). An item missing any trigger property is not a
// rich-result candidate at all and emits no errors/warnings — matching
// Screaming Frog, which on bare boilerplate Organization markup (logo absent)
// reports feat=None / 0 warnings. An empty trigger means always-validate.
var requirements = map[string]struct{ required, recommended, trigger []string }{
	"Product": {[]string{"name"}, []string{"image", "offers", "review", "aggregateRating"}, nil},
	// headline is recommended, not required: Google's Article rich result
	// has no required properties, and SF reports a missing headline as a
	// Rich Result Validation *Warning* (measured on yonedalabs.com)
	"Article":        {nil, []string{"headline", "image", "datePublished", "author"}, nil},
	"NewsArticle":    {nil, []string{"headline", "image", "datePublished", "author"}, nil},
	"BlogPosting":    {nil, []string{"headline", "image", "datePublished", "author"}, nil},
	"BreadcrumbList": {[]string{"itemListElement"}, nil, nil},
	"FAQPage":        {[]string{"mainEntity"}, nil, nil},
	"Recipe":         {[]string{"name", "image"}, []string{"recipeIngredient", "recipeInstructions"}, nil},
	"Event":          {[]string{"name", "startDate", "location"}, []string{"image", "offers"}, nil},
	"JobPosting":     {[]string{"title", "datePosted", "description", "hiringOrganization", "jobLocation"}, nil, nil},
	"LocalBusiness":  {[]string{"name", "address"}, []string{"telephone", "openingHours"}, nil},
	// Organization's Logo rich result is keyed on `logo`: SF only validates
	// (and warns on a missing recommended `url`) when a logo is present; without
	// a logo it is not a Logo candidate and emits nothing. `name` is therefore
	// not flagged as a hard requirement here (SF reports 0 errors on every
	// Organization page measured on infisical.com / trigger.dev).
	"Organization": {nil, []string{"url"}, []string{"logo"}},
	"VideoObject":  {[]string{"name", "thumbnailUrl", "uploadDate"}, []string{"description"}, nil},
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
		rawStr := raw.String()
		var parsed any
		if err := json.Unmarshal([]byte(rawStr), &parsed); err != nil {
			// Google's (and Screaming Frog's) JSON-LD parser tolerates raw
			// unescaped control characters inside string literals — common on
			// real sites (e.g. a newline in a clinic address). Go's
			// encoding/json rejects them ("invalid control character"). Retry
			// once with control chars escaped before reporting a parse error,
			// so we extract the structured data SF/Google extract instead of
			// dropping the whole block. (modernanimal.com clinic pages.)
			if cleaned := escapeJSONControlChars(rawStr); cleaned != rawStr {
				if err2 := json.Unmarshal([]byte(cleaned), &parsed); err2 == nil {
					found = true
					data.Recovered = append(data.Recovered,
						"jsonld: invalid raw control characters escaped to recover")
					walkJSONLD(parsed, data)
					return
				}
			}
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

// escapeJSONControlChars escapes raw control characters (U+0000–U+001F) that
// appear INSIDE JSON string literals — unescaped newlines/tabs that Google's
// and Screaming Frog's lenient JSON-LD parsers accept but encoding/json
// rejects. Structural characters and whitespace between tokens are untouched
// (a control char outside a string is left as-is). Returns the input unchanged
// when there is nothing inside-string to escape.
func escapeJSONControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString, escaped, changed := false, false, false
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inString:
			b.WriteRune(r)
			escaped = true
		case r == '"':
			inString = !inString
			b.WriteRune(r)
		case inString && r < 0x20:
			changed = true
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	if !changed {
		return s
	}
	return b.String()
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
	// Feature-eligibility gate: not a rich-result candidate unless every
	// trigger property is present (no trigger ⇒ always eligible).
	for _, p := range req.trigger {
		if !has(p) {
			return
		}
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
