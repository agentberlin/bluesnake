package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestPageDetailDiscoveryPath(t *testing.T) {
	a := testApp(t)

	const (
		seedURL = "https://site.test/"
		urlA    = "https://site.test/a"
	)
	st, err := store.CreateCrawl(a.storeDir, []string{seedURL}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	recs := []*crawler.PageRecord{
		{URL: seedURL, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: ""},
		{URL: urlA, Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: 1, DiscoveredFrom: seedURL},
	}
	for _, r := range recs {
		if err := st.Page(r); err != nil {
			t.Fatal(err)
		}
	}
	id := st.ID
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := a.PageDetail(id, urlA)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{seedURL, urlA}; !slices.Equal(d.DiscoveryPath, want) {
		t.Errorf("DiscoveryPath = %v, want %v", d.DiscoveryPath, want)
	}
	js, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"discoveryPath"`) {
		t.Errorf("PageDetail JSON missing discoveryPath key: %s", js)
	}

	seed, err := a.PageDetail(id, seedURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{seedURL}; !slices.Equal(seed.DiscoveryPath, want) {
		t.Errorf("seed DiscoveryPath = %v, want %v (just the url itself)", seed.DiscoveryPath, want)
	}
}
