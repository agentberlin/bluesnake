package crawler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/proxypool"
	"github.com/agentberlin/bluesnake/internal/robots"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// robotsMgr applies the configured robots.txt policy: per-host fetch+cache
// in respect mode, no download at all in ignore mode, download-but-disobey in
// ignore-report mode, and per-host custom robots.txt overrides that replace
// the live file (the tester workflow — the live site is never consulted).
// When the site-check pass may run, the raw retrieval is retained alongside
// the parsed file so the robots audit reuses the same single fetch.
type robotsMgr struct {
	cfg    *config.Config
	client sitecheck.Fetcher
	retain bool // keep raw fetch records for the site-check pass
	// esc is the crawl's proxy_on_block switch (nil when off). A robots.txt
	// fetch that came back blocked before the switch is cached as allow-all
	// like any non-2xx answer — but only until the switch: after it, the entry
	// is fetched again through the proxy and its real rules apply.
	esc *proxypool.Escalation

	mu     sync.Mutex
	cache  map[string]*robotsEntry // scheme://host[:port]
	custom map[string]*robots.File // hostname
}

type robotsEntry struct {
	file *robots.File
	rec  *sitecheck.RobotsFetch // nil unless retained
	// blocked marks a pre-switch fetch the site answered with a block signal.
	blocked bool
}

func newRobotsMgr(cfg *config.Config, client sitecheck.Fetcher, esc *proxypool.Escalation) (*robotsMgr, error) {
	m := &robotsMgr{
		cfg:    cfg,
		client: client,
		esc:    esc,
		retain: cfg.SiteChecks.Enabled != "never" && (cfg.SiteChecks.Robots || cfg.SiteChecks.Sitemap),
		cache:  make(map[string]*robotsEntry),
		custom: make(map[string]*robots.File),
	}
	for _, cr := range cfg.Robots.Custom {
		data, err := os.ReadFile(cr.File)
		if err != nil {
			return nil, fmt.Errorf("robots.custom for %s: %w", cr.Host, err)
		}
		m.custom[strings.ToLower(cr.Host)] = robots.Parse(data)
	}
	return m, nil
}

// wasBlocked reports whether the URL's host answered its robots.txt fetch
// with a block signal before a proxy_on_block switch.
func (m *robotsMgr) wasBlocked(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.cache[u.Scheme+"://"+u.Host]
	return ok && e.blocked
}

// customFor returns the custom robots.txt override for a hostname, nil when
// none is configured.
func (m *robotsMgr) customFor(host string) *robots.File {
	return m.custom[strings.ToLower(host)]
}

func (m *robotsMgr) check(ctx context.Context, rawURL string) robots.Verdict {
	if m.cfg.Robots.Mode == "ignore" {
		return robots.Verdict{Allowed: true}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return robots.Verdict{Allowed: true}
	}

	var file *robots.File
	if custom, ok := m.custom[strings.ToLower(u.Hostname())]; ok {
		file = custom
	} else {
		file = m.fileFor(ctx, u)
	}
	if m.cfg.Robots.Mode == "ignore-report" {
		return robots.Verdict{Allowed: true}
	}
	return file.Verdict(m.cfg.HTTP.RobotsUserAgent, rawURL)
}

// sitemapsFor returns the Sitemap directives for the URL's host, honoring
// the robots policy: nothing in ignore mode (robots.txt is never downloaded),
// the custom file's directives when one overrides the host, and the cached
// live file otherwise — shared with rule checking, never a second fetch.
func (m *robotsMgr) sitemapsFor(ctx context.Context, rawURL string) []string {
	if m.cfg.Robots.Mode == "ignore" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	if custom, ok := m.custom[strings.ToLower(u.Hostname())]; ok {
		return custom.Sitemaps
	}
	return m.fileFor(ctx, u).Sitemaps
}

// fileFor fetches and caches robots.txt per scheme+host. Retrieval semantics
// live in sitecheck.FetchRobots (up to five redirect hops — Google REP:
// robots.txt for the original host is whatever the chain resolves to, even
// cross-host; a seed-host 308 to the www robots.txt is the common case).
// Non-2xx terminal responses (and network errors) yield an allow-all file,
// matching Google's 4xx rule.
func (m *robotsMgr) fileFor(ctx context.Context, u *url.URL) *robots.File {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entryFor(ctx, u.Scheme+"://"+u.Host, m.retain).file
}

// fetchRecordFor returns the raw robots.txt retrieval for a site root — the
// site-check pass's input. It shares fileFor's cache, so a crawl that already
// consulted robots.txt for gating never fetches the file a second time; in
// ignore mode (policy never downloads) this is the one fetch that happens.
func (m *robotsMgr) fetchRecordFor(ctx context.Context, root string) *sitecheck.RobotsFetch {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entryFor(ctx, root, true).rec
}

// entryFor is the shared fetch+cache body; callers hold m.mu. withRec demands
// a retained raw record: a cache hit without one (unreachable in practice —
// records are retained whenever the pass can run) refetches and overwrites.
func (m *robotsMgr) entryFor(ctx context.Context, key string, withRec bool) *robotsEntry {
	if e, ok := m.cache[key]; ok && (!withRec || e.rec != nil) && !(e.blocked && m.esc.Escalated()) {
		return e
	}
	rf := sitecheck.FetchRobots(ctx, m.client, key)
	var file *robots.File
	if rf.Found() {
		file = robots.Parse(rf.Body)
	} else {
		file = robots.Parse(nil)
	}
	e := &robotsEntry{file: file}
	// Blocked is decided by the route the answer came over, not the switch
	// state before the fetch: the fetch can wait out a drain at the gate and
	// go through the proxy, and a block there must not mark the entry for a
	// refetch on every later check of the host.
	if m.esc != nil && rf.Proxy == proxypool.DirectLabel && proxypool.Classify(rf.Status, nil, rf.FetchError) != proxypool.NotBlock {
		e.blocked = true
	}
	if withRec {
		e.rec = rf
	}
	m.cache[key] = e
	return e
}

func mustCompile(pattern string) *regexp.Regexp {
	// patterns are pre-validated by config.Validate
	return regexp.MustCompile(pattern)
}
