// Package analyze implements the post-crawl analysis phase (DESIGN.md §5.6):
// whole-graph computations that can't run while streaming — link score
// (PageRank), redirect/canonical chains and loops, near-duplicate content
// (minhash), hreflang reciprocity, pagination sequences, and sitemap set
// operations. All analyses are pure over the loaded page set and re-runnable.
package analyze

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/indexability"
	"github.com/agentberlin/bluesnake/internal/isocodes"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/minhash"
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

// Run executes every enabled analysis over the crawl's pages.
// Option configures a Run.
type Option func(*analyzer)

// WithLinks supplies the stored link superset so the link graph (unique in/out +
// PageRank) and the hyperlinked set are computed in CSR form over it instead of
// each page's in-RAM Facts.Links — the Phase-2 PageRank-CSR path. Without it the
// analyzer falls back to Facts.Links (unchanged behaviour for existing callers).
func WithLinks(links []crawler.LinkRow) Option {
	return func(a *analyzer) { a.links = links }
}

func Run(pages map[string]*crawler.PageRecord, sitemaps SitemapIndex, llmstxt *LlmsTxtData, cfg *config.Config, opts ...Option) *Results {
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
	return r
}

type analyzer struct {
	pages       map[string]*crawler.PageRecord
	links       []crawler.LinkRow // stored link superset; nil ⇒ use Facts.Links
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
	if a.links != nil {
		for _, l := range a.links {
			if l.Type == string(parse.Hyperlink) && l.Dst != "" && l.Dst != l.Src {
				a.hyperlinked[l.Dst] = true
			}
		}
		return a.hyperlinked
	}
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
	internal := func(url string) bool {
		rec, ok := a.pages[url]
		return ok && rec.Scope == "internal"
	}
	if a.links != nil {
		// CSR over the stored link superset: same gate as the Facts.Links walk —
		// hyperlink, followed, internal src (with facts) -> internal dst.
		for _, l := range a.links {
			if l.Type != string(parse.Hyperlink) || l.Nofollow {
				continue
			}
			if src, ok := a.pages[l.Src]; !ok || src.Facts == nil || src.Scope != "internal" {
				continue
			}
			if !internal(l.Dst) {
				continue
			}
			if edges[l.Src] == nil {
				edges[l.Src] = map[string]bool{}
			}
			edges[l.Src][l.Dst] = true
		}
	} else {
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
	// Canonical accumulation order (FIN-CSRID). Go map iteration is randomized and
	// float addition is non-associative, so iterating `edges`/`dsts` in map order
	// made the stored link_score jitter run-to-run at ~1e-12. Sort the node set and
	// precompute each source's self-loop-excluded out-degree + sorted destination
	// list ONCE, then accumulate in that fixed order every iteration — making the
	// score bit-stable while reproducing the previous values exactly (a non-node or
	// dangling source contributes 0, as before).
	sort.Strings(nodes)
	srcs := make([]string, 0, len(edges))
	for src := range edges {
		srcs = append(srcs, src)
	}
	sort.Strings(srcs)
	out := make(map[string]int, len(srcs))
	dstsBySrc := make(map[string][]string, len(srcs))
	for _, src := range srcs {
		set := edges[src]
		n := len(set)
		if set[src] {
			n-- // self-loops count for link metrics, not for PageRank
		}
		out[src] = n
		if n == 0 {
			continue
		}
		dsts := make([]string, 0, n)
		for dst := range set {
			if dst != src {
				dsts = append(dsts, dst)
			}
		}
		sort.Strings(dsts)
		dstsBySrc[src] = dsts
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
		for _, src := range srcs {
			if out[src] == 0 {
				continue
			}
			share := damping * rank[src] / float64(out[src])
			for _, dst := range dstsBySrc[src] {
				next[dst] += share
			}
		}
		rank = next
	}
	// max and the scaling assignment are reductions/keyed writes, so map iteration
	// order does not affect them — only the accumulation above had to be ordered.
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
	type group struct{ primary, full *LlmsTxtFile }
	hostOf := func(u string) string {
		if p, err := url.Parse(u); err == nil {
			return p.Host
		}
		return u
	}
	byHost := map[string]*group{}
	for i := range d.Files {
		f := &d.Files[i]
		g := byHost[hostOf(f.URL)]
		if g == nil {
			g = &group{}
			byHost[hostOf(f.URL)] = g
		}
		switch f.Kind {
		case "llms_txt":
			g.primary = f
		case "llms_full_txt":
			g.full = f
		}
	}
	for _, g := range byHost {
		if g.primary == nil {
			continue
		}
		if !g.primary.Found {
			a.add(g.primary.URL, "llms_txt_missing", "")
			continue // nothing else to validate when the file is absent
		}
		if g.primary.Title == "" {
			a.add(g.primary.URL, "llms_txt_invalid_format", "missing H1 title")
		}
		if g.primary.Summary == "" {
			a.add(g.primary.URL, "llms_txt_missing_summary", "")
		}
		if g.primary.Malformed {
			a.add(g.primary.URL, "llms_txt_malformed_link_list", "")
		}
		if g.full != nil && !g.full.Found {
			a.add(g.full.URL, "llms_full_txt_missing", "")
		}
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
