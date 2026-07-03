package store

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
)

func TestSiteCheckStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	recs := []crawler.SiteCheckRecord{
		{Kind: "robots", Subject: "https://ex.com/robots.txt", Report: []byte(`{"found":false}`)},
		{Kind: "sitemap", Subject: "https://ex.com", Report: []byte(`{"missing":true}`)},
	}
	for _, rec := range recs {
		if err := c.SiteCheck(rec); err != nil {
			t.Fatal(err)
		}
	}
	// A resume re-runs the pass: same keys must replace, not accumulate.
	if err := c.SiteCheck(crawler.SiteCheckRecord{
		Kind: "robots", Subject: "https://ex.com/robots.txt", Report: []byte(`{"found":true}`),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := c.SiteChecks()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("site checks = %+v, want 2 rows (replace, not accumulate)", got)
	}
	byKind := map[string]string{}
	for _, sc := range got {
		byKind[sc.Kind] = string(sc.Report)
	}
	if byKind["robots"] != `{"found":true}` {
		t.Errorf("robots report = %s, want the replaced one", byKind["robots"])
	}
	if byKind["sitemap"] != `{"missing":true}` {
		t.Errorf("sitemap report = %s", byKind["sitemap"])
	}
}

func TestSiteChecksEmptyIsNonNil(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.SiteChecks()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("SiteChecks() = %v, want empty non-nil", got)
	}
}
