package sitecheck

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// A Product carrying offers is a Merchant Listing, which requires image —
// the canonical validation-error fixture from internal/structured's tests.
const productNoImage = `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@type":"Product","name":"Widget","offers":{"@type":"Offer","price":"10","priceCurrency":"USD"}}
</script></head><body></body></html>`

func TestStructuredValidationErrors(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, productNoImage)
	})
	rep, err := newChecker(t).Structured(context.Background(), s.URL+"/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Formats) != 1 || rep.Formats[0] != "jsonld" {
		t.Fatalf("formats = %v", rep.Formats)
	}
	if len(rep.Errors) == 0 {
		t.Fatal("Product-with-offers missing image must produce a validation error")
	}
	if !hasFinding(rep, "structured_validation_error") {
		t.Errorf("findings = %v", findingIDs(rep))
	}
	if hasFinding(rep, "structured_missing") {
		t.Errorf("structured_missing must not fire when markup exists: %v", findingIDs(rep))
	}
}

func TestStructuredMissing(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><head><title>t</title></head><body>plain</body></html>")
	})
	rep, err := newChecker(t).Structured(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := findingIDs(rep); len(got) != 1 || got[0] != "structured_missing" {
		t.Errorf("findings = %v, want [structured_missing]", got)
	}
}

// The tool forces all formats on even though extraction.structured_data
// defaults entirely off — the config keys budget crawl cost, not the tool.
func TestStructuredIgnoresExtractionConfig(t *testing.T) {
	chk := newChecker(t)
	sd := chk.cfg.Extraction.StructuredData
	if sd.JSONLD || sd.Microdata || sd.RDFa {
		t.Fatal("fixture assumption broken: extraction defaults changed")
	}
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, productNoImage)
	})
	rep, err := chk.Structured(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Formats) == 0 {
		t.Error("extraction must run regardless of the crawl config")
	}
}

func TestStructuredFetchFailure(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	rep, err := newChecker(t).Structured(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FetchStatus != 500 {
		t.Errorf("status = %d", rep.FetchStatus)
	}
	if got := rep.Findings(); got != nil {
		t.Errorf("an unfetchable page must yield no findings, got %v", got)
	}
}

func TestStructuredParseError(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><script type="application/ld+json">{not json</script></head></html>`)
	})
	rep, err := newChecker(t).Structured(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ParseErrors) == 0 || !hasFinding(rep, "structured_parse_error") {
		t.Errorf("parse_errors = %v findings = %v", rep.ParseErrors, findingIDs(rep))
	}
}
