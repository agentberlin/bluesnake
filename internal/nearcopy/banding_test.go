package nearcopy

import (
	"math/rand"
	"testing"

	"github.com/agentberlin/bluesnake/internal/minhash"
)

// missProbability is the exact chance that the candidate rule loses a pair
// whose signature values each agree with probability t, independently: the
// pair is found when some band agrees on every row AND at least MinMatch
// values agree. A dynamic program over the bands (and the leftover rows) of
// the joint distribution of (values agreed, a band agreed).
func missProbability(b Bands, t float64) float64 {
	// dist[full][m]: probability of m agreements so far, with (full=1) or
	// without a fully agreeing band.
	var dist [2][minhash.SigSize + 1]float64
	dist[0][0] = 1
	step := func(rows int, isBand bool) {
		var next [2][minhash.SigSize + 1]float64
		for full := 0; full < 2; full++ {
			for m, p := range dist[full] {
				if p == 0 {
					continue
				}
				for j := 0; j <= rows; j++ {
					q := p * binom(rows, j, t)
					f := full
					if isBand && j == rows {
						f = 1
					}
					next[f][m+j] += q
				}
			}
		}
		dist = next
	}
	for k := 0; k < b.Bands; k++ {
		step(b.Rows, true)
	}
	if left := minhash.SigSize - b.Bands*b.Rows; left > 0 {
		step(left, false)
	}
	found := 0.0
	for m := b.MinMatch; m <= minhash.SigSize; m++ {
		found += dist[1][m]
	}
	return 1 - found
}

func binom(n, k int, t float64) float64 {
	switch {
	case t <= 0:
		if k == 0 {
			return 1
		}
		return 0
	case t >= 1:
		if k == n {
			return 1
		}
		return 0
	}
	return binomPMF(n, k, t)
}

// TestBandingKeepsTheMissBudget: at every threshold from 15% up, a pair whose
// share is exactly the threshold is lost less than twice in 10,000 — under
// the spec's once in 1,000 with room for a hash family that is not perfectly
// independent — and a pair above the threshold less often than one at it.
func TestBandingKeepsTheMissBudget(t *testing.T) {
	for th := 15; th <= 100; th++ {
		b := Banding(th)
		if b.Rows < 1 || b.Bands*b.Rows > minhash.SigSize || b.MinMatch < 0 || b.MinMatch > minhash.SigSize {
			t.Fatalf("%d%%: bad banding %+v", th, b)
		}
		at := missProbability(b, float64(th)/100)
		if at > 2*MissBudget {
			t.Errorf("%d%%: %+v misses a pair at the threshold with probability %.2g", th, b, at)
		}
		if th <= 95 {
			if above := missProbability(b, float64(th+5)/100); above > at {
				t.Errorf("%d%%: a pair 5 points above is missed more often (%.2g) than one at it (%.2g)", th, above, at)
			}
		}
	}
	// 80%, the threshold Berlin runs at, pinned: three-row bands, and an
	// exact check from 38 agreeing values (a 59% estimate) up.
	if b := Banding(80); b != (Bands{Rows: 3, Bands: 21, MinMatch: 38}) {
		t.Errorf("80%% banding = %+v", b)
	}
	// 100% needs every value to agree: only identical sets are candidates.
	if b := Banding(100); b.MinMatch != minhash.SigSize {
		t.Errorf("100%% banding = %+v, want MinMatch 64", b)
	}
}

// TestRecallAtTheThreshold measures what the probability model predicts, with
// the real signature hash family: 20,000 pairs whose exact share is 80% —
// shingle sets of 80 shared and 10 own values each — run through the
// candidate rule, which must lose fewer than 20 (once in 1,000). And 10,000 at
// 90%. The sets are random 64-bit values, as FNV shingle hashes are.
func TestRecallAtTheThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("simulates 30,000 pairs")
	}
	rng := rand.New(rand.NewSource(80))
	for _, tc := range []struct {
		threshold, shared, own, trials int
	}{
		{80, 80, 10, 20000}, // 80 / (80 + 10 + 10)
		{90, 90, 5, 10000},  // 90 / (90 + 5 + 5)
	} {
		b := Banding(tc.threshold)
		missed := 0
		a := make([]uint64, tc.shared+tc.own)
		c := make([]uint64, tc.shared+tc.own)
		for trial := 0; trial < tc.trials; trial++ {
			for i := 0; i < tc.shared; i++ {
				a[i] = rng.Uint64()
				c[i] = a[i]
			}
			for i := tc.shared; i < len(a); i++ {
				a[i], c[i] = rng.Uint64(), rng.Uint64()
			}
			sa, sc := minhash.FromShingles(a), minhash.FromShingles(c)
			if !candidate(&sa, &sc, b) {
				missed++
			}
		}
		rate := float64(missed) / float64(tc.trials)
		t.Logf("%d%%: %d of %d pairs at the threshold missed (%.2g; model %.2g)",
			tc.threshold, missed, tc.trials, rate, missProbability(b, float64(tc.threshold)/100))
		if rate >= 1e-3 {
			t.Errorf("%d%%: missed %d of %d pairs at the threshold — once in %.0f, want under once in 1,000",
				tc.threshold, missed, tc.trials, 1/rate)
		}
	}
}

// candidate is the rule candidates() applies to a pair, independent of the
// band it is found in.
func candidate(a, c *minhash.Signature, b Bands) bool {
	if matches(a, c) < b.MinMatch {
		return false
	}
	for k := 0; k < b.Bands; k++ {
		if rowsEqual(a, c, k*b.Rows, (k+1)*b.Rows) {
			return true
		}
	}
	return false
}
