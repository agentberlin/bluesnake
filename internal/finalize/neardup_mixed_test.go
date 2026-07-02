package finalize

// #74 N1: mixed minhash state. finalize used to run near-dup over the
// ContentText-free lite map whenever ANY page carried a stored signature;
// every signature-less page then fell back to minhash.Of("") — the all-max
// signature, 100%-similar to every other empty signature — so dissimilar
// pages were flagged content_near_duplicate at 100% en masse. Reachable via
// re-analysis of a crawl whose pages predate near-dup being switched on
// (resume --force, config change + reanalyze).

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func TestNearDup_MixedSignatureState_NoFalsePositives(t *testing.T) {
	st, seed := crawlNearDup(t, true)
	base := strings.TrimSuffix(seed, "/")

	// Forge the mixed state: strip the stored signatures from /a and /c (two
	// genuinely DISSIMILAR pages), as if they were crawled before near-dup was
	// enabled. /b keeps its signature, so the map is mixed.
	if _, err := st.DB().Exec(`UPDATE pages SET minhash = NULL WHERE url IN (?, ?)`,
		base+"/a", base+"/c"); err != nil {
		t.Fatal(err)
	}

	// Re-analyze (the `analyze` command / desktop Reanalyze path).
	cfg := config.Default()
	cfg.Analysis.NearDuplicates = true
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 90
	if _, err := Analyze(st, cfg); err != nil {
		t.Fatal(err)
	}

	urls, err := st.IssueURLs("content_near_duplicate")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, u := range urls {
		got[u] = true
	}
	// The genuine near-dup pair must still be found (via the ContentText
	// fallback for the signature-less pages)...
	for _, p := range []string{"/a", "/b"} {
		if !got[base+p] {
			t.Errorf("genuine near-duplicate %s not flagged in the mixed-signature state (got %v)", p, urls)
		}
	}
	// ...and the dissimilar signature-less page must NOT be flagged: with the
	// bug, /a and /c both hash the lite map's empty ContentText to the all-max
	// signature and "match" at 100%.
	if got[base+"/c"] {
		t.Errorf("dissimilar page /c flagged as a near-duplicate — empty-text signatures matched each other (N1): %v", urls)
	}
}
