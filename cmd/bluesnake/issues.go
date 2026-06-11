package main

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

func newIssuesCmd() *cobra.Command {
	var storeDir string
	var showURLs string
	cmd := &cobra.Command{
		Use:   "issues <crawl-id>",
		Short: "Evaluate and summarise audit issues for a stored crawl",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()

			if showURLs != "" {
				urls, err := st.IssueURLs(showURLs)
				if err != nil {
					return err
				}
				for _, u := range urls {
					fmt.Fprintln(cmd.OutOrStdout(), u)
				}
				return nil
			}

			if err := evaluateIssues(st); err != nil {
				return err
			}
			counts, err := st.IssueCounts()
			if err != nil {
				return err
			}
			type row struct {
				def   issues.Def
				count int
			}
			var rows []row
			for id, n := range counts {
				if def, ok := issues.Lookup(id); ok {
					rows = append(rows, row{def, n})
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].def.Severity != rows[j].def.Severity {
					return rows[i].def.Severity < rows[j].def.Severity
				}
				return rows[i].count > rows[j].count
			})
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "SEVERITY\tPRIORITY\tISSUE\tURLS\tID")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s: %s\t%d\t%s\n",
					r.def.Severity, r.def.Priority, r.def.Tab, r.def.Name, r.count, r.def.ID)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&showURLs, "urls", "", "list URLs affected by the given issue id")
	return cmd
}

// evaluateIssues loads pages, runs the catalogue, and stores occurrences.
func evaluateIssues(st *store.Crawl) error {
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	return st.SaveIssues(issues.Evaluate(pages, cfg))
}
