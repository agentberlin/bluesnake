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

// Crawl finalizes a crawl that has just drained. It always persists the
// per-page aggregates (UpdateInlinks) and the final status; only when the crawl
// completed does it recompute depth over the full two-session graph (resume
// only) and run the analysis phase. The *crawler.Crawler is required for the
// resume depth recompute (it carries the config-derived follow predicate).
//
// It is best-effort: every step is attempted and the first error is returned so
// the caller can surface it without losing later side effects (e.g. status is
// still recorded even if persisting aggregates failed).
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
	status := store.StatusInterrupted
	if p.Completed {
		status = store.StatusCompleted
	}
	// The registry counts come from the full stored graph, never the per-session
	// Result: on resume res counts only this session's pages. Best-effort — fall
	// back to the Result if the store read fails, consistent with note().
	crawled, total := res.Crawled, res.Total
	if c2, t2, err := st.Counts(); err != nil {
		note(err)
	} else {
		crawled, total = c2, t2
	}
	note(store.SetStatus(p.StoreDir, st.ID, status, crawled, total))
	out := Outcome{Status: status, Crawled: crawled, Total: total}
	if !p.Completed {
		return out, firstErr
	}

	// A completed crawl — fresh or resumed — derives shortest-path depth and the
	// full-graph hyperlink inlink count from the stored link graph. Stream-and-
	// drop freed the in-RAM page map a fresh crawl used to compute these from, so
	// both paths now converge on the same store-backed recompute (guarded by
	// TestResumeEquivalence): depth = shortest followed-link path (Screaming Frog
	// parity), inlinks = full-graph hyperlink count. The BFS re-roots from every
	// seed Run() was given — all of them, including each uploaded list seed — so
	// list and spider crawls alike land on shortest-path depths. discovered_from
	// needs no recompute: it is already persisted by SaveInlinkSources above
	// (first-wins + seed-locked, per session). Guard on a non-empty seed set:
	// rooting the BFS from nothing would NULL every page's depth.
	if len(p.Seeds) > 0 {
		// Depth + inlinks read only Facts.Links and scalars, never the page body,
		// so the ContentText-free map keeps this off the page-body RAM axis.
		// Depth is a CSR BFS over the stored links superset (re-applying the
		// follow gate) + redirect edges — no page map materialised here.
		links, lerr := st.LinkRows()
		redirects, rerr := st.Redirects()
		urls, uerr := st.ProcessedURLs()
		switch {
		case lerr != nil:
			note(lerr)
		case rerr != nil:
			note(rerr)
		case uerr != nil:
			note(uerr)
		default:
			note(st.SaveDepthsMap(c.RecomputeDepthsFromLinks(links, redirects, urls, p.Seeds)))
		}
	}

	if p.Cfg.Analysis.Auto {
		a, err := Analyze(st, p.Cfg)
		note(err)
		if err == nil {
			out.Analyzed = true
			out.Chains, out.NearDups = a.Chains, a.NearDups
			out.IssueTotal, out.IssueChecks = a.IssueTotal, a.IssueChecks
		}
	}
	return out, firstErr
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
	if cfg.Analysis.NearDuplicates && cfg.Content.NearDuplicates.Enabled && !hasMinhashSignatures(lite) {
		// Near-duplicates need each page's minhash signature. When near-dup was
		// enabled at crawl time the signatures are persisted (the pages.minhash
		// column) and the ContentText-free lite map already carries them — analyze
		// reads them directly, so the page bodies are never re-materialised. Only
		// an older crawl, or one crawled with near-dup OFF, lacks the column; fall
		// back to the full map (the sole pass that re-loads ContentText) then.
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

// hasMinhashSignatures reports whether any page in the (lite) map carries a
// precomputed near-dup signature — i.e. the crawl persisted the minhash column.
// When true, near-dup runs over the lite map and never reloads ContentText.
func hasMinhashSignatures(pages map[string]*crawler.PageRecord) bool {
	for _, rec := range pages {
		if len(rec.Minhash) > 0 {
			return true
		}
	}
	return false
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
