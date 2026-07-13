package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/store"
)

// compareTools exposes the pairwise crawl comparison to agents: the same
// enriched, cached report (compare.Report) the desktop Compare tab and the
// project pulse serve — one cache row per (prev, curr) pair, shared across
// every surface.
func (s *Server) compareTools() []Tool {
	return []Tool{
		{
			Name: "compare_crawls",
			Description: "Diff two stored crawls of the SAME site — what changed between the runs. Returns pages added/removed (with status + title), " +
				"always-on state changes (HTTP status and indexability flips), element-level changes (titles, descriptions, h1, word count, content similarity, structured data), " +
				"per-issue movement decorated with names/severities and both occurrence totals, and status-mix/indexability distributions for both runs. " +
				"Pass two crawl ids (order is fixed chronologically for you), or just `site` to compare its two most recent completed crawls. " +
				"Results are cached per crawl pair and shared with the desktop app, so repeat calls are instant.",
			InputSchema: schema(map[string]any{
				"prev_crawl_id": strProp("Older crawl of the pair (see list_crawls). Required unless site is given."),
				"curr_crawl_id": strProp("Newer crawl of the pair. Required unless site is given."),
				"site":          strProp("Alternative to ids: a host[:port] or URL (e.g. \"example.com\") — compares the site's two most recent completed crawls."),
				"max_list":      intProp("Cap every URL list in the response (added/removed pages, state changes, element changes, per-issue buckets). Default 100, max 2000; the *_count fields are always exact."),
				"force":         boolProp("Recompute even when a cached comparison exists (replaces the cached row)."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					PrevCrawlID string `json:"prev_crawl_id"`
					CurrCrawlID string `json:"curr_crawl_id"`
					Site        string `json:"site"`
					MaxList     int    `json:"max_list"`
					Force       bool   `json:"force"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				prevID, currID := a.PrevCrawlID, a.CurrCrawlID
				switch {
				case a.Site != "":
					if prevID != "" || currID != "" {
						return "", fmt.Errorf("pass either site or the two crawl ids, not both")
					}
					var err error
					prevID, currID, err = s.resolveSitePair(a.Site)
					if err != nil {
						return "", err
					}
				case prevID == "" || currID == "":
					return "", fmt.Errorf("pass prev_crawl_id and curr_crawl_id (see list_crawls), or site to use its two most recent completed crawls")
				default:
					prevID, currID = s.orderPair(prevID, currID)
				}
				rep, err := compare.CachedReport(s.backend.StoreDir(), prevID, currID, a.Force, nil)
				if err != nil {
					return "", err
				}
				trimReport(rep, maxList(a.MaxList))
				return jsonText(struct {
					*compare.Report
					Note string `json:"note"`
				}{rep, compareNote})
			},
		},
	}
}

const compareNote = "Issue buckets: added/removed = page in both crawls entered/left the issue; new/missing = page only in one crawl. " +
	"URL lists are capped (max_list); every *_count field is exact. Full lists: query either crawl DB directly."

// maxList clamps the response's per-list cap (default 100).
func maxList(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 2000:
		return 2000
	default:
		return n
	}
}

// trimReport caps every URL list in the report for response size; counts stay
// exact. The cached row is untouched — this trims the returned copy only.
func trimReport(r *compare.Report, n int) {
	trim := func(u []string) []string {
		if len(u) > n {
			return u[:n]
		}
		return u
	}
	if len(r.NewPages) > n {
		r.NewPages = r.NewPages[:n]
	}
	if len(r.MissingPages) > n {
		r.MissingPages = r.MissingPages[:n]
	}
	if len(r.StateChanges) > n {
		r.StateChanges = r.StateChanges[:n]
	}
	if len(r.ElementChanges) > n {
		r.ElementChanges = r.ElementChanges[:n]
	}
	for i := range r.IssueDeltas {
		d := &r.IssueDeltas[i]
		d.Added, d.New = trim(d.Added), trim(d.New)
		d.Removed, d.Missing = trim(d.Removed), trim(d.Missing)
	}
}

// resolveSitePair returns the two most recent COMPLETED crawls whose seed host
// matches site (exact lowercased host[:port]; a full URL is tolerated).
func (s *Server) resolveSitePair(site string) (prevID, currID string, err error) {
	host := strings.ToLower(strings.TrimSpace(site))
	if strings.Contains(host, "://") {
		if u, e := url.Parse(host); e == nil && u.Host != "" {
			host = strings.ToLower(u.Host)
		}
	}
	host = strings.SplitN(host, "/", 2)[0]
	if host == "" {
		return "", "", fmt.Errorf("site must be a host like \"example.com\"")
	}
	infos, err := store.ListCrawls(s.backend.StoreDir())
	if err != nil {
		return "", "", err
	}
	var matches []store.Info
	for _, in := range infos {
		if in.Status != store.StatusCompleted {
			continue
		}
		if u, e := url.Parse(strings.TrimSpace(in.Seed)); e == nil && strings.ToLower(u.Host) == host {
			matches = append(matches, in)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Started.After(matches[j].Started) })
	if len(matches) < 2 {
		return "", "", fmt.Errorf("site %q has %d completed crawl(s) — a comparison needs two; list_crawls shows what exists", host, len(matches))
	}
	return matches[1].ID, matches[0].ID, nil
}

// orderPair swaps the ids when they arrive newest-first, so added/removed
// always read forward in time. Unknown ids pass through — CachedReport errors
// with the missing id.
func (s *Server) orderPair(prevID, currID string) (string, string) {
	infos, err := store.ListCrawls(s.backend.StoreDir())
	if err != nil {
		return prevID, currID
	}
	byID := map[string]store.Info{}
	for _, in := range infos {
		byID[in.ID] = in
	}
	p, pok := byID[prevID]
	c, cok := byID[currID]
	if pok && cok && p.Started.After(c.Started) {
		return currID, prevID
	}
	return prevID, currID
}
