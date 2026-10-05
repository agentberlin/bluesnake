package bundle

// Crawl-level stored data — the site-check reports and the llms.txt audit —
// rides on lines of its own between the header and the pages, counted in the
// header, and each page carries the sitemap entries that list it. Each is a
// stored value the crawl already kept, so the bundle's completeness rule (an
// omission is a bug) covers it.

import (
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
	"github.com/agentberlin/bluesnake/internal/store"
)

// storedCrawl is a crawl DB with one internal page and nothing else: the
// fixture for values written straight through the store's sink methods.
func storedCrawl(t *testing.T) *store.Crawl {
	t.Helper()
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	return c
}

// rawLines splits a bundle into its lines, as a consumer reads them off the wire.
func rawLines(raw []byte) []string {
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

// crawlLevelOf bundles a crawl and returns its header and crawl-level records.
func crawlLevelOf(t *testing.T, st *store.Crawl, info store.Info) (Header, []SiteCheck, []LlmsTxt, []byte) {
	t.Helper()
	_, _, raw := bundleOf(t, st, info, Options{})
	h, checks, files, _ := splitBundle(t, raw)
	return h, checks, files, raw
}

// Every report kind the site-check pass stores comes back out on a line of its
// own exactly as stored: written through the sink, bundled, then decoded into
// the check's own report type and compared with what went in.
func TestSiteCheckRecordCarriesEachReportVerbatim(t *testing.T) {
	for _, tt := range []struct {
		kind, subject string
		report        any
		decoded       func() any
	}{
		{sitecheck.KindRobots, "https://ex.test/robots.txt", &sitecheck.RobotsReport{
			Site: "https://ex.test", URL: "https://ex.test/robots.txt", Status: 200, Found: true,
			SizeBytes: 47, Groups: 2, Rules: 2, Sitemaps: []string{"https://ex.test/sitemap.xml"},
			Body: "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n",
		}, func() any { return &sitecheck.RobotsReport{} }},
		{sitecheck.KindSitemap, "https://ex.test", &sitecheck.SitemapReport{
			Site: "https://ex.test",
			Files: []sitecheck.SitemapFile{{URL: "https://ex.test/sitemap.xml", Source: "robots",
				Status: 200, Kind: "urlset", Entries: 3, InvalidLastmod: 1, InvalidLastmodEx: []string{"yesterday"}}},
		}, func() any { return &sitecheck.SitemapReport{} }},
		{sitecheck.KindAIBots, "https://ex.test/", &sitecheck.AIBotsReport{
			Site: "https://ex.test", URL: "https://ex.test/", RobotsFound: true, Live: true, ControlStatus: 200,
			Bots: []sitecheck.AIBotResult{
				{Bot: sitecheck.Bot{Name: "GPTBot", Operator: "OpenAI", Purpose: "training", RobotsToken: "GPTBot",
					UserAgent: "GPTBot/1.2", RespectsRobots: true}, BotVerdict: sitecheck.BotVerdict{RobotsLine: 2, RobotsRule: "Disallow: /",
					Probed: true, LiveStatus: 403, BlockedLive: true}},
				{Bot: sitecheck.Bot{Name: "Googlebot", Operator: "Google", Purpose: "search", RobotsToken: "Googlebot",
					RespectsRobots: true}, BotVerdict: sitecheck.BotVerdict{RobotsAllowed: true}},
			},
			Caveat: sitecheck.AIBotCaveat,
		}, func() any { return &sitecheck.AIBotsReport{} }},
		{sitecheck.KindRenderDiff, "https://ex.test/", &sitecheck.RenderDiffReport{
			URL: "https://ex.test/", FetchStatus: 200, Rendered: true, RawWordCount: 12, RenderedWordCount: 480,
			RenderedOnlyLinks: 1, RenderedOnlyLinkEx: []string{"https://ex.test/app"},
		}, func() any { return &sitecheck.RenderDiffReport{} }},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			c := storedCrawl(t)
			data, err := json.Marshal(tt.report)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.SiteCheck(crawler.SiteCheckRecord{Kind: tt.kind, Subject: tt.subject, Report: data}); err != nil {
				t.Fatal(err)
			}
			h, checks, _, raw := crawlLevelOf(t, c, store.Info{ID: c.ID})
			if h.SiteChecks != 1 || len(checks) != 1 {
				t.Fatalf("header counts %d site checks, records %+v; want the one stored report", h.SiteChecks, checks)
			}
			// The line names its kind first, so a reader can tell it from a page
			// before decoding the rest.
			if want := `{"record":"site_check","kind":"` + tt.kind + `",`; !strings.HasPrefix(rawLines(raw)[1], want) {
				t.Errorf("line 2 = %.80s…, want it to start %s", rawLines(raw)[1], want)
			}
			got := checks[0]
			if got.Kind != tt.kind || got.Subject != tt.subject {
				t.Errorf("site check = %s %s, want %s %s", got.Kind, got.Subject, tt.kind, tt.subject)
			}
			if string(got.Report) != string(data) {
				t.Errorf("report is not the stored JSON verbatim:\n got %s\nwant %s", got.Report, data)
			}
			back := tt.decoded()
			if err := json.Unmarshal(got.Report, back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, tt.report) {
				t.Errorf("report read back = %+v, want %+v", back, tt.report)
			}
		})
	}
}

// The robots.txt the crawl obeyed reaches the consumer inside the robots
// report — what lets it check pages against the file per bot without
// fetching it again. A real crawl here, not a hand-stored row.
func TestCrawledRobotsReportCarriesTheFileBody(t *testing.T) {
	st, info := crawledFixture(t, nil)
	_, checks, _, _ := crawlLevelOf(t, st, info)
	var kinds []string
	var robots *sitecheck.RobotsReport
	for _, sc := range checks {
		kinds = append(kinds, sc.Kind)
		if sc.Kind == sitecheck.KindRobots {
			robots = &sitecheck.RobotsReport{}
			if err := json.Unmarshal(sc.Report, robots); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A root-seeded spider crawl runs the default pass (auto): robots, sitemap
	// and ai_bots, never the opt-in render diff. Sorted by kind.
	if want := []string{sitecheck.KindAIBots, sitecheck.KindRobots, sitecheck.KindSitemap}; !slices.Equal(kinds, want) {
		t.Fatalf("site check kinds = %v, want %v", kinds, want)
	}
	// The fixture serves its about page at every unknown path, robots.txt included.
	if robots == nil || !robots.Found || !strings.Contains(robots.Body, "about body text") {
		t.Errorf("robots report = %+v, want the served file's body", robots)
	}
}

// Sorted by kind, then subject, so two bundles of one crawl stay
// byte-identical whatever order the pass stored its reports in.
func TestSiteCheckRecordsSortByKindThenSubject(t *testing.T) {
	c := storedCrawl(t)
	for _, r := range []crawler.SiteCheckRecord{
		{Kind: "sitemap", Subject: "https://b.test", Report: []byte(`{}`)},
		{Kind: "robots", Subject: "https://ex.test/robots.txt", Report: []byte(`{}`)},
		{Kind: "sitemap", Subject: "https://a.test", Report: []byte(`{}`)},
		{Kind: "ai_bots", Subject: "https://ex.test/", Report: []byte(`{}`)},
	} {
		if err := c.SiteCheck(r); err != nil {
			t.Fatal(err)
		}
	}
	_, checks, _, _ := crawlLevelOf(t, c, store.Info{ID: c.ID})
	var got []string
	for _, sc := range checks {
		got = append(got, sc.Kind+" "+sc.Subject)
	}
	want := []string{"ai_bots https://ex.test/", "robots https://ex.test/robots.txt",
		"sitemap https://a.test", "sitemap https://b.test"}
	if !slices.Equal(got, want) {
		t.Errorf("site_checks order = %v, want %v", got, want)
	}
}

// The site checks come first, then the llms.txt files, then the pages — every
// crawl-level record a few lines in, so a reader after only those stops early
// and a reader after only the pages skips a counted number of lines.
func TestCrawlLevelRecordsFollowTheHeaderBeforeThePages(t *testing.T) {
	c := storedCrawl(t)
	for _, r := range []crawler.SiteCheckRecord{
		{Kind: "sitemap", Subject: "https://ex.test", Report: []byte(`{}`)},
		{Kind: "robots", Subject: "https://ex.test/robots.txt", Report: []byte(`{}`)},
	} {
		if err := c.SiteCheck(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{"https://ex.test/llms.txt", "https://ex.test/llms-full.txt"} {
		if err := c.LlmsTxtFile(crawler.LlmsTxtRecord{URL: u, Kind: "llms_txt"}); err != nil {
			t.Fatal(err)
		}
	}
	h, _, _, raw := crawlLevelOf(t, c, store.Info{ID: c.ID})
	if h.SiteChecks != 2 || h.LlmsTxt != 2 || h.Pages != 1 {
		t.Fatalf("header counts %d site checks, %d llms.txt files, %d pages; want 2, 2, 1", h.SiteChecks, h.LlmsTxt, h.Pages)
	}
	var got []string
	for _, line := range rawLines(raw)[1:] {
		var rec struct{ Record, Kind, Subject, URL string }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.TrimSpace(rec.Record+" "+rec.Kind+" "+rec.Subject+rec.URL))
	}
	want := []string{
		"site_check robots https://ex.test/robots.txt",
		"site_check sitemap https://ex.test",
		"llms_txt llms_txt https://ex.test/llms-full.txt",
		"llms_txt llms_txt https://ex.test/llms.txt",
		"https://ex.test/", // a page names no record kind
	}
	if !slices.Equal(got, want) {
		t.Errorf("lines after the header:\n got %q\nwant %q", got, want)
	}
}

// A row exists exactly when a check ran, so a crawl that ran none — and
// fetched no llms.txt — counts 0 of each, and its pages follow the header.
func TestNothingRanCountsZeroAndGoesStraightToThePages(t *testing.T) {
	st, info := crawledFixture(t, func(c *config.Config) {
		c.SiteChecks.Enabled = "never"
		c.LlmsTxt.Check = false
	})
	_, _, raw := bundleOf(t, st, info, Options{})
	lines := rawLines(raw)
	for _, want := range []string{`"site_checks":0`, `"llms_txt":0`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header does not contain %s:\n%s", want, lines[0])
		}
	}
	if !strings.HasPrefix(lines[1], `{"url":`) {
		t.Errorf("line 2 is not a page:\n%.200s", lines[1])
	}
}

// Line 1 stays cheap to read whatever the crawl-level data weighs: the bodies
// it used to carry are on their own lines, and the header only counts them.
func TestHeaderStaysSmallBesideLargeCrawlLevelData(t *testing.T) {
	c := storedCrawl(t)
	robots, err := json.Marshal(&sitecheck.RobotsReport{
		URL: "https://ex.test/robots.txt", Status: 200, Found: true,
		Body: strings.Repeat("Disallow: /private\n", 500<<10/19), // ~500 KiB, the cap
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SiteCheck(crawler.SiteCheckRecord{Kind: sitecheck.KindRobots, Subject: "https://ex.test/robots.txt", Report: robots}); err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("All the docs, inlined. ", 1<<20/23) // ~1 MiB
	if err := c.LlmsTxtFile(crawler.LlmsTxtRecord{URL: "https://ex.test/llms-full.txt", Kind: "llms_full_txt",
		Status: 200, Found: true, Content: []byte(full)}); err != nil {
		t.Fatal(err)
	}
	h, checks, files, raw := crawlLevelOf(t, c, store.Info{ID: c.ID})
	if header := rawLines(raw)[0]; len(header) > 4<<10 {
		t.Errorf("header is %d bytes, want a few KB at most:\n%.300s…", len(header), header)
	}
	if h.SiteChecks != 1 || h.LlmsTxt != 1 || len(checks) != 1 || len(files) != 1 || files[0].Content != full {
		t.Errorf("records = %d site checks, %d llms.txt files; want both whole on their own lines", len(checks), len(files))
	}
}

// The llms.txt audit's files come out with every stored column, the raw body
// included, and each file nests the curated links it listed.
func TestLlmsTxtRecordsCarryTheFilesAndTheirLinks(t *testing.T) {
	c := storedCrawl(t)
	body := "# Ex\n\n> The example site.\n\n## Docs\n\n- [Guide](/guide): how to\n- [API](/api)\n"
	for _, rec := range []crawler.LlmsTxtRecord{
		{URL: "https://ex.test/llms.txt", Kind: "llms_txt", Status: 200, Found: true,
			Title: "Ex", Summary: "The example site.", Content: []byte(body)},
		{URL: "https://ex.test/llms-full.txt", Kind: "llms_full_txt", Status: 404, Content: []byte("not found")},
	} {
		if err := c.LlmsTxtFile(rec); err != nil {
			t.Fatal(err)
		}
	}
	// Recorded out of order: the bundle sorts each file's links by URL.
	for _, l := range [][4]string{
		{"https://ex.test/llms.txt", "https://ex.test/guide", "Docs", "Guide"},
		{"https://ex.test/llms.txt", "https://ex.test/api", "Docs", "API"},
	} {
		if err := c.LlmsTxtLink(l[0], l[1], l[2], l[3]); err != nil {
			t.Fatal(err)
		}
	}
	_, _, files, _ := crawlLevelOf(t, c, store.Info{ID: c.ID})
	want := []LlmsTxt{
		// Sorted by URL: llms-full.txt before llms.txt.
		{Record: RecordLlmsTxt, URL: "https://ex.test/llms-full.txt", Kind: "llms_full_txt", Status: 404,
			Content: "not found", Links: []LlmsTxtLink{}},
		{Record: RecordLlmsTxt, URL: "https://ex.test/llms.txt", Kind: "llms_txt", Status: 200, Found: true,
			Title: "Ex", Summary: "The example site.", Content: body, Links: []LlmsTxtLink{
				{URL: "https://ex.test/api", Section: "Docs", Anchor: "API"},
				{URL: "https://ex.test/guide", Section: "Docs", Anchor: "Guide"},
			}},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("llms_txt records = %+v\nwant %+v", files, want)
	}
}

// Each page carries the sitemap entries that list it — membership and the
// lastmod each sitemap gave it, sorted by sitemap — and [] when none does.
// A row stored before lastmod was kept (NULL) reads as "".
func TestPageCarriesItsSitemapEntries(t *testing.T) {
	c := storedCrawl(t)
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/unlisted", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][3]string{
		{"https://ex.test/sitemap-pages.xml", "https://ex.test/", "2026-01-15"},
		{"https://ex.test/sitemap-news.xml", "https://ex.test/", "2026-03-01T08:00:00+00:00"},
		{"https://ex.test/sitemap-main.xml", "https://ex.test/", ""},
	} {
		if err := c.SitemapEntry(e[0], e[1], e[2]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.DB().Exec(`INSERT INTO sitemap_entries(sitemap, url) VALUES(?, ?)`,
		"https://ex.test/sitemap-legacy.xml", "https://ex.test/"); err != nil {
		t.Fatal(err)
	}
	_, pages, _ := bundleOf(t, c, store.Info{ID: c.ID}, Options{})
	home := pageByPath(pages, "ex.test/")
	want := []SitemapEntry{
		{Sitemap: "https://ex.test/sitemap-legacy.xml", Lastmod: ""},
		{Sitemap: "https://ex.test/sitemap-main.xml", Lastmod: ""},
		{Sitemap: "https://ex.test/sitemap-news.xml", Lastmod: "2026-03-01T08:00:00+00:00"},
		{Sitemap: "https://ex.test/sitemap-pages.xml", Lastmod: "2026-01-15"},
	}
	if home == nil || !reflect.DeepEqual(home.Sitemaps, want) {
		t.Errorf("home sitemaps = %+v, want %+v", home, want)
	}
	// Decoded, [] is an empty slice and a missing key or null is nil.
	if unlisted := pageByPath(pages, "/unlisted"); unlisted == nil || unlisted.Sitemaps == nil || len(unlisted.Sitemaps) != 0 {
		t.Errorf("a page no sitemap lists must carry sitemaps: [], got %+v", unlisted)
	}
}

// An image link says whether its <img> had an alt attribute at all, so a
// consumer can tell a missing alt from a decorative alt="" — alt alone cannot,
// since it is omitted when empty. Other link types carry no such key.
func TestImageLinksCarryNoAltAttr(t *testing.T) {
	c, err := store.CreateCrawl(t.TempDir(), []string{"https://ex.test/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Page(&crawler.PageRecord{
		URL: "https://ex.test/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Facts: &parse.Facts{Links: []parse.Link{
			{Type: parse.Image, URL: "https://ex.test/missing.png", Raw: "/missing.png", NoAltAttr: true},
			{Type: parse.Image, URL: "https://ex.test/decorative.png", Raw: "/decorative.png"},
			{Type: parse.Image, URL: "https://ex.test/logo.png", Raw: "/logo.png", Alt: "Logo"},
			{Type: parse.Hyperlink, URL: "https://ex.test/about", Raw: "/about", Anchor: "About"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_, pages, raw := bundleOf(t, c, store.Info{ID: c.ID}, Options{LinkTypes: []string{LinkTypeAll}})
	if len(pages) != 1 || len(pages[0].Links) != 4 {
		t.Fatalf("pages = %+v", pages)
	}
	for _, tt := range []struct {
		url  string
		want *bool
	}{
		{"https://ex.test/missing.png", ptr(true)},
		{"https://ex.test/decorative.png", ptr(false)},
		{"https://ex.test/logo.png", ptr(false)},
		{"https://ex.test/about", nil},
	} {
		for _, l := range pages[0].Links {
			if l.URL != tt.url {
				continue
			}
			if (l.NoAltAttr == nil) != (tt.want == nil) || (l.NoAltAttr != nil && *l.NoAltAttr != *tt.want) {
				t.Errorf("%s: no_alt_attr = %v, want %v", tt.url, fmtBool(l.NoAltAttr), fmtBool(tt.want))
			}
		}
	}
	// On the wire: present (false included) on every image link, absent elsewhere.
	if n := strings.Count(string(raw), `"no_alt_attr":`); n != 3 {
		t.Errorf("no_alt_attr appears %d times, want once per image link:\n%s", n, raw)
	}
}

func ptr(b bool) *bool { return &b }

func fmtBool(b *bool) string {
	if b == nil {
		return "absent"
	}
	if *b {
		return "true"
	}
	return "false"
}

// A report is carried verbatim, so a stored row that is not JSON fails the
// bundle naming the row, rather than surfacing as an encoder error.
func TestCorruptSiteCheckReportFailsNamingTheRow(t *testing.T) {
	c := storedCrawl(t)
	if _, err := c.DB().Exec(`INSERT INTO site_checks(kind, subject, report) VALUES(?, ?, ?)`,
		"robots", "https://ex.test/robots.txt", "{truncated"); err != nil {
		t.Fatal(err)
	}
	err := Write(c, store.Info{ID: c.ID}, Options{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "site check robots https://ex.test/robots.txt") {
		t.Errorf("Write = %v, want an error naming the corrupt report", err)
	}
}
