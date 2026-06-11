package analyze

import "encoding/binary"

// fnv64aBand hashes one band (rowsPerBand consecutive signature values).
func fnv64aBand(sig signature, start, rows int) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	var buf [8]byte
	for i := start; i < start+rows; i++ {
		binary.LittleEndian.PutUint64(buf[:], sig[i])
		for _, b := range buf {
			h ^= uint64(b)
			h *= prime
		}
	}
	return h
}

// lshCandidates implements locality-sensitive hashing over minhash
// signatures: each signature is split into sigSize/rowsPerBand bands and two
// signatures are a candidate pair iff at least one band is identical. Bucket
// membership is by band hash, then verified by exact band comparison so hash
// collisions cannot produce false candidates. Pairs are deduplicated and
// ordered i < j. Replaces the all-pairs comparison: candidates still get an
// exact similarity verification by the caller.
func lshCandidates(sigs []signature, rowsPerBand int) [][2]int {
	if rowsPerBand <= 0 || len(sigs) < 2 {
		return nil
	}
	bandEqual := func(a, b signature, start int) bool {
		for i := start; i < start+rowsPerBand; i++ {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	seen := map[[2]int]bool{}
	var out [][2]int
	for start := 0; start+rowsPerBand <= sigSize; start += rowsPerBand {
		buckets := map[uint64][]int{}
		for i, sig := range sigs {
			h := fnv64aBand(sig, start, rowsPerBand)
			buckets[h] = append(buckets[h], i)
		}
		for _, idxs := range buckets {
			for x := 0; x < len(idxs); x++ {
				for y := x + 1; y < len(idxs); y++ {
					p := [2]int{idxs[x], idxs[y]} // ascending: sigs iterated in order
					if seen[p] || !bandEqual(sigs[p[0]], sigs[p[1]], start) {
						continue
					}
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// lshRowsPerBand picks the band width for a similarity threshold (percent).
// 4 rows × 16 bands is exact for the default 90% threshold (a >= 90% pair
// has <= 6 of 64 differing rows, so >= 10 intact bands). Lower thresholds
// need narrower bands to keep the candidate-recall probability ~1.
func lshRowsPerBand(thresholdPercent float64) int {
	switch {
	case thresholdPercent >= 85:
		return 4
	case thresholdPercent >= 55:
		return 2
	default:
		return 1
	}
}
