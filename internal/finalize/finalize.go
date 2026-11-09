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
	"github.com/agentberlin/bluesnake/internal/nearcopy"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// Outcome reports what finalization did, so each surface can render its own
// summary/events without re-querying.
type Outcome struct {
	Status   string // store.StatusCompleted | store.StatusInterrupted
	Crawled  int    // URLs fetched, over the full stored graph (authoritative)
	Total    int    // URLs encountered, over the full stored graph (authoritative)
	Analyzed bool
	Chains   int
	// NearCopies says the near-copy step ran — as part of the analysis, or on
	// its own when the rest of it is off; NearDups is then the number of pages
	// with at least one near copy.
	NearCopies  bool
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
		out.Chains, out.NearCopies, out.NearDups = a.Chains, a.NearCopies, a.NearDups
		out.IssueTotal, out.IssueChecks = a.IssueTotal, a.IssueChecks
	} else if analyze.NearCopiesOn(p.Cfg) {
		// The rest of the analysis is off, but near copies were asked for: they
		// are part of the bundle, so they are worked out here, once, before
		// any bundle is made — after a resume too — without link scores,
		// unique links or issues.
		nd, err := NearCopies(st, p.Cfg)
		if err != nil {
			note(err)
			return out, firstErr
		}
		out.NearCopies, out.NearDups = true, nd
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
// after a partial-crash finalize converges on the same result (EC-05). The BFS
// pulls each page's links from the store as it reaches the page and drops them
// once walked, so the step holds the page set and the depth map — sized by
// pages — and never the link rows (loading them whole put finalize on the links
// axis: 1.4 GB at 8,000 URLs of link-heavy pages). Guards on a non-empty seed
// set — rooting the BFS from nothing would NULL every page's depth.
// discovered_from needs no recompute here: it is already persisted by
// SaveInlinksFromEdges (first-wins + seed-locked).
func recomputeDepths(c *crawler.Crawler, st *store.Crawl, seeds []string) error {
	if len(seeds) == 0 {
		return nil
	}
	redirects, err := st.Redirects()
	if err != nil {
		return err
	}
	urls, err := st.ProcessedURLs()
	if err != nil {
		return err
	}
	out, err := st.OutLinks()
	if err != nil {
		return err
	}
	defer out.Close()
	depths, err := c.RecomputeDepths(out, redirects, urls, seeds)
	if err != nil {
		return err
	}
	return st.SaveDepthsMap(depths)
}

// Analyze runs the post-crawl analysis phase over the full stored graph: issue
// evaluation followed by the graph analyses (link score, redirect chains,
// near copies, hreflang, pagination, sitemaps), persisting everything back.
// Used by Crawl and by standalone re-analysis (the `analyze` command, desktop
// Reanalyze). The returned Outcome.Status is always StatusCompleted.
func Analyze(st *store.Crawl, cfg *config.Config) (Outcome, error) {
	// The lite map — no link lists, no page bodies — carries everything the
	// issue catalogue and graph analyses need except the per-page link and
	// content-text checks (streamed one page at a time below), the link graph
	// (read from the links table) and near copies (a step of their own that
	// streams each page's text from the store). Its size is set by pages, not
	// links: on a link-heavy site the link lists were 98% of the stored facts,
	// and a map that kept them needed 1.3 MB a page.
	lite, err := st.LoadPagesLite()
	if err != nil {
		return Outcome{}, err
	}
	occs, err := evaluateIssues(st, lite, cfg)
	if err != nil {
		return Outcome{}, err
	}
	// Full re-analysis is authoritative over the whole issues table: a nil
	// owned set replaces every row (stale analysis-phase rows included); the
	// analysis-phase occurrences are re-written by SaveAnalysis below.
	if err := st.SaveIssues(nil, occs); err != nil {
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
	siteChecks, err := st.SiteChecks()
	if err != nil {
		return Outcome{}, err
	}
	var nc *nearcopy.Result
	if analyze.NearCopiesOn(cfg) {
		if nc, err = nearCopies(st, cfg); err != nil {
			return Outcome{}, err
		}
	}
	// The PageRank/unique link graph is built from the stored hyperlinks,
	// streamed from the links table into int32 edges (8 bytes a hyperlink) —
	// never from the page map, which carries no link lists, and never as the
	// rows themselves.
	links := st.Hyperlinks()
	results := analyze.Run(lite, sitemaps, llmstxt, siteChecks, cfg,
		analyze.WithLinkSource(links), analyze.WithNearCopies(nc))
	if err := links.Err(); err != nil {
		return Outcome{}, err
	}
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
		NearCopies:  nc != nil,
		NearDups:    withCopies(nc),
		IssueTotal:  total,
		IssueChecks: len(counts),
	}, nil
}

// NearCopies runs the near-copy step on its own and persists its lists,
// leaving link scores and issues as they are: the step finalize runs when the
// rest of the analysis is off. It returns the number of pages with at least
// one near copy.
func NearCopies(st *store.Crawl, cfg *config.Config) (int, error) {
	nc, err := nearCopies(st, cfg)
	if err != nil {
		return 0, err
	}
	if err := st.SaveNearCopies(nc); err != nil {
		return 0, err
	}
	return withCopies(nc), nil
}

// nearCopies runs the step over the stored pages. It reads each compared
// page's text once, from the store, and keeps only its signature and a few
// fields; the shingle sets go to a temporary file beside the crawl database.
func nearCopies(st *store.Crawl, cfg *config.Config) (*nearcopy.Result, error) {
	s := analyze.NearCopySettings(cfg)
	return nearcopy.Run(st.NearCopyPages(s.IndexableOnly), s, st.TempDir())
}

// withCopies counts the pages with at least one near copy.
func withCopies(nc *nearcopy.Result) int {
	if nc == nil {
		return 0
	}
	n := 0
	for _, p := range nc.Pages {
		if p.Count > 0 {
			n++
		}
	}
	return n
}

// evaluateIssues runs the full issue catalogue with memory bounded by the page
// count: the whole-map checks — the cross-page duplicates among them, which read
// only titles, descriptions, headings and the content hash the lite map keeps —
// run over the lite map, and the checks that read a page's links or body
// (issues.LinkChecker, issues.ContentTextChecks) are fed both one page at a
// time from a single pass over the stored facts. The merged set is
// byte-identical to issues.Evaluate over a full map (pinned by the lite-path
// parity tests).
func evaluateIssues(st *store.Crawl, lite map[string]*crawler.PageRecord, cfg *config.Config) ([]issues.Occurrence, error) {
	occs := issues.Evaluate(lite, cfg, issues.SkipLinks())
	linkChecks := issues.NewLinkChecker(lite, cfg)
	err := st.StreamLinksAndText(func(url string, links []parse.Link, text string) error {
		rec, ok := lite[url]
		if !ok {
			return nil
		}
		linkChecks.Page(url, links)
		occs = append(occs, issues.ContentTextChecks(rec, text, cfg)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return append(occs, linkChecks.Finish()...), nil
}

// Issues re-evaluates only the issue catalogue over the full stored graph
// (without the graph analyses), persisting the occurrences. Used by the `issues`
// command, which lists issues and wants a cheap refresh, not a full re-analysis.
// It replaces only the rows of the checks it re-evaluated (#75): the
// analysis-phase occurrences a completed crawl already computed — chains,
// near-duplicates, hreflang, pagination, sitemaps, llms.txt — survive intact
// until the next Analyze recomputes them.
func Issues(st *store.Crawl, cfg *config.Config) error {
	lite, err := st.LoadPagesLite()
	if err != nil {
		return err
	}
	occs, err := evaluateIssues(st, lite, cfg)
	if err != nil {
		return err
	}
	return st.SaveIssues(issues.EvaluatedIDs(), occs)
}
