package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/robots"
	"github.com/agentberlin/bluesnake/internal/store"
)

// App is the Wails-bound service layer over the internal crawl engine.
type App struct {
	ctx      context.Context
	storeDir string
	mcp      *mcpManager    // localhost MCP server (settings toggle)
	tunnel   *tunnelManager // optional public HTTPS URL for the MCP server
	upd      *updateManager // self-update checker / installer

	mu      sync.Mutex
	session *crawlSession // at most one live crawl

	cacheMu    sync.Mutex
	pagesCache map[string]map[string]*crawler.PageRecord // crawlID -> pages
	issueCache map[string]map[string][]string            // crawlID -> url -> issue ids
	countCache map[string]map[string]int                 // crawlID -> tab -> row count
}

func NewApp() *App {
	a := &App{
		storeDir:   defaultStoreDir(),
		pagesCache: map[string]map[string]*crawler.PageRecord{},
		issueCache: map[string]map[string][]string{},
		countCache: map[string]map[string]int{},
	}
	a.mcp = newMCPManager(a)
	a.tunnel = newTunnelManager(a)
	a.upd = newUpdateManager(a)
	return a
}

func defaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bluesnake"
	}
	return filepath.Join(home, ".bluesnake")
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.mcp.initFromSettings()    // restore the MCP toggle, auto-starting the server
	a.tunnel.initFromSettings() // then the public-URL toggle (forwards to the MCP server)
}

func (a *App) shutdown(ctx context.Context) {
	a.tunnel.shutdown()
	a.mcp.shutdown()
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		s.stop("pause") // interrupted crawls resume cleanly; nothing is lost
		s.wait()
	}
}

func (a *App) invalidate(id string) {
	a.cacheMu.Lock()
	delete(a.pagesCache, id)
	delete(a.issueCache, id)
	delete(a.countCache, id)
	a.cacheMu.Unlock()
}

// ---------------------------------------------------------------------------
// crawl manager

type CrawlSummary struct {
	ID            string `json:"id"`
	Project       string `json:"project"`
	Seed          string `json:"seed"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	Started       string `json:"started"`
	Crawled       int    `json:"crawled"` // URLs fetched (got a response)
	Total         int    `json:"total"`   // URLs encountered (fetched + robots-blocked + errored)
	Issues        int    `json:"issues"`
	Warnings      int    `json:"warnings"`
	Opportunities int    `json:"opportunities"`
}

func (a *App) ListCrawls() ([]CrawlSummary, error) {
	infos, err := store.ListCrawls(a.storeDir)
	if err != nil {
		return nil, err
	}
	out := make([]CrawlSummary, 0, len(infos))
	for _, in := range infos {
		cs := CrawlSummary{
			ID: in.ID, Project: in.Project, Seed: in.Seed, Mode: in.Mode,
			Status: in.Status, Crawled: in.Crawled, Total: in.Total,
		}
		if !in.Started.IsZero() {
			cs.Started = in.Started.Format("2006-01-02 15:04")
		}
		if in.Status != store.StatusRunning {
			if st, err := store.OpenCrawl(a.storeDir, in.ID); err == nil {
				// crawls finished before `total` existed have it at 0; backfill
				// the encountered count from the pages table and persist it so
				// the registry-backed surfaces (CLI, MCP) pick it up too.
				if cs.Total == 0 {
					if n, err := st.PageCount(); err == nil && n > 0 {
						cs.Total = n
						_ = store.SetTotal(a.storeDir, in.ID, n)
					}
				}
				if counts, err := st.IssueCounts(); err == nil {
					for id, n := range counts {
						def, ok := issues.Lookup(id)
						if !ok || n == 0 {
							continue
						}
						switch def.Severity {
						case issues.Issue:
							cs.Issues += n
						case issues.Warning:
							cs.Warnings += n
						case issues.Opportunity:
							cs.Opportunities += n
						}
					}
				}
				st.Close()
			}
		}
		out = append(out, cs)
	}
	// newest first
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started > out[j].Started })
	return out, nil
}

func (a *App) DeleteCrawl(id string) error {
	a.invalidate(id)
	return store.DeleteCrawl(a.storeDir, id)
}

// ---------------------------------------------------------------------------
// starting / controlling crawls

type StartRequest struct {
	Mode       string   `json:"mode"` // spider | list
	URL        string   `json:"url"`
	ListURLs   []string `json:"listUrls"`
	SitemapURL string   `json:"sitemapUrl"`
	Project    string   `json:"project"`
	Profile    string   `json:"profile"`
	Threads    int      `json:"threads"`
	Rate       float64  `json:"rate"`     // URLs/sec, 0 = unlimited
	MaxDepth   int      `json:"maxDepth"` // -1 = unlimited
	Rendering  string   `json:"rendering"`
}

func (a *App) StartCrawl(req StartRequest) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil && !a.session.finished() {
		return "", fmt.Errorf("a crawl is already running")
	}

	cfg, err := a.loadProfileConfig(req.Profile)
	if err != nil {
		return "", err
	}
	if req.Threads > 0 {
		cfg.Speed.MaxThreads = req.Threads
	}
	cfg.Speed.MaxURLsPerSec = req.Rate
	if req.MaxDepth != 0 {
		cfg.Limits.MaxDepth = req.MaxDepth
	}
	if req.Rendering != "" {
		cfg.Rendering.Mode = req.Rendering
	}

	var seeds []string
	mode := "spider"
	switch req.Mode {
	case "list":
		mode = "list"
		cfg.Mode = "list"
		cfg.Limits.MaxDepth = cfg.ListMode.CrawlDepth
		if !cfg.ListMode.RespectRobots {
			cfg.Robots.Mode = "ignore"
		}
		if req.SitemapURL != "" {
			seeds, err = crawler.FetchSitemapURLs(a.ctx, cfg, req.SitemapURL)
			if err != nil {
				return "", err
			}
		} else {
			seeds = req.ListURLs
		}
		if len(seeds) == 0 {
			return "", fmt.Errorf("no URLs to audit")
		}
	default:
		if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
			return "", fmt.Errorf("enter a valid URL including http:// or https://")
		}
		seeds = []string{req.URL}
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	project := req.Project
	if project == "" {
		if u, err := url.Parse(seeds[0]); err == nil {
			project = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}

	st, err := store.CreateCrawl(a.storeDir, project, seeds[0], mode, cfg)
	if err != nil {
		return "", err
	}
	s, err := newCrawlSession(a, st, cfg, seeds, nil, nil)
	if err != nil {
		st.Close()
		return "", err
	}
	a.session = s
	go s.run()
	return st.ID, nil
}

func (a *App) ResumeCrawl(id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil && !a.session.finished() {
		return "", fmt.Errorf("a crawl is already running")
	}
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return "", err
	}
	cfgYAML, err := st.Meta("config")
	if err != nil {
		st.Close()
		return "", err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		st.Close()
		return "", err
	}
	seed, err := st.Meta("seed")
	if err != nil || seed == "" {
		st.Close()
		return "", fmt.Errorf("crawl %s has no stored seed", id)
	}
	processed, err := st.ProcessedURLs()
	if err != nil {
		st.Close()
		return "", err
	}
	pending, err := st.PendingFrontier()
	if err != nil {
		st.Close()
		return "", err
	}
	a.invalidate(id)
	s, err := newCrawlSession(a, st, cfg, []string{seed}, processed, pending)
	if err != nil {
		st.Close()
		return "", err
	}
	a.session = s
	go s.run()
	return id, nil
}

// PauseCrawl interrupts the live crawl, leaving it resumable.
func (a *App) PauseCrawl() {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		s.stop("pause")
	}
}

// StopCrawl ends the live crawl and finalises it as completed (analysis runs
// on what was crawled so far).
func (a *App) StopCrawl() {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		s.stop("stop")
	}
}

// ActiveProgress lets the progress view rehydrate after a reload; nil when no
// crawl is live.
func (a *App) ActiveProgress() *ProgressSnapshot {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s == nil || s.finished() {
		return nil
	}
	snap := s.snapshot()
	return &snap
}

// ---------------------------------------------------------------------------
// robots tester

type RobotsVerdict struct {
	URL     string `json:"url"`
	Allowed bool   `json:"allowed"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
}

func (a *App) TestRobots(robotsTxt, token string, urls []string) []RobotsVerdict {
	f := robots.Parse([]byte(robotsTxt))
	out := make([]RobotsVerdict, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		v := f.Verdict(token, u)
		rv := RobotsVerdict{URL: u, Allowed: v.Allowed}
		if v.Rule != nil {
			rv.Line = v.Rule.Line
			rv.Rule = v.Rule.Raw
		}
		out = append(out, rv)
	}
	return out
}

// FetchRobots downloads the live robots.txt for the host of the given URL.
func (a *App) FetchRobots(site string) (string, error) {
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("enter a full URL, e.g. https://example.com")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u.Scheme + "://" + u.Host + "/robots.txt")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("robots.txt returned HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 512*1024)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n]), nil
}

// ---------------------------------------------------------------------------
// compare

func (a *App) CompareCrawls(prevID, currID string) (*compare.Result, error) {
	prev, err := a.compareInput(prevID)
	if err != nil {
		return nil, err
	}
	curr, err := a.compareInput(currID)
	if err != nil {
		return nil, err
	}
	st, err := store.OpenCrawl(a.storeDir, currID)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return nil, err
	}
	return compare.Run(prev, curr, cfg)
}

func (a *App) compareInput(id string) (compare.Input, error) {
	pages, err := a.loadPages(id)
	if err != nil {
		return compare.Input{}, err
	}
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return compare.Input{}, err
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return compare.Input{}, err
	}
	iss := map[string][]string{}
	for issueID, n := range counts {
		if n == 0 {
			continue
		}
		urls, err := st.IssueURLs(issueID)
		if err != nil {
			continue
		}
		iss[issueID] = urls
	}
	return compare.Input{Pages: pages, Issues: iss}, nil
}

// ---------------------------------------------------------------------------
// analysis

func (a *App) Reanalyze(id string) error {
	st, err := store.OpenCrawl(a.storeDir, id)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := reanalyze(st); err != nil {
		return err
	}
	a.invalidate(id)
	return nil
}

func reanalyze(st *store.Crawl) error {
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	if err := st.SaveIssues(issues.Evaluate(pages, cfg)); err != nil {
		return err
	}
	sitemaps, err := st.SitemapIndex()
	if err != nil {
		return err
	}
	return st.SaveAnalysis(analyze.Run(pages, sitemaps, cfg))
}
