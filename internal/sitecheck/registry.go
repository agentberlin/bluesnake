package sitecheck

// Arg describes one tool argument for the registry (drives CLI help, the MCP
// run_tool schema, and the desktop hub).
type Arg struct {
	Type     string `json:"type"`
	Help     string `json:"help"`
	Required bool   `json:"required,omitempty"`
}

// Tool is one registry entry. The registry is the single catalogue every
// surface enumerates: `bluesnake tools list`, MCP list_tools, and the desktop
// Tools hub. Adding a tool = one entry here plus its check.
type Tool struct {
	Name    string         `json:"name"`
	Summary string         `json:"summary"`
	Args    map[string]Arg `json:"args,omitempty"`
}

// Tools returns the registry in display order.
func Tools() []Tool {
	return []Tool{
		{
			Name:    "robots",
			Summary: "Fetch and audit a site's robots.txt (Google REP semantics) and test URLs against it.",
			Args: map[string]Arg{
				"target":     {Type: "string", Help: "site root, or any URL on the site", Required: true},
				"urls":       {Type: "list of strings", Help: "URLs to test against the file"},
				"user_agent": {Type: "string", Help: "robots user-agent token for the verdicts (default: the configured http.robots_user_agent)"},
				"robots_txt": {Type: "string", Help: "audit this robots.txt body instead of fetching the live file"},
			},
		},
		{
			Name:    "sitemap",
			Summary: "Discover and validate a site's XML sitemaps: fetchability, XML shape, protocol limits, entry hygiene.",
			Args: map[string]Arg{
				"target":        {Type: "string", Help: "site root (discovery via robots.txt + conventions) or an explicit sitemap URL", Required: true},
				"check_entries": {Type: "int", Help: "live-check up to N listed URLs (default 0)"},
			},
		},
		{
			Name:    "aibots",
			Summary: "Test which AI crawlers (GPTBot, ClaudeBot, PerplexityBot, ...) can access a site: robots.txt verdicts plus live probes with each bot's User-Agent.",
			Args: map[string]Arg{
				"target": {Type: "string", Help: "site root, or any URL on the site", Required: true},
				"live":   {Type: "bool", Help: "probe the site root with each fetcher bot's User-Agent (default true)"},
				"skip":   {Type: "list of strings", Help: "registry bot names to exclude"},
			},
		},
		{
			Name:    "render",
			Summary: "Diff a page raw vs JavaScript-rendered: what non-rendering crawlers and AI bots miss (needs Chrome).",
			Args: map[string]Arg{
				"target": {Type: "string", Help: "page URL to fetch and render", Required: true},
			},
		},
		{
			Name:    "llms",
			Summary: "Fetch and structurally validate a site's /llms.txt and /llms-full.txt (llmstxt.org).",
			Args: map[string]Arg{
				"target": {Type: "string", Help: "site root, or any URL on the site", Required: true},
			},
		},
		{
			Name:    "structured",
			Summary: "Validate a page's schema.org structured data (JSON-LD, Microdata, RDFa) against Google rich-result requirements.",
			Args: map[string]Arg{
				"target": {Type: "string", Help: "page URL to fetch and validate", Required: true},
			},
		},
		{
			Name:    "serp",
			Summary: "Preview how a title and meta description render on Google's results page: pixel widths, threshold verdicts, truncation.",
			Args: map[string]Arg{
				"target":      {Type: "string", Help: "page URL to fetch the actual title/description from (optional when title/description are given)"},
				"title":       {Type: "string", Help: "title text to measure"},
				"description": {Type: "string", Help: "meta description text to measure"},
			},
		},
	}
}

// LookupTool returns the registry entry for a canonical tool name.
func LookupTool(name string) (Tool, bool) {
	for _, t := range Tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}
