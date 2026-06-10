package store

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/hhsecond/acrawler/internal/analyze"
	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/extract"
	"github.com/hhsecond/acrawler/internal/frontier"
	"github.com/hhsecond/acrawler/internal/issues"
	"github.com/hhsecond/acrawler/internal/parse"
	"github.com/hhsecond/acrawler/internal/structured"
)

func TestCrawlLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()

	c, err := CreateCrawl(dir, "proj", "https://ex.com/", "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// config frozen
	stored, err := c.Meta("config")
	if err != nil || stored == "" {
		t.Fatalf("config meta: %q %v", stored, err)
	}
	if _, err := config.Load([]byte(stored)); err != nil {
		t.Fatalf("stored config must load: %v", err)
	}

	// page + links round trip
	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		Indexable: true,
		Facts: &parse.Facts{
			Titles: []string{"Home"},
			Links: []parse.Link{
				{Type: parse.Hyperlink, URL: "https://ex.com/a", Anchor: "A"},
				{Type: parse.Image, URL: "https://ex.com/i.png", Alt: "pic"},
			},
		},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := c.db.QueryRow(`SELECT json_extract(facts, '$.Titles[0]') FROM pages WHERE url = ?`,
		"https://ex.com/").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Home" {
		t.Errorf("facts title = %q", title)
	}
	var linkCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM links WHERE src = ?`, "https://ex.com/").Scan(&linkCount)
	if linkCount != 2 {
		t.Errorf("links = %d, want 2", linkCount)
	}

	// frontier round trip
	if err := c.FrontierAdd(frontier.Item{URL: "https://ex.com/a", Depth: 1, Source: "https://ex.com/"}); err != nil {
		t.Fatal(err)
	}
	if err := c.FrontierAdd(frontier.Item{URL: "https://ex.com/b", Depth: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.FrontierDone("https://ex.com/a"); err != nil {
		t.Fatal(err)
	}
	pending, err := c.PendingFrontier()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].URL != "https://ex.com/b" {
		t.Errorf("pending = %+v", pending)
	}
	processed, err := c.ProcessedURLs()
	if err != nil {
		t.Fatal(err)
	}
	if len(processed) != 1 || processed[0] != "https://ex.com/" {
		t.Errorf("processed = %v", processed)
	}

	// registry
	infos, err := ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != c.ID || infos[0].Project != "proj" || infos[0].Status != StatusRunning {
		t.Errorf("registry = %+v", infos)
	}
	if err := SetStatus(dir, c.ID, StatusCompleted, 42); err != nil {
		t.Fatal(err)
	}
	infos, _ = ListCrawls(dir)
	if infos[0].Status != StatusCompleted || infos[0].Crawled != 42 {
		t.Errorf("after SetStatus: %+v", infos[0])
	}

	// reopen by ID
	c.Close()
	c2, err := OpenCrawl(dir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()

	// delete
	if err := DeleteCrawl(dir, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCrawl(dir, c.ID); err == nil {
		t.Error("deleted crawl must not open")
	}
	infos, _ = ListCrawls(dir)
	if len(infos) != 0 {
		t.Errorf("registry after delete = %+v", infos)
	}
}

// cancellingSink cancels the context after N pages, simulating an interrupt.
type cancellingSink struct {
	*Crawl
	mu     sync.Mutex
	count  int
	limit  int
	cancel context.CancelFunc
}

func (s *cancellingSink) Page(rec *crawler.PageRecord) error {
	err := s.Crawl.Page(rec)
	s.mu.Lock()
	s.count++
	if s.count == s.limit {
		s.cancel()
	}
	s.mu.Unlock()
	return err
}

func TestInterruptAndResume(t *testing.T) {
	const total = 60
	var mu sync.Mutex
	hits := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := range total {
				fmt.Fprintf(w, `<a href="/p%d">x</a> `, i)
			}
			return
		}
		fmt.Fprint(w, "<p>x</p>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Speed.MaxThreads = 2

	// phase 1: crawl, interrupted after ~15 pages
	st, err := CreateCrawl(dir, "proj", srv.URL+"/", "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &cancellingSink{Crawl: st, limit: 15, cancel: cancel}
	c, err := crawler.New(cfg, crawler.WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interrupted {
		t.Fatal("phase 1 must be interrupted")
	}
	processed, _ := st.ProcessedURLs()
	pending, _ := st.PendingFrontier()
	if len(processed) == 0 || len(pending) == 0 {
		t.Fatalf("interrupt state: %d processed, %d pending", len(processed), len(pending))
	}
	if len(processed) >= total+1 {
		t.Fatalf("interrupt too late: %d processed", len(processed))
	}
	st.Close()

	// phase 2: resume from the stored frontier
	st2, err := OpenCrawl(dir, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	processed, _ = st2.ProcessedURLs()
	pending, _ = st2.PendingFrontier()
	seed, _ := st2.Meta("seed")
	c2, err := crawler.New(cfg, crawler.WithSink(st2), crawler.WithResume(processed, pending))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Run(context.Background(), seed); err != nil {
		t.Fatal(err)
	}

	finalProcessed, _ := st2.ProcessedURLs()
	if len(finalProcessed) != total+1 {
		t.Errorf("after resume: %d pages processed, want %d", len(finalProcessed), total+1)
	}
	leftover, _ := st2.PendingFrontier()
	if len(leftover) != 0 {
		t.Errorf("frontier not drained: %d items left", len(leftover))
	}
	mu.Lock()
	defer mu.Unlock()
	for path, n := range hits {
		// robots.txt is re-fetched on resume by design (fresh policy check)
		if path != "/robots.txt" && n > 1 {
			t.Errorf("%s fetched %d times — resume must not re-fetch", path, n)
		}
	}
}

func TestLoadPagesAndIssues(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		Indexable: true, Depth: 2,
		Headers: map[string]string{"X-Frame-Options": "DENY"},
		Facts: &parse.Facts{
			Titles: []string{"Home"}, H1s: []string{"H"},
			Links: []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.com/a"}},
		},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/plain", Scope: "internal",
		State: crawler.StateCrawled, StatusCode: 404}); err != nil {
		t.Fatal(err)
	}

	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"]
	if got == nil || got.Depth != 2 || !got.Indexable {
		t.Fatalf("loaded page = %+v", got)
	}
	if got.Headers["X-Frame-Options"] != "DENY" {
		t.Errorf("headers not restored: %v", got.Headers)
	}
	if got.Facts == nil || len(got.Facts.Titles) != 1 || got.Facts.Titles[0] != "Home" {
		t.Errorf("facts not restored: %+v", got.Facts)
	}
	if len(got.Facts.Links) != 1 || got.Facts.Links[0].URL != "https://ex.com/a" {
		t.Errorf("facts links not restored: %+v", got.Facts.Links)
	}
	if plain := pages["https://ex.com/plain"]; plain == nil || plain.Facts != nil {
		t.Errorf("non-HTML page = %+v", plain)
	}

	// issues round trip
	occs := []issues.Occurrence{
		{URL: "https://ex.com/", IssueID: "title_missing"},
		{URL: "https://ex.com/", IssueID: "h1_missing", Detail: "d"},
		{URL: "https://ex.com/plain", IssueID: "title_missing"},
	}
	if err := c.SaveIssues(occs); err != nil {
		t.Fatal(err)
	}
	counts, err := c.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["title_missing"] != 2 || counts["h1_missing"] != 1 {
		t.Errorf("counts = %v", counts)
	}
	urls, err := c.IssueURLs("title_missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Errorf("urls = %v", urls)
	}
	// saving again replaces
	if err := c.SaveIssues(occs[:1]); err != nil {
		t.Fatal(err)
	}
	counts, _ = c.IssueCounts()
	if counts["title_missing"] != 1 || counts["h1_missing"] != 0 {
		t.Errorf("counts after replace = %v", counts)
	}

	// inlink aggregates
	rec.Inlinks = 7
	rec.DiscoveredFrom = "https://ex.com/from"
	if err := c.UpdateInlinks(map[string]*crawler.PageRecord{rec.URL: rec}); err != nil {
		t.Fatal(err)
	}
	pages, _ = c.LoadPages()
	if pages["https://ex.com/"].Inlinks != 7 {
		t.Errorf("inlinks = %d", pages["https://ex.com/"].Inlinks)
	}

	// meta set/get
	if err := c.SetMeta("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Meta("k"); v != "v" {
		t.Errorf("meta = %q", v)
	}
	if v, _ := c.Meta("absent"); v != "" {
		t.Errorf("absent meta = %q", v)
	}
}

func TestAnalysisPersistence(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	for _, url := range []string{"https://ex.com/", "https://ex.com/a"} {
		if err := c.Page(&crawler.PageRecord{URL: url, Scope: "internal",
			State: crawler.StateCrawled, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SitemapEntry("https://ex.com/sitemap.xml", "https://ex.com/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.SitemapEntry("https://ex.com/sitemap.xml", "https://ex.com/a"); err != nil {
		t.Fatal(err) // dedup via INSERT OR IGNORE
	}
	index, err := c.SitemapIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(index["https://ex.com/a"]) != 1 {
		t.Errorf("sitemap index = %v", index)
	}

	results := &analyze.Results{
		LinkScores: map[string]float64{"https://ex.com/": 100},
		UniqueIn:   map[string]int{"https://ex.com/a": 3},
		UniqueOut:  map[string]int{"https://ex.com/": 5},
		NearDups: map[string]analyze.NearDup{
			"https://ex.com/a": {ClosestMatch: "https://ex.com/", ClosestSimilarity: 95, Count: 1},
		},
		Chains: []analyze.Chain{{Type: "redirect", Source: "https://ex.com/r", Hops: []string{"a", "b"}}},
		Occurrences: []issues.Occurrence{
			{URL: "https://ex.com/a", IssueID: "content_near_duplicate"},
		},
	}
	if err := c.SaveAnalysis(results); err != nil {
		t.Fatal(err)
	}
	var score float64
	var uniqueIn int
	c.db.QueryRow(`SELECT link_score FROM pages WHERE url = 'https://ex.com/'`).Scan(&score)
	c.db.QueryRow(`SELECT unique_inlinks FROM pages WHERE url = 'https://ex.com/a'`).Scan(&uniqueIn)
	if score != 100 || uniqueIn != 3 {
		t.Errorf("score=%v uniqueIn=%d", score, uniqueIn)
	}
	chains, err := c.Chains()
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 1 || chains[0].Source != "https://ex.com/r" {
		t.Errorf("chains = %+v", chains)
	}
	counts, _ := c.IssueCounts()
	if counts["content_near_duplicate"] != 1 {
		t.Errorf("analysis issues not added: %v", counts)
	}
	// re-running SaveIssues (per-page evaluation) then AddIssues keeps both layers
	if err := c.SaveIssues([]issues.Occurrence{{URL: "https://ex.com/", IssueID: "title_missing"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddIssues(results.Occurrences); err != nil {
		t.Fatal(err)
	}
	counts, _ = c.IssueCounts()
	if counts["title_missing"] != 1 || counts["content_near_duplicate"] != 1 {
		t.Errorf("layered issues = %v", counts)
	}
}

func TestBlobStorage(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Blob("https://ex.com/", "html", []byte("<html>src</html>")); err != nil {
		t.Fatal(err)
	}
	path, err := c.BlobPath("https://ex.com/", "html")
	if err != nil || path == "" {
		t.Fatalf("blob path = %q, %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "<html>src</html>" {
		t.Errorf("blob content = %q, %v", data, err)
	}
	if p, _ := c.BlobPath("https://ex.com/", "screenshot"); p != "" {
		t.Error("absent blob must return empty path")
	}
}

func TestCustomResultsPersistence(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200,
		CustomResults: []extract.Result{
			{Kind: "search", Name: "phone", Value: "2"},
			{Kind: "extraction", Name: "sku", Value: "X"},
		},
		JSDiff:         &crawler.JSDiff{TitleChanged: true, RenderedTitle: "JS"},
		StructuredData: &structured.PageData{Formats: []string{"jsonld"}, Types: []string{"Product"}},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	var n int
	c.db.QueryRow(`SELECT COUNT(*) FROM custom_results`).Scan(&n)
	if n != 2 {
		t.Errorf("custom results = %d", n)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"]
	if got.JSDiff == nil || !got.JSDiff.TitleChanged || got.JSDiff.RenderedTitle != "JS" {
		t.Errorf("jsdiff = %+v", got.JSDiff)
	}
	if got.StructuredData == nil || got.StructuredData.Types[0] != "Product" {
		t.Errorf("structured = %+v", got.StructuredData)
	}
}
