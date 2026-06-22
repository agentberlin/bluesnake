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
// reviewCount) — a group with none of its members present emits one error;
// `recAnyOf` is the warning equivalent (e.g. a Product Merchant Listing
// recommends one of the gtin family). `trigger` props gate feature eligibility:
// a type is only a rich-result candidate when every trigger prop is present (an
// empty trigger means always-validate), so bare/incidental markup emits nothing.
// `conditional` carries rich-result sub-features the entity only qualifies for
// when a gating property is present (e.g. a Product carrying offers is also a
// Merchant Listing, which adds its own required/recommended properties).
type typeReq struct {
	required    []string
	recommended []string
	trigger     []string
	anyOf       [][]string
	recAnyOf    [][]string
	conditional []condFeature
}

// condFeature is a rich-result sub-feature a primary entity qualifies for only
// when `when` is present, contributing `req`'s requirements on top of the base
// type's. Google validates the same @type against several features at once (a
// Product is a Product Snippet always, and a Merchant Listing once it carries
// offers); modelling the conditional feature inline keeps the data-driven table
// as the single source of truth. The sub-feature's `req` is emitted without
// re-checking the base trigger (the caller has already gated eligibility).
type condFeature struct {
	when string
	req  typeReq
}

// softwareApp is Google's Software App rich result. MobileApplication and
// WebApplication are SoftwareApplication subtypes Google validates identically;
// we match on the leaf @type, so each subtype needs its own row. `offers` and a
// rating/review are both required for eligibility. The `offers` object's own
// price is validated as an integral nested entity (offerValidatedParents includes
// the app types — a Software App offer missing price is a Rich Result error,
// measured on SF v24.1); priceCurrency there is value-conditional and omitted
// (see offerCurrencyOptional). Recommended: applicationCategory, operatingSystem.
// (Property bucketing is grounded in
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
	// A Product is ALWAYS a Google Product Snippet candidate: it needs a name, at
	// least one of review / aggregateRating / offers (the anyOf error SF emits on a
	// bare Product), and recommends each of those three. Carrying `offers` ALSO
	// makes it a Merchant Listing (the conditional sub-feature), which REQUIRES
	// `image` — an error, not a warning — and recommends `description` plus a `gtin`
	// family member. `image` is therefore NOT a snippet recommendation: a Product
	// without offers and without image emits nothing about image (matching SF; the
	// old model warned it unconditionally). The nested Offer's own merchant
	// recommendations (availability/itemCondition) live in offerReq /
	// offerMerchantParents, since they are Product-specific. Grounded on SF v24.1
	// probes (/tmp/bs-probes/sd).
	"Product": {
		required:    []string{"name"},
		anyOf:       [][]string{{"review", "aggregateRating", "offers"}},
		recommended: []string{"offers", "review", "aggregateRating"},
		conditional: []condFeature{{
			when: "offers", // a Product carrying offers is a Merchant Listing
			req: typeReq{
				required:    []string{"image"},
				recommended: []string{"description"},
				recAnyOf:    [][]string{{"gtin", "gtin8", "gtin12", "gtin13", "gtin14", "isbn"}},
			},
		}},
	},
	// headline is recommended, not required: Google's Article rich result
	// has no required properties, and SF reports a missing headline as a
	// Rich Result Validation *Warning* (measured on yonedalabs.com). Google's
	// Article docs recommend BOTH datePublished and dateModified; SF surfaces the
	// missing recommended date as dateModified, so both are listed (cross-checked
	// vs SF on baseten.co 2026-06-21 — without dateModified, a page carrying only
	// datePublished diverged from SF silently).
	"Article":        {recommended: []string{"headline", "image", "datePublished", "dateModified", "author"}},
	"NewsArticle":    {recommended: []string{"headline", "image", "datePublished", "dateModified", "author"}},
	"BlogPosting":    {recommended: []string{"headline", "image", "datePublished", "dateModified", "author"}},
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
	// reviewRating (the trigger); then `author` is required and `datePublished`
	// is recommended (a missing one is a Rich Result warning). The recommendation
	// is unconditional, NOT parent-dependent: SF v24.1 emits "'…/datePublished'
	// property is recommended for 'Review'" on both a standalone Review and one
	// nested inside a Product (/tmp/bs-probes/review). `itemReviewed` is required
	// by Google only for *standalone* reviews ("omit if nested"), and the vast
	// majority of reviews are nested inside a Product/App where the parent is the
	// reviewed item — requiring it unconditionally would reproduce the R6
	// over-warning regression, so it is deliberately omitted.
	"Review": {trigger: []string{"reviewRating"}, required: []string{"author"}, recommended: []string{"datePublished"}},
	// AggregateRating contributes star ratings: ratingValue is required and at
	// least one of ratingCount / reviewCount. itemReviewed omitted for the same
	// nesting reason as Review.
	"AggregateRating": {required: []string{"ratingValue"}, anyOf: [][]string{{"ratingCount", "reviewCount"}}},

	// --- Integral sub-entities validated when nested in a rich result (see
	// integralProps). These are NOT page primary entities; they are reached as
	// the value of an offers/review/reviewRating/aggregateRating property and
	// carry their own Google-required properties. Scope is REQUIRED properties
	// (Rich Result errors); merchant-listing recommended breadth (Offer
	// itemCondition/availability, Product gtin/description) is a separate gap.
	//
	// Offer: Google requires a price (or priceSpecification) and a priceCurrency
	// (or priceSpecification). priceCurrency is only a *warning* under Product
	// Snippet but a hard *error* under Product Merchant Listings — per the agreed
	// "strictest severity wins" rule it is modelled as required. Measured on SF
	// v24.1: offers w/o price ⇒ error, offers w/o priceCurrency ⇒ error.
	"Offer": {anyOf: [][]string{{"price", "priceSpecification"}, {"priceCurrency", "priceSpecification"}}},
	// AggregateOffer (IS-A Offer) prices a range with lowPrice/highPrice rather
	// than a single price, so it needs its own rule — resolving it to the plain
	// Offer rule would false-error a valid AggregateOffer on a missing `price`.
	"AggregateOffer": {anyOf: [][]string{{"lowPrice", "price", "priceSpecification"}, {"priceCurrency", "priceSpecification"}}},
	// Rating (a reviewRating's value object): ratingValue is required. Measured on
	// SF v24.1: a reviewRating lacking ratingValue ⇒ "ratingValue required for
	// Rating" error. AggregateRating IS-A Rating but is curated directly above
	// (its own ratingCount/reviewCount rule), so it never falls back to this.
	"Rating": {required: []string{"ratingValue"}},
}

// integralProps maps a property name to the schema.org type Google validates its
// value as part of the PARENT's rich result. Unlike reference properties
// (seller, publisher, author, brand, itemReviewed) whose values are identity
// stubs that legitimately carry only a name/url, these nested objects ARE
// validated for their own required properties — e.g. a Product's offers must
// carry a price, a Review's reviewRating must carry a ratingValue. The mapped
// type is the type Google infers when the nested object omits an explicit @type
// (it is inferred from the property). This whitelist is the precise boundary of
// the R6 over-warn guard: everything NOT listed here is a reference stub and is
// recorded for the type set but never validated.
var integralProps = map[string]string{
	"offers":          "Offer",
	"review":          "Review",
	"reviews":         "Review",
	"reviewRating":    "Rating",
	"aggregateRating": "AggregateRating",
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
					walkJSONLD(parsed, data, true, "", "")
					return
				}
			}
			data.ParseErrors = append(data.ParseErrors, "jsonld: "+err.Error())
			return
		}
		found = true
		walkJSONLD(parsed, data, true, "", "")
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
// for entities that carry their own rich-result requirements — the page's
// primary entities (top-level nodes and @graph members) and integral nested
// sub-entities reached via an integralProps property (offers→Offer,
// review→Review, reviewRating→Rating). It is false for reference/identity stubs
// reached via any other property (offers.seller, publisher, author, brand, …):
// those carry only a name/url, so their types are recorded for the type set but
// never validated — validating a nested Store/Restaurant for `address` is an
// R6-class false error. `implied` is the type Google infers when a validated
// node omits an explicit @type (e.g. a bare `offers: {price: …}` is an Offer);
// it is "" for primary entities, which always declare a type. `parent` is the
// integral node's parent rich-result root, used to make a nested Offer's
// requirements parent-aware (see offerCurrencyOptional); it is "" for primary
// entities and for integral types whose requirements are parent-independent.
func walkJSONLD(v any, data *PageData, validate bool, implied, parent string) {
	switch node := v.(type) {
	case []any:
		for _, item := range node {
			walkJSONLD(item, data, validate, implied, parent)
		}
	case map[string]any:
		if graph, ok := node["@graph"]; ok {
			walkJSONLD(graph, data, true, "", "") // @graph members are primary entities
		}
		types := typeList(node["@type"])
		for i := range types {
			types[i] = shortType(types[i]) // normalize full-URL / prefixed @type
		}
		data.Types = append(data.Types, types...)
		if validate {
			vTypes := types
			if len(vTypes) == 0 && implied != "" {
				vTypes = []string{implied} // Google infers the type from the property
			}
			validateNode(vTypes, func(prop string) bool {
				_, ok := node[prop]
				return ok
			}, data, parent)
		}
		// Recurse: integral sub-entities of a VALIDATED node are validated against
		// their own requirements (parent-aware — see integralValidatedUnder);
		// every other nested object records its type only. Gating on `validate`
		// keeps a reference stub's whole subtree unvalidated — a seller Store
		// (itself a stub) must not have its own nested rating validated either.
		var parentRoots []string
		if validate {
			parentRoots = resolvedRoots(types)
		}
		for key, child := range node {
			if strings.HasPrefix(key, "@") {
				continue
			}
			if impliedType, integral := integralProps[key]; validate && integral && integralValidatedUnder(key, parentRoots) {
				childParent := ""
				if key == "offers" {
					childParent = offerParentRoot(parentRoots) // parent-aware Offer requirements
				}
				walkJSONLD(child, data, true, impliedType, childParent)
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				walkJSONLD(child, data, false, "", "")
			}
		}
	}
}

// offerValidatedParents are the parent rich-result roots under which Google
// validates a nested Offer's REQUIRED properties (a missing one is a Rich Result
// error). Google's Offer requirements are per-feature, so this is curated to the
// features that error on offer props (measured on SF v24.1 — message text from
// the Structured Data Validation Errors export):
//   - Product (Snippet + Merchant Listings): require a price AND a priceCurrency.
//   - Software App (+ Web/MobileApplication subtypes): require a price; the
//     priceCurrency requirement is value-conditional (only when price > 0).
//
// An Event offer is deliberately absent: its price/priceCurrency are RECOMMENDED
// (warnings), not errors, so validating it for required props would over-error.
var offerValidatedParents = map[string]bool{
	"Product": true, "SoftwareApplication": true, "WebApplication": true, "MobileApplication": true,
}

// offerCurrencyOptional are the parent roots under which a nested Offer's
// priceCurrency is NOT an unconditional error. Under the Google Software App
// feature, priceCurrency is required only when price > 0 ("'priceCurrency' is
// recommended if 'price' > 0") — a value condition the presence-based engine
// cannot test. Rather than over-error a free app (price 0, no currency), the
// priceCurrency requirement is dropped for these parents; the (rare) paid-app-
// without-currency case is a deliberate under-report. Product keeps priceCurrency
// unconditionally required, as does the standalone/unknown default ("").
var offerCurrencyOptional = map[string]bool{
	"SoftwareApplication": true, "WebApplication": true, "MobileApplication": true,
}

// offerMerchantParents are the parent rich-result roots under which a nested
// Offer carries Google's Merchant Listing RECOMMENDED properties (availability,
// itemCondition → warnings). Only Product: an Offer under a Software App
// recommends neither (its price is required outright), and an Event offer has a
// different recommended set (price/priceCurrency) — both measured on SF v24.1, so
// the merchant recommendations must not leak onto them. AggregateOffer is left out
// deliberately (no probe coverage; standalone/AggregateOffer merchant depth is a
// separate open gap).
var offerMerchantParents = map[string]bool{"Product": true}

// offerReq returns an Offer's (or AggregateOffer's) parent-aware requirements:
// the base price/currency rule, relaxed for currency-optional parents (Software
// App), plus the Merchant Listing recommended set (availability, itemCondition)
// when the Offer hangs off a Product. Centralizing this keeps Google's per-feature
// Offer profiles in one place rather than scattered across validateProps.
func offerReq(root, parent string) typeReq {
	req := requirements[root]
	if offerCurrencyOptional[parent] {
		req = dropPriceCurrencyGroup(req)
	}
	if root == "Offer" && offerMerchantParents[parent] {
		req.recommended = append(slices.Clone(req.recommended), "availability", "itemCondition")
	}
	return req
}

// integralValidatedUnder reports whether Google validates the value of integral
// property `prop` as part of the parent's rich result. `offers` is parent-aware
// (validated only under an offer-validated feature — see offerValidatedParents);
// the rating/review family carries parent-independent requirements (a Rating
// always needs a ratingValue, a Review an author), so it validates under any
// rich-result parent but never under a non-candidate one (empty parentRoots).
func integralValidatedUnder(prop string, parentRoots []string) bool {
	if len(parentRoots) == 0 {
		return false // parent is not itself a rich-result candidate
	}
	if prop == "offers" {
		for _, r := range parentRoots {
			if offerValidatedParents[r] {
				return true
			}
		}
		return false
	}
	return true
}

// offerParentRoot picks the parent rich-result root that governs a nested
// offer's requirements. When the parent node is co-typed under several
// offer-validated features, the strictest wins — a currency-REQUIRING parent
// (e.g. Product) dominates a currency-optional one (Software App) so the offer is
// never under-required on currency. Returns "" only when no parent qualifies
// (callers gate on integralValidatedUnder first, so the offer path always has one).
func offerParentRoot(parentRoots []string) string {
	rep := ""
	for _, r := range parentRoots {
		if !offerValidatedParents[r] {
			continue
		}
		if !offerCurrencyOptional[r] {
			return r // a currency-requiring parent dominates
		}
		rep = r
	}
	return rep
}

// resolvedRoots returns the curated rich-result roots a type set validates as
// (deduped, collapsed to most-specific). Empty when the node is not a
// rich-result candidate under the curated requirements.
func resolvedRoots(types []string) []string {
	seen := map[string]bool{}
	var roots []string
	for _, leaf := range types {
		if r := resolveType(leaf); r != "" && !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	return mostSpecific(roots)
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
		// A nested itemscope (the value of a parent item's itemprop) is validated
		// only when it is an integral sub-entity — introduced by an integralProps
		// itemprop (itemprop="offers"/"review"/"reviewRating"/…). A nested scope
		// reached via any other itemprop (a seller/publisher reference stub)
		// records its types but is not validated (same R6 scoping as JSON-LD).
		// The full-tree walk visits every itemscope, so deeper integral nesting
		// (Review→reviewRating→Rating) is reached as its own node.
		parent := "" // parent rich-result root, for parent-aware Offer requirements
		if hasItemscopeAncestor(n) {
			prop, _ := attrValue(n, "itemprop")
			if _, integral := integralProps[prop]; !integral {
				return
			}
			// Every itemscope ancestor up to the top-level item must also be
			// integral, else n is buried inside a reference stub (e.g. a seller
			// Store's rating) and must not be validated (parity with JSON-LD's
			// validate-gated recursion).
			if !integralItemscopeChain(n) {
				return
			}
			ancestorRoots := resolvedRoots(nearestItemscopeAncestorTypes(n))
			if !integralValidatedUnder(prop, ancestorRoots) {
				return
			}
			if prop == "offers" {
				parent = offerParentRoot(ancestorRoots)
			}
		}
		props := map[string]bool{}
		collectItemprops(n, props, true)
		validateNode(types, func(p string) bool { return props[p] }, data, parent)
	})
	if found {
		data.Formats = append(data.Formats, "microdata")
	}
}

// integralItemscopeChain reports whether every itemscope ancestor of n up to
// the top-level item was reached via an integral itemprop. A non-integral link
// anywhere in the chain means n sits inside a reference stub (e.g. a seller
// Store's nested rating) and must not be validated — the microdata analogue of
// JSON-LD's validate-gated recursion. The top-level item carries no itemprop,
// which terminates the walk as integral.
func integralItemscopeChain(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if _, scoped := attrValue(p, "itemscope"); !scoped {
			continue
		}
		prop, _ := attrValue(p, "itemprop")
		if prop == "" {
			return true // top-level item reached: chain is fully integral
		}
		if _, integral := integralProps[prop]; !integral {
			return false
		}
	}
	return true
}

// nearestItemscopeAncestorTypes returns the short-form @types of the closest
// enclosing itemscope element (the nested item's parent entity), used to make
// nested-Offer validation parent-aware. Empty when there is no typed ancestor.
func nearestItemscopeAncestorTypes(n *html.Node) []string {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		if _, scoped := attrValue(p, "itemscope"); !scoped {
			continue
		}
		it, _ := attrValue(p, "itemtype")
		if it == "" {
			return nil
		}
		var ts []string
		for t := range strings.FieldsSeq(it) {
			ts = append(ts, shortType(t))
		}
		return ts
	}
	return nil
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
func validateNode(types []string, has func(string) bool, data *PageData, parent string) {
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
		validateProps(rep[root], root, has, data, parent)
	}
}

// validateProps emits errors/warnings for one node against the curated `root`'s
// requirements, attributing them to the page's actual `leaf` type. `parent` is
// the node's parent rich-result root (an integral Offer/AggregateOffer is
// validated parent-aware — see offerReq). After the base requirements, any
// conditional sub-feature whose gating property is present contributes its own
// requirements too (e.g. a Product carrying offers is also a Merchant Listing).
func validateProps(leaf, root string, has func(string) bool, data *PageData, parent string) {
	req := requirements[root]
	if root == "Offer" || root == "AggregateOffer" {
		req = offerReq(root, parent) // parent-aware Offer profile (currency + merchant recs)
	}
	// Feature-eligibility gate: not a rich-result candidate unless every
	// trigger property is present (no trigger ⇒ always eligible).
	for _, p := range req.trigger {
		if !has(p) {
			return
		}
	}
	emitRequirements(leaf, req, has, data)
	// Conditional sub-features: a gating property's presence opts the entity into
	// an additional rich-result feature with its own requirements. The base
	// trigger has already passed, so the sub-feature is emitted directly.
	for _, cf := range req.conditional {
		if has(cf.when) {
			emitRequirements(leaf, cf.req, has, data)
		}
	}
}

// emitRequirements appends the error/warning messages for one requirement set,
// attributed to `leaf`. required props and unsatisfied `anyOf` groups are errors;
// recommended props and unsatisfied `recAnyOf` groups are warnings. It does NOT
// apply the trigger gate — eligibility is the caller's responsibility — so it can
// be reused for conditional sub-features.
func emitRequirements(leaf string, req typeReq, has func(string) bool, data *PageData) {
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
	for _, group := range req.recAnyOf {
		if !slices.ContainsFunc(group, has) {
			data.Warnings = append(data.Warnings,
				fmt.Sprintf("%s: missing recommended property (one of %s)", leaf, strings.Join(group, ", ")))
		}
	}
}

// dropPriceCurrencyGroup returns r without the anyOf group carrying the
// priceCurrency requirement, leaving the price group (and any others) intact. It
// relaxes an Offer rule under a parent feature where priceCurrency is value-
// conditional rather than an unconditional error. Deriving from the base rule
// (requirements["Offer"]/["AggregateOffer"]) keeps a single source of truth: the
// price requirement still tracks the offer's own type (price vs lowPrice).
func dropPriceCurrencyGroup(r typeReq) typeReq {
	out := typeReq{required: r.required, recommended: r.recommended, trigger: r.trigger, recAnyOf: r.recAnyOf, conditional: r.conditional}
	for _, g := range r.anyOf {
		if slices.Contains(g, "priceCurrency") {
			continue
		}
		out.anyOf = append(out.anyOf, g)
	}
	return out
}
