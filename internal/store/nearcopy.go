package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/nearcopy"
)

// nearCopiesKey is the analysis row recording the settings the near-copy step
// last ran with. Its absence means the step's lists are not there to read:
// every page's near_copies is NULL.
const nearCopiesKey = "near_copies"

// TempDir is the directory a finalize step keeps its working files in: next to
// the crawl's database, on the volume that holds the store.
func (c *Crawl) TempDir() string {
	return filepath.Join(c.dir, "crawls")
}

// NearCopyPages streams the pages the near-copy step may compare — internal,
// crawled, with facts, and indexable when indexableOnly — one at a time, each
// with what the step's filters read, its stored signature and its content
// text. One json_extract call pulls the facts it needs, so each facts blob is
// parsed once, and the SQL pre-filter only spares the step pages its own
// filter (nearcopy.Settings.Compares) would drop anyway.
func (c *Crawl) NearCopyPages(indexableOnly bool) nearcopy.Source {
	return func(fn func(*nearcopy.Page) error) error {
		q := `SELECT url, indexable, minhash,
			json_extract(facts, '$.WordCount', '$.Hash', '$.PrevHTML', '$.PrevHTTP', '$.ContentText')
			FROM pages WHERE scope = 'internal' AND state = ? AND facts IS NOT NULL`
		if indexableOnly {
			q += ` AND indexable = 1`
		}
		rows, err := c.db.Query(q, crawler.StateCrawled)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p := nearcopy.Page{Internal: true, Crawled: true}
			var indexable int
			var raw []byte
			if err := rows.Scan(&p.URL, &indexable, &p.Signature, &raw); err != nil {
				return err
			}
			p.Indexable = indexable == 1
			var parts struct {
				WordCount int
				Hash      string
				Prev      []string
				PrevHTTP  []string
				Text      string
			}
			var fields []json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 5 {
				return fmt.Errorf("%s: facts: near-copy fields: %v", p.URL, err)
			}
			for i, dst := range []any{&parts.WordCount, &parts.Hash, &parts.Prev, &parts.PrevHTTP, &parts.Text} {
				if string(fields[i]) == "null" {
					continue
				}
				if err := json.Unmarshal(fields[i], dst); err != nil {
					return fmt.Errorf("%s: facts: %w", p.URL, err)
				}
			}
			p.WordCount, p.Hash, p.Text = parts.WordCount, parts.Hash, parts.Text
			p.Paginated = len(parts.Prev) > 0 || len(parts.PrevHTTP) > 0 // parse.Facts.IsPaginated
			if err := fn(&p); err != nil {
				return err
			}
		}
		return rows.Err()
	}
}

// SaveNearCopies persists the near-copy step's result on its own — the run
// finalize makes when the rest of the analysis is off. It writes only the
// near-copy fields of page_metrics (and the settings row), so link scores and
// issues are left exactly as they were.
func (c *Crawl) SaveNearCopies(res *nearcopy.Result) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveNearCopies(tx, res); err != nil {
		return err
	}
	return tx.Commit()
}

// saveNearCopies replaces every page's near-copy fields with res: reset, then
// one upsert per compared page. A nil res (the step did not run) leaves every
// page not compared — near_copies and near_copy_count NULL, the two older
// fields 0 — and removes the settings row, so a re-analysis with the check
// turned off cannot leave the previous run's lists behind.
//
// closest_similarity and near_dup_count are written from the same list: the
// top share as a percent, and the full count.
func saveNearCopies(tx *sql.Tx, res *nearcopy.Result) error {
	if _, err := tx.Exec(`UPDATE page_metrics SET near_copies = NULL, near_copy_count = NULL,
		closest_similarity = 0, near_dup_count = 0
		WHERE near_copies IS NOT NULL OR closest_similarity != 0 OR near_dup_count != 0`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM analysis WHERE key = ?`, nearCopiesKey); err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO page_metrics(url, near_copies, near_copy_count, closest_similarity, near_dup_count)
		VALUES(?,?,?,?,?)
		ON CONFLICT(url) DO UPDATE SET near_copies = excluded.near_copies, near_copy_count = excluded.near_copy_count,
			closest_similarity = excluded.closest_similarity, near_dup_count = excluded.near_dup_count`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range res.Pages {
		list, err := json.Marshal(p.Copies)
		if err != nil {
			return err
		}
		closest := 0.0
		if len(p.Copies) > 0 {
			closest = math.Round(p.Copies[0].Share*10000) / 100 // 0.9722 → 97.22
		}
		if _, err := stmt.Exec(p.URL, string(list), p.Count, closest, p.Count); err != nil {
			return err
		}
	}
	settings, err := json.Marshal(res.Settings)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO analysis(key, value) VALUES(?, ?)`, nearCopiesKey, string(settings))
	return err
}

// NearCopySettings returns the settings the near-copy step last ran with, or
// nil when no stored result is there to read (the step has not run on this
// crawl, or ran before lists were kept). q may be a transaction: the bundle
// reads it inside the one that streams the lists it describes.
func NearCopySettings(q RowQueryer) (*nearcopy.Settings, error) {
	var raw string
	err := q.QueryRow(`SELECT value FROM analysis WHERE key = ?`, nearCopiesKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s nearcopy.Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("near-copy settings: %w", err)
	}
	return &s, nil
}
