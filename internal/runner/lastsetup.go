package runner

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// The "last crawl setup" source (#88): a site remembers the configuration it
// last ran with, so per-site config divergence needs no storage at all — every
// crawl already freezes its effective config into its own database at
// CreateCrawl, and the registry knows each crawl's seed. "Last" is derived
// from those two facts at enqueue time, never persisted, so deleting a crawl
// simply forgets that setup and profile edits/deletes can't dangle.

// LastSetup is the frozen setup of a site's most recent spider crawl.
type LastSetup struct {
	CrawlID    string
	Seed       string
	Started    time.Time
	ConfigYAML string // the crawl's frozen config, verbatim
}

// FindLastSetup returns the setup of the most recent spider crawl whose seed
// is the same site as rawURL, or nil when the site has never been crawled.
// Site identity is the exact lowercased host[:port] of the seed — the project
// layer's rule (§5.9): scheme and path never matter, and www./subdomains/
// ports are distinct sites (no folding). List-mode crawls never count: their
// frozen config carries mode-specific adjustments (depth 0, robots ignored)
// that would be wrong to inherit into a spider crawl. A matching crawl whose
// database can't be read is a loud error naming the crawl — silently sliding
// to an older setup would be spooky.
func FindLastSetup(storeDir, rawURL string) (*LastSetup, error) {
	site := siteHost(rawURL)
	if site == "" {
		return nil, fmt.Errorf("cannot derive a site from %q", rawURL)
	}
	infos, err := store.ListCrawls(storeDir)
	if err != nil {
		return nil, err
	}
	// ListCrawls is ordered oldest→newest (started, then creation order), so
	// the last match is the most recent crawl of the site.
	for i := len(infos) - 1; i >= 0; i-- {
		in := infos[i]
		if in.Mode != "spider" || siteHost(in.Seed) != site {
			continue
		}
		st, err := store.OpenCrawl(storeDir, in.ID)
		if err != nil {
			return nil, fmt.Errorf("the last crawl of %s (%s) can't provide its setup: %w", site, in.ID, err)
		}
		cfgYAML, err := st.Meta("config")
		st.Close()
		if err != nil {
			return nil, fmt.Errorf("the last crawl of %s (%s) can't provide its setup: %w", site, in.ID, err)
		}
		return &LastSetup{CrawlID: in.ID, Seed: in.Seed, Started: in.Started, ConfigYAML: cfgYAML}, nil
	}
	return nil, nil
}

// siteHost extracts the lowercased host[:port] of a URL (or bare host), "" when
// none can be derived. Same identity rule as the project layer's SiteKey; kept
// separate so the core never imports the removable project package.
func siteHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		raw = "//" + raw // make url.Parse read a bare host as the authority
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}
