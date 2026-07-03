// bluesnake is a headless, CLI-first website crawler and SEO auditor.
// See docs/DESIGN.md for the architecture this binary exposes.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/version"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// exit codes contract (DESIGN.md §3): 0 ok, 1 runtime error, 2 config error,
// 3 interrupted (resumable).
type exitErr struct {
	code int
	err  error
}

func (e exitErr) Error() string { return e.err.Error() }

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		code := 1
		var ee exitErr
		if errors.As(err, &ee) {
			code = ee.code
		}
		os.Exit(code)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "bluesnake",
		Short:         "A modern, headless website crawler and SEO auditor",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newConfigCmd())
	root.AddCommand(newCrawlCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newCompareCmd())
	root.AddCommand(newCrawlsCmd())
	root.AddCommand(newQueueCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newResumeCmd())
	root.AddCommand(newIssuesCmd())
	root.AddCommand(newAnalyzeCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newSitemapCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newToolsCmd())
	root.AddCommand(newVersionCmd())
	return root
}

// appVersion is the single canonical version (see internal/version).
var appVersion = version.Version

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "bluesnake "+appVersion)
		},
	}
}

func newConfigCmd() *cobra.Command {
	cfgCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage bluesnake configuration",
	}

	var stdout bool
	var outFile string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Emit a fully-populated default config",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := yaml.Marshal(config.Default())
			if err != nil {
				return err
			}
			header := "# bluesnake configuration. Every key shown with its default value.\n" +
				"# Any key may be omitted; unknown keys are errors.\n" +
				"# Reference: docs/DESIGN.md §4.\n"
			out := append([]byte(header), data...)
			if stdout || outFile == "" {
				cmd.OutOrStdout().Write(out)
				return nil
			}
			if _, err := os.Stat(outFile); err == nil {
				return exitErr{2, fmt.Errorf("%s already exists", outFile)}
			}
			if err := os.WriteFile(outFile, out, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", outFile)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&stdout, "stdout", false, "write to stdout instead of a file")
	initCmd.Flags().StringVarP(&outFile, "output", "o", "bluesnake.yaml", "output file")

	validateCmd := &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.LoadFile(args[0]); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: ok\n", args[0])
			return nil
		},
	}

	var cfgFile string
	var sets []string
	var crawlID string
	var storeDir string
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration (file + overrides over defaults), or a stored crawl's frozen config with --crawl",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --crawl prints the exact config frozen into a stored crawl at start —
			// the same config resume/analyze reuse, and what the desktop Setup tab
			// shows. It's a standalone source: combining it with --config/--set would
			// be ambiguous, so that's rejected.
			if crawlID != "" {
				if cfgFile != "" || len(sets) > 0 {
					err := errors.New("--crawl reads the crawl's frozen config; it can't be combined with --config or --set")
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
				st, err := store.OpenCrawl(storeDir, crawlID)
				if err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
				defer st.Close()
				cfgYAML, err := st.Meta("config")
				if err != nil {
					return err
				}
				cmd.OutOrStdout().Write([]byte(cfgYAML))
				return nil
			}
			c := config.Default()
			var err error
			if cfgFile != "" {
				c, err = config.LoadFile(cfgFile)
				if err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}
			for _, s := range sets {
				if err := c.Set(s); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}
			if err := c.Validate(); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			data, err := yaml.Marshal(c)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(data)
			return nil
		},
	}
	showCmd.Flags().StringVar(&cfgFile, "config", "", "config file")
	showCmd.Flags().StringArrayVar(&sets, "set", nil, "dotted-path override (key.path=value), repeatable")
	showCmd.Flags().StringVar(&crawlID, "crawl", "", "print the config frozen into this stored crawl id")
	showCmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")

	cfgCmd.AddCommand(initCmd, validateCmd, showCmd)
	return cfgCmd
}
