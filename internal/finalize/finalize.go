// Package finalize is the single post-crawl finalization path shared by the
// CLI, MCP and desktop. After a crawl drains it persists the per-page
// aggregates, records the final status, and — when the crawl completed —
// recomputes the full-graph crawl depth on resume and runs the analysis phase.
// Surface-specific output (CLI summaries, desktop events) stays with the caller
// via the returned Outcome; this package owns only the shared pipeline so the
// three surfaces can no longer drift.
package finalize

import (
	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// Outcome reports what finalization did, so each surface can render its own
// summary/events without re-querying.
type Outcome struct {
	Status      string // store.StatusCompleted | store.StatusInterrupted
	Crawled     int    // URLs fetched, over the full stored graph (authoritative)
	Total       int    // URLs encountered, over the full stored graph (authoritative)
	Analyzed    bool
	Chains      int
	NearDups    int
	IssueTotal  int // total issue occurrences across every check that fired
	IssueChecks int // number of distinct checks that fired
}

// Params carries the inputs the caller resolves per finalization.
type Params struct {
	StoreDir  string
	Cfg       *config.Config
	Seeds     []string // every seed Run() was given (resume re-roots depth from all)
	Resumed   bool     // this run resumed a stored crawl (informational; depth/inlinks now recompute for fresh and resume alike)
	Completed bool     // caller resolves pause-vs-stop into completed/interrupted
}

// Crawl finalizes a crawl that has just drained. It always persists the per-page
// aggregates (inlinks / first-wins discovered_from from the gated edges table)
// and records the final status; only when the crawl completed does it recompute
// depth over the full stored graph and run the analysis phase. The
// *crawler.Crawler is required for the depth recompute (it carries the
// config-derived follow predicate).
//
// Completion ordering is crash-safe (EC-05). The crawl is recorded
// StatusInterrupted up front — its resumable interim state — and only sealed
// StatusCompleted as the final step, once depth + analysis are durable on disk.
// A hard crash (OS kill / OOM / power loss) anywhere in the completed tail below
// therefore leaves a resumable, re-finalizable crawl, never a `completed` crawl
// carrying stale admit-time depths or an empty/partial issues+analysis table.
// Each tail step is idempotent, so a resume re-runs them to a byte-identical end
// state. ANY recorded error — including the inlinks/discovered_from write, the
// one aggregate this function computes itself (#74 R5) — blocks the seal: the
// crawl stays StatusInterrupted rather than sealing a wrong result, and the
// returned Outcome.Status reflects what the registry actually says.
func Crawl(c *crawler.Crawler, st *store.Crawl, res *crawler.Result, p Params) (Outcome, error) {
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Inlinks + first-wins discovered_from come from the gated edges table in pure
	// SQL (Phase-2 cutover) — no in-RAM RecomputeInlinks, no page map. Over the
	// full edges table this is already the full-graph count, so resume needs no
	// separate recompute. Seeds are seed-locked.
	note(st.SaveInlinksFromEdges(c.NormalizeSeeds(p.Seeds...)))
	// The registry counts come from the full stored graph, never the per-session
	// Result: on resume res counts only this session's pages. Best-effort — fall
	// back to the Result if the store read fails, consistent with note().
	crawled, total := res.Crawled, res.Total
	if c2, t2, err := st.Counts(); err != nil {
		note(err)
	} else {
		crawled, total = c2, t2
	}

	// Record the resumable interim status FIRST (EC-05). StatusCompleted is written
	// only after the completed tail below succeeds, so a crash mid-tail leaves the
	// crawl interrupted/resumable instead of completed-with-stale-data.
	note(store.SetStatus(p.StoreDir, st.ID, store.StatusInterrupted, crawled, total))
	out := Outcome{Status: store.StatusInterrupted, Crawled: crawled, Total: total}
	if !p.Completed {
		return out, firstErr
	}

	// --- completed tail (idempotent) ---------------------------------------------
	// A completed crawl — fresh or resumed — derives shortest-path depth from the
	// stored link graph (depth = shortest followed-link path, Screaming Frog
	// parity). Stream-and-drop freed the in-RAM page map a fresh crawl used to
	// compute this from, so both paths converge on the same store-backed recompute
	// (guarded by TestResumeEquivalence). A failure here is fatal to completion: we
	// return with the crawl still StatusInterrupted so a resume repairs it.
	if err := recomputeDepths(c, st, p.Seeds); err != nil {
		note(err)
		return out, firstErr
	}
	if p.Cfg.Analysis.Auto {
		a, err := Analyze(st, p.Cfg)
		if err != nil {
			note(err)
			return out, firstErr // leave StatusInterrupted; do not seal completed
		}
		out.Analyzed = true
		out.Chains, out.NearDups = a.Chains, a.NearDups
		out.IssueTotal, out.IssueChecks = a.IssueTotal, a.IssueChecks
	}

	// Depth + analysis are durable — but seal only if NOTHING failed: a noted
	// error anywhere above (e.g. the inlinks write) means the stored result is
	// wrong, and completing would freeze it as final (#74 R5). Outcome.Status
	// flips to completed only when the seal write itself succeeded, so the
	// caller-visible status can never disagree with the registry.
	if firstErr != nil {
		return out, firstErr
	}
	if err := store.SetStatus(p.StoreDir, st.ID, store.StatusCompleted, crawled, total); err != nil {
		note(err)
		return out, firstErr
	}
	out.Status = store.StatusCompleted
	return out, firstErr
}

// recomputeDepths recomputes shortest-followed-path depth over the full stored
// link graph (re-applying the follow gate) + redirect edges and persists it.
// Idempotent: re-running over the same graph yields the same depths, so a resume
// after a partial-crash finalize converges on the same result (EC-05). Reads only
// Facts.Links and scalars, never the page body, so it stays off the page-body RAM
// axis. Guards on a non-empty seed set — rooting the BFS from nothing would NULL
// every page's depth. discovered_from needs no recompute here: it is already
// persisted by SaveInlinksFromEdges (first-wins + seed-locked).
func recomputeDepths(c *crawler.Crawler, st *store.Crawl, seeds []string) error {
	if len(seeds) == 0 {
		return nil
	}
	links, err := st.LinkRows()
	if err != nil {
		return err
	}
	redirects, err := st.Redirects()
	if err != nil {
		return err
	}
	urls, err := st.ProcessedURLs()
	if err != nil {
		return err
	}
	return st.SaveDepthsMap(c.RecomputeDepthsFromLinks(links, redirects, urls, seeds))
}

// Analyze runs the post-crawl analysis phase over the full stored graph: issue
// evaluation followed by the graph analyses (link score, redirect chains,
// near-duplicates, hreflang, pagination, sitemaps), persisting everything back.
// Used by Crawl and by standalone re-analysis (the `analyze` command, desktop
// Reanalyze). The returned Outcome.Status is always StatusCompleted.
func Analyze(st *store.Crawl, cfg *config.Config) (Outcome, error) {
	// The ContentText-free map carries everything the issue catalogue and graph
	// analyses need except the two content-text scans (streamed below) and
	// near-duplicates (which needs the bodies — loaded fully only when enabled).
	lite, err := st.LoadPagesLite()
	if err != nil {
		return Outcome{}, err
	}
	occs, err := evaluateIssues(st, lite, cfg)
	if err != nil {
		return Outcome{}, err
	}
	if err := st.SaveIssues(occs); err != nil {
		return Outcome{}, err
	}
	sitemaps, err := st.SitemapIndex()
	if err != nil {
		return Outcome{}, err
	}
	llmstxt, err := st.LlmsTxt()
	if err != nil {
		return Outcome{}, err
	}
	analyzePages := lite
	if cfg.Analysis.NearDuplicates && cfg.Content.NearDuplicates.Enabled && !minhashCoverageComplete(lite) {
		// Near-duplicates need each page's minhash signature. When near-dup was
		// enabled at crawl time the signatures are persisted (the pages.minhash
		// column) and the ContentText-free lite map already carries them — analyze
		// reads them directly, so the page bodies are never re-materialised. The
		// fallback below fires whenever ANY content page lacks a signature: the
		// crawl ran with near-dup OFF and the operator turned it on for this
		// re-analysis (a pre-minhash crawl migrates the column in as NULL), or the
		// state is MIXED — some pages predate the switch-on (#74 N1). An
		// any-page-HAS-a-signature check ran the lite map through near-dup in the
		// mixed state, where every signature-less page hashed its empty lite
		// ContentText to the all-max signature and "matched" every other one at
		// 100%. It re-loads the full map (the sole pass that re-reads ContentText).
		full, ferr := st.LoadPages()
		if ferr != nil {
			return Outcome{}, ferr
		}
		analyzePages = full
	}
	// PageRank/unique link graph is computed in CSR form over the stored links
	// superset (Phase-2 PageRank-CSR), not each page's in-RAM Facts.Links.
	links, err := st.LinkRows()
	if err != nil {
		return Outcome{}, err
	}
	results := analyze.Run(analyzePages, sitemaps, llmstxt, cfg, analyze.WithLinks(links))
	if err := st.SaveAnalysis(results); err != nil {
		return Outcome{}, err
	}
	counts, err := st.IssueCounts()
	if err != nil {
		return Outcome{}, err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	crawled, encountered, _ := st.Counts() // informational; re-analysis doesn't change them
	return Outcome{
		Status:      store.StatusCompleted,
		Crawled:     crawled,
		Total:       encountered,
		Analyzed:    true,
		Chains:      len(results.Chains),
		NearDups:    len(results.NearDups),
		IssueTotal:  total,
		IssueChecks: len(counts),
	}, nil
}

// minhashCoverageComplete reports whether EVERY page near-dup could consider
// carries a precomputed signature — only then may near-dup run over the
// ContentText-free lite map. The check is a deliberate superset of analyze's
// candidate gate (crawled, internal, parsed, WordCount>0 — ignoring the
// indexable/paginated config filters): an over-broad miss merely loads the
// full map (correct, slower); an under-broad one would hand analyze a
// signature-less page whose empty lite ContentText hashes to the all-max
// signature and cross-matches every other one at 100% (#74 N1).
func minhashCoverageComplete(pages map[string]*crawler.PageRecord) bool {
	for _, rec := range pages {
		if rec.State != crawler.StateCrawled || rec.Scope != "internal" ||
			rec.Facts == nil || rec.Facts.WordCount == 0 {
			continue
		}
		if len(rec.Minhash) == 0 {
			return false
		}
	}
	return true
}

// evaluateIssues runs the full issue catalogue with bounded page-body memory: the
// whole-map checks run over the ContentText-free lite map (where the two content-
// text checks naturally no-op on the empty body), and those two — lorem/soft-404
// — are re-added from a one-row-at-a-time ContentText stream. The merged set is
// byte-identical to issues.Evaluate over a full map.
func evaluateIssues(st *store.Crawl, lite map[string]*crawler.PageRecord, cfg *config.Config) ([]issues.Occurrence, error) {
	// Cross-page duplicate detection runs in pure SQL (Phase-2 dup-rule-SQL); the
	// rest of the catalogue runs over the lite map with the two ContentText checks
	// streamed in.
	occs := issues.Evaluate(lite, cfg, issues.SkipDuplicates())
	dups, err := st.DuplicateIssues(cfg.Advanced.IgnoreNonIndexableForIssues, cfg.Advanced.IgnorePaginatedForDuplicates)
	if err != nil {
		return nil, err
	}
	occs = append(occs, dups...)
	err = st.StreamContentText(func(url, text string) error {
		if rec, ok := lite[url]; ok {
			occs = append(occs, issues.ContentTextChecks(rec, text, cfg)...)
		}
		return nil
	})
	return occs, err
}

// Issues re-evaluates only the issue catalogue over the full stored graph
// (without the graph analyses), persisting the occurrences. Used by the `issues`
// command, which lists issues and wants a cheap refresh, not a full re-analysis.
func Issues(st *store.Crawl, cfg *config.Config) error {
	lite, err := st.LoadPagesLite()
	if err != nil {
		return err
	}
	occs, err := evaluateIssues(st, lite, cfg)
	if err != nil {
		return err
	}
	return st.SaveIssues(occs)
}
