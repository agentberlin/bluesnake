package main

import (
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
)

// MemberPulse: trend points over the comparable history plus the since-last-
// crawl delta reduced from the (cached) pairwise comparison.
func TestMemberPulse(t *testing.T) {
	a := testApp(t)
	pa := NewProjectApp(a)

	// one comparable crawl: trend only, no delta yet
	seedCompareCrawl(t, a.storeDir, []*crawler.PageRecord{
		page("https://ex.com/a", 200, true, "Alpha"),
		page("https://ex.com/gone", 200, true, "Doomed"),
	}, []string{"https://ex.com/gone"})
	mp, err := pa.MemberPulse("", "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if mp.OK || len(mp.Trend) != 1 {
		t.Fatalf("single-crawl pulse = ok:%v trend:%d, want no delta + 1 point", mp.OK, len(mp.Trend))
	}
	if mp.Trend[0].URLs != 2 || mp.Trend[0].Issues == 0 {
		t.Errorf("trend point = %+v, want 2 URLs and title_missing counted", mp.Trend[0])
	}

	// a second crawl: /a flips to 404, /gone vanishes, /fresh appears.
	// Registry `started` has second resolution — space the crawls a beat apart
	// so the newest-first history (and thus the prev/curr pair) is stable.
	time.Sleep(1100 * time.Millisecond)
	seedCompareCrawl(t, a.storeDir, []*crawler.PageRecord{
		page("https://ex.com/a", 404, false, ""),
		page("https://ex.com/fresh", 200, true, "Fresh"),
	}, []string{"https://ex.com/fresh"})
	mp, err = pa.MemberPulse("", "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if !mp.OK || mp.Cached {
		t.Fatalf("pulse = ok:%v cached:%v, want fresh delta", mp.OK, mp.Cached)
	}
	if len(mp.Trend) != 2 || mp.Trend[0].Started > mp.Trend[1].Started {
		t.Errorf("trend = %+v, want 2 points oldest first", mp.Trend)
	}
	if mp.PagesPrev != 2 || mp.PagesCurr != 2 || mp.NewPages != 1 || mp.RemovedPages != 1 {
		t.Errorf("pages delta = %+v", mp)
	}
	if mp.StatusFlips != 1 || mp.IndexabilityFlips != 1 {
		t.Errorf("flips = %d/%d, want 1/1", mp.StatusFlips, mp.IndexabilityFlips)
	}
	// title_missing: /fresh is a new occurrence, /gone's went with its page
	if mp.IssuesAppeared != 1 || mp.IssuesResolved != 1 {
		t.Errorf("issue movement = +%d/−%d, want 1/1", mp.IssuesAppeared, mp.IssuesResolved)
	}
	if mp.PrevID == "" || mp.CurrID == "" || mp.PrevStarted == 0 || mp.CurrStarted == 0 {
		t.Errorf("pair provenance missing: %+v", mp)
	}

	// the delta rides the comparison cache
	mp2, err := pa.MemberPulse("", "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if !mp2.Cached {
		t.Error("second pulse did not hit the comparison cache")
	}

	// unknown site: no crawls, no delta, no error
	empty, err := pa.MemberPulse("", "never-crawled.com")
	if err != nil {
		t.Fatal(err)
	}
	if empty.OK || len(empty.Trend) != 0 {
		t.Errorf("empty site pulse = %+v", empty)
	}
}
