package main

import (
	"fmt"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
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
			cfgYAML, err := st.Meta("config")
			if err != nil {
				return err
			}
			cfg, err := config.Load([]byte(cfgYAML))
			if err != nil {
				return err
			}
			if err := runAnalysis(cmd, st, cfg, false); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	return cmd
}

// runAnalysis runs the post-crawl phases: issue evaluation, then the graph
// analyses, persisting everything back into the crawl database.
func runAnalysis(cmd *cobra.Command, st *store.Crawl, cfg *config.Config, quiet bool) error {
	if err := evaluateIssues(st); err != nil {
		return err
	}
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	sitemaps, err := st.SitemapIndex()
	if err != nil {
		return err
	}
	results := analyze.Run(pages, sitemaps, cfg)
	if err := st.SaveAnalysis(results); err != nil {
		return err
	}
	if !quiet {
		counts, err := st.IssueCounts()
		if err != nil {
			return err
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Analysis: %d chains, %d near-duplicate pages\n",
			len(results.Chains), len(results.NearDups))
		fmt.Fprintf(cmd.OutOrStdout(), "Issues: %d occurrences across %d checks (bluesnake issues %s)\n",
			total, len(counts), st.ID)
	}
	return nil
}
