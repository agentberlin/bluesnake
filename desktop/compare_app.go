package main

import (
	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/store"
)

// The Compare tab's backend is the shared enriched report (compare.Report,
// internal/compare/report.go) — the same shape the MCP compare tools serve,
// cached per (prev, curr) pair in the registry's comparisons table. The only
// desktop twist is the input loader: compareInput reuses App's in-memory
// pages cache instead of re-reading the crawl DBs. Cache rows are purged by
// invalidate() whenever either crawl's content changes.

// CompareCrawls diffs two finished crawls of the same site, serving the cached
// report when the pair has been compared before. force recomputes (and
// replaces the cached row) regardless.
func (a *App) CompareCrawls(prevID, currID string, force bool) (*compare.Report, error) {
	return compare.CachedReport(a.storeDir, prevID, currID, force, a.compareInput)
}

// DeleteComparison drops one cached comparison; the next CompareCrawls of the
// pair recomputes it.
func (a *App) DeleteComparison(prevID, currID string) error {
	return store.DeleteComparison(a.storeDir, prevID, currID)
}

func (a *App) compareInput(id string) (compare.Input, error) {
	pages, err := a.loadPages(id)
	if err != nil {
		return compare.Input{}, err
	}
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return compare.Input{}, err
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return compare.Input{}, err
	}
	iss := map[string][]string{}
	for issueID, n := range counts {
		if n == 0 {
			continue
		}
		urls, err := st.IssueURLs(issueID)
		if err != nil {
			continue
		}
		iss[issueID] = urls
	}
	return compare.Input{Pages: pages, Issues: iss}, nil
}
