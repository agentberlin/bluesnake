package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

// fcSink is a capturing sink that also answers as the content-hash authority,
// exercising the crawler's store-delegation branch (firstWithContent →
// FirstWithContent) without pulling in the store package (import cycle).
type fcSink struct {
	*capSink
	fcCanonical string
	fcFirst     bool
	fcErr       error
}

func (s *fcSink) FirstWithContent(hash, url string, claim bool) (string, bool, error) {
	return s.fcCanonical, s.fcFirst, s.fcErr
}

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

// TestResumeStateApplied pins that the crawler applies the WithResume data —
// no sink capability sniffing involved (#74 D2). The admitted set replays
// through the per-bucket counters, so a bucket that filled in the "prior
// session" admits nothing more; the edge sequence continues past MaxEdgeSeq,
// so this session's gated edges sort after the prior session's. (The old
// TestResumeRehydratesCountersWiring asserted only that Run returned nil —
// deleting the entire rehydration block kept it green, #74 R10. The full
// production-path effect is pinned by TestResumeEquivalence_ThroughRunner and
// TestResume_NoOverAdmitPerBucket_ThroughRunner in test/.)
func TestResumeStateApplied(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  `<a href="/new1">n1</a> <a href="/new2">n2</a>`,
		"/a": `<p>a</p>`, "/new1": `<p>n1</p>`, "/new2": `<p>n2</p>`,
	})
	abs := func(p string) string { return s.server.URL + p }
	cfg := config.Default()
	cfg.Speed.MaxThreads = 1
	cfg.Limits.MaxURLsPerDepth = 3 // depth-1 bucket: 2 admitted in the "prior session" + 1 here
	sink := newCapSink()
	c, err := New(cfg, WithSink(sink), WithResume(Resume{
		Processed:  []string{abs("/a")},
		Pending:    []frontier.Item{{URL: abs("/"), Depth: 0}},
		MaxEdgeSeq: 40,
		// Two depth-1 URLs already admitted by the prior session: only ONE of
		// /new1, /new2 may still fit the depth-1 bucket of 3.
		Admitted: []frontier.Item{{URL: abs("/a"), Depth: 1}, {URL: abs("/old"), Depth: 1}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), abs("/")); err != nil {
		t.Fatal(err)
	}
	pages := sink.snapshot()
	if pages[abs("/new1")] == nil && pages[abs("/new2")] == nil {
		t.Error("no new depth-1 page crawled — rehydrated counters over-count")
	}
	if pages[abs("/new1")] != nil && pages[abs("/new2")] != nil {
		t.Error("both /new1 and /new2 crawled — the admitted set did not rehydrate the depth-1 bucket counter")
	}
	// Edge seqs continue past the prior session's MaxEdgeSeq.
	for _, rec := range pages {
		for _, e := range rec.GatedEdges {
			if e.Seq <= 40 {
				t.Errorf("edge %s->%s got seq %d, want > the prior session's MaxEdgeSeq 40", rec.URL, e.Dst, e.Seq)
			}
		}
	}
}
