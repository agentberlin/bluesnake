package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/export"
	"github.com/hhsecond/acrawler/internal/issues"
	"github.com/hhsecond/acrawler/internal/sitemapgen"
	"github.com/hhsecond/acrawler/internal/store"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------------------------------------------------------------------------
// shared caches

func (a *App) loadPages(id string) (map[string]*crawler.PageRecord, error) {
	a.cacheMu.Lock()
	if p, ok := a.pagesCache[id]; ok {
		a.cacheMu.Unlock()
		return p, nil
	}
	a.cacheMu.Unlock()

	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return nil, err
	}
	a.cacheMu.Lock()
	a.pagesCache[id] = pages
	a.cacheMu.Unlock()
	return pages, nil
}

// urlIssues maps url -> issue ids, built once per crawl.
func (a *App) urlIssues(id string) (map[string][]string, error) {
	a.cacheMu.Lock()
	if m, ok := a.issueCache[id]; ok {
		a.cacheMu.Unlock()
		return m, nil
	}
	a.cacheMu.Unlock()

	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return nil, err
	}
	m := map[string][]string{}
	for issueID, n := range counts {
		if n == 0 {
			continue
		}
		urls, err := st.IssueURLs(issueID)
		if err != nil {
			continue
		}
		for _, u := range urls {
			m[u] = append(m[u], issueID)
		}
	}
	a.cacheMu.Lock()
	a.issueCache[id] = m
	a.cacheMu.Unlock()
	return m, nil
}

// ---------------------------------------------------------------------------
// datasets (results tables)

type DatasetPayload struct {
	Name      string     `json:"name"`
	Header    []string   `json:"header"`
	Rows      [][]string `json:"rows"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated"`
}

// Dataset builds one results tab. filterIssue restricts rows to URLs affected
// by that issue id; limit caps the rows sent over the bridge.
func (a *App) Dataset(id, tab, filterIssue string, limit int) (*DatasetPayload, error) {
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	d, err := export.BuildAny(st, tab, filterIssue)
	if err != nil {
		return nil, err
	}
	p := &DatasetPayload{Name: d.Name, Header: d.Header, Rows: d.Rows, Total: len(d.Rows)}
	if limit > 0 && len(p.Rows) > limit {
		p.Rows = p.Rows[:limit]
		p.Truncated = true
	}
	return p, nil
}

// DatasetCounts returns the row count of every tab (the dataset rail badges).
func (a *App) DatasetCounts(id string) (map[string]int, error) {
	a.cacheMu.Lock()
	if m, ok := a.countCache[id]; ok {
		a.cacheMu.Unlock()
		return m, nil
	}
	a.cacheMu.Unlock()

	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	m := map[string]int{}
	for _, tab := range export.List() {
		d, err := export.BuildAny(st, tab, "")
		if err != nil {
			continue
		}
		m[tab] = len(d.Rows)
	}
	a.cacheMu.Lock()
	a.countCache[id] = m
	a.cacheMu.Unlock()
	return m, nil
}

// ---------------------------------------------------------------------------
// overview

type IssueEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Priority string `json:"priority"`
	Count    int    `json:"count"`
}

type Overview struct {
	ID            string       `json:"id"`
	Seed          string       `json:"seed"`
	Total         int          `json:"total"`
	Internal      int          `json:"internal"`
	External      int          `json:"external"`
	S2xx          int          `json:"s2xx"`
	S3xx          int          `json:"s3xx"`
	S4xx          int          `json:"s4xx"`
	S5xx          int          `json:"s5xx"`
	Blocked       int          `json:"blocked"`
	NoResp        int          `json:"noresp"`
	Indexable     int          `json:"indexable"`
	NonIndexable  int          `json:"nonIndexable"`
	AvgLinkScore  float64      `json:"avgLinkScore"`
	Issues        int          `json:"issues"`
	Warnings      int          `json:"warnings"`
	Opportunities int          `json:"opportunities"`
	TopIssues     []IssueEntry `json:"topIssues"`
}

func (a *App) Overview(id string) (*Overview, error) {
	pages, err := a.loadPages(id)
	if err != nil {
		return nil, err
	}
	o := &Overview{ID: id}
	var scoreSum float64
	var scoreN int
	for _, p := range pages {
		o.Total++
		if p.Scope == "internal" {
			o.Internal++
		} else {
			o.External++
		}
		switch p.State {
		case crawler.StateBlockedRobots:
			o.Blocked++
			continue
		case crawler.StateError:
			o.NoResp++
			continue
		}
		switch {
		case p.StatusCode >= 500:
			o.S5xx++
		case p.StatusCode >= 400:
			o.S4xx++
		case p.StatusCode >= 300:
			o.S3xx++
		case p.StatusCode >= 200:
			o.S2xx++
		}
		if p.Scope == "internal" {
			if p.Indexable {
				o.Indexable++
			} else {
				o.NonIndexable++
			}
			if p.LinkScore > 0 {
				scoreSum += p.LinkScore
				scoreN++
			}
		}
	}
	if scoreN > 0 {
		o.AvgLinkScore = scoreSum / float64(scoreN)
	}

	groups, err := a.IssueSummary(id)
	if err != nil {
		return nil, err
	}
	var all []IssueEntry
	for _, g := range groups {
		for _, it := range g.Items {
			switch issues.Severity(it.Severity) {
			case issues.Issue:
				o.Issues += it.Count
			case issues.Warning:
				o.Warnings += it.Count
			case issues.Opportunity:
				o.Opportunities += it.Count
			}
			if it.Count > 0 {
				all = append(all, it)
			}
		}
	}
	sevRank := map[string]int{string(issues.Issue): 0, string(issues.Warning): 1, string(issues.Opportunity): 2}
	sort.SliceStable(all, func(i, j int) bool {
		if sevRank[all[i].Severity] != sevRank[all[j].Severity] {
			return sevRank[all[i].Severity] < sevRank[all[j].Severity]
		}
		return all[i].Count > all[j].Count
	})
	if len(all) > 8 {
		all = all[:8]
	}
	o.TopIssues = all
	if seed := seedOf(pages); seed != "" {
		o.Seed = seed
	}
	return o, nil
}

// discoveryPath walks discovered_from edges back to the seed (seed first,
// url last). The seen set makes it cycle-safe, so it walks the full depth.
func discoveryPath(pages map[string]*crawler.PageRecord, url string) []string {
	chain := []string{url}
	seen := map[string]bool{url: true}
	current := url
	for {
		rec, ok := pages[current]
		if !ok {
			break
		}
		parent := rec.DiscoveredFrom
		if parent == "" || seen[parent] {
			break
		}
		seen[parent] = true
		chain = append([]string{parent}, chain...)
		current = parent
	}
	return chain
}

func seedOf(pages map[string]*crawler.PageRecord) string {
	for u, p := range pages {
		if p.Depth == 0 && p.Scope == "internal" {
			return u
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// issues browser

type IssueGroup struct {
	Category string       `json:"category"`
	Items    []IssueEntry `json:"items"`
}

// IssueSummary returns the full check catalogue with live counts (zero counts
// included — the UI renders them as "passed").
func (a *App) IssueSummary(id string) ([]IssueGroup, error) {
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return nil, err
	}
	var groups []IssueGroup
	byCat := map[string]int{}
	for _, def := range issues.Catalogue() {
		cat := def.Tab
		idx, ok := byCat[cat]
		if !ok {
			groups = append(groups, IssueGroup{Category: cat})
			idx = len(groups) - 1
			byCat[cat] = idx
		}
		groups[idx].Items = append(groups[idx].Items, IssueEntry{
			ID: def.ID, Name: def.Name, Category: cat,
			Severity: string(def.Severity), Priority: string(def.Priority),
			Count: counts[def.ID],
		})
	}
	return groups, nil
}

// ---------------------------------------------------------------------------
// per-URL detail drawer

type LinkRef struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Anchor   string `json:"anchor"`
	Type     string `json:"type"`
	Position string `json:"position"`
	Nofollow bool   `json:"nofollow"`
	Origin   string `json:"origin"`
}

type PageDetail struct {
	URL               string            `json:"url"`
	StatusCode        int               `json:"statusCode"`
	Status            string            `json:"status"`
	State             string            `json:"state"`
	ContentType       string            `json:"contentType"`
	HTTPVersion       string            `json:"httpVersion"`
	Indexable         bool              `json:"indexable"`
	IndexabilityState string            `json:"indexabilityStatus"`
	Depth             int               `json:"depth"`
	ResponseTimeMs    int64             `json:"responseTimeMs"`
	SizeKB            int               `json:"sizeKB"`
	WordCount         int               `json:"wordCount"`
	LinkScore         float64           `json:"linkScore"`
	Inlinks           int               `json:"inlinksCount"`
	UniqueInlinks     int               `json:"uniqueInlinks"`
	UniqueOutlinks    int               `json:"uniqueOutlinks"`
	Title             string            `json:"title"`
	Description       string            `json:"description"`
	H1                string            `json:"h1"`
	Canonical         string            `json:"canonical"`
	RedirectURL       string            `json:"redirectUrl"`
	RedirectType      string            `json:"redirectType"`
	RobotsLine        int               `json:"robotsLine"`
	Similarity        float64           `json:"similarity"`
	DiscoveredFrom    string            `json:"discoveredFrom"`
	DiscoveryPath     []string          `json:"discoveryPath"`
	FetchError        string            `json:"fetchError"`
	Headers           map[string]string `json:"headers"`
	Issues            []IssueEntry      `json:"issues"`
	InlinkRefs        []LinkRef         `json:"inlinkRefs"`
	OutlinkRefs       []LinkRef         `json:"outlinkRefs"`
}

func (a *App) PageDetail(id, pageURL string) (*PageDetail, error) {
	pages, err := a.loadPages(id)
	if err != nil {
		return nil, err
	}
	p, ok := pages[pageURL]
	if !ok {
		return nil, fmt.Errorf("URL not found in crawl: %s", pageURL)
	}
	d := &PageDetail{
		URL: p.URL, StatusCode: p.StatusCode, Status: p.Status, State: p.State,
		ContentType: p.ContentType, HTTPVersion: p.HTTPVersion,
		Indexable: p.Indexable, IndexabilityState: p.IndexabilityStatus,
		Depth: p.Depth, ResponseTimeMs: p.ResponseTimeMs, SizeKB: p.Size / 1024,
		LinkScore: p.LinkScore, Inlinks: p.Inlinks,
		UniqueInlinks: p.UniqueInlinks, UniqueOutlinks: p.UniqueOutlinks,
		RedirectURL: p.RedirectURL, RedirectType: p.RedirectType,
		RobotsLine: p.MatchedRobotsLine, Similarity: p.ClosestSimilarity,
		DiscoveredFrom: p.DiscoveredFrom, DiscoveryPath: discoveryPath(pages, p.URL),
		FetchError: p.FetchError,
		Headers:    p.Headers,
	}
	if p.Facts != nil {
		f := p.Facts
		if len(f.Titles) > 0 {
			d.Title = f.Titles[0]
		}
		if len(f.Descriptions) > 0 {
			d.Description = f.Descriptions[0]
		}
		if len(f.H1s) > 0 {
			d.H1 = f.H1s[0]
		}
		if len(f.CanonicalHTML) > 0 {
			d.Canonical = f.CanonicalHTML[0]
		} else if len(f.CanonicalHTTP) > 0 {
			d.Canonical = f.CanonicalHTTP[0]
		}
		d.WordCount = f.WordCount
		for _, l := range f.Links {
			if l.Type != "hyperlink" || l.URL == "" {
				continue
			}
			d.OutlinkRefs = append(d.OutlinkRefs, LinkRef{
				From: p.URL, To: l.URL, Anchor: l.Anchor, Type: string(l.Type),
				Position: l.Position, Nofollow: l.Nofollow, Origin: l.Origin,
			})
			if len(d.OutlinkRefs) >= 60 {
				break
			}
		}
	}
	// inlinks: scan stored pages for hyperlinks pointing here (capped)
	for from, rec := range pages {
		if rec.Facts == nil || from == p.URL {
			continue
		}
		for _, l := range rec.Facts.Links {
			if l.URL == p.URL && l.Type == "hyperlink" {
				d.InlinkRefs = append(d.InlinkRefs, LinkRef{
					From: from, To: p.URL, Anchor: l.Anchor, Type: string(l.Type),
					Position: l.Position, Nofollow: l.Nofollow, Origin: l.Origin,
				})
				break
			}
		}
		if len(d.InlinkRefs) >= 60 {
			break
		}
	}

	urlIss, err := a.urlIssues(id)
	if err == nil {
		for _, issueID := range urlIss[p.URL] {
			if def, ok := issues.Lookup(issueID); ok {
				d.Issues = append(d.Issues, IssueEntry{
					ID: def.ID, Name: def.Name, Category: def.Tab,
					Severity: string(def.Severity), Priority: string(def.Priority),
				})
			}
		}
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// export & sitemap

// ExportDataset opens a save dialog and writes the tab in the chosen format.
// Returns the written path ("" when the user cancels).
func (a *App) ExportDataset(id, tab, filterIssue, format string) (string, error) {
	name := tab
	if filterIssue != "" {
		name += "-" + filterIssue
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export " + tab,
		DefaultFilename: fmt.Sprintf("%s.%s", name, format),
	})
	if err != nil || path == "" {
		return "", err
	}
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return "", err
	}
	defer st.Close()
	d, err := export.BuildAny(st, tab, filterIssue)
	if err != nil {
		return "", err
	}
	if format == "xlsx" {
		return path, export.WriteXLSX(d, path)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return path, export.Write(d, format, f)
}

// GenerateSitemap writes sitemap.xml (and shards) to a chosen directory.
func (a *App) GenerateSitemap(id string) (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose a folder for sitemap.xml",
	})
	if err != nil || dir == "" {
		return "", err
	}
	pages, err := a.loadPages(id)
	if err != nil {
		return "", err
	}
	files, err := sitemapgen.Generate(pages, sitemapgen.Options{})
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// DatasetTabs lists the exportable tab names in display order.
func (a *App) DatasetTabs() []string { return export.List() }
