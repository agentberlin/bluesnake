package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

func newAnalyzeCmd() *cobra.Command {
	var storeDir string
	cmd := &cobra.Command{
		Use:   "analyze <crawl-id>",
		Short: "(Re-)run post-crawl analysis: link score, chains, near-duplicates, hreflang, pagination, sitemaps",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()
			cfg, err := storedConfig(st)
			if err != nil {
				return err
			}
			out, err := finalize.Analyze(st, cfg)
			if err != nil {
				return err
			}
			printAnalysis(cmd, st.ID, out)
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	return cmd
}

// storedConfig loads the config frozen into a crawl at start.
func storedConfig(st *store.Crawl) (*config.Config, error) {
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return nil, err
	}
	return config.Load([]byte(cfgYAML))
}

// printAnalysis renders the post-analysis summary lines shared by the crawl,
// resume, list and analyze commands.
func printAnalysis(cmd *cobra.Command, id string, o finalize.Outcome) {
	if !o.Analyzed {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Analysis: %d chains, %d near-duplicate pages\n",
		o.Chains, o.NearDups)
	fmt.Fprintf(cmd.OutOrStdout(), "Issues: %d occurrences across %d checks (bluesnake issues %s)\n",
		o.IssueTotal, o.IssueChecks, id)
}
