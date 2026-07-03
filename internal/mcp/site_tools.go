package mcp

import (
	"context"
	"encoding/json"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// siteTools is the MCP surface of the standalone site testers
// (DESIGN.md §5.10): exactly two functions regardless of how many
// testers exist — list_tools (the registry) and run_tool (dispatch by name).
// Runs are stateless and never persisted; the same checks run automatically
// during full-domain crawls via site_checks and surface through
// issue_summary/query.
func (s *Server) siteTools() []Tool {
	return []Tool{
		{
			Name: "list_tools",
			Description: "List bluesnake's standalone site testers (robots.txt, XML sitemaps, AI-bot access, JS render diff, llms.txt, " +
				"structured data, SERP snippet preview) with their arguments. Run one with run_tool. " +
				"Results are computed live against the target and never persisted.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				return jsonText(sitecheck.Tools())
			},
		},
		{
			Name: "run_tool",
			Description: "Run one standalone site tester against a live site — no crawl required. " +
				"Returns the tool's report plus derived findings (the same issue IDs a crawl would store). " +
				"Tools: robots, sitemap, aibots, render, llms, structured, serp — call list_tools for each tool's arguments.",
			InputSchema: schema(map[string]any{
				"tool":   strProp("Canonical tool name: robots | sitemap | aibots | render | llms | structured | serp."),
				"target": strProp("Site root (render/structured/serp take the exact page URL; empty is allowed where a tool needs no fetch)."),
				"args":   map[string]any{"type": "object", "description": "Tool-specific options — see list_tools."},
			}, "tool", "target"),
			handler: s.runSiteTool,
		},
	}
}

func (s *Server) runSiteTool(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Tool   string          `json:"tool"`
		Target string          `json:"target"`
		Args   json.RawMessage `json:"args"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return "", err
	}
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		return "", err
	}
	// The backend's process limiter makes tool-run fetches/renders count
	// against the same ceilings as the crawls running beside them.
	chk := sitecheck.New(cfg, client, sitecheck.WithLimiter(s.backend.ProcessLimiter()))
	rep, err := chk.RunTool(ctx, a.Tool, a.Target, a.Args)
	if err != nil {
		return "", err
	}
	findings := rep.Findings()
	if findings == nil {
		findings = []sitecheck.Finding{}
	}
	return jsonText(struct {
		Report   any                 `json:"report"`
		Findings []sitecheck.Finding `json:"findings"`
	}{rep, findings})
}
