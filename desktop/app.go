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

	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/robots"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound service layer over the internal crawl engine.
type App struct {
	ctx      context.Context
	storeDir string
	mcp      *mcpManager    // localhost MCP server (settings toggle)
	tunnel   *tunnelManager // optional public HTTPS URL for the MCP server
	upd      *updateManager // self-update checker / installer

	// The crawl queue: every start (hand-driven or MCP-driven) enqueues a job;
	// the single dispatcher drains it through the executor, running up to
	// queueW crawls at once (speed.max_concurrent_crawls from the default
	// profile, read at construction — restart to apply; default 1). The queue
	// is persisted in the registry DB, so it survives restarts.
	mu     sync.Mutex
	exec   *runner.Executor
	disp   *queue.Dispatcher
	obs    *uiObserver
	queueW int

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
	a.ensureQueue()
	// Drain the persistent queue: this reconciles any job left running by a
	// previous crash (-> interrupted, the partial crawl stays resumable) and
	// then runs queued jobs, up to queueW at a time. A Start failure (registry error
	// during reconcile) leaves the dispatcher retryable; swallowing it meant
	// jobs were accepted forever and never drained, silently (#74 N4) — so
	// surface it and retry with backoff until the registry recovers.
	if err := a.disp.Start(ctx); err != nil {
		runtime.LogErrorf(ctx, "queue: start failed (will retry): %v", err)
		runtime.EventsEmit(ctx, "queue:error", err.Error())
		go a.retryQueueStart(ctx)
	}
	a.mcp.initFromSettings()    // restore the MCP toggle, auto-starting the server
	a.tunnel.initFromSettings() // then the public-URL toggle (forwards to the MCP server)
}

// retryQueueStart keeps trying to start the queue dispatcher until it succeeds
// or the app shuts down. Start is idempotent and, after #74 N4, retryable.
func (a *App) retryQueueStart(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := a.disp.Start(ctx); err == nil {
				runtime.EventsEmit(ctx, "queue:error", "")
				return
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.tunnel.shutdown()
	a.mcp.shutdown()
	a.mu.Lock()
	d := a.disp
	a.mu.Unlock()
	if d != nil {
		d.Shutdown() // pauses any in-flight crawl (resumable) and stops the loop
	}
}

// ensureQueue lazily builds the executor + dispatcher over the current store
// dir. Construction is deferred (not done in NewApp) because tests set storeDir
// after construction; startup wires the same path before draining.
func (a *App) ensureQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disp != nil {
		return
	}
	// speed.max_concurrent_crawls (default profile, read once here — restart to
	// apply, as the Settings copy says) drives how many crawls the dispatcher
	// runs at once. When parallel, ONE shared limiter bounds total fetches /
	// finalize passes / Chrome renders across all of them (H1/P17); with the
	// default of 1 the limiter is nil and the executor's per-crawl fallback
	// keeps today's single-crawl behaviour byte-identical. An unreadable
	// default profile fails safe to single-crawl — the same profile backs every
	// start job, so the real error surfaces on the first crawl start.
	w, lim, err := runner.ProcessWiring(a.storeDir)
	if err != nil && a.ctx != nil {
		runtime.LogWarningf(a.ctx, "queue: default profile unreadable, running single-crawl: %v", err)
	}
	a.queueW = w
	a.obs = &uiObserver{app: a, emit: func(event string, data ...interface{}) {
		runtime.EventsEmit(a.ctx, event, data...)
	}}
	var opts []runner.Option
	if lim != nil {
		opts = append(opts, runner.WithLimiter(lim))
	}
	a.exec = runner.New(a.storeDir, a.obs, opts...)
	a.disp = queue.New(queue.NewSQLiteStore(a.storeDir), a.exec, queue.WithConcurrency(w))
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
			ID: in.ID, Seed: in.Seed, Mode: in.Mode,
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
	Profile    string   `json:"profile"`
	Threads    int      `json:"threads"`
	Rate       float64  `json:"rate"`     // URLs/sec, 0 = unlimited
	MaxDepth   int      `json:"maxDepth"` // -1 = unlimited
	Rendering  string   `json:"rendering"`
}

// toSpec translates the desktop's start form into the neutral queue job spec:
// the per-field knobs (threads/rate/depth/rendering) become dotted-path config
// overrides, so the runner's BuildConfig is the single config-building path.
func (req StartRequest) toSpec() queue.JobSpec {
	cfg := map[string]any{
		"speed.max_urls_per_sec": req.Rate, // 0 = unlimited, set unconditionally
	}
	if req.Threads > 0 {
		cfg["speed.max_threads"] = req.Threads
	}
	if req.MaxDepth != 0 {
		cfg["limits.max_depth"] = req.MaxDepth
	}
	if req.Rendering != "" {
		cfg["rendering.mode"] = req.Rendering
	}
	spec := queue.JobSpec{Mode: req.Mode, Profile: req.Profile, Config: cfg}
	if req.Mode == "list" {
		spec.URLs = req.ListURLs
		spec.SitemapURL = req.SitemapURL
	} else {
		spec.URL = req.URL
	}
	return spec
}

func (req StartRequest) label() string {
	if req.URL != "" {
		return req.URL
	}
	if req.SitemapURL != "" {
		return req.SitemapURL
	}
	if len(req.ListURLs) > 0 {
		return req.ListURLs[0]
	}
	return "list crawl"
}

// StartCrawl validates the request and enqueues a crawl job, returning the job
// id. When the queue is idle the dispatcher starts it within a tick and the UI
// jumps to the live view on the crawl:started event; when a crawl is already
// running it queues behind it.
func (a *App) StartCrawl(req StartRequest) (string, error) {
	a.ensureQueue()
	spec := req.toSpec()
	if err := runner.ValidateSpec(a.storeDir, spec); err != nil {
		return "", err
	}
	j, err := a.disp.Enqueue(spec, "manual", "", req.label())
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

// ResumeCrawl enqueues a job that resumes an existing crawl.
func (a *App) ResumeCrawl(id string) (string, error) {
	a.ensureQueue()
	a.invalidate(id)
	j, err := a.disp.Enqueue(queue.JobSpec{ResumeID: id}, "manual", "", "resume "+id)
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

// PauseCrawl interrupts one live crawl by id, leaving it resumable; other
// running crawls are untouched. An empty id addresses the single running crawl
// (the pre-parallel call shape) and is a no-op when that is ambiguous.
func (a *App) PauseCrawl(crawlID string) {
	a.ensureQueue()
	if crawlID = a.resolveLive(crawlID); crawlID == "" {
		return
	}
	a.disp.PauseCrawl(crawlID)
}

// StopCrawl ends one live crawl by id and finalises it as completed (analysis
// runs on what was crawled so far); other running crawls are untouched.
func (a *App) StopCrawl(crawlID string) {
	a.ensureQueue()
	if crawlID = a.resolveLive(crawlID); crawlID == "" {
		return
	}
	a.disp.StopCrawl(crawlID)
}

// resolveLive maps an empty crawl id to the one running crawl; "" when idle or
// ambiguous (several running — the caller must address one).
func (a *App) resolveLive(crawlID string) string {
	if crawlID != "" {
		return crawlID
	}
	snaps := a.exec.Snapshots()
	if len(snaps) == 1 {
		return snaps[0].CrawlID
	}
	return ""
}

// ActiveProgress lets a progress view rehydrate after a reload: the snapshot
// for one crawl id, or — with an empty id — the oldest running crawl. Nil when
// nothing (or not that crawl) is live.
func (a *App) ActiveProgress(crawlID string) *ProgressSnapshot {
	a.mu.Lock()
	exec, obs := a.exec, a.obs
	a.mu.Unlock()
	if exec == nil {
		return nil
	}
	if crawlID != "" {
		snap, ok := exec.SnapshotCrawl(crawlID)
		if !ok {
			return nil
		}
		ps := obs.build(snap, "running")
		return &ps
	}
	snaps := exec.Snapshots()
	if len(snaps) == 0 {
		return nil
	}
	ps := obs.build(snaps[0], "running")
	return &ps
}

// RunningProgress returns a snapshot for every live crawl (oldest first), so
// the frontend can render one progress presentation per running crawl.
func (a *App) RunningProgress() []ProgressSnapshot {
	a.mu.Lock()
	exec, obs := a.exec, a.obs
	a.mu.Unlock()
	if exec == nil {
		return nil
	}
	snaps := exec.Snapshots()
	out := make([]ProgressSnapshot, len(snaps))
	for i, s := range snaps {
		out[i] = obs.build(s, "running")
	}
	return out
}

// ---------------------------------------------------------------------------
// queue management (desktop queue panel)

// QueueItem is one crawl-queue entry surfaced to the UI.
type QueueItem struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Source   string `json:"source"` // manual | project
	Label    string `json:"label"`
	CrawlID  string `json:"crawlId"`
	Error    string `json:"error,omitempty"`
	Enqueued string `json:"enqueued"`
}

// ListQueue returns every job in the queue (newest position last).
func (a *App) ListQueue() ([]QueueItem, error) {
	a.ensureQueue()
	jobs, err := a.disp.List()
	if err != nil {
		return nil, err
	}
	out := make([]QueueItem, 0, len(jobs))
	for _, j := range jobs {
		qi := QueueItem{
			ID: j.ID, Status: j.Status, Source: j.Source,
			Label: j.Label, CrawlID: j.CrawlID, Error: j.Error,
		}
		if !j.Enqueued.IsZero() {
			qi.Enqueued = j.Enqueued.Format("2006-01-02 15:04")
		}
		out = append(out, qi)
	}
	return out, nil
}

// EnqueueCrawl adds a job to the queue and returns its id. It is the entry point
// the removable project layer uses to drive "crawl all" through the same queue
// without the core App depending on the project package.
func (a *App) EnqueueCrawl(spec queue.JobSpec, source, projectID, label string) (string, error) {
	a.ensureQueue()
	j, err := a.disp.Enqueue(spec, source, projectID, label)
	if err != nil {
		return "", err
	}
	return j.ID, nil
}

// CancelJob drops a queued job, or stops the running one.
func (a *App) CancelJob(id string) error {
	a.ensureQueue()
	_, err := a.disp.Cancel(id)
	return err
}

// ClearJob removes a finished/canceled job from the queue list.
func (a *App) ClearJob(id string) error {
	a.ensureQueue()
	return store.DeleteJob(a.storeDir, id)
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
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	if _, err := finalize.Analyze(st, cfg); err != nil {
		return err
	}
	a.invalidate(id)
	return nil
}
