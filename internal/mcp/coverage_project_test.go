package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// fakeBackend is a minimal Backend used to drive tools that only need the store
// directory (project tools, data-access tools). Crawl-control methods are not
// exercised here.
type fakeBackend struct{ dir string }

func (f *fakeBackend) StartCrawl(context.Context, StartRequest) (string, error) { return "", nil }
func (f *fakeBackend) ResumeCrawl(string) (string, error)                       { return "", nil }
func (f *fakeBackend) PauseCrawl(string) error                                  { return nil }
func (f *fakeBackend) StopCrawl(string) error                                   { return nil }
func (f *fakeBackend) Running() []Progress                                      { return nil }
func (f *fakeBackend) StoreDir() string                                         { return f.dir }

func projectServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	return NewServer(&fakeBackend{dir: dir}, "test"), dir
}

// TestProjectToolsLifecycle exercises the full project tool surface:
// create_project, list_projects, add_competitor, remove_competitor — all
// through the JSON-RPC tools/call dispatch.
func TestProjectToolsLifecycle(t *testing.T) {
	s, _ := projectServer(t)

	// empty list first
	text, isErr := callTool(t, s, "list_projects", map[string]any{})
	if isErr {
		t.Fatalf("list_projects (empty): %s", text)
	}
	if strings.TrimSpace(text) != "null" && strings.TrimSpace(text) != "[]" {
		t.Errorf("empty project list = %q, want null/[]", text)
	}

	// create with default name
	text, isErr = callTool(t, s, "create_project", map[string]any{"main_domain": "https://main.com/path"})
	if isErr {
		t.Fatalf("create_project: %s", text)
	}
	var created struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		MainDomain string `json:"main_domain"`
	}
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatalf("decode create_project: %v\n%s", err, text)
	}
	if created.ID == "" {
		t.Fatal("create_project returned no id")
	}
	if created.MainDomain != "main.com" {
		t.Errorf("main domain folded wrong: %q", created.MainDomain)
	}
	if created.Name != "main.com's Project" {
		t.Errorf("default name = %q", created.Name)
	}

	// add two competitors
	if text, isErr = callTool(t, s, "add_competitor", map[string]any{"project_id": created.ID, "domain": "rival.com"}); isErr {
		t.Fatalf("add_competitor: %s", text)
	}
	if text, isErr = callTool(t, s, "add_competitor", map[string]any{"project_id": created.ID, "domain": "https://other.com/x"}); isErr {
		t.Fatalf("add_competitor 2: %s", text)
	}

	// list now shows the project
	text, isErr = callTool(t, s, "list_projects", map[string]any{})
	if isErr || !strings.Contains(text, created.ID) || !strings.Contains(text, "main.com") {
		t.Fatalf("list_projects: isErr=%v text=%s", isErr, text)
	}

	// remove one competitor
	if text, isErr = callTool(t, s, "remove_competitor", map[string]any{"project_id": created.ID, "domain": "rival.com"}); isErr {
		t.Fatalf("remove_competitor: %s", text)
	}
}

// TestProjectToolsErrors pins the error branches of the project tools: invalid
// domain on create, missing project on add_competitor.
func TestProjectToolsErrors(t *testing.T) {
	s, _ := projectServer(t)

	if _, isErr := callTool(t, s, "create_project", map[string]any{"main_domain": ""}); !isErr {
		t.Error("create_project with empty domain should error")
	}
	if _, isErr := callTool(t, s, "add_competitor", map[string]any{"project_id": "nope", "domain": "x.com"}); !isErr {
		t.Error("add_competitor to missing project should error")
	}
	// bad arguments JSON shape is surfaced as a tool error too
	if _, isErr := callTool(t, s, "create_project", map[string]any{"main_domain": 12345}); !isErr {
		t.Error("create_project with non-string domain should error")
	}
}

// TestProjectComparisonScorecard builds a real project with crawls and checks
// the project_comparison tool returns a scorecard (sites, main first, a no-crawl
// row). Crawls are seeded directly via the store package — fully hermetic.
func TestProjectComparisonScorecard(t *testing.T) {
	s, dir := projectServer(t)
	def := config.Default()

	makeComparableCrawl(t, dir, "https://main.com", def, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count) VALUES
			('https://main.com/', 'internal', 'crawled', 200, 1, 50, 0),
			('https://main.com/a', 'internal', 'crawled', 200, 1, 10, 0)`)
	})
	makeComparableCrawl(t, dir, "https://rival.com", def, func(c *store.Crawl) {
		c.DB().Exec(`INSERT INTO pages(url, scope, state, status_code, indexable, link_score, near_dup_count) VALUES
			('https://rival.com/', 'internal', 'crawled', 200, 1, 80, 0)`)
	})

	// create project + members through the tools
	text, _ := callTool(t, s, "create_project", map[string]any{"main_domain": "main.com"})
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(text), &created)
	callTool(t, s, "add_competitor", map[string]any{"project_id": created.ID, "domain": "rival.com"})
	callTool(t, s, "add_competitor", map[string]any{"project_id": created.ID, "domain": "nocrawl.com"})

	text, isErr := callTool(t, s, "project_comparison", map[string]any{"project_id": created.ID, "include_optional": true})
	if isErr {
		t.Fatalf("project_comparison: %s", text)
	}
	var card struct {
		Sites []struct {
			Domain string `json:"domain"`
			Role   string `json:"role"`
			Status string `json:"status"`
			URLs   int    `json:"urls"`
		} `json:"sites"`
	}
	if err := json.Unmarshal([]byte(text), &card); err != nil {
		t.Fatalf("decode scorecard: %v\n%s", err, text)
	}
	if len(card.Sites) != 3 {
		t.Fatalf("sites = %d, want 3", len(card.Sites))
	}
	if card.Sites[0].Role != "main" || card.Sites[0].Domain != "main.com" {
		t.Errorf("first row is not main: %+v", card.Sites[0])
	}
	var nocrawl bool
	for _, site := range card.Sites {
		if site.Domain == "nocrawl.com" && site.Status == "no-crawl" {
			nocrawl = true
		}
	}
	if !nocrawl {
		t.Errorf("nocrawl.com should be a no-crawl row: %s", text)
	}

	// project_comparison on a missing project is a tool error.
	if _, isErr := callTool(t, s, "project_comparison", map[string]any{"project_id": "nope"}); !isErr {
		t.Error("project_comparison on missing project should error")
	}
}

// makeComparableCrawl registers a completed root spider crawl with the given
// config so the project layer treats it as comparable.
func makeComparableCrawl(t *testing.T, dir, seed string, cfg *config.Config, fn func(*store.Crawl)) string {
	t.Helper()
	c, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fn != nil {
		fn(c)
	}
	id := c.ID
	c.Close()
	if err := store.SetStatus(dir, id, store.StatusCompleted, 0, 0); err != nil {
		t.Fatal(err)
	}
	return id
}
