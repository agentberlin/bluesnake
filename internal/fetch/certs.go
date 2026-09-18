package fetch

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// certExts are the file extensions scanned in a trusted-certificate directory.
var certExts = map[string]bool{".pem": true, ".crt": true, ".cer": true, ".ca": true}

// rootPool builds the certificate pool for http.trusted_cert_dirs: the system
// roots plus every PEM found in the configured directories. It returns nil when
// no directories are configured, which leaves the transport on the system roots
// alone.
//
// This is what makes a MITM proxy usable without turning verification off.
// Providers whose proxies terminate TLS — Bright Data's native proxy among them
// — re-sign every response with their own root, so a crawl through one either
// trusts that root or skips verification entirely. Skipping is not an option
// here: bluesnake reports on HSTS, mixed content and certificate validity, and
// an auditor that does not verify its own connections cannot audit anyone
// else's.
//
// Every failure is loud. A directory that cannot be read, a file that holds no
// certificate, or a directory with nothing to load is a configuration error at
// client construction — not a silent skip that would surface later as an
// inexplicable x509 failure on every URL in the crawl.
func rootPool(dirs []string) (*x509.CertPool, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		// Platforms without a readable system store (some minimal containers)
		// still get the configured roots rather than an error.
		pool = x509.NewCertPool()
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("http.trusted_cert_dirs: %w", err)
		}
		loaded := 0
		for _, e := range entries {
			if e.IsDir() || !certExts[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			path := filepath.Join(dir, e.Name())
			pem, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("http.trusted_cert_dirs: %w", err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("http.trusted_cert_dirs: %s holds no PEM certificate", path)
			}
			loaded++
		}
		if loaded == 0 {
			return nil, fmt.Errorf("http.trusted_cert_dirs: %s holds no %s certificate files",
				dir, strings.Join(sortedExts(), "/"))
		}
	}
	return pool, nil
}

func sortedExts() []string {
	return []string{".pem", ".crt", ".cer", ".ca"}
}
