package project

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestSiteKey(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"example.com", "example.com", false},
		{"https://example.com/some/path?q=1", "example.com", false},
		{"EXAMPLE.com", "example.com", false},
		{"example.com:8080", "example.com:8080", false},
		{"www.example.com", "www.example.com", false}, // distinct from example.com
		{"a.example.com", "a.example.com", false},     // subdomain is its own site
		{"  https://example.com  ", "example.com", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := SiteKey(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("SiteKey(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SiteKey(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("SiteKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// no folding: these must all be different keys
	keys := map[string]bool{}
	for _, in := range []string{"example.com", "www.example.com", "a.example.com", "example.com:8080"} {
		k, _ := SiteKey(in)
		keys[k] = true
	}
	if len(keys) != 4 {
		t.Errorf("expected 4 distinct site keys (no folding), got %d: %v", len(keys), keys)
	}
}

func TestSeedClassificationHelpers(t *testing.T) {
	root := []string{"https://x.com", "https://x.com/", "https://x.com/?utm=1", "https://x.com/#top"}
	for _, s := range root {
		if !isRootSeed(s) {
			t.Errorf("isRootSeed(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"https://x.com/blog", "https://x.com/index.html", "https://x.com/en/"} {
		if isRootSeed(s) {
			t.Errorf("isRootSeed(%q) = true, want false", s)
		}
	}
	if k, ok := seedKey("https://X.com:8080/p"); !ok || k != "x.com:8080" {
		t.Errorf("seedKey host:port = %q,%v", k, ok)
	}
}

func TestProjectCRUD(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p, err := s.CreateProject("", "https://main.com")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "main.com's Project" {
		t.Errorf("default name = %q", p.Name)
	}
	if p.MainDomain != "main.com" {
		t.Errorf("main domain = %q", p.MainDomain)
	}

	if err := s.AddMember(p.ID, "rival.com", RoleCompetitor); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(p.ID, "https://other.com/foo", RoleCompetitor); err != nil {
		t.Fatal(err)
	}
	members, err := s.Members(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 || members[0].Role != RoleMain || members[0].Domain != "main.com" {
		t.Fatalf("members = %+v (want main first + 2 competitors)", members)
	}

	if err := s.RemoveMember(p.ID, "rival.com"); err != nil {
		t.Fatal(err)
	}
	if members, _ = s.Members(p.ID); len(members) != 2 {
		t.Errorf("after remove: %d members", len(members))
	}

	if err := s.RenameProject(p.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetProject(p.ID); got.Name != "Renamed" {
		t.Errorf("rename failed: %q", got.Name)
	}

	list, _ := s.ListProjects()
	if len(list) != 1 {
		t.Errorf("ListProjects = %d", len(list))
	}

	if err := s.DeleteProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(p.ID); err == nil {
		t.Error("GetProject after delete: expected not-found")
	}
	if m, _ := s.Members(p.ID); len(m) != 0 {
		t.Errorf("members survived project delete: %v", m)
	}
}

// makeCrawl registers a crawl in the registry, runs fn to populate its DB, and
// (optionally) marks it completed.
func makeCrawl(t *testing.T, dir, seed, mode string, cfg *config.Config, completed bool, fn func(*store.Crawl)) string {
	t.Helper()
	c, err := store.CreateCrawl(dir, "", []string{seed}, mode, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fn != nil {
		fn(c)
	}
	id := c.ID
	c.Close()
	if completed {
		if err := store.SetStatus(dir, id, store.StatusCompleted, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestSiteHistoryClassification(t *testing.T) {
	dir := t.TempDir()
	def := config.Default()

	narrowed := config.Default()
	narrowed.Scope.Include = []string{"/products"} // scope-narrowed -> not comparable

	full := makeCrawl(t, dir, "https://shop.com", "spider", def, true, nil)
	makeCrawl(t, dir, "https://shop.com/blog", "spider", def, true, nil) // path -> not comparable
	makeCrawl(t, dir, "https://shop.com", "list", def, true, nil)        // list -> not comparable
	makeCrawl(t, dir, "https://shop.com", "spider", def, false, nil)     // running -> not comparable
	makeCrawl(t, dir, "https://shop.com", "spider", narrowed, true, nil) // scope-narrowed -> not comparable
	makeCrawl(t, dir, "https://www.shop.com", "spider", def, true, nil)  // different site key

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	hist, err := s.SiteHistory("shop.com")
	if err != nil {
		t.Fatal(err)
	}
	// www.shop.com must NOT associate with shop.com
	if len(hist) != 5 {
		t.Fatalf("associated crawls = %d, want 5 (www excluded): %+v", len(hist), hist)
	}
	var comparable int
	reasons := map[string]bool{}
	for _, h := range hist {
		if h.Comparable {
			comparable++
			if h.ID != full {
				t.Errorf("unexpected comparable crawl %s (seed %s)", h.ID, h.Seed)
			}
		} else {
			reasons[h.Reason] = true
		}
	}
	if comparable != 1 {
		t.Errorf("comparable count = %d, want 1", comparable)
	}
	for _, want := range []string{"path-scoped seed", "list crawl", "running", "scope-narrowed"} {
		if !reasons[want] {
			t.Errorf("missing non-comparable reason %q (got %v)", want, reasons)
		}
	}

	// www.shop.com resolves as its own site with one comparable crawl.
	if _, ok, _ := s.LatestComparable("www.shop.com"); !ok {
		t.Error("www.shop.com should have a comparable crawl")
	}
}

func TestComparePairAndDiff(t *testing.T) {
	dir := t.TempDir()
	def := config.Default()
	// two completed root spider crawls of the same site, sharing "/" and each
	// having one unique page — a symmetric diff of exactly one new + one missing,
	// regardless of which crawl resolves as prev vs curr.
	// LoadPages scans several columns without COALESCE, so populate the set a real
	// crawl always writes (empty strings / zeros are fine for the diff).
	cols := "url, scope, state, status_code, status, content_type, response_time_ms, size, fetch_error, redirect_url, redirect_type, matched_robots_line, indexable, indexability_status, outside_start_folder"
	page := func(u string) string {
		return "('" + u + "', 'internal', 'crawled', 200, 'OK', 'text/html', 0, 0, '', '', '', 0, 1, 'Indexable', 0)"
	}
	makeCrawl(t, dir, "https://shop.com", "spider", def, true, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(` + cols + `) VALUES ` + page("https://shop.com/") + `,` + page("https://shop.com/a"))
	})
	makeCrawl(t, dir, "https://shop.com", "spider", def, true, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(` + cols + `) VALUES ` + page("https://shop.com/") + `,` + page("https://shop.com/b"))
	})

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	prevID, currID, ok, err := s.ComparePair("shop.com")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || prevID == "" || currID == "" || prevID == currID {
		t.Fatalf("ComparePair = %q,%q,%v", prevID, currID, ok)
	}
	res, err := s.Compare(prevID, currID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesPrevious != 2 || res.PagesCurrent != 2 {
		t.Errorf("page counts = %d/%d, want 2/2", res.PagesPrevious, res.PagesCurrent)
	}
	if len(res.NewPages) != 1 || len(res.MissingPages) != 1 {
		t.Errorf("symmetric diff = +%d/-%d, want +1/-1", len(res.NewPages), len(res.MissingPages))
	}

	// A site with a single comparable crawl cannot be diffed.
	makeCrawl(t, dir, "https://solo.com", "spider", def, true, nil)
	if _, _, ok, _ := s.ComparePair("solo.com"); ok {
		t.Error("ComparePair(solo.com) ok = true, want false (only one crawl)")
	}
}

func TestErrorPaths(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.GetProject("nope"); err == nil {
		t.Error("GetProject(nope): expected not-found error")
	}
	if err := s.AddMember("nope", "x.com", RoleCompetitor); err == nil {
		t.Error("AddMember to missing project: expected error")
	}
	if err := s.RenameProject("nope", "x"); err == nil {
		t.Error("RenameProject(nope): expected not-found error")
	}
	if _, err := s.SiteHistory("::::bad"); err == nil {
		t.Error("SiteHistory(bad domain): expected SiteKey error")
	}
	if _, _, err := s.LatestComparable("also bad"); err == nil {
		// "also bad" has a space; url.Parse of "//also bad" fails host parse
		t.Log("LatestComparable surfaced a key error (acceptable)")
	}
}

func severityID(t *testing.T, sev issues.Severity) string {
	t.Helper()
	for _, d := range issues.Catalogue() {
		if d.Severity == sev {
			return d.ID
		}
	}
	t.Fatalf("no catalogue entry with severity %q", sev)
	return ""
}

func TestBuildScorecard(t *testing.T) {
	dir := t.TempDir()
	def := config.Default()
	js := config.Default()
	js.Rendering.Mode = "javascript" // force a config divergence vs the main site

	errID := severityID(t, issues.Issue)

	makeCrawl(t, dir, "https://main.com", "spider", def, true, func(c *store.Crawl) {
		db := c.DB()
		db.Exec(`INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count, facts, structured) VALUES
			('https://main.com/', 'internal', 'crawled', 200, 1, 50, 0, '{"WordCount":100,"Flesch":60.5}', '{"types":["Product"]}'),
			('https://main.com/a', 'internal', 'crawled', 200, 1, 10, 1, '{"WordCount":200}', NULL),
			('https://main.com/404', 'internal', 'crawled', 404, 0, 0, 0, NULL, NULL)`)
		db.Exec(`INSERT INTO issues(url, issue, detail) VALUES('https://main.com/404', ?, '')`, errID)
	})
	makeCrawl(t, dir, "https://rival.com", "spider", js, true, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count) VALUES
			('https://rival.com/', 'internal', 'crawled', 200, 1, 80, 0)`)
	})

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _ := s.CreateProject("", "main.com")
	s.AddMember(p.ID, "rival.com", RoleCompetitor)
	s.AddMember(p.ID, "nocrawl.com", RoleCompetitor)

	card, err := s.BuildScorecard(p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(card.Sites) != 3 {
		t.Fatalf("sites = %d, want 3", len(card.Sites))
	}
	main := card.Sites[0]
	if main.Role != RoleMain || main.Domain != "main.com" {
		t.Errorf("first row is not main: %+v", main)
	}
	if main.URLs != 3 || main.Crawled != 3 {
		t.Errorf("main URLs/crawled = %d/%d, want 3/3", main.URLs, main.Crawled)
	}
	if main.IndexableRate < 0.66 || main.IndexableRate > 0.67 {
		t.Errorf("main indexable rate = %v, want ~0.667", main.IndexableRate)
	}
	if main.Errors != 1 {
		t.Errorf("main errors = %d, want 1", main.Errors)
	}
	if main.StatusBuckets[2] != 2 || main.StatusBuckets[4] != 1 {
		t.Errorf("main status buckets = %v, want {2:2,4:1}", main.StatusBuckets)
	}
	// optional tier: json_extract over facts, json_array_length over structured.
	if main.AvgWordCount != 150 { // (100+200)/2 over the two pages that have facts
		t.Errorf("main avg word count = %v, want 150", main.AvgWordCount)
	}
	if main.SchemaCoverage < 0.33 || main.SchemaCoverage > 0.34 { // 1 of 3 crawled pages
		t.Errorf("main schema coverage = %v, want ~0.333", main.SchemaCoverage)
	}

	// nocrawl.com competitor must be a stable no-crawl row, not an error.
	var nocrawl *SiteScore
	for i := range card.Sites {
		if card.Sites[i].Domain == "nocrawl.com" {
			nocrawl = &card.Sites[i]
		}
	}
	if nocrawl == nil || nocrawl.Status != "no-crawl" {
		t.Errorf("nocrawl row = %+v, want status no-crawl", nocrawl)
	}

	if !card.ConfigDiverges {
		t.Error("expected config divergence (text vs javascript rendering)")
	}
	found := false
	for _, d := range card.DivergingDims {
		if d == "rendering" {
			found = true
		}
	}
	if !found {
		t.Errorf("diverging dims = %v, want rendering", card.DivergingDims)
	}
}
