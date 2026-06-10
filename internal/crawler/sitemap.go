package crawler

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/fetch"
	"github.com/hhsecond/acrawler/internal/frontier"
	"github.com/hhsecond/acrawler/internal/robots"
	"github.com/hhsecond/acrawler/internal/urlutil"
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

// crawlSitemaps fetches the configured (and robots-discovered) sitemaps,
// records their entries, and returns the listed URLs as crawl candidates.
func (c *Crawler) crawlSitemaps(ctx context.Context, seed string) []frontier.Item {
	urls := append([]string{}, c.cfg.Sitemaps.URLs...)
	if c.cfg.Sitemaps.AutoDiscoverViaRobots {
		if u, err := url.Parse(seed); err == nil {
			res := c.client.Fetch(ctx, u.Scheme+"://"+u.Host+"/robots.txt")
			if res.FetchError == "" && res.StatusCode == 200 {
				urls = append(urls, robots.Parse(res.Body).Sitemaps...)
			}
		}
	}
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
			if d, ok := c.admitTarget(norm, frontier.Item{URL: seed, Depth: -1}, false); ok {
				d.Depth = 0
				d.Source = ""
				items = append(items, d)
			}
		}
	}
	for _, u := range urls {
		walk(u, 0)
	}
	return items
}
