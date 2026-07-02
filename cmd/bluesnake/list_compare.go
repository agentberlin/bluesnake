package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/internal/compare"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/export"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var urlToken = regexp.MustCompile(`https?://\S+`)

func newListCmd() *cobra.Command {
	var (
		cfgFile, storeDir, sitemapURL string
		sets                          []string
		followRedirects               bool
		quiet                         bool
	)
	cmd := &cobra.Command{
		Use:   "list [<file>|-]",
		Short: "Audit a list of URLs (file, stdin, or --sitemap <url>)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			var err error
			if cfgFile != "" {
				if cfg, err = config.LoadFile(cfgFile); err != nil {
					return exitErr{2, err}
				}
			}
			for _, s := range sets {
				if err := cfg.Set(s); err != nil {
					return exitErr{2, err}
				}
			}
			// list-mode defaults (Screaming Frog semantics)
			cfg.Mode = "list"
			cfg.Limits.MaxDepth = cfg.ListMode.CrawlDepth
			if !cfg.ListMode.RespectRobots {
				cfg.Robots.Mode = "ignore"
			}
			if followRedirects {
				cfg.Advanced.AlwaysFollowRedirects = true
			}
			if err := cfg.Validate(); err != nil {
				return exitErr{2, err}
			}

			// The seed source resolves here for file/stdin input; a --sitemap is
			// fetched by the executor when the job runs (fresh at run time), the
			// same deferral every other surface gets.
			spec := queue.JobSpec{Mode: "list"}
			switch {
			case sitemapURL != "":
				spec.SitemapURL = sitemapURL
			case len(args) == 1:
				var data []byte
				if args[0] == "-" {
					data, err = io.ReadAll(cmd.InOrStdin())
				} else {
					data, err = os.ReadFile(args[0])
				}
				if err != nil {
					return exitErr{2, err}
				}
				seeds := urlToken.FindAllString(string(data), -1)
				if len(seeds) == 0 {
					return exitErr{2, fmt.Errorf("no http(s):// URLs found in the input")}
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "list mode: %d URLs\n", len(seeds))
				spec.URLs = seeds
			default:
				return exitErr{2, fmt.Errorf("provide a URL list file, '-' for stdin, or --sitemap <url>")}
			}
			cfgYAML, err := yaml.Marshal(cfg)
			if err != nil {
				return exitErr{1, err}
			}
			spec.ConfigYAML = string(cfgYAML)

			// One crawl path: the list audit runs through the same queue wiring as
			// crawl/resume, so limiter/finalize behaviour cannot drift per-command.
			obs := &cliObserver{done: make(chan struct{})}
			disp := queue.New(queue.NewMemStore(), runner.New(storeDir, obs))
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := disp.Start(ctx); err != nil {
				return exitErr{1, err}
			}
			if _, err := disp.Enqueue(spec, "manual", "", "list"); err != nil {
				return exitErr{1, err}
			}
			<-obs.done
			disp.Shutdown()

			out := obs.outcome()
			if out.Err != nil && out.Status != store.StatusInterrupted && out.CrawlID == "" {
				// the crawl never started (sitemap fetch failure, bad seed, ...)
				fmt.Fprintln(cmd.ErrOrStderr(), out.Err)
				return exitErr{1, out.Err}
			}
			if !quiet {
				obs.tally().print(cmd.OutOrStdout(), out.Crawled, out.Total, time.Duration(out.DurationSec)*time.Second)
				fmt.Fprintf(cmd.OutOrStdout(), "Crawl ID: %s\n", out.CrawlID)
			}
			if out.Status == store.StatusInterrupted {
				fmt.Fprintf(cmd.ErrOrStderr(), "crawl interrupted — resume with: bluesnake resume %s --store-dir %s\n", out.CrawlID, storeDir)
				return exitErr{3, fmt.Errorf("interrupted")}
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
	cmd.Flags().StringArrayVar(&sets, "set", nil, "config override, repeatable")
	cmd.Flags().StringVar(&sitemapURL, "sitemap", "", "download a sitemap (or index) as the URL source")
	cmd.Flags().BoolVar(&followRedirects, "follow-redirects", false, "follow redirect chains to their final target regardless of depth")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress the summary")
	return cmd
}

func newCompareCmd() *cobra.Command {
	var storeDir, format, outPath string
	var sets []string
	cmd := &cobra.Command{
		Use:   "compare <previous-id> <current-id>",
		Short: "Compare two stored crawls: issue deltas and element changes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			for _, s := range sets {
				if err := cfg.Set(s); err != nil {
					return exitErr{2, err}
				}
			}
			if err := cfg.Validate(); err != nil {
				return exitErr{2, err}
			}
			load := func(id string) (compare.Input, error) {
				st, err := store.OpenCrawl(storeDir, id)
				if err != nil {
					return compare.Input{}, err
				}
				defer st.Close()
				pages, err := st.LoadPages()
				if err != nil {
					return compare.Input{}, err
				}
				counts, err := st.IssueCounts()
				if err != nil {
					return compare.Input{}, err
				}
				issueURLs := map[string][]string{}
				for id := range counts {
					urls, err := st.IssueURLs(id)
					if err != nil {
						return compare.Input{}, err
					}
					issueURLs[id] = urls
				}
				return compare.Input{Pages: pages, Issues: issueURLs}, nil
			}
			prev, err := load(args[0])
			if err != nil {
				return exitErr{2, err}
			}
			curr, err := load(args[1])
			if err != nil {
				return exitErr{2, err}
			}
			result, err := compare.Run(prev, curr, cfg)
			if err != nil {
				return exitErr{2, err}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Pages: %d -> %d (%d new, %d missing)\n",
				result.PagesPrevious, result.PagesCurrent, len(result.NewPages), len(result.MissingPages))
			for _, d := range result.Deltas {
				fmt.Fprintf(out, "%s: +%d added, +%d new, -%d removed, -%d missing\n",
					d.IssueID, len(d.Added), len(d.New), len(d.Removed), len(d.Missing))
			}
			for _, c := range result.Changes {
				fmt.Fprintf(out, "changed %s %s: %q -> %q\n", c.Element, c.URL, c.Previous, c.Current)
			}
			if outPath != "" {
				d := compareDataset(result)
				if err := writeDataset(cmd, d, format, outPath); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote %s\n", outPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&format, "format", "csv", "csv | json | jsonl | xlsx (with --output)")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write the full comparison to a file")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "config override (compare.url_mapping etc.), repeatable")
	return cmd
}

func compareDataset(r *compare.Result) *export.Dataset {
	d := &export.Dataset{Name: "comparison",
		Header: []string{"kind", "issue_or_element", "url", "previous", "current"}}
	for _, delta := range r.Deltas {
		for bucket, urls := range map[string][]string{
			"added": delta.Added, "new": delta.New, "removed": delta.Removed, "missing": delta.Missing,
		} {
			for _, u := range urls {
				d.Rows = append(d.Rows, []string{"issue_" + bucket, delta.IssueID, u, "", ""})
			}
		}
	}
	for _, c := range r.Changes {
		d.Rows = append(d.Rows, []string{"change", c.Element, c.URL, c.Previous, c.Current})
	}
	return d
}
