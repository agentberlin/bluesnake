package sitecheck

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// smNamespace is the required sitemaps.org XML namespace.
const smNamespace = "http://www.sitemaps.org/schemas/sitemap/0.9"

// SitemapOptions customizes a sitemap check.
type SitemapOptions struct {
	// FromRobots are sitemap URLs the caller already discovered in robots.txt
	// (resolved). They keep the robots-declared cross-host exemption exactly
	// as if this check had read the directives itself.
	FromRobots []string
	// Declared are other known sitemap URLs (sitemaps.urls config).
	Declared []string
	// RobotsChecked marks FromRobots as the authoritative robots.txt answer —
	// even when empty: discovery then never fetches robots.txt itself (the
	// crawl pass has already run the robots check).
	RobotsChecked bool
	// CheckEntries live-checks up to N listed page URLs across the report
	// (tool mode). 0 = none. Results are report-only, never findings — the
	// crawl's sitemap set-op analysis owns entry-level issues.
	CheckEntries int
}

// EntryCheck is one live-checked listed URL (tool mode).
type EntryCheck struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// SitemapFile is the audit of one fetched sitemap (or sitemap-index) file.
type SitemapFile struct {
	URL              string       `json:"url"`
	Source           string       `json:"source"` // robots | convention | declared | index
	DeclaredInRobots bool         `json:"via_robots,omitempty"`
	Status           int          `json:"status"`
	FetchError       string       `json:"fetch_error,omitempty"`
	ContentType      string       `json:"content_type,omitempty"`
	RawBytes         int          `json:"raw_bytes"`
	SizeBytes        int          `json:"size_bytes"` // uncompressed
	Truncated        bool         `json:"truncated,omitempty"`
	Gzip             bool         `json:"gzip,omitempty"`
	Kind             string       `json:"kind,omitempty"` // urlset | sitemapindex | invalid
	XMLError         string       `json:"xml_error,omitempty"`
	Entries          int          `json:"entries"`
	Children         []string     `json:"children,omitempty"`
	Duplicates       int          `json:"duplicate_entries,omitempty"`
	InvalidURLs      int          `json:"invalid_urls,omitempty"`
	InvalidURLEx     []string     `json:"invalid_url_examples,omitempty"`
	CrossHost        int          `json:"cross_host_urls,omitempty"`
	CrossHostEx      []string     `json:"cross_host_examples,omitempty"`
	InvalidLastmod   int          `json:"invalid_lastmod,omitempty"`
	InvalidLastmodEx []string     `json:"invalid_lastmod_examples,omitempty"`
	EntryChecks      []EntryCheck `json:"entry_checks,omitempty"`
}

// SitemapReport is the whole sitemap audit for a site (or one explicit file).
type SitemapReport struct {
	Site    string        `json:"site,omitempty"`
	Missing bool          `json:"missing"`
	Files   []SitemapFile `json:"files,omitempty"`
	Skipped []string      `json:"skipped,omitempty"` // children beyond the expansion bound
}

type smEntry struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod"`
}

type smSet struct {
	XMLName  xml.Name
	URLs     []smEntry `xml:"url"`
	Children []smEntry `xml:"sitemap"`
}

// Sitemaps audits a site's XML sitemaps. target is a site root (discovery
// mode: robots.txt Sitemap: directives, falling back to the /sitemap.xml and
// /sitemap_index.xml conventions) or an explicit sitemap URL. Index files are
// expanded recursively.
func (c *Checker) Sitemaps(ctx context.Context, target string, opts SitemapOptions) (*SitemapReport, error) {
	type rootRef struct {
		url, source string
		viaRobots   bool
	}
	var roots []rootRef
	seenRoot := map[string]bool{}
	addRoot := func(u, source string, viaRobots bool) {
		if u != "" && !seenRoot[u] {
			seenRoot[u] = true
			roots = append(roots, rootRef{u, source, viaRobots})
		}
	}

	rep := &SitemapReport{}
	target = strings.TrimSpace(target)
	explicitFile := ""
	if target != "" {
		root, err := siteRoot(target)
		if err != nil {
			return nil, err
		}
		rep.Site = root
		full := target
		if !strings.Contains(full, "://") {
			full = "https://" + full
		}
		if u, err := url.Parse(full); err == nil && u.Path != "" && u.Path != "/" {
			explicitFile = full
		}
	}

	for _, k := range opts.FromRobots {
		addRoot(k, "robots", true)
	}
	for _, k := range opts.Declared {
		addRoot(k, "declared", false)
	}
	if explicitFile != "" {
		addRoot(explicitFile, "declared", false)
	}

	walked := map[string]bool{}
	if explicitFile == "" && rep.Site != "" {
		if !opts.RobotsChecked {
			for _, sm := range c.robotsSitemaps(ctx, rep.Site) {
				addRoot(sm, "robots", true)
			}
		}
		if len(roots) == 0 {
			// Convention probes: only a hit becomes a file — a 404 at a
			// conventional path is not a declared-sitemap fetch error.
			for _, p := range []string{"/sitemap.xml", "/sitemap_index.xml"} {
				u := rep.Site + p
				f := c.fetchSitemapFile(ctx, u, "convention", false, &opts)
				if f.FetchError == "" && f.Status >= 200 && f.Status < 300 {
					walked[u] = true
					rep.Files = append(rep.Files, *f)
					c.expandIndex(ctx, f, 1, rep, walked, &opts)
				}
			}
			rep.Missing = len(rep.Files) == 0
			return rep, nil
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("sitecheck: no sitemap target")
	}

	for _, r := range roots {
		if walked[r.url] || len(rep.Files) >= maxSitemapFiles {
			if !walked[r.url] {
				rep.Skipped = append(rep.Skipped, r.url)
			}
			continue
		}
		walked[r.url] = true
		f := c.fetchSitemapFile(ctx, r.url, r.source, r.viaRobots, &opts)
		rep.Files = append(rep.Files, *f)
		c.expandIndex(ctx, f, 1, rep, walked, &opts)
	}
	return rep, nil
}

// robotsSitemaps fetches robots.txt (same hop-following as the robots check)
// purely for its Sitemap: directives, resolving relative values.
func (c *Checker) robotsSitemaps(ctx context.Context, site string) []string {
	rep, err := c.Robots(ctx, site, RobotsOptions{})
	if err != nil || !rep.Found {
		return nil
	}
	base, err := url.Parse(rep.FinalURL)
	if err != nil {
		return nil
	}
	var out []string
	for _, sm := range rep.Sitemaps {
		if resolved, err := base.Parse(strings.TrimSpace(sm)); err == nil {
			out = append(out, resolved.String())
		}
	}
	return out
}

// expandIndex walks a sitemap index's children depth-first up to
// sitemapIndexDepth, bounded by maxSitemapFiles across the whole report.
func (c *Checker) expandIndex(ctx context.Context, f *SitemapFile, depth int, rep *SitemapReport, walked map[string]bool, opts *SitemapOptions) {
	if f.Kind != "sitemapindex" || depth > sitemapIndexDepth {
		return
	}
	for _, child := range f.Children {
		if walked[child] {
			continue
		}
		if len(rep.Files) >= maxSitemapFiles {
			rep.Skipped = append(rep.Skipped, child)
			continue
		}
		walked[child] = true
		cf := c.fetchSitemapFile(ctx, child, "index", f.DeclaredInRobots, opts)
		rep.Files = append(rep.Files, *cf)
		c.expandIndex(ctx, cf, depth+1, rep, walked, opts)
	}
}

func (c *Checker) fetchSitemapFile(ctx context.Context, u, source string, viaRobots bool, opts *SitemapOptions) *SitemapFile {
	f := &SitemapFile{URL: u, Source: source, DeclaredInRobots: viaRobots}
	res := c.client.Fetch(ctx, u)
	f.Status, f.FetchError, f.ContentType = res.StatusCode, res.FetchError, res.ContentType
	if res.FetchError != "" || res.StatusCode < 200 || res.StatusCode >= 300 {
		return f
	}
	body := res.Body
	f.RawBytes = len(body)
	f.Truncated = res.Truncated
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		f.Gzip = true
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			body, err = io.ReadAll(io.LimitReader(zr, gunzipCap))
		}
		if err != nil {
			f.Kind, f.XMLError = "invalid", "gzip: "+err.Error()
			return f
		}
	}
	f.SizeBytes = len(body)

	var set smSet
	if err := xml.Unmarshal(body, &set); err != nil {
		f.Kind, f.XMLError = "invalid", err.Error()
		return f
	}
	switch set.XMLName.Local {
	case "urlset", "sitemapindex":
		f.Kind = set.XMLName.Local
	default:
		f.Kind, f.XMLError = "invalid", fmt.Sprintf("unexpected root element <%s> (want <urlset> or <sitemapindex>)", set.XMLName.Local)
		return f
	}
	if set.XMLName.Space != smNamespace {
		// The protocol requires the namespace; keep processing so the report
		// stays useful, but the file is still flagged invalid.
		f.XMLError = fmt.Sprintf("missing or wrong xmlns %q (want %q)", set.XMLName.Space, smNamespace)
	}

	fileHost := ""
	if fu, err := url.Parse(u); err == nil {
		fileHost = fu.Host
	}
	dups := map[string]bool{}
	for _, e := range set.URLs {
		loc := strings.TrimSpace(e.Loc)
		f.Entries++
		if dups[loc] {
			f.Duplicates++
		} else {
			dups[loc] = true
		}
		lu, err := url.Parse(loc)
		switch {
		case err != nil || lu.Host == "" || (lu.Scheme != "http" && lu.Scheme != "https"):
			f.InvalidURLs++
			addExample(&f.InvalidURLEx, loc)
		case !strings.EqualFold(lu.Host, fileHost):
			f.CrossHost++
			addExample(&f.CrossHostEx, loc)
		}
		if e.Lastmod != "" && !validLastmod(strings.TrimSpace(e.Lastmod)) {
			f.InvalidLastmod++
			addExample(&f.InvalidLastmodEx, e.Lastmod)
		}
		if opts.CheckEntries > 0 && len(f.EntryChecks) < opts.CheckEntries {
			ec := EntryCheck{URL: loc}
			r := c.client.Fetch(ctx, loc)
			ec.Status, ec.Error = r.StatusCode, r.FetchError
			f.EntryChecks = append(f.EntryChecks, ec)
		}
	}
	base, _ := url.Parse(u)
	for _, e := range set.Children {
		loc := strings.TrimSpace(e.Loc)
		if base != nil {
			if resolved, err := base.Parse(loc); err == nil {
				loc = resolved.String()
			}
		}
		f.Children = append(f.Children, loc)
	}
	return f
}

// lastmodLayouts are the W3C datetime profiles the sitemaps protocol allows.
var lastmodLayouts = []string{
	"2006-01-02",
	time.RFC3339,
	"2006-01-02T15:04Z07:00",
	"2006-01",
	"2006",
}

func validLastmod(v string) bool {
	for _, layout := range lastmodLayouts {
		if _, err := time.Parse(layout, v); err == nil {
			return true
		}
	}
	return false
}

// Findings derives the issue occurrences (DESIGN.md §5.10).
// File-level findings attach to the sitemap URL; sitemap_missing to the site
// root. Cross-host entries in a robots.txt-declared sitemap (or a child of
// one) are a legitimate cross-submission per sitemaps.org, so they stay
// report-only there.
func (r *SitemapReport) Findings() []Finding {
	var out []Finding
	if r.Missing {
		return append(out, Finding{IssueID: "sitemap_missing", URL: r.Site + "/"})
	}
	for i := range r.Files {
		f := &r.Files[i]
		add := func(id, detail string) {
			out = append(out, Finding{IssueID: id, URL: f.URL, Detail: detail})
		}
		switch {
		case f.FetchError != "":
			add("sitemap_fetch_error", f.FetchError)
		case f.Status < 200 || f.Status >= 300:
			add("sitemap_fetch_error", fmt.Sprintf("status %d", f.Status))
		default:
			if f.XMLError != "" {
				add("sitemap_invalid_xml", f.XMLError)
			}
			if f.Kind == "urlset" && f.Entries == 0 && f.XMLError == "" {
				add("sitemap_empty", "")
			}
			if f.Entries > maxSitemapURLs {
				add("sitemap_over_50k", fmt.Sprintf("%d URLs (protocol limit 50,000)", f.Entries))
			}
			if f.SizeBytes > maxSitemapBytes || f.Truncated {
				add("sitemap_over_50mb", fmt.Sprintf("%d bytes uncompressed (protocol limit %d)", f.SizeBytes, maxSitemapBytes))
			}
			if f.CrossHost > 0 && !f.DeclaredInRobots {
				add("sitemap_cross_host_urls", fmt.Sprintf("%d entries on other hosts (e.g. %s)", f.CrossHost, strings.Join(f.CrossHostEx, ", ")))
			}
			if f.InvalidLastmod > 0 {
				add("sitemap_invalid_lastmod", fmt.Sprintf("%d invalid lastmod values (e.g. %s)", f.InvalidLastmod, strings.Join(f.InvalidLastmodEx, ", ")))
			}
		}
	}
	return out
}
