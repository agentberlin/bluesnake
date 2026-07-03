package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
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

// CrawlAll enqueues a spider crawl for every member domain of the project.
// The request's setup applies per member in two layers (#88): the base —
// ConfigSource "last" gives each member its own site's last-crawl setup
// (falling back to the app settings for a never-crawled member; the dialog's
// default), while a profile (or none) is one shared base for every member —
// and, over whichever base, the request's touched quick knobs as batch-wide
// absolute overrides. Nothing is stored per member: the setup belongs to the
// domain, resolved (and frozen — EnqueueCrawl → runner.FreezeSpec) at
// enqueue like any other crawl. Returns how many jobs it queued. The crawls
// drain through the app's single dispatcher (up to
// speed.max_concurrent_crawls at a time), interleaved with any hand-started
// crawls. A standalone crawl of a member domain already auto-joins the
// project, so this is just "(re)crawl everything in this project now".
func (a *ProjectApp) CrawlAll(projectID string, req StartRequest) (int, error) {
	s, err := a.open()
	if err != nil {
		return 0, err
	}
	defer s.Close()
	members, err := s.Members(projectID)
	if err != nil {
		return 0, err
	}
	// Freeze every member's spec before queueing any, so one member whose
	// setup can't resolve (e.g. an unreadable last crawl) fails the whole
	// batch cleanly instead of leaving a partial queue.
	specs := make([]queue.JobSpec, 0, len(members))
	for _, m := range members {
		// members are always spider crawls of their domain root; only the
		// request's setup source, profile and touched knobs carry over
		spec := req.toSpec()
		spec.Mode, spec.URLs, spec.SitemapURL = "", nil, ""
		spec.URL = "https://" + m.Domain
		frozen, err := runner.FreezeSpec(a.storeDir, spec)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", m.Domain, err)
		}
		specs = append(specs, frozen)
	}
	n := 0
	for i, m := range members {
		if _, err := a.app.EnqueueCrawl(specs[i], "project", projectID, m.Domain); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (a *ProjectApp) open() (*project.Store, error) { return project.Open(a.storeDir) }

// MemberSetup is one row of the crawl-all dialog's per-site plan: which setup
// the member will crawl with when each site uses its own ("last") source.
type MemberSetup struct {
	Domain  string `json:"domain"`
	Role    string `json:"role"`
	HasLast bool   `json:"hasLast"`           // false: never crawled — app settings
	CrawlID string `json:"crawlId,omitempty"` // the crawl whose setup will be reused
	Started int64  `json:"started,omitempty"` // unix seconds
	Error   string `json:"error,omitempty"`   // that crawl's setup can't be read
}

// CrawlAllPlan previews the per-member resolution the default crawl-all mode
// will use, through the same lookup enqueue resolves with
// (runner.FindLastSetup) — computed here in the desktop layer so the project
// package stays untouched by the setup-source feature. A member whose last
// crawl can't provide its setup is reported, not skipped: the dialog shows
// the problem before "start" would hit it.
func (a *ProjectApp) CrawlAllPlan(projectID string) ([]MemberSetup, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	members, err := s.Members(projectID)
	if err != nil {
		return nil, err
	}
	plan := make([]MemberSetup, 0, len(members))
	for _, m := range members {
		row := MemberSetup{Domain: m.Domain, Role: string(m.Role)}
		switch ls, err := runner.FindLastSetup(a.storeDir, "https://"+m.Domain); {
		case err != nil:
			row.Error = err.Error()
		case ls != nil:
			row.HasLast, row.CrawlID, row.Started = true, ls.CrawlID, ls.Started.Unix()
		}
		plan = append(plan, row)
	}
	return plan, nil
}

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
