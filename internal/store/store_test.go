package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", cfg)
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

	// frontier round trip (Admit is the admission authority — the durable write)
	if _, err := c.Admit(frontier.Item{URL: "https://ex.com/a", Depth: 1, Source: "https://ex.com/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admit(frontier.Item{URL: "https://ex.com/b", Depth: 1}); err != nil {
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
	if len(infos) != 1 || infos[0].ID != c.ID || infos[0].Status != StatusRunning {
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

// TestCounts pins Counts() to the crawler's own tally semantics (crawler.go:
// Result.Total = len(pages) over every recorded state; Result.Crawled = only
// state=="crawled"). finalize relies on this to persist authoritative,
// resume-correct registry counts, so a WHERE-clause drift here would silently
// regress fresh-crawl counts too.
func TestCounts(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	states := map[string]string{
		"https://ex.com/":      crawler.StateCrawled,
		"https://ex.com/a":     crawler.StateCrawled,
		"https://ex.com/b":     crawler.StateCrawled,
		"https://ex.com/block": crawler.StateBlockedRobots,
		"https://ex.com/err":   crawler.StateError,
		"https://ex.com/big":   crawler.StateSkippedTooLarge,
	}
	for url, state := range states {
		if err := c.Page(&crawler.PageRecord{URL: url, Scope: "internal", State: state}); err != nil {
			t.Fatal(err)
		}
	}

	crawled, total, err := c.Counts()
	if err != nil {
		t.Fatal(err)
	}
	// total counts every recorded URL (encountered); crawled only the fetched.
	if total != 6 {
		t.Errorf("total = %d, want 6 (every recorded state)", total)
	}
	if crawled != 3 {
		t.Errorf("crawled = %d, want 3 (only state=crawled)", crawled)
	}
	// total must agree with PageCount (the other definition of "encountered").
	if n, _ := c.PageCount(); n != total {
		t.Errorf("Counts total %d != PageCount %d", total, n)
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
	st, err := CreateCrawl(dir, []string{srv.URL + "/"}, "spider", cfg)
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
	// Snapshot what phase 1 actually crawled, and the hit counts so far: a page
	// recorded in phase 1 must never be re-fetched on resume. A page whose fetch
	// was aborted mid-flight by the interrupt is NOT recorded (it stays pending),
	// so it is legitimately fetched again — that re-fetch is correct, not a bug.
	p1crawled := map[string]bool{}
	for _, u := range processed {
		p1crawled[strings.TrimPrefix(u, srv.URL)] = true
	}
	mu.Lock()
	hitsP1 := map[string]int{}
	for k, v := range hits {
		hitsP1[k] = v
	}
	mu.Unlock()
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
		// A page crawled in phase 1 must not be fetched again on resume.
		if p1crawled[path] && n > hitsP1[path] {
			t.Errorf("%s was crawled in phase 1 but re-fetched %d more time(s) on resume", path, n-hitsP1[path])
		}
		// A page whose in-flight fetch was aborted by the interrupt is legitimately
		// re-fetched on resume, so at most two hits (one abort + one resume) is ok.
		if n > 2 {
			t.Errorf("%s fetched %d times — at most one abort + one resume fetch is expected", path, n)
		}
	}
}

func TestLoadPagesAndIssues(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
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

	// (inlink/discovered_from persistence is the gated-edges path, covered by
	// edges_sql_test.go's TestEdgesAndDepthSQLMethods over SaveInlinksFromEdges.)

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

// TestIssueMultiDetailPreserved pins that a single page emitting the SAME issue
// id with several DISTINCT details (e.g. a Recipe missing more than one required
// property) keeps every occurrence — the bug was a (url, issue) primary key that
// collapsed them to the last detail. Affected-URL counts must stay per-URL.
func TestIssueMultiDetailPreserved(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	occs := []issues.Occurrence{
		{URL: "https://ex.com/r", IssueID: "structured_validation_error", Detail: "Recipe: missing required property name"},
		{URL: "https://ex.com/r", IssueID: "structured_validation_error", Detail: "Recipe: missing required property image"},
		// exact duplicate occurrence — must collapse, not inflate
		{URL: "https://ex.com/r", IssueID: "structured_validation_error", Detail: "Recipe: missing required property name"},
		{URL: "https://ex.com/a", IssueID: "structured_validation_error", Detail: "Article: missing author"},
	}
	if err := c.SaveIssues(occs); err != nil {
		t.Fatal(err)
	}

	// every DISTINCT (url, issue, detail) survives: 2 on /r + 1 on /a = 3 rows
	var rows int
	c.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE issue = 'structured_validation_error'`).Scan(&rows)
	if rows != 3 {
		t.Errorf("stored detail rows = %d, want 3", rows)
	}

	// affected-URL counts stay per-URL, not per-detail
	counts, err := c.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["structured_validation_error"] != 2 {
		t.Errorf("affected URLs = %d, want 2", counts["structured_validation_error"])
	}
	urls, err := c.IssueURLs("structured_validation_error")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Errorf("IssueURLs = %v, want 2 distinct", urls)
	}

	// both details for /r are individually retrievable
	got := map[string]bool{}
	r, err := c.db.Query(`SELECT detail FROM issues WHERE url = 'https://ex.com/r' AND issue = 'structured_validation_error'`)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var d string
		if err := r.Scan(&d); err != nil {
			t.Fatal(err)
		}
		got[d] = true
	}
	if !got["Recipe: missing required property name"] || !got["Recipe: missing required property image"] {
		t.Errorf("details for /r = %v, want both Recipe properties", got)
	}
}

// TestIssuesDetailPKMigration pins that a crawl DB created with the old
// (url, issue) primary key is rebuilt to (url, issue, detail) on open, without
// losing rows — and that the rebuilt table then accepts multiple distinct
// details for one (url, issue).
func TestIssuesDetailPKMigration(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID

	// Reconstruct a pre-versioning crawl DB: drop the new table and recreate it
	// with the legacy single-detail primary key plus one existing row, and reset
	// user_version to 0 so the open path sees it as an un-stamped old database and
	// runs the migration ladder.
	if _, err := c.db.Exec(`DROP TABLE issues;
		CREATE TABLE issues(url TEXT, issue TEXT, detail TEXT, PRIMARY KEY(url, issue));
		INSERT INTO issues(url, issue, detail)
			VALUES('https://ex.com/', 'structured_validation_error', 'Recipe: missing required property name');
		PRAGMA user_version = 0;`); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening runs the migration.
	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	urls, err := c2.IssueURLs("structured_validation_error")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "https://ex.com/" {
		t.Fatalf("existing row lost in migration: %v", urls)
	}

	// the rebuilt PK now accepts a SECOND distinct detail for the same (url, issue)
	if err := c2.AddIssues([]issues.Occurrence{
		{URL: "https://ex.com/", IssueID: "structured_validation_error", Detail: "Recipe: missing required property image"},
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	c2.db.QueryRow(`SELECT COUNT(*) FROM issues WHERE issue = 'structured_validation_error'`).Scan(&n)
	if n != 2 {
		t.Errorf("after migration, distinct details for one (url, issue) = %d, want 2", n)
	}
	if counts, _ := c2.IssueCounts(); counts["structured_validation_error"] != 1 {
		t.Errorf("affected-URL count after migration = %d, want 1", counts["structured_validation_error"])
	}
	// the migrated DB is now stamped to the current revision, so a later open
	// skips the ladder entirely.
	var ver int
	c2.db.QueryRow(`PRAGMA user_version`).Scan(&ver)
	if want := ladderTop(crawlMigrations); ver != want {
		t.Errorf("user_version after migration = %d, want %d", ver, want)
	}
}

// TestMinhashColumnForwardMigration is the L4 guard (#70 L4 / #71 Group 6): a
// pre-minhash crawl DB (v3, no pages.minhash column) forward-migrates to v4 on
// open — the additive nullable column is added by the migration ladder, the
// existing rows survive (the column reads NULL), and the DB stamps to the ladder
// top. This pins the reconciled §0.3 decision: an additive nullable column rides
// the ladder (no backfill, openable) rather than refusing the old DB.
func TestMinhashColumnForwardMigration(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	if err := c.Page(&crawler.PageRecord{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct a pre-minhash crawl DB: drop the minhash column and stamp the
	// stored revision back to v3 (the version before the column was introduced).
	if _, err := c.db.Exec(`ALTER TABLE pages DROP COLUMN minhash;
		PRAGMA user_version = 3;`); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening runs the v4 additive-column migration.
	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("opening a v3 DB should forward-migrate, not fail: %v", err)
	}
	defer c2.Close()

	has, err := columnExists(c2.db, "pages", "minhash")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("pages.minhash column missing after forward-migration from v3")
	}
	// The pre-existing row survives; the new column reads NULL (no backfill).
	var minhash []byte
	if err := c2.db.QueryRow(`SELECT minhash FROM pages WHERE url = 'https://ex.com/'`).Scan(&minhash); err != nil {
		t.Fatalf("row lost or column unqueryable after migration: %v", err)
	}
	if minhash != nil {
		t.Errorf("migrated column backfilled a value (%v); an additive column must stay NULL", minhash)
	}
	var ver int
	c2.db.QueryRow(`PRAGMA user_version`).Scan(&ver)
	if want := ladderTop(crawlMigrations); ver != want {
		t.Errorf("user_version after migration = %d, want %d", ver, want)
	}
}

// TestFreshDBSchemaVersion pins that newly created databases are stamped to the
// top of their ladder, so a later raised floor can tell a fresh DB (current)
// from a genuinely old un-stamped one (revision 0).
func TestFreshDBSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var v int
	c.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if want := ladderTop(crawlMigrations); v != want {
		t.Errorf("fresh crawl DB user_version = %d, want %d", v, want)
	}

	reg, err := registryDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	var rv int
	reg.QueryRow(`PRAGMA user_version`).Scan(&rv)
	if want := ladderTop(registryMigrations); rv != want {
		t.Errorf("registry DB user_version = %d, want %d", rv, want)
	}
}

// TestUpgradeLadder pins the generic versioned migrator independently of the
// concrete crawl steps: stepwise apply + stamp, idempotent re-open, the
// minVersion floor refusal, and the fresh-DB straight-to-top stamp.
func TestUpgradeLadder(t *testing.T) {
	open := func(name string) *sql.DB {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
	version := func(db *sql.DB) int {
		var v int
		db.QueryRow(`PRAGMA user_version`).Scan(&v)
		return v
	}

	var applied []string
	ladder := []migration{
		{1, "one", func(tx *sql.Tx) error {
			applied = append(applied, "one")
			_, e := tx.Exec(`ALTER TABLE t ADD COLUMN a`)
			return e
		}},
		{2, "two", func(tx *sql.Tx) error {
			applied = append(applied, "two")
			_, e := tx.Exec(`ALTER TABLE t ADD COLUMN b`)
			return e
		}},
	}

	db := open("existing.db")
	if _, err := db.Exec(`CREATE TABLE t(id INTEGER)`); err != nil { // non-fresh, revision 0
		t.Fatal(err)
	}

	// floor refusal: a revision-0 DB with a floor of 2 is rejected, untouched.
	if err := upgrade(db, ladder, 2, false); err == nil {
		t.Error("expected floor refusal for v0 below minVersion 2")
	}
	if len(applied) != 0 || version(db) != 0 {
		t.Fatalf("refused upgrade still mutated: applied=%v v=%d", applied, version(db))
	}

	// normal run: apply both steps and stamp to the top.
	if err := upgrade(db, ladder, 0, false); err != nil {
		t.Fatal(err)
	}
	if version(db) != 2 || len(applied) != 2 {
		t.Fatalf("after upgrade: v=%d applied=%v", version(db), applied)
	}
	// idempotent: a second open re-applies nothing.
	if err := upgrade(db, ladder, 0, false); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 {
		t.Errorf("re-applied steps on current DB: %v", applied)
	}

	// fresh DB: stamped straight to the top without running any step.
	fresh := open("fresh.db")
	applied = nil
	if err := upgrade(fresh, ladder, 0, true); err != nil {
		t.Fatal(err)
	}
	if version(fresh) != 2 || len(applied) != 0 {
		t.Errorf("fresh stamp: v=%d applied=%v, want v=2 with no steps", version(fresh), applied)
	}
}

func TestAnalysisPersistence(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
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
	c, err := CreateCrawl(dir, full, "list", config.Default())
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
	c2, err := CreateCrawl(dir, []string{"https://solo.example/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got, err := c2.Seeds(); err != nil || len(got) != 1 || got[0] != "https://solo.example/" {
		t.Fatalf("spider Seeds() = %v, %v", got, err)
	}

	// an empty seed set is rejected up front
	if _, err := CreateCrawl(dir, nil, "list", config.Default()); err == nil {
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
		st, err := CreateCrawl(dir, []string{seedA, seedB}, "list", cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Page(&crawler.PageRecord{
			URL: seedA, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: 0, Facts: &parse.Facts{},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Admit(frontier.Item{URL: seedB, Depth: 0}); err != nil {
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
		// Reproduce finalize's completed-crawl depth step over the stored graph (the
		// production path; the store package can't import finalize — it imports us).
		links, err := st.LinkRows()
		if err != nil {
			t.Fatal(err)
		}
		redirects, err := st.Redirects()
		if err != nil {
			t.Fatal(err)
		}
		urls, err := st.ProcessedURLs()
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SaveDepthsMap(c.RecomputeDepthsFromLinks(links, redirects, urls, seeds)); err != nil {
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
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// seed three pages with stale depths
	for _, p := range []struct {
		url   string
		depth int
	}{{"https://ex.com/", 0}, {"https://ex.com/a", 5}, {"https://ex.com/b", 5}} {
		rec := &crawler.PageRecord{
			URL: p.url, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: p.depth,
		}
		if err := c.Page(rec); err != nil {
			t.Fatal(err)
		}
	}
	// inlinks baseline (a depth-only write must not disturb it); set it directly,
	// since Page() does not write the inlinks column (the edges path does).
	if _, err := c.db.Exec(`UPDATE pages SET inlinks = 7`); err != nil {
		t.Fatal(err)
	}

	// recomputed depths via the production writer: /a -> 1, /b has no path
	// (NoDepth -> SQL NULL).
	if err := c.SaveDepthsMap(map[string]int{
		"https://ex.com/": 0, "https://ex.com/a": 1, "https://ex.com/b": crawler.NoDepth,
	}); err != nil {
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

// TestLoadPagesLiteAndStreamContentText pins the Phase-2 finalize plumbing:
// LoadPagesLite returns every record with Facts intact but ContentText freed,
// while StreamContentText hands the bodies back one row at a time and LoadPages
// still round-trips them in full.
func TestLoadPagesLiteAndStreamContentText(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	const body = "the quick brown fox jumps over the lazy dog every single day"
	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled, StatusCode: 200,
		Facts: &parse.Facts{
			ContentText: body, WordCount: 12,
			Links: []parse.Link{{Type: parse.Hyperlink, URL: "https://ex.com/a"}},
		},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}

	lite, err := c.LoadPagesLite()
	if err != nil {
		t.Fatal(err)
	}
	lr := lite["https://ex.com/"]
	if lr == nil || lr.Facts == nil {
		t.Fatal("lite record missing facts")
	}
	if lr.Facts.ContentText != "" {
		t.Errorf("LoadPagesLite retained ContentText (%d bytes), want freed", len(lr.Facts.ContentText))
	}
	if len(lr.Facts.Links) != 1 || lr.Facts.WordCount != 12 {
		t.Errorf("lite record lost non-ContentText Facts: links=%d wc=%d", len(lr.Facts.Links), lr.Facts.WordCount)
	}

	full, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	if full["https://ex.com/"].Facts.ContentText != body {
		t.Errorf("LoadPages ContentText = %q, want full body", full["https://ex.com/"].Facts.ContentText)
	}

	got := map[string]string{}
	if err := c.StreamContentText(func(url, text string) error { got[url] = text; return nil }); err != nil {
		t.Fatal(err)
	}
	if got["https://ex.com/"] != body {
		t.Errorf("StreamContentText body = %q, want %q", got["https://ex.com/"], body)
	}
}

// Inlink counts + first-wins/seed-locked discovered_from persistence is the
// gated-edges path (store.SaveInlinksFromEdges), covered by edges_sql_test.go's
// TestEdgesAndDepthSQLMethods and TestRedirectPageEdgePersisted — the in-RAM
// aggregate writer (SaveInlinkSources) it replaced is gone.

// TestDropProjectMigration proves the registry ladder removes the retired
// legacy "project" column from a pre-existing registry while preserving rows.
func TestDropProjectMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Build an OLD-shape registry (crawls carries the legacy project column),
	// stamped at v1 so it is treated as an existing DB that must run step 2.
	if _, err := db.Exec(`
		CREATE TABLE crawls(id TEXT PRIMARY KEY, project TEXT, seed TEXT, mode TEXT, status TEXT,
			started INT, finished INT, crawled INT DEFAULT 0, total INT DEFAULT 0);
		CREATE TABLE brands(host TEXT PRIMARY KEY, logo BLOB, logo_type TEXT, fetched INT);
		PRAGMA user_version = 1;
		INSERT INTO crawls(id, project, seed, mode, status, started)
			VALUES('c1','legacy-label','https://ex.com/','spider','completed', 100);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	reg, err := registryDB(dir) // runs the ladder
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	if has, err := columnExists(reg, "crawls", "project"); err != nil || has {
		t.Fatalf("project column still present after migration (has=%v err=%v)", has, err)
	}
	var seed string
	if err := reg.QueryRow(`SELECT seed FROM crawls WHERE id='c1'`).Scan(&seed); err != nil || seed != "https://ex.com/" {
		t.Fatalf("row lost in migration: seed=%q err=%v", seed, err)
	}
	var v int
	if err := reg.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != ladderTop(registryMigrations) {
		t.Errorf("user_version = %d (err=%v), want %d", v, err, ladderTop(registryMigrations))
	}
}
