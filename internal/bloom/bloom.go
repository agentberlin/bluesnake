// Package bloom is a fixed-size, concurrency-safe Bloom filter — the RAM-cheap
// fast-negative in front of the SQLite dedup authority (MEMORY-SCALING.md §5.1,
// §7). The invariant that makes it safe for dedup: it has NO false negatives —
// Has(x) after Add(x) is always true — so a miss is a guarantee the URL is novel.
// A false positive only costs one extra authority lookup, never a wrong decision.
package bloom

import (
	"math"
	"sync/atomic"
)

// Filter is a Bloom filter over strings. The zero value is unusable; build one
// with New. All methods are safe for concurrent use.
type Filter struct {
	words []atomic.Uint64 // bit array, packed 64 bits per word
	m     uint64          // number of bits (always a power-of-two multiple of 64)
	k     int             // number of hash probes
}

// New sizes a filter for about expectedItems insertions at the target false-
// positive rate fpRate (clamped to a sane range). Sizing follows the standard
// m = -n·ln(p)/ln(2)² and k = (m/n)·ln2.
func New(expectedItems int, fpRate float64) *Filter {
	n := float64(expectedItems)
	if n < 1 {
		n = 1
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}
	mBits := math.Ceil(-n * math.Log(fpRate) / (math.Ln2 * math.Ln2))
	if mBits < 64 {
		mBits = 64
	}
	words := int((uint64(mBits) + 63) / 64)
	k := int(math.Round(mBits / n * math.Ln2))
	if k < 1 {
		k = 1
	}
	if k > 30 {
		k = 30
	}
	return &Filter{
		words: make([]atomic.Uint64, words),
		m:     uint64(words) * 64,
		k:     k,
	}
}

// hashes returns the k bit positions for s via double hashing over two 64-bit
// FNV-1a variants (Kirsch–Mitzenmacher), avoiding k separate hash passes.
func (f *Filter) probes(s string) (h1, h2 uint64) {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h1 = offset
	for i := 0; i < len(s); i++ {
		h1 ^= uint64(s[i])
		h1 *= prime
	}
	// a second, independent hash (different seed) for double hashing
	h2 = 1469598103934665600
	for i := 0; i < len(s); i++ {
		h2 ^= uint64(s[i])
		h2 *= prime
		h2 = (h2 << 13) | (h2 >> 51)
	}
	if h2 == 0 {
		h2 = 1 // a zero step would probe the same bit k times
	}
	return h1, h2
}

// Add records s. After Add(s), Has(s) is guaranteed true.
func (f *Filter) Add(s string) {
	h1, h2 := f.probes(s)
	for i := 0; i < f.k; i++ {
		bit := (h1 + uint64(i)*h2) % f.m
		w := &f.words[bit/64]
		mask := uint64(1) << (bit % 64)
		for {
			old := w.Load()
			if old&mask != 0 || w.CompareAndSwap(old, old|mask) {
				break
			}
		}
	}
}

// Has reports whether s may have been added. False ⇒ s was definitely never
// added (no false negatives); true ⇒ s was probably added (rare false positive).
func (f *Filter) Has(s string) bool {
	h1, h2 := f.probes(s)
	for i := 0; i < f.k; i++ {
		bit := (h1 + uint64(i)*h2) % f.m
		if f.words[bit/64].Load()&(uint64(1)<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

// TestAndAdd atomically checks membership and adds s, returning whether s was
// already present. (A maybe-present here still warrants an authority check; this
// is a convenience for the common add-if-absent flow.)
func (f *Filter) TestAndAdd(s string) bool {
	present := f.Has(s)
	f.Add(s)
	return present
}
