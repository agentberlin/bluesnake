package nearcopy

import (
	"cmp"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/minhash"
)

// pages is a Source over pages held in memory.
func pages(ps []*Page) Source {
	return func(fn func(*Page) error) error {
		for _, p := range ps {
			if err := fn(p); err != nil {
				return err
			}
		}
		return nil
	}
}

// page is a compared page by default: internal, crawled, indexable.
func page(url, text string) *Page {
	return &Page{URL: url, Internal: true, Crawled: true, Indexable: true,
		WordCount: len(strings.Fields(text)), Hash: "h:" + url, Text: text}
}

func words(prefix string, n int) []string {
	w := make([]string, n)
	for i := range w {
		w[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return w
}

func runPages(t *testing.T, ps []*Page, s Settings) *Result {
	t.Helper()
	res, err := Run(pages(ps), s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func byURL(res *Result) map[string]PageResult {
	m := map[string]PageResult{}
	for _, p := range res.Pages {
		m[p.URL] = p
	}
	return m
}

// bruteForce is the reference: every pair of compared pages measured exactly,
// with no candidate search at all.
func bruteForce(ps []*Page, s Settings) *Result {
	type cand struct {
		url, hash string
		set       []uint64
	}
	var cs []cand
	for _, p := range ps {
		if !s.Compares(p) {
			continue
		}
		if set := minhash.Shingles(p.Text); len(set) > 0 {
			cs = append(cs, cand{p.URL, p.Hash, set})
		}
	}
	slices.SortFunc(cs, func(a, b cand) int { return cmp.Compare(a.url, b.url) })
	type hit struct {
		other int
		bp    int64
	}
	hits := make([][]hit, len(cs))
	res := &Result{Settings: s}
	for i := range cs {
		for j := i + 1; j < len(cs); j++ {
			shared := int64(intersect(cs[i].set, cs[j].set))
			union := int64(len(cs[i].set)+len(cs[j].set)) - shared
			if shared*100 < int64(s.Threshold)*union {
				continue
			}
			bp := (shared*20000 + union) / (2 * union)
			hits[i] = append(hits[i], hit{j, bp})
			hits[j] = append(hits[j], hit{i, bp})
			res.Pairs++
		}
	}
	for i, c := range cs {
		hs := hits[i]
		slices.SortFunc(hs, func(a, b hit) int {
			if a.bp != b.bp {
				return cmp.Compare(b.bp, a.bp)
			}
			return cmp.Compare(a.other, b.other)
		})
		pr := PageResult{URL: c.url, Copies: []Copy{}, Count: len(hs)}
		for _, h := range hs {
			if len(pr.Copies) < MaxListed {
				pr.Copies = append(pr.Copies, Copy{cs[h.other].url, float64(h.bp) / 10000})
			}
			if cs[h.other].hash != c.hash {
				if pr.DistinctCount == 0 {
					pr.DistinctClosest = Copy{cs[h.other].url, float64(h.bp) / 10000}
				}
				pr.DistinctCount++
			}
		}
		res.Pages = append(res.Pages, pr)
	}
	return res
}

// corpus builds a site of template families: each family is a base text and
// variants of it with a growing share of words replaced, so its pairs spread
// across every similarity from identical to unrelated, many of them near the
// threshold. Some variants are exact text copies, some byte-identical twins
// (same raw hash), and a few pages fail a filter.
func corpus(seed int64, families, perFamily int) []*Page {
	rng := rand.New(rand.NewSource(seed))
	var ps []*Page
	vocab := words("v", 3000)
	for f := 0; f < families; f++ {
		base := make([]string, 120+rng.Intn(200))
		for i := range base {
			base[i] = vocab[rng.Intn(len(vocab))]
		}
		for v := 0; v < perFamily; v++ {
			w := slices.Clone(base)
			// Replace up to 1 word in 12, at random positions. Each replaced
			// word changes the five shingles over it, so the family's shares
			// run from 100% down past 50%.
			for k := rng.Intn(len(w)/12 + 1); k > 0; k-- {
				w[rng.Intn(len(w))] = vocab[rng.Intn(len(vocab))]
			}
			url := fmt.Sprintf("https://ex.test/f%02d/v%02d", f, v)
			p := page(url, strings.Join(w, " "))
			switch rng.Intn(12) {
			case 0: // an exact text copy of the base, other HTML
				p.Text = strings.Join(base, " ")
				p.WordCount = len(base)
			case 1: // a byte-identical twin of the family's first page
				p.Text = strings.Join(base, " ")
				p.WordCount = len(base)
				p.Hash = fmt.Sprintf("twin:%d", f)
			case 2:
				p.Indexable = false
			}
			ps = append(ps, p)
		}
	}
	return ps
}

// TestRunMatchesBruteForce is the step's correctness gate: over a corpus whose
// pairs spread across every similarity, the banded search finds exactly the
// pairs an all-pairs exact run finds, with the same shares, lists, counts and
// order, at several thresholds.
func TestRunMatchesBruteForce(t *testing.T) {
	ps := corpus(1, 14, 22)
	for _, th := range []int{50, 70, 80, 90, 95, 100} {
		s := Settings{Threshold: th, IndexableOnly: true}
		got, want := runPages(t, ps, s), bruteForce(ps, s)
		if want.Pairs == 0 {
			t.Fatalf("%d%%: the reference found no pairs — the test is vacuous", th)
		}
		if !reflect.DeepEqual(got, want) {
			diffResults(t, th, got, want)
		}
	}
}

func diffResults(t *testing.T, th int, got, want *Result) {
	t.Helper()
	t.Errorf("%d%%: pairs %d, want %d", th, got.Pairs, want.Pairs)
	g, w := byURL(got), byURL(want)
	shown := 0
	for url, wp := range w {
		if gp := g[url]; !reflect.DeepEqual(gp, wp) && shown < 5 {
			t.Errorf("%d%% %s:\n got  %+v\n want %+v", th, url, gp, wp)
			shown++
		}
	}
	if len(g) != len(w) {
		t.Errorf("%d%%: %d pages compared, want %d", th, len(g), len(w))
	}
}

// TestRunIsDeterministic: the same pages in any order give the same result,
// order included.
func TestRunIsDeterministic(t *testing.T) {
	ps := corpus(2, 6, 15)
	s := Settings{Threshold: 80, IndexableOnly: true}
	first := runPages(t, ps, s)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 3; i++ {
		shuffled := slices.Clone(ps)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if again := runPages(t, shuffled, s); !reflect.DeepEqual(first, again) {
			t.Fatal("a reordered source gave a different result")
		}
	}
}

// TestExactShareAndBothDirections pins one pair's measured share against a
// hand count: /a and /b share 196 of 200 words in order, one word swapped, so
// of their 196 shingles each, 191 are shared (the five windows over the
// swapped word differ) — 191 / (196+196-191) = 0.9502.
func TestExactShareAndBothDirections(t *testing.T) {
	base := words("alpha", 200)
	b := slices.Clone(base)
	b[100] = "swapped"
	res := runPages(t, []*Page{
		page("https://ex.test/a", strings.Join(base, " ")),
		page("https://ex.test/b", strings.Join(b, " ")),
		page("https://ex.test/c", strings.Join(words("zeta", 200), " ")),
	}, Settings{Threshold: 90})
	m := byURL(res)
	want := 191.0 / 201.0
	wantShare := float64(int64(want*10000+0.5)) / 10000
	if got := m["https://ex.test/a"].Copies; len(got) != 1 || got[0] != (Copy{"https://ex.test/b", wantShare}) {
		t.Errorf("/a copies = %+v, want /b at %v", got, wantShare)
	}
	if got := m["https://ex.test/b"].Copies; len(got) != 1 || got[0] != (Copy{"https://ex.test/a", wantShare}) {
		t.Errorf("/b copies = %+v, want /a at %v", got, wantShare)
	}
	if c := m["https://ex.test/c"]; c.Copies == nil || len(c.Copies) != 0 || c.Count != 0 {
		t.Errorf("/c was compared and has none: want an empty, non-nil list, got %+v", c)
	}
	if res.Pairs != 1 {
		t.Errorf("pairs = %d, want 1", res.Pairs)
	}
}

// TestThresholdIsInclusiveAndExact: a pair at exactly the threshold is listed,
// and is not at one point above. Each text is 84 shared words then 10 of its
// own: the 80 windows inside the shared words are common, the 10 that reach
// into the tail are each text's own, so the share is 80 / (90+90-80) = 80%.
func TestThresholdIsInclusiveAndExact(t *testing.T) {
	shared := words("s", 84)
	a := strings.Join(append(slices.Clone(shared), words("a", 10)...), " ")
	b := strings.Join(append(slices.Clone(shared), words("b", 10)...), " ")
	if sa, sb := minhash.Shingles(a), minhash.Shingles(b); intersect(sa, sb) != 80 || len(sa) != 90 || len(sb) != 90 {
		t.Fatalf("fixture: %d shared of %d and %d", intersect(sa, sb), len(sa), len(sb))
	}
	ps := []*Page{page("https://ex.test/a", a), page("https://ex.test/b", b)}
	at := runPages(t, ps, Settings{Threshold: 80})
	if at.Pairs != 1 || byURL(at)["https://ex.test/a"].Copies[0].Share != 0.8 {
		t.Errorf("a pair at exactly 80%% is not listed at 0.8 at threshold 80: %+v", at.Pages)
	}
	if above := runPages(t, ps, Settings{Threshold: 81}); above.Pairs != 0 {
		t.Error("a pair at 80% is listed at threshold 81")
	}
}

// TestByteIdenticalTwinsListedAt100: byte-identical pages are near copies at
// 100% — listed and counted — but not Distinct, which the issue reads; a
// same-text page with other HTML is Distinct at 100%.
func TestByteIdenticalTwinsListedAt100(t *testing.T) {
	text := strings.Join(words("w", 150), " ")
	twinA, twinB := page("https://ex.test/t1", text), page("https://ex.test/t2", text)
	twinA.Hash, twinB.Hash = "same", "same"
	other := page("https://ex.test/x", text) // same text, other raw body
	m := byURL(runPages(t, []*Page{twinA, twinB, other}, Settings{Threshold: 90}))
	t1 := m["https://ex.test/t1"]
	if t1.Count != 2 || len(t1.Copies) != 2 || t1.Copies[0] != (Copy{"https://ex.test/t2", 1}) ||
		t1.Copies[1] != (Copy{"https://ex.test/x", 1}) {
		t.Errorf("t1 = %+v, want t2 and x at 100%%, by URL", t1)
	}
	if t1.DistinctCount != 1 || t1.DistinctClosest != (Copy{"https://ex.test/x", 1}) {
		t.Errorf("t1 distinct = %d %+v, want only x", t1.DistinctCount, t1.DistinctClosest)
	}
	if x := m["https://ex.test/x"]; x.DistinctCount != 2 {
		t.Errorf("x distinct = %d, want both twins", x.DistinctCount)
	}
}

// TestLargeIdenticalClusterIsCappedAndCounted: 130 pages of one text and two
// near copies of it. Every page counts all its copies, lists MaxListed, the
// 100% ones first by URL; the near copies, which sort after every 100% entry,
// fall off the cluster pages' lists but are counted.
func TestLargeIdenticalClusterIsCappedAndCounted(t *testing.T) {
	base := words("w", 300)
	var ps []*Page
	for i := 0; i < 130; i++ {
		ps = append(ps, page(fmt.Sprintf("https://ex.test/shell/%03d", i), strings.Join(base, " ")))
	}
	near := slices.Clone(base)
	near[150] = "changed"
	ps = append(ps, page("https://ex.test/near1", strings.Join(near, " ")))
	near2 := slices.Clone(base)
	near2[10] = "other"
	ps = append(ps, page("https://ex.test/near2", strings.Join(near2, " ")))
	s := Settings{Threshold: 90}
	got, want := runPages(t, ps, s), bruteForce(ps, s)
	if !reflect.DeepEqual(got, want) {
		diffResults(t, 90, got, want)
	}
	m := byURL(got)
	shell := m["https://ex.test/shell/000"]
	if shell.Count != 131 || len(shell.Copies) != MaxListed || shell.Copies[0].URL != "https://ex.test/shell/001" {
		t.Errorf("shell/000: count %d, %d listed, first %+v", shell.Count, len(shell.Copies), shell.Copies[0])
	}
	if n1 := m["https://ex.test/near1"]; n1.Count != 131 || len(n1.Copies) != MaxListed {
		t.Errorf("near1: count %d, %d listed", n1.Count, len(n1.Copies))
	}
}

// TestFilters: a page that fails a filter is not compared — absent from the
// result, so its list reads as not compared — and is in no other page's list.
func TestFilters(t *testing.T) {
	text := strings.Join(words("w", 120), " ")
	mk := func(url string, edit func(*Page)) *Page {
		p := page(url, text)
		edit(p)
		return p
	}
	ps := []*Page{
		mk("https://ex.test/ok1", func(*Page) {}),
		mk("https://ex.test/ok2", func(*Page) {}),
		mk("https://ex.test/external", func(p *Page) { p.Internal = false }),
		mk("https://ex.test/blocked", func(p *Page) { p.Crawled = false }),
		mk("https://ex.test/noindex", func(p *Page) { p.Indexable = false }),
		mk("https://ex.test/page2", func(p *Page) { p.Paginated = true }),
		mk("https://ex.test/short", func(p *Page) { p.WordCount = 40 }),
		mk("https://ex.test/nowords", func(p *Page) { p.WordCount = 0 }),
		mk("https://ex.test/notext", func(p *Page) { p.Text = "" }),
	}
	s := Settings{Threshold: 90, MinWords: 50, IndexableOnly: true, IgnorePaginated: true}
	m := byURL(runPages(t, ps, s))
	if len(m) != 2 {
		t.Errorf("compared %v, want only ok1 and ok2", slices.Sorted(mapsKeys(m)))
	}
	if ok := m["https://ex.test/ok1"]; ok.Count != 1 || ok.Copies[0].URL != "https://ex.test/ok2" {
		t.Errorf("ok1 = %+v, want only ok2", ok)
	}
	// The defaults — no minimum, indexable and paginated pages in — compare
	// every internal crawled page with text.
	m = byURL(runPages(t, ps, Settings{Threshold: 90}))
	for _, url := range []string{"ok1", "ok2", "noindex", "page2", "short"} {
		if _, ok := m["https://ex.test/"+url]; !ok {
			t.Errorf("/%s not compared under the default filters", url)
		}
	}
	for _, url := range []string{"external", "blocked", "nowords", "notext"} {
		if _, ok := m["https://ex.test/"+url]; ok {
			t.Errorf("/%s compared under the default filters", url)
		}
	}
}

func mapsKeys(m map[string]PageResult) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestStoredSignatureEqualsComputed: the stored crawl-time signature and one
// computed from the text give the same result — they are the same signature.
func TestStoredSignatureEqualsComputed(t *testing.T) {
	ps := corpus(3, 5, 12)
	s := Settings{Threshold: 80}
	computed := runPages(t, ps, s)
	for _, p := range ps {
		p.Signature = minhash.Of(p.Text).Encode()
	}
	if stored := runPages(t, ps, s); !reflect.DeepEqual(computed, stored) {
		t.Error("the stored-signature path differs from the computed one")
	}
}

func TestRunRejectsBadThreshold(t *testing.T) {
	for _, th := range []int{-1, 101} {
		if _, err := Run(pages(nil), Settings{Threshold: th}, t.TempDir()); err == nil {
			t.Errorf("threshold %d accepted", th)
		}
	}
}

func TestRunRejectsRepeatedPage(t *testing.T) {
	text := strings.Join(words("w", 50), " ")
	if _, err := Run(pages([]*Page{page("https://ex.test/a", text), page("https://ex.test/a", text)}),
		Settings{Threshold: 90}, t.TempDir()); err == nil {
		t.Error("a page read twice was accepted")
	}
}

// TestRunRemovesItsTempFile: the shingle file goes when Run returns.
func TestRunRemovesItsTempFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(pages(corpus(4, 2, 5)), Settings{Threshold: 80}, dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind %v", entries)
	}
}
