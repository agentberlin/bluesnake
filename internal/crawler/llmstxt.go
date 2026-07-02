package crawler

import (
	"context"
	"net/url"

	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/llmstxt"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// LlmsTxtRecord is one fetched /llms.txt (or /llms-full.txt) file, with the
// outcome of structural validation. It is recorded on the sink so the analysis
// phase can emit the file-level checks (missing / invalid / no summary / ...).
type LlmsTxtRecord struct {
	URL       string
	Kind      string // llms_txt | llms_full_txt
	Status    int
	Found     bool
	Title     string
	Summary   string
	Malformed bool
	Content   []byte
}

// LlmsTxtSink is the optional sink extension for the llms.txt audit: the file
// record plus one row per curated link. Provenance (LlmsTxtLink) is recorded
// for every link regardless of whether it is crawled, so "was this URL listed
// in llms.txt?" stays answerable independently of the link graph.
type LlmsTxtSink interface {
	LlmsTxtFile(rec LlmsTxtRecord) error
	LlmsTxtLink(src, url, section, anchor string) error
}

// crawlLlmsTxt fetches the seed host's /llms.txt (and, when enabled,
// /llms-full.txt), validates it, records the file + its curated links on the
// sink, and returns the links as crawl candidates when crawl_linked is on.
//
// Unlike sitemap entries, curated links go through the normal scope rules: an
// external link is admitted only when external crawling is enabled, so with the
// house profile (externals off) it is recorded for the report but never fetched
// — it surfaces as an "unverified" link in analysis instead. Links are seeded at
// Depth 0 (Source "") like sitemap URLs; recomputeDepths gives them their real
// followed-link depth, or NoDepth when nothing links to them.
func (c *Crawler) crawlLlmsTxt(ctx context.Context, seed string) []frontier.Item {
	u, err := url.Parse(seed)
	if err != nil {
		return nil
	}
	base := u.Scheme + "://" + u.Host

	kinds := []struct{ path, kind string }{{"/llms.txt", "llms_txt"}}
	if c.cfg.LlmsTxt.FetchFull {
		kinds = append(kinds, struct{ path, kind string }{"/llms-full.txt", "llms_full_txt"})
	}
	sink, hasSink := c.sink.(LlmsTxtSink)

	var items []frontier.Item
	for _, k := range kinds {
		target := base + k.path
		// Under the global fetch cap like every crawl fetch (H1): with M crawls
		// starting in parallel, uncapped llms.txt fetches would exceed the cap.
		res := c.fetchCapped(ctx, target)
		if res == nil {
			return items // crawl cancelled while waiting for a slot
		}
		found := res.FetchError == "" && res.StatusCode == 200
		rec := LlmsTxtRecord{URL: target, Kind: k.kind, Status: res.StatusCode, Found: found, Content: res.Body}
		var file *llmstxt.File
		if found {
			file = llmstxt.Parse(res.Body)
			rec.Title, rec.Summary, rec.Malformed = file.Title, file.Summary, file.Malformed
		}
		if hasSink {
			c.noteSinkErr(sink.LlmsTxtFile(rec))
		}
		// Only the primary llms.txt carries a curated link index; llms-full.txt
		// is expanded prose for LLM context, not a link list.
		if k.kind != "llms_txt" || file == nil {
			continue
		}
		for _, l := range file.Links {
			// curated links may be relative; resolve them against the llms.txt URL
			norm, err := urlutil.Resolve(target, l.URL, c.opts)
			if err != nil {
				continue
			}
			if hasSink {
				c.noteSinkErr(sink.LlmsTxtLink(target, norm, l.Section, l.Name))
			}
			if !c.cfg.LlmsTxt.CrawlLinked {
				continue
			}
			if c.classify(norm) == urlutil.External && !c.cfg.Links.External.Crawl {
				continue
			}
			if d, ok := c.admitTarget(norm, frontier.Item{URL: target, Depth: -1}, false); ok {
				d.Depth = 0
				d.Source = ""
				items = append(items, d)
			}
		}
	}
	return items
}
