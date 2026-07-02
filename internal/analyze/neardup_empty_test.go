package analyze

// #74 N1 defense at the analyze seam: a page with neither a stored minhash
// signature nor ContentText must be EXCLUDED from near-dup — never given
// minhash.Of(""), the all-max signature that matches every other empty
// signature at 100%.

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
)

func TestMinhashOfEmptyNeverMatches(t *testing.T) {
	cfg := config.Default()
	cfg.Analysis.NearDuplicates = true
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Content.NearDuplicates.Threshold = 90

	// Two dissimilar pages as a ContentText-free lite map would carry them in
	// the mixed-signature state: real word counts, no signature, no body text.
	pages := map[string]*crawler.PageRecord{}
	for _, u := range []string{"https://ex.com/a", "https://ex.com/b"} {
		pages[u] = &crawler.PageRecord{
			URL: u, Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
			Indexable: true,
			// ContentText stripped (lite map); distinct raw-body hashes, so the
			// exact-duplicate exclusion cannot mask a false near-dup match.
			Facts: &parse.Facts{WordCount: 200, Hash: "hash-" + u},
		}
	}

	res := Run(pages, nil, nil, cfg)
	if len(res.NearDups) != 0 {
		t.Errorf("near-dup matched %d signature-less, text-less pages — the empty signature must never enter matching: %v",
			len(res.NearDups), res.NearDups)
	}
}
