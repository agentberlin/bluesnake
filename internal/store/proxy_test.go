package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

// The middle link of the attribution chain (D7/REQ-S7): a recorded egress must
// survive the store, or the column exists and is always empty.
func TestPageProxyRoundTrips(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	want := map[string]string{
		"https://ex.com/via-proxy": "http://brd.superproxy.io:44445",
		"https://ex.com/direct":    "direct",
		"https://ex.com/unset":     "", // a page stored before proxy support
	}
	for url, proxy := range want {
		if err := c.Page(&crawler.PageRecord{
			URL: url, Scope: "internal", State: crawler.StateCrawled, Proxy: proxy,
		}); err != nil {
			t.Fatalf("Page(%s): %v", url, err)
		}
	}

	pages, err := c.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	for url, proxy := range want {
		rec, ok := pages[url]
		if !ok {
			t.Fatalf("%s missing after reload", url)
		}
		if rec.Proxy != proxy {
			t.Errorf("%s proxy = %q, want %q", url, rec.Proxy, proxy)
		}
	}
}

// A page whose proxy column is NULL — every row written by a binary older than
// this feature — must load as empty rather than failing the scan, or the first
// analyze pass over an upgraded database dies.
func TestPageProxyNullScansAsEmpty(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Only `proxy` is left NULL: the other numeric columns are not COALESCEd by
	// loadPages either, so leaving them unset would fail the scan for a reason
	// that has nothing to do with this column.
	if _, err := c.db.Exec(`INSERT INTO pages(
		url, scope, state, depth, status_code, status, content_type, http_version,
		response_time_ms, size, fetch_error, redirect_url, redirect_type,
		matched_robots_line, indexable, indexability_status, inlinks,
		outside_start_folder, link_score, unique_inlinks, unique_outlinks,
		closest_similarity, proxy)
		VALUES(?,?,?,0,200,'OK','text/html','HTTP/1.1',0,0,'','','',0,1,'Indexable',0,0,0,0,0,0,NULL)`,
		"https://ex.com/legacy", "internal", crawler.StateCrawled); err != nil {
		t.Fatal(err)
	}
	pages, err := c.LoadPages()
	if err != nil {
		t.Fatalf("LoadPages over a NULL proxy column: %v", err)
	}
	if rec := pages["https://ex.com/legacy"]; rec == nil || rec.Proxy != "" {
		t.Errorf("legacy row = %+v, want an empty Proxy", rec)
	}
}

// Migration v6 is the only thing standing between an existing crawl database
// and "no such column: proxy" on every page insert. The fresh-schema CREATE
// already carries the column, so the step is ONLY exercised by an upgrade —
// which is exactly why it needs its own test rather than riding the fresh path.
func TestMigrationAddsProxyToAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pre-proxy.db")

	// A crawl DB as it looked before this feature: the current schema minus the
	// proxy column, stamped at the schema floor.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE pages(
		url TEXT PRIMARY KEY, scope TEXT, state TEXT, depth INT,
		status_code INT, status TEXT, content_type TEXT, http_version TEXT,
		response_time_ms INT, size INT, fetch_error TEXT,
		redirect_url TEXT, redirect_type TEXT, matched_robots_line INT,
		indexable INT, indexability_status TEXT,
		inlinks INT DEFAULT 0, discovered_from TEXT, outside_start_folder INT,
		link_score REAL DEFAULT 0, unique_inlinks INT DEFAULT 0, unique_outlinks INT DEFAULT 0,
		closest_similarity REAL DEFAULT 0, near_dup_count INT DEFAULT 0,
		duplicate_of TEXT, minhash BLOB,
		headers JSON, structured JSON, jsdiff JSON, facts JSON)`); err != nil {
		t.Fatal(err)
	}
	if err := setUserVersion(db, minCrawlVersion); err != nil {
		t.Fatal(err)
	}
	if has, err := columnExists(db, "pages", "proxy"); err != nil || has {
		t.Fatalf("pre-migration fixture already has the column (err=%v) — the test proves nothing", err)
	}

	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	has, err := columnExists(db, "pages", "proxy")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("migration did not add pages.proxy — an upgraded crawl would fail every page insert")
	}

	// And the upgraded table actually accepts and returns the value.
	if _, err := db.Exec(
		`INSERT INTO pages(url, scope, state, proxy) VALUES(?,?,?,?)`,
		"https://ex.com/", "internal", crawler.StateCrawled, "http://p:8080"); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
	var got string
	if err := db.QueryRow(`SELECT proxy FROM pages WHERE url = ?`, "https://ex.com/").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "http://p:8080" {
		t.Errorf("proxy = %q after migration", got)
	}

	// Re-running is a no-op, not an error: an already-upgraded DB is reopened
	// on every crawl.
	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Errorf("re-upgrade: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
