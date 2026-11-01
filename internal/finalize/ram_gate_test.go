package finalize

// The memory gate for the links axis. A 66,000-URL crawl of link-heavy pages
// (about 650 links a page) was killed twice in finalize on a 16 GB machine:
// the depth step loaded the links table whole as Go strings, the page map kept
// every page's link list, and analyze loaded the rows again beside it, so
// finalize's peak grew with the link count — about 1.3 MB a page. Finalize now
// pulls each page's links on demand for depth, drops the link lists from the
// page map and streams them per page for the checks, and keeps the link graph
// as int32 edges. This gate pins that: two stores with the same pages but 40×
// the hyperlinks must peak within a small budget of each other, while a
// detector arm that materialises the rows the old way must show the slope the
// gate forbids — if THAT arm goes flat the harness is blind, not the code fixed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// syntheticSite stores n crawled pages under one host, each carrying
// linksPerPage hyperlinks (with anchors and element paths, as a real crawl
// stores them) to other pages of the set, plus the matching gated edges, after
// a one-page crawl that gives the crawler its scope.
func syntheticSite(t *testing.T, n, linksPerPage int) (*store.Crawl, *crawler.Crawler, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/p0">start</a></body></html>`)
	}))
	t.Cleanup(srv.Close)
	seed := srv.URL + "/"
	cfg := config.Default()
	st, err := store.CreateCrawl(t.TempDir(), []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	var seq int64 = 1000
	for i := 0; i < n; i++ {
		url := fmt.Sprintf("%sp%d", seed, i)
		facts := &parse.Facts{Titles: []string{fmt.Sprintf("Page %d", i)}, WordCount: 50,
			ContentText: "some body text for the page that is not very long at all"}
		var edges []crawler.GatedEdge
		for k := 0; k < linksPerPage; k++ {
			dst := fmt.Sprintf("%sp%d", seed, (i+1+k*7)%n)
			facts.Links = append(facts.Links, parse.Link{
				Type: parse.Hyperlink, URL: dst, Raw: fmt.Sprintf("/p%d", (i+1+k*7)%n),
				Anchor:   fmt.Sprintf("Read about page %d in the navigation", (i+1+k*7)%n),
				PathType: "root_relative", ElemPath: fmt.Sprintf("/html/body/nav/ul/li[%d]/a", k+1),
				PositionPath: fmt.Sprintf("/html/body/nav#main/ul/li[%d]/a", k+1), Position: "navigation",
			})
			seq++
			edges = append(edges, crawler.GatedEdge{Dst: dst, Hyperlink: true, Seq: seq})
		}
		if err := st.Page(&crawler.PageRecord{
			URL: url, Scope: "internal", State: crawler.StateCrawled, StatusCode: 200, Status: "OK",
			ContentType: "text/html", Indexable: true, Depth: 1, DiscoveredFrom: seed,
			Facts: facts, GatedEdges: edges,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st, c, seed
}

// peakHeapDuring runs fn with the collector kept tight and returns the highest
// live-heap reading sampled while it ran, over the pre-run baseline — the
// finalize peak a container's memory limit would see, less the test's own.
func peakHeapDuring(fn func()) uint64 {
	old := debug.SetGCPercent(10)
	defer debug.SetGCPercent(old)
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	stop := make(chan struct{})
	done := make(chan uint64)
	go func() {
		var peak uint64
		var m runtime.MemStats
		tick := time.NewTicker(200 * time.Microsecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				done <- peak
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
		}
	}()
	fn()
	var end runtime.MemStats
	runtime.ReadMemStats(&end)
	close(stop)
	peak := <-done
	if end.HeapAlloc > peak {
		peak = end.HeapAlloc
	}
	if peak <= base.HeapAlloc {
		return 0
	}
	return peak - base.HeapAlloc
}

func TestFinalizeHeapFlatOnLinksAxis(t *testing.T) {
	if testing.Short() {
		t.Skip("finalize links-axis memory gate — skipped in -short")
	}
	const pages = 300
	const fewLinks, manyLinks = 4, 160 // 1,200 vs 48,000 hyperlink rows over the same page set
	// What the extra 46,800 hyperlinks may cost finalize: 8 bytes each as int32
	// edges (0.4 MB) plus collector slack. Materialised the old way — a Go
	// string pair per row, a parse.Link per link in the page map — they cost
	// several hundred bytes each, which the detector arm must show.
	const maxFlatSlope = 10 << 20
	const minDetectorSlope = 20 << 20

	cfg := config.Default()
	finalizePeak := func(links int) uint64 {
		st, c, seed := syntheticSite(t, pages, links)
		return peakHeapDuring(func() {
			if err := st.SaveInlinksFromEdges(c.NormalizeSeeds(seed)); err != nil {
				t.Fatal(err)
			}
			if err := recomputeDepths(c, st, []string{seed}); err != nil {
				t.Fatal(err)
			}
			if _, err := Analyze(st, cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
	flatSlope := int64(finalizePeak(manyLinks)) - int64(finalizePeak(fewLinks))
	t.Logf("finalize: peak(%d links/page) - peak(%d links/page) = %+.1f MB", manyLinks, fewLinks, float64(flatSlope)/(1<<20))
	if flatSlope > maxFlatSlope {
		t.Errorf("finalize's peak heap grew %.1f MB with the link count (max %.1f MB) — a link-shaped structure is being retained",
			float64(flatSlope)/(1<<20), float64(maxFlatSlope)/(1<<20))
	}

	// Detector arm: the shape finalize used to hold — every links row as Go
	// strings plus every page's decoded link list — kept alive together.
	type linkRow struct {
		src, dst, typ string
		nofollow      bool
	}
	materialisedPeak := func(links int) uint64 {
		st, _, _ := syntheticSite(t, pages, links)
		return peakHeapDuring(func() {
			var rows []linkRow
			lists := map[string][]parse.Link{}
			rs, err := st.DB().Query(`SELECT src, dst, type, nofollow FROM links`)
			if err != nil {
				t.Fatal(err)
			}
			for rs.Next() {
				var r linkRow
				var nf int
				if err := rs.Scan(&r.src, &r.dst, &r.typ, &nf); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, r)
			}
			rs.Close()
			ps, err := st.DB().Query(`SELECT url, json_extract(facts, '$.Links') FROM pages WHERE facts IS NOT NULL`)
			if err != nil {
				t.Fatal(err)
			}
			for ps.Next() {
				var url string
				var raw []byte
				if err := ps.Scan(&url, &raw); err != nil {
					t.Fatal(err)
				}
				var l []parse.Link
				if len(raw) > 0 {
					json.Unmarshal(raw, &l)
				}
				lists[url] = l
			}
			ps.Close()
			runtime.KeepAlive(rows)
			runtime.KeepAlive(lists)
		})
	}
	detectorSlope := int64(materialisedPeak(manyLinks)) - int64(materialisedPeak(fewLinks))
	t.Logf("materialised rows + link lists: slope = %+.1f MB", float64(detectorSlope)/(1<<20))
	if detectorSlope < minDetectorSlope {
		t.Errorf("detector arm slope %.1f MB is below %.1f MB — the harness cannot see a links-axis regression",
			float64(detectorSlope)/(1<<20), float64(minDetectorSlope)/(1<<20))
	}
}
