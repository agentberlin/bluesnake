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
