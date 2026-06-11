// Package frontier is the crawl's admission control: URL dedup and every
// pre-fetch limit (depth, per-depth, folder depth, query strings, URL length,
// per-subdomain, per-path caps). In-memory for now; the SQLite mirror for
// resume arrives with the storage milestone (DESIGN.md §5.2, §5.3).
package frontier

import (
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// Item is one admitted crawl candidate.
type Item struct {
	URL          string // normalized URL-encoded address
	Depth        int    // link hops from the seed; redirects count as a hop
	RedirectHops int    // consecutive redirect hops leading here
	Source       string // first discovering page ("" for seeds)
}

type Frontier struct {
	cfg *config.Config

	mu       sync.Mutex
	seen     map[string]bool
	perDepth map[int]int
	perSub   map[string]int
	perPath  []int // counters parallel to cfg.Limits.ByPath
}

func New(cfg *config.Config) *Frontier {
	return &Frontier{
		cfg:      cfg,
		seen:     make(map[string]bool),
		perDepth: make(map[int]int),
		perSub:   make(map[string]int),
		perPath:  make([]int, len(cfg.Limits.ByPath)),
	}
}

// Admit reports whether the item passes dedup and all configured limits, and
// records it. An admitted item is the caller's to crawl; rejected items are
// silently dropped (matching Screaming Frog: over-limit URLs are not reported).
func (f *Frontier) Admit(it Item) bool {
	lim := &f.cfg.Limits

	if lim.MaxURLLength > 0 && len(it.URL) > lim.MaxURLLength {
		return false
	}
	if lim.MaxDepth >= 0 && it.Depth > lim.MaxDepth {
		return false
	}
	if lim.MaxQueryStrings >= 0 && urlutil.QueryParamCount(it.URL) > lim.MaxQueryStrings {
		return false
	}
	if lim.MaxFolderDepth >= 0 && urlutil.FolderDepth(it.URL) > lim.MaxFolderDepth {
		return false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.seen[it.URL] {
		return false
	}
	if lim.MaxURLsPerDepth >= 0 && f.perDepth[it.Depth] >= lim.MaxURLsPerDepth {
		return false
	}
	host := urlutil.Host(it.URL)
	if lim.MaxPerSubdomain >= 0 && f.perSub[host] >= lim.MaxPerSubdomain {
		return false
	}
	pathIdx := -1
	for i, pl := range lim.ByPath {
		if strings.Contains(it.URL, pl.Pattern) {
			if f.perPath[i] >= pl.Max {
				return false
			}
			pathIdx = i
			break
		}
	}

	f.seen[it.URL] = true
	f.perDepth[it.Depth]++
	f.perSub[host]++
	if pathIdx >= 0 {
		f.perPath[pathIdx]++
	}
	return true
}

// MarkSeen records URLs as already processed (resume preseeding) without
// counting them against any limit.
func (f *Frontier) MarkSeen(urls []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range urls {
		f.seen[u] = true
	}
}

// Seen reports whether a URL was already admitted.
func (f *Frontier) Seen(url string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[url]
}

// Admitted returns the number of admitted URLs.
func (f *Frontier) Admitted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}
