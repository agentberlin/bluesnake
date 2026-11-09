// Package nearcopy finds, for every compared page of a crawl, the other pages
// whose content-area text is almost the same, and measures the exact share of
// text each pair has in common. It is the near-duplicate step of the analysis
// phase, and it also runs on its own after a crawl whose analysis is off
// (finalize), so a crawl that keeps the rest of the analysis off still gets its
// near copies before the bundle is written.
//
// A page's text is its set of 5-word shingles (minhash.Shingles), and a pair's
// share is the exact Jaccard of their two sets: shingles in both ÷ distinct
// shingles of either. The 64-value minhash signature only picks which pairs to
// measure, so no pair's share is an estimate. Choosing those candidates is
// where a true pair can be lost, and banding picks the candidate rule so that
// a pair whose share is exactly the threshold is missed less than once in
// 5,000 (Banding).
//
// Memory is set by pages, not by text: the step keeps each compared page's URL,
// raw-body hash and signature (about 600 bytes a page) and writes every
// page's shingle set once to a temporary file, from which an exact check reads
// the two sets it compares. The sets are never all in memory at once — a site
// of 67,000 pages can have 22 million distinct 5-word phrases.
package nearcopy

import (
	"bufio"
	"cmp"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/agentberlin/bluesnake/internal/minhash"
)

// MaxListed is the most near copies a page's list carries. Count still counts
// every one.
const MaxListed = 50

// Settings are the inputs that decide which pages are compared and which pairs
// are near copies. They are recorded with the result, because they are the
// floor of every list: a page with no near copies at 80% may have plenty at 60%.
type Settings struct {
	// Threshold is the lowest share, in percent, at which a pair is a near
	// copy: share >= Threshold/100.
	Threshold int `json:"threshold"`
	// MinWords: a page with fewer content words is not compared. A page with
	// no words is never compared, whatever this says.
	MinWords int `json:"min_words"`
	// IndexableOnly compares indexable pages only: a noindex page, or one
	// whose canonical names another address, is not a copy a search engine
	// sees.
	IndexableOnly bool `json:"indexable_only"`
	// IgnorePaginated leaves out page 2+ of a paginated series (a page that
	// declares rel="prev").
	IgnorePaginated bool `json:"ignore_paginated"`
}

// Page is one stored page as the step reads it.
type Page struct {
	URL       string
	Internal  bool // scope internal
	Crawled   bool // state crawled
	Indexable bool
	Paginated bool // declares rel="prev" (parse.Facts.IsPaginated)
	WordCount int  // content-area words (parse.Facts.WordCount)
	// Hash is the raw response body's hash (parse.Facts.Hash). It separates a
	// page's byte-identical twins, which are near copies at 100% and also the
	// exact-duplicate check's, from its other near copies (Distinct).
	Hash string
	// Signature is the stored minhash signature (minhash.Signature.Encode),
	// computed at crawl time; nil, or not minhash.EncodedLen bytes, means
	// none was stored and it is computed from Text.
	Signature []byte
	Text      string // content-area text (parse.Facts.ContentText)
}

// Compares reports whether the page passes the filters. A page that does not
// is not compared: it has no list, and it is in no other page's list.
func (s Settings) Compares(p *Page) bool {
	switch {
	case !p.Internal || !p.Crawled:
		return false
	case s.IndexableOnly && !p.Indexable:
		return false
	case s.IgnorePaginated && p.Paginated:
		return false
	case p.WordCount <= 0 || p.WordCount < s.MinWords:
		return false
	}
	return true
}

// Copy is one near copy of a page: the other page and the share of text the
// two have in common, from 0 to 1, rounded to 4 places.
type Copy struct {
	URL   string  `json:"url"`
	Share float64 `json:"share"`
}

// PageResult is one compared page's near copies.
type PageResult struct {
	URL string
	// Copies are the page's near copies, highest share first, then by URL, at
	// most MaxListed of them; empty (never nil) when it has none.
	Copies []Copy
	// Count is how many pages are near copies of this one — all of them, not
	// only the ones listed.
	Count int
	// DistinctCount and DistinctClosest leave out the page's byte-identical
	// twins (same raw-body Hash), which the exact-duplicate check already
	// reports: Screaming Frog's Near Duplicates excludes exact duplicates, and
	// the content_near_duplicate issue follows it. DistinctClosest is the
	// highest-share such copy (ties by URL); its URL is "" when there is none.
	DistinctCount   int
	DistinctClosest Copy
}

// Result is the step's output for one crawl.
type Result struct {
	Settings Settings
	// Pages is every compared page, sorted by URL; a page that was not
	// compared is absent.
	Pages []PageResult
	// Pairs is the number of near-copy pairs, each counted once.
	Pairs int
}

// Source hands each stored page to fn, one at a time, in any order. A page
// may be dropped once fn returns: the step keeps what it needs.
type Source func(fn func(*Page) error) error

// Run finds the near copies of every page src yields that s compares. tmpDir
// holds the temporary shingle file ("" is the system's temporary directory),
// which is removed before Run returns.
func Run(src Source, s Settings, tmpDir string) (*Result, error) {
	return runWith(src, s, tmpDir, Banding(s.Threshold), nil)
}

// runWith is Run with the candidate rule given, and onPair, when not nil,
// called with every near-copy pair of page URLs and its share in basis points
// — what the tests compare candidate rules by, since a Result keeps only each
// page's first MaxListed.
func runWith(src Source, s Settings, tmpDir string, b Bands, onPair func(a, b string, bp int32)) (*Result, error) {
	if s.Threshold < 0 || s.Threshold > 100 {
		return nil, fmt.Errorf("near copies: threshold must be between 0 and 100, got %d", s.Threshold)
	}
	f, err := os.CreateTemp(tmpDir, "bluesnake-nearcopy-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("near copies: %w", err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()
	r := &run{s: s, bands: b, onPair: onPair, sets: &spill{f: f, w: bufio.NewWriterSize(f, 1<<20)}}
	if err := r.load(src); err != nil {
		return nil, err
	}
	if err := r.group(); err != nil {
		return nil, err
	}
	if err := r.candidates(); err != nil {
		return nil, err
	}
	return r.result(), nil
}

// entry is what the step keeps of one compared page.
type entry struct {
	url    string
	hash   string
	sig    minhash.Signature
	off    int64  // the shingle set's byte offset in the spill file
	n      int32  // distinct shingles
	digest uint64 // of the shingle set; equal sets have equal digests
	grp    int32  // index into run.groups
}

// group is a set of compared pages with identical shingle sets: every pair in
// it is a near copy at 100%, and a pair across two groups has the share of
// their two sets. The candidate search runs over one page per group, which
// keeps a site of thousands of identical pages (a client-routed app serving
// one shell at every URL) from costing a comparison per pair.
type group struct {
	members []int32 // page indices, ascending — URL order
	// byHash counts members by raw-body hash, for groups whose members do not
	// all share one; nil when they do.
	byHash map[string]int32
	other  int32 // first member whose hash differs from members[0]'s; -1 when none
}

// acc accumulates one page's near copies as pairs are found.
type acc struct {
	top    []ref // at most MaxListed, unordered until result
	count  int
	dCount int
	dBest  ref
	hasD   bool // dBest is set
}

// ref is a near copy by page index, with its share in basis points (1/10000).
type ref struct {
	other int32
	bp    int32
}

// better is the list order: higher share first, then lower index — the URL,
// since pages are indexed in URL order.
func better(a, b ref) bool {
	return a.bp > b.bp || (a.bp == b.bp && a.other < b.other)
}

type run struct {
	s      Settings
	bands  Bands
	onPair func(a, b string, bp int32)
	sets   *spill
	pages  []entry
	groups []group
	accs   []acc
	pairs  int

	// The two most recently read sets, so a run of checks against one page
	// reads it once.
	bufA, bufB []uint64
	lastA      int32
}

// load reads every compared page: its shingle set goes to the spill file, the
// rest into pages, which is then put in URL order.
func (r *run) load(src Source) error {
	err := src(func(p *Page) error {
		if !r.s.Compares(p) {
			return nil
		}
		set := minhash.Shingles(p.Text)
		if len(set) == 0 {
			return nil // no text to compare: not compared
		}
		e := entry{url: p.URL, hash: p.Hash, n: int32(len(set)), digest: digest(set)}
		if len(p.Signature) == minhash.EncodedLen {
			e.sig = minhash.Decode(p.Signature)
		} else {
			e.sig = minhash.FromShingles(set)
		}
		off, err := r.sets.put(set)
		if err != nil {
			return err
		}
		e.off = off
		r.pages = append(r.pages, e)
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.sets.w.Flush(); err != nil {
		return fmt.Errorf("near copies: %w", err)
	}
	slices.SortFunc(r.pages, func(a, b entry) int { return cmp.Compare(a.url, b.url) })
	for i := 1; i < len(r.pages); i++ {
		if r.pages[i].url == r.pages[i-1].url {
			return fmt.Errorf("near copies: page %s read twice", r.pages[i].url)
		}
	}
	r.accs = make([]acc, len(r.pages))
	r.lastA = -1
	return nil
}

// group sorts the pages into groups of identical shingle sets. Pages are first
// bucketed by (digest, size); a page joins a group only once its set is read
// and found equal to the group's first member's, so a digest collision splits
// rather than merges.
func (r *run) group() error {
	order := make([]int32, len(r.pages))
	for i := range order {
		order[i] = int32(i)
	}
	slices.SortFunc(order, func(a, b int32) int {
		pa, pb := &r.pages[a], &r.pages[b]
		if c := cmp.Compare(pa.digest, pb.digest); c != 0 {
			return c
		}
		if c := cmp.Compare(pa.n, pb.n); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	for start := 0; start < len(order); {
		end := start + 1
		for end < len(order) && r.pages[order[end]].digest == r.pages[order[start]].digest &&
			r.pages[order[end]].n == r.pages[order[start]].n {
			end++
		}
		if err := r.groupRun(order[start:end]); err != nil {
			return err
		}
		start = end
	}
	slices.SortFunc(r.groups, func(a, b group) int { return cmp.Compare(a.members[0], b.members[0]) })
	for g := range r.groups {
		grp := &r.groups[g]
		for _, m := range grp.members {
			r.pages[m].grp = int32(g)
		}
		grp.other = -1
		h0 := r.pages[grp.members[0]].hash
		for _, m := range grp.members[1:] {
			if r.pages[m].hash != h0 {
				grp.other = m
				break
			}
		}
		if grp.other >= 0 {
			grp.byHash = map[string]int32{}
			for _, m := range grp.members {
				grp.byHash[r.pages[m].hash]++
			}
		}
		r.within(grp)
	}
	return nil
}

// groupRun splits one (digest, size) bucket — almost always one set, or one
// page — into groups of equal sets.
func (r *run) groupRun(idx []int32) error {
	if len(idx) == 1 {
		r.groups = append(r.groups, group{members: []int32{idx[0]}})
		return nil
	}
	type open struct {
		set  []uint64
		grp  group
		done bool
	}
	var opens []open
	for _, i := range idx {
		set, err := r.sets.read(r.pages[i].off, r.pages[i].n, nil)
		if err != nil {
			return err
		}
		placed := false
		for k := range opens {
			if slices.Equal(opens[k].set, set) {
				opens[k].grp.members = append(opens[k].grp.members, i)
				placed = true
				break
			}
		}
		if !placed {
			opens = append(opens, open{set: set, grp: group{members: []int32{i}}})
		}
	}
	for _, o := range opens {
		r.groups = append(r.groups, o.grp) // members ascend: idx did
	}
	return nil
}

// within records the pairs inside one group, all at 100%.
func (r *run) within(g *group) {
	m := len(g.members)
	if m < 2 {
		return
	}
	r.pairs += m * (m - 1) / 2
	if r.onPair != nil {
		for x, p := range g.members {
			for _, q := range g.members[x+1:] {
				r.onPair(r.pages[p].url, r.pages[q].url, 10000)
			}
		}
	}
	for _, p := range g.members {
		a := &r.accs[p]
		a.count += m - 1
		h := r.pages[p].hash
		a.dCount += m - g.sameHash(r, h)
		if d := g.firstDistinct(r, h); d >= 0 {
			a.offerDistinct(ref{d, 10000})
		}
		listed := 0
		for _, q := range g.members {
			if q == p {
				continue
			}
			if listed == MaxListed {
				break
			}
			a.offer(ref{q, 10000})
			listed++
		}
	}
}

// sameHash is how many of the group's members have raw-body hash h.
func (g *group) sameHash(r *run, h string) int {
	if g.byHash != nil {
		return int(g.byHash[h])
	}
	if r.pages[g.members[0]].hash == h {
		return len(g.members)
	}
	return 0
}

// firstDistinct is the group's first member whose raw-body hash is not h, or
// -1 when every member's is.
func (g *group) firstDistinct(r *run, h string) int32 {
	if r.pages[g.members[0]].hash != h {
		return g.members[0]
	}
	return g.other
}

// between records every pair across two groups whose sets share bp.
func (r *run) between(g1, g2 *group, bp int32) {
	r.pairs += len(g1.members) * len(g2.members)
	if r.onPair != nil {
		for _, p := range g1.members {
			for _, q := range g2.members {
				r.onPair(r.pages[p].url, r.pages[q].url, bp)
			}
		}
	}
	r.expand(g1, g2, bp)
	r.expand(g2, g1, bp)
}

// expand gives each member of from every member of to as a near copy at bp.
// Only the first MaxListed of to can make a list — the rest share their share
// and sort after them by URL — so no more are offered.
func (r *run) expand(from, to *group, bp int32) {
	listed := to.members[:min(len(to.members), MaxListed)]
	for _, p := range from.members {
		a := &r.accs[p]
		a.count += len(to.members)
		h := r.pages[p].hash
		a.dCount += len(to.members) - to.sameHash(r, h)
		if d := to.firstDistinct(r, h); d >= 0 {
			a.offerDistinct(ref{d, bp})
		}
		for _, q := range listed {
			a.offer(ref{q, bp})
		}
	}
}

// offer adds a near copy to the page's list, displacing its worst entry when
// the list is full and the new one sorts before it.
func (a *acc) offer(c ref) {
	if len(a.top) < MaxListed {
		a.top = append(a.top, c)
		return
	}
	worst := 0
	for i := 1; i < len(a.top); i++ {
		if better(a.top[worst], a.top[i]) {
			worst = i
		}
	}
	if better(c, a.top[worst]) {
		a.top[worst] = c
	}
}

func (a *acc) offerDistinct(c ref) {
	if !a.hasD || better(c, a.dBest) {
		a.dBest, a.hasD = c, true
	}
}

// candidates runs the banded search over one page per group and checks every
// candidate pair exactly.
func (r *run) candidates() error {
	reps := make([]int32, len(r.groups))
	for g := range r.groups {
		reps[g] = r.groups[g].members[0]
	}
	if len(reps) < 2 {
		return nil
	}
	b := r.bands
	type key struct {
		h uint64
		g int32
	}
	keys := make([]key, len(reps))
	for band := 0; band < b.Bands; band++ {
		start := band * b.Rows
		for g, p := range reps {
			keys[g] = key{bandHash(&r.pages[p].sig, start, b.Rows), int32(g)}
		}
		slices.SortFunc(keys, func(x, y key) int {
			if c := cmp.Compare(x.h, y.h); c != 0 {
				return c
			}
			return cmp.Compare(x.g, y.g)
		})
		for lo := 0; lo < len(keys); {
			hi := lo + 1
			for hi < len(keys) && keys[hi].h == keys[lo].h {
				hi++
			}
			for x := lo; x < hi; x++ {
				for y := x + 1; y < hi; y++ {
					gi, gj := keys[x].g, keys[y].g
					si, sj := &r.pages[reps[gi]].sig, &r.pages[reps[gj]].sig
					// A pair is checked in the first band its signatures share,
					// and only there: a hash collision is not a shared band, and
					// a pair sharing an earlier band was checked in it.
					if !rowsEqual(si, sj, start, start+b.Rows) || sharesEarlierBand(si, sj, band, b.Rows) {
						continue
					}
					if err := r.check(gi, gj, b.MinMatch); err != nil {
						return err
					}
				}
			}
			lo = hi
		}
	}
	return nil
}

// check measures one candidate pair of groups exactly and records it when its
// share reaches the threshold.
func (r *run) check(gi, gj int32, minMatch int) error {
	ia, ib := r.groups[gi].members[0], r.groups[gj].members[0]
	a, b := &r.pages[ia], &r.pages[ib]
	if matches(&a.sig, &b.sig) < minMatch {
		return nil
	}
	// The share can be no more than the smaller set over the larger.
	t := int64(r.s.Threshold)
	lo, hi := int64(min(a.n, b.n)), int64(max(a.n, b.n))
	if lo*100 < t*hi {
		return nil
	}
	var err error
	if r.lastA != ia {
		if r.bufA, err = r.sets.read(a.off, a.n, r.bufA); err != nil {
			return err
		}
		r.lastA = ia
	}
	if r.bufB, err = r.sets.read(b.off, b.n, r.bufB); err != nil {
		return err
	}
	shared := int64(intersect(r.bufA, r.bufB))
	union := int64(a.n) + int64(b.n) - shared
	if shared*100 < t*union {
		return nil
	}
	r.between(&r.groups[gi], &r.groups[gj], int32((shared*20000+union)/(2*union)))
	return nil
}

// result builds the per-page lists in URL order.
func (r *run) result() *Result {
	res := &Result{Settings: r.s, Pages: make([]PageResult, len(r.pages)), Pairs: r.pairs}
	for i := range r.pages {
		a := &r.accs[i]
		slices.SortFunc(a.top, func(x, y ref) int {
			if better(x, y) {
				return -1
			}
			if better(y, x) {
				return 1
			}
			return 0
		})
		pr := PageResult{URL: r.pages[i].url, Copies: make([]Copy, len(a.top)), Count: a.count, DistinctCount: a.dCount}
		for k, c := range a.top {
			pr.Copies[k] = Copy{URL: r.pages[c.other].url, Share: share(c.bp)}
		}
		if a.dCount > 0 {
			pr.DistinctClosest = Copy{URL: r.pages[a.dBest.other].url, Share: share(a.dBest.bp)}
		}
		res.Pages[i] = pr
		a.top = nil
	}
	return res
}

// share converts basis points to the 0–1 share: the double nearest the
// 4-place decimal, which encodes as exactly those places.
func share(bp int32) float64 {
	return float64(bp) / 10000
}

// --- the shingle-set file -----------------------------------------------------

// spill is the temporary file every compared page's shingle set is written to
// once, as little-endian uint64s, and read back from per exact check.
type spill struct {
	f   *os.File
	w   *bufio.Writer
	off int64
	buf []byte
}

func (s *spill) put(set []uint64) (int64, error) {
	off := s.off
	var b [8]byte
	for _, v := range set {
		binary.LittleEndian.PutUint64(b[:], v)
		if _, err := s.w.Write(b[:]); err != nil {
			return 0, fmt.Errorf("near copies: %w", err)
		}
	}
	s.off += int64(len(set)) * 8
	return off, nil
}

// read returns the n-shingle set at off, reusing dst's storage.
func (s *spill) read(off int64, n int32, dst []uint64) ([]uint64, error) {
	size := int(n) * 8
	if cap(s.buf) < size {
		s.buf = make([]byte, size)
	}
	b := s.buf[:size]
	if _, err := s.f.ReadAt(b, off); err != nil && err != io.EOF {
		return nil, fmt.Errorf("near copies: reading shingles: %w", err)
	}
	dst = slices.Grow(dst[:0], int(n))[:n]
	for i := range dst {
		dst[i] = binary.LittleEndian.Uint64(b[i*8:])
	}
	return dst, nil
}

// --- set and signature arithmetic ----------------------------------------------

// intersect counts the values two ascending, repeat-free sets share.
func intersect(a, b []uint64) int {
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			n++
			i++
			j++
		}
	}
	return n
}

// digest hashes a shingle set, to bucket equal sets together.
func digest(set []uint64) uint64 {
	h := uint64(14695981039346656037)
	for _, v := range set {
		h ^= v
		h *= 1099511628211
		h ^= h >> 31
	}
	return h
}

// bandHash hashes rows [start, start+rows) of a signature (FNV-1a over their
// bytes).
func bandHash(sig *minhash.Signature, start, rows int) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	var buf [8]byte
	for i := start; i < start+rows; i++ {
		binary.LittleEndian.PutUint64(buf[:], sig[i])
		for _, b := range buf {
			h ^= uint64(b)
			h *= prime
		}
	}
	return h
}

func rowsEqual(a, b *minhash.Signature, from, to int) bool {
	for i := from; i < to; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sharesEarlierBand reports whether the two signatures are equal across any
// band before band.
func sharesEarlierBand(a, b *minhash.Signature, band, rows int) bool {
	for k := 0; k < band; k++ {
		if rowsEqual(a, b, k*rows, (k+1)*rows) {
			return true
		}
	}
	return false
}

// matches counts the signature positions two signatures agree on: 64 times
// the estimated share.
func matches(a, b *minhash.Signature) int {
	n := 0
	for i := range a {
		if a[i] == b[i] {
			n++
		}
	}
	return n
}
