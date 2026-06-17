package crawler

import (
	"context"
	"encoding/xml"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// FetchSitemapURLs downloads a sitemap (or sitemap index) and returns the
// listed page URLs — the list-mode "download XML sitemap" input source.
func FetchSitemapURLs(ctx context.Context, cfg *config.Config, sitemapURL string) ([]string, error) {
	client, err := fetch.New(cfg)
	if err != nil {
		return nil, err
	}
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

// SitemapSink is the optional sink extension for sitemap entries.
type SitemapSink interface {
	SitemapEntry(sitemap, url string) error
}

type sitemapURLSet struct {
	URLs []struct {
		Loc string `xml:"loc"`
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
	return c.enumerateSitemaps(ctx, urls, seed)
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
		return nil
	}
	return c.enumerateSitemaps(ctx, urls, pageURL)
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
	var items []frontier.Item
	seen := map[string]bool{}
	var walk func(sitemapURL string, depth int)
	walk = func(sitemapURL string, depth int) {
		if seen[sitemapURL] || depth > 2 {
			return
		}
		seen[sitemapURL] = true
		res := c.client.Fetch(ctx, sitemapURL)
		if res.FetchError != "" || res.StatusCode != 200 {
			return
		}
		var set sitemapURLSet
		if err := xml.Unmarshal(res.Body, &set); err != nil {
			return
		}
		for _, child := range set.Sitemaps {
			walk(child.Loc, depth+1)
		}
		for _, entry := range set.URLs {
			norm, err := urlutil.Normalize(entry.Loc, c.opts)
			if err != nil {
				continue
			}
			if sink, ok := c.sink.(SitemapSink); ok && c.sink != nil {
				c.noteSinkErr(sink.SitemapEntry(sitemapURL, norm))
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
	return items
}
