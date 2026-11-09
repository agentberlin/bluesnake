package nearcopy

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// generated is a Source that makes each page's text as it is asked for, so
// the test holds no corpus of its own and the heap it measures is the step's.
// Pages come in families of five, each a few words from the first, so the
// candidate search and the exact checks run too.
func generated(n, words int) Source {
	return func(fn func(*Page) error) error {
		rng := rand.New(rand.NewSource(int64(words)))
		var base []string
		for i := 0; i < n; i++ {
			if i%5 == 0 {
				base = make([]string, words)
				for k := range base {
					base[k] = fmt.Sprintf("w%d", rng.Intn(1_000_000))
				}
			}
			w := append([]string(nil), base...)
			for k := 0; k < i%5; k++ {
				w[rng.Intn(len(w))] = "edit"
			}
			if err := fn(page(fmt.Sprintf("https://ex.test/p%05d", i), strings.Join(w, " "))); err != nil {
				return err
			}
		}
		return nil
	}
}

// peakHeap runs fn and returns the highest heap in use while it ran, above
// what was in use before.
func peakHeap(fn func()) uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapInuse
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	fn()
	close(done)
	if p := peak.Load(); p > base {
		return p - base
	}
	return 0
}

// TestRunMemoryIsSetByPagesNotText is the step's memory gate: twenty-six times
// the text per page — 3,000 pages of 4,000 words rather than 150 — must not
// grow its heap by more than a fraction of what holding every page's
// shingle set would cost (3,000 × ~4,000 × 8 bytes = 96 MB). The sets live in
// the temporary file; memory holds a signature and a few fields per page.
func TestRunMemoryIsSetByPagesNotText(t *testing.T) {
	if testing.Short() {
		t.Skip("shingles 12 million words")
	}
	const pagesN = 3000
	measure := func(words int) (uint64, int) {
		var res *Result
		peak := peakHeap(func() {
			var err error
			if res, err = Run(generated(pagesN, words), Settings{Threshold: 80}, t.TempDir()); err != nil {
				t.Fatal(err)
			}
		})
		return peak, res.Pairs
	}
	short, shortPairs := measure(150)
	long, longPairs := measure(4000)
	t.Logf("peak heap: %.1f MB at 150 words a page, %.1f MB at 4,000 (%d and %d pairs)",
		float64(short)/(1<<20), float64(long)/(1<<20), shortPairs, longPairs)
	if shortPairs == 0 || longPairs == 0 {
		t.Fatal("no near copies found — the exact checks did not run")
	}
	const allowed = 24 << 20
	if long > short+allowed {
		t.Errorf("4,000-word pages peaked %.1f MB above 150-word pages (allowed %d MB): the step is holding text",
			float64(long-short)/(1<<20), allowed>>20)
	}
}
