package project

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// pageCols is the column set a real crawl always populates; LoadPages reads
// several columns without COALESCE so the diff/aggregate paths need them set.
const pageCols = "url, scope, state, status_code, status, content_type, response_time_ms, size, fetch_error, redirect_url, redirect_type, matched_robots_line, indexable, indexability_status, outside_start_folder"

func pageVals(u string) string {
	return "('" + u + "', 'internal', 'crawled', 200, 'OK', 'text/html', 0, 0, '', '', '', 0, 1, 'Indexable', 0)"
}

// TestSeedKeyAndRootSeedEdges exercises the parse-failure and non-root branches
// of seedKey/isRootSeed that the happy-path tests don't reach.
func TestSeedKeyAndRootSeedEdges(t *testing.T) {
	if _, ok := seedKey("://bad host"); ok {
		t.Error("seedKey on an unparsable seed should return ok=false")
	}
	if _, ok := seedKey("not-a-url-no-host"); ok {
		t.Error("seedKey on a hostless seed should return ok=false")
	}
	if k, ok := seedKey("HTTPS://Shop.COM/path"); !ok || k != "shop.com" {
		t.Errorf("seedKey lowercases host: %q,%v", k, ok)
	}
	// isRootSeed: control char makes url.Parse fail -> false.
	if isRootSeed("http://x.com/\x7f") {
		t.Error("isRootSeed on an unparsable seed should be false")
	}
	if !isRootSeed("https://x.com") || !isRootSeed("https://x.com/") {
		t.Error("bare host and trailing slash are both root seeds")
	}
	if isRootSeed("https://x.com/deep/path") {
		t.Error("a deep path is not a root seed")
	}
}

// TestSiteKeyErrorBranches covers the empty and unparsable inputs to SiteKey.
func TestSiteKeyErrorBranches(t *testing.T) {
	for _, bad := range []string{"", "   ", "http://[::1", "//"} {
		if k, err := SiteKey(bad); err == nil {
			t.Errorf("SiteKey(%q) = %q, want error", bad, k)
		}
	}
}

// TestStoreErrorsAfterClose drives the DB-error return paths of every Store
// method by operating on a closed database (every query then fails).
func TestStoreErrorsAfterClose(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a real project so SiteKey passes and we reach the DB call that fails.
	p, err := s.CreateProject("", "main.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateProject("", "x.com"); err == nil {
		t.Error("CreateProject on closed DB: want error")
	}
	if _, err := s.ListProjects(); err == nil {
		t.Error("ListProjects on closed DB: want error")
	}
	if _, err := s.GetProject(p.ID); err == nil {
		t.Error("GetProject on closed DB: want error")
	}
	if err := s.RenameProject(p.ID, "new"); err == nil {
		t.Error("RenameProject on closed DB: want error")
	}
	if err := s.DeleteProject(p.ID); err == nil {
		t.Error("DeleteProject on closed DB: want error")
	}
	// AddMember first does GetProject, which fails on the closed DB.
	if err := s.AddMember(p.ID, "rival.com", RoleCompetitor); err == nil {
		t.Error("AddMember on closed DB: want error")
	}
	if err := s.RemoveMember(p.ID, "rival.com"); err == nil {
		t.Error("RemoveMember on closed DB: want error")
	}
	if _, err := s.Members(p.ID); err == nil {
		t.Error("Members on closed DB: want error")
	}
}

// TestMemberValidationErrors covers the SiteKey-rejection branches in
// AddMember/RemoveMember (bad domain) without touching the DB.
func TestMemberValidationErrors(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.CreateProject("", "main.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(p.ID, "   ", RoleCompetitor); err == nil {
		t.Error("AddMember with empty domain: want SiteKey error")
	}
	if err := s.RemoveMember(p.ID, "   "); err == nil {
		t.Error("RemoveMember with empty domain: want SiteKey error")
	}
	// CreateProject with a bad main domain fails at SiteKey before any insert.
	if _, err := s.CreateProject("named", ""); err == nil {
		t.Error("CreateProject with empty main domain: want error")
	}
}

// TestRenameEmptyName covers the empty-name guard in RenameProject.
func TestRenameEmptyName(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	p, _ := s.CreateProject("", "main.com")
	if err := s.RenameProject(p.ID, "   "); err == nil {
		t.Error("RenameProject with blank name: want error")
	}
}

// TestCustomProjectName covers the non-default-name branch of CreateProject and
// the explicit-name path that bypasses the "'s Project" default.
func TestCustomProjectName(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	p, err := s.CreateProject("My Study", "https://main.com/ignored/path")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "My Study" {
		t.Errorf("name = %q, want explicit name kept", p.Name)
	}
	if p.MainDomain != "main.com" {
		t.Errorf("main domain = %q, want canonicalized host", p.MainDomain)
	}
}

// TestScopeNarrowedReadErrors covers scopeNarrowed's error branches: a missing
// crawl (OpenCrawl fails -> not-narrowed) and a crawl with no config meta.
func TestScopeNarrowedReadErrors(t *testing.T) {
	dir := t.TempDir()
	if scopeNarrowed(dir, "does-not-exist") {
		t.Error("scopeNarrowed on a missing crawl must be false (treated not-narrowed)")
	}
	// A real completed crawl with a default (un-narrowed) config is not narrowed.
	id := makeCrawl(t, dir, "https://scope.com", "spider", config.Default(), true, nil)
	if scopeNarrowed(dir, id) {
		t.Error("default-config crawl must not be scope-narrowed")
	}
}

// TestClassifyUnfinishedAndScopeNarrowed covers the "unfinished" default branch
// of classify and the scope-narrowed true path end to end via SiteHistory.
func TestClassifyUnfinishedAndScopeNarrowed(t *testing.T) {
	dir := t.TempDir()
	def := config.Default()
	narrowed := config.Default()
	narrowed.Scope.Include = []string{"/products"}

	good := makeCrawl(t, dir, "https://site.com", "spider", def, true, nil)
	// A crawl left in its initial (non-completed, non-running) status -> "unfinished".
	unfinished := makeCrawl(t, dir, "https://site.com", "spider", def, false, nil)
	store.SetStatus(dir, unfinished, store.StatusInterrupted, 0, 0)
	makeCrawl(t, dir, "https://site.com", "spider", narrowed, true, nil)

	s, _ := Open(dir)
	defer s.Close()
	hist, err := s.SiteHistory("site.com")
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]bool{}
	var comparable int
	for _, h := range hist {
		if h.Comparable {
			comparable++
			if h.ID != good {
				t.Errorf("unexpected comparable crawl %s", h.ID)
			}
		} else {
			reasons[h.Reason] = true
		}
	}
	if comparable != 1 {
		t.Errorf("comparable = %d, want 1", comparable)
	}
	for _, want := range []string{"unfinished", "scope-narrowed"} {
		if !reasons[want] {
			t.Errorf("missing reason %q, got %v", want, reasons)
		}
	}
}

// TestSiteHistoryEmpty verifies a site with no associated crawls yields an empty
// (non-error) history, and ComparePair/LatestComparable report nothing.
func TestSiteHistoryEmpty(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	hist, err := s.SiteHistory("ghost.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 0 {
		t.Errorf("history for unknown site = %d, want 0", len(hist))
	}
	if _, ok, _ := s.LatestComparable("ghost.com"); ok {
		t.Error("LatestComparable on a site with no crawls: want ok=false")
	}
	if _, _, ok, _ := s.ComparePair("ghost.com"); ok {
		t.Error("ComparePair on a site with no crawls: want ok=false")
	}
}

// TestComparePairAndHelperErrors covers ComparePair's domain-error branch and
// Compare/compareInput/crawlConfig error returns when given missing crawl IDs.
func TestComparePairAndHelperErrors(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()

	if _, _, _, err := s.ComparePair("::bad host"); err == nil {
		t.Error("ComparePair with a bad domain: want SiteKey error")
	}
	// compareInput error: prev side missing.
	if _, err := s.Compare("missing-prev", "missing-curr"); err == nil {
		t.Error("Compare with missing prev crawl: want error")
	}
	// crawlConfig error: prev and curr exist as DBs but currID is missing.
	good := makeCrawl(t, dir, "https://c.com", "spider", config.Default(), true, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(` + pageCols + `) VALUES ` + pageVals("https://c.com/"))
	})
	if _, err := s.Compare(good, "missing-curr"); err == nil {
		t.Error("Compare with missing curr crawl: want error")
	}
	// crawlConfig error path standalone.
	if _, err := s.crawlConfig("missing"); err == nil {
		t.Error("crawlConfig on a missing crawl: want error")
	}
}

// TestCompareWithIssues drives compareInput's issue-iteration branch (non-zero
// IssueCounts -> IssueURLs) and a full Compare run that reports an issue diff.
func TestCompareWithIssues(t *testing.T) {
	dir := t.TempDir()
	def := config.Default()
	errID := severityID(t, issues.Issue)

	prev := makeCrawl(t, dir, "https://shop.com", "spider", def, true, func(c *store.Crawl) {
		db := c.DB()
		db.Exec(`INSERT INTO pages(` + pageCols + `) VALUES ` + pageVals("https://shop.com/") + `,` + pageVals("https://shop.com/old"))
		db.Exec(`INSERT INTO issues(url, issue, detail) VALUES('https://shop.com/old', ?, '')`, errID)
	})
	curr := makeCrawl(t, dir, "https://shop.com", "spider", def, true, func(c *store.Crawl) {
		db := c.DB()
		db.Exec(`INSERT INTO pages(` + pageCols + `) VALUES ` + pageVals("https://shop.com/") + `,` + pageVals("https://shop.com/new"))
		db.Exec(`INSERT INTO issues(url, issue, detail) VALUES('https://shop.com/new', ?, '')`, errID)
	})

	s, _ := Open(dir)
	defer s.Close()
	res, err := s.Compare(prev, curr)
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesPrevious != 2 || res.PagesCurrent != 2 {
		t.Errorf("page counts = %d/%d, want 2/2", res.PagesPrevious, res.PagesCurrent)
	}
	if len(res.NewPages) != 1 || len(res.MissingPages) != 1 {
		t.Errorf("page diff = +%d/-%d, want +1/-1", len(res.NewPages), len(res.MissingPages))
	}
}

// TestBuildScorecardErrors covers BuildScorecard's not-found project branch.
func TestBuildScorecardErrors(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()
	if _, err := s.BuildScorecard("nope", false); err == nil {
		t.Error("BuildScorecard on a missing project: want not-found error")
	}
}

// TestDivergenceDimensions exercises the max_depth, max_urls and robots
// divergence branches (the existing scorecard test only covers rendering), plus
// the <2-scored short-circuit, and the includeOptional=false path of scoreCrawl.
func TestDivergenceDimensions(t *testing.T) {
	dir := t.TempDir()

	base := config.Default()
	base.Limits.MaxDepth = 3
	base.Limits.MaxURLs = 1000
	base.Robots.Mode = "respect"

	other := config.Default()
	other.Limits.MaxDepth = 7    // -> max_depth diverges
	other.Limits.MaxURLs = 2000  // -> max_urls diverges
	other.Robots.Mode = "ignore" // -> robots diverges
	// rendering stays "text" in both -> rendering must NOT diverge

	page := func(u string) string {
		return `INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count) VALUES('` + u + `', 'internal', 'crawled', 200, 1, 40, 0)`
	}
	makeCrawl(t, dir, "https://main.com", "spider", base, true, func(c *store.Crawl) { c.DB().Exec(page("https://main.com/")) })
	makeCrawl(t, dir, "https://rival.com", "spider", other, true, func(c *store.Crawl) { c.DB().Exec(page("https://rival.com/")) })

	s, _ := Open(dir)
	defer s.Close()
	p, _ := s.CreateProject("", "main.com")
	s.AddMember(p.ID, "rival.com", RoleCompetitor)

	// includeOptional=false -> HasOptional stays false, optional aggregates skipped.
	card, err := s.BuildScorecard(p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !card.ConfigDiverges {
		t.Fatal("expected config divergence")
	}
	got := map[string]bool{}
	for _, d := range card.DivergingDims {
		got[d] = true
	}
	for _, want := range []string{"max_depth", "max_urls", "robots"} {
		if !got[want] {
			t.Errorf("missing diverging dim %q, got %v", want, card.DivergingDims)
		}
	}
	if got["rendering"] {
		t.Error("rendering should not diverge (both text)")
	}
	for _, row := range card.Sites {
		if row.Status == "ok" && row.HasOptional {
			t.Errorf("HasOptional should be false when includeOptional=false: %+v", row)
		}
	}
}

// TestScoreCrawlSeverityBuckets covers scoreCrawl's per-severity issue tally —
// the Warning and Opportunity branches plus the unknown-issue (Lookup !ok) skip
// that the existing scorecard test (Issue-only) never reaches.
func TestScoreCrawlSeverityBuckets(t *testing.T) {
	dir := t.TempDir()
	issueID := severityID(t, issues.Issue)
	warnID := severityID(t, issues.Warning)
	oppID := severityID(t, issues.Opportunity)

	makeCrawl(t, dir, "https://main.com", "spider", config.Default(), true, func(c *store.Crawl) {
		db := c.DB()
		db.Exec(`INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count) VALUES
			('https://main.com/', 'internal', 'crawled', 200, 1, 50, 0),
			('https://main.com/a', 'internal', 'crawled', 200, 1, 10, 0),
			('https://main.com/b', 'internal', 'crawled', 200, 1, 10, 0),
			('https://main.com/c', 'internal', 'crawled', 200, 1, 10, 0)`)
		db.Exec(`INSERT INTO issues(url, issue, detail) VALUES
			('https://main.com/a', ?, ''),
			('https://main.com/b', ?, ''),
			('https://main.com/c', ?, ''),
			('https://main.com/', 'totally_unknown_issue_id', '')`, issueID, warnID, oppID)
	})

	s, _ := Open(dir)
	defer s.Close()
	p, _ := s.CreateProject("", "main.com")
	card, err := s.BuildScorecard(p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	row := card.Sites[0]
	if row.Errors != 1 {
		t.Errorf("errors = %d, want 1", row.Errors)
	}
	if row.Warnings != 1 {
		t.Errorf("warnings = %d, want 1", row.Warnings)
	}
	if row.Opportunities != 1 {
		t.Errorf("opportunities = %d, want 1", row.Opportunities)
	}
}

// TestDivergenceSingleScored covers the <2-scored short-circuit in divergence
// (only the main site has a comparable crawl, the competitor has none).
func TestDivergenceSingleScored(t *testing.T) {
	dir := t.TempDir()
	makeCrawl(t, dir, "https://main.com", "spider", config.Default(), true, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(url, scope, state, status_code, indexable) VALUES('https://main.com/', 'internal', 'crawled', 200, 1)`)
	})
	s, _ := Open(dir)
	defer s.Close()
	p, _ := s.CreateProject("", "main.com")
	s.AddMember(p.ID, "nocrawl.com", RoleCompetitor)
	card, err := s.BuildScorecard(p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if card.ConfigDiverges || len(card.DivergingDims) != 0 {
		t.Errorf("with one scored site, divergence must be false: %v", card.DivergingDims)
	}
}
