package minhash

import (
	"fmt"
	"slices"
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

// TestOfUnchanged pins Of to the values it produced before the shingling was
// shared with Shingles: crawls store these signatures (pages.minhash), so a
// change here would compare new pages against old ones under two hash families.
func TestOfUnchanged(t *testing.T) {
	s := Of("The quick brown Fox jumps over the lazy dog, and the dog sleeps on under the warm sun.")
	if s[0] != 0x29f50610a32e33b1 || s[17] != 0x3c17ae77f0c48c3b || s[42] != 0x148d8997fb44cd1a || s[63] != 0x1a2f5e0743554e8f {
		t.Errorf("Of changed: %#x %#x %#x %#x", s[0], s[17], s[42], s[63])
	}
	short := Of("three short words") // fewer than five words: one shingle of all of them
	if short[0] != 0x6c7e210b7e767c8f || short[63] != 0x1658a0d3c809588c {
		t.Errorf("Of changed on a short text: %#x %#x", short[0], short[63])
	}
}

// TestShinglesMatchOf: the shingle set is what Of hashes — sorted, without
// repeats — so FromShingles over it rebuilds Of exactly, and the set's size is
// the number of distinct 5-word windows.
func TestShinglesMatchOf(t *testing.T) {
	for _, text := range []string{
		"",
		"one",
		"three short words",
		longDoc("word", 200),
		"a b c d e a b c d e a b c d e", // repeated windows collapse
		"Case MATTERS not at all: case matters not at all:",
	} {
		set := Shingles(text)
		if !slices.IsSorted(set) {
			t.Errorf("%q: shingles not sorted", text)
		}
		if len(slices.Compact(slices.Clone(set))) != len(set) {
			t.Errorf("%q: shingles repeat", text)
		}
		if FromShingles(set) != Of(text) {
			t.Errorf("%q: FromShingles(Shingles) != Of", text)
		}
	}
	if n := len(Shingles(longDoc("word", 200))); n != 196 {
		t.Errorf("200 distinct words give %d shingles, want 196", n)
	}
	if n := len(Shingles("a b c d e a b c d e a b c d e")); n != 5 {
		t.Errorf("a five-word cycle gives %d distinct shingles, want 5", n)
	}
	if n := len(Shingles("three short words")); n != 1 {
		t.Errorf("a three-word text gives %d shingles, want 1", n)
	}
	if Shingles("") != nil {
		t.Error("an empty text has shingles")
	}
}
