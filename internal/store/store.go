// Package store persists crawls to per-crawl SQLite databases (WAL mode,
// continuous commit → crash-safe) plus a registry database listing all
// crawls with their IDs, projects and status (DESIGN.md §5.3). It implements
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

	"github.com/hhsecond/acrawler/internal/analyze"
	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/fetch"
	"github.com/hhsecond/acrawler/internal/frontier"
	"github.com/hhsecond/acrawler/internal/issues"
	"github.com/hhsecond/acrawler/internal/parse"
	"github.com/hhsecond/acrawler/internal/structured"
	"github.com/hhsecond/acrawler/internal/warc"
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
	Project  string
	Seed     string
	Mode     string
	Status   string
	Started  time.Time
	Finished time.Time
	Crawled  int
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
  headers JSON, structured JSON, jsdiff JSON, facts JSON
);
CREATE TABLE IF NOT EXISTS links(
  src TEXT, dst TEXT, type TEXT, anchor TEXT, alt TEXT,
  nofollow INT, rel TEXT, target TEXT, path_type TEXT,
  elem_path TEXT, position TEXT
);
CREATE INDEX IF NOT EXISTS links_src ON links(src);
CREATE INDEX IF NOT EXISTS links_dst ON links(dst);
CREATE TABLE IF NOT EXISTS frontier(url TEXT PRIMARY KEY, depth INT, redirect_hops INT, source TEXT);
CREATE TABLE IF NOT EXISTS issues(url TEXT, issue TEXT, detail TEXT, PRIMARY KEY(url, issue));
CREATE TABLE IF NOT EXISTS custom_results(url TEXT, kind TEXT, name TEXT, value TEXT, PRIMARY KEY(url, kind, name));
CREATE TABLE IF NOT EXISTS sitemap_entries(sitemap TEXT, url TEXT, PRIMARY KEY(sitemap, url));
CREATE TABLE IF NOT EXISTS analysis(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS blobs(url TEXT, kind TEXT, path TEXT, PRIMARY KEY(url, kind));
`

// Crawl is an open per-crawl database.
type Crawl struct {
	ID  string
	dir string
	db  *sql.DB

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
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS crawls(
		id TEXT PRIMARY KEY, project TEXT, seed TEXT, mode TEXT, status TEXT,
		started INT, finished INT, crawled INT DEFAULT 0)`)
	if err != nil {
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

// CreateCrawl registers a new crawl and opens its database, freezing the
// config into it (resume refuses a different config unless forced).
func CreateCrawl(dir, project, seed, mode string, cfg *config.Config) (*Crawl, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, err
	}
	defer reg.Close()

	id := newCrawlID()
	_, err = reg.Exec(`INSERT INTO crawls(id, project, seed, mode, status, started) VALUES(?,?,?,?,?,?)`,
		id, project, seed, mode, StatusRunning, time.Now().Unix())
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
	for key, value := range map[string]string{
		"config": string(cfgYAML), "seed": seed, "mode": mode,
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
	if _, err := db.Exec(crawlSchema); err != nil {
		db.Close()
		return nil, err
	}
	// migration for crawl DBs created before the column existed; the error
	// ("duplicate column") is the normal case and deliberately ignored
	db.Exec(`ALTER TABLE pages ADD COLUMN http_version TEXT`)
	return &Crawl{ID: id, dir: dir, db: db}, nil
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
				"software": "acrawler",
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
		 discovered_from, outside_start_folder, headers, structured, jsdiff, facts)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.URL, rec.Scope, rec.State, rec.Depth, rec.StatusCode, rec.Status,
		rec.ContentType, rec.HTTPVersion, rec.ResponseTimeMs, rec.Size, rec.FetchError,
		rec.RedirectURL, rec.RedirectType, rec.MatchedRobotsLine,
		boolInt(rec.Indexable), rec.IndexabilityStatus,
		rec.DiscoveredFrom, boolInt(rec.OutsideStartFolder), headersJSON, structuredJSON, jsdiffJSON, factsJSON)
	if err != nil {
		return err
	}
	for _, cr := range rec.CustomResults {
		if _, err := c.db.Exec(`INSERT OR REPLACE INTO custom_results(url, kind, name, value) VALUES(?,?,?,?)`,
			rec.URL, cr.Kind, cr.Name, cr.Value); err != nil {
			return err
		}
	}
	if rec.Facts == nil {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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

// --- resume support ---

// PendingFrontier returns the admitted-but-unprocessed items.
func (c *Crawl) PendingFrontier() ([]frontier.Item, error) {
	rows, err := c.db.Query(`SELECT url, depth, redirect_hops, source FROM frontier`)
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

// UpdateInlinks writes the inlink aggregates computed by the crawl.
func (c *Crawl) UpdateInlinks(pages map[string]*crawler.PageRecord) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for url, rec := range pages {
		if _, err := tx.Exec(`UPDATE pages SET inlinks = ?, discovered_from = ? WHERE url = ?`,
			rec.Inlinks, rec.DiscoveredFrom, url); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadPages reconstructs every PageRecord (including parsed facts) from the
// crawl database, keyed by URL.
func (c *Crawl) LoadPages() (map[string]*crawler.PageRecord, error) {
	rows, err := c.db.Query(`SELECT url, scope, state, depth, status_code, status,
		content_type, COALESCE(http_version,''), response_time_ms, size, fetch_error, redirect_url,
		redirect_type, matched_robots_line, indexable, indexability_status,
		inlinks, COALESCE(discovered_from,''), outside_start_folder,
		link_score, unique_inlinks, unique_outlinks, closest_similarity,
		headers, structured, jsdiff, facts FROM pages`)
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
			&headersJSON, &structuredJSON, &jsdiffJSON, &factsJSON); err != nil {
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
		}
		pages[rec.URL] = rec
	}
	return pages, rows.Err()
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

// IssueCounts returns issue id -> affected URL count.
func (c *Crawl) IssueCounts() (map[string]int, error) {
	rows, err := c.db.Query(`SELECT issue, COUNT(*) FROM issues GROUP BY issue`)
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

// IssueURLs returns the URLs affected by one issue.
func (c *Crawl) IssueURLs(issueID string) ([]string, error) {
	rows, err := c.db.Query(`SELECT url FROM issues WHERE issue = ? ORDER BY url`, issueID)
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
	rows, err := reg.Query(`SELECT id, project, seed, mode, status, started, COALESCE(finished, 0), crawled
		FROM crawls ORDER BY started`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var infos []Info
	for rows.Next() {
		var in Info
		var started, finished int64
		if err := rows.Scan(&in.ID, &in.Project, &in.Seed, &in.Mode, &in.Status,
			&started, &finished, &in.Crawled); err != nil {
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

// SetStatus updates a crawl's registry row.
func SetStatus(dir, id, status string, crawled int) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`UPDATE crawls SET status = ?, crawled = ?, finished = ? WHERE id = ?`,
		status, crawled, time.Now().Unix(), id)
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
