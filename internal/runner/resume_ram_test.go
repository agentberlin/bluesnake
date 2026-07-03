package runner

// The #77 residual gate: resume's per-bucket counter rehydration must NOT scale
// with the frontier. Before this fix loadResume materialised the whole admitted
// set (pages ∪ pending frontier) into a []frontier.Item — a frontier-linear RAM
// term on every bucket-capped resume. The fix streams that set through
// frontier.BucketCounts and retains only the small perDepth/perSub/perPath maps.

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/store"
)

// admittedFrontierStore builds a crawl DB carrying n admitted-but-pending
// frontier rows — one host, a handful of depths, padded URLs for a realistic
// per-row string cost. This is the state a bucket-capped crawl interrupted
// mid-flight leaves behind: the input resume rehydrates its counters from. A
// small SQLite page cache is forced so a full-table scan can never retain
// table-proportional memory and confound the frontier-axis slope.
func admittedFrontierStore(t *testing.T, n int) *store.Crawl {
	t.Helper()
	cfg := config.Default()
	cfg.Limits.MaxPerSubdomain = n + 1000 // a bucket cap IS configured (AnyBucketCap); never rejects
	st, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`PRAGMA cache_size = -256`); err != nil { // ~256 KiB, bounded
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 40)
	tx, err := st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO frontier(url, depth, redirect_hops, source, claimed, seq) VALUES(?,?,0,'',0,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("https://ex.com/facet/%s/%d", pad, i), i%5, i+1); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return st
}

// retainedHeapAlloc returns the live-heap bytes build()'s result retains over the
// pre-build baseline (forced-GC HeapAlloc; the result is kept alive across the
// post-measurement so it cannot be collected before the sample).
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

// TestResumeBucketCounterRAMFlat is the #77 residual "done" gate: the retained
// RAM of resume's bucket-counter rehydration must be flat on the frontier axis.
// Two admitted sets differing by ~50k rows must retain within a couple MB of
// each other — loadResume streams them and keeps only the aggregate maps. The
// detector arm materialises the same set the way the pre-#77 loader did
// (Resume.Admitted []frontier.Item) and MUST show the frontier-linear slope the
// gate forbids; if THAT arm goes flat the harness is blind, not the code fixed.
func TestResumeBucketCounterRAMFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("resume bucket-counter memory gate — skipped in -short")
	}
	const small, large = 5_000, 55_000
	const maxFlatSlope = 2 << 20   // aggregate maps + bounded page cache
	const minLinearSlope = 3 << 20 // the O(frontier) admitted slice the fix removes

	// The fix: loadResume streams the admitted rows through frontier.BucketCounts
	// (AnyBucketCap true), retaining only perDepth/perSub/perPath.
	loadResumeRetained := func(n int) uint64 {
		st := admittedFrontierStore(t, n)
		defer st.Close()
		lim := config.Default().Limits
		lim.MaxPerSubdomain = n + 1000
		return retainedHeapAlloc(t, func() any {
			r, err := loadResume(st, &lim)
			if err != nil {
				t.Fatal(err)
			}
			return r
		})
	}
	flatSlope := int64(loadResumeRetained(large)) - int64(loadResumeRetained(small))
	t.Logf("loadResume: retained(+%dk rows) - retained(+%dk rows) = %+.1f MB",
		large/1000, small/1000, float64(flatSlope)/(1<<20))
	if flatSlope > maxFlatSlope {
		t.Errorf("resume retained %.1f MB more across a %dk-row admitted delta — bucket rehydration is still frontier-linear (want < %d MB)",
			float64(flatSlope)/(1<<20), (large-small)/1000, maxFlatSlope>>20)
	}

	// Detector: materialising the admitted set (pre-#77 Resume.Admitted) MUST
	// show the linear slope the gate exists to forbid.
	materialisedRetained := func(n int) uint64 {
		st := admittedFrontierStore(t, n)
		defer st.Close()
		return retainedHeapAlloc(t, func() any {
			var items []frontier.Item
			if err := st.EachAdmitted(func(url string, depth int) error {
				items = append(items, frontier.Item{URL: url, Depth: depth})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return items
		})
	}
	matSlope := int64(materialisedRetained(large)) - int64(materialisedRetained(small))
	t.Logf("materialised slice (pre-#77): slope = %+.1f MB", float64(matSlope)/(1<<20))
	if matSlope < minLinearSlope {
		t.Errorf("detector check failed: the materialised-slice arm grew only %.1f MB (want > %d MB) — the gate cannot see the failure mode it forbids",
			float64(matSlope)/(1<<20), minLinearSlope>>20)
	}
}
