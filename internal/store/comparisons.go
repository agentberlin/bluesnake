package store

import (
	"database/sql"
	"errors"
	"time"
)

// Cached pairwise crawl comparisons. A comparison of two finished crawls is
// deterministic, so the desktop app computes it once and stores the rendered
// payload here keyed by the ordered (prev, curr) pair — revisiting a diff is
// then a single registry read instead of two LoadPages passes. The payload is
// an opaque JSON blob owned by the caller; rows are purged whenever either
// side's content can change (crawl deleted, resumed, or re-analyzed), and are
// otherwise free to delete at any time — they are a cache, never authority.

// SaveComparison inserts or replaces the cached payload for a crawl pair.
func SaveComparison(dir, prevID, currID string, payload []byte) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`INSERT INTO comparisons(prev_id, curr_id, created, payload) VALUES(?,?,?,?)
		ON CONFLICT(prev_id, curr_id) DO UPDATE SET created = excluded.created, payload = excluded.payload`,
		prevID, currID, time.Now().Unix(), string(payload))
	return err
}

// GetComparison returns the cached payload and its computation time, or a nil
// payload when the pair has never been compared (not an error).
func GetComparison(dir, prevID, currID string) ([]byte, time.Time, error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer reg.Close()
	var payload string
	var created int64
	err = reg.QueryRow(`SELECT payload, created FROM comparisons WHERE prev_id = ? AND curr_id = ?`,
		prevID, currID).Scan(&payload, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return []byte(payload), time.Unix(created, 0), nil
}

// DeleteComparison drops one cached pair. Missing rows are fine.
func DeleteComparison(dir, prevID, currID string) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`DELETE FROM comparisons WHERE prev_id = ? AND curr_id = ?`, prevID, currID)
	return err
}

// PurgeComparisons drops every cached pair involving the crawl — called when
// its content changes (resume, re-analyze) or it is deleted.
func PurgeComparisons(dir, crawlID string) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`DELETE FROM comparisons WHERE prev_id = ? OR curr_id = ?`, crawlID, crawlID)
	return err
}
