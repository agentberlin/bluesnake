package store

// Sitemap entries keep the lastmod each sitemap gave a URL. A lastmod belongs
// to an entry, not to a URL — one URL listed in two sitemaps can carry two
// dates — so it lives on the (sitemap, url) row the crawl already records.

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// sitemapLastmod reads one entry's stored lastmod; valid is false for SQL NULL
// (a row recorded before the column existed).
func sitemapLastmod(t *testing.T, db *sql.DB, sitemap, url string) (lastmod string, valid bool) {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow(`SELECT lastmod FROM sitemap_entries WHERE sitemap = ? AND url = ?`,
		sitemap, url).Scan(&v); err != nil {
		t.Fatalf("reading %s in %s: %v", url, sitemap, err)
	}
	return v.String, v.Valid
}

func TestSitemapEntryKeepsLastmodPerSitemap(t *testing.T) {
	c, err := CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	const main, news = "https://ex.com/sitemap.xml", "https://ex.com/news-sitemap.xml"
	for _, e := range []struct{ sitemap, url, lastmod string }{
		{main, "https://ex.com/a", "2026-01-15"},
		{news, "https://ex.com/a", "2026-03-01T08:00:00+00:00"},
		{main, "https://ex.com/b", ""},           // no <lastmod>: stored as "", not NULL
		{main, "https://ex.com/a", "2027-12-31"}, // listed twice in one sitemap: the first entry wins
	} {
		if err := c.SitemapEntry(e.sitemap, e.url, e.lastmod); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct{ sitemap, url, want string }{
		{main, "https://ex.com/a", "2026-01-15"},
		{news, "https://ex.com/a", "2026-03-01T08:00:00+00:00"},
		{main, "https://ex.com/b", ""},
	} {
		got, valid := sitemapLastmod(t, c.db, tt.sitemap, tt.url)
		if !valid || got != tt.want {
			t.Errorf("%s in %s: lastmod = %q (valid %v), want %q", tt.url, tt.sitemap, got, valid, tt.want)
		}
	}
	// Membership is unchanged: the analysis index still lists each sitemap once.
	idx, err := c.SitemapIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := idx["https://ex.com/a"]; len(got) != 2 {
		t.Errorf("sitemap index for /a = %v, want both sitemaps", got)
	}
}

// A crawl recorded before lastmod was kept has NULL on its rows. Resuming it
// walks the sitemaps again, and that walk fills the NULL in rather than being
// ignored as a duplicate — while a row that already has a value keeps it.
func TestSitemapEntryFillsALegacyNullLastmod(t *testing.T) {
	c, err := CreateCrawl(t.TempDir(), []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	const sm, u = "https://ex.com/sitemap.xml", "https://ex.com/a"
	if _, err := c.db.Exec(`INSERT INTO sitemap_entries(sitemap, url) VALUES(?, ?)`, sm, u); err != nil {
		t.Fatal(err)
	}
	if _, valid := sitemapLastmod(t, c.db, sm, u); valid {
		t.Fatal("fixture row already has a lastmod — the test proves nothing")
	}
	if err := c.SitemapEntry(sm, u, "2026-01-15"); err != nil {
		t.Fatal(err)
	}
	if got, valid := sitemapLastmod(t, c.db, sm, u); !valid || got != "2026-01-15" {
		t.Errorf("legacy row after re-walk: lastmod = %q (valid %v), want the walked value", got, valid)
	}
	if err := c.SitemapEntry(sm, u, "2027-12-31"); err != nil {
		t.Fatal(err)
	}
	if got, _ := sitemapLastmod(t, c.db, sm, u); got != "2026-01-15" {
		t.Errorf("a recorded lastmod was overwritten: %q", got)
	}
}

// Migration v8 is what stands between an existing crawl database and "no such
// column: lastmod" on every sitemap entry. The fresh-schema CREATE already
// carries the column, so only an upgrade exercises the step.
func TestMigrationAddsLastmodToAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-lastmod.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A crawl DB as it looked at v7: sitemap_entries without lastmod. Only the
	// steps above v7 run: v8 ALTERs this table, and v9 rewrites pages' facts.
	if _, err := db.Exec(`CREATE TABLE sitemap_entries(sitemap TEXT, url TEXT, PRIMARY KEY(sitemap, url))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE pages(url TEXT PRIMARY KEY, facts JSON)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sitemap_entries(sitemap, url) VALUES(?, ?)`,
		"https://ex.com/sitemap.xml", "https://ex.com/a"); err != nil {
		t.Fatal(err)
	}
	if err := setUserVersion(db, 7); err != nil {
		t.Fatal(err)
	}
	if has, err := columnExists(db, "sitemap_entries", "lastmod"); err != nil || has {
		t.Fatalf("pre-migration fixture already has the column (err=%v) — the test proves nothing", err)
	}

	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if has, err := columnExists(db, "sitemap_entries", "lastmod"); err != nil || !has {
		t.Fatalf("migration did not add sitemap_entries.lastmod (err=%v)", err)
	}
	// The legacy row keeps its membership and has no lastmod: NULL, which a
	// re-walk fills in (above) and readers render as "".
	if _, valid := sitemapLastmod(t, db, "https://ex.com/sitemap.xml", "https://ex.com/a"); valid {
		t.Error("legacy row gained a lastmod it was never given")
	}
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != 9 {
		t.Errorf("user_version = %d (err %v), want 9", v, err)
	}
	if err := upgrade(db, crawlMigrations, minCrawlVersion, false); err != nil {
		t.Errorf("re-upgrade: %v", err)
	}
}
