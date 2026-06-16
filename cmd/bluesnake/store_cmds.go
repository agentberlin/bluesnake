package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

func defaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bluesnake"
	}
	return filepath.Join(home, ".bluesnake")
}

func newCrawlsCmd() *cobra.Command {
	var storeDir string
	crawlsCmd := &cobra.Command{
		Use:   "crawls",
		Short: "Manage stored crawls",
	}
	crawlsCmd.PersistentFlags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")

	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List stored crawls",
		RunE: func(cmd *cobra.Command, args []string) error {
			infos, err := store.ListCrawls(storeDir)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPROJECT\tMODE\tSTATUS\tURLS\tCRAWLED\tSEED")
			for _, in := range infos {
				total := in.Total
				if total == 0 {
					total = in.Crawled
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
					in.ID, in.Project, in.Mode, in.Status, total, in.Crawled, in.Seed)
			}
			return w.Flush()
		},
	}

	rmCmd := &cobra.Command{
		Use:   "rm <crawl-id>...",
		Short: "Delete stored crawls",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if err := store.DeleteCrawl(storeDir, id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
			}
			return nil
		},
	}

	crawlsCmd.AddCommand(lsCmd, rmCmd)
	return crawlsCmd
}

func newResumeCmd() *cobra.Command {
	var storeDir string
	var sets []string
	var cfgFile string
	var force bool

	cmd := &cobra.Command{
		Use:   "resume <crawl-id>",
		Short: "Resume an interrupted crawl from its stored frontier",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// the config was frozen into the crawl at start; a different config
			// would change discovery semantics mid-crawl
			if (len(sets) > 0 || cfgFile != "") && !force {
				err := errors.New("resume uses the config stored with the crawl; pass --force to override it")
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
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
			if force {
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
				if err := cfg.Validate(); err != nil {
					return exitErr{2, err}
				}
			}

			seeds, err := st.Seeds()
			if err != nil {
				return err
			}
			if len(seeds) == 0 {
				return fmt.Errorf("crawl %s has no stored seed", args[0])
			}
			processed, err := st.ProcessedURLs()
			if err != nil {
				return err
			}
			pending, err := st.PendingFrontier()
			if err != nil {
				return err
			}

			c, err := crawler.New(cfg,
				crawler.WithSink(st), crawler.WithResume(processed, pending))
			if err != nil {
				return exitErr{2, err}
			}
			defer c.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			res, err := c.Run(ctx, seeds...)
			if err != nil {
				return exitErr{1, err}
			}
			// A resumed crawl that drains to completion is finalised exactly like
			// a fresh one: aggregates, status, full-graph depth recompute (so the
			// original run's pages don't keep stale admit-time depths), and
			// analysis — all via the shared finalize path.
			out, ferr := finalize.Crawl(c, st, res, finalize.Params{
				StoreDir: storeDir, Cfg: cfg, Seeds: seeds, Resumed: true, Completed: !res.Interrupted,
			})
			if res.Interrupted {
				fmt.Fprintf(cmd.ErrOrStderr(), "crawl interrupted — resume with: bluesnake resume %s --store-dir %s\n", st.ID, storeDir)
				return exitErr{3, errors.New("interrupted")}
			}
			printSummary(cmd, res)
			if ferr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "finalize:", ferr)
			} else {
				printAnalysis(cmd, st.ID, out)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (requires --force)")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "config override (requires --force)")
	cmd.Flags().BoolVar(&force, "force", false, "allow resuming with a different config")
	return cmd
}
