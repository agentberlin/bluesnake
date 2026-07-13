package main

import (
	"fmt"
	"sort"

	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
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
// drain through the app's single dispatcher (speed.max_concurrent_crawls at a
// time; 0 = all in parallel), interleaved with any hand-started
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

// MemberPulse is one member's "what's happening" summary (Mode A, always-on):
// the site's history reduced to trend points plus the delta between its two
// latest comparable crawls. The delta rides the desktop comparison cache
// (App.CompareCrawls), so the pairwise diff computes once per crawl pair and
// every later visit — here or in the crawl's Compare tab — is a registry read.
// Computed entirely in the desktop layer; the project package stays untouched.
type MemberPulse struct {
	Domain string `json:"domain"`
	OK     bool   `json:"ok"`     // false: fewer than two comparable crawls
	Cached bool   `json:"cached"` // delta served from the comparison cache

	PrevID      string `json:"prev_id,omitempty"`
	CurrID      string `json:"curr_id,omitempty"`
	PrevStarted int64  `json:"prev_started,omitempty"` // unix seconds
	CurrStarted int64  `json:"curr_started,omitempty"`

	PagesPrev    int `json:"pages_prev"`
	PagesCurr    int `json:"pages_curr"`
	NewPages     int `json:"new_pages"`
	RemovedPages int `json:"removed_pages"`

	StatusFlips       int `json:"status_flips"`
	IndexabilityFlips int `json:"indexability_flips"`
	ElementChanges    int `json:"element_changes"`

	IssuesAppeared int `json:"issues_appeared"`
	IssuesResolved int `json:"issues_resolved"`

	IndexablePrev int `json:"indexable_prev"`
	IndexableCurr int `json:"indexable_curr"`

	TopMoves []IssueMove  `json:"top_moves,omitempty"`
	Trend    []TrendPoint `json:"trend"` // oldest → newest, at most pulseTrendCap points
}

// IssueMove is one issue's net movement between the two latest crawls.
type IssueMove struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Net      int    `json:"net"`
}

// TrendPoint is one comparable crawl reduced to sparkline numbers.
type TrendPoint struct {
	CrawlID       string `json:"crawl_id"`
	Started       int64  `json:"started"`
	URLs          int    `json:"urls"`
	Issues        int    `json:"issues"`
	Warnings      int    `json:"warnings"`
	Opportunities int    `json:"opportunities"`
}

// pulseTrendCap bounds the per-member history walk (one crawl-DB open per
// point for its issue counts).
const pulseTrendCap = 12

func (a *ProjectApp) MemberPulse(id, domain string) (*MemberPulse, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	hist, err := s.SiteHistory(domain)
	if err != nil {
		return nil, err
	}
	var comp []project.SiteCrawl
	for _, c := range hist { // newest first
		if c.Comparable {
			comp = append(comp, c)
		}
	}
	if len(comp) > pulseTrendCap {
		comp = comp[:pulseTrendCap]
	}
	mp := &MemberPulse{Domain: domain}
	for i := len(comp) - 1; i >= 0; i-- { // oldest → newest
		mp.Trend = append(mp.Trend, trendPoint(a.storeDir, comp[i]))
	}
	if len(comp) < 2 {
		return mp, nil
	}
	prev, curr := comp[1], comp[0]
	p, err := a.app.CompareCrawls(prev.ID, curr.ID, false)
	if err != nil {
		return nil, err
	}
	mp.OK, mp.Cached = true, p.Cached
	mp.PrevID, mp.CurrID = prev.ID, curr.ID
	mp.PrevStarted, mp.CurrStarted = prev.Started.Unix(), curr.Started.Unix()
	mp.PagesPrev, mp.PagesCurr = p.PagesPrev, p.PagesCurr
	mp.NewPages, mp.RemovedPages = p.NewCount, p.MissingCount
	mp.StatusFlips, mp.IndexabilityFlips = p.StatusFlipCount, p.IndexFlipCount
	mp.ElementChanges = p.ElementChangeCount
	mp.IndexablePrev, mp.IndexableCurr = p.IndexablePrev, p.IndexableCurr
	moves := make([]IssueMove, 0, len(p.IssueDeltas))
	for _, d := range p.IssueDeltas {
		mp.IssuesAppeared += d.AddedCount + d.NewCount
		mp.IssuesResolved += d.RemovedCount + d.MissingCount
		if net := d.CurrCount - d.PrevCount; net != 0 {
			moves = append(moves, IssueMove{Name: d.Name, Severity: d.Severity, Net: net})
		}
	}
	sort.SliceStable(moves, func(i, j int) bool { return abs(moves[i].Net) > abs(moves[j].Net) })
	if len(moves) > 3 {
		moves = moves[:3]
	}
	mp.TopMoves = moves
	return mp, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// trendPoint reduces one comparable crawl to its sparkline numbers, opening
// its DB only for the issue counts (same per-crawl read ListCrawls does).
func trendPoint(dir string, c project.SiteCrawl) TrendPoint {
	tp := TrendPoint{CrawlID: c.ID, Started: c.Started.Unix(), URLs: c.Total}
	if tp.URLs == 0 {
		tp.URLs = c.Crawled
	}
	st, err := store.OpenCrawl(dir, c.ID)
	if err != nil {
		return tp
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return tp
	}
	for issueID, n := range counts {
		def, ok := issues.Lookup(issueID)
		if !ok || n == 0 {
			continue
		}
		switch def.Severity {
		case issues.Issue:
			tp.Issues += n
		case issues.Warning:
			tp.Warnings += n
		case issues.Opportunity:
			tp.Opportunities += n
		}
	}
	return tp
}
