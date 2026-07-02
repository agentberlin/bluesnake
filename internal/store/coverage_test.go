package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
)

// TestBrandLogoRoundTrip covers the per-host brand cache in the registry DB:
// a miss returns empty (no error), a save then read round-trips the bytes and
// content type, and a second save for the same host replaces the first (the
// favicon is re-fetched only the first time a brand is seen).
func TestBrandLogoRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// miss: nothing cached yet
	data, ct, err := GetBrandLogo(dir, "example.com")
	if err != nil {
		t.Fatalf("GetBrandLogo miss: %v", err)
	}
	if data != nil || ct != "" {
		t.Errorf("miss should be empty, got data=%q ct=%q", data, ct)
	}

	// save + read round-trip
	png := []byte("\x89PNG\r\n\x1a\nfavicon-bytes")
	if err := SaveBrandLogo(dir, "example.com", "image/png", png); err != nil {
		t.Fatal(err)
	}
	got, gotCT, err := GetBrandLogo(dir, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) || gotCT != "image/png" {
		t.Errorf("round-trip = %q/%q, want %q/image/png", got, gotCT, png)
	}

	// replace: a second save for the same host wins
	ico := []byte("ICO-bytes")
	if err := SaveBrandLogo(dir, "example.com", "image/x-icon", ico); err != nil {
		t.Fatal(err)
	}
	got, gotCT, _ = GetBrandLogo(dir, "example.com")
	if string(got) != string(ico) || gotCT != "image/x-icon" {
		t.Errorf("after replace = %q/%q, want %q/image/x-icon", got, gotCT, ico)
	}

	// distinct hosts are independent
	if d, _, _ := GetBrandLogo(dir, "other.com"); d != nil {
		t.Errorf("other host should be a miss, got %q", d)
	}
}

// TestLlmsTxtRoundTrip covers the llms.txt audit persistence: a fetched file
// record and its curated links are stored, then LlmsTxt() reloads both. An
// untouched crawl yields an empty (non-nil) set.
func TestLlmsTxtRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// empty before anything is fetched
	empty, err := c.LlmsTxt()
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty.Files) != 0 || len(empty.Links) != 0 {
		t.Fatalf("empty LlmsTxt = %+v", empty)
	}

	rec := crawler.LlmsTxtRecord{
		URL: "https://ex.com/llms.txt", Kind: "llms_txt", Status: 200, Found: true,
		Title: "Example", Summary: "the summary", Malformed: false,
		Content: []byte("# Example\n\n## Docs\n- [Guide](https://ex.com/guide)\n"),
	}
	if err := c.LlmsTxtFile(rec); err != nil {
		t.Fatal(err)
	}
	if err := c.LlmsTxtLink("https://ex.com/llms.txt", "https://ex.com/guide", "Docs", "Guide"); err != nil {
		t.Fatal(err)
	}
	// duplicate link is ignored (INSERT OR IGNORE on the (src,url) key)
	if err := c.LlmsTxtLink("https://ex.com/llms.txt", "https://ex.com/guide", "Docs", "Guide again"); err != nil {
		t.Fatal(err)
	}

	data, err := c.LlmsTxt()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(data.Files))
	}
	f := data.Files[0]
	if f.URL != rec.URL || f.Kind != "llms_txt" || f.Status != 200 || !f.Found || f.Title != "Example" || f.Summary != "the summary" || f.Malformed {
		t.Errorf("reloaded file = %+v", f)
	}
	if len(data.Links) != 1 {
		t.Fatalf("links = %d, want 1 (duplicate ignored)", len(data.Links))
	}
	if l := data.Links[0]; l.Src != rec.URL || l.URL != "https://ex.com/guide" || l.Section != "Docs" || l.Anchor != "Guide" {
		t.Errorf("reloaded link = %+v", l)
	}
}

// TestSitemapIndexRoundTrip covers SitemapEntry → SitemapIndex: each entry maps
// a page URL to the sitemaps that list it, a page listed in two sitemaps yields
// both, and a duplicate (sitemap,url) entry is ignored.
func TestSitemapIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.SitemapEntry("https://ex.com/sitemap.xml", "https://ex.com/a"))
	must(c.SitemapEntry("https://ex.com/sitemap.xml", "https://ex.com/a")) // dup ignored
	must(c.SitemapEntry("https://ex.com/sitemap.xml", "https://ex.com/b"))
	must(c.SitemapEntry("https://ex.com/news-sitemap.xml", "https://ex.com/a"))

	idx, err := c.SitemapIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(idx["https://ex.com/a"]); got != 2 {
		t.Errorf("/a listed in %d sitemaps, want 2: %v", got, idx["https://ex.com/a"])
	}
	if got := len(idx["https://ex.com/b"]); got != 1 {
		t.Errorf("/b listed in %d sitemaps, want 1", got)
	}
}

// TestSetTotalBackfill covers the lazy encountered-URL backfill: a crawl row
// created without a total can have it filled in later, without touching status.
func TestSetTotalBackfill(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	c.Close()

	infos, _ := ListCrawls(dir)
	if len(infos) != 1 || infos[0].Total != 0 {
		t.Fatalf("fresh crawl total = %+v, want 0", infos)
	}
	if err := SetTotal(dir, id, 137); err != nil {
		t.Fatal(err)
	}
	infos, _ = ListCrawls(dir)
	if infos[0].Total != 137 {
		t.Errorf("after SetTotal: total = %d, want 137", infos[0].Total)
	}
	if infos[0].Status != StatusRunning {
		t.Errorf("SetTotal must not change status, got %q", infos[0].Status)
	}
}

// TestCrawlIDValidation locks the path-traversal guard shared by OpenCrawl and
// CrawlDBPath: a network-exposed id can never escape the crawls dir. Valid ids
// resolve to the on-disk path; separators, "..", absolute, and empty are
// rejected with a "not found" error (never an open).
func TestCrawlIDValidation(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	c.Close()

	// a real id resolves to its on-disk db path
	path, err := CrawlDBPath(dir, id)
	if err != nil {
		t.Fatalf("CrawlDBPath(%q): %v", id, err)
	}
	if path != crawlPath(dir, id) {
		t.Errorf("path = %q, want %q", path, crawlPath(dir, id))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("resolved path must exist: %v", err)
	}

	// traversing / malformed ids are refused by both entry points
	for _, bad := range []string{"", ".", "..", "../escape", "a/b", `a\b`, "/abs", "foo/../bar"} {
		if _, err := CrawlDBPath(dir, bad); err == nil {
			t.Errorf("CrawlDBPath(%q) must error", bad)
		}
		if _, err := OpenCrawl(dir, bad); err == nil {
			t.Errorf("OpenCrawl(%q) must error", bad)
		}
	}

	// a syntactically valid but nonexistent id is "not found", not an open
	if _, err := CrawlDBPath(dir, "20200101-000000-deadbe"); err == nil {
		t.Error("CrawlDBPath of a missing crawl must error")
	}
	if _, err := OpenCrawl(dir, "20200101-000000-deadbe"); err == nil {
		t.Error("OpenCrawl of a missing crawl must error")
	}
}

// TestDeleteCrawl covers removal: the registry row and the on-disk database
// (plus its -wal/-shm sidecars) are gone afterwards.
func TestDeleteCrawl(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/", State: crawler.StateCrawled, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	c.Close()

	if _, err := os.Stat(crawlPath(dir, id)); err != nil {
		t.Fatalf("db should exist before delete: %v", err)
	}
	if err := DeleteCrawl(dir, id); err != nil {
		t.Fatal(err)
	}
	if infos, _ := ListCrawls(dir); len(infos) != 0 {
		t.Errorf("registry still lists %d crawls after delete", len(infos))
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(crawlPath(dir, id) + suffix); !os.IsNotExist(err) {
			t.Errorf("file %q should be gone, stat err = %v", crawlPath(dir, id)+suffix, err)
		}
	}
	// deleting an already-absent crawl is a no-op, not an error
	if err := DeleteCrawl(dir, id); err != nil {
		t.Errorf("re-delete should be a no-op, got %v", err)
	}
}

// TestPageHeadersRoundTrip covers the response-header column: a page stored with
// headers reloads with them intact (Page marshals the map, LoadPages unmarshals).
func TestPageHeadersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Headers: map[string]string{"Content-Type": "text/html", "X-Robots-Tag": "noindex"},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"].Headers
	if got["Content-Type"] != "text/html" || got["X-Robots-Tag"] != "noindex" {
		t.Errorf("headers = %v", got)
	}
}

// TestSeedsEmpty covers Seeds() when the seeds meta is empty — it returns a nil
// set rather than failing to unmarshal "".
func TestSeedsEmpty(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetMeta("seeds", ""); err != nil {
		t.Fatal(err)
	}
	seeds, err := c.Seeds()
	if err != nil {
		t.Fatalf("Seeds() on empty meta: %v", err)
	}
	if seeds != nil {
		t.Errorf("Seeds() = %v, want nil", seeds)
	}
}

// TestOperationsOnClosedCrawlReturnErrors is a robustness check: after the crawl
// DB is closed, the sink/read methods surface an error instead of panicking. It
// also exercises each method's first failure branch in one place.
func TestOperationsOnClosedCrawlReturnErrors(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	page := &crawler.PageRecord{URL: "https://ex.com/", State: crawler.StateCrawled, Facts: &parse.Facts{}}

	checks := map[string]func() error{
		"Page":                 func() error { return c.Page(page) },
		"Admit":                func() error { _, err := c.Admit(frontier.Item{URL: "https://ex.com/a"}); return err },
		"FrontierDone":         func() error { return c.FrontierDone("https://ex.com/a") },
		"PendingFrontier":      func() error { _, err := c.PendingFrontier(); return err },
		"ProcessedURLs":        func() error { _, err := c.ProcessedURLs(); return err },
		"SaveInlinksFromEdges": func() error { return c.SaveInlinksFromEdges([]string{"https://ex.com/"}) },
		"SaveDepthsMap":        func() error { return c.SaveDepthsMap(map[string]int{"https://ex.com/": 0}) },
		"LoadPages":            func() error { _, err := c.LoadPages(); return err },
		"SaveIssues":           func() error { return c.SaveIssues(nil, nil) },
		"SaveIssuesOwned":      func() error { return c.SaveIssues([]string{"i"}, []issues.Occurrence{{URL: "u", IssueID: "i"}}) },
		"IssueCounts":          func() error { _, err := c.IssueCounts(); return err },
		"IssueURLs":            func() error { _, err := c.IssueURLs("i"); return err },
		"SitemapIndex":         func() error { _, err := c.SitemapIndex(); return err },
		"LlmsTxt":              func() error { _, err := c.LlmsTxt(); return err },
		"Chains":               func() error { _, err := c.Chains(); return err },
		"Counts":               func() error { _, _, err := c.Counts(); return err },
		"PageCount":            func() error { _, err := c.PageCount(); return err },
		"Meta":                 func() error { _, err := c.Meta("config"); return err },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s on closed crawl returned nil error", name)
		}
	}
}

// TestProjectJobFailure covers a project-sourced job that fails: the optional
// project_id/label columns store real values (not NULL) and round-trip, and
// FinishJob records a terminal failure with its error detail.
func TestProjectJobFailure(t *testing.T) {
	dir := t.TempDir()
	j, err := EnqueueJob(dir, Job{
		Source: "project", ProjectID: "proj-1", Label: "competitor.example",
		Request: `{"url":"https://competitor.example/"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimNextJob(dir); err != nil {
		t.Fatal(err)
	}
	if err := FinishJob(dir, j.ID, JobFailed, "fetch: connection refused"); err != nil {
		t.Fatal(err)
	}

	jobs, _ := ListJobs(dir)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	got := jobs[0]
	if got.Source != "project" || got.ProjectID != "proj-1" || got.Label != "competitor.example" {
		t.Errorf("project fields = %+v", got)
	}
	if got.Status != JobFailed || got.Error != "fetch: connection refused" || got.Finished.IsZero() {
		t.Errorf("failure not recorded: status=%q err=%q finished=%v", got.Status, got.Error, got.Finished)
	}
}

// TestBlobExtensions covers extFor's branch table via Blob: html→.html,
// rendered_html→.html, screenshot→.jpg, anything else→.bin. DB() exposes the
// live handle the analyze/export layers read through.
func TestBlobExtensions(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	cases := []struct{ kind, wantExt string }{
		{"html", ".html"},
		{"rendered_html", ".html"},
		{"screenshot", ".jpg"},
		{"warc", ".bin"}, // unknown kind falls through to .bin
	}
	for _, tc := range cases {
		if err := c.Blob("https://ex.com/", tc.kind, []byte("x")); err != nil {
			t.Fatalf("Blob(%q): %v", tc.kind, err)
		}
		path, err := c.BlobPath("https://ex.com/", tc.kind)
		if err != nil || path == "" {
			t.Fatalf("BlobPath(%q) = %q, %v", tc.kind, path, err)
		}
		if filepath.Ext(path) != tc.wantExt {
			t.Errorf("kind %q → ext %q, want %q", tc.kind, filepath.Ext(path), tc.wantExt)
		}
	}

	// DB() returns a usable handle
	if c.DB() == nil {
		t.Fatal("DB() returned nil")
	}
	var n int
	if err := c.DB().QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&n); err != nil {
		t.Fatalf("query through DB(): %v", err)
	}
	if n != len(cases) {
		t.Errorf("blobs = %d, want %d", n, len(cases))
	}
}
