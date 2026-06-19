package analyze

import (
	"hash/fnv"
	"strings"
)

const sigSize = 64

// signature is a minhash signature over word shingles.
type signature [sigSize]uint64

// minhash builds a signature from 5-word shingles of the text. Similarity
// between two signatures estimates the Jaccard similarity of the shingle
// sets (the Screaming Frog near-duplicate model).
func minhash(text string) signature {
	var sig signature
	for i := range sig {
		sig[i] = ^uint64(0)
	}
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return sig
	}
	shingleLen := 5
	if len(words) < shingleLen {
		shingleLen = len(words)
	}
	for i := 0; i+shingleLen <= len(words); i++ {
		h := fnv.New64a()
		for _, w := range words[i : i+shingleLen] {
			h.Write([]byte(w))
			h.Write([]byte{0})
		}
		base := h.Sum64()
		for k := range sig {
			// k-th permutation via multiply-xor mixing
			v := base ^ (uint64(k)*0x9E3779B97F4A7C15 + 0x517CC1B727220A95)
			v *= 0xBF58476D1CE4E5B9
			v ^= v >> 27
			if v < sig[k] {
				sig[k] = v
			}
		}
	}
	return sig
}

func (s signature) similarity(other signature) float64 {
	match := 0
	for i := range s {
		if s[i] == other[i] {
			match++
		}
	}
	return float64(match) / float64(sigSize)
}

// ContentSimilarity reports the content similarity (0–100%) between two
// content-area texts, using the same 5-word-shingle minhash signature as
// near-duplicate analysis. It is the single source of truth for "how alike is
// this content", shared by the Near Duplicates report and crawl-comparison
// "content" change detection so both speak the same similarity language. Two
// empty texts are 100% similar (no content either side); empty vs non-empty is
// ~0% (content appeared or vanished).
func ContentSimilarity(a, b string) float64 {
	return minhash(a).similarity(minhash(b)) * 100
}
