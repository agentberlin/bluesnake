package runner

// P3 (issue #73): resuming a crawl that predates the gated `edges` table would
// overwrite its inlinks/discovered_from with empty values (the edges authority is
// empty). The resume path must refuse such a crawl loudly and leave its stored
// data untouched, rather than silently corrupting it.

import (
	"context"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestResume_RefusesPreEdgesCrawl(t *testing.T) {
	const chain = 4
	srv := chainServer(t, chain)

	// Interrupt after 2 pages so there's a genuine pending tail to resume.
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	crawlID := obs.startID

	// Mark the crawl as predating the edges table (as the v4 forward-migration of a
	// genuinely old DB would), and capture its stored aggregates.
	var beforeInlinks int
	var beforeFrom string
	func() {
		st, err := store.OpenCrawl(dir, crawlID)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.DB().Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('pre_edges','1')`); err != nil {
			t.Fatalf("mark pre_edges: %v", err)
		}
		pages, err := st.LoadPages()
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range pages {
			beforeInlinks += rec.Inlinks
			if rec.DiscoveredFrom != "" {
				beforeFrom = rec.DiscoveredFrom
			}
		}
	}()

	// Resume must refuse with a clear re-crawl message.
	_, err := New(dir, &recObs{}).Run(context.Background(),
		queue.JobSpec{ResumeID: crawlID}, nil)
	if err == nil {
		t.Fatal("resuming a pre-edges crawl should be refused, got nil error")
	}
	if !strings.Contains(err.Error(), "re-crawl") {
		t.Errorf("resume error = %q, want a clear re-crawl message", err)
	}

	// The refusal happened before finalize, so the stored aggregates are intact —
	// no silent corruption.
	st, err := store.OpenCrawl(dir, crawlID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	var afterInlinks int
	var afterFrom string
	for _, rec := range pages {
		afterInlinks += rec.Inlinks
		if rec.DiscoveredFrom != "" {
			afterFrom = rec.DiscoveredFrom
		}
	}
	if afterInlinks != beforeInlinks || afterFrom != beforeFrom {
		t.Errorf("stored aggregates changed despite refusal: inlinks %d->%d, discovered_from %q->%q",
			beforeInlinks, afterInlinks, beforeFrom, afterFrom)
	}
}
