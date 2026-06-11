// Package compare diffs two stored crawls (DESIGN.md §5.7): per-issue URL
// membership deltas using Screaming Frog's four buckets (Added/New/Removed/
// Missing), plus element-level change detection between crawls, with regex
// URL mapping so restructured sites compare as the same pages.
package compare

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

// Delta is the per-issue membership change between two crawls.
// Screaming Frog semantics: Added/Removed = URL exists in both crawls and
// entered/left the issue; New/Missing = URL exists in only one crawl.
type Delta struct {
	IssueID string   `json:"issue"`
	Added   []string `json:"added,omitempty"`
	New     []string `json:"new,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// Change is one element-level difference on a URL present in both crawls.
type Change struct {
	URL      string `json:"url"`
	Element  string `json:"element"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

// Result is the full comparison.
type Result struct {
	PagesPrevious int      `json:"pages_previous"`
	PagesCurrent  int      `json:"pages_current"`
	NewPages      []string `json:"new_pages,omitempty"`
	MissingPages  []string `json:"missing_pages,omitempty"`
	Deltas        []Delta  `json:"issue_deltas,omitempty"`
	Changes       []Change `json:"changes,omitempty"`
}

// Input bundles one crawl's data.
type Input struct {
	Pages  map[string]*crawler.PageRecord
	Issues map[string][]string // issue id -> URLs
}

// Run compares previous vs current. URL mapping regexes from the config are
// applied to *previous* URLs so renamed structures align.
func Run(prev, curr Input, cfg *config.Config) (*Result, error) {
	mapURL, err := buildMapper(cfg.Compare.URLMapping)
	if err != nil {
		return nil, err
	}

	prevPages := map[string]*crawler.PageRecord{}
	for url, rec := range prev.Pages {
		prevPages[mapURL(url)] = rec
	}
	prevIssues := map[string]map[string]bool{}
	for id, urls := range prev.Issues {
		set := map[string]bool{}
		for _, u := range urls {
			set[mapURL(u)] = true
		}
		prevIssues[id] = set
	}
	currIssues := map[string]map[string]bool{}
	for id, urls := range curr.Issues {
		set := map[string]bool{}
		for _, u := range urls {
			set[u] = true
		}
		currIssues[id] = set
	}

	res := &Result{PagesPrevious: len(prevPages), PagesCurrent: len(curr.Pages)}
	for url := range curr.Pages {
		if _, ok := prevPages[url]; !ok {
			res.NewPages = append(res.NewPages, url)
		}
	}
	for url := range prevPages {
		if _, ok := curr.Pages[url]; !ok {
			res.MissingPages = append(res.MissingPages, url)
		}
	}
	sort.Strings(res.NewPages)
	sort.Strings(res.MissingPages)

	// issue deltas
	allIssues := map[string]bool{}
	for id := range prevIssues {
		allIssues[id] = true
	}
	for id := range currIssues {
		allIssues[id] = true
	}
	ids := make([]string, 0, len(allIssues))
	for id := range allIssues {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := Delta{IssueID: id}
		for url := range currIssues[id] {
			_, inPrevCrawl := prevPages[url]
			switch {
			case !inPrevCrawl:
				d.New = append(d.New, url)
			case !prevIssues[id][url]:
				d.Added = append(d.Added, url)
			}
		}
		for url := range prevIssues[id] {
			_, inCurrCrawl := curr.Pages[url]
			switch {
			case !inCurrCrawl:
				d.Missing = append(d.Missing, url)
			case !currIssues[id][url]:
				d.Removed = append(d.Removed, url)
			}
		}
		if len(d.Added)+len(d.New)+len(d.Removed)+len(d.Missing) > 0 {
			sort.Strings(d.Added)
			sort.Strings(d.New)
			sort.Strings(d.Removed)
			sort.Strings(d.Missing)
			res.Deltas = append(res.Deltas, d)
		}
	}

	res.Changes = changeDetection(prevPages, curr.Pages, cfg)
	return res, nil
}

func buildMapper(mappings []config.URLMapping) (func(string) string, error) {
	type rule struct {
		re      *regexp.Regexp
		replace string
	}
	var rules []rule
	for _, m := range mappings {
		re, err := regexp.Compile(m.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compare.url_mapping: %w", err)
		}
		rules = append(rules, rule{re, m.Replace})
	}
	return func(url string) string {
		for _, r := range rules {
			url = r.re.ReplaceAllString(url, r.replace)
		}
		return url
	}, nil
}

// changeDetection compares the configured elements for URLs in both crawls.
func changeDetection(prev, curr map[string]*crawler.PageRecord, cfg *config.Config) []Change {
	enabled := cfg.Compare.ChangeDetection
	var changes []Change
	urls := make([]string, 0, len(curr))
	for u := range curr {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	first := func(v []string) string {
		if len(v) > 0 {
			return v[0]
		}
		return ""
	}
	for _, url := range urls {
		p, ok := prev[url]
		c := curr[url]
		if !ok {
			continue
		}
		note := func(element, prevVal, currVal string) {
			if prevVal != currVal && slices.Contains(enabled, element) {
				changes = append(changes, Change{URL: url, Element: element, Previous: prevVal, Current: currVal})
			}
		}
		note("crawl_depth", itoa(p.Depth), itoa(c.Depth))
		note("links", itoa(p.Inlinks), itoa(c.Inlinks))
		if p.Facts != nil && c.Facts != nil {
			note("titles", first(p.Facts.Titles), first(c.Facts.Titles))
			note("descriptions", first(p.Facts.Descriptions), first(c.Facts.Descriptions))
			note("h1", first(p.Facts.H1s), first(c.Facts.H1s))
			note("word_count", itoa(p.Facts.WordCount), itoa(c.Facts.WordCount))
		}
	}
	return changes
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
