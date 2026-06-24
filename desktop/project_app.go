package main

import (
	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/queue"
)

// ProjectApp is the Wails binding for the opt-in project layer (competitor
// study). It is a SEPARATE bound struct (not methods on *App) so Wails generates
// its own ProjectApp.js and the core App binding is untouched — removing the
// feature is: drop this file, drop ProjectApp from the Bind slice in main.go,
// delete the generated ProjectApp.js and the frontend projects view. It holds no
// crawl state; every call opens the project layer's own database read-side. The
// one-way reference to *App lets "crawl all" enqueue through the core queue; the
// core App never references the project layer back, so removal stays clean.
type ProjectApp struct {
	storeDir string
	app      *App
}

// NewProjectApp constructs the binding against the shared store directory.
func NewProjectApp(app *App) *ProjectApp {
	return &ProjectApp{storeDir: app.storeDir, app: app}
}

// CrawlAll enqueues a default spider crawl for every member domain of the
// project, returning how many jobs it queued. The crawls run one at a time
// through the app's single dispatcher (no parallel crawls), interleaved with any
// hand-started crawls. A standalone crawl of a member domain already auto-joins
// the project, so this is just "(re)crawl everything in this project now".
func (a *ProjectApp) CrawlAll(projectID string) (int, error) {
	s, err := a.open()
	if err != nil {
		return 0, err
	}
	defer s.Close()
	members, err := s.Members(projectID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range members {
		spec := queue.JobSpec{URL: "https://" + m.Domain}
		if _, err := a.app.EnqueueCrawl(spec, "project", projectID, m.Domain); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (a *ProjectApp) open() (*project.Store, error) { return project.Open(a.storeDir) }

// SiteView is one project member plus its full (classified) crawl history.
type SiteView struct {
	Domain string              `json:"domain"`
	Role   string              `json:"role"`
	Crawls []project.SiteCrawl `json:"crawls"` // newest first; Comparable flags the usable ones
}

// ListProjects returns all projects.
func (a *ProjectApp) ListProjects() ([]project.Project, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	ps, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	if ps == nil {
		ps = []project.Project{}
	}
	return ps, nil
}

// CreateProject creates a project anchored on mainDomain.
func (a *ProjectApp) CreateProject(name, mainDomain string) (*project.Project, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.CreateProject(name, mainDomain)
}

// RenameProject changes a project's display name.
func (a *ProjectApp) RenameProject(id, name string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RenameProject(id, name)
}

// DeleteProject removes a project (crawls untouched).
func (a *ProjectApp) DeleteProject(id string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.DeleteProject(id)
}

// AddCompetitor adds a competitor domain to a project.
func (a *ProjectApp) AddCompetitor(id, domain string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.AddMember(id, domain, project.RoleCompetitor)
}

// RemoveCompetitor removes a domain from a project.
func (a *ProjectApp) RemoveCompetitor(id, domain string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RemoveMember(id, domain)
}

// ProjectSites returns each member site with its classified crawl history
// (newest first). The frontend renders the latest comparable crawl and grays out
// the rest with their reason.
func (a *ProjectApp) ProjectSites(id string) ([]SiteView, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	members, err := s.Members(id)
	if err != nil {
		return nil, err
	}
	views := make([]SiteView, 0, len(members))
	for _, m := range members {
		hist, err := s.SiteHistory(m.Domain)
		if err != nil {
			return nil, err
		}
		if hist == nil {
			hist = []project.SiteCrawl{}
		}
		views = append(views, SiteView{Domain: m.Domain, Role: string(m.Role), Crawls: hist})
	}
	return views, nil
}

// ProjectComparison computes the cross-competitor scorecard (Mode B).
func (a *ProjectApp) ProjectComparison(id string, includeOptional bool) (*project.Scorecard, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.BuildScorecard(id, includeOptional)
}

// ProjectDiff is the per-competitor over-time URL/issue diff (Mode A): it
// resolves the site's two latest comparable crawls and runs the existing
// pairwise compare. ok is false when the site has fewer than two such crawls.
type ProjectDiffResult struct {
	OK     bool            `json:"ok"`
	PrevID string          `json:"prev_id,omitempty"`
	CurrID string          `json:"curr_id,omitempty"`
	Result *compare.Result `json:"result,omitempty"`
}

func (a *ProjectApp) ProjectDiff(id, domain string) (*ProjectDiffResult, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	prevID, currID, ok, err := s.ComparePair(domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &ProjectDiffResult{OK: false}, nil
	}
	res, err := s.Compare(prevID, currID)
	if err != nil {
		return nil, err
	}
	return &ProjectDiffResult{OK: true, PrevID: prevID, CurrID: currID, Result: res}, nil
}
