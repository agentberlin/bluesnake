package sitecheck

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHealthRobots(t *testing.T) {
	rep := &RobotsReport{URL: "https://ex.com/robots.txt", Status: 200, Found: true,
		SizeBytes: 1536, Rules: 4, Sitemaps: []string{"https://ex.com/s.xml"}}
	h, ok := Health(KindRobots, mustJSON(t, rep))
	if !ok || h.Label != "robots.txt" {
		t.Fatalf("h = %+v ok = %v", h, ok)
	}
	for _, want := range []string{"HTTP 200", "1.5 KB", "4 rules", "1 sitemap directive"} {
		if !strings.Contains(h.Summary, want) {
			t.Errorf("summary %q missing %q", h.Summary, want)
		}
	}
	if len(h.Findings) != 0 {
		t.Errorf("healthy file findings = %v", h.Findings)
	}

	missing := &RobotsReport{URL: "https://ex.com/robots.txt", Status: 404}
	h, _ = Health(KindRobots, mustJSON(t, missing))
	if h.Summary != "missing (HTTP 404)" || len(h.Findings) == 0 {
		t.Errorf("missing file: summary %q findings %v", h.Summary, h.Findings)
	}
}

func TestHealthSitemap(t *testing.T) {
	rep := &SitemapReport{Site: "https://ex.com", Files: []SitemapFile{
		{URL: "https://ex.com/sitemap.xml", Status: 200, Kind: "urlset", Entries: 4812},
		{URL: "https://ex.com/broken.xml", Status: 404, Source: "robots"},
	}}
	h, ok := Health(KindSitemap, mustJSON(t, rep))
	if !ok || !strings.Contains(h.Summary, "1 sitemap · 4812 URLs") {
		t.Errorf("summary = %q ok = %v", h.Summary, ok)
	}
	if len(h.Findings) == 0 {
		t.Error("the 404 file must surface as a finding")
	}

	h, _ = Health(KindSitemap, mustJSON(t, &SitemapReport{Site: "https://ex.com", Missing: true}))
	if h.Summary != "none found" {
		t.Errorf("summary = %q", h.Summary)
	}
}

func TestHealthAIBots(t *testing.T) {
	rep := &AIBotsReport{Site: "https://ex.com", URL: "https://ex.com/", Live: true, RobotsFound: true, Bots: []AIBotResult{
		{Bot: Bot{Name: "GPTBot"}, RobotsAllowed: true},
		{Bot: Bot{Name: "ClaudeBot"}, RobotsAllowed: false},
		{Bot: Bot{Name: "PerplexityBot"}, RobotsAllowed: true, Probed: true, BlockedLive: true, LiveStatus: 403},
	}}
	h, ok := Health(KindAIBots, mustJSON(t, rep))
	if !ok || !strings.Contains(h.Summary, "1/3 crawlers allowed") || !strings.Contains(h.Summary, "live-probed") {
		t.Errorf("summary = %q ok = %v", h.Summary, ok)
	}
}

func TestHealthRender(t *testing.T) {
	dependent := &RenderDiffReport{URL: "https://ex.com/", Rendered: true, RawWordCount: 40, RenderedWordCount: 900}
	h, ok := Health(KindRenderDiff, mustJSON(t, dependent))
	if !ok || !strings.Contains(h.Summary, "needs JavaScript") {
		t.Errorf("summary = %q ok = %v", h.Summary, ok)
	}
	fine := &RenderDiffReport{URL: "https://ex.com/", Rendered: true, RawWordCount: 900, RenderedWordCount: 910}
	if h, _ = Health(KindRenderDiff, mustJSON(t, fine)); h.Summary != "content visible without JavaScript" {
		t.Errorf("summary = %q", h.Summary)
	}
}

func TestHealthUnknownKindDegrades(t *testing.T) {
	if _, ok := Health("future_check", []byte(`{}`)); ok {
		t.Error("unknown kind must degrade to no entry")
	}
	if _, ok := Health(KindRobots, []byte(`not json`)); ok {
		t.Error("undecodable report must degrade to no entry")
	}
}

func TestLlmsHealth(t *testing.T) {
	rep := &LlmsReport{Files: []LlmsFile{
		{URL: "https://ex.com/llms.txt", Kind: "llms_txt", Status: 200, Found: true, Title: "Ex"},
		{URL: "https://ex.com/llms-full.txt", Kind: "llms_full_txt", Status: 404},
	}}
	h := LlmsHealth(rep)
	if h.Label != "llms.txt" || h.Summary != "llms.txt present" {
		t.Errorf("h = %+v", h)
	}
	hasFull := false
	for _, f := range h.Findings {
		if f.IssueID == "llms_full_txt_missing" {
			hasFull = true
		}
	}
	if !hasFull {
		t.Errorf("findings = %v, want llms_full_txt_missing", h.Findings)
	}
}
