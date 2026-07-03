package sitecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// RunTool dispatches a registry tool by canonical name with a JSON-encoded
// args object — the single generic entry point behind MCP run_tool and the
// desktop Tools hub (the CLI keeps typed flags per subcommand). Args are
// decoded strictly: unknown keys are errors naming the offender, so a
// mis-spelled option self-corrects against the registry.
func (c *Checker) RunTool(ctx context.Context, name, target string, args json.RawMessage) (Reporter, error) {
	if _, ok := LookupTool(name); !ok {
		var names []string
		for _, t := range Tools() {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("unknown tool %q (available: %s)", name, strings.Join(names, ", "))
	}
	switch name {
	case "robots":
		var opts struct {
			URLs      []string `json:"urls"`
			UserAgent string   `json:"user_agent"`
			RobotsTxt string   `json:"robots_txt"`
		}
		if err := decodeToolArgs(args, &opts); err != nil {
			return nil, err
		}
		ro := RobotsOptions{TestURLs: opts.URLs, TestUserAgent: opts.UserAgent}
		if opts.RobotsTxt != "" {
			return c.EvaluateRobotsFile([]byte(opts.RobotsTxt), ro), nil
		}
		return c.Robots(ctx, target, ro)
	case "sitemap":
		var opts struct {
			CheckEntries int `json:"check_entries"`
		}
		if err := decodeToolArgs(args, &opts); err != nil {
			return nil, err
		}
		return c.Sitemaps(ctx, target, SitemapOptions{CheckEntries: opts.CheckEntries})
	case "aibots":
		opts := struct {
			Live *bool    `json:"live"`
			Skip []string `json:"skip"`
		}{}
		if err := decodeToolArgs(args, &opts); err != nil {
			return nil, err
		}
		return c.AIBots(ctx, target, AIBotOptions{Live: opts.Live == nil || *opts.Live, Skip: opts.Skip})
	case "render":
		if err := decodeToolArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		return c.RenderDiff(ctx, target)
	case "llms":
		if err := decodeToolArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		return c.LlmsTxt(ctx, target)
	case "structured":
		if err := decodeToolArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		return c.Structured(ctx, target)
	default: // "serp" — the registry gate above keeps this exhaustive
		var opts struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := decodeToolArgs(args, &opts); err != nil {
			return nil, err
		}
		return c.Serp(ctx, SerpOptions{Title: opts.Title, Description: opts.Description, URL: target})
	}
}

// decodeToolArgs strictly decodes a tool's args object: unknown keys are
// errors naming the offender, so a mis-spelled option can self-correct.
func decodeToolArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("invalid args: %v (see list_tools for this tool's options)", err)
	}
	return nil
}
