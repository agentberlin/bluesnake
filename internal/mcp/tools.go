package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
	"gopkg.in/yaml.v3"
)

// Tool is one MCP tool: JSON-Schema'd input, text output. Handlers return an
// error to produce an isError result the model can read and self-correct on.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// ---- JSON Schema helpers --------------------------------------------------

func schema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func decodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func jsonText(v any) (string, error) {
	out, err := json.MarshalIndent(v, "", " ")
	return string(out), err
}

// ---------------------------------------------------------------------------

func (s *Server) buildTools() []Tool {
	return []Tool{
		// -- discovery: what can be configured -----------------------------
		{
			Name: "list_config_options",
			Description: "List every crawl configuration knob: dotted key, type, default, allowed values, and what it does. " +
				"Pass any of these keys in start_crawl's `config` map. Optionally filter by section (the key prefix before the first dot, e.g. \"limits\", \"rendering\", \"speed\").",
			InputSchema: schema(map[string]any{
				"section": strProp("Only return keys in this section, e.g. \"scope\", \"limits\", \"rendering\", \"robots\", \"speed\", \"http\", \"thresholds\", \"content\", \"analysis\"."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Section string `json:"section"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				opts := Catalog()
				if a.Section != "" {
					filtered := opts[:0]
					for _, o := range opts {
						if o.Key == a.Section || strings.HasPrefix(o.Key, a.Section+".") {
							filtered = append(filtered, o)
						}
					}
					opts = filtered
					if len(opts) == 0 {
						return "", fmt.Errorf("no config section %q — call list_config_options without arguments to see everything", a.Section)
					}
				}
				return jsonText(map[string]any{
					"options": opts,
					"usage":   "Pass values via start_crawl's `config` map, e.g. {\"limits.max_urls\": 500, \"rendering.mode\": \"javascript\"}. Options marked with a note may be partially wired.",
				})
			},
		},
		{
			Name: "list_profiles",
			Description: "List saved configuration profiles (named YAML presets shared with the desktop app and CLI). " +
				"Pass a profile name to start_crawl to use it as the base config.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				names := ListProfileNames(s.backend.StoreDir())
				out := map[string]any{
					"profiles": names,
					"note":     "start_crawl uses \"" + defaultProfileName + "\" semantics when no profile is given: the saved default profile if present, otherwise built-in defaults. get_profile_config shows a profile's effective settings.",
				}
				if len(names) == 0 {
					out["profiles"] = []string{}
					out["note"] = "No saved profiles yet — start_crawl will use built-in defaults (Screaming Frog-parity settings). The desktop app's Settings view creates profiles."
				}
				return jsonText(out)
			},
		},
		{
			Name:        "get_profile_config",
			Description: "Show a profile's full effective configuration as YAML (every knob with its value). Omit `profile` for the defaults used when start_crawl gets no profile.",
			InputSchema: schema(map[string]any{
				"profile": strProp("Profile name from list_profiles. Omit for the default."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Profile string `json:"profile"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				cfg, err := loadProfile(s.backend.StoreDir(), a.Profile)
				if err != nil {
					return "", err
				}
				data, err := yaml.Marshal(cfg)
				if err != nil {
					return "", err
				}
				return string(data), nil
			},
		},

		// -- crawl control --------------------------------------------------
		{
			Name: "start_crawl",
			Description: "Start a crawl in the background and return its crawl_id immediately. Spider mode (default) needs `url`; " +
				"list mode audits a fixed set via `urls` or `sitemap_url`. Base config comes from `profile` (or defaults); " +
				"any knob from list_config_options can be overridden per-crawl via `config`. " +
				"Poll crawl_status to watch progress. One crawl runs at a time.",
			InputSchema: schema(map[string]any{
				"url":         strProp("Seed URL for spider mode, including http:// or https://."),
				"mode":        map[string]any{"type": "string", "enum": []string{"spider", "list"}, "description": "spider (default) follows links from url; list audits a fixed URL set."},
				"urls":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "URLs to audit in list mode."},
				"sitemap_url": strProp("XML sitemap whose URLs become the list (list mode)."),
				"project":     strProp("Project name for the stored crawl. Defaults to the seed's hostname."),
				"profile":     strProp("Config profile to start from (see list_profiles)."),
				"config":      map[string]any{"type": "object", "additionalProperties": true, "description": "Dotted-path overrides, e.g. {\"limits.max_urls\": 500, \"speed.max_threads\": 10, \"rendering.mode\": \"javascript\"}. Discover keys with list_config_options."},
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var req StartRequest
				if err := decodeArgs(raw, &req); err != nil {
					return "", err
				}
				id, err := s.backend.StartCrawl(ctx, req)
				if err != nil {
					return "", err
				}
				return jsonText(map[string]any{
					"crawl_id": id,
					"state":    "running",
					"next":     "Poll crawl_status (a few seconds apart) until state is \"completed\"; then issue_summary and query.",
				})
			},
		},
		{
			Name: "crawl_status",
			Description: "Progress of the live crawl (counters, queue, URLs/sec) or the stored status of a finished one. " +
				"Omit crawl_id for the live crawl (falling back to the most recent).",
			InputSchema: schema(map[string]any{
				"crawl_id": strProp("Crawl to inspect. Omit for the live/most recent crawl."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					CrawlID string `json:"crawl_id"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				if p := s.backend.Progress(); p != nil && (a.CrawlID == "" || a.CrawlID == p.CrawlID) {
					return jsonText(p)
				}
				info, err := s.crawlInfo(a.CrawlID)
				if err != nil {
					return "", err
				}
				return jsonText(info)
			},
		},
		{
			Name:        "pause_crawl",
			Description: "Pause the live crawl. Everything crawled so far is saved and the crawl stays resumable via resume_crawl.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				if err := s.backend.PauseCrawl(); err != nil {
					return "", err
				}
				return jsonText(map[string]any{
					"state": "pausing",
					"note":  "Workers wind down within a few seconds; the crawl is then resumable with resume_crawl. crawl_status reports \"interrupted\" once paused.",
				})
			},
		},
		{
			Name:        "resume_crawl",
			Description: "Resume a paused/interrupted crawl from its stored frontier. Already-crawled URLs are not re-fetched.",
			InputSchema: schema(map[string]any{
				"crawl_id": strProp("The interrupted crawl to resume (see list_crawls)."),
			}, "crawl_id"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					CrawlID string `json:"crawl_id"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				id, err := s.backend.ResumeCrawl(a.CrawlID)
				if err != nil {
					return "", err
				}
				return jsonText(map[string]any{"crawl_id": id, "state": "running"})
			},
		},
		{
			Name:        "stop_crawl",
			Description: "Stop the live crawl and finalise it as completed: inlink aggregation and (if configured) issue analysis run on what was crawled so far. Use pause_crawl instead if you may want to continue later.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				if err := s.backend.StopCrawl(); err != nil {
					return "", err
				}
				return jsonText(map[string]any{
					"state": "stopping",
					"note":  "Finalisation (inlinks + analysis) runs now; crawl_status reports \"completed\" when done.",
				})
			},
		},
		{
			Name:        "list_crawls",
			Description: "List all stored crawls: id, project, seed, mode, status (running | completed | interrupted), start time, URL count.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				infos, err := store.ListCrawls(s.backend.StoreDir())
				if err != nil {
					return "", err
				}
				sort.SliceStable(infos, func(i, j int) bool { return infos[i].Started.After(infos[j].Started) })
				out := make([]map[string]any, 0, len(infos))
				for _, in := range infos {
					out = append(out, crawlInfoJSON(in))
				}
				return jsonText(map[string]any{"crawls": out, "count": len(out)})
			},
		},

		// -- data access ----------------------------------------------------
		{
			Name: "get_database_schema",
			Description: "The SQLite schema of a crawl database (every crawl has the same shape): exact DDL plus column annotations and example queries. " +
				"Read this once before using query.",
			InputSchema: schema(map[string]any{
				"crawl_id": strProp("Crawl whose database to describe. Omit for the most recent."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					CrawlID string `json:"crawl_id"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				db, id, err := s.openCrawlRO(a.CrawlID)
				if err != nil {
					return "", err
				}
				defer db.Close()
				rows, err := db.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END, name`)
				if err != nil {
					return "", err
				}
				defer rows.Close()
				var ddl strings.Builder
				for rows.Next() {
					var stmt string
					if err := rows.Scan(&stmt); err != nil {
						return "", err
					}
					ddl.WriteString(stmt)
					ddl.WriteString(";\n")
				}
				if err := rows.Err(); err != nil {
					return "", err
				}
				return fmt.Sprintf("Schema of crawl %s (dialect: SQLite; access via the query tool is read-only):\n\n%s\n%s", id, ddl.String(), schemaNotes), nil
			},
		},
		{
			Name: "query",
			Description: "Run a read-only SQL query against a crawl's SQLite database and get rows back as JSON. " +
				"Any single SQLite SELECT/CTE works, including json_extract over the JSON columns. " +
				"Call get_database_schema first to see tables and columns.",
			InputSchema: schema(map[string]any{
				"sql":      strProp("One SQLite statement, e.g. SELECT url, status_code FROM pages WHERE status_code >= 400."),
				"crawl_id": strProp("Crawl to query (see list_crawls). Omit for the most recent."),
				"max_rows": intProp("Row cap for the result (default 200, max 5000). The result notes when rows were truncated."),
			}, "sql"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					SQL     string `json:"sql"`
					CrawlID string `json:"crawl_id"`
					MaxRows int    `json:"max_rows"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				if strings.TrimSpace(a.SQL) == "" {
					return "", fmt.Errorf("sql is required")
				}
				if err := guardReadOnlySQL(a.SQL); err != nil {
					return "", err
				}
				maxRows := a.MaxRows
				if maxRows <= 0 {
					maxRows = 200
				}
				if maxRows > 5000 {
					maxRows = 5000
				}
				db, id, err := s.openCrawlRO(a.CrawlID)
				if err != nil {
					return "", err
				}
				defer db.Close()
				qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return runQuery(qctx, db, id, a.SQL, maxRows)
			},
		},
		{
			Name: "issue_summary",
			Description: "The audit verdict for a crawl: every triggered check with its name, severity (issue | warning | opportunity), priority, and how many URLs are affected. " +
				"Affected URLs are one query away: SELECT url, detail FROM issues WHERE issue = '<id>'.",
			InputSchema: schema(map[string]any{
				"crawl_id": strProp("Crawl to summarise. Omit for the most recent."),
			}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					CrawlID string `json:"crawl_id"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				return s.issueSummary(ctx, a.CrawlID)
			},
		},
	}
}

// ---------------------------------------------------------------------------
// shared tool plumbing

// resolveCrawlID maps "" to the most recent stored crawl.
func (s *Server) resolveCrawlID(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	infos, err := store.ListCrawls(s.backend.StoreDir())
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("no crawls stored yet — start one with start_crawl")
	}
	latest := infos[0]
	for _, in := range infos[1:] {
		if in.Started.After(latest.Started) {
			latest = in
		}
	}
	return latest.ID, nil
}

// openCrawlRO opens a crawl database read-only — crawl integrity is engine
// business; the query tool is a window, not a write path. WAL means reads
// stay consistent while a crawl is writing.
func (s *Server) openCrawlRO(id string) (*sql.DB, string, error) {
	id, err := s.resolveCrawlID(id)
	if err != nil {
		return nil, "", err
	}
	path, err := store.CrawlDBPath(s.backend.StoreDir(), id)
	if err != nil {
		return nil, "", err
	}
	// mode=ro makes the main DB read-only; query_only blocks any writes to
	// attached/temp DBs; trusted_schema=0 hardens against malicious
	// schema-defined functions. Combined with guardReadOnlySQL (which rejects
	// anything that isn't a single SELECT/WITH/EXPLAIN/VALUES and thus blocks
	// ATTACH), this keeps the publicly-reachable query tool a true read window.
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(0)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=3000"); err != nil {
		db.Close()
		return nil, "", err
	}
	return db, id, nil
}

// guardReadOnlySQL rejects anything that isn't a single read-only statement.
// This is the primary defense (driver-independent) against ATTACH/DETACH
// escaping the read-only main DB to read arbitrary files — ATTACH cannot appear
// inside a SELECT and cannot be a second statement, so requiring a single
// SELECT/WITH/EXPLAIN/VALUES/PRAGMA statement blocks it outright.
func guardReadOnlySQL(q string) error {
	// Reject multiple statements (ignoring a single optional trailing ';').
	body := strings.TrimRight(strings.TrimSpace(q), "; \t\r\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("only a single statement is allowed")
	}
	switch firstSQLKeyword(body) {
	case "select", "with", "explain", "values", "pragma":
		return nil
	default:
		return fmt.Errorf("only read-only SELECT/WITH queries are allowed")
	}
}

// firstSQLKeyword returns the lowercased first token of a SQL statement after
// skipping leading line (--) and block (/* */) comments and whitespace.
func firstSQLKeyword(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
			} else {
				return ""
			}
		default:
			end := len(s)
			for i, r := range s {
				if r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '(' {
					end = i
					break
				}
			}
			return strings.ToLower(s[:end])
		}
	}
}

func runQuery(ctx context.Context, db *sql.DB, crawlID, query string, maxRows int) (string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("sqlite: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	var data [][]any
	truncated := false
	for rows.Next() {
		if len(data) >= maxRows {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		data = append(data, vals)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sqlite: %w", err)
	}
	out := map[string]any{
		"crawl_id":  crawlID,
		"columns":   cols,
		"rows":      data,
		"row_count": len(data),
	}
	if truncated {
		out["truncated"] = true
		out["note"] = fmt.Sprintf("only the first %d rows returned — narrow the query or raise max_rows", maxRows)
	}
	return jsonText(out)
}

func crawlInfoJSON(in store.Info) map[string]any {
	// total is "URLs encountered" (fetched + robots-blocked + errored), the
	// headline count; crawled is the fetched subset. Older crawls predate the
	// total column (0) — fall back to crawled so the headline is never blank.
	total := in.Total
	if total == 0 {
		total = in.Crawled
	}
	m := map[string]any{
		"crawl_id": in.ID,
		"project":  in.Project,
		"seed":     in.Seed,
		"mode":     in.Mode,
		"status":   in.Status,
		"crawled":  in.Crawled,
		"total":    total,
	}
	if !in.Started.IsZero() {
		m["started"] = in.Started.Format(time.RFC3339)
	}
	if !in.Finished.IsZero() {
		m["finished"] = in.Finished.Format(time.RFC3339)
	}
	return m
}

func (s *Server) crawlInfo(id string) (map[string]any, error) {
	id, err := s.resolveCrawlID(id)
	if err != nil {
		return nil, err
	}
	infos, err := store.ListCrawls(s.backend.StoreDir())
	if err != nil {
		return nil, err
	}
	for _, in := range infos {
		if in.ID == id {
			m := crawlInfoJSON(in)
			switch in.Status {
			case store.StatusCompleted:
				m["next"] = "issue_summary for the audit verdict; query for anything deeper."
			case store.StatusInterrupted:
				m["next"] = "resume_crawl continues it; the data crawled so far is already queryable."
			}
			return m, nil
		}
	}
	return nil, fmt.Errorf("crawl %q not found — list_crawls shows what exists", id)
}

func (s *Server) issueSummary(ctx context.Context, id string) (string, error) {
	db, id, err := s.openCrawlRO(id)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT issue, COUNT(*) FROM issues GROUP BY issue`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type check struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Severity string `json:"severity"`
		Priority string `json:"priority"`
		URLs     int    `json:"urls_affected"`
	}
	var checks []check
	totals := map[string]int{"issue": 0, "warning": 0, "opportunity": 0}
	for rows.Next() {
		var cid string
		var n int
		if err := rows.Scan(&cid, &n); err != nil {
			return "", err
		}
		c := check{ID: cid, Name: cid, Severity: "issue", URLs: n}
		if def, ok := issues.Lookup(cid); ok {
			c.Name = def.Name
			c.Severity = string(def.Severity)
			c.Priority = string(def.Priority)
		}
		totals[c.Severity] += n
		checks = append(checks, c)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sevRank := map[string]int{"issue": 0, "warning": 1, "opportunity": 2}
	prioRank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(checks, func(i, j int) bool {
		if sevRank[checks[i].Severity] != sevRank[checks[j].Severity] {
			return sevRank[checks[i].Severity] < sevRank[checks[j].Severity]
		}
		if prioRank[checks[i].Priority] != prioRank[checks[j].Priority] {
			return prioRank[checks[i].Priority] < prioRank[checks[j].Priority]
		}
		return checks[i].URLs > checks[j].URLs
	})
	out := map[string]any{
		"crawl_id": id,
		"totals": map[string]int{
			"issues": totals["issue"], "warnings": totals["warning"], "opportunities": totals["opportunity"],
		},
		"checks": checks,
		"note":   "Issue occurrences appear after a crawl completes (analysis.auto) or after re-analysis. Affected URLs: query SELECT url, detail FROM issues WHERE issue = '<id>'.",
	}
	if checks == nil {
		out["checks"] = []check{}
	}
	return jsonText(out)
}

const schemaNotes = `Notes:
- pages: one row per discovered URL. scope is internal|external; state is crawled|blocked_robots|error|skipped_*; indexable is 0/1 with the reason in indexability_status; redirect_url/redirect_type are set for 3xx/meta-refresh/JS redirects (redirects are stored as data, never auto-followed).
- pages.facts (JSON, PascalCase keys): the parsed on-page data — Titles, Descriptions, H1s, H2s, MetaRobots, CanonicalHTML, HreflangHTML, WordCount, TextRatio, Flesch, Lang, Links. Access with json_extract, e.g. json_extract(facts,'$.Titles[0]') or json_extract(facts,'$.WordCount').
- pages.headers (JSON): response headers. pages.structured (JSON): JSON-LD/Microdata/RDFa items. pages.jsdiff (JSON): static-vs-rendered differences (JavaScript rendering mode).
- links: the edge list. type is hyperlink|image|css|js|iframe|canonical|hreflang|next|prev|amp|meta_refresh|...; nofollow is 0/1.
- issues: issue ids per URL — issue_summary translates ids to names/severities.
- meta: key/value crawl metadata (config YAML under 'config', seed, mode).
- frontier: URLs discovered but not yet crawled (the pending queue of a paused crawl).
- custom_results: custom search/extraction hits (kind is 'search'|'extraction').
- sitemap_entries: URLs listed per sitemap.
- analysis: post-crawl analysis blobs (redirect chains, near-duplicate clusters) keyed by analysis name.

Example queries:
  SELECT status_code, COUNT(*) FROM pages WHERE scope='internal' GROUP BY status_code;
  SELECT url, json_extract(facts,'$.Titles[0]') AS title FROM pages WHERE indexable=1 AND length(title) > 60;
  SELECT url, inlinks, link_score FROM pages WHERE scope='internal' ORDER BY link_score DESC LIMIT 20;
  SELECT src, anchor FROM links WHERE dst = 'https://example.com/old-page' AND type = 'hyperlink' LIMIT 50;
  SELECT url, json_extract(facts,'$.WordCount') AS words FROM pages WHERE state='crawled' ORDER BY words ASC LIMIT 10;
  SELECT url, detail FROM issues WHERE issue = '<id from issue_summary>';`
