package main

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/store"
	"github.com/spf13/cobra"
)

func newCrawlCmd() *cobra.Command {
	var (
		cfgFile   string
		storeDir  string
		project   string
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

			st, err := store.CreateCrawl(storeDir, project, args[0], "spider", cfg)
			if err != nil {
				return exitErr{1, err}
			}
			defer st.Close()

			c, err := crawler.New(cfg, crawler.WithSink(st))
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			defer c.Close()

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			res, err := c.Run(ctx, args[0])
			if err != nil {
				return exitErr{1, err}
			}
			finishCrawl(cmd, st, storeDir, res)
			if !quiet {
				printSummary(cmd, res)
				fmt.Fprintf(cmd.OutOrStdout(), "Crawl ID: %s\n", st.ID)
			}
			if res.Interrupted {
				return exitErr{3, fmt.Errorf("interrupted")}
			}
			if cfg.Analysis.Auto {
				if err := runAnalysis(cmd, st, cfg, quiet); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "analysis:", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (YAML)")
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&project, "project", "", "project name for the stored crawl")
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

func printSummary(cmd *cobra.Command, res *crawler.Result) {
	var s2, s3, s4, s5, blocked, errs, indexable, nonIndexable, internal, external int
	for _, p := range res.Pages {
		switch p.Scope {
		case "internal":
			internal++
		case "external":
			external++
		}
		switch p.State {
		case crawler.StateBlockedRobots:
			blocked++
			continue
		case crawler.StateError:
			errs++
			continue
		}
		switch {
		case p.StatusCode >= 500:
			s5++
		case p.StatusCode >= 400:
			s4++
		case p.StatusCode >= 300:
			s3++
		case p.StatusCode >= 200:
			s2++
		}
		if p.Indexable {
			indexable++
		} else {
			nonIndexable++
		}
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Crawled %d URLs (%d internal, %d external) in %s\n",
		res.Crawled, internal, external, res.Duration.Round(res.Duration/100+1))
	fmt.Fprintf(out, "  2xx: %d  3xx: %d  4xx: %d  5xx: %d  blocked: %d  no-response: %d\n",
		s2, s3, s4, s5, blocked, errs)
	fmt.Fprintf(out, "  indexable: %d  non-indexable: %d\n", indexable, nonIndexable)
}
