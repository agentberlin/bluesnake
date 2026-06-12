// Package analyze implements the post-crawl analysis phase (DESIGN.md §5.6):
// whole-graph computations that can't run while streaming — link score
// (PageRank), redirect/canonical chains and loops, near-duplicate content
// (minhash), hreflang reciprocity, pagination sequences, and sitemap set
// operations. All analyses are pure over the loaded page set and re-runnable.
package analyze

import (
	"fmt"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/indexability"
	"github.com/agentberlin/bluesnake/internal/isocodes"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
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

// Run executes every enabled analysis over the crawl's pages.
func Run(pages map[string]*crawler.PageRecord, sitemaps SitemapIndex, cfg *config.Config) *Results {
	r := &Results{
		LinkScores: map[string]float64{},
		UniqueIn:   map[string]int{},
		UniqueOut:  map[string]int{},
		NearDups:   map[string]NearDup{},
	}
	a := &analyzer{pages: pages, cfg: cfg, res: r}
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
	return r
}

type analyzer struct {
	pages       map[string]*crawler.PageRecord
	cfg         *config.Config
	res         *Results
	hyperlinked map[string]bool // memoized by hyperlinkedSet
}

func (a *analyzer) add(url, id, detail string) {
	a.res.Occurrences = append(a.res.Occurrences, issues.Occurrence{URL: url, IssueID: id, Detail: detail})
}

// hyperlinkedSet returns every URL that is the target of at least one
// hyperlink from another page (self-links excluded). The "unlinked" checks
// (canonical-/hreflang-/pagination-only discoverability) test against it.
func (a *analyzer) hyperlinkedSet() map[string]bool {
	if a.hyperlinked != nil {
		return a.hyperlinked
	}
	a.hyperlinked = map[string]bool{}
	for src, rec := range a.pages {
		if rec.Facts == nil {
			continue
		}
		for _, l := range rec.Facts.Links {
			if l.Type == parse.Hyperlink && l.URL != "" && l.URL != src {
				a.hyperlinked[l.URL] = true
			}
		}
	}
	return a.hyperlinked
}

// linkGraph computes unique in/outlink counts and PageRank-style link scores
// over followed internal hyperlink edges between crawled pages.
func (a *analyzer) linkGraph() {
	// self-links count towards unique in/outlinks (Screaming Frog parity);
	// PageRank below skips them so a page cannot vote for itself
	edges := map[string]map[string]bool{} // src -> set of dst
	for url, rec := range a.pages {
		if rec.Facts == nil || rec.Scope != "internal" {
			continue
		}
		for _, l := range rec.Facts.Links {
			if l.Type != parse.Hyperlink || l.Nofollow {
				continue
			}
			target, ok := a.pages[l.URL]
			if !ok || target.Scope != "internal" {
				continue
			}
			if edges[url] == nil {
				edges[url] = map[string]bool{}
			}
			edges[url][l.URL] = true
		}
	}
	for src, dsts := range edges {
		a.res.UniqueOut[src] = len(dsts)
		for dst := range dsts {
			a.res.UniqueIn[dst]++
		}
	}

	if !a.cfg.Analysis.LinkScore {
		return
	}
	// PageRank, d = 0.85
	const damping, iterations = 0.85, 40
	nodes := make([]string, 0, len(a.pages))
	for url, rec := range a.pages {
		if rec.Scope == "internal" && rec.State == crawler.StateCrawled {
			nodes = append(nodes, url)
		}
	}
	if len(nodes) == 0 {
		return
	}
	rank := make(map[string]float64, len(nodes))
	for _, n := range nodes {
		rank[n] = 1.0 / float64(len(nodes))
	}
	for range iterations {
		next := make(map[string]float64, len(nodes))
		base := (1 - damping) / float64(len(nodes))
		for _, n := range nodes {
			next[n] = base
		}
		for src, dsts := range edges {
			out := len(dsts)
			if dsts[src] {
				out-- // self-loops count for link metrics, not for PageRank
			}
			if out == 0 {
				continue
			}
			share := damping * rank[src] / float64(out)
			for dst := range dsts {
				if dst == src {
					continue
				}
				next[dst] += share
			}
		}
		rank = next
	}
	max := 0.0
	for _, v := range rank {
		if v > max {
			max = v
		}
	}
	if max > 0 {
		for n, v := range rank {
			a.res.LinkScores[n] = v / max * 100
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
		if rec.Facts.WordCount == 0 {
			continue
		}
		cands = append(cands, cand{url, minhash(rec.Facts.ContentText)})
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
		sim := cands[i].sig.similarity(cands[j].sig) * 100
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
