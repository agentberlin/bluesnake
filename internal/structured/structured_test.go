package structured

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func extract(t *testing.T, body string, enable func(*config.StructuredDataConfig)) *PageData {
	t.Helper()
	cfg := config.Default()
	enable(&cfg.Extraction.StructuredData)
	return Extract([]byte(body), cfg)
}

func TestDisabledReturnsNil(t *testing.T) {
	cfg := config.Default()
	if Extract([]byte(`<html></html>`), cfg) != nil {
		t.Error("disabled extraction must return nil")
	}
}

func TestJSONLD(t *testing.T) {
	body := `<html><head><script type="application/ld+json">
	{"@context":"https://schema.org","@type":"Product","name":"Widget","offers":{"@type":"Offer","price":"10","priceCurrency":"USD"}}
	</script></head><body></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil || len(d.Formats) != 1 || d.Formats[0] != "jsonld" {
		t.Fatalf("data = %+v", d)
	}
	hasType := func(want string) bool {
		for _, typ := range d.Types {
			if typ == want {
				return true
			}
		}
		return false
	}
	if !hasType("Product") || !hasType("Offer") {
		t.Errorf("types = %v", d.Types)
	}
	// A Product carrying offers is a Merchant Listing, which REQUIRES image —
	// missing it is a Rich Result error (measured on SF v24.1, not a warning).
	if !hasIssue(d.Errors, "image") {
		t.Errorf("merchant-listing Product w/o image: errors=%v, want an image required error", d.Errors)
	}
	// description / gtin (and the Offer's availability / itemCondition) are the
	// merchant-listing recommended properties.
	if len(d.Warnings) == 0 {
		t.Error("expected recommended-property warnings")
	}
}

// Organization is a "Logo" rich-result candidate only when it carries a logo.
// Screaming Frog warns on a missing recommended `url` ONLY for logo-bearing
// Organization markup; a bare boilerplate Organization (no logo) is not a
// candidate and emits nothing — it must NOT produce a "missing logo" warning.
// (Measured: infisical.com 1927 / trigger.dev 291 false logo warnings before
// the feature-eligibility gate.)
func TestOrganizationLogoEligibilityGate(t *testing.T) {
	jsonld := func(org string) string {
		return `<html><head><script type="application/ld+json">` + org +
			`</script></head><body></body></html>`
	}
	warnContains := func(d *PageData, sub string) bool {
		for _, w := range d.Warnings {
			if strings.Contains(w, sub) {
				return true
			}
		}
		return false
	}

	// No logo ⇒ not a Logo candidate ⇒ no errors, no warnings at all.
	d := extract(t, jsonld(`{"@context":"https://schema.org","@type":"Organization","name":"Acme"}`),
		func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Warnings) != 0 || len(d.Errors) != 0 {
		t.Errorf("logo-less Organization: errors=%v warnings=%v, want none", d.Errors, d.Warnings)
	}

	// Logo present but url missing ⇒ eligible ⇒ warn the missing recommended url.
	d = extract(t, jsonld(`{"@context":"https://schema.org","@type":"Organization","name":"Acme","logo":"https://x/l.png"}`),
		func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if !warnContains(d, "url") {
		t.Errorf("logo-bearing Organization missing url: warnings=%v, want missing-url warning", d.Warnings)
	}
	if warnContains(d, "logo") {
		t.Errorf("must never warn about a missing logo: warnings=%v", d.Warnings)
	}

	// Logo + url present ⇒ eligible, fully satisfied ⇒ no warnings.
	d = extract(t, jsonld(`{"@context":"https://schema.org","@type":"Organization","name":"Acme","logo":"https://x/l.png","url":"https://x"}`),
		func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if len(d.Warnings) != 0 {
		t.Errorf("complete Organization: warnings=%v, want none", d.Warnings)
	}
}

// Google's and SF's JSON-LD parsers tolerate raw unescaped control characters
// inside string literals (a newline in an address, common on real sites —
// modernanimal.com clinic pages). bluesnake must retry with them escaped and
// still extract the data, not drop the whole block as a parse error.
func TestJSONLDLenientControlChars(t *testing.T) {
	body := "<html><head><script type=\"application/ld+json\">" +
		"{\"@context\":\"https://schema.org\",\"@type\":\"VeterinaryCare\"," +
		"\"name\":\"Clinic\",\"address\":\"123 Main St\nSuite 4\"}" +
		"</script></head><body></body></html>"
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.ParseErrors) != 0 {
		t.Errorf("parse errors = %v, want none (raw control char must be tolerated)", d.ParseErrors)
	}
	var ok bool
	for _, ty := range d.Types {
		if ty == "VeterinaryCare" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("types = %v, want VeterinaryCare extracted", d.Types)
	}
	// The data is recovered, but the source JSON-LD is technically invalid —
	// surface that to the owner (Google/SF tolerate it silently; we don't).
	if len(d.Recovered) == 0 {
		t.Error("Recovered = empty, want a note that invalid JSON-LD was leniently recovered")
	}
}

// Valid JSON-LD must NOT be flagged as recovered.
func TestJSONLDValidNotMarkedRecovered(t *testing.T) {
	body := `<html><head><script type="application/ld+json">
	{"@context":"https://schema.org","@type":"Article","headline":"Hi","author":"x"}
	</script></head><body></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Recovered) != 0 {
		t.Errorf("Recovered = %v, want none for valid JSON-LD", d.Recovered)
	}
}

func TestJSONLDGraphAndMissingRequired(t *testing.T) {
	body := `<html><head><script type="application/ld+json">
	{"@context":"https://schema.org","@graph":[{"@type":"Article","author":"x"}]}
	</script></head></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	// Article's headline is recommended (not required): SF reports its
	// absence as a Rich Result Validation Warning, never an error
	if len(d.Errors) != 0 {
		t.Errorf("errors = %v, want none", d.Errors)
	}
	var headlineWarn bool
	for _, w := range d.Warnings {
		if strings.Contains(w, "headline") {
			headlineWarn = true
		}
	}
	if !headlineWarn {
		t.Errorf("warnings = %v, want missing-headline warning", d.Warnings)
	}
}

// Google's Article rich result recommends BOTH datePublished and dateModified.
// SF's Google-Article validator surfaces the missing recommended date property
// as dateModified; bluesnake historically only checked datePublished, so a page
// that carries datePublished but lacks dateModified slipped past silently
// (latent divergence found cross-checking vs SF on baseten.co 2026-06-21, where
// every Article was missing both dates so the URL counts happened to coincide).
// Both dates are now recommended for the Article family.
func TestArticleDateModifiedRecommended(t *testing.T) {
	// Has datePublished but NOT dateModified ⇒ must warn about dateModified,
	// and never as an error (Article has no required properties).
	d := jsonld(t, `{"@context":"https://schema.org","@type":"Article","headline":"H","image":"i.jpg","author":"A","datePublished":"2026-01-01"}`)
	if len(d.Errors) != 0 {
		t.Errorf("errors = %v, want none (Article has no required props)", d.Errors)
	}
	if !hasIssue(d.Warnings, "dateModified") {
		t.Errorf("warnings = %v, want a missing-dateModified warning", d.Warnings)
	}
	if hasIssue(d.Warnings, "datePublished") {
		t.Errorf("warnings = %v, datePublished is present so must not warn on it", d.Warnings)
	}
	// Has both dates (plus the other recommended props) ⇒ no date warnings.
	d2 := jsonld(t, `{"@context":"https://schema.org","@type":"Article","headline":"H","image":"i.jpg","author":"A","datePublished":"2026-01-01","dateModified":"2026-02-02"}`)
	if hasIssue(d2.Warnings, "datePublished") || hasIssue(d2.Warnings, "dateModified") {
		t.Errorf("warnings = %v, want no date warnings when both dates present", d2.Warnings)
	}
}

func TestJSONLDParseError(t *testing.T) {
	body := `<html><head><script type="application/ld+json">{not json}</script></head></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil || len(d.ParseErrors) != 1 {
		t.Fatalf("data = %+v", d)
	}
}

func TestMicrodata(t *testing.T) {
	body := `<html><body>
	<div itemscope itemtype="https://schema.org/Recipe">
	  <span itemprop="name">Cake</span>
	</div></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.Microdata = true })
	if d == nil || d.Formats[0] != "microdata" || d.Types[0] != "Recipe" {
		t.Fatalf("data = %+v", d)
	}
	// image required but missing
	found := false
	for _, e := range d.Errors {
		if strings.Contains(e, "image") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %v", d.Errors)
	}
}

func TestRDFa(t *testing.T) {
	body := `<html><body vocab="https://schema.org/" typeof="Organization">
	<span property="name">ACME</span></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.RDFa = true })
	if d == nil || d.Formats[0] != "rdfa" || d.Types[0] != "Organization" {
		t.Fatalf("data = %+v", d)
	}
}

func TestNonePresent(t *testing.T) {
	d := extract(t, `<html><body><p>plain</p></body></html>`, func(s *config.StructuredDataConfig) {
		s.JSONLD, s.Microdata, s.RDFa = true, true, true
	})
	if d != nil {
		t.Errorf("no structured data must yield nil, got %+v", d)
	}
}

// --- Rich-result matrix breadth: SoftwareApplication / Review / AggregateRating ---
// Grounded in Google's current rich-results docs (review-snippet, software-app);
// HowTo is intentionally omitted (Google deprecated HowTo rich results in
// Sep 2023). See structured.go `requirements` comments.

func jsonld(t *testing.T, body string) *PageData {
	t.Helper()
	return extract(t, `<html><head><script type="application/ld+json">`+body+
		`</script></head><body></body></html>`, func(s *config.StructuredDataConfig) { s.JSONLD = true })
}

func hasIssue(items []string, sub string) bool {
	for _, it := range items {
		if strings.Contains(it, sub) {
			return true
		}
	}
	return false
}

// Google Software App: name + offers + (aggregateRating OR review) required;
// applicationCategory + operatingSystem recommended.
func TestSoftwareApplication(t *testing.T) {
	// Fully specified app ⇒ no errors, no warnings. (offers carries priceCurrency
	// so the now-validated nested Offer is complete too.)
	d := jsonld(t, `{"@context":"https://schema.org","@type":"SoftwareApplication","name":"App",
		"offers":{"@type":"Offer","price":"0","priceCurrency":"USD"},
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.5","ratingCount":"100"},
		"applicationCategory":"BusinessApplication","operatingSystem":"Web"}`)
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("complete SoftwareApplication: errors=%v warnings=%v, want none", d.Errors, d.Warnings)
	}

	// Missing offers ⇒ required error; applicationCategory/operatingSystem ⇒ warnings.
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.5","ratingCount":"100"}}`)
	if !hasIssue(d.Errors, "offers") {
		t.Errorf("missing offers: errors=%v, want offers error", d.Errors)
	}
	if hasIssue(d.Errors, "name") {
		t.Errorf("name present: errors=%v, want no name error", d.Errors)
	}
	if !hasIssue(d.Warnings, "applicationCategory") || !hasIssue(d.Warnings, "operatingSystem") {
		t.Errorf("warnings=%v, want applicationCategory + operatingSystem", d.Warnings)
	}

	// Missing BOTH aggregateRating and review ⇒ anyOf error (one of them required).
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App","offers":{"@type":"Offer","price":"5","priceCurrency":"USD"}}`)
	if !hasIssue(d.Errors, "aggregateRating") || !hasIssue(d.Errors, "review") {
		t.Errorf("no rating/review: errors=%v, want an 'one of aggregateRating, review' error", d.Errors)
	}

	// A `review` satisfies the anyOf even without aggregateRating ⇒ no anyOf error.
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App","offers":{"@type":"Offer","price":"5","priceCurrency":"USD"},
		"review":{"@type":"Review","author":"Bo","reviewRating":{"@type":"Rating","ratingValue":"5"}}}`)
	if hasIssue(d.Errors, "one of") {
		t.Errorf("review present: errors=%v, want no anyOf error", d.Errors)
	}
}

// WebApplication / MobileApplication are SoftwareApplication subtypes Google
// treats identically; they validate against the same requirements.
func TestSoftwareApplicationSubtypes(t *testing.T) {
	for _, typ := range []string{"WebApplication", "MobileApplication"} {
		d := jsonld(t, `{"@type":"`+typ+`","name":"App"}`)
		if !hasIssue(d.Errors, "offers") {
			t.Errorf("%s missing offers: errors=%v, want offers error (subtype must validate)", typ, d.Errors)
		}
	}
}

// Google Review snippet: a Review is only a candidate when it carries a
// reviewRating (the trigger); then `author` is required. itemReviewed is
// nesting-dependent and deliberately not required (avoids the R6 over-warn trap
// on product/app reviews where the parent is the reviewed item).
func TestReview(t *testing.T) {
	// reviewRating present (trigger), author missing ⇒ required error.
	d := jsonld(t, `{"@type":"Review","reviewRating":{"@type":"Rating","ratingValue":"5"},
		"itemReviewed":{"@type":"Thing","name":"X"}}`)
	if !hasIssue(d.Errors, "author") {
		t.Errorf("rating-bearing review w/o author: errors=%v, want author error", d.Errors)
	}

	// reviewRating + author ⇒ satisfied.
	d = jsonld(t, `{"@type":"Review","author":"Ada","reviewRating":{"@type":"Rating","ratingValue":"5"}}`)
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("complete review: errors=%v warnings=%v, want none", d.Errors, d.Warnings)
	}

	// No reviewRating ⇒ NOT a snippet candidate ⇒ no errors/warnings even w/o author.
	d = jsonld(t, `{"@type":"Review","author":"Ada","reviewBody":"text only"}`)
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("rating-less review: errors=%v warnings=%v, want none (not a candidate)", d.Errors, d.Warnings)
	}
}

// Google AggregateRating: ratingValue required; at least one of
// ratingCount / reviewCount required.
func TestAggregateRating(t *testing.T) {
	ok := []string{
		`{"@type":"AggregateRating","ratingValue":"4.2","reviewCount":"50"}`,
		`{"@type":"AggregateRating","ratingValue":"4.2","ratingCount":"50"}`,
	}
	for _, body := range ok {
		d := jsonld(t, body)
		if len(d.Errors) != 0 {
			t.Errorf("valid aggregateRating %s: errors=%v, want none", body, d.Errors)
		}
	}

	// No count at all ⇒ anyOf error.
	d := jsonld(t, `{"@type":"AggregateRating","ratingValue":"4.2"}`)
	if !hasIssue(d.Errors, "ratingCount") || !hasIssue(d.Errors, "reviewCount") {
		t.Errorf("count-less aggregateRating: errors=%v, want 'one of ratingCount, reviewCount'", d.Errors)
	}

	// Missing ratingValue ⇒ required error.
	d = jsonld(t, `{"@type":"AggregateRating","ratingCount":"10"}`)
	if !hasIssue(d.Errors, "ratingValue") {
		t.Errorf("valueless aggregateRating: errors=%v, want ratingValue error", d.Errors)
	}
}

// A Product with a well-formed nested aggregateRating + review must NOT produce
// any rating/review errors or warnings (the R6 over-warning regression guard).
// This Product has no offers, so it is a Product Snippet but NOT a Merchant
// Listing — the only warning is the missing recommended `offers` (image is not a
// snippet recommendation, and description/gtin are merchant-listing-only).
func TestNestedRatingNoOverWarn(t *testing.T) {
	d := jsonld(t, `{"@type":"Product","name":"P",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.5","reviewCount":"100"},
		"review":[{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","ratingValue":"5"}}]}`)
	if hasIssue(d.Errors, "author") || hasIssue(d.Errors, "ratingValue") || hasIssue(d.Errors, "one of") {
		t.Errorf("nested rating/review must not error: errors=%v", d.Errors)
	}
	for _, w := range d.Warnings {
		if !strings.Contains(w, "offers") {
			t.Errorf("unexpected warning %q (only the missing recommended offers expected)", w)
		}
	}
}

// --- Subtype-hierarchy resolution through the JSON-LD path ---
// A schema.org subtype validates against its most-specific curated ancestor's
// rules, and the issue is attributed to the leaf type the page actually used.

func TestSubtypeLocalBusiness(t *testing.T) {
	// Restaurant IS-A LocalBusiness ⇒ name+address required.
	d := jsonld(t, `{"@type":"Restaurant","name":"Joe's"}`)
	if !hasIssue(d.Errors, "Restaurant") || !hasIssue(d.Errors, "address") {
		t.Errorf("Restaurant missing address: errors=%v, want a 'Restaurant ... address' error", d.Errors)
	}
	if hasIssue(d.Errors, "name") {
		t.Errorf("name present: errors=%v, want no name error", d.Errors)
	}
	// Complete restaurant ⇒ no errors.
	d = jsonld(t, `{"@type":"Restaurant","name":"Joe's","address":"1 Main St"}`)
	if len(d.Errors) != 0 {
		t.Errorf("complete Restaurant: errors=%v, want none", d.Errors)
	}
	// Medical subtype resolves to LocalBusiness, not Organization.
	d = jsonld(t, `{"@type":"Hospital","name":"Mercy"}`)
	if !hasIssue(d.Errors, "address") {
		t.Errorf("Hospital missing address: errors=%v, want address error (LocalBusiness rules)", d.Errors)
	}
}

func TestSubtypeArticleRecommendedOnly(t *testing.T) {
	// TechArticle IS-A Article ⇒ recommended-only, never an error.
	d := jsonld(t, `{"@type":"TechArticle","author":"x"}`)
	if len(d.Errors) != 0 {
		t.Errorf("TechArticle: errors=%v, want none (Article has no required props)", d.Errors)
	}
	if !hasIssue(d.Warnings, "TechArticle") || !hasIssue(d.Warnings, "headline") {
		t.Errorf("TechArticle: warnings=%v, want a 'TechArticle ... headline' warning", d.Warnings)
	}
}

func TestSubtypeOrganizationTriggerInherited(t *testing.T) {
	// Corporation IS-A Organization, whose Logo feature is trigger-gated on a
	// logo. No logo ⇒ not a candidate ⇒ nothing (the R6 guard, inherited).
	d := jsonld(t, `{"@type":"Corporation","name":"Acme"}`)
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("logo-less Corporation: errors=%v warnings=%v, want none", d.Errors, d.Warnings)
	}
	// Logo present, url missing ⇒ inherit the recommended-url warning.
	d = jsonld(t, `{"@type":"Corporation","name":"Acme","logo":"https://x/l.png"}`)
	if !hasIssue(d.Warnings, "url") || hasIssue(d.Warnings, "logo") {
		t.Errorf("logo-bearing Corporation: warnings=%v, want a missing-url (not missing-logo) warning", d.Warnings)
	}
}

// A node multi-typed with a type and its supertype must not double-validate.
func TestSubtypeAntichainNoDoubleValidate(t *testing.T) {
	for _, body := range []string{
		`{"@type":["NewsArticle","Article"],"author":"x"}`,
		`{"@type":["LocalBusiness","Organization"],"name":"X"}`,
	} {
		d := jsonld(t, body)
		seen := map[string]bool{}
		for _, m := range append(append([]string{}, d.Errors...), d.Warnings...) {
			if seen[m] {
				t.Errorf("%s: duplicate message %q (supertype not collapsed)", body, m)
			}
			seen[m] = true
		}
	}
	// ["LocalBusiness","Organization"] name-only ⇒ exactly the LocalBusiness
	// address error, and no Organization logo machinery.
	d := jsonld(t, `{"@type":["LocalBusiness","Organization"],"name":"X"}`)
	if !hasIssue(d.Errors, "address") {
		t.Errorf("errors=%v, want address error", d.Errors)
	}
}

// Grounded exclusions: subtypes Google routes to a different/retired feature
// must produce no errors/warnings (over-warn guards).
func TestSubtypeExclusions(t *testing.T) {
	for _, body := range []string{
		`{"@type":"Car","name":"Model X"}`,           // ↛ Product (Vehicle-listing deprecated)
		`{"@type":"VideoGame","name":"Doom"}`,        // ↛ SoftwareApplication (no offers)
		`{"@type":"OperatingSystem","name":"Linux"}`, // ↛ SoftwareApplication (metadata)
		`{"@type":"RuntimePlatform","name":"JVM"}`,   // ↛ SoftwareApplication
		`{"@type":"ClaimReview","claimReviewed":"x","reviewRating":{"@type":"Rating","ratingValue":"1"},"url":"https://x"}`, // ↛ Review (Fact Check, not author-required)
	} {
		d := jsonld(t, body)
		if d == nil {
			continue // type recorded but nothing to validate is fine
		}
		if len(d.Errors) != 0 || len(d.Warnings) != 0 {
			t.Errorf("%s: errors=%v warnings=%v, want none (excluded from inheritance)", body, d.Errors, d.Warnings)
		}
	}
}

// VideoGame is excluded, but an EXPLICIT SoftwareApplication co-type opts the
// page into the app rich result and must validate.
func TestSubtypeVideoGameCoTypedValidates(t *testing.T) {
	d := jsonld(t, `{"@type":["VideoGame","SoftwareApplication"],"name":"Doom"}`)
	if !hasIssue(d.Errors, "offers") {
		t.Errorf("co-typed app: errors=%v, want offers error (explicit SoftwareApplication validates)", d.Errors)
	}
}

// ReviewNewsArticle IS-A both NewsArticle and Review (the only incomparable
// tie). It must validate as NewsArticle (recommended-only) and NEVER fire the
// Review "missing author" error.
func TestSubtypeReviewNewsArticleTie(t *testing.T) {
	d := jsonld(t, `{"@type":"ReviewNewsArticle","headline":"H","author":"A"}`)
	if len(d.Errors) != 0 {
		t.Errorf("ReviewNewsArticle: errors=%v, want none (NewsArticle has no required props; Review suppressed)", d.Errors)
	}
}

// B1 regression: RDFa is type-recording-only (no property set is collected), so
// a resolvable subtype must NOT be validated — else every RDFa subtype page
// would false-error on missing required props.
func TestRDFaSubtypeNotValidated(t *testing.T) {
	d := extract(t, `<html><body vocab="https://schema.org/" typeof="Restaurant">
		<span property="name">Joe's</span></body></html>`,
		func(s *config.StructuredDataConfig) { s.RDFa = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("RDFa Restaurant: errors=%v warnings=%v, want none (RDFa collects no props)", d.Errors, d.Warnings)
	}
}

// A complete root-level item with a nested non-curated item (PostalAddress)
// must not false-error from the nested item.
func TestMicrodataNestedNoFalseError(t *testing.T) {
	d := extract(t, `<html><body>
		<div itemscope itemtype="https://schema.org/Restaurant">
		  <span itemprop="name">Joe's</span>
		  <div itemprop="address" itemscope itemtype="https://schema.org/PostalAddress">
		    <span itemprop="streetAddress">1 Main St</span>
		  </div>
		</div></body></html>`,
		func(s *config.StructuredDataConfig) { s.Microdata = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Errors) != 0 {
		t.Errorf("Restaurant w/ nested PostalAddress: errors=%v, want none", d.Errors)
	}
}

// M5: a full-URL JSON-LD @type must normalize and resolve identically to the
// bare leaf, the same way Microdata's itemtype URL does.
func TestJSONLDFullURLTypeResolves(t *testing.T) {
	d := jsonld(t, `{"@context":"https://schema.org","@type":"https://schema.org/Restaurant","name":"Joe's"}`)
	if !hasIssue(d.Errors, "Restaurant") || !hasIssue(d.Errors, "address") {
		t.Errorf("full-URL @type: errors=%v, want a 'Restaurant ... address' error", d.Errors)
	}
}

// m8: the same entity appearing twice (here a Product in @graph and inline)
// must report each finding once.
func TestGraphInlineDedup(t *testing.T) {
	d := jsonld(t, `{"@context":"https://schema.org","@graph":[
		{"@type":"Product","name":"P"},
		{"@type":"Product","name":"P"}]}`)
	seen := map[string]bool{}
	for _, w := range d.Warnings {
		if seen[w] {
			t.Errorf("duplicate warning %q across identical entities", w)
		}
		seen[w] = true
	}
}

// B1 (the blocker the review caught): a business/media subtype that appears as a
// NESTED reference value (offers.seller, publisher, author, location, video) is
// a stub that legitimately carries only a name/url — it must NOT be validated
// for the parent feature's required props (R6-class false error). Only the
// page's primary entity (top-level / @graph member) is validated.
func TestNestedReferenceEntityNotValidated(t *testing.T) {
	cases := []string{
		`{"@type":"Product","name":"Shirt","image":"s.jpg","offers":{"@type":"Offer","price":"9","priceCurrency":"USD","seller":{"@type":"Store","name":"Acme Outlet"}}}`,
		`{"@type":"NewsArticle","headline":"H","image":"i","datePublished":"2024","author":"A","publisher":{"@type":"Restaurant","name":"Joe's"}}`,
		`{"@type":"Recipe","name":"Cake","image":"c.jpg","author":{"@type":"Bakery","name":"Sweet"}}`,
		`{"@type":"Event","name":"Gig","startDate":"2024-01-01","location":{"@type":"Hotel","name":"Grand"}}`,
		`{"@type":"Article","headline":"H","image":"i","datePublished":"2024","author":"A","video":{"@type":"VideoObject","name":"clip"}}`,
	}
	for _, body := range cases {
		d := jsonld(t, body)
		if len(d.Errors) != 0 {
			t.Errorf("%s\n  nested stub must not error: errors=%v", body, d.Errors)
		}
	}
	// Microdata equivalent: Product > Offer > nested Store itemscope.
	d := extract(t, `<html><body>
		<div itemscope itemtype="https://schema.org/Product">
		  <span itemprop="name">Shirt</span>
		  <span itemprop="image">s.jpg</span>
		  <div itemprop="offers" itemscope itemtype="https://schema.org/Offer">
		    <span itemprop="price">9</span>
		    <span itemprop="priceCurrency">USD</span>
		    <div itemprop="seller" itemscope itemtype="https://schema.org/Store">
		      <span itemprop="name">Acme Outlet</span>
		    </div>
		  </div>
		</div></body></html>`,
		func(s *config.StructuredDataConfig) { s.Microdata = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Errors) != 0 {
		t.Errorf("microdata nested Store must not error: errors=%v", d.Errors)
	}
	// …but the nested types are still recorded for the type set.
	if !slices.Contains(d.Types, "Store") || !slices.Contains(d.Types, "Offer") {
		t.Errorf("nested types must still be recorded: types=%v", d.Types)
	}
}

// A nested entity that is genuinely incomplete is silently allowed (not the
// primary entity); a TOP-LEVEL one of the same type IS validated — confirming
// the guard keys on nesting, not on the type.
func TestTopLevelStillValidatedAlongsideNested(t *testing.T) {
	top := jsonld(t, `{"@type":"Store","name":"Acme"}`) // top-level ⇒ validated
	if !hasIssue(top.Errors, "address") {
		t.Errorf("top-level Store: errors=%v, want address error", top.Errors)
	}
}

// M1: a microdata itemtype with several space-separated type URLs records ALL
// of them and validation is order-independent (not just the last token's leaf).
func TestMicrodataMultiItemtype(t *testing.T) {
	d := extract(t, `<html><body>
		<div itemscope itemtype="https://schema.org/Product https://schema.org/IndividualProduct">
		  <span itemprop="name">Widget</span>
		</div></body></html>`,
		func(s *config.StructuredDataConfig) { s.Microdata = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if !slices.Contains(d.Types, "Product") || !slices.Contains(d.Types, "IndividualProduct") {
		t.Errorf("multi-itemtype: types=%v, want both Product and IndividualProduct", d.Types)
	}
	// Restaurant declared anywhere in the itemtype list must drive validation.
	d = extract(t, `<html><body>
		<div itemscope itemtype="https://schema.org/Thing https://schema.org/Restaurant">
		  <span itemprop="name">Joe's</span>
		</div></body></html>`,
		func(s *config.StructuredDataConfig) { s.Microdata = true })
	if !hasIssue(d.Errors, "address") {
		t.Errorf("itemtype with Restaurant: errors=%v, want address error regardless of token order", d.Errors)
	}
}

// JSON-LD @type recorded in PageData.Types is normalized to the short form
// (parity with SF and Microdata), even from a full URL.
func TestTypesNormalized(t *testing.T) {
	d := jsonld(t, `{"@context":"https://schema.org","@type":"https://schema.org/Product","name":"P"}`)
	if !slices.Contains(d.Types, "Product") {
		t.Errorf("types=%v, want short-form Product", d.Types)
	}
	for _, ty := range d.Types {
		if strings.Contains(ty, "/") {
			t.Errorf("type %q not normalized to short form", ty)
		}
	}
}

// --- Nested integral-object validation (G5-matrix nested-property checks) ---
//
// Google (and Screaming Frog, which mirrors the Rich Results Test) validate the
// integral sub-entities of a rich result — a Product's offers→Offer and
// review→Review, a Review's reviewRating→Rating — against their OWN required
// properties, not just the parent. bluesnake previously validated the page's
// primary entity only, so an Offer with no price slipped past silently. The
// engine now recurses into a curated whitelist of integral properties
// (offers/review/reviews/reviewRating/aggregateRating) while still leaving
// reference stubs (seller/publisher/author/brand) unvalidated.
//
// Ground truth measured on SF v24.1 (STANDARD config) probe pages:
//   offers w/o price        ⇒ Rich Result Error  "Offer ... price ... required"
//   offers w/o priceCurrency⇒ Rich Result Error  "Offer ... priceCurrency ... required"
//   reviewRating w/o ratingValue ⇒ Rich Result Error "Rating ... ratingValue ... required"
// (The merchant-listing recommended breadth — Offer itemCondition/availability,
// Product image/description/gtin — is pinned by TestProductMerchantListing.)

func TestNestedOfferRequiredProperties(t *testing.T) {
	// offers present but missing price ⇒ Offer price error (anyOf price/priceSpec).
	d := jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","priceCurrency":"USD"}}`)
	if !hasIssue(d.Errors, "Offer") || !hasIssue(d.Errors, "price") {
		t.Errorf("offers w/o price: errors=%v, want an Offer price error", d.Errors)
	}

	// offers present but missing priceCurrency ⇒ Offer priceCurrency error.
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9.99"}}`)
	if !hasIssue(d.Errors, "Offer") || !hasIssue(d.Errors, "priceCurrency") {
		t.Errorf("offers w/o priceCurrency: errors=%v, want an Offer priceCurrency error", d.Errors)
	}

	// Complete offers ⇒ no Offer error (priceSpecification also satisfies both).
	for _, off := range []string{
		`{"@type":"Offer","price":"9.99","priceCurrency":"USD"}`,
		`{"@type":"Offer","priceSpecification":{"@type":"PriceSpecification","price":"9.99","priceCurrency":"USD"}}`,
	} {
		d = jsonld(t, `{"@type":"Product","name":"P","offers":`+off+`}`)
		if hasIssue(d.Errors, "Offer") {
			t.Errorf("complete offers %s: errors=%v, want no Offer error", off, d.Errors)
		}
	}

	// offers with no @type ⇒ Google infers Offer from the property; still validated.
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"price":"9.99"}}`)
	if !hasIssue(d.Errors, "priceCurrency") {
		t.Errorf("typeless offers w/o currency: errors=%v, want priceCurrency error (implied Offer)", d.Errors)
	}
}

func TestNestedRatingRequiredRatingValue(t *testing.T) {
	// A review's reviewRating missing ratingValue ⇒ Rating ratingValue error.
	d := jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"},
		"review":{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","bestRating":"5"}}}`)
	if !hasIssue(d.Errors, "Rating") || !hasIssue(d.Errors, "ratingValue") {
		t.Errorf("reviewRating w/o ratingValue: errors=%v, want a Rating ratingValue error", d.Errors)
	}

	// A Product's nested aggregateRating missing ratingValue ⇒ error (validated nested).
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"},
		"aggregateRating":{"@type":"AggregateRating","ratingCount":"10"}}`)
	if !hasIssue(d.Errors, "ratingValue") {
		t.Errorf("nested aggregateRating w/o ratingValue: errors=%v, want a ratingValue error", d.Errors)
	}
}

// AggregateOffer (IS-A Offer) uses lowPrice, not price — validating it against
// the plain Offer rule would false-error on valid markup. It has its own rule.
func TestNestedAggregateOfferNotMisvalidated(t *testing.T) {
	d := jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"AggregateOffer","lowPrice":"9.99","priceCurrency":"USD"}}`)
	if hasIssue(d.Errors, "price") && !hasIssue(d.Errors, "lowPrice") {
		t.Errorf("valid AggregateOffer: errors=%v, want no plain-price error", d.Errors)
	}
	// Empty AggregateOffer ⇒ still flags the missing low/price + currency.
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"AggregateOffer"}}`)
	if !hasIssue(d.Errors, "lowPrice") || !hasIssue(d.Errors, "priceCurrency") {
		t.Errorf("empty AggregateOffer: errors=%v, want lowPrice + priceCurrency errors", d.Errors)
	}
}

// Offer validation is parent-aware: Google's Offer requirements are per-feature
// (measured on SF v24.1, message text from the Structured Data Validation Errors
// export):
//
//	Product (Snippet + Merchant Listings) → price AND priceCurrency required (errors).
//	Software App (+ Web/MobileApplication) → price required (error); priceCurrency
//	    required only when price > 0 ("'priceCurrency' is recommended if 'price' > 0").
//	Event → price/priceCurrency are RECOMMENDED (warnings), not errors.
//
// bluesnake validates the price requirement under Product and Software App, but
// not under Event (recommended-only). priceCurrency is an unconditional error
// under Product; under Software App it is value-conditional (price > 0), which the
// presence-based engine cannot test, so it is omitted there — a deliberate
// under-report that never over-errors a free app (see offerCurrencyOptional).
func TestNestedOfferParentAware(t *testing.T) {
	// A Product url-only offer DOES require price + priceCurrency.
	d := jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","url":"https://x"}}`)
	if !hasIssue(d.Errors, "Offer") || !hasIssue(d.Errors, "price") {
		t.Errorf("Product url-only offer: errors=%v, want an Offer price error", d.Errors)
	}

	// Event with a url-only offer ⇒ NO Offer error (price is recommended there).
	d = jsonld(t, `{"@type":"Event","name":"Gig","startDate":"2026-09-01",
		"location":{"@type":"Place","name":"Hall","address":"1 Main St"},
		"offers":{"@type":"Offer","url":"https://tickets.example","availability":"https://schema.org/InStock"}}`)
	if hasIssue(d.Errors, "Offer") {
		t.Errorf("Event url-only offer: errors=%v, want NO Offer error (recommended-only there)", d.Errors)
	}

	// SoftwareApplication offer missing price ⇒ price ERROR (matches SF: "'price'
	// property is required for 'Offer'" under the Google Software App feature).
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App","aggregateRating":{"@type":"AggregateRating","ratingValue":"4","ratingCount":"9"},
		"offers":{"@type":"Offer","availability":"https://schema.org/InStock"}}`)
	if !hasIssue(d.Errors, "Offer") || !hasIssue(d.Errors, "price") {
		t.Errorf("SoftwareApplication offer missing price: errors=%v, want an Offer price error", d.Errors)
	}

	// SoftwareApplication offer WITH a price but no priceCurrency ⇒ NO error.
	// SF errors here (priceCurrency required when price > 0), but that condition is
	// value-based; bluesnake under-reports rather than over-error a free app.
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App","aggregateRating":{"@type":"AggregateRating","ratingValue":"4","ratingCount":"9"},
		"offers":{"@type":"Offer","price":"9.99","availability":"https://schema.org/InStock"}}`)
	if hasIssue(d.Errors, "Offer") {
		t.Errorf("SoftwareApplication offer price-no-currency: errors=%v, want NO error (priceCurrency is value-conditional, deliberately under-reported)", d.Errors)
	}

	// A complete SoftwareApplication offer (price + currency) ⇒ no Offer error.
	d = jsonld(t, `{"@type":"SoftwareApplication","name":"App","aggregateRating":{"@type":"AggregateRating","ratingValue":"4","ratingCount":"9"},
		"offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}`)
	if hasIssue(d.Errors, "Offer") {
		t.Errorf("SoftwareApplication complete offer: errors=%v, want NO Offer error", d.Errors)
	}

	// Microdata SoftwareApplication offer missing price ⇒ price ERROR too (parity
	// across formats — both surfaces share this engine).
	mbody := `<html><body>
	<div itemscope itemtype="https://schema.org/SoftwareApplication">
	  <span itemprop="name">App</span>
	  <div itemprop="aggregateRating" itemscope itemtype="https://schema.org/AggregateRating">
	    <span itemprop="ratingValue">4</span><span itemprop="ratingCount">9</span>
	  </div>
	  <div itemprop="offers" itemscope itemtype="https://schema.org/Offer">
	    <span itemprop="availability">https://schema.org/InStock</span>
	  </div>
	</div></body></html>`
	dm := extract(t, mbody, func(s *config.StructuredDataConfig) { s.Microdata = true })
	if !hasIssue(dm.Errors, "Offer") || !hasIssue(dm.Errors, "price") {
		t.Errorf("microdata SoftwareApplication offer missing price: errors=%v, want an Offer price error", dm.Errors)
	}
}

// The R6 over-warn boundary, locked precisely: a nested REFERENCE stub reached
// via a non-integral property (seller→Store, publisher→Organization) records its
// type but is NEVER validated for its own required properties. Validating a
// nested Store for `address` is the original R6 false-error.
func TestNestedReferenceStubNotValidated(t *testing.T) {
	// offers.seller = a Store with no address ⇒ must NOT emit a Store/address error.
	d := jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD",
		"seller":{"@type":"Store","name":"Acme Shop"}}}`)
	if hasIssue(d.Errors, "address") || hasIssue(d.Errors, "Store") {
		t.Errorf("offers.seller=Store: errors=%v, want NO Store/address error (reference stub)", d.Errors)
	}
	if !slices.Contains(d.Types, "Store") {
		t.Errorf("types=%v, want Store recorded even though unvalidated", d.Types)
	}

	// A reference stub's OWN nested integral child stays unvalidated too: the
	// seller Store carries a malformed aggregateRating (no ratingValue), but it
	// is inside a stub subtree, so no error fires (the whole subtree is a stub).
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9","priceCurrency":"USD",
		"seller":{"@type":"Store","name":"Acme","aggregateRating":{"@type":"AggregateRating","ratingCount":"3"}}}}`)
	if hasIssue(d.Errors, "ratingValue") {
		t.Errorf("stub's nested rating: errors=%v, want none (whole seller subtree is a reference stub)", d.Errors)
	}

	// Article.publisher = a logo-less Organization ⇒ no Organization warning/error
	// (publisher is a reference, not an integral part of the Article rich result).
	d = jsonld(t, `{"@type":"Article","headline":"H","image":"i","author":"A","datePublished":"x","dateModified":"y",
		"publisher":{"@type":"Organization","name":"Pub"}}`)
	if hasIssue(d.Errors, "Organization") || hasIssue(d.Warnings, "Organization") {
		t.Errorf("Article.publisher=Organization: errors=%v warnings=%v, want none (reference stub)", d.Errors, d.Warnings)
	}
}

// Microdata gets the same nested-integral validation: a nested Offer itemscope
// reached via itemprop="offers" is validated; a seller stub is not.
func TestMicrodataNestedOffer(t *testing.T) {
	body := `<html><body>
	<div itemscope itemtype="https://schema.org/Product">
	  <span itemprop="name">Widget</span>
	  <span itemprop="image">i.jpg</span>
	  <div itemprop="offers" itemscope itemtype="https://schema.org/Offer">
	    <span itemprop="price">9.99</span>
	    <div itemprop="seller" itemscope itemtype="https://schema.org/Store">
	      <span itemprop="name">Acme Shop</span>
	    </div>
	  </div>
	</div></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.Microdata = true })
	if !hasIssue(d.Errors, "Offer") || !hasIssue(d.Errors, "priceCurrency") {
		t.Errorf("microdata offers w/o priceCurrency: errors=%v, want Offer priceCurrency error", d.Errors)
	}
	if hasIssue(d.Errors, "address") || hasIssue(d.Errors, "Store") {
		t.Errorf("microdata offers.seller=Store: errors=%v, want NO Store/address error", d.Errors)
	}
}

// --- Product / Offer Merchant Listing model (G5-matrix residual (b)) ---
//
// Pinned to Screaming Frog v24.1 (STANDARD config) measured behaviour
// (/tmp/bs-probes/sd: prod_bare, ml_min, prod_ml_clean, prod_snip_review, swapp_*).
// A Product is ALWAYS a Product Snippet candidate: one of review / aggregateRating
// / offers is required (an error if none), and each of those three is recommended.
// Carrying `offers` additionally makes the Product a Merchant Listing, which
// REQUIRES `image` (an error, not a warning) and recommends `description` + a
// `gtin` family member; its nested Offer then recommends `availability` +
// `itemCondition`. Software-App / Event offers carry DIFFERENT per-feature Offer
// rules and must not inherit the merchant recommendations.
func TestProductMerchantListing(t *testing.T) {
	// (1) Bare Product (none of review/aggregateRating/offers) ⇒ snippet-eligibility
	//     anyOf ERROR + the three snippet recommended warnings. NOT a merchant
	//     listing, so NO image/description/gtin findings at all.
	d := jsonld(t, `{"@type":"Product","name":"P"}`)
	if !hasIssue(d.Errors, "one of") || !hasIssue(d.Errors, "offers") {
		t.Errorf("bare Product: errors=%v, want a 'one of review, aggregateRating, offers' error", d.Errors)
	}
	for _, w := range []string{"offers", "review", "aggregateRating"} {
		if !hasIssue(d.Warnings, w) {
			t.Errorf("bare Product: warnings=%v, want snippet recommended %q", d.Warnings, w)
		}
	}
	if hasIssue(d.Errors, "image") || hasIssue(d.Warnings, "image") ||
		hasIssue(d.Warnings, "description") || hasIssue(d.Warnings, "gtin") {
		t.Errorf("bare Product (no offers) must not emit merchant image/description/gtin: errors=%v warnings=%v", d.Errors, d.Warnings)
	}

	// (2) Merchant listing missing image (Product + offers, no image) ⇒ image is a
	//     REQUIRED error (never a warning); offers satisfies snippet eligibility (no
	//     anyOf error); description + gtin recommended; the nested Offer recommends
	//     availability + itemCondition.
	d = jsonld(t, `{"@type":"Product","name":"P","offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}`)
	if !hasIssue(d.Errors, "image") {
		t.Errorf("merchant listing w/o image: errors=%v, want an image REQUIRED error", d.Errors)
	}
	if hasIssue(d.Warnings, "image") {
		t.Errorf("image must be a merchant-listing error, never a warning: warnings=%v", d.Warnings)
	}
	if hasIssue(d.Errors, "one of") {
		t.Errorf("offers satisfies snippet eligibility: errors=%v, want no anyOf error", d.Errors)
	}
	for _, w := range []string{"description", "gtin", "availability", "itemCondition"} {
		if !hasIssue(d.Warnings, w) {
			t.Errorf("merchant listing: warnings=%v, want merchant recommended %q", d.Warnings, w)
		}
	}

	// (3) Complete merchant listing ⇒ no errors, no warnings.
	d = jsonld(t, `{"@type":"Product","name":"P","image":"i.jpg","description":"d","gtin13":"0001234560001",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.5","reviewCount":"12"},
		"review":{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","ratingValue":"5"}},
		"offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD","availability":"https://schema.org/InStock","itemCondition":"https://schema.org/NewCondition"}}`)
	if len(d.Errors) != 0 || len(d.Warnings) != 0 {
		t.Errorf("complete merchant listing: errors=%v warnings=%v, want none", d.Errors, d.Warnings)
	}

	// (4) Snippet-only Product (review present, no offers) ⇒ NOT a merchant listing:
	//     no image/description/gtin/availability/itemCondition; review satisfies
	//     eligibility (no error); snippet still recommends offers + aggregateRating.
	d = jsonld(t, `{"@type":"Product","name":"P","review":{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","ratingValue":"5"}}}`)
	if len(d.Errors) != 0 {
		t.Errorf("snippet-only Product: errors=%v, want none (review satisfies eligibility, image not required)", d.Errors)
	}
	for _, bad := range []string{"image", "description", "gtin", "availability", "itemCondition"} {
		if hasIssue(d.Warnings, bad) {
			t.Errorf("snippet-only Product must not emit merchant property %q: warnings=%v", bad, d.Warnings)
		}
	}
	if !hasIssue(d.Warnings, "offers") || !hasIssue(d.Warnings, "aggregateRating") {
		t.Errorf("snippet-only Product: warnings=%v, want snippet recommended offers + aggregateRating", d.Warnings)
	}

	// (5) gtin family: any of gtin/gtin8/.../isbn satisfies the recommendation.
	d = jsonld(t, `{"@type":"Product","name":"P","image":"i.jpg","description":"d","isbn":"123",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4","reviewCount":"2"},
		"review":{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","ratingValue":"5"}},
		"offers":{"@type":"Offer","price":"9","priceCurrency":"USD","availability":"x","itemCondition":"x"}}`)
	if hasIssue(d.Warnings, "gtin") {
		t.Errorf("isbn present satisfies the gtin recommendation: warnings=%v", d.Warnings)
	}
}

// A nested Offer's merchant-listing recommended set (availability/itemCondition)
// is Product-specific: a Software-App offer must NOT inherit it (SF v24.1: swapp
// offers warn on neither), while a Product offer does.
func TestOfferMerchantRecommendedParentAware(t *testing.T) {
	// Software-App offer missing availability/itemCondition ⇒ NO such warnings.
	d := jsonld(t, `{"@type":"SoftwareApplication","name":"App",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4","ratingCount":"9"},
		"applicationCategory":"X","operatingSystem":"Web",
		"offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}`)
	if hasIssue(d.Warnings, "availability") || hasIssue(d.Warnings, "itemCondition") {
		t.Errorf("Software-App offer must not carry merchant availability/itemCondition: warnings=%v", d.Warnings)
	}
	// Product offer DOES carry them.
	d = jsonld(t, `{"@type":"Product","name":"P","image":"i.jpg","description":"d","gtin":"1",
		"aggregateRating":{"@type":"AggregateRating","ratingValue":"4","reviewCount":"2"},
		"review":{"@type":"Review","author":"A","reviewRating":{"@type":"Rating","ratingValue":"5"}},
		"offers":{"@type":"Offer","price":"9.99","priceCurrency":"USD"}}`)
	if !hasIssue(d.Warnings, "availability") || !hasIssue(d.Warnings, "itemCondition") {
		t.Errorf("Product offer: warnings=%v, want availability + itemCondition", d.Warnings)
	}
}

// Microdata parity: a merchant-listing Product carries the same model across both
// surfaces (both share internal/structured).
func TestMicrodataProductMerchantListing(t *testing.T) {
	body := `<html><body>
	<div itemscope itemtype="https://schema.org/Product">
	  <span itemprop="name">Widget</span>
	  <div itemprop="offers" itemscope itemtype="https://schema.org/Offer">
	    <span itemprop="price">9.99</span><span itemprop="priceCurrency">USD</span>
	  </div>
	</div></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.Microdata = true })
	if !hasIssue(d.Errors, "image") {
		t.Errorf("microdata merchant listing w/o image: errors=%v, want image required error", d.Errors)
	}
	for _, w := range []string{"description", "gtin", "availability", "itemCondition"} {
		if !hasIssue(d.Warnings, w) {
			t.Errorf("microdata merchant listing: warnings=%v, want merchant recommended %q", d.Warnings, w)
		}
	}
}
