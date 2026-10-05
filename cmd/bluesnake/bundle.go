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
	var gzipOut, full bool
	cmd := &cobra.Command{
		Use:   "bundle <crawl-id>",
		Short: "Export a whole crawl as one streamable JSON Lines file (page text, structured data, link graph; --full adds stored HTML)",
		Long: "Write a stored crawl as JSON Lines: one header record describing the crawl —\n" +
			"its site-check reports (robots.txt, sitemaps, AI-bot access) and llms.txt files\n" +
			"included — then one record per page carrying everything the crawl stored about\n" +
			"it: its text, response headers, structured data (including the raw JSON-LD\n" +
			"blocks), the author evidence it shows, custom search/extraction values, the\n" +
			"sitemaps that list it, link-graph metrics and its nested link edges.\n" +
			"With --full, a crawl that kept its page sources (extraction.store_html,\n" +
			"store_rendered_html) also carries them on every page record; the header's\n" +
			"`stored` and `full` say what the crawl kept and whether this file has it. The\n" +
			"stream is versioned, counted and byte-reproducible, so a consumer can refuse a\n" +
			"format it does not understand, detect a truncated transfer, and diff two\n" +
			"bundles of the same crawl.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := bundle.Options{
				Scope:     scope,
				LinkTypes: strings.Split(linkTypes, ","),
				// An explicit --gzip wins; otherwise the output's extension decides,
				// so `-o out.jsonl.gz` does what it says.
				GZIP: gzipOut,
				Full: full,
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
	cmd.Flags().BoolVar(&full, "full", false,
		"also carry each page's stored HTML (raw and rendered) when the crawl kept it — the sources are most of the file")
	return cmd
}
