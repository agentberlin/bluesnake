package main

import (
	"os"
	"strings"

	"github.com/agentberlin/bluesnake/internal/bundle"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

// newBundleCmd exports a whole crawl as one machine-readable file. It is its
// own command rather than an `export <crawl> pages --format jsonl` because
// export's Dataset{Header []string, Rows [][]string} is flat by construction
// and a page record is not: headings, robots directives, schema.org types, raw
// JSON-LD blocks and link edges are all natural multiples, and body text has no
// column at all.
func newBundleCmd() *cobra.Command {
	var storeDir, outPath, scope, linkTypes string
	var gzipOut bool
	cmd := &cobra.Command{
		Use:   "bundle <crawl-id>",
		Short: "Export a whole crawl as one streamable JSON Lines file (page text, structured data, link graph)",
		Long: "Write a stored crawl as JSON Lines: one header record describing the crawl,\n" +
			"then one record per page carrying its text, structured data (including the raw\n" +
			"JSON-LD blocks) and its nested link edges. The stream is versioned, counted and\n" +
			"byte-reproducible, so a consumer can refuse a format it does not understand,\n" +
			"detect a truncated transfer, and diff two bundles of the same crawl.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := bundle.Options{
				Scope:     scope,
				LinkTypes: strings.Split(linkTypes, ","),
				// An explicit --gzip wins; otherwise the output's extension decides,
				// so `-o out.jsonl.gz` does what it says.
				GZIP: gzipOut,
			}
			if !cmd.Flags().Changed("gzip") {
				opts.GZIP = strings.HasSuffix(outPath, ".gz")
			}
			if err := opts.Validate(); err != nil {
				return exitErr{2, err}
			}
			// The crawl-level facts (status, timings, headline counts) live in the
			// registry, not the crawl DB. A bundle of an interrupted crawl is
			// legitimate — the header says so rather than the export refusing it.
			info, err := store.CrawlInfo(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()

			if outPath == "" || outPath == "-" {
				return bundle.Write(st, info, opts, cmd.OutOrStdout())
			}
			f, err := os.Create(outPath)
			if err != nil {
				return err
			}
			if err := bundle.Write(st, info, opts, f); err != nil {
				f.Close()
				return err
			}
			return f.Close()
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "output file (default stdout)")
	cmd.Flags().BoolVar(&gzipOut, "gzip", false, "gzip the output (implied when --output ends in .gz)")
	cmd.Flags().StringVar(&scope, "scope", bundle.ScopeInternal, "internal | external | all")
	cmd.Flags().StringVar(&linkTypes, "link-types", "hyperlink",
		"comma-separated link types to nest under each page, or \"all\"")
	return cmd
}
