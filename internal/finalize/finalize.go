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
	Resumed   bool     // this run resumed a stored crawl
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

	note(st.UpdateInlinks(res.Pages))
	status := store.StatusInterrupted
	if p.Completed {
		status = store.StatusCompleted
	}
	note(store.SetStatus(p.StoreDir, st.ID, status, res.Crawled, res.Total))
	out := Outcome{Status: status}
	if !p.Completed {
		return out, firstErr
	}

	// On resume this session rewrote depth only for its own pages; recompute it
	// over the full two-session graph so depths match a fresh crawl (Screaming
	// Frog parity). The BFS re-roots from every seed Run() was given — all of
	// them, including each uploaded list seed — so list and spider resumes alike
	// land on shortest-path depths. Guard on a non-empty seed set: rooting from
	// nothing would NULL every page's depth. Fresh crawls already hold those
	// depths from Run().
	if p.Resumed && len(p.Seeds) > 0 {
		if all, err := st.LoadPages(); err != nil {
			note(err)
		} else {
			c.RecomputeDepths(all, p.Seeds...)
			note(st.SaveDepths(all))
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
	pages, err := st.LoadPages()
	if err != nil {
		return Outcome{}, err
	}
	if err := st.SaveIssues(issues.Evaluate(pages, cfg)); err != nil {
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
	results := analyze.Run(pages, sitemaps, llmstxt, cfg)
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
	return Outcome{
		Status:      store.StatusCompleted,
		Analyzed:    true,
		Chains:      len(results.Chains),
		NearDups:    len(results.NearDups),
		IssueTotal:  total,
		IssueChecks: len(counts),
	}, nil
}

// Issues re-evaluates only the issue catalogue over the full stored graph
// (without the graph analyses), persisting the occurrences. Used by the `issues`
// command, which lists issues and wants a cheap refresh, not a full re-analysis.
func Issues(st *store.Crawl, cfg *config.Config) error {
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	return st.SaveIssues(issues.Evaluate(pages, cfg))
}
