// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/types"
)

// Exporter handles exporting crawl data
type Exporter struct {
	app         *app.App
	store       *store.Store
	crawlID     uint
	projectID   uint
	domain      string
	outputDir   string
	format      string
	exportLinks bool
}

// Export exports crawl results to the specified format
func (e *Exporter) Export() error {
	// Create output directory
	if err := os.MkdirAll(e.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Export internal URLs
	if err := e.exportInternal(); err != nil {
		return fmt.Errorf("failed to export internal URLs: %v", err)
	}

	// Export links if requested
	if e.exportLinks {
		if err := e.exportOutlinks(); err != nil {
			return fmt.Errorf("failed to export outlinks: %v", err)
		}
	}

	// Export summary
	if err := e.exportSummary(); err != nil {
		return fmt.Errorf("failed to export summary: %v", err)
	}

	return nil
}

// exportInternal exports all discovered URLs
func (e *Exporter) exportInternal() error {
	// Collect all results with pagination
	var allResults []types.CrawlResult
	var cursor uint = 0
	const pageSize = 1000

	for {
		page, err := e.app.GetCrawlWithResultsPaginated(e.crawlID, pageSize, cursor, "all")
		if err != nil {
			return err
		}

		allResults = append(allResults, page.Results...)

		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}

	if e.format == "json" {
		return e.exportInternalJSON(allResults)
	}
	return e.exportInternalCSV(allResults)
}

func (e *Exporter) exportInternalJSON(results []types.CrawlResult) error {
	output := struct {
		CrawlID       uint                 `json:"crawlId"`
		Domain        string               `json:"domain"`
		CrawlDateTime string               `json:"crawlDateTime"`
		TotalURLs     int                  `json:"totalUrls"`
		Results       []types.CrawlResult  `json:"results"`
	}{
		CrawlID:       e.crawlID,
		Domain:        e.domain,
		CrawlDateTime: time.Now().Format(time.RFC3339),
		TotalURLs:     len(results),
		Results:       results,
	}

	filePath := filepath.Join(e.outputDir, "internal_all.json")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func (e *Exporter) exportInternalCSV(results []types.CrawlResult) error {
	filePath := filepath.Join(e.outputDir, "internal_all.csv")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Write header (ScreamingFrog-compatible)
	header := []string{
		"Address",
		"Status Code",
		"Content Type",
		"Title 1",
		"Meta Description 1",
		"H1-1",
		"H2-1",
		"Canonical Link Element 1",
		"Word Count",
		"Indexability",
		"Crawl Depth",
		"Content Hash",
	}
	if err := w.Write(header); err != nil {
		return err
	}

	// Write data
	for _, r := range results {
		row := []string{
			r.URL,
			strconv.Itoa(r.Status),
			r.ContentType,
			r.Title,
			r.MetaDescription,
			r.H1,
			r.H2,
			r.CanonicalURL,
			strconv.Itoa(r.WordCount),
			r.Indexable,
			strconv.Itoa(r.Depth),
			r.ContentHash,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// PageLinkExport represents a page link for export
type PageLinkExport struct {
	SourceURL string `json:"sourceUrl"`
	TargetURL string `json:"targetUrl"`
	LinkType  string `json:"linkType"`
	LinkText  string `json:"linkText"`
	Follow    bool   `json:"follow"`
	Rel       string `json:"rel"`
	Target    string `json:"target"`
	PathType  string `json:"pathType"`
	Position  string `json:"position"`
}

// exportOutlinks exports all page links
func (e *Exporter) exportOutlinks() error {
	// Get all page links from the store using the store directly
	links, err := e.store.GetAllLinksForCrawl(e.crawlID)
	if err != nil {
		return err
	}

	// Convert to export format
	exportLinks := make([]PageLinkExport, len(links))
	for i, l := range links {
		exportLinks[i] = PageLinkExport{
			SourceURL: l.SourceURL,
			TargetURL: l.TargetURL,
			LinkType:  l.LinkType,
			LinkText:  l.LinkText,
			Follow:    l.Follow,
			Rel:       l.Rel,
			Target:    l.Target,
			PathType:  l.PathType,
			Position:  l.Position,
		}
	}

	if e.format == "json" {
		return e.exportOutlinksJSON(exportLinks)
	}
	return e.exportOutlinksCSV(exportLinks)
}

func (e *Exporter) exportOutlinksJSON(links []PageLinkExport) error {
	output := struct {
		CrawlID uint              `json:"crawlId"`
		Links   []PageLinkExport  `json:"links"`
	}{
		CrawlID: e.crawlID,
		Links:   links,
	}

	filePath := filepath.Join(e.outputDir, "all_outlinks.json")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func (e *Exporter) exportOutlinksCSV(links []PageLinkExport) error {
	filePath := filepath.Join(e.outputDir, "all_outlinks.csv")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Write header (ScreamingFrog-compatible)
	header := []string{
		"Source",
		"Destination",
		"Type",
		"Anchor",
		"Follow",
		"Rel",
		"Target",
		"Path Type",
		"Link Position",
	}
	if err := w.Write(header); err != nil {
		return err
	}

	// Write data
	for _, l := range links {
		follow := "true"
		if !l.Follow {
			follow = "false"
		}
		row := []string{
			l.SourceURL,
			l.TargetURL,
			l.LinkType,
			l.LinkText,
			follow,
			l.Rel,
			l.Target,
			l.PathType,
			l.Position,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// exportSummary exports crawl summary/metadata
func (e *Exporter) exportSummary() error {
	stats, err := e.app.GetCrawlStats(e.crawlID)
	if err != nil {
		return err
	}

	summary := struct {
		CrawlID       uint   `json:"crawlId"`
		ProjectID     uint   `json:"projectId"`
		Domain        string `json:"domain"`
		ExportedAt    string `json:"exportedAt"`
		TotalURLs     int    `json:"totalUrls"`
		HTMLPages     int    `json:"htmlPages"`
		Images        int    `json:"images"`
		JavaScript    int    `json:"javascript"`
		CSS           int    `json:"css"`
		Fonts         int    `json:"fonts"`
		Others        int    `json:"others"`
	}{
		CrawlID:    e.crawlID,
		ProjectID:  e.projectID,
		Domain:     e.domain,
		ExportedAt: time.Now().Format(time.RFC3339),
		TotalURLs:  stats.Total,
		HTMLPages:  stats.HTML,
		Images:     stats.Images,
		JavaScript: stats.JavaScript,
		CSS:        stats.CSS,
		Fonts:      stats.Fonts,
		Others:     stats.Others,
	}

	filePath := filepath.Join(e.outputDir, "crawl_summary.json")
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

// runExport handles the export command
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)

	var crawlID uint
	var output string
	var format string
	var exportLinks bool
	var contentType string

	fs.UintVar(&crawlID, "crawl-id", 0, "Crawl ID to export (required)")
	fs.UintVar(&crawlID, "c", 0, "Crawl ID (shorthand)")
	fs.StringVar(&output, "output", ".", "Output directory")
	fs.StringVar(&output, "o", ".", "Output directory (shorthand)")
	fs.StringVar(&format, "format", "json", "Output format: json, csv")
	fs.StringVar(&format, "f", "json", "Output format (shorthand)")
	fs.BoolVar(&exportLinks, "export-links", false, "Export outlinks")
	fs.StringVar(&contentType, "type", "all", "Content type filter: all, html, images, css, js")
	fs.StringVar(&contentType, "t", "all", "Content type filter (shorthand)")

	fs.Usage = func() {
		fmt.Println(`Usage: bluesnake export [flags]

Export crawl results to JSON or CSV.

Flags:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  # Export crawl results as JSON
  bluesnake export --crawl-id 123 -o ./export

  # Export as CSV with links
  bluesnake export --crawl-id 123 --format csv --export-links -o ./export`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if crawlID == 0 {
		fs.Usage()
		return fmt.Errorf("--crawl-id is required")
	}

	// Validate format
	if format != "json" && format != "csv" {
		return fmt.Errorf("invalid format: %s (must be json or csv)", format)
	}

	// Initialize store and app
	st, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}

	emitter := &app.NoOpEmitter{}
	coreApp := app.NewApp(st, emitter)
	coreApp.Startup(context.Background())

	// Get crawl info to get project ID and domain
	// We need to get this from the store
	crawlInfo, err := st.GetCrawlByID(crawlID)
	if err != nil {
		return fmt.Errorf("crawl not found: %v", err)
	}

	projectInfo, err := st.GetProjectByID(crawlInfo.ProjectID)
	if err != nil {
		return fmt.Errorf("project not found: %v", err)
	}

	exporter := &Exporter{
		app:         coreApp,
		store:       st,
		crawlID:     crawlID,
		projectID:   crawlInfo.ProjectID,
		domain:      projectInfo.Domain,
		outputDir:   output,
		format:      format,
		exportLinks: exportLinks,
	}

	fmt.Printf("Exporting crawl %d to %s...\n", crawlID, output)

	if err := exporter.Export(); err != nil {
		return err
	}

	fmt.Println("Export complete!")
	return nil
}
