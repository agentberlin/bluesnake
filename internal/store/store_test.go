package store

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/extract"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
)

func TestCrawlLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()

	c, err := CreateCrawl(dir, "proj", []string{"https://ex.com/"}, "spider", cfg)
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
	if err := SetStatus(dir, c.ID, StatusCompleted, 42, 50); err != nil {
		t.Fatal(err)
	}
	infos, _ = ListCrawls(dir)
	if infos[0].Status != StatusCompleted || infos[0].Crawled != 42 || infos[0].Total != 50 {
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
	st, err := CreateCrawl(dir, "proj", []string{srv.URL + "/"}, "spider", cfg)
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
	seeds, _ := st2.Seeds()
	c2, err := crawler.New(cfg, crawler.WithSink(st2), crawler.WithResume(processed, pending))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Run(context.Background(), seeds...); err != nil {
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
		// Site-level well-known files (robots.txt, llms.txt) are re-fetched on
		// resume by design — a fresh policy/validation check, idempotent in the
		// store — so they are exempt from the no-re-fetch rule for pages.
		switch path {
		case "/robots.txt", "/llms.txt", "/llms-full.txt":
			continue
		}
		if n > 1 {
			t.Errorf("%s fetched %d times — resume must not re-fetch", path, n)
		}
	}
}

func TestLoadPagesAndIssues(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, "", []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, "", []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, "", []string{"https://ex.com/"}, "spider", config.Default())
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

// TestSeedsRoundTrip covers the seed set CreateCrawl freezes into a crawl: a
// list crawl records every uploaded seed in order, a spider crawl records its
// one seed, the registry's representative seed is seeds[0], and an empty seed
// set is rejected.
func TestSeedsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// list crawl: the full ordered set round-trips
	full := []string{"https://a.example/", "https://b.example/", "https://c.example/p"}
	c, err := CreateCrawl(dir, "proj", full, "list", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	seeds, err := c.Seeds()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != len(full) {
		t.Fatalf("Seeds() = %v, want %v", seeds, full)
	}
	for i := range full {
		if seeds[i] != full[i] {
			t.Errorf("Seeds()[%d] = %q, want %q (order must be preserved)", i, seeds[i], full[i])
		}
	}
	// the registry lists the representative seed (seeds[0]) for `crawls ls`
	infos, err := ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Seed != full[0] {
		t.Errorf("registry seed = %q, want %q", infos[0].Seed, full[0])
	}

	// spider crawl: a single seed round-trips as a one-element set
	c2, err := CreateCrawl(dir, "proj", []string{"https://solo.example/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got, err := c2.Seeds(); err != nil || len(got) != 1 || got[0] != "https://solo.example/" {
		t.Fatalf("spider Seeds() = %v, %v", got, err)
	}

	// an empty seed set is rejected up front
	if _, err := CreateCrawl(dir, "proj", nil, "list", config.Default()); err == nil {
		t.Error("CreateCrawl with no seeds must error")
	}
}

// TestListModeResumeRestoresAllSeeds is the multi-seed list-mode resume case the
// single-seed restore got wrong. An interrupted list crawl has two uploaded
// seeds on different hosts: seedA crawled, seedB still pending. On resume the
// full seed set is restored, so seedB's host is classified internal (seedAuth
// covers every seed) and the full-graph depth recompute keeps both at depth 0.
// The contrast subtest restores only seedA — the old behaviour — and shows seedB
// falling out of scope (external) and losing its depth.
func TestListModeResumeRestoresAllSeeds(t *testing.T) {
	html := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<p>seed</p>")
	}
	srvA := httptest.NewServer(http.HandlerFunc(html))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(html))
	defer srvB.Close()
	seedA, seedB := srvA.URL+"/", srvB.URL+"/"

	cfg := config.Default()
	cfg.Mode = "list"
	cfg.Limits.MaxDepth = 0    // list default: audit exactly the uploaded URLs
	cfg.Robots.Mode = "ignore" // list default: don't gate the audited URLs

	// setup writes an interrupted list crawl with both seeds frozen in:
	// seedA already crawled at depth 0, seedB still pending at depth 0.
	setup := func(dir string) string {
		st, err := CreateCrawl(dir, "listtest", []string{seedA, seedB}, "list", cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Page(&crawler.PageRecord{
			URL: seedA, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: 0, Facts: &parse.Facts{},
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.FrontierAdd(frontier.Item{URL: seedB, Depth: 0}); err != nil {
			t.Fatal(err)
		}
		if err := SetStatus(dir, st.ID, StatusInterrupted, 1, 1); err != nil {
			t.Fatal(err)
		}
		id := st.ID
		st.Close()
		return id
	}

	// resume drains the crawl like the real resume path: it restores the seeds
	// (every stored seed when override is nil, the way the CLI/MCP/desktop resume
	// paths do via st.Seeds), reruns the crawl, then recomputes depth over the
	// full two-session graph and persists it — the resume branch of
	// finalize.Crawl, inlined to avoid an import cycle (finalize imports store).
	resume := func(dir, id string, override []string) map[string]*crawler.PageRecord {
		st, err := OpenCrawl(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		seeds := override
		if seeds == nil {
			if seeds, err = st.Seeds(); err != nil {
				t.Fatal(err)
			}
		}
		processed, _ := st.ProcessedURLs()
		pending, _ := st.PendingFrontier()
		c, err := crawler.New(cfg, crawler.WithSink(st), crawler.WithResume(processed, pending))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Run(context.Background(), seeds...); err != nil {
			t.Fatal(err)
		}
		all, err := st.LoadPages()
		if err != nil {
			t.Fatal(err)
		}
		c.RecomputeDepths(all, seeds...)
		if err := st.SaveDepths(all); err != nil {
			t.Fatal(err)
		}
		pages, err := st.LoadPages()
		if err != nil {
			t.Fatal(err)
		}
		return pages
	}

	// the real path: resume restores every stored seed via st.Seeds
	t.Run("all seeds restored", func(t *testing.T) {
		dir := t.TempDir()
		pages := resume(dir, setup(dir), nil)
		b := pages[seedB]
		if b == nil {
			t.Fatal("seedB not crawled on resume")
		}
		if b.Scope != "internal" {
			t.Errorf("seedB scope = %q, want internal (seedAuth must cover every stored seed)", b.Scope)
		}
		if pages[seedA].Depth != 0 || b.Depth != 0 {
			t.Errorf("depths = {A:%d, B:%d}, want both 0", pages[seedA].Depth, b.Depth)
		}
	})

	t.Run("single seed restored (regression contrast)", func(t *testing.T) {
		dir := t.TempDir()
		pages := resume(dir, setup(dir), []string{seedA})
		b := pages[seedB]
		if b == nil {
			t.Fatal("seedB not crawled on resume")
		}
		if b.Scope != "external" {
			t.Errorf("seedB scope = %q, want external under single-seed restore", b.Scope)
		}
		if b.Depth != crawler.NoDepth {
			t.Errorf("seedB depth = %d, want NoDepth (%d) under single-seed restore", b.Depth, crawler.NoDepth)
		}
	})
}

// TestSaveDepths verifies the resume depth fix's persistence step: it rewrites
// only the depth column for every supplied page (NoDepth -> NULL), leaving other
// columns untouched.
func TestSaveDepths(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "proj", []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// seed three pages with stale depths and a non-zero inlink count
	seeded := map[string]*crawler.PageRecord{}
	for _, p := range []struct {
		url   string
		depth int
	}{{"https://ex.com/", 0}, {"https://ex.com/a", 5}, {"https://ex.com/b", 5}} {
		rec := &crawler.PageRecord{
			URL: p.url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: p.depth, Inlinks: 7,
		}
		if err := c.Page(rec); err != nil {
			t.Fatal(err)
		}
		seeded[p.url] = rec
	}
	// inlinks are persisted by UpdateInlinks, not Page; set the baseline so the
	// "SaveDepths must not disturb inlinks" assertion below is meaningful.
	if err := c.UpdateInlinks(seeded); err != nil {
		t.Fatal(err)
	}

	// recomputed depths: /a corrected to 1, /b has no path (NoDepth -> NULL)
	corrected := map[string]*crawler.PageRecord{
		"https://ex.com/":  {URL: "https://ex.com/", Depth: 0},
		"https://ex.com/a": {URL: "https://ex.com/a", Depth: 1},
		"https://ex.com/b": {URL: "https://ex.com/b", Depth: crawler.NoDepth},
	}
	if err := c.SaveDepths(corrected); err != nil {
		t.Fatal(err)
	}

	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for url, want := range map[string]int{
		"https://ex.com/": 0, "https://ex.com/a": 1, "https://ex.com/b": crawler.NoDepth,
	} {
		if got := pages[url].Depth; got != want {
			t.Errorf("%s depth = %d, want %d", url, got, want)
		}
		// depth-only update must not disturb inlinks
		if got := pages[url].Inlinks; got != 7 {
			t.Errorf("%s inlinks = %d, want 7 (SaveDepths must touch only depth)", url, got)
		}
	}
	// NoDepth must be stored as SQL NULL, not -1
	var nullCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM pages WHERE depth IS NULL`).Scan(&nullCount)
	if nullCount != 1 {
		t.Errorf("NULL depths = %d, want 1", nullCount)
	}
}
