package bundle

// The bundle's Phase-2 gate. MEMORY-SCALING.md §4 (regime 3) / Phase 2 is an
// extended account of why LoadPages' whole-crawl map — every PageRecord
// INCLUDING ContentText, the dominant per-record cost — is the wrong shape at
// scale, and Phase 2a exists specifically to keep page bodies off the finalize
// peak. An export that re-materialises that map would reintroduce the term on a
// new axis, and would do it invisibly: a bundle of a 500-page test crawl looks
// identical either way.
//
// So this pins the shape, not the outcome: the RAM a bundle retains must be FLAT
// on the page-count axis. The detector arm materialises the same crawl the way
// LoadPages does and MUST show the linear slope the gate forbids — if THAT arm
// goes flat the harness has gone blind, not the code correct.

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// bodyTextStore builds a crawl DB of n pages, each carrying a realistic body of
// content text — the per-record cost the whole-map load is dominated by — and,
// as a crawl with extraction.store_html on would, one stored HTML file per page,
// so a Full bundle exercises the file-per-page read path too. A small SQLite
// page cache is forced so a full-table scan can never retain table-proportional
// memory and confound the page-axis slope.
func bodyTextStore(t *testing.T, n int) *store.Crawl {
	t.Helper()
	cfg := config.Default()
	cfg.Extraction.StoreHTML = true
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`PRAGMA cache_size = -256`); err != nil { // ~256 KiB, bounded
		t.Fatal(err)
	}
	body := strings.Repeat("lorem ipsum dolor sit amet consectetur ", 40) // ~1.5 KB
	html := []byte("<html><body><h1>Heading</h1><p>" + body + "</p></body></html>")
	for i := range n {
		url := fmt.Sprintf("https://ex.test/page/%d", i)
		if err := st.Page(&crawler.PageRecord{
			URL: url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html",
			Facts: &parse.Facts{
				Titles: []string{"Page title"}, Headings: []parse.Heading{{Level: 1, Text: "Heading"}},
				WordCount: 240, ContentText: body,
				Links: []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.test/page/0", Anchor: "home"}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.Blob(url, "html", html); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// retainedHeapAlloc returns the live-heap bytes build()'s result retains over
// the pre-build baseline (forced-GC HeapAlloc; the result is kept alive across
// the post-measurement so it cannot be collected before the sample).
func retainedHeapAlloc(t *testing.T, build func() any) uint64 {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	obj := build()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(obj)
	if after.HeapAlloc <= before.HeapAlloc {
		return 0
	}
	return after.HeapAlloc - before.HeapAlloc
}

func TestBundleRAMFlatOnPageCount(t *testing.T) {
	if testing.Short() {
		t.Skip("bundle memory gate — skipped in -short")
	}
	const small, large = 200, 6_000
	const maxFlatSlope = 2 << 20   // one page record + the bounded page cache
	const minLinearSlope = 4 << 20 // the O(pages) map a LoadPages-based writer would hold

	// The contract: one sql.Rows scan, one page record at a time, nothing
	// retained — stored sources included, hence Full. The bundle goes to
	// io.Discard so the OUTPUT is not what is measured — only what the writer
	// holds while producing it.
	bundleRetained := func(n int) uint64 {
		st := bodyTextStore(t, n)
		defer st.Close()
		return retainedHeapAlloc(t, func() any {
			if err := Write(st, store.Info{ID: st.ID}, Options{Full: true}, io.Discard); err != nil {
				t.Fatal(err)
			}
			return nil
		})
	}
	flatSlope := int64(bundleRetained(large)) - int64(bundleRetained(small))
	t.Logf("bundle: retained(%d pages) - retained(%d pages) = %+.1f MB",
		large, small, float64(flatSlope)/(1<<20))
	if flatSlope > maxFlatSlope {
		t.Errorf("bundle retained %.1f MB more across a %d-page delta — it is page-linear again (want < %d MB); a whole-map load has been reintroduced",
			float64(flatSlope)/(1<<20), large-small, maxFlatSlope>>20)
	}

	// Detector: holding the same crawl the way LoadPages does MUST show the
	// linear slope the gate exists to forbid.
	loadPagesRetained := func(n int) uint64 {
		st := bodyTextStore(t, n)
		defer st.Close()
		return retainedHeapAlloc(t, func() any {
			pages, err := st.LoadPages()
			if err != nil {
				t.Fatal(err)
			}
			return pages
		})
	}
	mapSlope := int64(loadPagesRetained(large)) - int64(loadPagesRetained(small))
	t.Logf("LoadPages map: slope = %+.1f MB", float64(mapSlope)/(1<<20))
	if mapSlope < minLinearSlope {
		t.Errorf("detector check failed: the LoadPages arm grew only %.1f MB (want > %d MB) — the gate cannot see the failure mode it forbids",
			float64(mapSlope)/(1<<20), minLinearSlope>>20)
	}
}
