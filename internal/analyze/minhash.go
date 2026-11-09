package analyze

import "github.com/agentberlin/bluesnake/internal/minhash"

// ContentSimilarity reports the content similarity (0–100%) between two
// content-area texts, estimated with the same 5-word-shingle minhash signature
// the near-copy step picks its candidates with. Crawl comparison uses it for
// "content" change detection. Two empty texts are 100% similar (no content
// either side); empty vs non-empty is ~0% (content appeared or vanished).
func ContentSimilarity(a, b string) float64 {
	return minhash.Of(a).Similarity(minhash.Of(b)) * 100
}
