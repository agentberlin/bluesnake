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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
)

// crawlFlags holds all the flags for the crawl command
type crawlFlags struct {
	// Core options
	parallelism       int
	userAgent         string
	includeSubdomains bool
	maxURLs           int

	// JS rendering
	jsRendering bool
	initialWait int
	scrollWait  int
	finalWait   int

	// Discovery
	spider        bool
	sitemap       bool
	sitemapURL    string
	checkExternal bool

	// Robots/directives
	robotsTxt              string
	followNofollowInternal bool
	followNofollowExternal bool
	respectNoindex         bool
	respectMetaNoindex     bool

	// Output
	output      string
	format      string
	exportLinks bool
	quiet       bool
}

func runCrawl(args []string) error {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)

	var flags crawlFlags

	// Core options
	fs.IntVar(&flags.parallelism, "parallelism", 5, "Number of concurrent requests")
	fs.IntVar(&flags.parallelism, "p", 5, "Number of concurrent requests (shorthand)")
	fs.StringVar(&flags.userAgent, "user-agent", "bluesnake/1.0 (+https://snake.blue)", "Custom User-Agent string")
	fs.StringVar(&flags.userAgent, "A", "bluesnake/1.0 (+https://snake.blue)", "Custom User-Agent string (shorthand)")
	fs.BoolVar(&flags.includeSubdomains, "include-subdomains", false, "Crawl all subdomains of the target domain")
	fs.IntVar(&flags.maxURLs, "max-urls", 0, "Maximum URLs to crawl (0 = unlimited)")

	// JS rendering
	fs.BoolVar(&flags.jsRendering, "js-rendering", false, "Enable JavaScript rendering via headless Chrome")
	fs.BoolVar(&flags.jsRendering, "j", false, "Enable JavaScript rendering (shorthand)")
	fs.IntVar(&flags.initialWait, "initial-wait", 1500, "Initial wait after page load in milliseconds")
	fs.IntVar(&flags.scrollWait, "scroll-wait", 2000, "Wait after scrolling for lazy-loaded content in milliseconds")
	fs.IntVar(&flags.finalWait, "final-wait", 1000, "Final wait before capturing HTML in milliseconds")

	// Discovery
	fs.BoolVar(&flags.spider, "spider", true, "Enable link discovery by spidering")
	fs.BoolVar(&flags.sitemap, "sitemap", true, "Enable sitemap URL discovery")
	fs.StringVar(&flags.sitemapURL, "sitemap-url", "", "Custom sitemap URL (optional)")
	fs.BoolVar(&flags.checkExternal, "check-external", true, "Validate external resources for broken links")

	// Robots/directives
	fs.StringVar(&flags.robotsTxt, "robots-txt", "respect", "robots.txt mode: respect, ignore, ignore-report")
	fs.BoolVar(&flags.followNofollowInternal, "follow-nofollow-internal", false, "Follow internal links with rel=\"nofollow\"")
	fs.BoolVar(&flags.followNofollowExternal, "follow-nofollow-external", false, "Follow external links with rel=\"nofollow\"")
	fs.BoolVar(&flags.respectNoindex, "respect-noindex", true, "Respect X-Robots-Tag: noindex headers")
	fs.BoolVar(&flags.respectMetaNoindex, "respect-meta-noindex", true, "Respect meta robots noindex")

	// Output
	fs.StringVar(&flags.output, "output", ".", "Output directory for results")
	fs.StringVar(&flags.output, "o", ".", "Output directory (shorthand)")
	fs.StringVar(&flags.format, "format", "json", "Output format: json, csv")
	fs.StringVar(&flags.format, "f", "json", "Output format (shorthand)")
	fs.BoolVar(&flags.exportLinks, "export-links", false, "Export outlinks")
	fs.BoolVar(&flags.quiet, "quiet", false, "Suppress progress output")
	fs.BoolVar(&flags.quiet, "q", false, "Suppress progress output (shorthand)")

	fs.Usage = func() {
		fmt.Println(`Usage: bluesnake crawl <url> [flags]

Start a new crawl for the specified URL.

Flags:`)
		fs.PrintDefaults()
		fmt.Println(`
Examples:
  # Basic crawl
  bluesnake crawl https://example.com

  # Crawl with custom parallelism and output
  bluesnake crawl https://example.com -p 10 -o ./results

  # Crawl with JavaScript rendering
  bluesnake crawl https://example.com --js-rendering

  # Crawl with custom settings
  bluesnake crawl https://example.com \
    --parallelism 10 \
    --js-rendering \
    --include-subdomains \
    --format csv \
    --output ./results`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Check for URL argument
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("URL argument is required")
	}

	urlStr := fs.Arg(0)

	// Validate URL
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}

	// Validate format
	if flags.format != "json" && flags.format != "csv" {
		return fmt.Errorf("invalid format: %s (must be json or csv)", flags.format)
	}

	// Validate robots-txt mode
	if flags.robotsTxt != "respect" && flags.robotsTxt != "ignore" && flags.robotsTxt != "ignore-report" {
		return fmt.Errorf("invalid robots-txt mode: %s (must be respect, ignore, or ignore-report)", flags.robotsTxt)
	}

	// Initialize store and app
	st, err := store.NewStore()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}

	// Create CLI emitter for progress output
	emitter := &CLIEmitter{quiet: flags.quiet}
	coreApp := app.NewApp(st, emitter)
	coreApp.Startup(context.Background())

	// Build sitemap URLs array
	var sitemapURLs []string
	if flags.sitemapURL != "" {
		sitemapURLs = []string{flags.sitemapURL}
	}

	// Update configuration for the domain
	if err := coreApp.UpdateConfigForDomain(
		urlStr,
		flags.jsRendering,
		flags.initialWait,
		flags.scrollWait,
		flags.finalWait,
		flags.parallelism,
		flags.userAgent,
		flags.includeSubdomains,
		flags.spider,
		flags.sitemap,
		sitemapURLs,
		flags.checkExternal,
		flags.robotsTxt,
		flags.followNofollowInternal,
		flags.followNofollowExternal,
		flags.respectMetaNoindex,
		flags.respectNoindex,
	); err != nil {
		return fmt.Errorf("failed to configure crawl: %v", err)
	}

	// Start the crawl
	if !flags.quiet {
		fmt.Printf("Starting crawl for %s...\n", urlStr)
	}

	projectInfo, err := coreApp.StartCrawl(urlStr)
	if err != nil {
		return fmt.Errorf("failed to start crawl: %v", err)
	}

	if !flags.quiet {
		fmt.Printf("Project ID: %d, Crawl ID: %d\n", projectInfo.ID, projectInfo.LatestCrawlID)
		fmt.Printf("Domain: %s\n\n", projectInfo.Domain)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for crawl completion with progress display
	done := make(chan bool, 1)

	go func() {
		waitForCrawlCompletion(coreApp, projectInfo.ID, projectInfo.LatestCrawlID, flags.quiet)
		done <- true
	}()

	select {
	case <-done:
		// Crawl completed
	case sig := <-sigChan:
		if !flags.quiet {
			fmt.Printf("\nReceived %v, stopping crawl...\n", sig)
		}
		if err := coreApp.StopCrawl(projectInfo.ID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stop crawl cleanly: %v\n", err)
		}
		// Wait a moment for cleanup
		time.Sleep(2 * time.Second)
	}

	// Get final stats
	stats, err := coreApp.GetCrawlStats(projectInfo.LatestCrawlID)
	if err != nil {
		return fmt.Errorf("failed to get crawl stats: %v", err)
	}

	if !flags.quiet {
		fmt.Printf("\n\nCrawl completed!\n")
		fmt.Printf("  Total URLs: %d\n", stats.Total)
		fmt.Printf("  HTML pages: %d\n", stats.HTML)
		fmt.Printf("  Images: %d\n", stats.Images)
		fmt.Printf("  JavaScript: %d\n", stats.JavaScript)
		fmt.Printf("  CSS: %d\n", stats.CSS)
		fmt.Printf("  Fonts: %d\n", stats.Fonts)
		fmt.Printf("  Other: %d\n", stats.Others)
	}

	// Export results
	if !flags.quiet {
		fmt.Printf("\nExporting results to %s...\n", flags.output)
	}

	exporter := &Exporter{
		app:         coreApp,
		store:       st,
		crawlID:     projectInfo.LatestCrawlID,
		projectID:   projectInfo.ID,
		domain:      projectInfo.Domain,
		outputDir:   flags.output,
		format:      flags.format,
		exportLinks: flags.exportLinks,
	}

	if err := exporter.Export(); err != nil {
		return fmt.Errorf("failed to export results: %v", err)
	}

	if !flags.quiet {
		fmt.Println("Done!")
	}

	return nil
}

// waitForCrawlCompletion polls for crawl completion and displays progress
func waitForCrawlCompletion(coreApp *app.App, projectID uint, crawlID uint, quiet bool) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastCrawled := 0
	stableCount := 0 // Count how many times the crawled count hasn't changed

	for {
		<-ticker.C

		stats, err := coreApp.GetCrawlStats(crawlID)
		if err != nil {
			continue
		}

		if !quiet {
			fmt.Printf("\rCrawled: %d | Total discovered: %d | HTML: %d | Images: %d | JS: %d | CSS: %d",
				stats.Crawled, stats.Total, stats.HTML, stats.Images, stats.JavaScript, stats.CSS)
		}

		// Check if crawl is complete (no new URLs crawled for 5 seconds)
		if stats.Crawled == lastCrawled {
			stableCount++
			if stableCount >= 5 && stats.Queued == 0 {
				return
			}
		} else {
			stableCount = 0
			lastCrawled = stats.Crawled
		}
	}
}
