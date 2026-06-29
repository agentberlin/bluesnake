package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

// fcSink is a capturing sink that also answers as the content-hash authority and
// the admitted-set source, exercising the crawler's store-delegation branches
// (firstWithContent → FirstWithContent; resume rehydration → AdmittedItems)
// without pulling in the store package (which would be an import cycle).
type fcSink struct {
	*capSink
	fcCanonical string
	fcFirst     bool
	fcErr       error
	admitted    []frontier.Item
}

func (s *fcSink) FirstWithContent(hash, url string, claim bool) (string, bool, error) {
	return s.fcCanonical, s.fcFirst, s.fcErr
}
func (s *fcSink) AdmittedItems() ([]frontier.Item, error) { return s.admitted, nil }

// TestFirstWithContentDelegatesToSink covers the store-backed identical-content
// path (#70 M4): when the sink is the content authority, firstWithContent forwards
// to it (so the in-RAM map never grows), and a sink error degrades conservatively
// to "first/novel" while recording the error.
func TestFirstWithContentDelegatesToSink(t *testing.T) {
	sink := &fcSink{capSink: newCapSink(), fcCanonical: "https://ex/orig", fcFirst: false}
	c, err := New(config.Default(), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	if canon, first := c.firstWithContent("h", "https://ex/dup", true); first || canon != "https://ex/orig" {
		t.Errorf("delegation = (%q,%v), want (https://ex/orig,false)", canon, first)
	}
	// The in-RAM map must stay empty on the delegated path (the M4 bound).
	if len(c.seenContent) != 0 {
		t.Errorf("seenContent grew to %d on the delegated path, want 0", len(c.seenContent))
	}
	// Error arm: treat as first/novel and surface the error via the sink.
	sink.fcErr = errors.New("boom")
	if canon, first := c.firstWithContent("h2", "https://ex/x", true); !first || canon != "https://ex/x" {
		t.Errorf("error arm = (%q,%v), want (https://ex/x,true)", canon, first)
	}
	if c.sinkErr == nil {
		t.Error("a content-authority error was not recorded on the crawler")
	}
}

// TestResumeRehydratesCountersWiring covers the crawler's resume wiring (#70 M3):
// when resuming with a per-bucket cap configured and a sink that can supply the
// admitted set, Run consults AdmittedItems and replays them through the frontier
// counters. The counter effect itself is pinned in the frontier package; here we
// exercise the crawler-side wiring end to end.
func TestResumeRehydratesCountersWiring(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<p>x</p>"})
	cfg := config.Default()
	cfg.Limits.MaxURLsPerDepth = 5 // a per-bucket cap → anyBucketCapSet() is true
	sink := &fcSink{capSink: newCapSink(), admitted: []frontier.Item{{URL: "https://seen/", Depth: 0}}}
	c, err := New(cfg, WithSink(sink), WithResume([]string{"https://seen/"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}
}
