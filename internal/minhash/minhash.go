// Package minhash computes minhash signatures over word shingles — the shared
// content-similarity primitive used by near-duplicate analysis and crawl
// comparison. It lives in its own leaf package (no internal imports) so the
// crawler can precompute a page's signature at crawl time and persist it,
// while analyze (which imports crawler) reads it back — neither side pulls the
// other in. See MEMORY-SCALING.md §5.5 (near-dup minhash column).
package minhash

import (
	"encoding/binary"
	"hash/fnv"
	"strings"
)

// SigSize is the number of permutations (uint64 minima) in a signature. 64
// permutations give a Jaccard-similarity estimate with ~12.5% standard error
// per band, which the LSH banding in analyze tolerates at the default 90%
// near-duplicate threshold.
const SigSize = 64

// Signature is a minhash signature over 5-word shingles.
type Signature [SigSize]uint64

// EncodedLen is the byte length of an encoded signature (little-endian uint64s).
const EncodedLen = SigSize * 8

// Of builds a signature from 5-word shingles of the text. Similarity between
// two signatures estimates the Jaccard similarity of the shingle sets (the
// Screaming Frog near-duplicate model). It is deterministic and config-
// independent (fixed 5-word shingle, fixed SigSize permutations), which is what
// lets the crawler precompute it once and the analyzer reuse it verbatim.
func Of(text string) Signature {
	var sig Signature
	for i := range sig {
		sig[i] = ^uint64(0)
	}
	words := strings.Fields(strings.ToLower(text))
	if len(words) == 0 {
		return sig
	}
	shingleLen := min(5, len(words))
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

// Similarity estimates the Jaccard similarity (0–1) of the two shingle sets as
// the fraction of matching signature positions.
func (s Signature) Similarity(other Signature) float64 {
	match := 0
	for i := range s {
		if s[i] == other[i] {
			match++
		}
	}
	return float64(match) / float64(SigSize)
}

// Encode serializes a signature to a fixed-width little-endian byte slice for
// storage in the pages.minhash BLOB column.
func (s Signature) Encode() []byte {
	b := make([]byte, EncodedLen)
	for i, v := range s {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}

// Decode reverses Encode. A slice that is not exactly EncodedLen bytes (a
// missing/corrupt column) decodes to the empty-text signature, so a caller that
// reaches Decode on bad data degrades to "no content" rather than panicking.
func Decode(b []byte) Signature {
	if len(b) != EncodedLen {
		return Of("")
	}
	var s Signature
	for i := range s {
		s[i] = binary.LittleEndian.Uint64(b[i*8:])
	}
	return s
}
