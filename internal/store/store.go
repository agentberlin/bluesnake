// Package store persists crawls to per-crawl SQLite databases (WAL mode,
// continuous commit → crash-safe) plus a registry database listing all
// crawls with their IDs and status (DESIGN.md §5.3). It implements
// crawler.Sink so the crawl engine streams pages and frontier mutations into
// the database as it runs, which is what makes pause/resume work.
package store

import (
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/bloom"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/structured"
	"github.com/agentberlin/bluesnake/internal/warc"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// Statuses in the registry.
const (
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusInterrupted = "interrupted"
)

// Info is one registry row.
type Info struct {
	ID       string
	Seed     string
	Mode     string
	Status   string
	Started  time.Time
	Finished time.Time
	Crawled  int // URLs fetched (got a response)
	Total    int // URLs encountered (fetched + robots-blocked + errored)
}

const crawlSchema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS pages(
  url TEXT PRIMARY KEY, scope TEXT, state TEXT, depth INT,
  status_code INT, status TEXT, content_type TEXT, http_version TEXT,
  response_time_ms INT, size INT, fetch_error TEXT,
  redirect_url TEXT, redirect_type TEXT, matched_robots_line INT,
  indexable INT, indexability_status TEXT,
  inlinks INT DEFAULT 0, discovered_from TEXT, outside_start_folder INT,
  link_score REAL DEFAULT 0, unique_inlinks INT DEFAULT 0, unique_outlinks INT DEFAULT 0,
  closest_similarity REAL DEFAULT 0, near_dup_count INT DEFAULT 0,
  duplicate_of TEXT,
  minhash BLOB,
  headers JSON, structured JSON, jsdiff JSON, facts JSON
);
CREATE TABLE IF NOT EXISTS links(
  src TEXT, dst TEXT, type TEXT, anchor TEXT, alt TEXT,
  nofollow INT, rel TEXT, target TEXT, path_type TEXT,
  elem_path TEXT, position TEXT
);
CREATE INDEX IF NOT EXISTS links_src ON links(src);
CREATE INDEX IF NOT EXISTS links_dst ON links(dst);
-- edges is the GATED, REWRITTEN discovery graph (one row per followed edge the
-- crawler actually admitted: src page -> rewritten dst), unlike the raw ungated
-- links table. hyperlink marks the inlink-counting subset; seq is the monotonic
-- discovery order that makes first-wins discovered_from run-to-run stable
-- (MEMORY-SCALING.md §5.5). It lets finalize derive inlinks/discovered_from/depth
-- without re-applying the Go rewrite+filter chain in SQL.
CREATE TABLE IF NOT EXISTS edges(src TEXT, dst TEXT, hyperlink INT, seq INTEGER);
CREATE INDEX IF NOT EXISTS edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS edges_src ON edges(src);
CREATE TABLE IF NOT EXISTS frontier(url TEXT PRIMARY KEY, depth INT, redirect_hops INT, source TEXT);
-- content_hash is the on-disk authority for the raw-body identical-content
-- short-circuit (R8): hash -> the first (canonical) URL that claimed it. It bounds
-- the formerly-unbounded in-RAM seenContent map (MEMORY-SCALING.md §5.4 / #70 M4),
-- preserving first-writer-wins via the hash PRIMARY KEY.
CREATE TABLE IF NOT EXISTS content_hash(hash TEXT PRIMARY KEY, url TEXT);
CREATE TABLE IF NOT EXISTS issues(url TEXT, issue TEXT, detail TEXT, PRIMARY KEY(url, issue, detail));
CREATE TABLE IF NOT EXISTS custom_results(url TEXT, kind TEXT, name TEXT, value TEXT, PRIMARY KEY(url, kind, name));
CREATE TABLE IF NOT EXISTS sitemap_entries(sitemap TEXT, url TEXT, PRIMARY KEY(sitemap, url));
CREATE TABLE IF NOT EXISTS llmstxt(
  url TEXT PRIMARY KEY, kind TEXT, status INT, found INT,
  title TEXT, summary TEXT, malformed INT, content TEXT);
CREATE TABLE IF NOT EXISTS llmstxt_links(
  src TEXT, url TEXT, section TEXT, anchor TEXT, PRIMARY KEY(src, url));
CREATE TABLE IF NOT EXISTS analysis(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS blobs(url TEXT, kind TEXT, path TEXT, PRIMARY KEY(url, kind));
`

// bloomCapacity sizes each crawl's dedup Bloom filter: enough bits for ~1M
// admitted URLs at a 1% false-positive rate (~1.2 MB). It is a RAM-cheap fast
// negative in front of the SQLite authority — a miss skips the DB, a hit (or a
// rare false positive) is confirmed against the tables, so correctness never
// depends on the sizing. Larger crawls just see a higher FP rate (more confirm
// reads), never a wrong dedup decision (MEMORY-SCALING.md §0.4/§7).
const bloomCapacity = 1 << 20

// Crawl is an open per-crawl database.
type Crawl struct {
	ID  string
	dir string
	db  *sql.DB

	// bloom is the fast-negative dedup filter in front of the SQLite authority.
	// Per-crawl and in-memory; a fresh (cold) filter on resume is fine — the
	// authoritative INSERT … WHERE NOT EXISTS(pages) backstops every miss.
	bloom *bloom.Filter

	// WARC archive (extraction.store_warc), created lazily on first Archive.
	// archiveMu guards the lazy init and the writes — the crawler calls
	// Archive from many worker goroutines concurrently.
	archiveMu   sync.Mutex
	archive     *warc.Writer
	archiveFile *os.File
	archivePath string
}

func registryDB(dir string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "registry.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fresh, err := isFreshDB(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS crawls(
			id TEXT PRIMARY KEY, seed TEXT, mode TEXT, status TEXT,
			started INT, finished INT, crawled INT DEFAULT 0, total INT DEFAULT 0);
		CREATE TABLE IF NOT EXISTS brands(
			host TEXT PRIMARY KEY, logo BLOB, logo_type TEXT, fetched INT);
		CREATE TABLE IF NOT EXISTS jobs(
			id TEXT PRIMARY KEY, status TEXT NOT NULL, position INTEGER NOT NULL,
			source TEXT NOT NULL, project_id TEXT, label TEXT, request TEXT NOT NULL,
			crawl_id TEXT, error TEXT, enqueued INTEGER NOT NULL, started INTEGER, finished INTEGER);
		CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status, position);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := upgrade(db, registryMigrations, minRegistryVersion, fresh); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func newCrawlID() string {
	b := make([]byte, 3)
	rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// CreateCrawl registers a new crawl and opens its database, freezing the config
// and the full seed set into it. A list crawl uploads many seeds (all depth 0);
// a spider crawl has exactly one. seeds must be non-empty; resume restores every
// seed so host classification and the depth BFS root from all of them. seeds[0]
// is the registry's representative seed for `crawls ls`.
func CreateCrawl(dir string, seeds []string, mode string, cfg *config.Config) (*Crawl, error) {
	if len(seeds) == 0 {
		return nil, fmt.Errorf("crawl needs at least one seed")
	}
	reg, err := registryDB(dir)
	if err != nil {
		return nil, err
	}
	defer reg.Close()

	id := newCrawlID()
	_, err = reg.Exec(`INSERT INTO crawls(id, seed, mode, status, started) VALUES(?,?,?,?,?)`,
		id, seeds[0], mode, StatusRunning, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	c, err := openCrawlDB(dir, id)
	if err != nil {
		return nil, err
	}
	cfgYAML, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	seedsJSON, err := json.Marshal(seeds)
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]string{
		"config": string(cfgYAML), "seeds": string(seedsJSON), "mode": mode,
	} {
		if _, err := c.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES(?,?)`, key, value); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// OpenCrawl opens an existing crawl database by ID. The id must be a single
// safe path element — never a separator or "..": the network-exposed `serve`
// API passes user-controlled ids straight through, and a traversing id would
// otherwise let it open (and CREATE-TABLE / ALTER into) an arbitrary file.
func OpenCrawl(dir, id string) (*Crawl, error) {
	if !validCrawlID(id) {
		return nil, fmt.Errorf("crawl %q not found", id)
	}
	if _, err := os.Stat(crawlPath(dir, id)); err != nil {
		return nil, fmt.Errorf("crawl %q not found", id)
	}
	return openCrawlDB(dir, id)
}

// validCrawlID rejects ids that aren't a plain filename (path separators,
// "..", empty, or absolute) so a crawl id can never escape the crawls dir.
func validCrawlID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	return id == filepath.Base(id)
}

func crawlPath(dir, id string) string {
	return filepath.Join(dir, "crawls", id+".db")
}

// CrawlDBPath returns the on-disk database path of a stored crawl, with the
// same id validation and existence check as OpenCrawl. It lets read-only
// consumers (the MCP query tool) open their own connection without running
// the schema DDL a writable open performs.
func CrawlDBPath(dir, id string) (string, error) {
	if !validCrawlID(id) {
		return "", fmt.Errorf("crawl %q not found", id)
	}
	path := crawlPath(dir, id)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("crawl %q not found", id)
	}
	return path, nil
}

func openCrawlDB(dir, id string) (*Crawl, error) {
	path := crawlPath(dir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite single-writer; serialize through database/sql
	fresh, err := isFreshDB(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(crawlSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err := upgrade(db, crawlMigrations, minCrawlVersion, fresh); err != nil {
		db.Close()
		return nil, err
	}
	return &Crawl{ID: id, dir: dir, db: db, bloom: bloom.New(bloomCapacity, 0.01)}, nil
}

// --- schema versioning & migrations ---------------------------------------
//
// Crawl and registry databases carry a schema revision in SQLite's built-in
// user_version header slot (zero-cost, durable). openCrawlDB/registryDB CREATE
// the latest shape and then call upgrade(): a fresh database is stamped straight
// to the top of its ladder; an existing one runs only the steps above its stored
// revision. The common case — an already-current database — costs one pragma read.
//
// Each step has a STABLE version number (never renumbered or reordered) and an
// idempotent apply func. Stability plus the minVersion floor are what let us
// later *delete* retired steps without silently half-migrating old data — see
// "Retiring a migration" in DESIGN.md §5.3.
type migration struct {
	version int // the user_version this step brings the database up to
	name    string
	apply   func(*sql.Tx) error
}

// crawlMigrations is the per-crawl-DB ladder. APPEND ONLY.
var crawlMigrations = []migration{
	{1, "pages.http_version", func(tx *sql.Tx) error { return addColumn(tx, "pages", "http_version TEXT") }},
	{2, "pages.duplicate_of", func(tx *sql.Tx) error { return addColumn(tx, "pages", "duplicate_of TEXT") }},
	{3, "issues.detail_in_pk", migrateIssuesDetailPK},
	{4, "pages.minhash", func(tx *sql.Tx) error { return addColumn(tx, "pages", "minhash BLOB") }},
}

// registryMigrations is the ladder for the single shared registry DB. APPEND ONLY.
var registryMigrations = []migration{
	{1, "crawls.total", func(tx *sql.Tx) error { return addColumn(tx, "crawls", "total INT DEFAULT 0") }},
	// The legacy free-text per-crawl "project" label was retired when the
	// first-class project layer (internal/project) landed; drop it from existing
	// registries so it lingers nowhere.
	{2, "crawls.drop_project", dropCrawlsProject},
}

// minCrawlVersion / minRegistryVersion are the oldest revisions we still carry
// steps for: 0 = accept and migrate everything (nothing retired yet). Raising a
// floor to F (and deleting every step with version <= F) makes DBs below F fail
// with a clear re-crawl message instead of running an incomplete ladder.
const (
	minCrawlVersion    = 0
	minRegistryVersion = 0
)

// ladderTop is the latest revision a ladder migrates to (its highest version).
func ladderTop(ladder []migration) int {
	top := 0
	for _, m := range ladder {
		if m.version > top {
			top = m.version
		}
	}
	return top
}

// upgrade brings db to the top of ladder via user_version. A fresh DB (CREATE
// just built the latest shape) is stamped straight to the top; an existing DB
// runs the steps above its stored revision. A non-fresh DB below minVersion is
// refused so a ladder with retired steps never half-migrates old data.
func upgrade(db *sql.DB, ladder []migration, minVersion int, fresh bool) error {
	target := ladderTop(ladder)
	if fresh {
		return setUserVersion(db, target)
	}
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v >= target {
		return nil // already current — the hot path
	}
	if v < minVersion {
		return fmt.Errorf("database schema v%d predates the minimum supported v%d — re-crawl or remove it", v, minVersion)
	}
	for _, m := range ladder {
		if m.version <= v {
			continue
		}
		if err := applyStep(db, m); err != nil {
			return fmt.Errorf("migration %q: %w", m.name, err)
		}
	}
	return nil
}

// applyStep runs one migration and bumps user_version in the SAME transaction,
// so a crash mid-step rolls back atomically — the DB stays at its prior revision
// rather than stranding a half-applied schema.
func applyStep(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.apply(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

func setUserVersion(db *sql.DB, v int) error {
	// user_version takes no bind parameters; v is an in-code constant, not input.
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v))
	return err
}

// addColumn applies an ADD COLUMN that tolerates the column already existing:
// DBs created before user_version tracking may already carry it (from the old
// best-effort ALTERs) while still reporting a stored revision of 0.
func addColumn(tx *sql.Tx, table, colDef string) error {
	if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s`, table, colDef)); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	return nil
}

// dropCrawlsProject removes the retired legacy "project" column from an existing
// registry. Idempotent: a DB already without it (fresh, or migrated) is a no-op,
// so re-running the ladder never fails.
func dropCrawlsProject(tx *sql.Tx) error {
	has, err := columnExists(tx, "crawls", "project")
	if err != nil || !has {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE crawls DROP COLUMN project`)
	return err
}

// columnExists reports whether table has a column named col. table is an in-code
// constant (never user input), so the PRAGMA interpolation is safe.
func columnExists(q queryer, table, col string) (bool, error) {
	rows, err := q.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// isFreshDB reports whether the database has no user tables yet — i.e. this open
// is creating it, so CREATE builds the latest shape and the ladder is moot.
func isFreshDB(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&n)
	return n == 0, err
}

// migrateIssuesDetailPK rebuilds an issues table created with the legacy
// (url, issue) primary key to (url, issue, detail). The old key collapsed every
// occurrence of one issue id on a page to its last detail (an INSERT OR REPLACE
// over the same key); the new key keeps each distinct occurrence — e.g. one row
// per missing required structured-data property. SQLite can't ALTER a primary
// key, so the rebuild copies into a fresh table and swaps it in. applyStep wraps
// this in a transaction; the detail-in-PK guard keeps it idempotent regardless.
func migrateIssuesDetailPK(tx *sql.Tx) error {
	inPK, err := detailInIssuesPK(tx)
	if err != nil || inPK {
		return err // already on the (url, issue, detail) key
	}
	_, err = tx.Exec(`
		CREATE TABLE issues_migrate(url TEXT, issue TEXT, detail TEXT, PRIMARY KEY(url, issue, detail));
		INSERT OR IGNORE INTO issues_migrate(url, issue, detail) SELECT url, issue, detail FROM issues;
		DROP TABLE issues;
		ALTER TABLE issues_migrate RENAME TO issues;`)
	return err
}

// queryer is the read surface shared by *sql.DB and *sql.Tx, so schema
// inspection works inside or outside a transaction.
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// detailInIssuesPK reports whether the issues table's primary key already
// includes the detail column (the post-fix shape). It reads the actual key from
// the schema rather than string-matching the DDL, so it is robust to formatting.
// PRAGMA table_info's pk column is the 1-based position of a column within the
// primary key, 0 when the column is not part of it.
func detailInIssuesPK(q queryer) (bool, error) {
	rows, err := q.Query(`PRAGMA table_info(issues)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == "detail" && pk > 0 {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (c *Crawl) Close() error {
	c.archiveMu.Lock()
	if c.archiveFile != nil {
		c.archive.Close()
		c.archiveFile.Close()
		c.archiveFile = nil
	}
	c.archiveMu.Unlock()
	return c.db.Close()
}

// Archive implements crawler.ArchiveSink: fetched responses stream into
// <crawl-id>.assets/archive.warc.gz (one gzip member per record), created
// lazily with a leading warcinfo record. Safe for concurrent use — the
// crawler calls it from every worker goroutine.
func (c *Crawl) Archive(url string, res *fetch.Result) error {
	c.archiveMu.Lock()
	defer c.archiveMu.Unlock()
	if c.archive == nil {
		dir := filepath.Join(c.dir, "crawls", c.ID+".assets")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "archive.warc.gz")
		// O_APPEND so resuming a crawl (a fresh *Crawl over the same id)
		// extends the existing archive instead of truncating it; gzip
		// members concatenate, which is the standard .warc.gz layout.
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		c.archiveFile = f
		c.archivePath = path
		c.archive = warc.NewWriter(f)
		if info.Size() == 0 { // only the first writer emits the warcinfo record
			if err := c.archive.WriteWarcinfo(map[string]string{
				"software": "bluesnake",
				"format":   "WARC File Format 1.1",
			}); err != nil {
				return err
			}
		}
	}
	proto := res.HTTPVersion
	if proto == "" {
		proto = "HTTP/1.1"
	}
	return c.archive.WriteResponse(url, res.StatusCode, proto, res.Headers, res.Body)
}

// ArchivePath returns the WARC archive location ("" when nothing was archived).
func (c *Crawl) ArchivePath() string { return c.archivePath }

// DB exposes the underlying handle for the analyze/export/report layers.
func (c *Crawl) DB() *sql.DB { return c.db }

// Meta returns a meta value ("config", "seed", "mode", ...).
func (c *Crawl) Meta(key string) (string, error) {
	var v string
	err := c.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (c *Crawl) SetMeta(key, value string) error {
	_, err := c.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES(?,?)`, key, value)
	return err
}

// Seeds returns the crawl's seed URLs, as frozen by CreateCrawl: every uploaded
// URL for a list crawl, the single start URL for a spider crawl. Resume restores
// the whole set so seed-host classification and the depth BFS root from every
// seed.
func (c *Crawl) Seeds() ([]string, error) {
	raw, err := c.Meta("seeds")
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var seeds []string
	if err := json.Unmarshal([]byte(raw), &seeds); err != nil {
		return nil, err
	}
	return seeds, nil
}

// --- crawler.Sink implementation ---

func (c *Crawl) Page(rec *crawler.PageRecord) error {
	var factsJSON []byte
	if rec.Facts != nil {
		var err error
		if factsJSON, err = json.Marshal(rec.Facts); err != nil {
			return err
		}
	}
	var headersJSON, structuredJSON []byte
	if len(rec.Headers) > 0 {
		var err error
		if headersJSON, err = json.Marshal(rec.Headers); err != nil {
			return err
		}
	}
	if rec.StructuredData != nil {
		var err error
		if structuredJSON, err = json.Marshal(rec.StructuredData); err != nil {
			return err
		}
	}
	var jsdiffJSON []byte
	if rec.JSDiff != nil {
		var err error
		if jsdiffJSON, err = json.Marshal(rec.JSDiff); err != nil {
			return err
		}
	}
	_, err := c.db.Exec(`INSERT OR REPLACE INTO pages
		(url, scope, state, depth, status_code, status, content_type, http_version,
		 response_time_ms, size, fetch_error, redirect_url, redirect_type,
		 matched_robots_line, indexable, indexability_status,
		 discovered_from, outside_start_folder, duplicate_of, minhash, headers, structured, jsdiff, facts)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.URL, rec.Scope, rec.State, rec.Depth, rec.StatusCode, rec.Status,
		rec.ContentType, rec.HTTPVersion, rec.ResponseTimeMs, rec.Size, rec.FetchError,
		rec.RedirectURL, rec.RedirectType, rec.MatchedRobotsLine,
		boolInt(rec.Indexable), rec.IndexabilityStatus,
		rec.DiscoveredFrom, boolInt(rec.OutsideStartFolder), rec.DuplicateOf, minhashBlob(rec.Minhash), headersJSON, structuredJSON, jsdiffJSON, factsJSON)
	if err != nil {
		return err
	}
	for _, cr := range rec.CustomResults {
		if _, err := c.db.Exec(`INSERT OR REPLACE INTO custom_results(url, kind, name, value) VALUES(?,?,?,?)`,
			rec.URL, cr.Kind, cr.Name, cr.Value); err != nil {
			return err
		}
	}
	// links come from Facts.Links (HTML only); the gated discovery edges ride
	// rec.GatedEdges. A redirect page has a GatedEdge (its redirect target) but no
	// Facts, so the edges write must NOT be gated on Facts — otherwise the redirect
	// target's first-wins discovered_from is lost from the edges table, which is
	// the sole source for SaveInlinksFromEdges in the SQL finalize (#70 H5).
	if rec.Facts == nil && len(rec.GatedEdges) == 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if rec.Facts != nil {
		if _, err := tx.Exec(`DELETE FROM links WHERE src = ?`, rec.URL); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO links
			(src, dst, type, anchor, alt, nofollow, rel, target, path_type, elem_path, position)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, l := range rec.Facts.Links {
			if _, err := stmt.Exec(rec.URL, l.URL, string(l.Type), l.Anchor, l.Alt,
				boolInt(l.Nofollow), l.Rel, l.Target, l.PathType, l.ElemPath, l.Position); err != nil {
				return err
			}
		}
	}
	// The gated/rewritten discovery edges (re-crawl replaces them, like links).
	if _, err := tx.Exec(`DELETE FROM edges WHERE src = ?`, rec.URL); err != nil {
		return err
	}
	if len(rec.GatedEdges) > 0 {
		estmt, err := tx.Prepare(`INSERT INTO edges(src, dst, hyperlink, seq) VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer estmt.Close()
		for _, e := range rec.GatedEdges {
			if _, err := estmt.Exec(rec.URL, e.Dst, boolInt(e.Hyperlink), e.Seq); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (c *Crawl) FrontierAdd(it frontier.Item) error {
	_, err := c.db.Exec(`INSERT OR IGNORE INTO frontier(url, depth, redirect_hops, source) VALUES(?,?,?,?)`,
		it.URL, it.Depth, it.RedirectHops, it.Source)
	return err
}

func (c *Crawl) FrontierDone(url string) error {
	_, err := c.db.Exec(`DELETE FROM frontier WHERE url = ?`, url)
	return err
}

// --- frontier.Dedup: the on-disk visited-set authority -----------------------
// These let the crawler drop its in-memory dedup set: a URL is "seen" iff it is
// an un-done frontier row OR a crawled pages row, derivable from the tables the
// store already writes (MEMORY-SCALING.md §5.1). Admit is the atomic, exactly-
// once gate; it runs OUTSIDE the frontier's cap mutex, so its (microsecond,
// WAL-cached) DB work never serialises the in-memory cap accounting.

// Admit records the URL as a frontier row iff it is novel — neither an existing
// frontier row (the url PRIMARY KEY) nor a crawled pages row — returning
// firstSeen=true only on the first admission. The WHERE-NOT-EXISTS(pages) clause
// is what stops a re-discovered, already-crawled URL from being re-admitted after
// FrontierDone deleted its frontier row (EC-14).
func (c *Crawl) Admit(it frontier.Item) (bool, error) {
	// Bloom fast-negative (MEMORY-SCALING.md §5.1/§7): a miss is a guarantee the
	// URL was never admitted, so go straight to the authoritative insert. A hit is
	// only "maybe seen" — the high-frequency re-discovery case — so confirm cheaply
	// against the tables and reject a true duplicate with a READ instead of a
	// serialized INSERT write-attempt (the win under WAL + single-writer conn + M
	// parallel crawls). A rare false positive falls through to the same exact
	// insert, so the Bloom can never drop a novel URL or re-admit a seen one — the
	// DB PK + WHERE NOT EXISTS(pages) stay the authority.
	if c.bloom != nil && c.bloom.Has(it.URL) {
		seen, err := c.Seen(it.URL)
		if err != nil {
			return false, err
		}
		if seen {
			return false, nil
		}
	}
	res, err := c.db.Exec(
		`INSERT OR IGNORE INTO frontier(url, depth, redirect_hops, source)
		 SELECT ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM pages WHERE url = ?)`,
		it.URL, it.Depth, it.RedirectHops, it.Source, it.URL)
	if err != nil {
		return false, err
	}
	if c.bloom != nil {
		c.bloom.Add(it.URL)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Remove undoes a just-admitted frontier row (the frontier's cap-overflow
// rollback). It is the same delete as FrontierDone but named for the dedup role.
func (c *Crawl) Remove(url string) error {
	_, err := c.db.Exec(`DELETE FROM frontier WHERE url = ?`, url)
	return err
}

// Seen reports whether the URL is already known (a frontier or pages row).
func (c *Crawl) Seen(url string) (bool, error) {
	var seen bool
	err := c.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM frontier WHERE url=?) OR EXISTS(SELECT 1 FROM pages WHERE url=?)`,
		url, url).Scan(&seen)
	return seen, err
}

// MarkSeen is a no-op for the on-disk authority: a resume's already-processed
// URLs ARE the pages rows, so the authority already knows them — there is no
// in-memory set to preseed.
func (c *Crawl) MarkSeen([]string) error { return nil }

// Count returns the number of distinct admitted URLs (frontier ∪ pages).
func (c *Crawl) Count() (int, error) {
	var n int
	err := c.db.QueryRow(
		`SELECT COUNT(*) FROM (SELECT url FROM frontier UNION SELECT url FROM pages)`).Scan(&n)
	return n, err
}

// FirstWithContent is the on-disk authority for the raw-body identical-content
// short-circuit (R8), bounding the formerly-unbounded in-RAM seenContent map
// (#70 M4). It reports the canonical URL for a content hash and whether url is the
// first page seen with it. claim=false (a page that will NOT expand) never records
// itself as canonical — so it can't shadow a later in-folder twin's outlinks —
// exactly mirroring the in-RAM firstWithContent. First-writer-wins under races is
// preserved by the hash PRIMARY KEY: concurrent claimers all INSERT OR IGNORE then
// read back the single winner.
func (c *Crawl) FirstWithContent(hash, url string, claim bool) (canonical string, first bool, err error) {
	var existing string
	switch err = c.db.QueryRow(`SELECT url FROM content_hash WHERE hash = ?`, hash).Scan(&existing); err {
	case nil:
		return existing, false, nil // already claimed
	case sql.ErrNoRows:
		// novel hash so far
	default:
		return "", false, err
	}
	if !claim {
		return url, true, nil // first, but does not record itself (won't expand)
	}
	if _, err = c.db.Exec(`INSERT OR IGNORE INTO content_hash(hash, url) VALUES(?, ?)`, hash, url); err != nil {
		return "", false, err
	}
	var winner string
	if err = c.db.QueryRow(`SELECT url FROM content_hash WHERE hash = ?`, hash).Scan(&winner); err != nil {
		return "", false, err
	}
	return winner, winner == url, nil
}

// --- resume support ---

// PendingFrontier returns the admitted-but-unprocessed items. It excludes any
// frontier row whose URL already has a pages row: a crash between Page() and
// FrontierDone() (two non-atomic writes) can leave that stale pair, and returning
// it would make a resume re-fetch an already-crawled page — a wasted round-trip
// and a double-charge against MaxURLs (EC-02). The WHERE-NOT-EXISTS guard mirrors
// Admit's, so the resume frontier carries exactly the genuinely-unprocessed tail.
func (c *Crawl) PendingFrontier() ([]frontier.Item, error) {
	rows, err := c.db.Query(`SELECT url, depth, redirect_hops, source FROM frontier
		WHERE NOT EXISTS (SELECT 1 FROM pages WHERE pages.url = frontier.url)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		if err := rows.Scan(&it.URL, &it.Depth, &it.RedirectHops, &it.Source); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// AdmittedItems returns every URL the crawl has admitted, with its admit-time
// depth — the union of crawled pages and pending frontier rows. The two sets are
// disjoint: Admit refuses a URL that already has a pages row, and FrontierDone
// drops a frontier row the moment its page is recorded. Resume replays these
// through the frontier's per-bucket counters (RehydrateCounters) so a resumed
// crawl enforces MaxURLsPerDepth / per-subdomain / per-path caps against the same
// running totals a straight crawl had, instead of granting a fresh bucket budget
// per session (FR-08 / MEMORY-SCALING.md §5.1). A resume only ever follows an
// interrupted crawl, whose depths are still admit-time (the completed-crawl depth
// recompute never ran), so pages.depth is the bucket each page was admitted into.
func (c *Crawl) AdmittedItems() ([]frontier.Item, error) {
	rows, err := c.db.Query(`SELECT url, COALESCE(depth, 0), 0, '' FROM pages
		UNION ALL
		SELECT url, depth, redirect_hops, source FROM frontier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		if err := rows.Scan(&it.URL, &it.Depth, &it.RedirectHops, &it.Source); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// ProcessedURLs returns every URL already recorded (must not be re-fetched).
func (c *Crawl) ProcessedURLs() ([]string, error) {
	rows, err := c.db.Query(`SELECT url FROM pages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

// UpdateInlinks writes the post-crawl page aggregates: inlink counts and the
// recomputed crawl depth (NULL when no followed-link path reaches the URL).
func (c *Crawl) UpdateInlinks(pages map[string]*crawler.PageRecord) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for url, rec := range pages {
		depth := sql.NullInt64{Int64: int64(rec.Depth), Valid: rec.Depth != crawler.NoDepth}
		if _, err := tx.Exec(`UPDATE pages SET inlinks = ?, discovered_from = ?, depth = ? WHERE url = ?`,
			rec.Inlinks, rec.DiscoveredFrom, depth, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveDepths rewrites only the crawl-depth column for every supplied page
// (NULL when no followed-link path reaches the URL). Resume uses it to persist
// depths recomputed over the full two-session graph — UpdateInlinks alone
// touches only the resumed session's pages, leaving the original run's depths
// stale. Inlinks/discovered_from are intentionally left untouched here.
func (c *Crawl) SaveDepths(pages map[string]*crawler.PageRecord) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE pages SET depth = ? WHERE url = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for url, rec := range pages {
		depth := sql.NullInt64{Int64: int64(rec.Depth), Valid: rec.Depth != crawler.NoDepth}
		if _, err := stmt.Exec(depth, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveDepthsMap writes a url->depth map computed by the depth CSR (NoDepth -> SQL
// NULL). The SQL-cutover counterpart to SaveDepths, taking the CSR result instead
// of a page map, so finalize's depth step never materialises the page records.
func (c *Crawl) SaveDepthsMap(depths map[string]int) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE pages SET depth = ? WHERE url = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for url, d := range depths {
		depth := sql.NullInt64{Int64: int64(d), Valid: d != crawler.NoDepth}
		if _, err := stmt.Exec(depth, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveInlinks rewrites only the inlinks column for every supplied page. Resume
// uses it to persist inlink counts recomputed over the full two-session graph
// (crawler.RecomputeInlinks); UpdateInlinks alone counts a resumed session's
// own edges, under-reporting pages linked across the interrupt boundary. depth
// and discovered_from are owned by SaveDepths and the preserved per-session
// discoverer, so they are left untouched here.
func (c *Crawl) SaveInlinks(pages map[string]*crawler.PageRecord) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE pages SET inlinks = ? WHERE url = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for url, rec := range pages {
		if _, err := stmt.Exec(rec.Inlinks, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LinkRows returns every stored raw link (the ungated superset) for the depth
// CSR; the crawler re-applies the follow gate over them.
func (c *Crawl) LinkRows() ([]crawler.LinkRow, error) {
	rows, err := c.db.Query(`SELECT src, dst, type, nofollow FROM links`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []crawler.LinkRow
	for rows.Next() {
		var l crawler.LinkRow
		var nofollow int
		if err := rows.Scan(&l.Src, &l.Dst, &l.Type, &nofollow); err != nil {
			return nil, err
		}
		l.Nofollow = nofollow == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

// Redirects returns the redirect edges (page url -> redirect target) for the
// depth CSR — a redirect counts as a hop.
func (c *Crawl) Redirects() (map[string]string, error) {
	rows, err := c.db.Query(`SELECT url, redirect_url FROM pages WHERE redirect_url IS NOT NULL AND redirect_url != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var url, dst string
		if err := rows.Scan(&url, &dst); err != nil {
			return nil, err
		}
		out[url] = dst
	}
	return out, rows.Err()
}

// MaxEdgeSeq returns the highest gated-edge seq recorded so far (0 when empty).
// A resumed crawl seeds its edge counter past this so its new edges sort AFTER
// the prior session's — keeping MIN(seq) first-wins discovered_from stable across
// the resume boundary (the rowid-instability trap, MEMORY-SCALING.md §5.5).
func (c *Crawl) MaxEdgeSeq() (int64, error) {
	var n int64
	err := c.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM edges`).Scan(&n)
	return n, err
}

// InlinksFromEdges derives the raw hyperlink inlink count per URL from the gated
// edges table — the SQL equivalent of replaying discoverLinks→noteInlink in RAM
// (RecomputeInlinks). Self-links count, matching the in-RAM semantics. This is
// the Phase-2 SQL/CSR finalize path that lets the depth/inlinks recompute drop
// the in-RAM page map (MEMORY-SCALING.md §5.5).
func (c *Crawl) InlinksFromEdges() (map[string]int, error) {
	rows, err := c.db.Query(`SELECT dst, COUNT(*) FROM edges WHERE hyperlink = 1 GROUP BY dst`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var dst string
		var n int
		if err := rows.Scan(&dst, &n); err != nil {
			return nil, err
		}
		out[dst] = n
	}
	return out, rows.Err()
}

// DiscoveredFromEdges derives first-wins discovered_from per URL: the source of
// the lowest-seq edge into each target (any edge type, like noteInlink's first-
// wins). seq is the monotonic crawl order, so this is run-to-run stable and
// stable across resume (later sessions get higher seq) — unlike a rowid-based MIN.
// Seed-locking ("" for seeds) is applied by the caller.
func (c *Crawl) DiscoveredFromEdges() (map[string]string, error) {
	rows, err := c.db.Query(
		`SELECT dst, src FROM edges e WHERE e.seq = (SELECT MIN(seq) FROM edges WHERE dst = e.dst)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var dst, src string
		if err := rows.Scan(&dst, &src); err != nil {
			return nil, err
		}
		if _, dup := out[dst]; !dup { // guard against ties (same min seq is impossible — seq is unique)
			out[dst] = src
		}
	}
	return out, rows.Err()
}

// DuplicateIssues computes the cross-page duplicate occurrences (content hash /
// title / description / h1 / h2) in pure SQL — the Phase-2 dup-rule-SQL path,
// byte-equal to issues.duplicates() over the page map. It reproduces every
// nuance: the multi-clause eligibility gate (crawled ∧ internal ∧ HTML ∧ — when
// configured — indexable ∧ not-paginated, the last via json_array_length of the
// rel=prev arrays, so no precomputed column is needed), the H1/H2 "either of the
// first two" matching (json_each with index < 2, which also makes a page whose two
// h1s are identical its own duplicate), and the per-(url,key) detail rows.
func (c *Crawl) DuplicateIssues(ignoreNonIndexable, ignorePaginated bool) ([]issues.Occurrence, error) {
	elig := `state = 'crawled' AND scope = 'internal' AND facts IS NOT NULL
		AND (content_type LIKE '%text/html%' OR content_type LIKE '%application/xhtml%')`
	if ignoreNonIndexable {
		elig += ` AND indexable = 1`
	}
	if ignorePaginated {
		elig += ` AND COALESCE(json_array_length(facts, '$.PrevHTML'), 0) = 0
			AND COALESCE(json_array_length(facts, '$.PrevHTTP'), 0) = 0`
	}

	var occs []issues.Occurrence
	scan := func(q, issueID string) error {
		rows, err := c.db.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var url, key string
			if err := rows.Scan(&url, &key); err != nil {
				return err
			}
			occs = append(occs, issues.Occurrence{URL: url, IssueID: issueID, Detail: key})
		}
		return rows.Err()
	}

	// single-key duplicates: hash, title[0], description[0]
	single := func(keyExpr, issueID string) error {
		q := fmt.Sprintf(`WITH e AS (SELECT url, %s AS k FROM pages WHERE %s)
			SELECT url, k FROM e WHERE k IS NOT NULL AND k != ''
			  AND k IN (SELECT k FROM e WHERE k IS NOT NULL AND k != '' GROUP BY k HAVING COUNT(*) >= 2)`,
			keyExpr, elig)
		return scan(q, issueID)
	}
	if err := single(`json_extract(facts, '$.Hash')`, "content_exact_duplicate"); err != nil {
		return nil, err
	}
	if err := single(`json_extract(facts, '$.Titles[0]')`, "title_duplicate"); err != nil {
		return nil, err
	}
	if err := single(`json_extract(facts, '$.Descriptions[0]')`, "description_duplicate"); err != nil {
		return nil, err
	}

	// either-of-first-2 duplicates: h1, h2 (each of the first two values is a key)
	either := func(arrayPath, issueID string) error {
		q := fmt.Sprintf(`WITH e AS (SELECT url, facts FROM pages WHERE %s),
			keys AS (SELECT e.url AS url, je.value AS k FROM e, json_each(e.facts, '%s') je
			         WHERE je.key < 2 AND je.value IS NOT NULL AND je.value != '')
			SELECT url, k FROM keys WHERE k IN (SELECT k FROM keys GROUP BY k HAVING COUNT(*) >= 2)`,
			elig, arrayPath)
		return scan(q, issueID)
	}
	if err := either(`$.H1s`, "h1_duplicate"); err != nil {
		return nil, err
	}
	if err := either(`$.H2s`, "h2_duplicate"); err != nil {
		return nil, err
	}
	return occs, nil
}

// SaveInlinksFromEdges is the Phase-2 SQL cutover: it persists the raw hyperlink
// inlink count and first-wins (seq-MIN) discovered_from for every page, computed
// purely in SQL over the gated edges table — no in-RAM RecomputeInlinks, no page
// map. Seeds are seed-locked to "". Over the full edges table (both sessions on
// resume) this yields full-graph inlinks, so it subsumes the old resume recompute.
func (c *Crawl) SaveInlinksFromEdges(seeds []string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE pages SET inlinks = (
		SELECT COUNT(*) FROM edges WHERE dst = pages.url AND hyperlink = 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE pages SET discovered_from = COALESCE(
		(SELECT src FROM edges e WHERE e.dst = pages.url ORDER BY seq LIMIT 1), '')`); err != nil {
		return err
	}
	for _, s := range seeds { // seed-lock: a backlink must not become a seed's discoverer
		if _, err := tx.Exec(`UPDATE pages SET discovered_from = '' WHERE url = ?`, s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveInlinkSources persists the per-URL discovery aggregate the crawler hands
// back after a stream-and-drop crawl: the raw hyperlink inlink count and the
// first-wins, seed-locked discovered_from. It is the always-run aggregate write
// (fresh and interrupted alike) that the old UpdateInlinks(res.Pages) performed,
// minus depth — depth is recomputed over the stored graph by the completed-crawl
// finalize path (SaveDepths). An interrupted crawl keeps the admit-time depth
// record() already wrote until a resume recomputes it over the full graph.
// Aggregate entries for URLs with no pages row (discovered but never crawled)
// no-op, exactly as the old per-record UPDATE did.
func (c *Crawl) SaveInlinkSources(agg map[string]crawler.InlinkAgg) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE pages SET inlinks = ?, discovered_from = ? WHERE url = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for url, a := range agg {
		if _, err := stmt.Exec(a.Count, a.First, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Counts returns the authoritative URL tallies over the full stored graph:
// total = every recorded URL (the "encountered" count, incl. robots-blocked and
// errored — identical to PageCount), crawled = the fetched subset (state ==
// "crawled"). These mirror the crawler's per-run Result.Total (len(c.pages)) and
// Result.Crawled (state==crawled) exactly, but read from the store so they stay
// correct across a resume, where the per-session Result sees only its own pages.
// finalize derives the registry counts from this, never from the Result.
func (c *Crawl) Counts() (crawled, total int, err error) {
	err = c.db.QueryRow(
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE state = ?) FROM pages`,
		crawler.StateCrawled).Scan(&total, &crawled)
	return crawled, total, err
}

// LoadPages reconstructs every PageRecord (including parsed facts) from the
// crawl database, keyed by URL.
// PageCount returns the number of URLs recorded for this crawl (every state) —
// the "URLs encountered" total, without materialising every row.
func (c *Crawl) PageCount() (int, error) {
	var n int
	err := c.db.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&n)
	return n, err
}

// LoadPages reconstructs every stored page record, including the full Facts
// (with ContentText). Used by re-analysis, compare, and any path that needs the
// page body text.
func (c *Crawl) LoadPages() (map[string]*crawler.PageRecord, error) {
	return c.loadPages(false)
}

// LoadPagesLite reconstructs every page record but frees Facts.ContentText
// per-row as it loads, so the returned map never holds the page bodies — the
// dominant per-record cost. The finalize aggregates (depth, inlinks, link graph,
// PageRank, duplicate keys, every issue check except the two content-text scans)
// read only Links + scalars, so they run identically over this lighter map; the
// two ContentText checks (lorem/soft-404) and near-duplicates are fed the body
// text separately (StreamContentText / a full LoadPages when near-dup is on).
// This is what keeps the finalize peak off the page-body axis (MEMORY-SCALING.md
// §4 regime 3 / Phase 2).
func (c *Crawl) LoadPagesLite() (map[string]*crawler.PageRecord, error) {
	return c.loadPages(true)
}

func (c *Crawl) loadPages(stripContent bool) (map[string]*crawler.PageRecord, error) {
	rows, err := c.db.Query(`SELECT url, scope, state, COALESCE(depth, -1), status_code, status,
		content_type, COALESCE(http_version,''), response_time_ms, size, fetch_error, redirect_url,
		redirect_type, matched_robots_line, indexable, indexability_status,
		inlinks, COALESCE(discovered_from,''), outside_start_folder,
		link_score, unique_inlinks, unique_outlinks, closest_similarity,
		COALESCE(duplicate_of,''), minhash, headers, structured, jsdiff, facts FROM pages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := make(map[string]*crawler.PageRecord)
	for rows.Next() {
		rec := &crawler.PageRecord{}
		var indexable, outside int
		var headersJSON, structuredJSON, jsdiffJSON, factsJSON []byte
		if err := rows.Scan(&rec.URL, &rec.Scope, &rec.State, &rec.Depth,
			&rec.StatusCode, &rec.Status, &rec.ContentType, &rec.HTTPVersion, &rec.ResponseTimeMs,
			&rec.Size, &rec.FetchError, &rec.RedirectURL, &rec.RedirectType,
			&rec.MatchedRobotsLine, &indexable, &rec.IndexabilityStatus,
			&rec.Inlinks, &rec.DiscoveredFrom, &outside,
			&rec.LinkScore, &rec.UniqueInlinks, &rec.UniqueOutlinks, &rec.ClosestSimilarity,
			&rec.DuplicateOf, &rec.Minhash, &headersJSON, &structuredJSON, &jsdiffJSON, &factsJSON); err != nil {
			return nil, err
		}
		rec.Indexable = indexable == 1
		rec.OutsideStartFolder = outside == 1
		if len(headersJSON) > 0 {
			if err := json.Unmarshal(headersJSON, &rec.Headers); err != nil {
				return nil, err
			}
		}
		if len(structuredJSON) > 0 {
			rec.StructuredData = &structured.PageData{}
			if err := json.Unmarshal(structuredJSON, rec.StructuredData); err != nil {
				return nil, err
			}
		}
		if len(jsdiffJSON) > 0 {
			rec.JSDiff = &crawler.JSDiff{}
			if err := json.Unmarshal(jsdiffJSON, rec.JSDiff); err != nil {
				return nil, err
			}
		}
		if len(factsJSON) > 0 {
			rec.Facts = &parse.Facts{}
			if err := json.Unmarshal(factsJSON, rec.Facts); err != nil {
				return nil, err
			}
			if stripContent {
				rec.Facts.ContentText = "" // freed per-row: never retained in the map
			}
		}
		pages[rec.URL] = rec
	}
	return pages, rows.Err()
}

// StreamContentText yields each page's URL and body text one row at a time,
// holding only a single ContentText in memory. finalize uses it to run the two
// ContentText-dependent issue checks (lorem/soft-404) over a LoadPagesLite map
// without ever materialising all page bodies at once.
func (c *Crawl) StreamContentText(fn func(url, text string) error) error {
	rows, err := c.db.Query(
		`SELECT url, COALESCE(json_extract(facts, '$.ContentText'), '') FROM pages`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var url, text string
		if err := rows.Scan(&url, &text); err != nil {
			return err
		}
		if err := fn(url, text); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SaveIssues replaces all stored issue occurrences.
func (c *Crawl) SaveIssues(occs []issues.Occurrence) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM issues`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO issues(url, issue, detail) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range occs {
		if _, err := stmt.Exec(o.URL, o.IssueID, o.Detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IssueCounts returns issue id -> affected URL count. A page may store several
// rows for one issue id (one per distinct detail), so this counts distinct URLs,
// never raw occurrence rows.
func (c *Crawl) IssueCounts() (map[string]int, error) {
	rows, err := c.db.Query(`SELECT issue, COUNT(DISTINCT url) FROM issues GROUP BY issue`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	return counts, rows.Err()
}

// IssueURLs returns the URLs affected by one issue. DISTINCT collapses the
// several detail rows a page may store for one issue id into a single URL.
func (c *Crawl) IssueURLs(issueID string) ([]string, error) {
	rows, err := c.db.Query(`SELECT DISTINCT url FROM issues WHERE issue = ? ORDER BY url`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, rows.Err()
}

// Blob stores page source (or other binary assets) on disk next to the
// crawl database and records the location (Bulk Export > All Page Source).
func (c *Crawl) Blob(url, kind string, data []byte) error {
	dir := filepath.Join(c.dir, "crawls", c.ID+".assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sum := md5.Sum([]byte(url + "|" + kind))
	name := hex.EncodeToString(sum[:]) + extFor(kind)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	_, err := c.db.Exec(`INSERT OR REPLACE INTO blobs(url, kind, path) VALUES(?,?,?)`, url, kind, path)
	return err
}

func extFor(kind string) string {
	switch kind {
	case "html", "rendered_html":
		return ".html"
	case "screenshot":
		return ".jpg" // chromedp.FullScreenshot encodes JPEG
	}
	return ".bin"
}

// BlobPath returns the stored asset path for a URL+kind ("" when absent).
func (c *Crawl) BlobPath(url, kind string) (string, error) {
	var path string
	err := c.db.QueryRow(`SELECT path FROM blobs WHERE url = ? AND kind = ?`, url, kind).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return path, err
}

// SitemapEntry records one URL listed in a sitemap (crawler sink extension).
func (c *Crawl) SitemapEntry(sitemap, url string) error {
	_, err := c.db.Exec(`INSERT OR IGNORE INTO sitemap_entries(sitemap, url) VALUES(?,?)`, sitemap, url)
	return err
}

// LlmsTxtFile records one fetched /llms.txt (or /llms-full.txt) file and its
// structural-validation outcome (crawler sink extension).
func (c *Crawl) LlmsTxtFile(rec crawler.LlmsTxtRecord) error {
	_, err := c.db.Exec(`INSERT OR REPLACE INTO llmstxt
		(url, kind, status, found, title, summary, malformed, content) VALUES(?,?,?,?,?,?,?,?)`,
		rec.URL, rec.Kind, rec.Status, boolInt(rec.Found),
		rec.Title, rec.Summary, boolInt(rec.Malformed), string(rec.Content))
	return err
}

// LlmsTxtLink records one curated link listed in an llms.txt file — provenance
// that survives independently of the link graph and frontier dedup.
func (c *Crawl) LlmsTxtLink(src, url, section, anchor string) error {
	_, err := c.db.Exec(`INSERT OR IGNORE INTO llmstxt_links(src, url, section, anchor) VALUES(?,?,?,?)`,
		src, url, section, anchor)
	return err
}

// LlmsTxt reloads the stored llms.txt audit input (files + curated links) for
// the analysis phase. Returns an empty (non-nil) set when no file was fetched.
func (c *Crawl) LlmsTxt() (*analyze.LlmsTxtData, error) {
	data := &analyze.LlmsTxtData{}
	rows, err := c.db.Query(`SELECT url, kind, status, found, title, summary, malformed FROM llmstxt`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f analyze.LlmsTxtFile
		var found, malformed int
		if err := rows.Scan(&f.URL, &f.Kind, &f.Status, &found, &f.Title, &f.Summary, &malformed); err != nil {
			return nil, err
		}
		f.Found = found == 1
		f.Malformed = malformed == 1
		data.Files = append(data.Files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lrows, err := c.db.Query(`SELECT src, url, section, anchor FROM llmstxt_links`)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var l analyze.LlmsTxtLink
		if err := lrows.Scan(&l.Src, &l.URL, &l.Section, &l.Anchor); err != nil {
			return nil, err
		}
		data.Links = append(data.Links, l)
	}
	return data, lrows.Err()
}

// SitemapIndex returns page URL -> sitemaps listing it.
func (c *Crawl) SitemapIndex() (analyze.SitemapIndex, error) {
	rows, err := c.db.Query(`SELECT url, sitemap FROM sitemap_entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := analyze.SitemapIndex{}
	for rows.Next() {
		var url, sitemap string
		if err := rows.Scan(&url, &sitemap); err != nil {
			return nil, err
		}
		index[url] = append(index[url], sitemap)
	}
	return index, rows.Err()
}

// AddIssues appends occurrences without clearing existing ones (the analysis
// phase adds to the per-page evaluation).
func (c *Crawl) AddIssues(occs []issues.Occurrence) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO issues(url, issue, detail) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range occs {
		if _, err := stmt.Exec(o.URL, o.IssueID, o.Detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveAnalysis writes the analysis pass back: per-page metrics, chains, and
// the analysis-phase issue occurrences.
func (c *Crawl) SaveAnalysis(r *analyze.Results) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for url, score := range r.LinkScores {
		if _, err := tx.Exec(`UPDATE pages SET link_score = ? WHERE url = ?`, score, url); err != nil {
			return err
		}
	}
	for url, n := range r.UniqueIn {
		if _, err := tx.Exec(`UPDATE pages SET unique_inlinks = ? WHERE url = ?`, n, url); err != nil {
			return err
		}
	}
	for url, n := range r.UniqueOut {
		if _, err := tx.Exec(`UPDATE pages SET unique_outlinks = ? WHERE url = ?`, n, url); err != nil {
			return err
		}
	}
	for url, nd := range r.NearDups {
		if _, err := tx.Exec(`UPDATE pages SET closest_similarity = ?, near_dup_count = ? WHERE url = ?`,
			nd.ClosestSimilarity, nd.Count, url); err != nil {
			return err
		}
	}
	chains, err := json.Marshal(r.Chains)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO analysis(key, value) VALUES('chains', ?)`, string(chains)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return c.AddIssues(r.Occurrences)
}

// Chains returns the stored redirect/canonical chains.
func (c *Crawl) Chains() ([]analyze.Chain, error) {
	var raw string
	err := c.db.QueryRow(`SELECT value FROM analysis WHERE key = 'chains'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var chains []analyze.Chain
	return chains, json.Unmarshal([]byte(raw), &chains)
}

// --- registry operations ---

func ListCrawls(dir string) ([]Info, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	rows, err := reg.Query(`SELECT id, seed, mode, status, started, COALESCE(finished, 0), crawled, COALESCE(total, 0)
		FROM crawls ORDER BY started`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var infos []Info
	for rows.Next() {
		var in Info
		var started, finished int64
		if err := rows.Scan(&in.ID, &in.Seed, &in.Mode, &in.Status,
			&started, &finished, &in.Crawled, &in.Total); err != nil {
			return nil, err
		}
		in.Started = time.Unix(started, 0)
		if finished > 0 {
			in.Finished = time.Unix(finished, 0)
		}
		infos = append(infos, in)
	}
	return infos, rows.Err()
}

// SetStatus updates a crawl's registry row. crawled is URLs fetched; total is
// URLs encountered (fetched + robots-blocked + errored — SF's headline count).
func SetStatus(dir, id, status string, crawled, total int) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`UPDATE crawls SET status = ?, crawled = ?, total = ?, finished = ? WHERE id = ?`,
		status, crawled, total, time.Now().Unix(), id)
	return err
}

// SetTotal backfills the encountered-URL count on an existing crawl row. Crawls
// finished before `total` existed have it at 0; the desktop list fills it in
// lazily (a COUNT over the crawl's pages) the first time they're shown.
func SetTotal(dir, id string, total int) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`UPDATE crawls SET total = ? WHERE id = ?`, total, id)
	return err
}

// DeleteCrawl removes the crawl database and its registry row.
func DeleteCrawl(dir, id string) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	if _, err := reg.Exec(`DELETE FROM crawls WHERE id = ?`, id); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(crawlPath(dir, id) + suffix)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// minhashBlob stores an empty signature as SQL NULL (not a zero-length blob) so
// "has a precomputed signature" is a clean IS NOT NULL test and a near-dup-off
// crawl leaves the column unset.
func minhashBlob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
