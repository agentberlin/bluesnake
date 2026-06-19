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
	"slices"
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

// typeReq encodes one schema.org type's Google rich-result property
// requirements. A missing `required` prop emits an error; a missing
// `recommended` prop emits a warning. `anyOf` groups model Google's
// "at least one of" requirements (e.g. an AggregateRating needs ratingCount OR
// reviewCount) — a group with none of its members present emits one error.
// `trigger` props gate feature eligibility: a type is only a rich-result
// candidate when every trigger prop is present (an empty trigger means
// always-validate), so bare/incidental markup emits nothing.
type typeReq struct {
	required    []string
	recommended []string
	trigger     []string
	anyOf       [][]string
}

// softwareApp is Google's Software App rich result. MobileApplication and
// WebApplication are SoftwareApplication subtypes Google validates identically;
// we match on the leaf @type, so each subtype needs its own row. `offers`
// (price) and a rating/review are both required for eligibility; the curated
// engine checks top-level property presence, so we require the `offers` object
// itself (not the nested `offers.price`). Recommended: applicationCategory,
// operatingSystem. (Property bucketing is grounded in
// developers.google.com/search/docs/appearance/structured-data/software-app.)
var softwareApp = typeReq{
	required:    []string{"name", "offers"},
	recommended: []string{"applicationCategory", "operatingSystem"},
	anyOf:       [][]string{{"aggregateRating", "review"}},
}

// requirements: Google rich-results required/recommended properties for the
// curated feature subset. The validation engine is data-driven, so extending
// coverage is a table edit. `trigger` props gate feature eligibility: Google's
// Rich Results Test only validates a type for a feature when the feature's
// trigger properties are present (e.g. Organization is a "Logo" candidate only
// when it carries a logo). An item missing any trigger property is not a
// rich-result candidate at all and emits no errors/warnings — matching
// Screaming Frog, which on bare boilerplate Organization markup (logo absent)
// reports feat=None / 0 warnings. An empty trigger means always-validate.
//
// HowTo is deliberately absent: Google deprecated HowTo rich results in
// Sep 2023 (no longer shown in Search), so validating it would chase a stale
// feature rather than correct current behaviour.
var requirements = map[string]typeReq{
	"Product": {required: []string{"name"}, recommended: []string{"image", "offers", "review", "aggregateRating"}},
	// headline is recommended, not required: Google's Article rich result
	// has no required properties, and SF reports a missing headline as a
	// Rich Result Validation *Warning* (measured on yonedalabs.com)
	"Article":        {recommended: []string{"headline", "image", "datePublished", "author"}},
	"NewsArticle":    {recommended: []string{"headline", "image", "datePublished", "author"}},
	"BlogPosting":    {recommended: []string{"headline", "image", "datePublished", "author"}},
	"BreadcrumbList": {required: []string{"itemListElement"}},
	"FAQPage":        {required: []string{"mainEntity"}},
	"Recipe":         {required: []string{"name", "image"}, recommended: []string{"recipeIngredient", "recipeInstructions"}},
	"Event":          {required: []string{"name", "startDate", "location"}, recommended: []string{"image", "offers"}},
	"JobPosting":     {required: []string{"title", "datePosted", "description", "hiringOrganization", "jobLocation"}},
	"LocalBusiness":  {required: []string{"name", "address"}, recommended: []string{"telephone", "openingHours"}},
	// Organization's Logo rich result is keyed on `logo`: SF only validates
	// (and warns on a missing recommended `url`) when a logo is present; without
	// a logo it is not a Logo candidate and emits nothing. `name` is therefore
	// not flagged as a hard requirement here (SF reports 0 errors on every
	// Organization page measured on infisical.com / trigger.dev).
	"Organization": {recommended: []string{"url"}, trigger: []string{"logo"}},
	"VideoObject":  {required: []string{"name", "thumbnailUrl", "uploadDate"}, recommended: []string{"description"}},

	"SoftwareApplication": softwareApp,
	"WebApplication":      softwareApp,
	"MobileApplication":   softwareApp,
	// Review snippet: a Review is only a candidate when it carries a
	// reviewRating (the trigger); then `author` is required. `itemReviewed` is
	// required by Google only for *standalone* reviews ("omit if nested"), and
	// the vast majority of reviews are nested inside a Product/App where the
	// parent is the reviewed item — requiring it unconditionally would
	// reproduce the R6 over-warning regression, so it is deliberately omitted.
	"Review": {trigger: []string{"reviewRating"}, required: []string{"author"}},
	// AggregateRating contributes star ratings: ratingValue is required and at
	// least one of ratingCount / reviewCount. itemReviewed omitted for the same
	// nesting reason as Review.
	"AggregateRating": {required: []string{"ratingValue"}, anyOf: [][]string{{"ratingCount", "reviewCount"}}},
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
	// The same logical entity can appear twice (e.g. a Review both in @graph and
	// referenced inline), validating to identical messages; collapse them so a
	// page reports each finding once.
	data.Errors = dedupStrings(data.Errors)
	data.Warnings = dedupStrings(data.Warnings)
	return data
}

// dedupStrings removes duplicate strings, preserving first-seen order. Returns
// the input unchanged when there are no duplicates (the common case).
func dedupStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
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
					walkJSONLD(parsed, data, true)
					return
				}
			}
			data.ParseErrors = append(data.ParseErrors, "jsonld: "+err.Error())
			return
		}
		found = true
		walkJSONLD(parsed, data, true)
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

// walkJSONLD handles objects, arrays and @graph containers. `validate` is true
// for the page's primary entities (top-level nodes and @graph members) and
// false for entities reached as a property value (offers.seller, publisher,
// author, …): those are reference/identity stubs that legitimately carry only a
// name/url, so their types are recorded for the type set but never validated —
// Google scopes a rich-result's required properties to the primary entity, and
// validating a nested Store/Restaurant for `address` is an R6-class false error.
func walkJSONLD(v any, data *PageData, validate bool) {
	switch node := v.(type) {
	case []any:
		for _, item := range node {
			walkJSONLD(item, data, validate)
		}
	case map[string]any:
		if graph, ok := node["@graph"]; ok {
			walkJSONLD(graph, data, true) // @graph members are primary entities
		}
		types := typeList(node["@type"])
		for i := range types {
			types[i] = shortType(types[i]) // normalize full-URL / prefixed @type
		}
		data.Types = append(data.Types, types...)
		if validate {
			validateNode(types, func(prop string) bool {
				_, ok := node[prop]
				return ok
			}, data)
		}
		// Nested property values record their types only — never validated.
		for key, child := range node {
			if strings.HasPrefix(key, "@") {
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				walkJSONLD(child, data, false)
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
		// itemtype may carry several space-separated type URLs (e.g.
		// "…/Product …/IndividualProduct"); record and validate them all, the
		// same way RDFa's typeof is split — a single shortType would keep only
		// the last token's leaf and make the outcome token-order-dependent.
		var types []string
		for t := range strings.FieldsSeq(itemtype) {
			types = append(types, shortType(t))
		}
		data.Types = append(data.Types, types...)
		// A nested itemscope (the value of a parent item's itemprop) is a
		// reference stub like a nested JSON-LD entity — record its types but do
		// not validate it for required properties (same primary-entity scoping).
		if hasItemscopeAncestor(n) {
			return
		}
		props := map[string]bool{}
		collectItemprops(n, props, true)
		validateNode(types, func(p string) bool { return props[p] }, data)
	})
	if found {
		data.Formats = append(data.Formats, "microdata")
	}
}

// hasItemscopeAncestor reports whether n sits inside another itemscope element,
// i.e. n is a nested microdata item rather than a top-level one.
func hasItemscopeAncestor(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if _, ok := attrValue(p, "itemscope"); ok {
			return true
		}
	}
	return false
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

// validateNode validates one structured-data node's (already shortType-
// normalized) @type set. Each declared type resolves to its curated rich-result
// root; redundant supertype roots are then collapsed (mostSpecific) so a
// ["Restaurant"] node validates once as LocalBusiness, a ["NewsArticle","Article"]
// node validates once as NewsArticle, and a ["VideoGame","SoftwareApplication"]
// node still validates as SoftwareApplication (the explicit app co-type) even
// though VideoGame alone is excluded. The leaf type name is reported in the
// issue so the owner sees the markup they actually wrote.
func validateNode(types []string, has func(string) bool, data *PageData) {
	rep := map[string]string{} // curated root -> representative leaf for the message
	var roots []string
	for _, leaf := range types {
		root := resolveType(leaf)
		if root == "" {
			continue
		}
		if _, seen := rep[root]; !seen {
			rep[root] = leaf
			roots = append(roots, root)
		}
	}
	roots = mostSpecific(roots)
	slices.Sort(roots) // deterministic emission order
	for _, root := range roots {
		validateProps(rep[root], root, has, data)
	}
}

// validateProps emits errors/warnings for one node against the curated `root`'s
// requirements, attributing them to the page's actual `leaf` type.
func validateProps(leaf, root string, has func(string) bool, data *PageData) {
	req := requirements[root]
	// Feature-eligibility gate: not a rich-result candidate unless every
	// trigger property is present (no trigger ⇒ always eligible).
	for _, p := range req.trigger {
		if !has(p) {
			return
		}
	}
	for _, p := range req.required {
		if !has(p) {
			data.Errors = append(data.Errors, fmt.Sprintf("%s: missing required property %q", leaf, p))
		}
	}
	for _, group := range req.anyOf {
		if !slices.ContainsFunc(group, has) {
			data.Errors = append(data.Errors,
				fmt.Sprintf("%s: missing required property (one of %s)", leaf, strings.Join(group, ", ")))
		}
	}
	for _, p := range req.recommended {
		if !has(p) {
			data.Warnings = append(data.Warnings, fmt.Sprintf("%s: missing recommended property %q", leaf, p))
		}
	}
}
