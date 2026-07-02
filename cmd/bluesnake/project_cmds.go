package main

import (
	"fmt"
	"io"
	"os/signal"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/render"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// newProjectCmd is the `bluesnake projects` subtree: an opt-in competitor-study
// layer over stored crawls. It reads the crawl registry read-only and keeps its
// own data in <store-dir>/projects.db — removing this command and the package
// leaves the rest of the product untouched.
func newProjectCmd() *cobra.Command {
	var storeDir string
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "Group a main domain with competitors for comparison",
	}
	cmd.PersistentFlags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")

	open := func() (*project.Store, error) { return project.Open(storeDir) }

	createCmd := &cobra.Command{
		Use:   "create <main-domain>",
		Short: "Create a project anchored on a main domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			p, err := s.CreateProject(name, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created project %s (%s)\n", p.ID, p.Name)
			return nil
		},
	}
	createCmd.Flags().String("name", "", "display name (default \"<main-domain>'s Project\")")

	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			ps, err := s.ListProjects()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tMAIN DOMAIN")
			for _, p := range ps {
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.ID, p.Name, p.MainDomain)
			}
			return w.Flush()
		},
	}

	rmCmd := &cobra.Command{
		Use:   "rm <project-id>",
		Short: "Delete a project (crawls are untouched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.DeleteProject(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}

	addCmd := &cobra.Command{
		Use:   "add <project-id> <competitor-domain>",
		Short: "Add a competitor domain to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.AddMember(args[0], args[1], project.RoleCompetitor); err != nil {
				return exitErr{2, err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added %s to %s\n", args[1], args[0])
			return nil
		},
	}

	removeCmd := &cobra.Command{
		Use:   "remove <project-id> <domain>",
		Short: "Remove a domain from a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.RemoveMember(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s from %s\n", args[1], args[0])
			return nil
		},
	}

	showCmd := &cobra.Command{
		Use:   "show <project-id>",
		Short: "Show a project's sites and each site's crawl history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			p, err := s.GetProject(args[0])
			if err != nil {
				return err
			}
			members, err := s.Members(p.ID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  (%s)\n", p.Name, p.ID)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SITE\tROLE\tCRAWL\tWHEN\tSTATUS")
			for _, m := range members {
				hist, err := s.SiteHistory(m.Domain)
				if err != nil {
					return err
				}
				if len(hist) == 0 {
					fmt.Fprintf(w, "%s\t%s\t—\t—\tno crawl yet\n", m.Domain, m.Role)
					continue
				}
				for _, h := range hist {
					status := "comparable"
					if !h.Comparable {
						status = h.Reason
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
						m.Domain, m.Role, h.ID, h.Started.Format("2006-01-02 15:04"), status)
				}
			}
			return w.Flush()
		},
	}

	var optional bool
	compareCmd := &cobra.Command{
		Use:   "compare <project-id>",
		Short: "Competitor scorecard: main vs competitors (latest comparable crawl each)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			card, err := s.BuildScorecard(args[0], optional)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", card.ProjectName)
			if card.ConfigDiverges {
				fmt.Fprintf(cmd.ErrOrStderr(), "⚠ configs differ across sites (%v) — interpret with care; re-crawl for a fair comparison\n", card.DivergingDims)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 2, 2, ' ', 0)
			head := "SITE\tROLE\tWHEN\tURLS\tINDEX%\tERR\tWARN\tOPP\tLINKSCORE\tRENDER"
			if optional {
				head += "\tAVGWORDS\tSCHEMA%"
			}
			fmt.Fprintln(w, head)
			for _, r := range card.Sites {
				if r.Status != "ok" {
					fmt.Fprintf(w, "%s\t%s\tno comparable crawl\t\t\t\t\t\t\t\n", r.Domain, r.Role)
					continue
				}
				line := fmt.Sprintf("%s\t%s\t%s\t%d\t%.0f%%\t%d\t%d\t%d\t%.1f\t%s",
					r.Domain, r.Role, time.Unix(r.Started, 0).Format("2006-01-02"),
					r.URLs, r.IndexableRate*100, r.Errors, r.Warnings, r.Opportunities,
					r.AvgLinkScore, r.Rendering)
				if optional {
					line += fmt.Sprintf("\t%.0f\t%.0f%%", r.AvgWordCount, r.SchemaCoverage*100)
				}
				fmt.Fprintln(w, line)
			}
			return w.Flush()
		},
	}
	compareCmd.Flags().BoolVar(&optional, "optional", false, "include content-depth & schema metrics (reads page JSON)")

	diffCmd := &cobra.Command{
		Use:   "diff <project-id> <domain>",
		Short: "Over-time URL/issue diff for one site (its two latest comparable crawls)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			defer s.Close()
			prevID, currID, ok, err := s.ComparePair(args[1])
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "%s needs at least two comparable crawls to diff\n", args[1])
				return nil
			}
			res, err := s.Compare(prevID, currID)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s → %s\n", args[1], prevID, currID)
			fmt.Fprintf(cmd.OutOrStdout(), "  pages: %d → %d  (+%d new, -%d missing)\n",
				res.PagesPrevious, res.PagesCurrent, len(res.NewPages), len(res.MissingPages))
			for _, d := range res.Deltas {
				if len(d.New)+len(d.Removed)+len(d.Added)+len(d.Missing) == 0 {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %s: +%d added, -%d resolved\n",
					d.IssueID, len(d.Added)+len(d.New), len(d.Removed)+len(d.Missing))
			}
			return nil
		},
	}

	var (
		parallel       int
		crawlAllConfig string
	)
	crawlAllCmd := &cobra.Command{
		Use:   "crawl-all <project-id>",
		Short: "Crawl every member domain of the project (up to --parallel at once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := open()
			if err != nil {
				return err
			}
			members, err := s.Members(args[0])
			s.Close()
			if err != nil {
				return exitErr{2, err}
			}
			if len(members) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "project has no member domains to crawl")
				return nil
			}
			cfg := config.Default()
			if crawlAllConfig != "" {
				if cfg, err = config.LoadFile(crawlAllConfig); err != nil {
					return exitErr{2, err}
				}
			}
			// Parallelism comes from --parallel when set, else the config's
			// max_concurrent_crawls knob (M2), so off-CLI surfaces and the CLI agree.
			if !cmd.Flags().Changed("parallel") && cfg.Speed.MaxConcurrentCrawls > 0 {
				parallel = cfg.Speed.MaxConcurrentCrawls
			}
			if parallel < 1 {
				parallel = 1
			}
			cfgYAML, err := yaml.Marshal(cfg)
			if err != nil {
				return exitErr{1, err}
			}
			// In-process drain: the dispatcher runs up to `parallel` member crawls at
			// once through the shared executor, with ONE process-wide limiter bounding
			// total concurrent fetches across them. The global cap is the user's
			// speed.max_global_threads knob (0 = unlimited) — NOT parallel × per-crawl
			// threads, which equals the sum of the per-crawl maxima and so could never
			// bind (H1). A single finalize pass runs at a time so M crawls finishing
			// together don't each materialise an analysis working set (§5.6 / H2).
			// Chrome renders get their own process-wide slot pool (REN-01/#76):
			// rendering.max_global_renders, 0 = the cores-scaled tab ceiling.
			lim := limiter.New(cfg.Speed.MaxGlobalThreads, 1, render.GlobalRenderCap(cfg))
			obs := &groupObserver{out: cmd.OutOrStdout(), total: len(members), done: make(chan struct{})}
			disp := queue.New(queue.NewMemStore(),
				runner.New(storeDir, obs, runner.WithLimiter(lim)),
				queue.WithConcurrency(parallel))
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := disp.Start(ctx); err != nil {
				return exitErr{1, err}
			}
			for _, m := range members {
				if _, err := disp.Enqueue(queue.JobSpec{URL: "https://" + m.Domain, ConfigYAML: string(cfgYAML)}, "project", args[0], m.Domain); err != nil {
					return exitErr{1, err}
				}
			}
			// A SIGINT with members still queued means obs.done can never close
			// (the queued members' OnDone never fires) — waiting solely on it
			// hung forever, with the deferred stop() also swallowing the second
			// Ctrl-C (#74 N3). Race the group against the signal context; on
			// interrupt, restore default signal handling FIRST (a second Ctrl-C
			// kills), then shut down (pauses in-flight crawls, resumable).
			select {
			case <-obs.done:
				disp.Shutdown()
				return nil
			case <-ctx.Done():
				stop()
				disp.Shutdown()
				fmt.Fprintln(cmd.ErrOrStderr(), "interrupted — in-flight member crawls paused (resumable); queued members not started")
				return exitErr{3, fmt.Errorf("interrupted")}
			}
		},
	}

	crawlAllCmd.Flags().IntVar(&parallel, "parallel", 1, "member crawls to run at once (default: speed.max_concurrent_crawls)")
	crawlAllCmd.Flags().StringVar(&crawlAllConfig, "config", "", "config file (YAML) applied to every member crawl")
	cmd.AddCommand(createCmd, lsCmd, rmCmd, addCmd, removeCmd, showCmd, compareCmd, diffCmd, crawlAllCmd)
	return cmd
}

// groupObserver tracks a `projects crawl-all` run, printing a line per crawl and
// closing done once every member crawl has finished.
type groupObserver struct {
	out   io.Writer
	total int
	done  chan struct{}

	mu       sync.Mutex
	finished int
}

func (o *groupObserver) OnStart(crawlID, seed string) {
	fmt.Fprintf(o.out, "crawling %s …\n", seed)
}

func (o *groupObserver) OnPage(string, *crawler.PageRecord) {}

func (o *groupObserver) OnDone(out runner.Outcome) {
	o.mu.Lock()
	o.finished++
	n := o.finished
	o.mu.Unlock()
	status := out.Status
	if out.Err != nil {
		status = "error: " + out.Err.Error()
	}
	fmt.Fprintf(o.out, "  [%d/%d] %s — %s (%d crawled)\n", n, o.total, out.CrawlID, status, out.Crawled)
	if n == o.total {
		close(o.done)
	}
}
