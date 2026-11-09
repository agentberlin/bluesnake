package nearcopy

import (
	"math"

	"github.com/agentberlin/bluesnake/internal/minhash"
)

// MissBudget bounds, separately, each of the two ways the candidate search can
// lose a pair whose share is exactly the threshold: no band of its signatures
// matches, or too few of its signature values do. Together a pair at the
// threshold is missed less than twice in 10,000 times (a pair above it, less
// often still) — the spec asks for less than once in 1,000.
const MissBudget = 1e-4

// Bands is the candidate rule for a threshold. A pair of pages is checked
// exactly when its two signatures agree on every row of at least one band of
// Rows rows (Bands bands, the leftover rows in none), and on at least MinMatch
// of the 64 values overall. Each value agrees with probability equal to the
// pair's true share, independently, so both conditions are binomial in it.
type Bands struct {
	Rows, Bands, MinMatch int
}

// Banding picks the candidate rule for a threshold (percent). Rows is the
// widest band whose miss chance at the threshold is within MissBudget — wider
// bands make fewer false candidates — and MinMatch the highest agreement whose
// shortfall chance is. Below about 14% even single-row bands cannot keep the
// budget, and Rows stays 1: the search is still run, at the recall 64 values
// allow.
//
// MinMatch is the margin the old near-duplicate check lacked. It dropped a
// candidate whose estimate was under the threshold, so a pair truly at 81%
// that estimated 77% vanished; the estimate's spread at 80% is ±5 points.
// MinMatch sits far enough below the threshold that only a pair below it
// fails it, and the exact share, not the estimate, then decides.
func Banding(threshold int) Bands {
	t := float64(threshold) / 100
	b := Bands{Rows: 1}
	for r := 8; r >= 1; r-- {
		if bandMiss(t, r) <= MissBudget {
			b.Rows = r
			break
		}
	}
	b.Bands = minhash.SigSize / b.Rows
	for k := minhash.SigSize; k >= 0; k-- {
		if agreeBelow(k, t) <= MissBudget {
			b.MinMatch = k
			break
		}
	}
	return b
}

// bandMiss is the chance that no band of r rows agrees, for a pair whose
// values agree with probability t each: (1 - t^r)^(64/r).
func bandMiss(t float64, r int) float64 {
	return math.Pow(1-math.Pow(t, float64(r)), float64(minhash.SigSize/r))
}

// agreeBelow is the chance that fewer than k of the 64 values agree:
// P(X < k) for X ~ Binomial(64, t).
func agreeBelow(k int, t float64) float64 {
	switch {
	case k <= 0:
		return 0
	case t <= 0:
		return 1
	case t >= 1:
		if k > minhash.SigSize {
			return 1
		}
		return 0
	}
	p := 0.0
	for j := 0; j < k && j <= minhash.SigSize; j++ {
		p += binomPMF(minhash.SigSize, j, t)
	}
	return p
}

// binomPMF is P(X = k) for X ~ Binomial(n, t), 0 < t < 1, in log space.
func binomPMF(n, k int, t float64) float64 {
	ln := lgamma(n+1) - lgamma(k+1) - lgamma(n-k+1) +
		float64(k)*math.Log(t) + float64(n-k)*math.Log1p(-t)
	return math.Exp(ln)
}

func lgamma(x int) float64 {
	v, _ := math.Lgamma(float64(x))
	return v
}
