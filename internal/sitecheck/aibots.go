package sitecheck

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/robots"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// Bot is one AI crawler in the registry: how it appears in robots.txt, how it
// fetches (when it does), and its operator-documented behaviour. The registry
// is data, not code — config can extend or skip entries.
type Bot struct {
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Purpose  string `json:"purpose"` // training | search | user_action
	// RobotsToken is the user-agent token matched in robots.txt. Empty means
	// the operator publishes no token (Google-Agent): robots.txt cannot
	// address the bot, so it gets live probes and no robots verdict.
	RobotsToken string `json:"robots_token,omitempty"`
	// RobotsFallback lists, in order, the tokens whose robots.txt group the
	// bot follows when no group names it, before * — the operator's
	// documented fallback (Applebot follows Googlebot's group).
	RobotsFallback []string `json:"robots_fallback,omitempty"`
	// UserAgent is the full request User-Agent for live probes. Empty means
	// the entry is robots.txt-evaluated only and never probed: either a
	// control token no fetcher sends (Google-Extended, Applebot-Extended), or
	// a search engine's crawler that sites verify by reverse DNS (Googlebot,
	// Bingbot, Applebot) — a probe sending its UA from our IP is blocked as an
	// impostor by most WAFs, an edge block the real crawler never meets.
	UserAgent string `json:"user_agent,omitempty"`
	// RespectsRobots is the operator-documented (or, where noted in the
	// registry, widely-reported) behaviour. A robots.txt block against a bot
	// that does not honour robots.txt is ineffective — only an edge block
	// works; surfaces pair the two verdicts using this flag.
	RespectsRobots bool   `json:"respects_robots"`
	DocURL         string `json:"doc_url,omitempty"`
}

// TokenOnly reports whether the entry is evaluated against robots.txt only,
// never probed live (see UserAgent).
func (b Bot) TokenOnly() bool { return b.UserAgent == "" }

// probeChrome fills the Chrome/W.X.Y.Z placeholder some operators publish in
// their user agents. The version changes nothing a robots.txt group or a
// user-agent rule matches.
const probeChrome = "137.0.0.0"

// DefaultBots is the embedded AI-crawler registry. UA strings and behaviour
// follow each operator's published bot documentation (rosters churn — entries
// carry their doc URL so staleness is checkable). Config extends via
// site_checks.ai_bots.bots / skips via .skip.
//
// The search engines' own crawlers sit beside their training tokens because
// AI answers are built from what they index — Google's AI Overviews and AI
// Mode from Googlebot, Copilot from Bingbot, Siri and Apple Intelligence from
// Applebot — while Applebot-Extended only opts a site out of training. Without
// both, a report cannot tell a site that blocks training from one that blocks
// being found.
func DefaultBots() []Bot {
	return []Bot{
		{Name: "GPTBot", Operator: "OpenAI", Purpose: "training", RobotsToken: "GPTBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.4; +https://openai.com/gptbot",
			RespectsRobots: true, DocURL: "https://developers.openai.com/api/docs/bots"},
		{Name: "OAI-SearchBot", Operator: "OpenAI", Purpose: "search", RobotsToken: "OAI-SearchBot",
			UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36; compatible; OAI-SearchBot/1.4; +https://openai.com/searchbot",
			RespectsRobots: true, DocURL: "https://developers.openai.com/api/docs/bots"},
		{Name: "ChatGPT-User", Operator: "OpenAI", Purpose: "user_action", RobotsToken: "ChatGPT-User",
			UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot",
			// OpenAI documents that for user-initiated fetches "robots.txt
			// rules may not apply".
			RespectsRobots: false, DocURL: "https://developers.openai.com/api/docs/bots"},
		// Anthropic publishes the names and an IP list but no full user
		// agents, so these strings are not checked against a primary source.
		{Name: "ClaudeBot", Operator: "Anthropic", Purpose: "training", RobotsToken: "ClaudeBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.claude.com/en/articles/8896518"},
		{Name: "Claude-User", Operator: "Anthropic", Purpose: "user_action", RobotsToken: "Claude-User",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-User/1.0; +Claude-User@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.claude.com/en/articles/8896518"},
		{Name: "Claude-SearchBot", Operator: "Anthropic", Purpose: "search", RobotsToken: "Claude-SearchBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-SearchBot/1.0; +Claude-SearchBot@anthropic.com)",
			RespectsRobots: true, DocURL: "https://support.claude.com/en/articles/8896518"},
		{Name: "PerplexityBot", Operator: "Perplexity", Purpose: "search", RobotsToken: "PerplexityBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)",
			RespectsRobots: true, DocURL: "https://docs.perplexity.ai/docs/resources/perplexity-crawlers"},
		{Name: "Perplexity-User", Operator: "Perplexity", Purpose: "user_action", RobotsToken: "Perplexity-User",
			UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user)",
			// Perplexity documents that user-initiated fetches generally
			// ignore robots.txt — a robots block alone cannot stop it.
			RespectsRobots: false, DocURL: "https://docs.perplexity.ai/docs/resources/perplexity-crawlers"},
		{Name: "Googlebot", Operator: "Google", Purpose: "search", RobotsToken: "Googlebot",
			// Verified by reverse DNS, so token-only (see Bot.UserAgent).
			RespectsRobots: true, DocURL: "https://developers.google.com/search/docs/crawling-indexing/googlebot"},
		{Name: "Google-Extended", Operator: "Google", Purpose: "search", RobotsToken: "Google-Extended",
			// Control token honoured by Google's ordinary crawlers — no
			// fetcher sends this UA, so it is robots-evaluated only. It opts
			// a site out of Gemini training AND of grounding in the Gemini
			// apps and Vertex AI, so blocking it costs Gemini citations (AI
			// Overviews and AI Mode are unaffected). Purpose holds one value;
			// it is labelled search because the lost citations are the cost
			// a report reader must not miss.
			RespectsRobots: true, DocURL: "https://developers.google.com/crawling/docs/crawlers-fetchers/google-common-crawlers"},
		// Google's user-triggered fetchers publish a user agent and no robots
		// token — they "generally ignore robots.txt" — so they get live
		// probes and no robots verdict.
		{Name: "Google-Agent", Operator: "Google", Purpose: "user_action",
			UserAgent:      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko; compatible; Google-Agent; +https://developers.google.com/crawling/docs/crawlers-fetchers/google-agent) Chrome/" + probeChrome + " Safari/537.36",
			RespectsRobots: false, DocURL: "https://developers.google.com/crawling/docs/crawlers-fetchers/google-user-triggered-fetchers"},
		{Name: "Google-GeminiNotebook", Operator: "Google", Purpose: "user_action",
			// Replaced Google-NotebookLM.
			UserAgent:      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36 (compatible; Google-GeminiNotebook; +https://developers.google.com/crawling/docs/crawlers-fetchers/google-gemininotebook)",
			RespectsRobots: false, DocURL: "https://developers.google.com/crawling/docs/crawlers-fetchers/google-user-triggered-fetchers"},
		{Name: "Bingbot", Operator: "Microsoft", Purpose: "search", RobotsToken: "bingbot",
			RespectsRobots: true, DocURL: "https://www.bing.com/webmasters/help/which-crawlers-does-bing-use-8c184ec0"},
		{Name: "Applebot", Operator: "Apple", Purpose: "search", RobotsToken: "Applebot",
			// Apple: "If robots instructions don't mention Applebot but
			// mention Googlebot, the Apple robot will follow Googlebot
			// instructions."
			RobotsFallback: []string{"Googlebot"},
			RespectsRobots: true, DocURL: "https://support.apple.com/en-us/119829"},
		{Name: "Applebot-Extended", Operator: "Apple", Purpose: "training", RobotsToken: "Applebot-Extended",
			RespectsRobots: true, DocURL: "https://support.apple.com/en-us/119829"},
		{Name: "CCBot", Operator: "Common Crawl", Purpose: "training", RobotsToken: "CCBot",
			UserAgent:      "CCBot/2.0 (https://commoncrawl.org/faq/)",
			RespectsRobots: true, DocURL: "https://commoncrawl.org/ccbot"},
		{Name: "meta-externalagent", Operator: "Meta", Purpose: "training", RobotsToken: "meta-externalagent",
			UserAgent:      "meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)",
			RespectsRobots: true, DocURL: "https://developers.facebook.com/documentation/sharing/webmasters/web-crawlers"},
		{Name: "meta-externalfetcher", Operator: "Meta", Purpose: "user_action", RobotsToken: "meta-externalfetcher",
			UserAgent: "meta-externalfetcher/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)",
			// Meta documents this user-initiated fetcher as it may bypass
			// robots.txt rules.
			RespectsRobots: false, DocURL: "https://developers.facebook.com/documentation/sharing/webmasters/web-crawlers"},
		{Name: "Meta-WebIndexer", Operator: "Meta", Purpose: "search", RobotsToken: "meta-webindexer",
			// Indexes pages Meta AI cites.
			UserAgent:      "meta-webindexer/1.1",
			RespectsRobots: true, DocURL: "https://developers.facebook.com/documentation/sharing/webmasters/web-crawlers"},
		{Name: "Bytespider", Operator: "ByteDance", Purpose: "training", RobotsToken: "Bytespider",
			UserAgent: "Mozilla/5.0 (Linux; Android 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; Bytespider; spider-feedback@bytedance.com)",
			// No operator documentation; widely reported to ignore robots.txt.
			RespectsRobots: false},
		{Name: "Amazonbot", Operator: "Amazon", Purpose: "training", RobotsToken: "Amazonbot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Amazonbot/0.1) Chrome/" + probeChrome + " Safari/537.36",
			RespectsRobots: true, DocURL: "https://developer.amazon.com/amazonbot"},
		{Name: "Amzn-SearchBot", Operator: "Amazon", Purpose: "search", RobotsToken: "Amzn-SearchBot",
			UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Amzn-SearchBot/0.1) Chrome/" + probeChrome + " Safari/537.36",
			// Amazon: without an Amzn-SearchBot group it "will crawl in
			// accordance with the robots.txt directives given to other search
			// bots", naming none. We read that as the search engines'
			// crawlers in the registry, Googlebot's group first, then
			// Bingbot's.
			RobotsFallback: []string{"Googlebot", "bingbot"},
			RespectsRobots: true, DocURL: "https://developer.amazon.com/amazonbot"},
		{Name: "Amzn-User", Operator: "Amazon", Purpose: "user_action", RobotsToken: "Amzn-User",
			UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Amzn-User/0.1) Chrome/" + probeChrome + " Safari/537.36",
			// Alexa's live answers; Amazon documents it "may not follow all
			// robots.txt directives".
			RespectsRobots: false, DocURL: "https://developer.amazon.com/amazonbot"},
		{Name: "DuckAssistBot", Operator: "DuckDuckGo", Purpose: "search", RobotsToken: "DuckAssistBot",
			UserAgent:      "DuckAssistBot/1.2; (+http://duckduckgo.com/duckassistbot.html)",
			RespectsRobots: true, DocURL: "https://duckduckgo.com/duckduckgo-help-pages/results/duckassistbot"},
		{Name: "MistralAI-User", Operator: "Mistral", Purpose: "user_action", RobotsToken: "MistralAI-User",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; MistralAI-User/1.0; +https://docs.mistral.ai/robots)",
			RespectsRobots: true, DocURL: "https://docs.mistral.ai/robots"},
		{Name: "MistralAI-Index", Operator: "Mistral", Purpose: "search", RobotsToken: "MistralAI-Index",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; MistralAI-Index/1.0; +https://docs.mistral.ai/robots)",
			RespectsRobots: true, DocURL: "https://docs.mistral.ai/robots"},
		{Name: "MistralAI-Training", Operator: "Mistral", Purpose: "training", RobotsToken: "MistralAI-Training",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; MistralAI-Training/1.0; +https://docs.mistral.ai/robots)",
			RespectsRobots: true, DocURL: "https://docs.mistral.ai/robots"},
		{Name: "YouBot", Operator: "You.com", Purpose: "search", RobotsToken: "YouBot",
			UserAgent:      "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; YouBot/1.0; +https://docs.you.com/youbot; env:prod) Chrome/" + probeChrome + " Safari/537.36",
			RespectsRobots: true, DocURL: "https://you.com/docs/youbot"},
	}
}

// AIBotCaveat travels inside every report so no surface can forget it.
const AIBotCaveat = "Live probes test User-Agent-based blocking only: a WAF that verifies " +
	"published crawler IP ranges may treat the real bot differently in either direction."

// AIBotOptions customizes an AI-bot access check.
type AIBotOptions struct {
	Live  bool     // probe each audited URL once per fetcher bot (plus one control fetch)
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

// BotVerdict is one bot's verdicts for one URL.
type BotVerdict struct {
	// RobotsAllowed is robots.txt's verdict for the bot's token. A bot with
	// no robots token is one robots.txt cannot address: always allowed, no
	// matched rule.
	RobotsAllowed bool   `json:"robots_allowed"`
	RobotsLine    int    `json:"robots_line,omitempty"`
	RobotsRule    string `json:"robots_rule,omitempty"`
	// RobotsVia names the fallback token whose group decided (see
	// Bot.RobotsFallback); empty when the bot's own group or * did.
	RobotsVia   string `json:"robots_via,omitempty"`
	Probed      bool   `json:"probed,omitempty"`
	LiveStatus  int    `json:"live_status,omitempty"`
	LiveError   string `json:"live_error,omitempty"`
	BlockedLive bool   `json:"blocked_live,omitempty"`
}

// AIBotResult is one bot's verdicts beside its definition.
type AIBotResult struct {
	Bot
	BotVerdict
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
	rf := FetchRobots(ctx, capped{c}, root)
	return c.EvaluateAIBots(ctx, root, robots.Parse(rf.Body), rf.Found(), opts), nil
}

// EvaluateAIBots runs the audit over an already-parsed robots.txt (the crawl
// pass reuses its single fetch; custom robots overrides pass their file).
// Live probes still fetch the site root.
func (c *Checker) EvaluateAIBots(ctx context.Context, root string, f *robots.File, robotsFound bool, opts AIBotOptions) *AIBotsReport {
	rep := auditURL(ctx, capped{c}, f, root+"/", assembleBots(opts), opts.Live)
	rep.Site, rep.RobotsFound = root, robotsFound
	return rep
}

// auditURL is the per-URL audit the site check and the URL-list tool share:
// every bot's robots verdict for u and, when live, one control fetch with
// the configured User-Agent followed by one probe per fetcher bot.
func auditURL(ctx context.Context, client Fetcher, f *robots.File, u string, bots []Bot, live bool) *AIBotsReport {
	rep := &AIBotsReport{URL: u, Live: live, Caveat: AIBotCaveat}
	if live {
		res := client.Fetch(ctx, u)
		rep.ControlStatus, rep.ControlError = res.StatusCode, res.FetchError
	}
	for _, bot := range bots {
		r := AIBotResult{Bot: bot, BotVerdict: BotVerdict{RobotsAllowed: true}}
		if bot.RobotsToken != "" {
			v, via := f.VerdictFallback(bot.RobotsToken, bot.RobotsFallback, u)
			r.RobotsAllowed, r.RobotsVia = v.Allowed, via
			if v.Rule != nil {
				r.RobotsLine, r.RobotsRule = v.Rule.Line, v.Rule.Raw
			}
		}
		if live && !bot.TokenOnly() {
			r.Probed = true
			res := client.FetchWith(ctx, u, fetch.Override{UserAgent: bot.UserAgent})
			r.LiveStatus, r.LiveError = res.StatusCode, res.FetchError
			r.BlockedLive = blockedLive(r.BotVerdict, rep.ControlStatus, rep.ControlError)
		}
		rep.Bots = append(rep.Bots, r)
	}
	return rep
}

// blockedLive classifies an edge-level block: the control fetch succeeded
// (2xx/3xx) while the bot's probe did not. Attribution needs a healthy
// control — when the site fails for everyone, nothing is bot-specific.
func blockedLive(v BotVerdict, controlStatus int, controlError string) bool {
	controlOK := controlError == "" && controlStatus >= 200 && controlStatus < 400
	if !controlOK {
		return false
	}
	probeOK := v.LiveError == "" && v.LiveStatus >= 200 && v.LiveStatus < 400
	return !probeOK
}

// AIBotsURLsReport is the AI-bot audit over a list of pages on one host: the
// bot definitions and the robots.txt once, then each page's verdicts.
type AIBotsURLsReport struct {
	Site   string           `json:"site"`
	Live   bool             `json:"live"`
	Bots   []Bot            `json:"bots"`
	Robots AIBotsRobotsTxt  `json:"robots"`
	URLs   []AIBotsURLAudit `json:"urls"`
	Caveat string           `json:"caveat"`
}

// AIBotsRobotsTxt is the robots.txt every verdict was evaluated against, in
// the robots report's fields.
type AIBotsRobotsTxt struct {
	URL           string   `json:"url"`
	FinalURL      string   `json:"final_url,omitempty"`
	RedirectChain []string `json:"redirect_chain,omitempty"`
	Status        int      `json:"status"`
	FetchError    string   `json:"fetch_error,omitempty"`
	Found         bool     `json:"found"`
	// Body is capped at the 500 KiB Google reads.
	Body string `json:"body,omitempty"`
}

// AIBotsURLAudit is one page's control fetch and every bot's verdicts.
type AIBotsURLAudit struct {
	URL           string            `json:"url"`
	ControlStatus int               `json:"control_status,omitempty"`
	ControlError  string            `json:"control_error,omitempty"`
	Bots          []NamedBotVerdict `json:"bots"`
}

// NamedBotVerdict is a bot's verdicts keyed by its name; the definition
// travels once, in AIBotsURLsReport.Bots.
type NamedBotVerdict struct {
	Name string `json:"name"`
	BotVerdict
}

// AIBotsURLs runs the AI-bot audit on each listed page of a site instead of
// its root: one robots.txt fetch for every verdict, then per page what
// EvaluateAIBots does for the root. Every URL must be on the site's host —
// one robots.txt governs them all. Up to speed.max_threads pages run at once,
// and every fetch is paced to speed.max_urls_per_sec, so the pages × bots
// fetches to one host stay within the configured speed.
func (c *Checker) AIBotsURLs(ctx context.Context, site string, urls []string, opts AIBotOptions) (*AIBotsURLsReport, error) {
	root, err := siteRoot(site)
	if err != nil {
		return nil, err
	}
	pages, err := sameHostURLs(root, urls)
	if err != nil {
		return nil, err
	}
	client := c.paced()
	rf := FetchRobots(ctx, client, root)
	f := robots.Parse(rf.Body)
	bots := assembleBots(opts)
	rep := &AIBotsURLsReport{
		Site: root, Live: opts.Live, Bots: bots, Caveat: AIBotCaveat,
		Robots: AIBotsRobotsTxt{
			URL: rf.URL, FinalURL: rf.FinalURL, RedirectChain: rf.Chain,
			Status: rf.Status, FetchError: rf.FetchError, Found: rf.Found(),
			Body: string(rf.Body[:min(len(rf.Body), maxRobotsBytes)]),
		},
		URLs: make([]AIBotsURLAudit, len(pages)),
	}
	work := make(chan int)
	var wg sync.WaitGroup
	for range min(max(c.cfg.Speed.MaxThreads, 1), len(pages)) {
		wg.Go(func() {
			for i := range work {
				page := auditURL(ctx, client, f, pages[i], bots, opts.Live)
				res := AIBotsURLAudit{URL: page.URL, ControlStatus: page.ControlStatus, ControlError: page.ControlError}
				for _, b := range page.Bots {
					res.Bots = append(res.Bots, NamedBotVerdict{Name: b.Name, BotVerdict: b.BotVerdict})
				}
				rep.URLs[i] = res
			}
		})
	}
	for i := range pages {
		work <- i
	}
	close(work)
	wg.Wait()
	return rep, nil
}

// sameHostURLs checks every URL is an http(s) URL on root's host and drops
// repeats, keeping the first occurrence's order.
func sameHostURLs(root string, urls []string) ([]string, error) {
	host := urlutil.Authority(root)
	var out []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u := normalizePageURL(raw)
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return nil, fmt.Errorf("sitecheck: %q is not an http(s) URL", raw)
		}
		if urlutil.Authority(u) != host {
			return nil, fmt.Errorf("sitecheck: %s is not on %s's host (one run covers one host)", raw, root)
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("sitecheck: no URLs to audit")
	}
	return out, nil
}

// Findings derives each page's findings exactly as the site check derives
// the root's, attached to that page's URL.
func (r *AIBotsURLsReport) Findings() []Finding {
	defs := make(map[string]Bot, len(r.Bots))
	for _, b := range r.Bots {
		defs[b.Name] = b
	}
	var out []Finding
	for _, u := range r.URLs {
		page := &AIBotsReport{URL: u.URL, ControlStatus: u.ControlStatus, ControlError: u.ControlError}
		for _, v := range u.Bots {
			page.Bots = append(page.Bots, AIBotResult{Bot: defs[v.Name], BotVerdict: v.BotVerdict})
		}
		out = append(out, page.Findings()...)
	}
	return out
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
			via, note := "", ""
			if b.RobotsVia != "" {
				via = fmt.Sprintf(" (no group of its own; follows %s's)", b.RobotsVia)
			}
			if !b.RespectsRobots {
				note = "; note: this bot does not honour robots.txt — only an edge block is effective"
			}
			add("ai_bot_blocked_robots", fmt.Sprintf("%s (%s): line %d: %s%s%s",
				b.Name, b.Operator, b.RobotsLine, b.RobotsRule, via, note))
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
