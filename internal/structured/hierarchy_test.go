package structured

import (
	"slices"
	"testing"
	"time"
)

// Resolution facts cross-checked against the authoritative schema.org graph
// (schemaorg_hierarchy.txt). These pin the IS-A resolution independently of the
// JSON-LD/Microdata extraction paths.
func TestResolveType(t *testing.T) {
	cases := []struct{ typ, want string }{
		// LocalBusiness subtree (the headline win).
		{"Restaurant", "LocalBusiness"},
		{"Bakery", "LocalBusiness"},
		{"Attorney", "LocalBusiness"},
		{"AutoRepair", "LocalBusiness"},
		// MedicalBusiness-branch types are subtypes of BOTH LocalBusiness and
		// Organization; most-specific wins (LocalBusiness IS-A Organization).
		{"Hospital", "LocalBusiness"},
		{"Pharmacy", "LocalBusiness"},
		{"Physician", "LocalBusiness"},
		// …but a pure MedicalOrganization-branch type (no MedicalBusiness/
		// LocalBusiness ancestor) resolves to Organization (logo-gated, safe).
		{"VeterinaryCare", "Organization"},
		{"DiagnosticLab", "Organization"},
		// Other curated subtrees.
		{"TechArticle", "Article"},
		{"Festival", "Event"},
		{"Corporation", "Organization"},
		{"NGO", "Organization"},
		// Direct curated hits short-circuit (keep their own rules).
		{"LocalBusiness", "LocalBusiness"},
		{"NewsArticle", "NewsArticle"},
		{"WebApplication", "WebApplication"},
		{"MobileApplication", "MobileApplication"},
		// Not under any curated root ⇒ no validation.
		{"QAPage", ""}, // IS-A WebPage, not FAQPage
		{"Thing", ""},
		{"Person", ""},
		{"WebPage", ""},
		// Grounded exclusions: a subtype routed to a different/retired Google
		// feature must NOT inherit its curated parent's rules.
		{"Car", ""},                          // Vehicle subtree ↛ Product (Vehicle-listing deprecated 2025)
		{"Vehicle", ""},                      //
		{"Motorcycle", ""},                   // subtree exclusion reaches descendants
		{"VideoGame", ""},                    // bare VideoGame: no Software App rich result
		{"OperatingSystem", ""},              // metadata, legitimately no offers
		{"RuntimePlatform", ""},              //
		{"ClaimReview", ""},                  // Fact Check feature, not author-required
		{"MediaReview", ""},                  // media-authenticity feature (sibling of ClaimReview)
		{"EmployerReview", ""},               // Employer Rating feature
		{"EmployerAggregateRating", ""},      // Employer Rating feature
		{"ReviewNewsArticle", "NewsArticle"}, // sole incomparable tie → NewsArticle (Review suppressed)
		// …but ordinary Review subtypes keep validating (author genuinely required).
		{"CriticReview", "Review"},
		{"UserReview", "Review"},
		// UserInteraction telemetry ↛ Event, but real Event subtypes are kept.
		{"UserComments", ""},
		{"BroadcastEvent", "Event"},
	}
	for _, c := range cases {
		if got := resolveType(c.typ); got != c.want {
			t.Errorf("resolveType(%q) = %q, want %q", c.typ, got, c.want)
		}
	}
}

// mostSpecific drops redundant supertypes within one node's resolved-root set
// but keeps genuinely unrelated roots.
func TestMostSpecific(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{[]string{"NewsArticle", "Article"}, []string{"NewsArticle"}},
		{[]string{"LocalBusiness", "Organization"}, []string{"LocalBusiness"}},
		{[]string{"Product", "SoftwareApplication"}, []string{"Product", "SoftwareApplication"}}, // unrelated
		{[]string{"Product"}, []string{"Product"}},
	}
	for _, c := range cases {
		got := mostSpecific(c.in)
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("mostSpecific(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The embedded graph must load and contain the expected scale, so a silently
// truncated/empty data file fails loudly rather than disabling all resolution.
func TestHierarchyLoaded(t *testing.T) {
	hierarchyOnce.Do(initHierarchy)
	if len(parentsOf) < 800 {
		t.Fatalf("schema.org hierarchy looks truncated: %d edges (want >800)", len(parentsOf))
	}
	if !ancestorsOf["Restaurant"]["LocalBusiness"] {
		t.Error("Restaurant should have LocalBusiness as a transitive ancestor")
	}
	if !ancestorsOf["LocalBusiness"]["Organization"] {
		t.Error("LocalBusiness should have Organization as a transitive ancestor")
	}
}

// transitiveAncestors must terminate (not loop) on a cyclic graph and degrade
// gracefully — schema.org is acyclic, but a corrupt data file must not hang.
func TestTransitiveAncestorsCycleSafe(t *testing.T) {
	done := make(chan map[string]map[string]bool, 1)
	go func() {
		done <- transitiveAncestors(map[string][]string{
			"A": {"B"}, "B": {"C"}, "C": {"A"}, // cycle
			"X": {"A"},
		})
	}()
	select {
	case anc := <-done:
		if !anc["X"]["A"] {
			t.Errorf("X should still reach A across the cycle; got %v", anc["X"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transitiveAncestors did not terminate on a cyclic graph")
	}
}

// Resolution must be deterministic across runs despite Go's randomized map
// iteration (the emitted issue order must not flap). Two UNRELATED roots
// (SoftwareApplication + Product) exercise the multi-root sort path.
func TestResolutionDeterministic(t *testing.T) {
	body := `{"@context":"https://schema.org","@type":["SoftwareApplication","Product"]}`
	first := jsonld(t, body)
	for i := range 20 {
		got := jsonld(t, body)
		if !slices.Equal(got.Errors, first.Errors) || !slices.Equal(got.Warnings, first.Warnings) {
			t.Fatalf("nondeterministic output:\n run0 err=%v warn=%v\n run%d err=%v warn=%v",
				first.Errors, first.Warnings, i, got.Errors, got.Warnings)
		}
	}
}
