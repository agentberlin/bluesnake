package main

import (
	"errors"
	"fmt"
	"io"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newCrawlCmd() *cobra.Command {
	var (
		cfgFile   string
		storeDir  string
		sets      []string
		threads   int
		depth     int
		rate      float64
		maxURLs   int
		include   []string
		exclude   []string
		userAgent string
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "crawl <url>",
		Short: "Crawl a site in spider mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			var err error
			if cfgFile != "" {
				if cfg, err = config.LoadFile(cfgFile); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}
			for _, s := range sets {
				if err := cfg.Set(s); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}
			// shorthand flags override file and --set values
			if cmd.Flags().Changed("threads") {
				cfg.Speed.MaxThreads = threads
			}
			if cmd.Flags().Changed("depth") {
				cfg.Limits.MaxDepth = depth
			}
			if cmd.Flags().Changed("rate") {
				cfg.Speed.MaxURLsPerSec = rate
			}
			if cmd.Flags().Changed("max-urls") {
				cfg.Limits.MaxURLs = maxURLs
			}
			if cmd.Flags().Changed("user-agent") {
				cfg.HTTP.UserAgent = userAgent
			}
			cfg.Scope.Include = append(cfg.Scope.Include, include...)
			cfg.Scope.Exclude = append(cfg.Scope.Exclude, exclude...)
			if err := cfg.Validate(); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}

			// The crawl runs through the same queue wiring every surface uses: an
			// in-process dispatcher drains a single job through the shared executor.
			// The CLI's file/flag config travels as a frozen ConfigYAML spec, and a
			// cliObserver tallies the live stream for the summary. Ctrl-C cancels the
			// signal context, which the executor turns into a resumable interrupt.
			cfgYAML, err := yaml.Marshal(cfg)
			if err != nil {
				return exitErr{1, err}
			}
			spec := queue.JobSpec{URL: args[0], ConfigYAML: string(cfgYAML)}

			obs := &cliObserver{done: make(chan struct{})}
			disp := queue.New(queue.NewMemStore(), runner.New(storeDir, obs))

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := disp.Start(ctx); err != nil {
				return exitErr{1, err}
			}
			if _, err := disp.Enqueue(spec, "manual", "", args[0]); err != nil {
				return exitErr{1, err}
			}
			<-obs.done
			disp.Shutdown()

			out := obs.outcome()
			if out.Err != nil && out.Status != store.StatusInterrupted && out.CrawlID == "" {
				// the crawl never started (bad seed, sitemap fetch, ...)
				fmt.Fprintln(cmd.ErrOrStderr(), out.Err)
				return exitErr{1, out.Err}
			}
			if !quiet {
				obs.tally().print(cmd.OutOrStdout(), out.Crawled, out.Total, time.Duration(out.DurationSec)*time.Second)
				fmt.Fprintf(cmd.OutOrStdout(), "Crawl ID: %s\n", out.CrawlID)
			}
			if out.Status == store.StatusInterrupted {
				fmt.Fprintf(cmd.ErrOrStderr(), "crawl interrupted — resume with: bluesnake resume %s --store-dir %s\n", out.CrawlID, storeDir)
				return exitErr{3, errors.New("interrupted")}
			}
			if out.Err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "finalize:", out.Err)
			} else if !quiet {
				printAnalysis(cmd, out.CrawlID, finalize.Outcome{
					Analyzed: out.Analyzed, Chains: out.Chains, NearDups: out.NearDups,
					IssueTotal: out.IssueTotal, IssueChecks: out.IssueChecks,
				})
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (YAML)")
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "dotted-path config override (key.path=value), repeatable")
	cmd.Flags().IntVar(&threads, "threads", 0, "max concurrent threads (speed.max_threads)")
	cmd.Flags().IntVar(&depth, "depth", 0, "max crawl depth (limits.max_depth)")
	cmd.Flags().Float64Var(&rate, "rate", 0, "max URLs per second (speed.max_urls_per_sec)")
	cmd.Flags().IntVar(&maxURLs, "max-urls", 0, "max URLs to crawl (limits.max_urls)")
	cmd.Flags().StringArrayVar(&include, "include", nil, "include pattern (scope.include), repeatable")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "exclude pattern (scope.exclude), repeatable")
	cmd.Flags().StringVar(&userAgent, "user-agent", "", "HTTP user-agent (http.user_agent)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress the summary")
	return cmd
}

// cliObserver implements runner.Observer for the one-shot CLI crawl: it tallies
// the page stream for the summary, captures the crawl id and the terminal
// outcome, and signals done when the crawl ends (or fails to start).
type cliObserver struct {
	done chan struct{}

	mu  sync.Mutex
	t   crawlTally
	out runner.Outcome
}

func (o *cliObserver) OnStart(crawlID, seed string) {
	o.mu.Lock()
	o.out.CrawlID = crawlID
	o.mu.Unlock()
}

func (o *cliObserver) OnPage(rec *crawler.PageRecord) {
	o.mu.Lock()
	o.t.add(rec)
	o.mu.Unlock()
}

func (o *cliObserver) OnDone(out runner.Outcome) {
	o.mu.Lock()
	id := o.out.CrawlID
	o.out = out
	if o.out.CrawlID == "" {
		o.out.CrawlID = id
	}
	o.mu.Unlock()
	close(o.done)
}

func (o *cliObserver) outcome() runner.Outcome {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out
}

func (o *cliObserver) tally() crawlTally {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.t
}

// crawlTally is the per-status/scope breakdown the CLI summary prints. The
// pages-based printSummary (used by resume/list) and the streaming cliObserver
// both feed it, so every surface prints byte-identical summaries.
type crawlTally struct {
	s2, s3, s4, s5          int
	blocked, errs           int
	indexable, nonIndexable int
	internal, external      int
}

func (t *crawlTally) add(rec *crawler.PageRecord) {
	switch rec.Scope {
	case "internal":
		t.internal++
	case "external":
		t.external++
	}
	switch rec.State {
	case crawler.StateBlockedRobots:
		t.blocked++
		return
	case crawler.StateError:
		t.errs++
		return
	}
	switch {
	case rec.StatusCode >= 500:
		t.s5++
	case rec.StatusCode >= 400:
		t.s4++
	case rec.StatusCode >= 300:
		t.s3++
	case rec.StatusCode >= 200:
		t.s2++
	}
	if rec.Indexable {
		t.indexable++
	} else {
		t.nonIndexable++
	}
}

func (t crawlTally) print(out io.Writer, crawled, total int, dur time.Duration) {
	fmt.Fprintf(out, "Found %d URLs (%d internal, %d external) — %d crawled in %s\n",
		total, t.internal, t.external, crawled, dur.Round(dur/100+1))
	fmt.Fprintf(out, "  2xx: %d  3xx: %d  4xx: %d  5xx: %d  blocked: %d  no-response: %d\n",
		t.s2, t.s3, t.s4, t.s5, t.blocked, t.errs)
	fmt.Fprintf(out, "  indexable: %d  non-indexable: %d\n", t.indexable, t.nonIndexable)
}

// printSummary renders the post-crawl tally from a stored page set (used by the
// resume/list paths, which load the full graph). crawled/total are the
// authoritative full-graph counts from finalize's Outcome.
func printSummary(cmd *cobra.Command, pages map[string]*crawler.PageRecord, crawled, total int, dur time.Duration) {
	var t crawlTally
	for _, p := range pages {
		t.add(p)
	}
	t.print(cmd.OutOrStdout(), crawled, total, dur)
}
