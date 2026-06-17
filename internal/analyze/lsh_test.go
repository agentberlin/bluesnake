package analyze

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

func mkSig(f func(i int) uint64) signature {
	var s signature
	for i := range s {
		s[i] = f(i)
	}
	return s
}

// pairSet validates the lshCandidates result contract (i < j, no duplicate
// pairs, indices in range) and converts it to a set.
func pairSet(t *testing.T, pairs [][2]int, n int) map[[2]int]bool {
	t.Helper()
	set := map[[2]int]bool{}
	for _, p := range pairs {
		if p[0] < 0 || p[1] >= n {
			t.Fatalf("pair %v out of range for %d signatures", p, n)
		}
		if p[0] >= p[1] {
			t.Errorf("pair %v not ordered i < j", p)
		}
		if set[p] {
			t.Errorf("duplicate pair %v", p)
		}
		set[p] = true
	}
	return set
}

func TestLSHCandidates(t *testing.T) {
	base := mkSig(func(i int) uint64 { return uint64(i) + 1 })

	// equal to base only at indices 0..3: exactly one band at rowsPerBand=4,
	// only half a band at rowsPerBand=8
	shareBandZero := base
	for i := 4; i < sigSize; i++ {
		shareBandZero[i] = uint64(i) + 1000
	}

	// one differing row inside every 4-row band: 3 of 4 matching rows must
	// not make a band identical
	oneOffPerBand := base
	for band := 0; band < sigSize/4; band++ {
		oneOffPerBand[band*4] = 9000 + uint64(band)
	}

	// equal to base only at indices 0..7: a full band at rowsPerBand=8
	shareFirstEight := base
	for i := 8; i < sigSize; i++ {
		shareFirstEight[i] = uint64(i) + 2000
	}

	cases := []struct {
		name string
		sigs []signature
		rows int
		want [][2]int
	}{
		{"empty input", nil, 4, nil},
		{"single signature", []signature{base}, 4, nil},
		// identical sigs share all 16 bands but the pair is reported once
		{"identical pair", []signature{base, base}, 4, [][2]int{{0, 1}}},
		{"single shared band suffices", []signature{base, shareBandZero}, 4, [][2]int{{0, 1}}},
		{"every band differs by one row", []signature{base, oneOffPerBand}, 4, nil},
		{"three identical", []signature{base, base, base}, 4, [][2]int{{0, 1}, {0, 2}, {1, 2}}},
		{"two disjoint identical pairs", []signature{base, base, oneOffPerBand, oneOffPerBand}, 4,
			[][2]int{{0, 1}, {2, 3}}},
		{"8 rows per band, shared 8-row prefix", []signature{base, shareFirstEight}, 8, [][2]int{{0, 1}}},
		{"8 rows per band, 4-row prefix is not a band", []signature{base, shareBandZero}, 8, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pairSet(t, lshCandidates(tc.sigs, tc.rows), len(tc.sigs))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d pairs %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for _, p := range tc.want {
				if !got[p] {
					t.Errorf("missing pair %v", p)
				}
			}
		})
	}
}

// TestLSHEquivalentToAllPairs pins that LSH banding plus exact verification
// finds the same >= 90%-similar pairs as the all-pairs loop it replaces.
// The equivalence is exact, not probabilistic: a pair at similarity >= 0.9
// has >= 58 of 64 matching rows, so at most 6 of the 16 four-row bands are
// broken and at least one band is always identical.
func TestLSHEquivalentToAllPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vocab := func(prefix string, n int) []string {
		v := make([]string, n)
		for i := range v {
			v[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return v
	}
	baseVocab := vocab("base", 40)
	otherVocab := vocab("unrel", 40) // disjoint from baseVocab
	randomDoc := func(v []string, n int) []string {
		words := make([]string, n)
		for i := range words {
			words[i] = v[rng.Intn(len(v))]
		}
		return words
	}
	// 20 base docs, each with 2 near-copies (an adjacent run of 2-3 words
	// replaced keeps shingle overlap, hence similarity, well above 90%),
	// plus 60 unrelated docs from a disjoint vocabulary: 120 docs total.
	var docs []string
	for d := range 20 {
		base := randomDoc(baseVocab, 200)
		docs = append(docs, strings.Join(base, " "))
		for c := range 2 {
			cp := append([]string(nil), base...)
			run := 2 + rng.Intn(2)
			start := rng.Intn(len(cp) - run)
			for k := range run {
				cp[start+k] = fmt.Sprintf("swap%d_%d_%d", d, c, k)
			}
			docs = append(docs, strings.Join(cp, " "))
		}
	}
	for range 60 {
		docs = append(docs, strings.Join(randomDoc(otherVocab, 200), " "))
	}
	sigs := make([]signature, len(docs))
	for i, d := range docs {
		sigs[i] = minhash(d)
	}

	const threshold = 0.9
	want := map[[2]int]bool{}
	for i := range sigs {
		for j := i + 1; j < len(sigs); j++ {
			if sigs[i].similarity(sigs[j]) >= threshold {
				want[[2]int{i, j}] = true
			}
		}
	}
	// 37 pairs pass with this seed; guard against the comparison going
	// vacuous if the corpus generation drifts.
	if len(want) < 30 {
		t.Fatalf("corpus produced only %d similar pairs, want >= 30", len(want))
	}

	got := map[[2]int]bool{}
	for p := range pairSet(t, lshCandidates(sigs, 4), len(sigs)) {
		if sigs[p[0]].similarity(sigs[p[1]]) >= threshold {
			got[p] = true
		}
	}
	for p := range want {
		if !got[p] {
			t.Errorf("pair %v (similarity %.4f) missed by LSH candidates",
				p, sigs[p[0]].similarity(sigs[p[1]]))
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("pair %v reported via LSH but absent from all-pairs set", p)
		}
	}
}

// TestNearDuplicateClusterOfThree pins Run() near-duplicate output for a
// cluster larger than a pair: with three mutually >= 90%-similar pages every
// member counts both others, and unrelated pages stay unflagged.
func TestNearDuplicateClusterOfThree(t *testing.T) {
	var w []string
	for i := range 200 {
		w = append(w, fmt.Sprintf("word%dx", i))
	}
	long := strings.Join(w, " ")
	wb := append([]string(nil), w...)
	wb[100] = "changedb"
	similarB := strings.Join(wb, " ")
	wc := append([]string(nil), w...)
	wc[101] = "changedc"
	similarC := strings.Join(wc, " ")
	var d []string
	for i := range 200 {
		d = append(d, fmt.Sprintf("other%dy", i))
	}
	different := strings.Join(d, " ")

	mk := func(url, text, hash string) *crawler.PageRecord {
		p := page(url)
		p.Facts.ContentText = text
		p.Facts.WordCount = len(strings.Fields(text))
		p.Facts.Hash = hash
		return p
	}
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 90
	cfg.Analysis.NearDuplicates = true
	pages := toMap(
		mk("https://ex.com/a", long, "h1"),
		mk("https://ex.com/b", similarB, "h2"),
		mk("https://ex.com/c", similarC, "h3"),
		mk("https://ex.com/d", different, "h4"),
	)
	r := Run(pages, nil, nil, cfg)

	cluster := []string{"https://ex.com/a", "https://ex.com/b", "https://ex.com/c"}
	for _, url := range cluster {
		nd, ok := r.NearDups[url]
		if !ok {
			t.Fatalf("%s missing from NearDups: %+v", url, r.NearDups)
		}
		if nd.Count < 2 {
			t.Errorf("%s Count = %d, want >= 2 (similar to both cluster pages)", url, nd.Count)
		}
		if nd.ClosestSimilarity < 90 {
			t.Errorf("%s ClosestSimilarity = %v, want >= 90", url, nd.ClosestSimilarity)
		}
		if nd.ClosestMatch == url || !slices.Contains(cluster, nd.ClosestMatch) {
			t.Errorf("%s ClosestMatch = %q, want another cluster page", url, nd.ClosestMatch)
		}
		if !hasOcc(r, url, "content_near_duplicate") {
			t.Errorf("missing content_near_duplicate occurrence on %s", url)
		}
	}
	if _, ok := r.NearDups["https://ex.com/d"]; ok {
		t.Error("unrelated page flagged as near duplicate")
	}
	if hasOcc(r, "https://ex.com/d", "content_near_duplicate") {
		t.Error("unrelated page has content_near_duplicate occurrence")
	}
}
