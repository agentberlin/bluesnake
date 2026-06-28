package bloom

import (
	"fmt"
	"sync"
	"testing"
)

// TestNoFalseNegatives (FR-02) is the load-bearing dedup invariant: every added
// item must always report Has==true. A false negative would re-crawl a seen URL.
func TestNoFalseNegatives(t *testing.T) {
	f := New(10000, 0.01)
	for i := 0; i < 10000; i++ {
		s := fmt.Sprintf("https://example.com/page/%d", i)
		f.Add(s)
		if !f.Has(s) {
			t.Fatalf("false negative immediately after Add(%q)", s)
		}
	}
	for i := 0; i < 10000; i++ {
		s := fmt.Sprintf("https://example.com/page/%d", i)
		if !f.Has(s) {
			t.Fatalf("false negative for %q after full load", s)
		}
	}
}

// TestFalsePositiveRateBounded sanity-checks sizing: never-added items rarely
// report present, so the authority isn't consulted for most novel URLs.
func TestFalsePositiveRateBounded(t *testing.T) {
	const n = 5000
	f := New(n, 0.01)
	for i := 0; i < n; i++ {
		f.Add(fmt.Sprintf("added/%d", i))
	}
	fp := 0
	const trials = 10000
	for i := 0; i < trials; i++ {
		if f.Has(fmt.Sprintf("absent/%d", i)) {
			fp++
		}
	}
	if rate := float64(fp) / trials; rate > 0.05 { // target 1%, generous ceiling
		t.Errorf("false-positive rate = %.3f, want well under 0.05", rate)
	}
}

// TestConcurrentNoFalseNegatives runs Add/Has from many goroutines; every added
// key must remain present (-race catches torn word writes).
func TestConcurrentNoFalseNegatives(t *testing.T) {
	f := New(20000, 0.01)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2500; i++ {
				s := fmt.Sprintf("g%d-i%d", g, i)
				f.Add(s)
				if !f.Has(s) {
					t.Errorf("false negative after concurrent Add(%q)", s)
					return
				}
			}
		}()
	}
	wg.Wait()
	for g := 0; g < 8; g++ {
		for i := 0; i < 2500; i++ {
			s := fmt.Sprintf("g%d-i%d", g, i)
			if !f.Has(s) {
				t.Fatalf("false negative for %q after concurrent load", s)
			}
		}
	}
}

// TestTinyFilterStillNoFalseNegatives (forced ~100% FP) pins that even a
// saturated filter never produces a false negative — the property that lets the
// DB authority be the sole arbiter of false positives.
func TestTinyFilterStillNoFalseNegatives(t *testing.T) {
	f := New(1, 0.5)
	keys := []string{"a", "b", "c", "d", "e", "lorem", "ipsum", ""}
	for _, k := range keys {
		f.Add(k)
	}
	for _, k := range keys {
		if !f.Has(k) {
			t.Errorf("tiny filter false negative for %q", k)
		}
	}
}

// TestTestAndAdd pins the add-if-absent convenience: first sight reports absent,
// thereafter present.
func TestTestAndAdd(t *testing.T) {
	f := New(100, 0.01)
	if f.TestAndAdd("x") {
		t.Error("TestAndAdd reported a never-seen key as present")
	}
	if !f.TestAndAdd("x") {
		t.Error("TestAndAdd reported an added key as absent")
	}
}
