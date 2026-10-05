package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/parse"
)

// legacyHeadings is how facts stored headings before migration v9: the h1 and
// h2 texts, the document order of every level, and a page-level flag for an h1
// whose text came from an image alt.
type legacyHeadings struct {
	H1s           []string
	H2s           []string
	HeadingLevels []int
	H1AltText     bool
}

// headings rebuilds the one heading record from the legacy lists: levels from
// HeadingLevels, h1 and h2 text from H1s and H2s in order, "" for h3–h6, whose
// text was never kept (the links.position_path precedent: a value that needs
// the HTML stays empty), and the page-level H1AltText as FromAlt on the first
// h1, which is all the flag recorded.
func (l legacyHeadings) headings() []parse.Heading {
	var hs []parse.Heading
	var h1, h2 int
	for _, level := range l.HeadingLevels {
		h := parse.Heading{Level: level}
		switch level {
		case 1:
			if h1 < len(l.H1s) {
				h.Text = l.H1s[h1]
			}
			h.FromAlt = h1 == 0 && l.H1AltText
			h1++
		case 2:
			if h2 < len(l.H2s) {
				h.Text = l.H2s[h2]
			}
			h2++
		}
		hs = append(hs, h)
	}
	return hs
}

// headingsBatch bounds how many pages one pass of migrateHeadings holds.
const headingsBatch = 1000

// migrateHeadings is ladder step v9: it rewrites every stored page's facts
// from the legacy heading lists to the Headings record, so analysis, compare
// and the exports read an old crawl the way they read a new one — without it
// every old page would read as missing its h1. Only the legacy fields are
// decoded here; SQLite splices the record into the facts document, which is
// otherwise copied untouched. Pages are read in rowid batches and the reads
// closed before the writes, so a large crawl is neither held in memory nor
// updated under an open cursor.
func migrateHeadings(tx *sql.Tx) error {
	var after int64
	for {
		ids, legacy, err := legacyHeadingsAfter(tx, after)
		if err != nil {
			return err
		}
		for i, id := range ids {
			record, err := json.Marshal(legacy[i].headings())
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE pages SET facts = json_set(
					json_remove(facts, '$.H1s', '$.H2s', '$.HeadingLevels', '$.H1AltText'),
					'$.Headings', json(?)) WHERE rowid = ?`, string(record), id); err != nil {
				return err
			}
		}
		if len(ids) < headingsBatch {
			return nil
		}
		after = ids[len(ids)-1]
	}
}

// legacyHeadingsAfter reads the next batch of pages after rowid that still
// carry the legacy heading lists, with those lists decoded. A page already in
// the new shape has no HeadingLevels key and is skipped, so the step is safe
// to re-run.
func legacyHeadingsAfter(tx *sql.Tx, after int64) ([]int64, []legacyHeadings, error) {
	rows, err := tx.Query(`SELECT rowid, json_object(
			'H1s', facts -> '$.H1s', 'H2s', facts -> '$.H2s',
			'HeadingLevels', facts -> '$.HeadingLevels', 'H1AltText', facts -> '$.H1AltText')
		FROM pages WHERE rowid > ? AND facts IS NOT NULL AND json_type(facts, '$.HeadingLevels') IS NOT NULL
		ORDER BY rowid LIMIT ?`, after, headingsBatch)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []int64
	var legacy []legacyHeadings
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, nil, err
		}
		var l legacyHeadings
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return nil, nil, fmt.Errorf("page rowid %d: %w", id, err)
		}
		ids = append(ids, id)
		legacy = append(legacy, l)
	}
	return ids, legacy, rows.Err()
}
