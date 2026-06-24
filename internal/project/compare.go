package project

import (
	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// Compare runs the existing pairwise crawl comparison (internal/compare,
// unchanged) on two crawl IDs — the per-competitor over-time URL/issue diff,
// after ComparePair resolves a site's two latest comparable crawls. The current
// crawl's frozen config drives the comparison (matches the desktop behavior).
func (s *Store) Compare(prevID, currID string) (*compare.Result, error) {
	prev, err := s.compareInput(prevID)
	if err != nil {
		return nil, err
	}
	curr, err := s.compareInput(currID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.crawlConfig(currID)
	if err != nil {
		return nil, err
	}
	return compare.Run(prev, curr, cfg)
}

func (s *Store) compareInput(id string) (compare.Input, error) {
	st, err := store.OpenCrawl(s.dir, id)
	if err != nil {
		return compare.Input{}, err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return compare.Input{}, err
	}
	counts, err := st.IssueCounts()
	if err != nil {
		return compare.Input{}, err
	}
	iss := map[string][]string{}
	for issueID, n := range counts {
		if n == 0 {
			continue
		}
		urls, err := st.IssueURLs(issueID)
		if err != nil {
			continue
		}
		iss[issueID] = urls
	}
	return compare.Input{Pages: pages, Issues: iss}, nil
}

func (s *Store) crawlConfig(id string) (*config.Config, error) {
	st, err := store.OpenCrawl(s.dir, id)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	yamlStr, err := st.Meta("config")
	if err != nil {
		return nil, err
	}
	return config.Load([]byte(yamlStr))
}
