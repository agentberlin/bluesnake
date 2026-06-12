// bluesnake is a headless, CLI-first website crawler and SEO auditor.
// See docs/DESIGN.md for the architecture this binary exposes.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/robots"
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
	root.AddCommand(newResumeCmd())
	root.AddCommand(newIssuesCmd())
	root.AddCommand(newAnalyzeCmd())
	root.AddCommand(newExportCmd())
	root.AddCommand(newReportCmd())
	root.AddCommand(newSitemapCmd())
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newRobotsCmd())
	root.AddCommand(newVersionCmd())
	return root
}

func newRobotsCmd() *cobra.Command {
	robotsCmd := &cobra.Command{
		Use:   "robots",
		Short: "Robots.txt tools",
	}

	var robotsFile, userAgent string
	testCmd := &cobra.Command{
		Use:   "test <url>...",
		Short: "Test URLs against a robots.txt file (Google REP semantics)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if robotsFile == "" {
				return exitErr{2, errors.New("--robots-file is required (live fetching arrives with the crawler)")}
			}
			data, err := os.ReadFile(robotsFile)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			f := robots.Parse(data)
			for _, u := range args {
				v := f.Verdict(userAgent, u)
				if v.Allowed {
					fmt.Fprintf(cmd.OutOrStdout(), "ALLOWED  %s\n", u)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "BLOCKED  %s  (line %d: %s)\n", u, v.Rule.Line, v.Rule.Raw)
				}
			}
			return nil
		},
	}
	testCmd.Flags().StringVar(&robotsFile, "robots-file", "", "robots.txt file to test against")
	testCmd.Flags().StringVar(&userAgent, "robots-user-agent", "bluesnake", "robots user-agent token")

	robotsCmd.AddCommand(testCmd)
	return robotsCmd
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
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration (file + overrides over defaults)",
		RunE: func(cmd *cobra.Command, args []string) error {
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

	cfgCmd.AddCommand(initCmd, validateCmd, showCmd)
	return cfgCmd
}
