package analyze

import "testing"

func TestContentSimilarity(t *testing.T) {
	const a = "the quick brown fox jumps over the lazy dog while the sun sets slowly"
	const b = "an entirely different sentence about distributed systems and consensus protocols today"

	// Identical text is fully similar.
	if got := ContentSimilarity(a, a); got != 100 {
		t.Errorf("ContentSimilarity(a,a) = %v, want 100", got)
	}
	// Two empty texts are identical (no content either side -> no change).
	if got := ContentSimilarity("", ""); got != 100 {
		t.Errorf("ContentSimilarity(\"\",\"\") = %v, want 100", got)
	}
	// Disjoint texts are nearly dissimilar.
	if got := ContentSimilarity(a, b); got > 20 {
		t.Errorf("ContentSimilarity(a,b) = %v, want < 20 (disjoint)", got)
	}
	// Empty vs non-empty counts as a full change (~0% similar).
	if got := ContentSimilarity("", a); got > 20 {
		t.Errorf("ContentSimilarity(\"\",a) = %v, want < 20", got)
	}
	// Symmetric.
	if ContentSimilarity(a, b) != ContentSimilarity(b, a) {
		t.Errorf("ContentSimilarity not symmetric")
	}
}
