package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// Runner is the CLI's Backend: a self-contained crawl session manager
// mirroring the desktop app's (one live crawl, pause keeps it resumable,
// stop finalises early, auto-analysis on completion).
type Runner struct {
	storeDir string

	mu   sync.Mutex
	sess *runnerSession
}

func NewRunner(storeDir string) *Runner { return &Runner{storeDir: storeDir} }

func (r *Runner) StoreDir() string { return r.storeDir }

func (r *Runner) StartCrawl(ctx context.Context, req StartRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sess != nil && !r.sess.finished() {
		return "", fmt.Errorf("a crawl is already running (crawl %s) — pause_crawl or stop_crawl first", r.sess.st.ID)
	}
	cfg, err := BuildConfig(r.storeDir, req)
	if err != nil {
		return "", err
	}
	seeds, mode, err := ResolveSeeds(ctx, cfg, req)
	if err != nil {
		return "", err
	}
	st, err := store.CreateCrawl(r.storeDir, DefaultProject(req.Project, seeds), seeds[0], mode, cfg)
	if err != nil {
		return "", err
	}
	s, err := newRunnerSession(r.storeDir, st, cfg, seeds, nil, nil)
	if err != nil {
		st.Close()
		return "", err
	}
	r.sess = s
	go s.run()
	return st.ID, nil
}

func (r *Runner) ResumeCrawl(id string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sess != nil && !r.sess.finished() {
		return "", fmt.Errorf("a crawl is already running (crawl %s)", r.sess.st.ID)
	}
	st, err := store.OpenCrawl(r.storeDir, id)
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
	s, err := newRunnerSession(r.storeDir, st, cfg, []string{seed}, processed, pending)
	if err != nil {
		st.Close()
		return "", err
	}
	r.sess = s
	go s.run()
	return id, nil
}

func (r *Runner) PauseCrawl() error { return r.signal("pause") }
func (r *Runner) StopCrawl() error  { return r.signal("stop") }

func (r *Runner) signal(mode string) error {
	r.mu.Lock()
	s := r.sess
	r.mu.Unlock()
	if s == nil || s.finished() {
		return fmt.Errorf("no crawl is running")
	}
	s.stop(mode)
	return nil
}

func (r *Runner) Progress() *Progress {
	r.mu.Lock()
	s := r.sess
	r.mu.Unlock()
	if s == nil || s.finished() {
		return nil
	}
	p := s.snapshot()
	return &p
}

// Shutdown pauses any live crawl (leaving it resumable) and waits for the
// session to flush — called when the MCP transport closes.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	s := r.sess
	r.mu.Unlock()
	if s != nil && !s.finished() {
		s.stop("pause")
		s.wait()
	}
}

// ---------------------------------------------------------------------------
// runnerSession mirrors the desktop crawlSession (counters fed by a teeing
// sink, pause vs stop, finalisation with inlinks/status/auto-analysis) minus
// the UI event stream.

type runnerSession struct {
	storeDir string
	st       *store.Crawl
	cfg      *config.Config
	c        *crawler.Crawler
	seeds    []string
	cancel   context.CancelFunc
	ctx      context.Context
	doneCh   chan struct{}

	mu         sync.Mutex
	stopMode   string // "" | "pause" | "stop"
	done       bool
	crawled    int
	discovered int
	s2, s3     int
	s4, s5     int
	blocked    int
	noresp     int
	indexable  int
	recent     []time.Time
	started    time.Time
}

func newRunnerSession(storeDir string, st *store.Crawl, cfg *config.Config, seeds []string, processed []string, pending []frontier.Item) (*runnerSession, error) {
	s := &runnerSession{
		storeDir: storeDir, st: st, cfg: cfg, seeds: seeds,
		started: time.Now(),
		doneCh:  make(chan struct{}),
		// resumed crawls start from what is already on disk
		crawled:    len(processed),
		discovered: len(processed) + len(pending),
	}
	opts := []crawler.Option{crawler.WithSink(&runnerSink{inner: st, s: s})}
	if processed != nil || pending != nil {
		opts = append(opts, crawler.WithResume(processed, pending))
	}
	c, err := crawler.New(cfg, opts...)
	if err != nil {
		return nil, err
	}
	s.c = c
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

func (s *runnerSession) run() {
	defer close(s.doneCh)
	defer s.c.Close()
	defer s.st.Close()

	res, _ := s.c.Run(s.ctx, s.seeds...)

	s.mu.Lock()
	s.done = true
	mode := s.stopMode
	s.mu.Unlock()

	if res == nil {
		return
	}
	_ = s.st.UpdateInlinks(res.Pages)
	status := store.StatusCompleted
	// Pause keeps the crawl resumable; stop finalises early as completed.
	if res.Interrupted && mode != "stop" {
		status = store.StatusInterrupted
	}
	_ = store.SetStatus(s.storeDir, s.st.ID, status, res.Crawled)
	if status == store.StatusCompleted && s.cfg.Analysis.Auto {
		_ = reanalyze(s.st, s.cfg)
	}
}

// reanalyze re-runs issue evaluation and the graph analyses (same phases as
// the CLI's runAnalysis / the desktop's reanalyze).
func reanalyze(st *store.Crawl, cfg *config.Config) error {
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

func (s *runnerSession) stop(mode string) {
	s.mu.Lock()
	if s.stopMode == "" {
		s.stopMode = mode
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *runnerSession) wait() { <-s.doneCh }

func (s *runnerSession) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *runnerSession) snapshot() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	// live rate over a 4s sliding window
	cutoff := time.Now().Add(-4 * time.Second)
	i := 0
	for i < len(s.recent) && s.recent[i].Before(cutoff) {
		i++
	}
	s.recent = s.recent[i:]

	queue := s.discovered - s.crawled
	if queue < 0 {
		queue = 0
	}
	return Progress{
		CrawlID: s.st.ID, Seed: s.seeds[0], State: "running",
		Crawled: s.crawled, Discovered: s.discovered, Queue: queue,
		S2xx: s.s2, S3xx: s.s3, S4xx: s.s4, S5xx: s.s5,
		Blocked: s.blocked, NoResponse: s.noresp, Indexable: s.indexable,
		RatePerSec: float64(len(s.recent)) / 4.0,
		ElapsedSec: int(time.Since(s.started).Seconds()),
	}
}

func (s *runnerSession) onPage(rec *crawler.PageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crawled++
	s.recent = append(s.recent, time.Now())
	switch rec.State {
	case crawler.StateBlockedRobots:
		s.blocked++
	case crawler.StateError:
		s.noresp++
	default:
		switch {
		case rec.StatusCode >= 500:
			s.s5++
		case rec.StatusCode >= 400:
			s.s4++
		case rec.StatusCode >= 300:
			s.s3++
		case rec.StatusCode >= 200:
			s.s2++
		}
		if rec.Indexable {
			s.indexable++
		}
	}
}

// runnerSink tees the crawl stream: persistence first, then counters.
type runnerSink struct {
	inner *store.Crawl
	s     *runnerSession
}

func (t *runnerSink) Page(rec *crawler.PageRecord) error {
	if err := t.inner.Page(rec); err != nil {
		return err
	}
	t.s.onPage(rec)
	return nil
}

func (t *runnerSink) FrontierAdd(it frontier.Item) error {
	if err := t.inner.FrontierAdd(it); err != nil {
		return err
	}
	t.s.mu.Lock()
	t.s.discovered++
	t.s.mu.Unlock()
	return nil
}

func (t *runnerSink) FrontierDone(url string) error { return t.inner.FrontierDone(url) }

// Blob and Archive forward the optional sink extensions (stored HTML,
// screenshots, WARC) — the engine reaches them by type assertion.
func (t *runnerSink) Blob(url, kind string, data []byte) error {
	return t.inner.Blob(url, kind, data)
}

func (t *runnerSink) Archive(url string, res *fetch.Result) error {
	return t.inner.Archive(url, res)
}
