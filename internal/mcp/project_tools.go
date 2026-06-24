package mcp

import (
	"context"
	"encoding/json"

	"github.com/agentberlin/bluesnake/internal/project"
)

// projectTools is the MCP surface of the opt-in project layer (competitor
// study). It is additive and removable: delete this file and the one
// `append(tools, s.projectTools()...)` line in buildTools. Handlers open the
// project layer's own database and read crawls through store's public API only.
func (s *Server) projectTools() []Tool {
	open := func() (*project.Store, error) { return project.Open(s.backend.StoreDir()) }

	return []Tool{
		{
			Name:        "list_projects",
			Description: "List competitor-study projects: id, name, main domain. A project groups a main domain with competitor domains for comparison.",
			InputSchema: schema(map[string]any{}),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				st, err := open()
				if err != nil {
					return "", err
				}
				defer st.Close()
				ps, err := st.ListProjects()
				if err != nil {
					return "", err
				}
				return jsonText(ps)
			},
		},
		{
			Name:        "create_project",
			Description: "Create a competitor-study project anchored on a main domain. The default name is \"<main_domain>'s Project\". Add competitors with add_competitor.",
			InputSchema: schema(map[string]any{
				"main_domain": strProp("The main site's domain or URL (e.g. \"example.com\"). Stored as its exact host[:port] — no www/subdomain folding."),
				"name":        strProp("Optional display name; defaults to \"<main_domain>'s Project\"."),
			}, "main_domain"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					MainDomain string `json:"main_domain"`
					Name       string `json:"name"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				st, err := open()
				if err != nil {
					return "", err
				}
				defer st.Close()
				p, err := st.CreateProject(a.Name, a.MainDomain)
				if err != nil {
					return "", err
				}
				return jsonText(p)
			},
		},
		{
			Name:        "add_competitor",
			Description: "Add a competitor domain to a project. The domain is keyed by its exact host[:port]; its crawl history is resolved live from the crawl registry.",
			InputSchema: schema(map[string]any{
				"project_id": strProp("Project id (see list_projects)."),
				"domain":     strProp("Competitor domain or URL."),
			}, "project_id", "domain"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					ProjectID string `json:"project_id"`
					Domain    string `json:"domain"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				st, err := open()
				if err != nil {
					return "", err
				}
				defer st.Close()
				if err := st.AddMember(a.ProjectID, a.Domain, project.RoleCompetitor); err != nil {
					return "", err
				}
				return "ok", nil
			},
		},
		{
			Name:        "remove_competitor",
			Description: "Remove a domain from a project. Crawls are not deleted.",
			InputSchema: schema(map[string]any{
				"project_id": strProp("Project id."),
				"domain":     strProp("Domain to remove."),
			}, "project_id", "domain"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					ProjectID string `json:"project_id"`
					Domain    string `json:"domain"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				st, err := open()
				if err != nil {
					return "", err
				}
				defer st.Close()
				if err := st.RemoveMember(a.ProjectID, a.Domain); err != nil {
					return "", err
				}
				return "ok", nil
			},
		},
		{
			Name: "project_comparison",
			Description: "Competitor scorecard for a project: the main site vs each competitor, using each site's LATEST comparable crawl (a full-site spider crawl of the root). " +
				"Returns per-site metrics (URLs, indexable rate, status-code buckets, issue counts by severity, link score, near-dup load; word count & schema coverage when include_optional is set), the resolved crawl id + timestamp, config badges, and a config-divergence flag. " +
				"Sites with no comparable crawl come back with status \"no-crawl\".",
			InputSchema: schema(map[string]any{
				"project_id":       strProp("Project id (see list_projects)."),
				"include_optional": map[string]any{"type": "boolean", "description": "Include content-depth (avg word count, readability) and schema.org coverage; reads page JSON, slightly costlier."},
			}, "project_id"),
			handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					ProjectID       string `json:"project_id"`
					IncludeOptional bool   `json:"include_optional"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				st, err := open()
				if err != nil {
					return "", err
				}
				defer st.Close()
				card, err := st.BuildScorecard(a.ProjectID, a.IncludeOptional)
				if err != nil {
					return "", err
				}
				return jsonText(card)
			},
		},
	}
}
