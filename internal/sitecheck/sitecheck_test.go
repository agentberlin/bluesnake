package sitecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSiteRoot(t *testing.T) {
	cases := []struct {
		in, want string
		err      bool
	}{
		{in: "example.com", want: "https://example.com"},
		{in: "  example.com:8080  ", want: "https://example.com:8080"},
		{in: "http://example.com/deep/page?q=1", want: "http://example.com"},
		{in: "https://www.example.com/", want: "https://www.example.com"},
		{in: "", err: true},
		{in: "ftp://example.com", err: true},
		{in: "https://", err: true},
		{in: "http://%zz", err: true},
	}
	for _, c := range cases {
		got, err := siteRoot(c.in)
		if c.err != (err != nil) || got != c.want {
			t.Errorf("siteRoot(%q) = %q, %v; want %q, err=%v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestRegistry(t *testing.T) {
	tools := Tools()
	if len(tools) == 0 {
		t.Fatal("empty registry")
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool.Name == "" || tool.Summary == "" {
			t.Errorf("registry entry %+v needs name and summary", tool)
		}
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
	}
	if _, ok := LookupTool("robots"); !ok {
		t.Error("LookupTool(robots) not found")
	}
	if _, ok := LookupTool("no-such-tool"); ok {
		t.Error("LookupTool(no-such-tool) unexpectedly found")
	}
}

func TestDecodeFindingsSitemap(t *testing.T) {
	rep := &SitemapReport{Missing: true, Site: "https://ex.com"}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	got := DecodeFindings(KindSitemap, data)
	if len(got) != 1 || got[0].IssueID != "sitemap_missing" || got[0].URL != "https://ex.com/" {
		t.Fatalf("decoded = %+v", got)
	}
}

func TestSitemapsBadTargets(t *testing.T) {
	c := newChecker(t)
	if _, err := c.Sitemaps(context.Background(), "", SitemapOptions{}); err == nil {
		t.Error("empty target with no known sitemaps must error")
	}
	if _, err := c.Sitemaps(context.Background(), "ftp://x", SitemapOptions{}); err == nil {
		t.Error("bad scheme must error")
	}
	if _, err := c.Robots(context.Background(), "", RobotsOptions{}); err == nil {
		t.Error("empty robots target must error")
	}
}

// The expansion bound is recorded, never silent: children beyond
// maxSitemapFiles land in Skipped.
func TestSitemapExpansionBound(t *testing.T) {
	var children strings.Builder
	for i := 0; i < maxSitemapFiles+10; i++ {
		fmt.Fprintf(&children, "<sitemap><loc>BASE/c%d.xml</loc></sitemap>", i)
	}
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{
			"/index.xml": fmt.Sprintf(`<?xml version="1.0"?><sitemapindex xmlns=%q>%s</sitemapindex>`, smNS, children.String()),
		},
	})
	for i := 0; i < maxSitemapFiles+10; i++ {
		s.bodies[fmt.Sprintf("/c%d.xml", i)] = urlset(entry("BASE/a"))
	}
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/index.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != maxSitemapFiles {
		t.Errorf("files = %d, want the %d bound", len(rep.Files), maxSitemapFiles)
	}
	if len(rep.Skipped) != 11 { // 110 children + 1 index - 100 bound
		t.Errorf("skipped = %d, want 11", len(rep.Skipped))
	}
}

func TestSitemapInvalidEntryURLs(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": urlset(
			entry("not a url"),
			entry("ftp://ex.com/x"),
			entry("BASE/fine"),
		)},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f := rep.Files[0]; f.InvalidURLs != 2 || len(f.InvalidURLEx) != 2 {
		t.Errorf("file = %+v, want 2 invalid entry URLs", f)
	}
}

func TestSitemapCorruptGzip(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{0x1f, 0x8b, 0xff, 0x00, 0x01}) // gzip magic, garbage stream
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.URL+"/sm.xml.gz", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Files[0]
	if !f.Gzip || f.Kind != "invalid" || !strings.HasPrefix(f.XMLError, "gzip:") {
		t.Errorf("file = %+v, want a gzip decode failure", f)
	}
	if !hasFinding(rep, "sitemap_invalid_xml") {
		t.Errorf("findings = %v, want sitemap_invalid_xml", findingIDs(rep))
	}
}

// Relative Sitemap: directives in robots.txt resolve against the robots URL.
func TestRobotsSitemapDirectiveRelativeResolution(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		robots: "Sitemap: /rel.xml\n",
		bodies: map[string]string{"/rel.xml": urlset(entry("BASE/a"))},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 || rep.Files[0].URL != s.server.URL+"/rel.xml" {
		t.Fatalf("report = %+v, want the relative directive resolved", rep)
	}
}
