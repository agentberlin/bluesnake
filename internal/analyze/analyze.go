// Package analyze implements the post-crawl analysis phase (DESIGN.md §5.6):
// whole-graph computations that can't run while streaming — link score
// (PageRank), redirect/canonical chains and loops, near-duplicate content
// (minhash), hreflang reciprocity, pagination sequences, and sitemap set
// operations. All analyses are pure over the loaded page set and re-runnable.
package analyze

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/indexability"
	"github.com/agentberlin/bluesnake/internal/isocodes"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/minhash"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// Chain is one redirect or canonical chain.
type Chain struct {
	Type        string   `json:"type"` // redirect | canonical
	Source      string   `json:"source"`
	Hops        []string `json:"hops"`
	Final       string   `json:"final"`
	FinalStatus int      `json:"final_status"`
	Loop        bool     `json:"loop"`
}

// NearDup is the near-duplicate result for one URL.
type NearDup struct {
	ClosestMatch      string  `json:"closest_match"`
	ClosestSimilarity float64 `json:"closest_similarity"` // percent
	Count             int     `json:"count"`
}

// Results carries everything the analysis pass computed.
type Results struct {
	LinkScores  map[string]float64
	UniqueIn    map[string]int
	UniqueOut   map[string]int
	Chains      []Chain
	NearDups    map[string]NearDup
	Occurrences []issues.Occurrence
}

// SitemapIndex maps page URL -> sitemaps listing it (empty when sitemap
// crawling was off).
type SitemapIndex map[string][]string

// LlmsTxtFile is one fetched /llms.txt (or /llms-full.txt) record with its
// structural-validation outcome.
type LlmsTxtFile struct {
	URL       string
	Kind      string // llms_txt | llms_full_txt
	Status    int
	Found     bool
	Title     string
	Summary   string
	Malformed bool
}

// LlmsTxtLink is one curated link listed in an llms.txt file, to be resolved
// against the crawl graph.
type LlmsTxtLink struct {
	Src     string // the llms.txt URL that listed it
	URL     string // normalized target URL
	Section string
	Anchor  string
}

// LlmsTxtData is the stored llms.txt audit input (files + curated links).
type LlmsTxtData struct {
	Files []LlmsTxtFile
	Links []LlmsTxtLink
}

// SiteCheck is one stored site-level check report (DESIGN.md §5.10): the
// crawl's site-check pass persisted it; this phase re-derives its findings.
type SiteCheck struct {
	Kind    string
	Subject string
	Report  []byte
}

// Run executes every enabled analysis over the crawl's pages.
// Option configures a Run.
type Option func(*analyzer)

// LinkSource streams the stored hyperlinks for the link graph: every links row
// of type hyperlink — raw src and dst, nofollow flag — one at a time, in any
// order, as often as it is asked. store.Hyperlinks serves it from the links
// table; HyperlinkRows serves it from memory.
type LinkSource interface {
	EachHyperlink(fn func(src, dst string, nofollow bool))
}

// WithLinkSource supplies the stored hyperlinks so the link graph (unique in/out
// + PageRank) and the hyperlinked set are built from the links table rather than
// each page's Facts.Links — which the lite map finalize analyses over does not
// carry. The graph is kept as int32 edges between internal pages (8 bytes a
// hyperlink, deduplicated), never as the rows themselves. Without a source the
// analyzer reads Facts.Links, for callers holding a whole-map load.
func WithLinkSource(s LinkSource) Option {
	return func(a *analyzer) { a.links = s }
}

// Hyperlink is one stored hyperlink row for HyperlinkRows.
type Hyperlink struct {
	Src, Dst string
	Nofollow bool
}

// HyperlinkRows is a LinkSource over hyperlinks held in memory — the harness
// for callers that have the graph in hand (tests).
type HyperlinkRows []Hyperlink

// EachHyperlink implements LinkSource.
func (r HyperlinkRows) EachHyperlink(fn func(src, dst string, nofollow bool)) {
	for _, h := range r {
		fn(h.Src, h.Dst, h.Nofollow)
	}
}

func Run(pages map[string]*crawler.PageRecord, sitemaps SitemapIndex, llmstxt *LlmsTxtData, siteChecks []SiteCheck, cfg *config.Config, opts ...Option) *Results {
	r := &Results{
		LinkScores: map[string]float64{},
		UniqueIn:   map[string]int{},
		UniqueOut:  map[string]int{},
		NearDups:   map[string]NearDup{},
	}
	a := &analyzer{pages: pages, cfg: cfg, res: r}
	for _, o := range opts {
		o(a)
	}
	if cfg.Analysis.Links || cfg.Analysis.LinkScore {
		a.linkGraph()
	}
	if cfg.Analysis.RedirectChains {
		a.chains()
	}
	if cfg.Analysis.NearDuplicates && cfg.Content.NearDuplicates.Enabled {
		a.nearDuplicates()
	}
	if cfg.Analysis.Hreflang {
		a.hreflang()
	}
	if cfg.Analysis.Pagination {
		a.pagination()
	}
	if cfg.Analysis.Sitemaps && len(sitemaps) > 0 {
		a.sitemaps(sitemaps)
	}
	if cfg.Analysis.LlmsTxt && llmstxt != nil && len(llmstxt.Files) > 0 {
		a.llmsTxt(llmstxt)
	}
	// Site-check findings need no analysis.* gate: reports exist iff the
	// crawl's pass ran (site_checks config gated it there). Re-derivation
	// keeps re-analysis idempotent without refetching anything.
	a.siteChecks(siteChecks)
	return r
}

// siteChecks maps stored site-level check reports to issue occurrences via
// the single derivation in internal/sitecheck — the same findings the
// standalone tools show.
func (a *analyzer) siteChecks(checks []SiteCheck) {
	for _, sc := range checks {
		for _, f := range sitecheck.DecodeFindings(sc.Kind, sc.Report) {
			a.add(f.URL, f.IssueID, f.Detail)
		}
	}
}

type analyzer struct {
	pages       map[string]*crawler.PageRecord
	links       LinkSource // stored hyperlinks; nil ⇒ use Facts.Links
	cfg         *config.Config
	res         *Results
	hyperlinked map[string]bool // memoized by hyperlinkedSet
}

func (a *analyzer) add(url, id, detail string) {
	a.res.Occurrences = append(a.res.Occurrences, issues.Occurrence{URL: url, IssueID: id, Detail: detail})
}

// hyperlinkedSet returns every page that is the target of at least one
// hyperlink from another page (self-links excluded). The "unlinked" checks
// (canonical-/hreflang-/pagination-only discoverability) test crawled pages
// against it, so only page URLs are kept: a hyperlink to a URL the crawl never
// recorded has no reader, and keeping it would size the set by distinct link
// targets rather than pages. linkGraph fills it as a by-product of its pass
// when it runs first.
func (a *analyzer) hyperlinkedSet() map[string]bool {
	if a.hyperlinked != nil {
		return a.hyperlinked
	}
	a.hyperlinked = map[string]bool{}
	if a.links != nil {
		a.links.EachHyperlink(func(src, dst string, _ bool) { a.noteHyperlinked(src, dst) })
		return a.hyperlinked
	}
	for src, rec := range a.pages {
		if rec.Facts == nil {
			continue
		}
		for _, l := range rec.Facts.Links {
			if l.Type == parse.Hyperlink {
				a.noteHyperlinked(src, l.URL)
			}
		}
	}
	return a.hyperlinked
}

func (a *analyzer) noteHyperlinked(src, dst string) {
	if dst == "" || dst == src {
		return
	}
	if _, ok := a.pages[dst]; ok {
		a.hyperlinked[dst] = true
	}
}

// linkGraph computes unique in/outlink counts and PageRank-style link scores
// over followed internal hyperlink edges between crawled pages.
//
// The graph is an int32 edge list: every internal page gets an id in sorted URL
// order, and each followed hyperlink from an internal page with facts to an
// internal page becomes one packed (src, dst) pair, sorted and deduplicated.
// That is 8 bytes a hyperlink and nothing per row otherwise, whether the rows
// arrive from the links table (WithLinkSource) or from Facts.Links, so the
// graph's memory is set by the hyperlink count alone — the string-keyed edge
// sets it replaces held a Go string per endpoint of every edge, and the loaded
// links table behind them ~250 bytes a row.
//
// Self-links count towards unique in/outlinks (Screaming Frog parity); PageRank
// skips them so a page cannot vote for itself.
func (a *analyzer) linkGraph() {
	internal := make([]string, 0, len(a.pages))
	for url, rec := range a.pages {
		if rec.Scope == "internal" {
			internal = append(internal, url)
		}
	}
	sort.Strings(internal)
	id := make(map[string]int32, len(internal))
	for i, u := range internal {
		id[u] = int32(i)
	}
	// The edge gate, the same for both sources: hyperlink, followed, from an
	// internal page with facts, to an internal page.
	var pairs []uint64
	edge := func(src, dst string, nofollow bool) {
		if nofollow {
			return
		}
		s, ok := id[src]
		if !ok || a.pages[src].Facts == nil {
			return
		}
		d, ok := id[dst]
		if !ok {
			return
		}
		pairs = append(pairs, uint64(s)<<32|uint64(d))
	}
	if a.links != nil {
		// One pass serves the hyperlinked set too, when nothing has built it yet.
		if a.hyperlinked == nil {
			a.hyperlinked = map[string]bool{}
			a.links.EachHyperlink(func(src, dst string, nofollow bool) {
				a.noteHyperlinked(src, dst)
				edge(src, dst, nofollow)
			})
		} else {
			a.links.EachHyperlink(edge)
		}
	} else {
		for url, rec := range a.pages {
			if rec.Facts == nil || rec.Scope != "internal" {
				continue
			}
			for _, l := range rec.Facts.Links {
				if l.Type == parse.Hyperlink {
					edge(url, l.URL, l.Nofollow)
				}
			}
		}
	}
	slices.Sort(pairs)
	pairs = slices.Compact(pairs)
	for _, p := range pairs {
		a.res.UniqueOut[internal[p>>32]]++
		a.res.UniqueIn[internal[uint32(p)]]++
	}
	if !a.cfg.Analysis.LinkScore {
		return
	}
	a.pageRank(internal, pairs)
}

// pageRank scores the internal crawled pages (the node set) over the sorted,
// deduplicated edge list, d = 0.85, 40 iterations, scaled to v/max·100.
//
// Canonical accumulation order (FIN-CSRID): float addition is non-associative,
// so the contributions into each node must land in a fixed order for the stored
// link_score to be bit-stable run-to-run. The edge list is sorted by (src, dst)
// with ids in sorted URL order, so every iteration walks sources ascending and
// each source's destinations ascending — the order the earlier sorted-map
// implementation used, which TestLinkScore_DeterministicAcrossRuns pins.
//
// A PageRank edge exists only between node-set members (#75 bug 3): a
// destination outside the set — a self-loop, or an internal URL that was never
// crawled (robots-blocked, errored, over-limit, still queued) — is dropped from
// the graph entirely, so it neither receives a share nor dilutes its source's
// out-degree. This is the classic dangling-link removal from the PageRank
// literature; a source left with no node dsts is dangling and its mass is not
// redistributed. Only the unique in/out metrics keep non-crawled internal dsts.
func (a *analyzer) pageRank(internal []string, pairs []uint64) {
	const damping, iterations = 0.85, 40
	n := len(internal)
	node := make([]bool, n)
	nodes := 0
	for i, u := range internal {
		if a.pages[u].State == crawler.StateCrawled {
			node[i] = true
			nodes++
		}
	}
	if nodes == 0 {
		return
	}
	out := make([]int, n)
	edges := pairs[:0] // filtered in place; the unique metrics are done with pairs
	for _, p := range pairs {
		s, d := int(p>>32), int(uint32(p))
		if s != d && node[s] && node[d] {
			edges = append(edges, p)
			out[s]++
		}
	}
	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range internal {
		if node[i] {
			rank[i] = 1.0 / float64(nodes)
		}
	}
	base := (1 - damping) / float64(nodes)
	for range iterations {
		for i := range next {
			next[i] = 0
			if node[i] {
				next[i] = base
			}
		}
		for _, p := range edges {
			s, d := p>>32, uint32(p)
			next[d] += damping * rank[s] / float64(out[s])
		}
		rank, next = next, rank
	}
	max := 0.0
	for i, v := range rank {
		if node[i] && v > max {
			max = v
		}
	}
	if max > 0 {
		for i, u := range internal {
			if node[i] {
				a.res.LinkScores[u] = rank[i] / max * 100
			}
		}
	}
}

// chains follows redirect targets (and canonical targets) through the page
// set, flagging chains of 2+ hops and loops.
func (a *analyzer) chains() {
	for url, rec := range a.pages {
		if rec.RedirectURL != "" {
			a.followChain(url, "redirect", func(r *crawler.PageRecord) string { return r.RedirectURL })
		}
		if canonicalTarget(rec) != "" && canonicalTarget(rec) != url {
			a.followChain(url, "canonical", func(r *crawler.PageRecord) string {
				if t := canonicalTarget(r); t != r.URL {
					return t
				}
				return ""
			})
		}
	}
}

func canonicalTarget(rec *crawler.PageRecord) string {
	if rec.Facts == nil {
		return ""
	}
	for _, c := range rec.Facts.CanonicalHTML {
		if c != "" {
			return c
		}
	}
	for _, c := range rec.Facts.CanonicalHTTP {
		if c != "" {
			return c
		}
	}
	return ""
}

func (a *analyzer) followChain(source, typ string, next func(*crawler.PageRecord) string) {
	seen := map[string]bool{source: true}
	var hops []string
	current := source
	loop := false
	for {
		rec, ok := a.pages[current]
		if !ok {
			break
		}
		target := next(rec)
		if target == "" {
			break
		}
		hops = append(hops, target)
		if seen[target] {
			loop = true
			break
		}
		seen[target] = true
		current = target
		if len(hops) > 25 {
			break
		}
	}
	if len(hops) < 2 && !loop {
		return
	}
	chain := Chain{Type: typ, Source: source, Hops: hops, Loop: loop}
	if final, ok := a.pages[hops[len(hops)-1]]; ok {
		chain.Final = final.URL
		chain.FinalStatus = final.StatusCode
	}
	a.res.Chains = append(a.res.Chains, chain)
	switch {
	case loop && typ == "redirect":
		a.add(source, "redirect_loop", fmt.Sprintf("%d hops", len(hops)))
	case typ == "redirect":
		a.add(source, "redirect_chain", fmt.Sprintf("%d hops", len(hops)))
	case typ == "canonical":
		a.add(source, "canonical_chain", fmt.Sprintf("%d hops", len(hops)))
	}
}

// nearDuplicates estimates pairwise content similarity with minhash
// signatures over 5-word shingles of the content-area text.
func (a *analyzer) nearDuplicates() {
	threshold := float64(a.cfg.Content.NearDuplicates.Threshold)
	type cand struct {
		url string
		sig signature
	}
	var cands []cand
	for url, rec := range a.pages {
		if rec.Facts == nil || rec.Scope != "internal" || rec.State != crawler.StateCrawled {
			continue
		}
		if a.cfg.Content.NearDuplicates.IndexableOnly && !rec.Indexable {
			continue
		}
		// SF's "Ignore Paginated URLs for Duplicate Filters" also covers the
		// Content near-duplicate filter: page 2+ of a sequence (declares
		// rel="prev") is neither flagged nor offered as a match target.
		if a.cfg.Advanced.IgnorePaginatedForDuplicates && rec.Facts.IsPaginated() {
			continue
		}
		if rec.Facts.WordCount == 0 {
			continue
		}
		// Prefer the signature precomputed at crawl time (the pages.minhash
		// column, populated when near-dup was enabled) so we never re-materialise
		// ContentText. Older crawls / near-dup-off crawls carry no column; fall
		// back to hashing the body, which finalize loads in that case. A page
		// with NEITHER a signature nor body text (a lite-map page predating the
		// near-dup switch-on) is excluded outright: hashing the empty text yields
		// the all-max signature, which matches every other empty signature at
		// 100% (#74 N1) — finalize reloads the full map for that state, so
		// reaching here without text is a caller bug this line defends against.
		sig := minhash.Decode(rec.Minhash)
		if len(rec.Minhash) != minhash.EncodedLen {
			if rec.Facts.ContentText == "" {
				continue
			}
			sig = minhash.Of(rec.Facts.ContentText)
		}
		cands = append(cands, cand{url, sig})
	}
	exact := map[string]string{} // hash -> first url, to exclude exact dups
	for _, c := range cands {
		if rec := a.pages[c.url]; rec.Facts != nil {
			exact[c.url] = rec.Facts.Hash
		}
	}
	// LSH banding prunes the candidate space (vs all-pairs); every candidate
	// pair is still verified with the exact signature similarity.
	sigs := make([]signature, len(cands))
	for i, c := range cands {
		sigs[i] = c.sig
	}
	for _, p := range lshCandidates(sigs, lshRowsPerBand(threshold)) {
		i, j := p[0], p[1]
		if exact[cands[i].url] == exact[cands[j].url] {
			continue // exact duplicates are a separate check
		}
		sim := cands[i].sig.Similarity(cands[j].sig) * 100
		if sim < threshold {
			continue
		}
		a.noteNearDup(cands[i].url, cands[j].url, sim)
		a.noteNearDup(cands[j].url, cands[i].url, sim)
	}
	for url, nd := range a.res.NearDups {
		a.add(url, "content_near_duplicate",
			fmt.Sprintf("%.0f%% similar to %s", nd.ClosestSimilarity, nd.ClosestMatch))
	}
}

func (a *analyzer) noteNearDup(url, other string, sim float64) {
	nd := a.res.NearDups[url]
	nd.Count++
	if sim > nd.ClosestSimilarity {
		nd.ClosestSimilarity = sim
		nd.ClosestMatch = other
	}
	a.res.NearDups[url] = nd
}

// validHreflang checks a code against the embedded ISO registries: an
// assigned ISO 639-1 language, optionally followed by an assigned ISO 3166-1
// alpha-2 region, or the literal x-default. Well-formed but unassigned codes
// ("zz", "en-ZZ") are invalid.
func validHreflang(code string) bool {
	if strings.EqualFold(code, "x-default") {
		return true
	}
	lang, region, hasRegion := strings.Cut(code, "-")
	if !isocodes.ValidLanguage(lang) {
		return false
	}
	return !hasRegion || isocodes.ValidRegion(region)
}

// hreflangEntries returns a page's HTML + HTTP hreflang annotations.
func hreflangEntries(rec *crawler.PageRecord) []parse.Hreflang {
	return append(append([]parse.Hreflang{}, rec.Facts.HreflangHTML...), rec.Facts.HreflangHTTP...)
}

// hreflangSelfCode returns the language code a page declares for itself
// (its annotation whose URL is the page, x-default aside), "" if none.
func hreflangSelfCode(rec *crawler.PageRecord) string {
	if rec.Facts == nil {
		return ""
	}
	for _, h := range hreflangEntries(rec) {
		if h.URL == rec.URL && !strings.EqualFold(h.Lang, "x-default") {
			return h.Lang
		}
	}
	return ""
}

func (a *analyzer) hreflang() {
	annotated := map[string]string{} // annotation target -> smallest annotating source
	for url, rec := range a.pages {
		if rec.Facts == nil {
			continue
		}
		entries := hreflangEntries(rec)
		if len(entries) == 0 {
			continue
		}
		selfRef, xDefault := false, false
		for _, h := range entries {
			if !validHreflang(h.Lang) {
				a.add(url, "hreflang_invalid_code", h.Lang)
			}
			if strings.EqualFold(h.Lang, "x-default") {
				xDefault = true
			}
			if h.URL == url {
				selfRef = true
				continue
			}
			target, ok := a.pages[h.URL]
			if !ok {
				continue
			}
			if cur, seen := annotated[h.URL]; !seen || url < cur {
				annotated[h.URL] = url
			}
			if target.StatusCode != 200 {
				a.add(url, "hreflang_non_200", h.URL)
				continue
			}
			// the return-link family: hreflang must reference indexable,
			// canonical URLs, with codes matching the target's own declaration
			if !strings.EqualFold(h.Lang, "x-default") {
				if sc := hreflangSelfCode(target); sc != "" && !strings.EqualFold(sc, h.Lang) {
					a.add(url, "hreflang_inconsistent_return",
						fmt.Sprintf("%s self-declares %q, annotated %q", h.URL, sc, h.Lang))
				}
			}
			if t := canonicalTarget(target); t != "" && t != target.URL {
				a.add(url, "hreflang_non_canonical_return", h.URL)
			}
			if target.IndexabilityStatus == indexability.Noindex {
				a.add(url, "hreflang_noindex_return", h.URL)
			}
			if target.Facts != nil {
				returns := false
				for _, th := range hreflangEntries(target) {
					if th.URL == url {
						returns = true
						break
					}
				}
				if !returns {
					a.add(url, "hreflang_missing_return", h.URL)
				}
			}
		}
		if !selfRef {
			a.add(url, "hreflang_missing_self_reference", "")
		}
		if !xDefault {
			a.add(url, "hreflang_missing_x_default", "")
		}
	}
	hyperlinked := a.hyperlinkedSet()
	for t, src := range annotated {
		rec := a.pages[t]
		if rec.Scope == "internal" && rec.State == crawler.StateCrawled && !hyperlinked[t] {
			a.add(t, "hreflang_unlinked", "annotated by "+src)
		}
	}
}

// nextTarget / prevTarget return the first pagination annotation in a
// direction ("" when none) — the step functions for loop walking.
func nextTarget(rec *crawler.PageRecord) string {
	if rec.Facts == nil {
		return ""
	}
	for _, t := range append(append([]string{}, rec.Facts.NextHTML...), rec.Facts.NextHTTP...) {
		if t != "" {
			return t
		}
	}
	return ""
}

func prevTarget(rec *crawler.PageRecord) string {
	if rec.Facts == nil {
		return ""
	}
	for _, t := range append(append([]string{}, rec.Facts.PrevHTML...), rec.Facts.PrevHTTP...) {
		if t != "" {
			return t
		}
	}
	return ""
}

// followPagination walks a pagination chain from start, reporting the hop
// count when the chain revisits a URL (a loop). Bounded like followChain.
func (a *analyzer) followPagination(start string, step func(*crawler.PageRecord) string) (int, bool) {
	seen := map[string]bool{start: true}
	current := start
	for hops := 1; hops <= 25; hops++ {
		rec, ok := a.pages[current]
		if !ok {
			return 0, false
		}
		t := step(rec)
		if t == "" {
			return 0, false
		}
		if seen[t] {
			return hops, true
		}
		seen[t] = true
		current = t
	}
	return 0, false
}

func (a *analyzer) pagination() {
	annotated := map[string]string{} // pagination target -> smallest annotating source
	for url, rec := range a.pages {
		if rec.Facts == nil {
			continue
		}
		check := func(targets []string, expectBack func(*parse.Facts) []string) {
			for _, t := range targets {
				target, ok := a.pages[t]
				if !ok {
					continue
				}
				if t != url {
					if cur, seen := annotated[t]; !seen || url < cur {
						annotated[t] = url
					}
				}
				if target.StatusCode != 200 {
					a.add(url, "pagination_non_200", t)
					continue
				}
				if target.Facts == nil {
					continue
				}
				back := expectBack(target.Facts)
				reciprocal := false
				for _, b := range back {
					if b == url {
						reciprocal = true
						break
					}
				}
				if !reciprocal {
					a.add(url, "pagination_sequence_error", t)
				}
			}
		}
		check(rec.Facts.NextHTML, func(f *parse.Facts) []string { return f.PrevHTML })
		check(rec.Facts.PrevHTML, func(f *parse.Facts) []string { return f.NextHTML })
		// loops are walked per direction: reciprocal next/prev pairs are the
		// healthy shape, a chain revisiting a URL in one direction is not
		for _, step := range []func(*crawler.PageRecord) string{nextTarget, prevTarget} {
			if hops, loop := a.followPagination(url, step); loop {
				a.add(url, "pagination_loop", fmt.Sprintf("%d hops", hops))
			}
		}
	}
	hyperlinked := a.hyperlinkedSet()
	for t, src := range annotated {
		rec := a.pages[t]
		if rec.Scope == "internal" && rec.State == crawler.StateCrawled && !hyperlinked[t] {
			a.add(t, "pagination_unlinked", "in pagination of "+src)
		}
	}
}

// sitemaps runs the set operations between sitemap entries and crawl results.
func (a *analyzer) sitemaps(index SitemapIndex) {
	perSitemap := map[string]int{} // sitemap URL -> listed URL count
	for _, listedIn := range index {
		for _, sm := range listedIn {
			perSitemap[sm]++
		}
	}
	for sm, n := range perSitemap {
		if n > 50000 {
			a.add(sm, "sitemap_over_50k", fmt.Sprintf("%d URLs", n))
		}
	}
	for url, listedIn := range index {
		rec, ok := a.pages[url]
		if !ok {
			continue
		}
		if !rec.Indexable && rec.State == crawler.StateCrawled {
			a.add(url, "sitemap_non_indexable", strings.Join(listedIn, ", "))
		}
		// A <urlset> must list crawlable page URLs. An entry that is itself an
		// XML sitemap means the site listed a child sitemap as a <url> instead
		// of declaring it in a <sitemapindex>/<sitemap> — the child's URLs are
		// then not properly declared. bluesnake (unlike SF, which silently
		// re-parses it) stays spec-correct AND surfaces the malformation.
		if rec.Scope == "internal" && rec.State == crawler.StateCrawled &&
			isSitemapContentType(rec.ContentType) {
			a.add(url, "sitemap_nested_as_url", strings.Join(listedIn, ", "))
		}
		// discovered only via the sitemap: no inlinks and no followed-link
		// path from a seed (depth 0 covers resumed crawls, which keep
		// admit-time depths)
		if rec.Inlinks == 0 && rec.DiscoveredFrom == "" &&
			(rec.Depth == crawler.NoDepth || rec.Depth == 0) {
			a.add(url, "sitemap_orphan", strings.Join(listedIn, ", "))
		}
		if len(listedIn) > 1 {
			a.add(url, "sitemap_in_multiple", strings.Join(listedIn, ", "))
		}
	}
	for url, rec := range a.pages {
		if rec.Scope != "internal" || rec.State != crawler.StateCrawled ||
			!rec.Indexable || rec.Facts == nil {
			continue
		}
		if _, listed := index[url]; !listed {
			a.add(url, "sitemap_not_in_sitemap", "")
		}
	}
}

// llmsTxt audits the /llms.txt site files: structural validation of each file
// (per host) plus cross-validation of its curated links against the crawl
// graph. Like the sitemap analyzer it keys every issue on the relevant URL —
// the llms.txt URL for file-level checks, the curated target for link checks —
// so the issues table needs no synthetic page rows.
func (a *analyzer) llmsTxt(d *LlmsTxtData) {
	// File-level checks: one implementation, shared with the standalone
	// `tools llms` tester (sitecheck.LlmsReport.Findings), so the two paths
	// can never disagree.
	rep := &sitecheck.LlmsReport{}
	for _, f := range d.Files {
		rep.Files = append(rep.Files, sitecheck.LlmsFile{
			URL: f.URL, Kind: f.Kind, Status: f.Status, Found: f.Found,
			Title: f.Title, Summary: f.Summary, Malformed: f.Malformed,
		})
	}
	for _, f := range rep.Findings() {
		a.add(f.URL, f.IssueID, f.Detail)
	}

	// Cross-validate curated links against the crawl. A link absent from the
	// page set (external with externals off, out of scope, or never reached)
	// can't be judged broken — it surfaces as "unverified" instead.
	for _, l := range d.Links {
		detail := "listed in " + l.Src
		if l.Section != "" {
			detail += " §" + l.Section
		}
		rec, ok := a.pages[l.URL]
		switch {
		case !ok:
			a.add(l.URL, "llms_txt_link_unverified", detail)
		case rec.State == crawler.StateError || rec.StatusCode < 200 || rec.StatusCode >= 400:
			a.add(l.URL, "llms_txt_broken_link", fmt.Sprintf("status %d; %s", rec.StatusCode, detail))
		case !rec.Indexable:
			a.add(l.URL, "llms_txt_link_non_indexable", strings.TrimSpace(rec.IndexabilityStatus+"; "+detail))
		}
	}
}

// isSitemapContentType reports whether a content type is generic XML as served
// for XML sitemaps (application/xml, text/xml). RSS/Atom feeds also carry "xml"
// but are not sitemaps, so they're excluded.
func isSitemapContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if !strings.Contains(ct, "xml") {
		return false
	}
	return !strings.Contains(ct, "rss") && !strings.Contains(ct, "atom")
}
