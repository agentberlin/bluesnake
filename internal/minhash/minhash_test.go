package minhash

import (
	"fmt"
	"strings"
	"testing"
)

func longDoc(prefix string, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return strings.Join(w, " ")
}

func TestOfDeterministicAndSimilarity(t *testing.T) {
	a := longDoc("word", 200)
	sig1, sig2 := Of(a), Of(a)
	if sig1 != sig2 {
		t.Error("Of is not deterministic for identical input")
	}
	if s := sig1.Similarity(sig2); s != 1.0 {
		t.Errorf("self-similarity = %v, want 1.0", s)
	}

	// A near-copy (one word changed in a 200-word doc) stays highly similar.
	// The minhash estimate over 64 permutations carries sampling variance, so
	// this is a generous floor — the point is "much closer than disjoint", not a
	// precise value.
	b := strings.Fields(a)
	b[100] = "swapped"
	if s := Of(a).Similarity(Of(strings.Join(b, " "))); s < 0.85 {
		t.Errorf("near-copy similarity = %v, want >= 0.85", s)
	}
	// Disjoint vocabularies are dissimilar.
	if s := Of(a).Similarity(Of(longDoc("other", 200))); s > 0.2 {
		t.Errorf("disjoint similarity = %v, want <= 0.2", s)
	}
	// Two empty texts are identical (the all-max signature).
	if s := Of("").Similarity(Of("")); s != 1.0 {
		t.Errorf("empty-vs-empty similarity = %v, want 1.0", s)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	sig := Of(longDoc("token", 120))
	b := sig.Encode()
	if len(b) != EncodedLen {
		t.Fatalf("encoded length = %d, want %d", len(b), EncodedLen)
	}
	if got := Decode(b); got != sig {
		t.Error("Decode(Encode(sig)) != sig")
	}
}

func TestDecodeBadLengthDegradesToEmpty(t *testing.T) {
	empty := Of("")
	if Decode(nil) != empty {
		t.Error("Decode(nil) should degrade to the empty-text signature")
	}
	if Decode([]byte{1, 2, 3}) != empty {
		t.Error("Decode(short) should degrade to the empty-text signature")
	}
}
