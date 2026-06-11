// Package export builds tabular datasets from a stored crawl — the tab
// exports, bulk link/issue exports, and named reports — and writes them as
// CSV, JSON, JSONL or XLSX. Every dataset is discoverable (List/Reports),
// and any tab can be restricted to the URLs affected by one issue, which is
// how Screaming Frog's per-filter exports map onto our issue IDs.
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/issues"
	"github.com/hhsecond/acrawler/internal/serpwidth"
	"github.com/hhsecond/acrawler/internal/store"
	"github.com/xuri/excelize/v2"
)

// Dataset is one exportable table.
type Dataset struct {
	Name   string
	Header []string
	Rows   [][]string
}

var tabs = []string{
	"internal", "external", "response_codes", "titles", "descriptions",
	"h1", "canonicals", "hreflang", "images", "security", "issues", "links",
	"custom",
}

var reports = []string{
	"crawl_overview", "redirect_chains", "canonical_chains",
	"insecure_content", "orphan_pages", "crawl_paths",
}

// List returns the exportable tab names.
func List() []string { return tabs }

// Reports returns the report names.
func Reports() []string { return reports }

// Build constructs a tab dataset, optionally restricted to URLs affected by
// one issue id.
func Build(st *store.Crawl, name, filterIssue string) (*Dataset, error) {
	if !slices.Contains(tabs, name) {
		return nil, fmt.Errorf("unknown export %q (try: %s)", name, strings.Join(tabs, ", "))
	}
	switch name {
	case "issues":
		return buildIssues(st)
	case "links":
		return buildLinks(st)
	case "custom":
		return buildCustom(st)
	}

	pages, err := st.LoadPages()
	if err != nil {
		return nil, err
	}
	var only map[string]bool
	if filterIssue != "" {
		urls, err := st.IssueURLs(filterIssue)
		if err != nil {
			return nil, err
		}
		only = map[string]bool{}
		for _, u := range urls {
			only[u] = true
		}
	}
	d := &Dataset{Name: name}
	for _, url := range sortedURLs(pages) {
		rec := pages[url]
		if only != nil && !only[url] {
			continue
		}
		if row, ok := tabRow(name, rec); ok {
			d.Rows = append(d.Rows, row)
		}
	}
	d.Header = tabHeader(name)
	return d, nil
}

func sortedURLs(pages map[string]*crawler.PageRecord) []string {
	urls := make([]string, 0, len(pages))
	for u := range pages {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls
}

func tabHeader(name string) []string {
	switch name {
	case "internal", "external":
		return []string{"url", "status_code", "status", "content_type", "http_version",
			"indexability", "indexability_status", "depth", "inlinks", "unique_inlinks",
			"unique_outlinks", "link_score", "response_time_ms", "size", "title",
			"meta_description", "h1", "word_count", "canonical", "redirect_url", "redirect_type"}
	case "response_codes":
		return []string{"url", "scope", "state", "status_code", "status", "redirect_url",
			"redirect_type", "fetch_error"}
	case "titles":
		return []string{"url", "title", "length", "pixel_width", "count", "indexability_status"}
	case "descriptions":
		return []string{"url", "description", "length", "pixel_width", "count", "indexability_status"}
	case "h1":
		return []string{"url", "h1", "length", "count", "indexability_status"}
	case "canonicals":
		return []string{"url", "canonical", "count", "indexability", "indexability_status"}
	case "hreflang":
		return []string{"url", "lang", "target"}
	case "images":
		return []string{"url", "content_type", "size_kb", "inlinks"}
	case "security":
		return []string{"url", "scheme", "status_code", "indexability_status"}
	}
	return nil
}

func tabRow(name string, rec *crawler.PageRecord) ([]string, bool) {
	f := rec.Facts
	first := func(values []string) string {
		if len(values) > 0 {
			return values[0]
		}
		return ""
	}
	indexability := "non-indexable"
	if rec.Indexable {
		indexability = "indexable"
	}
	switch name {
	case "internal", "external":
		if (name == "internal") != (rec.Scope == "internal") {
			return nil, false
		}
		title, desc, h1, canonical := "", "", "", ""
		wordCount := 0
		if f != nil {
			title, desc, h1 = first(f.Titles), first(f.Descriptions), first(f.H1s)
			canonical = first(f.CanonicalHTML)
			wordCount = f.WordCount
		}
		return []string{rec.URL, itoa(rec.StatusCode), rec.Status, rec.ContentType,
			rec.HTTPVersion, indexability, rec.IndexabilityStatus, itoa(rec.Depth),
			itoa(rec.Inlinks), itoa(rec.UniqueInlinks), itoa(rec.UniqueOutlinks),
			fmt.Sprintf("%.1f", rec.LinkScore), itoa(int(rec.ResponseTimeMs)),
			itoa(rec.Size), title, desc, h1, itoa(wordCount), canonical,
			rec.RedirectURL, rec.RedirectType}, true
	case "response_codes":
		return []string{rec.URL, rec.Scope, rec.State, itoa(rec.StatusCode), rec.Status,
			rec.RedirectURL, rec.RedirectType, rec.FetchError}, true
	case "titles":
		if f == nil {
			return nil, false
		}
		title := first(f.Titles)
		return []string{rec.URL, title, itoa(len([]rune(title))),
			itoa(serpwidth.Title(title)), itoa(len(f.Titles)), rec.IndexabilityStatus}, true
	case "descriptions":
		if f == nil {
			return nil, false
		}
		desc := first(f.Descriptions)
		return []string{rec.URL, desc, itoa(len([]rune(desc))),
			itoa(serpwidth.Description(desc)), itoa(len(f.Descriptions)), rec.IndexabilityStatus}, true
	case "h1":
		if f == nil {
			return nil, false
		}
		return []string{rec.URL, first(f.H1s), itoa(len([]rune(first(f.H1s)))),
			itoa(len(f.H1s)), rec.IndexabilityStatus}, true
	case "canonicals":
		if f == nil {
			return nil, false
		}
		all := len(f.CanonicalHTML) + len(f.CanonicalHTTP)
		canonical := first(f.CanonicalHTML)
		if canonical == "" {
			canonical = first(f.CanonicalHTTP)
		}
		return []string{rec.URL, canonical, itoa(all), indexability, rec.IndexabilityStatus}, true
	case "images":
		if !strings.HasPrefix(rec.ContentType, "image/") {
			return nil, false
		}
		return []string{rec.URL, rec.ContentType, itoa(rec.Size / 1024), itoa(rec.Inlinks)}, true
	case "security":
		if rec.Scope != "internal" || f == nil {
			return nil, false
		}
		scheme := "https"
		if strings.HasPrefix(rec.URL, "http://") {
			scheme = "http"
		}
		return []string{rec.URL, scheme, itoa(rec.StatusCode), rec.IndexabilityStatus}, true
	case "hreflang":
		return nil, false // expanded separately (multi-row)
	}
	return nil, false
}

// BuildHreflang expands one row per hreflang annotation.
func buildHreflang(pages map[string]*crawler.PageRecord) *Dataset {
	d := &Dataset{Name: "hreflang", Header: tabHeader("hreflang")}
	for _, url := range sortedURLs(pages) {
		rec := pages[url]
		if rec.Facts == nil {
			continue
		}
		for _, h := range rec.Facts.HreflangHTML {
			d.Rows = append(d.Rows, []string{url, h.Lang, h.URL})
		}
		for _, h := range rec.Facts.HreflangHTTP {
			d.Rows = append(d.Rows, []string{url, h.Lang, h.URL})
		}
	}
	return d
}

func buildIssues(st *store.Crawl) (*Dataset, error) {
	rows, err := st.DB().Query(`SELECT url, issue, detail FROM issues ORDER BY issue, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Dataset{Name: "issues", Header: []string{"url", "issue", "severity", "priority", "tab", "detail"}}
	for rows.Next() {
		var url, id, detail string
		if err := rows.Scan(&url, &id, &detail); err != nil {
			return nil, err
		}
		def, _ := issues.Lookup(id)
		d.Rows = append(d.Rows, []string{url, id, string(def.Severity), string(def.Priority), def.Tab, detail})
	}
	return d, rows.Err()
}

func buildLinks(st *store.Crawl) (*Dataset, error) {
	rows, err := st.DB().Query(`SELECT src, dst, type, anchor, alt, nofollow, rel,
		target, path_type, position FROM links ORDER BY src, dst`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Dataset{Name: "links", Header: []string{"source", "destination", "type", "anchor",
		"alt", "nofollow", "rel", "target", "path_type", "position"}}
	for rows.Next() {
		var src, dst, typ, anchor, alt, rel, target, pathType, position string
		var nofollow int
		if err := rows.Scan(&src, &dst, &typ, &anchor, &alt, &nofollow, &rel,
			&target, &pathType, &position); err != nil {
			return nil, err
		}
		d.Rows = append(d.Rows, []string{src, dst, typ, anchor, alt,
			strconv.FormatBool(nofollow == 1), rel, target, pathType, position})
	}
	return d, rows.Err()
}

func buildCustom(st *store.Crawl) (*Dataset, error) {
	rows, err := st.DB().Query(`SELECT url, kind, name, value FROM custom_results ORDER BY name, url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Dataset{Name: "custom", Header: []string{"url", "kind", "name", "value"}}
	for rows.Next() {
		var url, kind, name, value string
		if err := rows.Scan(&url, &kind, &name, &value); err != nil {
			return nil, err
		}
		d.Rows = append(d.Rows, []string{url, kind, name, value})
	}
	return d, rows.Err()
}

// BuildReport constructs a named report dataset.
func BuildReport(st *store.Crawl, name string) (*Dataset, error) {
	switch name {
	case "crawl_overview":
		return reportOverview(st)
	case "redirect_chains", "canonical_chains":
		return reportChains(st, strings.TrimSuffix(name, "_chains"))
	case "insecure_content":
		return reportByIssuePrefix(st, "insecure_content", "security_")
	case "orphan_pages":
		return reportByIssuePrefix(st, "orphan_pages", "sitemap_orphan")
	case "crawl_paths":
		return reportCrawlPaths(st)
	}
	return nil, fmt.Errorf("unknown report %q (try: %s)", name, strings.Join(reports, ", "))
}

func reportOverview(st *store.Crawl) (*Dataset, error) {
	pages, err := st.LoadPages()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, rec := range pages {
		counts["total"]++
		counts["scope:"+rec.Scope]++
		counts["state:"+rec.State]++
		if rec.StatusCode > 0 {
			counts[fmt.Sprintf("status:%dxx", rec.StatusCode/100)]++
		}
		if rec.State == crawler.StateCrawled {
			if rec.Indexable {
				counts["indexable"]++
			} else {
				counts["non-indexable"]++
			}
		}
	}
	issueCounts, err := st.IssueCounts()
	if err != nil {
		return nil, err
	}
	for id, n := range issueCounts {
		counts["issue:"+id] = n
	}
	d := &Dataset{Name: "crawl_overview", Header: []string{"metric", "count"}}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.Rows = append(d.Rows, []string{k, itoa(counts[k])})
	}
	return d, nil
}

func reportChains(st *store.Crawl, typ string) (*Dataset, error) {
	chains, err := st.Chains()
	if err != nil {
		return nil, err
	}
	d := &Dataset{Name: typ + "_chains",
		Header: []string{"source", "hops", "chain", "final", "final_status", "loop"}}
	for _, c := range chains {
		if c.Type != typ {
			continue
		}
		d.Rows = append(d.Rows, []string{c.Source, itoa(len(c.Hops)),
			strings.Join(c.Hops, " -> "), c.Final, itoa(c.FinalStatus),
			strconv.FormatBool(c.Loop)})
	}
	return d, nil
}

// reportCrawlPaths reconstructs each URL's discovery path by walking the
// discovered_from edges back to the seed (SF's crawl path report). Walks are
// cycle-safe and capped at 25 hops; a parent that was never stored still
// appears in the path (it is the only known edge) but ends the walk.
func reportCrawlPaths(st *store.Crawl) (*Dataset, error) {
	pages, err := st.LoadPages()
	if err != nil {
		return nil, err
	}
	d := &Dataset{Name: "crawl_paths", Header: []string{"url", "hops", "path"}}
	for _, url := range sortedURLs(pages) {
		chain := []string{url}
		seen := map[string]bool{url: true}
		current := url
		for len(chain) <= 25 {
			rec, ok := pages[current]
			if !ok {
				break // unknown parent: already in the chain, nothing to follow
			}
			parent := rec.DiscoveredFrom
			if parent == "" || seen[parent] {
				break
			}
			seen[parent] = true
			chain = append([]string{parent}, chain...)
			current = parent
		}
		d.Rows = append(d.Rows, []string{url, itoa(len(chain) - 1), strings.Join(chain, " -> ")})
	}
	return d, nil
}

func reportByIssuePrefix(st *store.Crawl, name, prefix string) (*Dataset, error) {
	rows, err := st.DB().Query(`SELECT url, issue, detail FROM issues WHERE issue LIKE ? ORDER BY url`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Dataset{Name: name, Header: []string{"url", "issue", "detail"}}
	for rows.Next() {
		var url, id, detail string
		if err := rows.Scan(&url, &id, &detail); err != nil {
			return nil, err
		}
		d.Rows = append(d.Rows, []string{url, id, detail})
	}
	return d, rows.Err()
}

// BuildAny resolves tab or hreflang datasets uniformly.
func BuildAny(st *store.Crawl, name, filterIssue string) (*Dataset, error) {
	if name == "hreflang" {
		pages, err := st.LoadPages()
		if err != nil {
			return nil, err
		}
		return buildHreflang(pages), nil
	}
	return Build(st, name, filterIssue)
}

// Write serializes a dataset: csv, json (array of objects), or jsonl.
func Write(d *Dataset, format string, w io.Writer) error {
	switch format {
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(d.Header); err != nil {
			return err
		}
		if err := cw.WriteAll(d.Rows); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	case "json", "jsonl":
		enc := json.NewEncoder(w)
		objs := make([]map[string]string, 0, len(d.Rows))
		for _, row := range d.Rows {
			obj := make(map[string]string, len(d.Header))
			for i, col := range d.Header {
				if i < len(row) {
					obj[col] = row[i]
				}
			}
			if format == "jsonl" {
				if err := enc.Encode(obj); err != nil {
					return err
				}
				continue
			}
			objs = append(objs, obj)
		}
		if format == "json" {
			enc.SetIndent("", "  ")
			return enc.Encode(objs)
		}
		return nil
	}
	return fmt.Errorf("unknown format %q (csv, json, jsonl, xlsx)", format)
}

// WriteXLSX writes the dataset as an Excel workbook.
func WriteXLSX(d *Dataset, path string) error {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for col, name := range d.Header {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		f.SetCellValue(sheet, cell, name)
	}
	for r, row := range d.Rows {
		for col, value := range row {
			cell, _ := excelize.CoordinatesToCellName(col+1, r+2)
			f.SetCellValue(sheet, cell, value)
		}
	}
	return f.SaveAs(path)
}

func itoa(n int) string { return strconv.Itoa(n) }
