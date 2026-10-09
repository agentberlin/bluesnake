package crawler

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/proxypool"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// FetchSitemapURLs downloads a sitemap (or sitemap index) and returns the
// listed page URLs — the list-mode "download XML sitemap" input source.
func FetchSitemapURLs(ctx context.Context, cfg *config.Config, sitemapURL string) ([]string, error) {
	client, err := fetch.New(cfg)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections() // transient client: don't pin its conns past the walk
	opts := urlutil.Options{KeepFragments: cfg.Advanced.CrawlFragments}
	var urls []string
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(u string, depth int) error {
		if seen[u] || depth > 2 {
			return nil
		}
		seen[u] = true
		res := client.Fetch(ctx, u)
		if res.FetchError != "" {
			return fmt.Errorf("fetching sitemap %s: %s", u, res.FetchError)
		}
		if res.StatusCode != 200 {
			return fmt.Errorf("fetching sitemap %s: status %d", u, res.StatusCode)
		}
		var set sitemapURLSet
		if err := xml.Unmarshal(res.Body, &set); err != nil {
			return fmt.Errorf("parsing sitemap %s: %w", u, err)
		}
		for _, child := range set.Sitemaps {
			if err := walk(child.Loc, depth+1); err != nil {
				return err
			}
		}
		for _, entry := range set.URLs {
			if norm, err := urlutil.Normalize(entry.Loc, opts); err == nil {
				urls = append(urls, norm)
			}
		}
		return nil
	}
	if err := walk(sitemapURL, 0); err != nil {
		return nil, err
	}
	return urls, nil
}

// SitemapSink is the optional sink extension for sitemap entries: one call per
// URL a sitemap lists, with the <lastmod> that entry gave it ("" for none).
type SitemapSink interface {
	SitemapEntry(sitemap, url, lastmod string) error
}

type sitemapURLSet struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		Lastmod string `xml:"lastmod"`
	} `xml:"url"`
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

// crawlSitemaps fetches the seed host's configured (and robots-discovered)
// sitemaps, records their entries, and returns the listed URLs as crawl
// candidates. Per-host discovery for any other in-scope host the crawl later
// enters is handled by discoverHostSitemaps (R17); claiming the seed host here
// keeps that path from re-running discovery for the seed.
func (c *Crawler) crawlSitemaps(ctx context.Context, seed string) []frontier.Item {
	c.claimSitemapHost(seed)
	urls := append([]string{}, c.cfg.Sitemaps.URLs...)
	if c.cfg.Sitemaps.AutoDiscoverViaRobots {
		// discovery goes through the robots manager: it honors ignore mode
		// (no download), custom per-host overrides, and the rule-check cache
		urls = append(urls, c.robots.sitemapsFor(ctx, seed)...)
	}
	items, blocked := c.walkSitemaps(ctx, urls, seed)
	c.rerunSitemapsIfBlocked(seed, blocked, true)
	return items
}

// rerunSitemapsIfBlocked schedules sitemap discovery for src to run again
// after a proxy_on_block switch when the site answered robots.txt or a sitemap
// fetch with a block signal: those sitemaps were never read, and the URLs they
// list would otherwise be missed for good.
func (c *Crawler) rerunSitemapsIfBlocked(src string, blocked, withConfigured bool) {
	if !c.egress.preSwitch() || (!blocked && !c.robots.wasBlocked(src)) {
		return
	}
	// A sitemap that still answers with a block on its last direct try was
	// never recorded before proxy_on_block either: lastTry changes nothing.
	c.egress.addRerun(func(ctx context.Context, _ bool) []frontier.Item {
		var urls []string
		if withConfigured {
			urls = append(urls, c.cfg.Sitemaps.URLs...)
		}
		if c.cfg.Sitemaps.AutoDiscoverViaRobots {
			urls = append(urls, c.robots.sitemapsFor(ctx, src)...)
		}
		items, _ := c.walkSitemaps(ctx, urls, src)
		return items
	})
}

// discoverHostSitemaps runs robots-based sitemap auto-discovery for an in-scope
// host the crawl has just entered (R17). Sitemap discovery is otherwise
// seed-host-only, so sitemap-only pages on other in-scope hosts (additional
// subdomains, with crawl_all_subdomains on) are never found. Returns nil when
// discovery is disabled, this is list mode, the host was already processed, or
// the host advertises no sitemap. The per-host guard makes it run once per host.
func (c *Crawler) discoverHostSitemaps(ctx context.Context, pageURL string) []frontier.Item {
	if c.cfg.Mode == "list" || !c.cfg.Sitemaps.CrawlLinked || !c.cfg.Sitemaps.AutoDiscoverViaRobots {
		return nil
	}
	if !c.claimSitemapHost(pageURL) {
		return nil
	}
	urls := c.robots.sitemapsFor(ctx, pageURL)
	if len(urls) == 0 {
		c.rerunSitemapsIfBlocked(pageURL, false, false)
		return nil
	}
	items, blocked := c.walkSitemaps(ctx, urls, pageURL)
	c.rerunSitemapsIfBlocked(pageURL, blocked, false)
	return items
}

// claimSitemapHost marks a host's sitemap auto-discovery as done, returning true
// only for the first caller per host (authority). Concurrency-safe: process runs
// many crawlOne goroutines that may reach a new host at the same time.
func (c *Crawler) claimSitemapHost(rawURL string) bool {
	host := urlutil.Authority(rawURL)
	c.sitemapMu.Lock()
	defer c.sitemapMu.Unlock()
	if c.sitemapHosts[host] {
		return false
	}
	c.sitemapHosts[host] = true
	return true
}

// enumerateSitemaps walks the given sitemaps (following sitemap-index children
// up to two levels), records each entry on the sink, and returns the listed URLs
// as crawl candidates. Sitemap-discovered URLs carry no followed-link depth
// (Depth 0, Source "") — recomputeDepths assigns them NoDepth unless a link also
// reaches them. Safe to call concurrently (only local state is mutated).
func (c *Crawler) enumerateSitemaps(ctx context.Context, sitemapURLs []string, src string) []frontier.Item {
	items, _ := c.walkSitemaps(ctx, sitemapURLs, src)
	return items
}

// walkSitemaps is enumerateSitemaps that also reports whether any sitemap
// fetch came back blocked before a proxy_on_block switch. Under the toggle,
// sitemaps already read in full are skipped, so a post-switch rerun fetches
// only what the block hid and never records an entry twice.
func (c *Crawler) walkSitemaps(ctx context.Context, sitemapURLs []string, src string) ([]frontier.Item, bool) {
	var items []frontier.Item
	blocked := false
	seen := map[string]bool{}
	var walk func(sitemapURL string, depth int)
	walk = func(sitemapURL string, depth int) {
		if seen[sitemapURL] || depth > 2 || c.sitemapRead(sitemapURL) {
			return
		}
		seen[sitemapURL] = true
		// Under the global fetch cap like every crawl fetch (H1): the sitemap
		// walk can touch dozens of files, and with M crawls starting in
		// parallel an uncapped walk would exceed the cap.
		res := c.fetchCapped(ctx, sitemapURL)
		if res == nil { // crawl cancelled while waiting for a slot
			return
		}
		if c.egress.preSwitch() && res.Proxy == proxypool.DirectLabel && classify(res) != proxypool.NotBlock {
			blocked = true
			return
		}
		if res.FetchError != "" || res.StatusCode != 200 {
			return
		}
		var set sitemapURLSet
		if err := xml.Unmarshal(res.Body, &set); err != nil {
			return
		}
		c.markSitemapRead(sitemapURL)
		for _, child := range set.Sitemaps {
			walk(child.Loc, depth+1)
		}
		for _, entry := range set.URLs {
			norm, err := urlutil.Normalize(entry.Loc, c.opts)
			if err != nil {
				continue
			}
			if sink, ok := c.sink.(SitemapSink); ok && c.sink != nil {
				// lastmod is kept as written — a W3C datetime of any precision, or
				// whatever the site put there — with only the XML whitespace around
				// it dropped; judging it is the consumer's call.
				c.noteSinkErr(sink.SitemapEntry(sitemapURL, norm, strings.TrimSpace(entry.Lastmod)))
			}
			if d, ok := c.admitTarget(norm, frontier.Item{URL: src, Depth: -1}, false); ok {
				d.Depth = 0
				d.Source = ""
				items = append(items, d)
			}
		}
	}
	for _, u := range sitemapURLs {
		walk(u, 0)
	}
	return items, blocked
}

// sitemapRead / markSitemapRead track sitemaps read in full, for
// proxy_on_block reruns only (nil-map reads are false; nothing is tracked when
// the toggle is off).
func (c *Crawler) sitemapRead(u string) bool {
	if c.egress == nil {
		return false
	}
	c.sitemapMu.Lock()
	defer c.sitemapMu.Unlock()
	return c.sitemapsRead[u]
}

func (c *Crawler) markSitemapRead(u string) {
	if c.egress == nil {
		return
	}
	c.sitemapMu.Lock()
	if c.sitemapsRead == nil {
		c.sitemapsRead = map[string]bool{}
	}
	c.sitemapsRead[u] = true
	c.sitemapMu.Unlock()
}
