package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
			fmt.Fprintln(w, "ID\tMODE\tSTATUS\tURLS\tCRAWLED\tSEED")
			for _, in := range infos {
				total := in.Total
				if total == 0 {
					total = in.Crawled
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n",
					in.ID, in.Mode, in.Status, total, in.Crawled, in.Seed)
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
			// --force replaces the crawl's FROZEN config before the resume runs:
			// validate the override, persist it, and every later resume sees the
			// same (single, durable) config — instead of a one-session override
			// that silently reverts.
			if force {
				if err := persistForcedConfig(storeDir, args[0], cfgFile, sets); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}

			// The resume runs through the same queue wiring as every other crawl
			// (one crawl path): the dispatcher's executor owns the resume-open
			// guards — pre-edges refusal, completed-status refusal, resume-state
			// load — so the CLI cannot drift from the MCP/desktop semantics again
			// (#74 R1). The CLI only renders the outcome.
			obs := &cliObserver{done: make(chan struct{})}
			disp := queue.New(queue.NewMemStore(), runner.New(storeDir, obs))
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := disp.Start(ctx); err != nil {
				return exitErr{1, err}
			}
			if _, err := disp.Enqueue(queue.JobSpec{ResumeID: args[0]}, "manual", "", "resume "+args[0]); err != nil {
				return exitErr{1, err}
			}
			<-obs.done
			disp.Shutdown()

			out := obs.outcome()
			if out.Err != nil && out.CrawlID == "" {
				// the resume was refused before a crawl session began (unknown id,
				// pre-edges, already completed, resume-state load failure)
				fmt.Fprintln(cmd.ErrOrStderr(), out.Err)
				return exitErr{2, out.Err}
			}
			if out.Status == store.StatusInterrupted {
				fmt.Fprintf(cmd.ErrOrStderr(), "crawl interrupted — resume with: bluesnake resume %s --store-dir %s\n", args[0], storeDir)
				return exitErr{3, errors.New("interrupted")}
			}
			if out.Err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), out.Err)
				return exitErr{1, out.Err}
			}
			// Break down the full two-session graph (the registry counts are
			// cumulative); records were streamed to the store, so read them back
			// rather than tallying only this session's page stream.
			if st, err := store.OpenCrawl(storeDir, args[0]); err == nil {
				pages, _ := st.LoadPages()
				st.Close()
				printSummary(cmd, pages, out.Crawled, out.Total, time.Duration(out.DurationSec)*time.Second)
			}
			printAnalysis(cmd, out.CrawlID, finalize.Outcome{
				Analyzed: out.Analyzed, Chains: out.Chains, NearDups: out.NearDups,
				IssueTotal: out.IssueTotal, IssueChecks: out.IssueChecks,
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (requires --force; replaces the crawl's stored config)")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "config override (requires --force; persisted into the crawl)")
	cmd.Flags().BoolVar(&force, "force", false, "resume with a different config, replacing the one stored with the crawl")
	return cmd
}

// persistForcedConfig applies a --force override on top of the crawl's frozen
// config, validates it, and writes it back as the crawl's config — the single
// durable config every subsequent session (this resume and any later one) runs.
func persistForcedConfig(storeDir, id, cfgFile string, sets []string) error {
	st, err := store.OpenCrawl(storeDir, id)
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
	if cfgFile != "" {
		if cfg, err = config.LoadFile(cfgFile); err != nil {
			return err
		}
	}
	for _, s := range sets {
		if err := cfg.Set(s); err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	newYAML, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return st.SetMeta("config", string(newYAML))
}
