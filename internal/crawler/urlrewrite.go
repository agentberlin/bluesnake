package crawler

import (
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// URLOptions are the normalization knobs the crawl applies to every URL it keys
// a page by (DESIGN.md §5.2), derived from the crawl's frozen config.
func URLOptions(cfg *config.Config) urlutil.Options {
	return urlutil.Options{
		KeepFragments: cfg.Advanced.CrawlFragments,
		LowercaseHex:  cfg.Advanced.PercentEncoding == "lower",
	}
}

// NewURLRewriter builds the discovery rewriter the crawl runs every discovered
// URL through before fetching it: the url_rewriting rules, then normalization.
// Post-crawl consumers use it to map a URL as a page sent it — a redirect's
// Location, a canonical — onto the key the crawl stored that target under.
func NewURLRewriter(cfg *config.Config) *urlutil.Rewriter {
	var replaces []urlutil.RegexReplace
	for _, rr := range cfg.URLRewriting.RegexReplace {
		replaces = append(replaces, urlutil.RegexReplace{
			Pattern: mustCompile(rr.Pattern), Replace: rr.Replace,
		})
	}
	return urlutil.NewRewriter(cfg.URLRewriting.RemoveParams, replaces, cfg.URLRewriting.Lowercase, URLOptions(cfg))
}
