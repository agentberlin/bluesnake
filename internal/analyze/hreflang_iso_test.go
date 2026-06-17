package analyze

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
)

func hasOccDetail(r *Results, url, id, detail string) bool {
	for _, o := range r.Occurrences {
		if o.URL == url && o.IssueID == id && o.Detail == detail {
			return true
		}
	}
	return false
}

// Hreflang codes must be assigned ISO 639-1 languages with optionally
// assigned ISO 3166-1 alpha-2 regions — structurally well-formed but
// unassigned codes like "zz" or "en-ZZ" are invalid.
func TestHreflangISORegistryValidation(t *testing.T) {
	self := "https://ex.com/en"
	p := page(self)
	p.Facts.HreflangHTML = []parse.Hreflang{
		{Lang: "en", URL: self},
		{Lang: "en-US", URL: self},
		{Lang: "EN-us", URL: self},
		{Lang: "x-default", URL: self},
		{Lang: "zz", URL: self},
		{Lang: "en-ZZ", URL: self},
		{Lang: "qq-US", URL: self},
		{Lang: "zz-!!", URL: self},
	}
	r := Run(toMap(p), nil, nil, config.Default())

	for _, bad := range []string{"zz", "en-ZZ", "qq-US", "zz-!!"} {
		if !hasOccDetail(r, self, "hreflang_invalid_code", bad) {
			t.Errorf("missing hreflang_invalid_code with Detail %q", bad)
		}
	}
	for _, good := range []string{"en", "en-US", "EN-us", "x-default"} {
		if hasOccDetail(r, self, "hreflang_invalid_code", good) {
			t.Errorf("valid code %q flagged as hreflang_invalid_code", good)
		}
	}
}
