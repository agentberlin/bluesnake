package sitecheck

import (
	"context"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/robots"
)

// Bot is one AI crawler in the registry: how it appears in robots.txt, how it
// fetches (when it does), and its operator-documented behaviour. The registry
// is data, not code — config can extend or skip entries.
type Bot struct {
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Purpose  string `json:"purpose"` // training | search | user_action
	// RobotsToken is the user-agent token matched in robots.txt.
	RobotsToken string `json:"robots_token"`
	// UserAgent is the full request User-Agent for live probes. Empty means
	// the entry is a robots.txt control token only (Google-Extended,
	// Applebot-Extended) — no fetcher sends it, so it is never probed.
	UserAgent string `json:"user_agent,omitempty"`
	// RespectsRobots is the operator-documented (or, where noted in the
	// registry, widely-reported) behaviour. A robots.txt block against a bot
	// that does not honour robots.txt is ineffective — only an edge block
	// works; surfaces pair the two verdicts using this flag.
	RespectsRobots bool   `json:"respects_robots"`
	DocURL         string `json:"doc_url,omitempty"`
}

// TokenOnly reports whether the entry is a robots.txt control token with no
// fetcher behind it.
func (b Bot) TokenOnly() bool { return b.UserAgent == "" }

// DefaultBots is the embedded AI-crawler registry. UA strings and behaviour
// follow each operator's published bot documentation (rosters churn — entries
// carry their doc URL so staleness is checkable). Config extends via
// site_checks.ai_bots.bots / skips via .skip.
func DefaultBots() []Bot {
	return []Bot{
		{Name: "GPTBot", Operator: "OpenAI", Purpose: "training", RobotsToken: "GPTBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot",
			RespectsRobots: true, DocURL: "https://platform.openai.com/docs/bots"},
		{Name: "OAI-SearchBot", Operator: "OpenAI", Purpose: "search", RobotsToken: "OAI-SearchBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot",
			RespectsRobots: true, DocURL: "https://platform.openai.com/docs/bots"},
		{Name: "ChatGPT-User", Operator: "OpenAI", Purpose: "user_action", RobotsToken: "ChatGPT-User",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot",
			RespectsRobots: true, DocURL: "https://platform.openai.com/docs/bots"},
		{Name: "ClaudeBot", Operator: "Anthropic", Purpose: "training", RobotsToken: "ClaudeBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.anthropic.com/en/articles/8896518"},
		{Name: "Claude-User", Operator: "Anthropic", Purpose: "user_action", RobotsToken: "Claude-User",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-User/1.0; +Claude-User@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.anthropic.com/en/articles/8896518"},
		{Name: "Claude-SearchBot", Operator: "Anthropic", Purpose: "search", RobotsToken: "Claude-SearchBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-SearchBot/1.0; +Claude-SearchBot@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.anthropic.com/en/articles/8896518"},
		{Name: "PerplexityBot", Operator: "Perplexity", Purpose: "search", RobotsToken: "PerplexityBot",
			UserAgent:      "Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)",
			RespectsRobots: true, DocURL: "https://docs.perplexity.ai/guides/bots"},
		{Name: "Perplexity-User", Operator: "Perplexity", Purpose: "user_action", RobotsToken: "Perplexity-User",
			UserAgent: "Mozilla/5.0 (compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user)",
			// Perplexity documents that user-initiated fetches generally
			// ignore robots.txt — a robots block alone cannot stop it.
			RespectsRobots: false, DocURL: "https://docs.perplexity.ai/guides/bots"},
		{Name: "Google-Extended", Operator: "Google", Purpose: "training", RobotsToken: "Google-Extended",
			// Control token honoured by Google's ordinary crawlers — no
			// fetcher sends this UA, so it is robots-evaluated only.
			RespectsRobots: true, DocURL: "https://developers.google.com/search/docs/crawling-indexing/overview-google-crawlers"},
		{Name: "Applebot-Extended", Operator: "Apple", Purpose: "training", RobotsToken: "Applebot-Extended",
			RespectsRobots: true, DocURL: "https://support.apple.com/en-us/119829"},
		{Name: "CCBot", Operator: "Common Crawl", Purpose: "training", RobotsToken: "CCBot",
			UserAgent:      "CCBot/2.0 (https://commoncrawl.org/faq/)",
			RespectsRobots: true, DocURL: "https://commoncrawl.org/ccbot"},
		{Name: "meta-externalagent", Operator: "Meta", Purpose: "training", RobotsToken: "meta-externalagent",
			UserAgent:      "meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)",
			RespectsRobots: true, DocURL: "https://developers.facebook.com/docs/sharing/webmasters/web-crawlers"},
		{Name: "meta-externalfetcher", Operator: "Meta", Purpose: "user_action", RobotsToken: "meta-externalfetcher",
			UserAgent: "meta-externalfetcher/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)",
			// Meta documents this user-initiated fetcher as it may bypass
			// robots.txt rules.
			RespectsRobots: false, DocURL: "https://developers.facebook.com/docs/sharing/webmasters/web-crawlers"},
		{Name: "Bytespider", Operator: "ByteDance", Purpose: "training", RobotsToken: "Bytespider",
			UserAgent: "Mozilla/5.0 (Linux; Android 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; Bytespider; spider-feedback@bytedance.com)",
			// No operator documentation; widely reported to ignore robots.txt.
			RespectsRobots: false},
		{Name: "Amazonbot", Operator: "Amazon", Purpose: "training", RobotsToken: "Amazonbot",
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_10_1) AppleWebKit/600.2.5 (KHTML, like Gecko) Version/8.0.2 Safari/600.2.5 (Amazonbot/0.1; +https://developer.amazon.com/support/amazonbot)",
			RespectsRobots: true, DocURL: "https://developer.amazon.com/support/amazonbot"},
		{Name: "DuckAssistBot", Operator: "DuckDuckGo", Purpose: "search", RobotsToken: "DuckAssistBot",
			UserAgent:      "DuckAssistBot/1.1; (+http://duckduckgo.com/duckassistbot.html)",
			RespectsRobots: true, DocURL: "https://duckduckgo.com/duckassistbot"},
		{Name: "MistralAI-User", Operator: "Mistral", Purpose: "user_action", RobotsToken: "MistralAI-User",
			UserAgent:      "Mozilla/5.0 (compatible; MistralAI-User/1.0; +https://docs.mistral.ai/robots)",
			RespectsRobots: true, DocURL: "https://docs.mistral.ai/robots"},
	}
}

// AIBotCaveat travels inside every report so no surface can forget it.
const AIBotCaveat = "Live probes test User-Agent-based blocking only: a WAF that verifies " +
	"published crawler IP ranges may treat the real bot differently in either direction."

// AIBotOptions customizes an AI-bot access check.
type AIBotOptions struct {
	Live  bool     // probe the site root once per fetcher bot (plus one control fetch)
	Extra []Bot    // registry extensions/overrides (matched by Name)
	Skip  []string // registry names to exclude
}

// AIBotOptionsFromConfig maps the site_checks.ai_bots config block onto check
// options — shared by the crawl pass and any surface honouring config.
// Custom bots default to respecting robots.txt and to their name as the
// robots token.
func AIBotOptionsFromConfig(cfg config.AIBotsChecksConfig) AIBotOptions {
	opts := AIBotOptions{Live: cfg.LiveProbe, Skip: cfg.Skip}
	for _, b := range cfg.Bots {
		token := b.RobotsToken
		if token == "" {
			token = b.Name
		}
		opts.Extra = append(opts.Extra, Bot{
			Name: b.Name, Operator: b.Operator, Purpose: b.Purpose,
			RobotsToken: token, UserAgent: b.UserAgent, RespectsRobots: true,
		})
	}
	return opts
}

// AIBotResult is one bot's verdicts.
type AIBotResult struct {
	Bot
	RobotsAllowed bool   `json:"robots_allowed"`
	RobotsLine    int    `json:"robots_line,omitempty"`
	RobotsRule    string `json:"robots_rule,omitempty"`
	Probed        bool   `json:"probed,omitempty"`
	LiveStatus    int    `json:"live_status,omitempty"`
	LiveError     string `json:"live_error,omitempty"`
	BlockedLive   bool   `json:"blocked_live,omitempty"`
}

// AIBotsReport is the AI-bot access audit for a site.
type AIBotsReport struct {
	Site          string        `json:"site"`
	URL           string        `json:"url"` // the probed URL (site root)
	RobotsFound   bool          `json:"robots_found"`
	Live          bool          `json:"live"`
	ControlStatus int           `json:"control_status,omitempty"` // site root with the configured UA
	ControlError  string        `json:"control_error,omitempty"`
	Bots          []AIBotResult `json:"bots"`
	Caveat        string        `json:"caveat"`
}

// AIBots audits which AI crawlers can access a site: robots.txt verdicts for
// every registry bot (no extra requests) plus, with opts.Live, one fetch of
// the site root per fetcher bot compared against a control fetch with the
// configured User-Agent — catching WAF/CDN-level blocks robots.txt testing
// can never see.
func (c *Checker) AIBots(ctx context.Context, site string, opts AIBotOptions) (*AIBotsReport, error) {
	root, err := siteRoot(site)
	if err != nil {
		return nil, err
	}
	rf := FetchRobots(ctx, c.client, root)
	var f *robots.File
	if rf.Found() {
		f = robots.Parse(rf.Body)
	} else {
		f = robots.Parse(nil)
	}
	return c.EvaluateAIBots(ctx, root, f, rf.Found(), opts), nil
}

// EvaluateAIBots runs the audit over an already-parsed robots.txt (the crawl
// pass reuses its single fetch; custom robots overrides pass their file).
// Live probes still fetch the site root.
func (c *Checker) EvaluateAIBots(ctx context.Context, root string, f *robots.File, robotsFound bool, opts AIBotOptions) *AIBotsReport {
	rep := &AIBotsReport{
		Site: root, URL: root + "/",
		RobotsFound: robotsFound,
		Live:        opts.Live,
		Caveat:      AIBotCaveat,
	}
	if opts.Live {
		res := c.client.Fetch(ctx, rep.URL)
		rep.ControlStatus, rep.ControlError = res.StatusCode, res.FetchError
	}
	for _, bot := range assembleBots(opts) {
		r := AIBotResult{Bot: bot}
		v := f.Verdict(bot.RobotsToken, rep.URL)
		r.RobotsAllowed = v.Allowed
		if v.Rule != nil {
			r.RobotsLine, r.RobotsRule = v.Rule.Line, v.Rule.Raw
		}
		if opts.Live && !bot.TokenOnly() {
			r.Probed = true
			res := c.client.FetchWith(ctx, rep.URL, fetch.Override{UserAgent: bot.UserAgent})
			r.LiveStatus, r.LiveError = res.StatusCode, res.FetchError
			r.BlockedLive = blockedLive(r, rep)
		}
		rep.Bots = append(rep.Bots, r)
	}
	return rep
}

// blockedLive classifies an edge-level block: the control fetch succeeded
// (2xx/3xx) while the bot's probe did not. Attribution needs a healthy
// control — when the site fails for everyone, nothing is bot-specific.
func blockedLive(r AIBotResult, rep *AIBotsReport) bool {
	controlOK := rep.ControlError == "" && rep.ControlStatus >= 200 && rep.ControlStatus < 400
	if !controlOK {
		return false
	}
	probeOK := r.LiveError == "" && r.LiveStatus >= 200 && r.LiveStatus < 400
	return !probeOK
}

// assembleBots merges the registry with config extensions and skips.
// An Extra entry whose Name matches a registry bot replaces it.
func assembleBots(opts AIBotOptions) []Bot {
	skip := map[string]bool{}
	for _, s := range opts.Skip {
		skip[s] = true
	}
	override := map[string]Bot{}
	for _, b := range opts.Extra {
		override[b.Name] = b
	}
	var out []Bot
	seen := map[string]bool{}
	for _, b := range DefaultBots() {
		if skip[b.Name] {
			continue
		}
		if o, ok := override[b.Name]; ok {
			b = o
		}
		seen[b.Name] = true
		out = append(out, b)
	}
	for _, b := range opts.Extra {
		if !seen[b.Name] && !skip[b.Name] {
			out = append(out, b)
		}
	}
	return out
}

// Findings derives the issue occurrences (DESIGN.md §5.10), attached
// to the probed URL. Severity is Warning throughout: blocking AI bots can be
// deliberate policy — the check's job is visibility, especially for edge
// blocks the site owner may not know their CDN applies.
func (r *AIBotsReport) Findings() []Finding {
	var out []Finding
	add := func(id, detail string) {
		out = append(out, Finding{IssueID: id, URL: r.URL, Detail: detail})
	}
	crawlers, blocked := 0, 0
	for _, b := range r.Bots {
		if !b.RobotsAllowed {
			note := ""
			if !b.RespectsRobots {
				note = "; note: this bot does not honour robots.txt — only an edge block is effective"
			}
			add("ai_bot_blocked_robots", fmt.Sprintf("%s (%s): line %d: %s%s",
				b.Name, b.Operator, b.RobotsLine, b.RobotsRule, note))
		}
		// The all-blocked headline counts site-wide crawlers (training +
		// search); user-action fetchers retrieve single pages on demand and
		// say nothing about crawl posture.
		if b.Purpose == "training" || b.Purpose == "search" {
			crawlers++
			if !b.RobotsAllowed {
				blocked++
			}
		}
		if b.BlockedLive {
			detail := fmt.Sprintf("%s (%s): status %d with the bot User-Agent vs %d for the control fetch",
				b.Name, b.Operator, b.LiveStatus, r.ControlStatus)
			if b.LiveError != "" {
				detail = fmt.Sprintf("%s (%s): %s with the bot User-Agent (control fetch: %d)",
					b.Name, b.Operator, b.LiveError, r.ControlStatus)
			}
			add("ai_bot_blocked_live", detail)
		}
	}
	if crawlers > 0 && blocked == crawlers {
		add("ai_bots_all_blocked_robots", fmt.Sprintf("all %d AI training/search crawlers in the registry are disallowed", crawlers))
	}
	return out
}
