package project

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Role is a site's relationship to a project.
type Role string

const (
	RoleMain       Role = "main"
	RoleCompetitor Role = "competitor"
)

// Project is a competitor-study group: a main site plus competitor sites.
type Project struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	MainDomain string    `json:"main_domain"`
	Created    time.Time `json:"created"`
}

// Member is one site in a project, keyed by its exact host[:port] site key.
type Member struct {
	Domain string    `json:"domain"`
	Role   Role      `json:"role"`
	Added  time.Time `json:"added"`
}

// Store is the project layer's OWN database (<store-dir>/projects.db). It never
// touches the crawl registry or per-crawl databases; it only reads them through
// store's public API. See doc.go for the zero-core-change contract.
type Store struct {
	db  *sql.DB
	dir string
}

const projectSchema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS projects(
  id TEXT PRIMARY KEY, name TEXT, main_domain TEXT, created INT);
CREATE TABLE IF NOT EXISTS project_domains(
  project_id TEXT, domain TEXT, role TEXT, added INT,
  PRIMARY KEY(project_id, domain));
`

// Open opens (creating if needed) the project database in dir — the same shared
// store directory the crawl registry lives in, but projects.db is a separate
// file owned entirely by this package.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "projects.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(projectSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dir: dir}, nil
}

// Close closes the project database.
func (s *Store) Close() error { return s.db.Close() }

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// SiteKey canonicalizes a domain or URL to the project's exact site key: the
// lowercased host with its explicit port (if any). No www-stripping, no
// subdomain or registrable-domain folding — example.com, www.example.com,
// a.example.com and example.com:8080 are all distinct sites by design. It
// accepts a bare host ("example.com"), a host:port, or a full URL.
func SiteKey(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty domain")
	}
	parse := raw
	if !strings.Contains(parse, "//") {
		parse = "//" + parse // make url.Parse read a bare host as the authority
	}
	u, err := url.Parse(parse)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid domain %q", raw)
	}
	return strings.ToLower(u.Host), nil
}

// CreateProject creates a project anchored on mainDomain. An empty name defaults
// to "<main_domain>'s Project". The main domain is recorded as a RoleMain member.
func (s *Store) CreateProject(name, mainDomain string) (*Project, error) {
	key, err := SiteKey(mainDomain)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = key + "'s Project"
	}
	id := newID()
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO projects(id, name, main_domain, created) VALUES(?,?,?,?)`,
		id, name, key, now.Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO project_domains(project_id, domain, role, added) VALUES(?,?,?,?)`,
		id, key, string(RoleMain), now.Unix()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Project{ID: id, Name: name, MainDomain: key, Created: now}, nil
}

// ListProjects returns all projects, oldest first.
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, main_domain, created FROM projects ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var created int64
		if err := rows.Scan(&p.ID, &p.Name, &p.MainDomain, &created); err != nil {
			return nil, err
		}
		p.Created = time.Unix(created, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject returns one project by id.
func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	var created int64
	err := s.db.QueryRow(`SELECT id, name, main_domain, created FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.MainDomain, &created)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	p.Created = time.Unix(created, 0)
	return &p, nil
}

// RenameProject changes a project's display name.
func (s *Store) RenameProject(id, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name cannot be empty")
	}
	res, err := s.db.Exec(`UPDATE projects SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %q not found", id)
	}
	return nil
}

// DeleteProject removes a project and its membership rows. Crawls are untouched.
func (s *Store) DeleteProject(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM project_domains WHERE project_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AddMember adds (or re-roles) a competitor site. The domain is canonicalized to
// its exact host[:port] site key.
func (s *Store) AddMember(projectID, domain string, role Role) error {
	if _, err := s.GetProject(projectID); err != nil {
		return err
	}
	key, err := SiteKey(domain)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO project_domains(project_id, domain, role, added) VALUES(?,?,?,?)`,
		projectID, key, string(role), time.Now().Unix())
	return err
}

// RemoveMember drops a site from a project.
func (s *Store) RemoveMember(projectID, domain string) error {
	key, err := SiteKey(domain)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM project_domains WHERE project_id = ? AND domain = ?`, projectID, key)
	return err
}

// Members returns a project's sites, the main site first then competitors by domain.
func (s *Store) Members(projectID string) ([]Member, error) {
	rows, err := s.db.Query(`SELECT domain, role, added FROM project_domains WHERE project_id = ?
		ORDER BY CASE role WHEN 'main' THEN 0 ELSE 1 END, domain`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var added int64
		if err := rows.Scan(&m.Domain, &m.Role, &added); err != nil {
			return nil, err
		}
		m.Added = time.Unix(added, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}
