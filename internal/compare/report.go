package compare

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// The enriched comparison report: the engine Result decorated with what a
// consumer needs to read the diff without further crawl-DB work — issue
// catalogue names/severities and per-crawl occurrence counts, page briefs
// (status/title/indexability) for added/removed URLs, and both crawls'
// status-mix / indexability distributions. One report shape serves every
// surface (desktop Compare tab, project pulse, MCP compare tools), and
// CachedReport stores it in the registry's comparisons table keyed by the
// (prev, curr) pair — a diff of two finished crawls is deterministic, so it
// computes once and every revisit anywhere is a single registry read.

// ReportVersion invalidates cached reports whose shape predates the current
// struct — a stale-version row is treated as a cache miss.
const ReportVersion = 2

// list caps keep a big-site report bounded (over the Wails bridge or an MCP
// response); the *Count fields always carry the uncapped totals.
const (
	reportPageCap     = 1000 // new/missing page briefs
	reportChangeCap   = 2000 // element + state change rows
	reportIssueURLCap = 500  // URLs per issue-delta bucket
)

type Report struct {
	Version    int    `json:"v"`
	PrevID     string `json:"prev_id"`
	CurrID     string `json:"curr_id"`
	ComputedAt string `json:"computed_at"`
	Cached     bool   `json:"cached"`

	PagesPrev int `json:"pages_prev"`
	PagesCurr int `json:"pages_curr"`

	// status-mix buckets keyed 2xx/3xx/4xx/5xx/blocked/noresp
	StatusMixPrev map[string]int `json:"status_mix_prev"`
	StatusMixCurr map[string]int `json:"status_mix_curr"`
	// indexability over internal URLs
	IndexablePrev    int `json:"indexable_prev"`
	IndexableCurr    int `json:"indexable_curr"`
	NonIndexablePrev int `json:"non_indexable_prev"`
	NonIndexableCurr int `json:"non_indexable_curr"`

	NewCount     int         `json:"new_count"`
	MissingCount int         `json:"missing_count"`
	NewPages     []PageBrief `json:"new_pages"`
	MissingPages []PageBrief `json:"missing_pages"`

	StateChangeCount   int           `json:"state_change_count"`
	StatusFlipCount    int           `json:"status_flip_count"`       // pages whose status code moved
	IndexFlipCount     int           `json:"indexability_flip_count"` // pages whose indexability flipped
	StateChanges       []StateChange `json:"state_changes"`
	ElementChangeCount int           `json:"element_change_count"`
	ElementChanges     []Change      `json:"element_changes"`

	IssueDeltas []IssueMovement `json:"issue_deltas"`
}

// PageBrief is one added/removed URL with enough context to recognise it.
type PageBrief struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Status    int    `json:"status"`
	Indexable bool   `json:"indexable"`
}

// IssueMovement is one issue's movement between the crawls, decorated from
// the catalogue and carrying both occurrence totals so a consumer can render
// "12 → 9" without loading either crawl.
type IssueMovement struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Severity  string `json:"severity"`
	PrevCount int    `json:"prev_count"`
	CurrCount int    `json:"curr_count"`

	// SF's four buckets (lists capped at reportIssueURLCap, counts exact):
	// Added/Removed = page in both crawls entered/left the issue;
	// New/Missing = page only in one crawl.
	AddedCount   int      `json:"added_count"`
	NewCount     int      `json:"new_count"`
	RemovedCount int      `json:"removed_count"`
	MissingCount int      `json:"missing_count"`
	Added        []string `json:"added,omitempty"`
	New          []string `json:"new,omitempty"`
	Removed      []string `json:"removed,omitempty"`
	Missing      []string `json:"missing,omitempty"`
}

// BuildReport runs the comparison and decorates the result into a Report.
// PrevID/CurrID/ComputedAt are provenance the caller stamps (CachedReport
// does); everything else is derived from the two inputs.
func BuildReport(prev, curr Input, cfg *config.Config) (*Report, error) {
	res, err := Run(prev, curr, cfg)
	if err != nil {
		return nil, err
	}

	r := &Report{Version: ReportVersion, PagesPrev: res.PagesPrevious, PagesCurr: res.PagesCurrent}
	r.StatusMixPrev, r.IndexablePrev, r.NonIndexablePrev = pageDistributions(prev.Pages)
	r.StatusMixCurr, r.IndexableCurr, r.NonIndexableCurr = pageDistributions(curr.Pages)

	// Missing pages are reported under their MAPPED URL (Run rewrites previous
	// URLs through compare.url_mapping), so brief lookups need the same view.
	mapURL, err := NewURLMapper(cfg.Compare.URLMapping)
	if err != nil {
		return nil, err
	}
	prevByMapped := make(map[string]*crawler.PageRecord, len(prev.Pages))
	for u, rec := range prev.Pages {
		prevByMapped[mapURL(u)] = rec
	}

	r.NewCount = len(res.NewPages)
	r.MissingCount = len(res.MissingPages)
	r.NewPages = pageBriefs(res.NewPages, curr.Pages, reportPageCap)
	r.MissingPages = pageBriefs(res.MissingPages, prevByMapped, reportPageCap)

	r.StateChangeCount = len(res.StateChanges)
	for _, s := range res.StateChanges {
		if s.PrevStatus != s.CurrStatus {
			r.StatusFlipCount++
		}
		if s.PrevIndexable != s.CurrIndexable {
			r.IndexFlipCount++
		}
	}
	r.StateChanges = res.StateChanges
	if len(r.StateChanges) > reportChangeCap {
		r.StateChanges = r.StateChanges[:reportChangeCap]
	}
	r.ElementChangeCount = len(res.Changes)
	r.ElementChanges = res.Changes
	if len(r.ElementChanges) > reportChangeCap {
		r.ElementChanges = r.ElementChanges[:reportChangeCap]
	}

	for _, d := range res.Deltas {
		v := IssueMovement{
			ID: d.IssueID, Name: d.IssueID, Severity: string(issues.Warning),
			PrevCount: len(prev.Issues[d.IssueID]), CurrCount: len(curr.Issues[d.IssueID]),
			AddedCount: len(d.Added), NewCount: len(d.New),
			RemovedCount: len(d.Removed), MissingCount: len(d.Missing),
			Added: capURLs(d.Added), New: capURLs(d.New),
			Removed: capURLs(d.Removed), Missing: capURLs(d.Missing),
		}
		if def, ok := issues.Lookup(d.IssueID); ok {
			v.Name, v.Severity = def.Name, string(def.Severity)
		}
		r.IssueDeltas = append(r.IssueDeltas, v)
	}
	// canonical order: severity first, then how much the issue moved
	sevRank := map[string]int{string(issues.Issue): 0, string(issues.Warning): 1, string(issues.Opportunity): 2}
	sort.SliceStable(r.IssueDeltas, func(i, j int) bool {
		a, b := r.IssueDeltas[i], r.IssueDeltas[j]
		if sevRank[a.Severity] != sevRank[b.Severity] {
			return sevRank[a.Severity] < sevRank[b.Severity]
		}
		am, bm := absInt(a.CurrCount-a.PrevCount), absInt(b.CurrCount-b.PrevCount)
		if am != bm {
			return am > bm
		}
		return a.Name < b.Name
	})
	return r, nil
}

// CachedReport returns the pair's report, serving the comparisons cache when
// the pair was compared before (Cached=true, ComputedAt = the row's age) and
// building + caching it otherwise. force skips the cache read; the fresh
// report still replaces the cached row. load overrides how a crawl id becomes
// an Input (nil = LoadInput) — the desktop passes its page-cache-backed loader.
func CachedReport(dir, prevID, currID string, force bool, load func(string) (Input, error)) (*Report, error) {
	if !force {
		if raw, created, err := store.GetComparison(dir, prevID, currID); err == nil && raw != nil {
			var r Report
			if json.Unmarshal(raw, &r) == nil && r.Version == ReportVersion {
				r.Cached = true
				r.ComputedAt = created.Format("2006-01-02 15:04")
				return &r, nil
			}
		}
	}
	if load == nil {
		load = func(id string) (Input, error) { return LoadInput(dir, id) }
	}
	prev, err := load(prevID)
	if err != nil {
		return nil, err
	}
	curr, err := load(currID)
	if err != nil {
		return nil, err
	}
	// the current crawl's frozen config drives the comparison
	cfg, err := LoadCrawlConfig(dir, currID)
	if err != nil {
		return nil, err
	}
	r, err := BuildReport(prev, curr, cfg)
	if err != nil {
		return nil, err
	}
	r.PrevID, r.CurrID = prevID, currID
	r.ComputedAt = time.Now().Format("2006-01-02 15:04")
	if raw, err := json.Marshal(r); err == nil {
		// best-effort: an unsaveable cache row just means recomputing next time
		_ = store.SaveComparison(dir, prevID, currID, raw)
	}
	return r, nil
}

// LoadInput reads one stored crawl into a comparison Input: its pages plus
// the URL list of every triggered issue.
func LoadInput(dir, id string) (Input, error) {
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		return Input{}, err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return Input{}, err
	}
	counts, err := st.IssueCounts()
	if err != nil {
		return Input{}, err
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
	return Input{Pages: pages, Issues: iss}, nil
}

// LoadCrawlConfig loads a crawl's frozen config.
func LoadCrawlConfig(dir, id string) (*config.Config, error) {
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return nil, err
	}
	return config.Load([]byte(cfgYAML))
}

// pageDistributions buckets one crawl's pages the way the desktop overview
// does: status mix keyed 2xx/3xx/4xx/5xx/blocked/noresp, and indexability
// counted over internal URLs only.
func pageDistributions(pages map[string]*crawler.PageRecord) (mix map[string]int, indexable, nonIndexable int) {
	mix = map[string]int{}
	for _, pg := range pages {
		if pg.Scope == "internal" {
			if pg.Indexable {
				indexable++
			} else {
				nonIndexable++
			}
		}
		switch pg.State {
		case crawler.StateBlockedRobots:
			mix["blocked"]++
			continue
		case crawler.StateError:
			mix["noresp"]++
			continue
		}
		switch {
		case pg.StatusCode >= 500:
			mix["5xx"]++
		case pg.StatusCode >= 400:
			mix["4xx"]++
		case pg.StatusCode >= 300:
			mix["3xx"]++
		case pg.StatusCode >= 200:
			mix["2xx"]++
		}
	}
	return mix, indexable, nonIndexable
}

func pageBriefs(urls []string, pages map[string]*crawler.PageRecord, limit int) []PageBrief {
	out := make([]PageBrief, 0, min(len(urls), limit))
	for _, u := range urls {
		if len(out) == limit {
			break
		}
		b := PageBrief{URL: u}
		if rec, ok := pages[u]; ok {
			b.Status, b.Indexable = rec.StatusCode, rec.Indexable
			if rec.Facts != nil && len(rec.Facts.Titles) > 0 {
				b.Title = rec.Facts.Titles[0]
			}
		}
		out = append(out, b)
	}
	return out
}

func capURLs(urls []string) []string {
	if len(urls) <= reportIssueURLCap {
		return urls
	}
	return urls[:reportIssueURLCap]
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
