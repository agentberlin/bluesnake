package analyze

import "github.com/agentberlin/bluesnake/internal/minhash"

// The minhash signature primitive lives in internal/minhash (a leaf package the
// crawler can also import, to precompute and persist a page's signature at crawl
// time). analyze keeps thin local aliases so the LSH/near-dup code reads the
// same as before.
const sigSize = minhash.SigSize

type signature = minhash.Signature

// ContentSimilarity reports the content similarity (0–100%) between two
// content-area texts, using the same 5-word-shingle minhash signature as
// near-duplicate analysis. It is the single source of truth for "how alike is
// this content", shared by the Near Duplicates report and crawl-comparison
// "content" change detection so both speak the same similarity language. Two
// empty texts are 100% similar (no content either side); empty vs non-empty is
// ~0% (content appeared or vanished).
func ContentSimilarity(a, b string) float64 {
	return minhash.Of(a).Similarity(minhash.Of(b)) * 100
}
