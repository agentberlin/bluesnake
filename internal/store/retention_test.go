package store

// Retention round-trips: two values the parser computes and the store must keep
// rather than discard — the verbatim JSON-LD blocks behind a structured-data
// verdict, and the id/class-annotated path behind a link's position label. Both
// exist only at parse time, so a consumer that cannot read them off a stored
// crawl cannot get them at all without re-crawling.

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
)

// Both JSON-LD blocks survive the structured JSON column, in document order.
func TestStoreRoundTripsRawJSONLDInOrder(t *testing.T) {
	c, err := CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	first := `{"@context":"https://schema.org","@type":"Organization","name":"First"}`
	second := `{"@context":"https://schema.org","@type":"WebPage","name":"Second"}`
	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html",
		StructuredData: &structured.PageData{
			Formats: []string{"jsonld"},
			Types:   []string{"Organization", "WebPage"},
			JSONLD:  []string{first, second},
		},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"]
	if got == nil || got.StructuredData == nil {
		t.Fatalf("page = %+v, want stored structured data", got)
	}
	if len(got.StructuredData.JSONLD) != 2 {
		t.Fatalf("JSONLD = %v, want both blocks", got.StructuredData.JSONLD)
	}
	if got.StructuredData.JSONLD[0] != first || got.StructuredData.JSONLD[1] != second {
		t.Errorf("JSONLD = %v, want %q then %q — document order", got.StructuredData.JSONLD, first, second)
	}
}

// PositionPath rides Facts.Links through the facts JSON column AND lands in its
// own links column, so both the bundle (which reads facts) and a SQL consumer
// (which reads links) see it.
func TestStoreRoundTripsLinkPositionPath(t *testing.T) {
	c, err := CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	const posPath = "/html/body/div[@class='site-footer']/a"
	rec := &crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, ContentType: "text/html",
		Facts: &parse.Facts{Links: []parse.Link{{
			Type: parse.Hyperlink, URL: "https://ex.com/imprint", Anchor: "Imprint",
			ElemPath: "//body/div[3]/a", PositionPath: posPath, Position: "footer",
		}}},
	}
	if err := c.Page(rec); err != nil {
		t.Fatal(err)
	}

	var col string
	if err := c.DB().QueryRow(
		`SELECT position_path FROM links WHERE src = ?`, "https://ex.com/").Scan(&col); err != nil {
		t.Fatal(err)
	}
	if col != posPath {
		t.Errorf("links.position_path = %q, want %q", col, posPath)
	}

	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	got := pages["https://ex.com/"]
	if got == nil || got.Facts == nil || len(got.Facts.Links) != 1 {
		t.Fatalf("page = %+v, want one stored link", got)
	}
	if got.Facts.Links[0].PositionPath != posPath {
		t.Errorf("Facts.Links[0].PositionPath = %q, want %q", got.Facts.Links[0].PositionPath, posPath)
	}
	if got.Facts.Links[0].ElemPath != "//body/div[3]/a" {
		t.Errorf("ElemPath = %q — the pure-positional path must be unaffected", got.Facts.Links[0].ElemPath)
	}
}

// Migration v7 is what stands between an existing crawl database and "no such
// column: position_path" on every page insert. The fresh-schema CREATE already
// carries the column, so only an upgrade exercises the step.
func TestMigrationAddsPositionPathToAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-position-path.db")

	// A crawl DB as it looked at v6: the links table without position_path. Only
	// steps above v6 run: v7 ALTERs links, v8 ALTERs sitemap_entries and v9
	// rewrites pages' facts, so the fixture carries those tables too (production
	// runs upgrade() after the schema's CREATE IF NOT EXISTS pass, so every table
	// already exists).
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE links(
		src TEXT, dst TEXT, type TEXT, anchor TEXT, alt TEXT,
		nofollow INT, rel TEXT, target TEXT, path_type TEXT,
		elem_path TEXT, position TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO links(src, dst, type, elem_path, position) VALUES(?,?,?,?,?)`,
		"https://ex.com/", "https://ex.com/a", "hyperlink", "//body/a", "content"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sitemap_entries(sitemap TEXT, url TEXT, PRIMARY KEY(sitemap, url))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE pages(url TEXT PRIMARY KEY, facts JSON)`); err != nil {
		t.Fatal(err)
	}
	if err := setUserVersion(db, 6); err != nil {
		t.Fatal(err)
	}
	if has, err := columnExists(db, "links", "position_path"); err != nil || has {
		t.Fatalf("pre-migration fixture already has the column (err=%v) — the test proves nothing", err)
	}

	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	has, err := columnExists(db, "links", "position_path")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("migration did not add links.position_path — an upgraded crawl would fail every link insert")
	}

	// The pre-existing row serves an empty column rather than erroring: the value
	// cannot be recovered without the DOM, so a re-crawl is the only way to fill it.
	var legacy string
	if err := db.QueryRow(
		`SELECT COALESCE(position_path, '') FROM links WHERE src = ?`, "https://ex.com/").Scan(&legacy); err != nil {
		t.Fatalf("reading the migrated column on a legacy row: %v", err)
	}
	if legacy != "" {
		t.Errorf("legacy position_path = %q, want empty", legacy)
	}

	// And the upgraded table accepts and returns the value.
	if _, err := db.Exec(
		`INSERT INTO links(src, dst, type, position_path) VALUES(?,?,?,?)`,
		"https://ex.com/b", "https://ex.com/c", "hyperlink",
		"/html/body/div[@class='site-footer']/a"); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}

	// Re-running is a no-op, not an error: an already-upgraded DB is reopened on
	// every crawl.
	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Errorf("re-upgrade: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
