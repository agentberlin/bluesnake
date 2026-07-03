package crawler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

func TestSiteChecksApplyGating(t *testing.T) {
	cases := []struct {
		name   string
		seed   string
		mutate func(*config.Config)
		want   bool
	}{
		{"root spider crawl", "https://ex.com/", nil, true},
		{"root without trailing slash", "https://ex.com", nil, true},
		{"path seed", "https://ex.com/blog/", nil, false},
		{"query on root", "https://ex.com/?page=1", nil, false},
		{"include-narrowed", "https://ex.com/", func(c *config.Config) {
			c.Scope.Include = []string{"/blog/"}
		}, false},
		{"list mode", "https://ex.com/", func(c *config.Config) { c.Mode = "list" }, false},
		{"never overrides root crawl", "https://ex.com/", func(c *config.Config) {
			c.SiteChecks.Enabled = "never"
		}, false},
		{"always overrides path seed", "https://ex.com/blog/", func(c *config.Config) {
			c.SiteChecks.Enabled = "always"
		}, true},
		{"always overrides list mode", "https://ex.com/", func(c *config.Config) {
			c.Mode = "list"
			c.SiteChecks.Enabled = "always"
		}, true},
		{"all checks disabled", "https://ex.com/", func(c *config.Config) {
			c.SiteChecks.Robots = false
			c.SiteChecks.Sitemap = false
			c.SiteChecks.AIBots.Check = false
		}, false},
		{"low max_urls still applies", "https://ex.com/", func(c *config.Config) {
			c.Limits.MaxURLs = 10
		}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			c, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.siteChecksApply(tt.seed); got != tt.want {
				t.Errorf("siteChecksApply(%q) = %v, want %v", tt.seed, got, tt.want)
			}
		})
	}
}

// captureSink records site-check reports (the store's sink extension shape).
type captureSink struct {
	mu   sync.Mutex
	recs []SiteCheckRecord
}

func (s *captureSink) Page(*PageRecord) error          { return nil }
func (s *captureSink) FrontierAdd(frontier.Item) error { return nil }
func (s *captureSink) FrontierDone(string) error       { return nil }
func (s *captureSink) SiteCheck(rec SiteCheckRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	return nil
}

func crawlWithSink(t *testing.T, s *site, sink Sink, mutate func(*config.Config)) *Result {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSiteCheckPassStoresReports(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  "<html><body>" + link("/a") + "</body></html>",
		"/a": "<html></html>",
	})
	sink := &captureSink{}
	crawlWithSink(t, s, sink, nil)

	// Reports reach tests the way they reach production consumers: through
	// the sink (the Result carries only counters).
	kinds := map[string]SiteCheckRecord{}
	for _, rec := range sink.recs {
		kinds[rec.Kind] = rec
	}
	if len(kinds) != 3 {
		t.Fatalf("sink site checks = %+v, want robots + sitemap + ai_bots", sink.recs)
	}

	// The fixture has no robots.txt and no sitemap: the stored reports must
	// re-derive exactly those findings.
	var robotsRep sitecheck.RobotsReport
	if err := json.Unmarshal(kinds[sitecheck.KindRobots].Report, &robotsRep); err != nil {
		t.Fatal(err)
	}
	if robotsRep.Found {
		t.Error("robots.txt reported found on a fixture without one")
	}
	ids := map[string]bool{}
	for _, rec := range sink.recs {
		for _, f := range sitecheck.DecodeFindings(rec.Kind, rec.Report) {
			ids[f.IssueID] = true
		}
	}
	if !ids["robots_txt_missing"] || !ids["sitemap_missing"] {
		t.Errorf("derived findings = %v, want robots_txt_missing + sitemap_missing", ids)
	}
}

func TestSiteCheckPassSkippedWhenGatedOff(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<html></html>"})
	sink := &captureSink{}
	crawlWithSink(t, s, sink, func(c *config.Config) {
		c.SiteChecks.Enabled = "never"
	})
	if len(sink.recs) != 0 {
		t.Errorf("site checks ran with enabled=never: %+v", sink.recs)
	}
}

// SiteCheckProgress feeds the live progress surfaces: "" when the pass is not
// part of the crawl, "done" with report + finding counts after Run returns
// (Run's completion barrier waits for the pass).
func TestSiteCheckProgress(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<html></html>"})
	c, err := New(config.Default(), WithSink(&captureSink{}))
	if err != nil {
		t.Fatal(err)
	}
	if state, _, _ := c.SiteCheckProgress(); state != "" {
		t.Errorf("state before Run = %q, want empty", state)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}
	state, checks, findings := c.SiteCheckProgress()
	// bare fixture: robots + sitemap + ai_bots reports, robots_txt_missing +
	// sitemap_missing findings at minimum
	if state != "done" || checks != 3 || findings < 2 {
		t.Errorf("progress = %q %d %d, want done/3/≥2", state, checks, findings)
	}

	gated, err := New(config.Default(), WithSink(&captureSink{}))
	if err != nil {
		t.Fatal(err)
	}
	gated.cfg.SiteChecks.Enabled = "never"
	if _, err := gated.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := gated.SiteCheckProgress(); state != "" {
		t.Errorf("gated-off state = %q, want empty", state)
	}
}

// The pass's AI-bot audit reuses the crawl's single robots.txt fetch and
// live-probes with each bot's User-Agent; a UA-keyed edge block becomes an
// ai_bot_blocked_live finding.
func TestSiteCheckPassAIBots(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<html></html>"})
	s.pages["/robots.txt"] = "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\nSitemap: /sm.xml\n"
	s.pages["/sm.xml"] = `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>` +
		s.server.URL + `/</loc></url></urlset>`
	blockClaude(s)

	sink := &captureSink{}
	crawlWithSink(t, s, sink, nil)
	ids := map[string]bool{}
	for _, rec := range sink.recs {
		for _, f := range sitecheck.DecodeFindings(rec.Kind, rec.Report) {
			ids[f.IssueID] = true
		}
	}
	if !ids["ai_bot_blocked_robots"] || !ids["ai_bot_blocked_live"] {
		t.Errorf("derived findings = %v, want the robots and edge blocks", ids)
	}
	if got := s.hitCount("/robots.txt"); got != 1 {
		t.Errorf("robots.txt fetched %d times, want 1 (gating + audits share it)", got)
	}
}

// blockClaude wraps the fixture server: requests whose UA contains ClaudeBot
// get a 403 (a UA-keyed WAF).
func blockClaude(s *site) {
	inner := s.server.Config.Handler
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("User-Agent"), "ClaudeBot") && r.URL.Path != "/robots.txt" {
			w.WriteHeader(403)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestSiteCheckPassHealthySite(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<html></html>"})
	s.pages["/robots.txt"] = "User-agent: *\nDisallow:\nSitemap: " + s.server.URL + "/sitemap.xml\n"
	s.pages["/sitemap.xml"] = `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>` +
		s.server.URL + `/</loc></url></urlset>`

	sink := &captureSink{}
	crawlWithSink(t, s, sink, nil)
	for _, rec := range sink.recs {
		if f := sitecheck.DecodeFindings(rec.Kind, rec.Report); f != nil {
			t.Errorf("healthy site: %s findings = %+v, want none", rec.Kind, f)
		}
	}
}
