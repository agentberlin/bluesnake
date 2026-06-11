package crawler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/fetch"
	"github.com/hhsecond/acrawler/internal/robots"
)

// robotsMgr applies the configured robots.txt policy: per-host fetch+cache
// in respect mode, no download at all in ignore mode, download-but-disobey in
// ignore-report mode, and per-host custom robots.txt overrides that replace
// the live file (the tester workflow — the live site is never consulted).
type robotsMgr struct {
	cfg    *config.Config
	client *fetch.Client

	mu     sync.Mutex
	cache  map[string]*robots.File // scheme://host[:port]
	custom map[string]*robots.File // hostname
}

func newRobotsMgr(cfg *config.Config, client *fetch.Client) (*robotsMgr, error) {
	m := &robotsMgr{
		cfg:    cfg,
		client: client,
		cache:  make(map[string]*robots.File),
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

// fileFor fetches and caches robots.txt per scheme+host. Non-2xx responses
// (and network errors) yield an allow-all file, matching Google's 4xx rule.
func (m *robotsMgr) fileFor(ctx context.Context, u *url.URL) *robots.File {
	key := u.Scheme + "://" + u.Host

	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.cache[key]; ok {
		return f
	}
	res := m.client.Fetch(ctx, key+"/robots.txt")
	var file *robots.File
	if res.FetchError == "" && res.StatusCode >= 200 && res.StatusCode < 300 {
		file = robots.Parse(res.Body)
	} else {
		file = robots.Parse(nil)
	}
	m.cache[key] = file
	return file
}

func mustCompile(pattern string) *regexp.Regexp {
	// patterns are pre-validated by config.Validate
	return regexp.MustCompile(pattern)
}
