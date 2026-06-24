package project

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// SiteCrawl is one crawl associated with a site (its seed host[:port] matches
// the site key), classified for comparability.
type SiteCrawl struct {
	ID         string    `json:"id"`
	Seed       string    `json:"seed"`
	Mode       string    `json:"mode"`
	Status     string    `json:"status"`
	Started    time.Time `json:"started"`
	Crawled    int       `json:"crawled"`
	Total      int       `json:"total"`
	Comparable bool      `json:"comparable"`
	Reason     string    `json:"reason,omitempty"` // why not comparable
}

// seedKey extracts the site key (lowercased host[:port]) from a stored seed,
// which is the verbatim, un-normalized URL the crawl was started with.
func seedKey(seed string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(seed))
	if err != nil || u.Host == "" {
		return "", false
	}
	return strings.ToLower(u.Host), true
}

// isRootSeed reports whether the seed addresses the site root (path "" or "/").
// Query and fragment never disqualify a root.
func isRootSeed(seed string) bool {
	u, err := url.Parse(strings.TrimSpace(seed))
	if err != nil {
		return false
	}
	p := u.EscapedPath()
	return p == "" || p == "/"
}

// SiteHistory returns every crawl associated with the site key for `domain`
// (resolved live from the crawl registry), newest first, each classified for
// comparability. Comparable = spider + root seed + finished + not scope-narrowed.
func (s *Store) SiteHistory(domain string) ([]SiteCrawl, error) {
	key, err := SiteKey(domain)
	if err != nil {
		return nil, err
	}
	infos, err := store.ListCrawls(s.dir)
	if err != nil {
		return nil, err
	}
	var out []SiteCrawl
	for _, in := range infos {
		sk, ok := seedKey(in.Seed)
		if !ok || sk != key {
			continue
		}
		sc := SiteCrawl{
			ID: in.ID, Seed: in.Seed, Mode: in.Mode, Status: in.Status,
			Started: in.Started, Crawled: in.Crawled, Total: in.Total,
		}
		sc.Comparable, sc.Reason = s.classify(in)
		out = append(out, sc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// classify decides comparability of an associated crawl, cheapest gates first;
// it opens the crawl DB (to read the frozen config) only when the registry-only
// gates pass.
func (s *Store) classify(in store.Info) (bool, string) {
	if in.Mode == "list" {
		return false, "list crawl"
	}
	if !isRootSeed(in.Seed) {
		return false, "path-scoped seed"
	}
	switch in.Status {
	case store.StatusRunning:
		return false, "running"
	case store.StatusCompleted:
		// registry gates pass — fall through to the config-level check
	default:
		return false, "unfinished"
	}
	if scopeNarrowed(s.dir, in.ID) {
		return false, "scope-narrowed"
	}
	return true, ""
}

// scopeNarrowed reports whether a crawl's frozen config restricts scope with
// include rules (meaning it did not cover the whole site). A read error is
// treated as not-narrowed so a transient open failure never silently hides a
// crawl from comparison.
func scopeNarrowed(dir, id string) bool {
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		return false
	}
	defer st.Close()
	yamlStr, err := st.Meta("config")
	if err != nil || yamlStr == "" {
		return false
	}
	cfg, err := config.Load([]byte(yamlStr))
	if err != nil {
		return false
	}
	return len(cfg.Scope.Include) > 0
}

// comparable returns a site's comparable crawls, newest first.
func (s *Store) comparable(domain string) ([]SiteCrawl, error) {
	hist, err := s.SiteHistory(domain)
	if err != nil {
		return nil, err
	}
	var c []SiteCrawl
	for _, sc := range hist {
		if sc.Comparable {
			c = append(c, sc)
		}
	}
	return c, nil
}

// LatestComparable returns a site's most recent comparable crawl, if any.
func (s *Store) LatestComparable(domain string) (SiteCrawl, bool, error) {
	c, err := s.comparable(domain)
	if err != nil || len(c) == 0 {
		return SiteCrawl{}, false, err
	}
	return c[0], true, nil
}

// ComparePair returns the two most recent comparable crawls of a site for the
// per-competitor over-time diff: prev = older, curr = newest. ok is false when
// the site has fewer than two comparable crawls.
func (s *Store) ComparePair(domain string) (prevID, currID string, ok bool, err error) {
	c, err := s.comparable(domain)
	if err != nil {
		return "", "", false, err
	}
	if len(c) < 2 {
		return "", "", false, nil
	}
	return c[1].ID, c[0].ID, true, nil
}
