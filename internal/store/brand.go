package store

import (
	"database/sql"
	"time"
)

// Brand data lives in the registry database keyed by host, so every crawl of
// the same site shares it and it survives across crawls and app launches.
// Today that's just the favicon (fetched once, then served from disk); the
// table has room to grow into other per-brand metadata later.

// GetBrandLogo returns the cached favicon for host: the raw image bytes and its
// content type. Both are empty (with a nil error) when nothing is cached yet.
func GetBrandLogo(dir, host string) (data []byte, contentType string, err error) {
	reg, err := registryDB(dir)
	if err != nil {
		return nil, "", err
	}
	defer reg.Close()
	err = reg.QueryRow(`SELECT logo, logo_type FROM brands WHERE host = ?`, host).Scan(&data, &contentType)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

// SaveBrandLogo caches host's favicon (replacing any previous one) so the logo
// is fetched from the network only the first time a brand is seen.
func SaveBrandLogo(dir, host, contentType string, data []byte) error {
	reg, err := registryDB(dir)
	if err != nil {
		return err
	}
	defer reg.Close()
	_, err = reg.Exec(`INSERT OR REPLACE INTO brands(host, logo, logo_type, fetched) VALUES(?,?,?,?)`,
		host, data, contentType, time.Now().Unix())
	return err
}
