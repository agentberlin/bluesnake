package structured

import (
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
	{"@context":"https://schema.org","@type":"Product","name":"Widget","offers":{"@type":"Offer","price":"10"}}
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
	if len(d.Errors) != 0 {
		t.Errorf("product has name; errors = %v", d.Errors)
	}
	// image/review/aggregateRating recommended but missing
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
