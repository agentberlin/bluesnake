package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentberlin/bluesnake/internal/export"
	"github.com/agentberlin/bluesnake/internal/sitemapgen"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

// writeDataset writes to a file (or stdout when path is "-"/empty), with
// xlsx requiring a file path.
func writeDataset(cmd *cobra.Command, d *export.Dataset, format, outPath string) error {
	if format == "xlsx" {
		if outPath == "" || outPath == "-" {
			return exitErr{2, fmt.Errorf("xlsx output requires --output <file>")}
		}
		return export.WriteXLSX(d, outPath)
	}
	if outPath == "" || outPath == "-" {
		return export.Write(d, format, cmd.OutOrStdout())
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return export.Write(d, format, f)
}

func newExportCmd() *cobra.Command {
	var storeDir, format, outPath, filter string
	var list bool
	cmd := &cobra.Command{
		Use:   "export [<crawl-id>] [<tab>]",
		Short: "Export crawl data (tabs, links, issues) as csv/json/jsonl/xlsx",
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				fmt.Fprintln(cmd.OutOrStdout(), strings.Join(export.List(), "\n"))
				return nil
			}
			if len(args) != 2 {
				return exitErr{2, fmt.Errorf("usage: bluesnake export <crawl-id> <tab> (or --list)")}
			}
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()
			d, err := export.BuildAny(st, args[1], filter)
			if err != nil {
				return exitErr{2, err}
			}
			return writeDataset(cmd, d, format, outPath)
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&format, "format", "csv", "csv | json | jsonl | xlsx")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "output file (default stdout)")
	cmd.Flags().StringVar(&filter, "filter", "", "restrict to URLs affected by this issue id")
	cmd.Flags().BoolVar(&list, "list", false, "list exportable datasets")
	return cmd
}

func newReportCmd() *cobra.Command {
	var storeDir, format, outPath string
	var list bool
	cmd := &cobra.Command{
		Use:   "report [<crawl-id>] [<name>]",
		Short: "Generate a named report (crawl overview, chains, insecure content, orphans)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				fmt.Fprintln(cmd.OutOrStdout(), strings.Join(export.Reports(), "\n"))
				return nil
			}
			if len(args) != 2 {
				return exitErr{2, fmt.Errorf("usage: bluesnake report <crawl-id> <name> (or --list)")}
			}
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()
			d, err := export.BuildReport(st, args[1])
			if err != nil {
				return exitErr{2, err}
			}
			return writeDataset(cmd, d, format, outPath)
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVar(&format, "format", "csv", "csv | json | jsonl | xlsx")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "output file (default stdout)")
	cmd.Flags().BoolVar(&list, "list", false, "list available reports")
	return cmd
}

func newSitemapCmd() *cobra.Command {
	var storeDir, outDir string
	var lastmod bool
	cmd := &cobra.Command{
		Use:   "sitemap <crawl-id>",
		Short: "Generate XML sitemap(s) from a stored crawl",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.OpenCrawl(storeDir, args[0])
			if err != nil {
				return exitErr{2, err}
			}
			defer st.Close()
			pages, err := st.LoadPages()
			if err != nil {
				return err
			}
			files, err := sitemapgen.Generate(pages, sitemapgen.Options{IncludeLastmod: lastmod})
			if err != nil {
				return err
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return err
			}
			for _, f := range files {
				path := filepath.Join(outDir, f.Name)
				if err := os.WriteFile(path, f.Data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	cmd.Flags().StringVarP(&outDir, "output", "o", ".", "output directory")
	cmd.Flags().BoolVar(&lastmod, "lastmod", false, "include lastmod from Last-Modified headers")
	return cmd
}
