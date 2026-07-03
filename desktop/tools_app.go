package main

import (
	"context"
	"encoding/json"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
)

// ToolsApp is the Wails binding for the standalone Tools hub — the desktop
// surface of internal/sitecheck. Like ProjectApp it is a SEPARATE bound struct
// so Wails generates its own ToolsApp.js and the core App binding stays
// untouched. Tool runs are throwaway by design (nothing is persisted
// anywhere); the same checks run automatically during full-domain crawls via
// site_checks and surface in the ordinary issues view. The App reference
// exists only for the process limiter: tool runs share the fetch/render
// ceilings of the crawls they run beside.
type ToolsApp struct{ app *App }

// NewToolsApp constructs the binding.
func NewToolsApp(app *App) *ToolsApp { return &ToolsApp{app: app} }

// ListTools returns the sitecheck registry — the hub renders one card per
// entry, so a new tool needs no desktop registration beyond its sub-view.
func (t *ToolsApp) ListTools() []sitecheck.Tool { return sitecheck.Tools() }

// toolFinding is a sitecheck finding decorated with its catalogue severity and
// display name, so the frontend findings strip renders the same chips as the
// crawl issues view without shipping the catalogue to JavaScript.
type toolFinding struct {
	sitecheck.Finding
	Severity string `json:"severity"`
	Name     string `json:"name"`
}

// RunTool runs one standalone tester and returns the same {report, findings}
// JSON the CLI --json flag and MCP run_tool emit, findings enriched with
// catalogue severities. argsJSON is the tool-specific options object ("" for
// none); unknown keys are rejected with the offender named.
func (t *ToolsApp) RunTool(name, target, argsJSON string) (string, error) {
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		return "", err
	}
	chk := sitecheck.New(cfg, client, sitecheck.WithLimiter(t.app.processLimiter()))
	rep, err := chk.RunTool(context.Background(), name, target, json.RawMessage(argsJSON))
	if err != nil {
		return "", err
	}
	fs := rep.Findings()
	decorated := make([]toolFinding, 0, len(fs))
	for _, f := range fs {
		tf := toolFinding{Finding: f, Name: f.IssueID}
		if def, ok := issues.Lookup(f.IssueID); ok {
			tf.Severity, tf.Name = string(def.Severity), def.Name
		}
		decorated = append(decorated, tf)
	}
	out, err := json.Marshal(struct {
		Report   any           `json:"report"`
		Findings []toolFinding `json:"findings"`
	}{rep, decorated})
	if err != nil {
		return "", err
	}
	return string(out), nil
}
