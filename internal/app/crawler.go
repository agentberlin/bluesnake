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

package app

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/types"
)

// activeCrawl tracks an ongoing crawl
type activeCrawl struct {
	projectID   uint
	crawlID     uint
	domain      string
	url         string
	cancel      context.CancelFunc
	stopChan    chan struct{} // Signal to stop the crawl
	stopped     bool          // Whether the crawl was stopped
	stats       *crawlStats
	statusMutex sync.RWMutex // Protects stats reads/writes
}

// crawlStats tracks crawl statistics for the desktop app
type crawlStats struct {
	startTime        time.Time
	pagesCrawled     int // HTML pages crawled successfully
	totalURLsCrawled int // Total URLs crawled (including resources like images, CSS, JS, fonts)
	totalDiscovered  int // Total unique URLs discovered (from bluesnake)
	url              string
	domain           string
	projectID        uint
	crawlID          uint
	// Track discovered vs crawled URLs for UI display
	discoveredURLs *sync.Map // URLs discovered but not yet crawled (from bluesnake)
	crawledURLs    *sync.Map // URLs that have been crawled
}

// StartCrawl initiates a crawl for the given URL and returns the project info.
// The returned ProjectInfo contains the canonical domain (after following redirects),
// which may differ from the input URL (e.g., amahahealth.com -> www.amahahealth.com).
func (a *App) StartCrawl(urlStr string) (*types.ProjectInfo, error) {
	// Resolve redirects to get the canonical URL
	// e.g., amahahealth.com -> www.amahahealth.com
	resolvedURL := resolveURL(urlStr)

	// Normalize the resolved URL
	normalizedURL, domain, err := normalizeURL(resolvedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	// Parse the normalized URL
	parsedURL, err := url.Parse(normalizedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse normalized URL: %v", err)
	}

	// Get or create project using the canonical domain
	project, err := a.store.GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create project: %v", err)
	}

	// Check if this project is already being crawled
	a.crawlsMutex.RLock()
	_, alreadyCrawling := a.activeCrawls[project.ID]
	a.crawlsMutex.RUnlock()

	if alreadyCrawling {
		return nil, fmt.Errorf("crawl already in progress for this project")
	}

	// Get configuration to check if incremental crawling is enabled
	config, err := a.store.GetOrCreateConfig(project.ID, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %v", err)
	}

	var crawl *store.Crawl
	var runID *uint

	if config.IncrementalCrawlingEnabled {
		// Check for paused run to auto-resume
		pausedRun, err := a.store.GetPausedRun(project.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to check for paused run: %v", err)
		}

		if pausedRun != nil {
			// Auto-resume: mark run as in_progress, create crawl under it
			if err := a.store.UpdateRunState(pausedRun.ID, store.RunStateInProgress); err != nil {
				return nil, fmt.Errorf("failed to update run state: %v", err)
			}
			crawl, err = a.store.CreateCrawlWithRun(project.ID, pausedRun.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to create crawl: %v", err)
			}
			runID = &pausedRun.ID

			// Run crawler with resume (uses existing queue)
			go a.runCrawlerWithResume(parsedURL, normalizedURL, domain, project.ID, crawl.ID, runID)
		} else {
			// New run: clear queue, create run + crawl
			if err := a.store.ClearQueue(project.ID); err != nil {
				log.Printf("Failed to clear queue for fresh crawl: %v", err)
			}
			run, err := a.store.CreateIncrementalRun(project.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to create run: %v", err)
			}
			crawl, err = a.store.CreateCrawlWithRun(project.ID, run.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to create crawl: %v", err)
			}
			runID = &run.ID

			// Run crawler fresh (clears queue first)
			go a.runCrawler(parsedURL, normalizedURL, domain, project.ID, crawl.ID, runID)
		}
	} else {
		// Non-incremental crawl: create crawl without run
		crawl, err = a.store.CreateCrawl(project.ID, time.Now().Unix(), 0, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to create crawl: %v", err)
		}

		// Run crawler without run tracking
		go a.runCrawler(parsedURL, normalizedURL, domain, project.ID, crawl.ID, nil)
	}

	// Return project info so frontend knows which project was created
	// Include the crawl ID so frontend can immediately start tracking it
	return &types.ProjectInfo{
		ID:            project.ID,
		URL:           normalizedURL,
		Domain:        domain,
		LatestCrawlID: crawl.ID,
	}, nil
}

// StopCrawl stops an active crawl for a specific project
func (a *App) StopCrawl(projectID uint) error {
	a.crawlsMutex.Lock()
	defer a.crawlsMutex.Unlock()

	ac, exists := a.activeCrawls[projectID]
	if !exists {
		return fmt.Errorf("no active crawl found for project %d", projectID)
	}

	// Mark as stopped
	ac.statusMutex.Lock()
	ac.stopped = true
	ac.statusMutex.Unlock()

	// Close the stop channel to signal all goroutines
	close(ac.stopChan)
	log.Printf("Stop signal sent for project %d", projectID)

	// Also cancel the context for cleanup
	if ac.cancel != nil {
		ac.cancel()
	}

	return nil
}

// buildDomainFilter creates a regex filter for domain matching based on includeSubdomains flag.
// If includeSubdomains is true, it matches the domain and all its subdomains.
// If includeSubdomains is false, it matches only the exact domain.
// Examples for domain "example.com":
//   - includeSubdomains=false: matches "example.com" but not "blog.example.com"
//   - includeSubdomains=true: matches "example.com", "blog.example.com", "api.example.com", etc.
func buildDomainFilter(domain string, includeSubdomains bool) (*regexp.Regexp, error) {
	var pattern string
	if includeSubdomains {
		// Match domain or any subdomain: (.*\.)?example\.com
		// If domain has a port, keep it in the pattern
		parts := strings.Split(domain, ":")
		domainWithoutPort := parts[0]
		escapedDomain := regexp.QuoteMeta(domainWithoutPort)

		if len(parts) > 1 {
			// Domain has a port, include it in the pattern
			port := parts[1]
			pattern = fmt.Sprintf(`^https?://(.*\.)?%s:%s(/|$|\?)`, escapedDomain, port)
		} else {
			// No port in domain
			pattern = fmt.Sprintf(`^https?://(.*\.)?%s(/|$|\?)`, escapedDomain)
		}
	} else {
		// Match exact domain only: example\.com or example\.com:port
		escapedDomain := regexp.QuoteMeta(domain)
		pattern = fmt.Sprintf(`^https?://%s(/|$|\?)`, escapedDomain)
	}

	return regexp.Compile(pattern)
}

// sanitizeURLToFilename converts a URL to a disk-safe filename
// Replaces non-disk-friendly characters with underscores
// Example: "https://example.com/blog/post-1?page=2" -> "blog_post-1_page_2.txt"
func sanitizeURLToFilename(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		// If URL parsing fails, use a hash-based fallback
		return fmt.Sprintf("url_%d.txt", len(urlStr))
	}

	// Get the path and query from URL
	path := parsedURL.Path
	query := parsedURL.RawQuery

	// Combine path and query
	fullPath := path
	if query != "" {
		fullPath = path + "?" + query
	}

	// Handle root path
	if fullPath == "" || fullPath == "/" {
		return "index.txt"
	}

	// Remove leading slash
	fullPath = strings.TrimPrefix(fullPath, "/")

	// Replace non-disk-friendly characters with underscores
	// Characters to replace: / ? = & # : * " < > | spaces
	replacer := strings.NewReplacer(
		"/", "_",
		"?", "_",
		"=", "_",
		"&", "_",
		"#", "_",
		":", "_",
		"*", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
	)

	sanitized := replacer.Replace(fullPath)

	// Add .txt extension if not already present
	if !strings.HasSuffix(sanitized, ".txt") {
		sanitized = sanitized + ".txt"
	}

	return sanitized
}

// saveContentToDisk saves the text content of a page to disk
// Content is saved to ~/.bluesnake/<domain>/<crawlid>/<sanitized-url>.txt
func saveContentToDisk(domain string, crawlID uint, pageURL string, content string) error {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Create directory structure: ~/.bluesnake/<domain>/<crawlid>/
	contentDir := filepath.Join(homeDir, ".bluesnake", domain, fmt.Sprintf("%d", crawlID))
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		return fmt.Errorf("failed to create content directory: %v", err)
	}

	// Generate filename from URL
	filename := sanitizeURLToFilename(pageURL)
	filePath := filepath.Join(contentDir, filename)

	// Write content to file
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write content file: %v", err)
	}

	return nil
}

func (a *App) runCrawler(parsedURL *url.URL, normalizedURL string, domain string, projectID uint, crawlID uint, runID *uint) {
	// Initialize crawl stats
	stats := &crawlStats{
		startTime:        time.Now(),
		pagesCrawled:     0,
		totalURLsCrawled: 0,
		totalDiscovered:  0,
		url:              normalizedURL,
		domain:           domain,
		projectID:        projectID,
		crawlID:          crawlID,
		discoveredURLs:   &sync.Map{}, // Initialize for tracking discovered URLs
		crawledURLs:      &sync.Map{}, // Initialize for tracking crawled URLs
	}

	// Create cancellation context
	crawlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register this crawl as active
	stopChan := make(chan struct{}, 1)
	activeCrawlInfo := &activeCrawl{
		projectID:   projectID,
		crawlID:     crawlID,
		domain:      domain,
		url:         normalizedURL,
		cancel:      cancel,
		stopChan:    stopChan,
		stats:       stats,
		statusMutex: sync.RWMutex{},
	}

	a.crawlsMutex.Lock()
	a.activeCrawls[projectID] = activeCrawlInfo
	a.crawlsMutex.Unlock()

	// Clean up when done
	defer func() {
		a.crawlsMutex.Lock()
		delete(a.activeCrawls, projectID)
		a.crawlsMutex.Unlock()
	}()

	// Get configuration for this project
	config, err := a.store.GetOrCreateConfig(projectID, domain)
	if err != nil {
		log.Printf("Failed to get config for project %d: %v", projectID, err)
		// Use defaults if config retrieval fails
		config = &store.Config{
			JSRenderingEnabled:     false,
			Parallelism:            5,
			UserAgent:              "bluesnake/1.0 (+https://snake.blue)",
			IncludeSubdomains:      true, // Default to including subdomains
			DiscoveryMechanisms:    "[\"spider\"]",
			SitemapURLs:            "",
			CheckExternalResources: true, // Default to checking external resources
		}
	}

	// Convert discovery mechanisms to bluesnake types
	mechanisms := []bluesnake.DiscoveryMechanism{}
	for _, m := range config.GetDiscoveryMechanismsArray() {
		mechanisms = append(mechanisms, bluesnake.DiscoveryMechanism(m))
	}

	// Build domain filter based on IncludeSubdomains setting
	domainFilter, err := buildDomainFilter(domain, config.IncludeSubdomains)
	if err != nil {
		log.Printf("Failed to build domain filter: %v", err)
		return
	}

	// For incremental crawling, add the initial URL to the queue
	// Note: Queue clearing is handled in StartCrawl when starting a new run
	if config.IncrementalCrawlingEnabled {
		if err := a.store.AddSingleToQueue(projectID, parsedURL.String(), int64(bluesnake.URLHash(parsedURL.String())), "initial", 0); err != nil {
			log.Printf("Failed to add initial URL to queue: %v", err)
		}
	}

	// Build crawler configuration based on database config
	crawlerConfig := &bluesnake.CrawlerConfig{
		MaxDepth:            0,                              // 0 means unlimited depth
		URLFilters:          []*regexp.Regexp{domainFilter}, // Use URLFilters instead of AllowedDomains
		DiscoveryMechanisms: mechanisms,
		SitemapURLs:         config.GetSitemapURLsArray(),
		ResourceValidation: &bluesnake.ResourceValidationConfig{
			Enabled:       true,
			ResourceTypes: []string{"image", "script", "stylesheet", "font"},
			CheckExternal: config.CheckExternalResources,
		},
		HTTP: &bluesnake.HTTPConfig{
			UserAgent:       config.UserAgent,
			EnableRendering: config.JSRenderingEnabled,
		},
		// Incremental crawling settings
		MaxURLsToVisit: config.CrawlBudget,
	}

	// Create the high-level crawler with context
	crawler := bluesnake.NewCrawler(crawlCtx, crawlerConfig)

	// Apply parallelism limit to the underlying collector
	if config.Parallelism > 0 {
		crawler.Collector.Limit(&bluesnake.LimitRule{
			DomainGlob:  "*",
			Parallelism: config.Parallelism,
		})
	}

	// Set up URL discovery handler for categorization
	a.setupURLDiscoveryHandler(crawler)

	// Add the starting URL to discovered URLs so it shows up in the UI immediately
	stats.discoveredURLs.Store(parsedURL.String(), true)

	// Set up callback for resource visits (images, CSS, JS, etc.)
	crawler.SetOnResourceVisit(func(result *bluesnake.ResourceResult) {
		// Check if crawl was stopped
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't process
		default:
		}

		// Save resource to database (same table as pages, but won't count as "page crawled")
		// Status 0 means error/unreachable
		indexable := "-" // Resources are not indexable by search engines
		if err := a.store.SaveDiscoveredUrl(stats.crawlID, result.URL, true, result.Status, "", "", "", "", "", 0, "", indexable, result.ContentType, result.Error, result.Depth); err != nil {
			log.Printf("Failed to save resource URL: %v", err)
		}

		// Count successful resource fetches toward totalURLsCrawled
		if result.Error == "" {
			stats.totalURLsCrawled++
		}

		// For incremental crawling, add resource URL to queue and mark as visited
		if config.IncrementalCrawlingEnabled {
			if err := a.store.AddAndMarkVisited(projectID, result.URL, int64(bluesnake.URLHash(result.URL)), "resource"); err != nil {
				log.Printf("Failed to add/mark resource URL visited in queue: %v", err)
			}
		}

		// Note: Resources are NOT counted toward pagesCrawled stat
		// They're tracked separately for resource validation purposes
	})

	// Set up callback for individual page results
	crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
		// Check if crawl was stopped
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't process
		default:
		}

		// Mark this URL as crawled (for UI tracking)
		stats.crawledURLs.Store(result.URL, true)

		// Track all discovered URLs from this page
		// Add internal links (these are the crawlable URLs we discover)
		if result.Links != nil {
			for _, link := range result.Links.Internal {
				stats.discoveredURLs.Store(link.URL, true)
			}
		}

		// Only count successful crawls (not errors)
		if result.Error == "" {
			stats.pagesCrawled++     // HTML pages only
			stats.totalURLsCrawled++ // All URLs including pages
		}

		// Save to database - all crawling logic handled by bluesnake
		if err := a.store.SaveDiscoveredUrl(stats.crawlID, result.URL, true, result.Status, result.Title, result.MetaDescription, result.H1, result.H2, result.CanonicalURL, result.WordCount, result.ContentHash, result.Indexable, result.ContentType, result.Error, result.Depth); err != nil {
			log.Printf("Failed to save crawled URL: %v", err)
		}

		// For incremental crawling, add URL to queue and mark as visited, then add discovered URLs
		// Using AddAndMarkVisited ensures ALL crawled URLs are tracked (sitemap, redirects, etc.)
		if config.IncrementalCrawlingEnabled {
			if err := a.store.AddAndMarkVisited(projectID, result.URL, int64(bluesnake.URLHash(result.URL)), "crawled"); err != nil {
				log.Printf("Failed to add/mark URL visited in queue: %v", err)
			}

			// Add discovered URLs to queue
			if result.Links != nil {
				var queueItems []store.CrawlQueueItem
				for _, link := range result.Links.Internal {
					if link.Action == bluesnake.URLActionCrawl {
						queueItems = append(queueItems, store.CrawlQueueItem{
							ProjectID: projectID,
							URL:       link.URL,
							URLHash:   int64(bluesnake.URLHash(link.URL)),
							Source:    "spider",
							Depth:     0,
							Visited:   false,
						})
					}
				}
				if len(queueItems) > 0 {
					if err := a.store.AddToQueue(projectID, queueItems); err != nil {
						log.Printf("Failed to add discovered URLs to queue: %v", err)
					}
				}
			}
		}

		// Save page links to database
		if result.Links != nil {
			// Convert bluesnake links to database format
			var outboundLinks []store.PageLinkData

			// Combine internal and external links
			for _, link := range result.Links.Internal {
				status := 0
				if link.Status != nil {
					status = *link.Status
				}
				outboundLinks = append(outboundLinks, store.PageLinkData{
					URL:         link.URL,
					Type:        link.Type,
					Text:        link.Text,
					Context:     link.Context,
					IsInternal:  link.IsInternal,
					Status:      status,
					Title:       link.Title,
					ContentType: link.ContentType,
					Position:    link.Position,
					DOMPath:     link.DOMPath,
					URLAction:   string(link.Action),
					Follow:      link.Follow,
					Rel:         link.Rel,
					Target:      link.Target,
					PathType:    link.PathType,
				})

				// If this is an unvisited URL (URLAction="record"), save it to DiscoveredUrl table
				if link.Action == bluesnake.URLActionRecordOnly {
					// Save to DiscoveredUrl with visited=false
					indexable := "-" // Unvisited URLs don't have indexability info
					if err := a.store.SaveDiscoveredUrl(stats.crawlID, link.URL, false, status, link.Title, "", "", "", "", 0, "", indexable, link.ContentType, "", result.Depth+1); err != nil {
						log.Printf("Failed to save unvisited URL: %v", err)
					}
				}
			}
			for _, link := range result.Links.External {
				status := 0
				if link.Status != nil {
					status = *link.Status
				}
				outboundLinks = append(outboundLinks, store.PageLinkData{
					URL:         link.URL,
					Type:        link.Type,
					Text:        link.Text,
					Context:     link.Context,
					IsInternal:  link.IsInternal,
					Status:      status,
					Title:       link.Title,
					ContentType: link.ContentType,
					Position:    link.Position,
					DOMPath:     link.DOMPath,
					URLAction:   string(link.Action),
					Follow:      link.Follow,
					Rel:         link.Rel,
					Target:      link.Target,
					PathType:    link.PathType,
				})

				// If this is an unvisited URL (URLAction="record"), save it to DiscoveredUrl table
				if link.Action == bluesnake.URLActionRecordOnly {
					// Save to DiscoveredUrl with visited=false
					indexable := "-" // Unvisited URLs don't have indexability info
					if err := a.store.SaveDiscoveredUrl(stats.crawlID, link.URL, false, status, link.Title, "", "", "", "", 0, "", indexable, link.ContentType, "", result.Depth+1); err != nil {
						log.Printf("Failed to save unvisited URL: %v", err)
					}
				}
			}

			// Save outbound links
			if err := a.store.SavePageLinks(stats.crawlID, result.URL, outboundLinks, nil); err != nil {
				log.Printf("Failed to save page links: %v", err)
			}
		}

		// Save text content to disk (only for successful HTML crawls)
		if result.Error == "" && strings.Contains(result.ContentType, "text/html") {
			textContent := result.GetTextContent()
			if textContent != "" {
				if err := saveContentToDisk(domain, stats.crawlID, result.URL, textContent); err != nil {
					// Log error but don't fail the crawl
					log.Printf("Failed to save content for %s: %v", result.URL, err)
				}
			}
		}
	})

	// Set up callback for crawl completion
	// This is the single source of truth for all crawl/run state changes
	crawler.SetOnCrawlComplete(func(result *bluesnake.CrawlResult) {
		stats.totalDiscovered = result.TotalDiscovered
		crawlDuration := time.Since(stats.startTime).Milliseconds()

		// Handle based on completion reason
		switch result.Reason {
		case bluesnake.CompletionReasonBudgetReached:
			// Budget was hit - save pending URLs and pause run for resume
			if len(result.PendingURLs) > 0 {
				var queueItems []store.CrawlQueueItem
				for _, pending := range result.PendingURLs {
					queueItems = append(queueItems, store.CrawlQueueItem{
						ProjectID: projectID,
						URL:       pending.URL,
						URLHash:   int64(bluesnake.URLHash(pending.URL)),
						Source:    pending.Source,
						Depth:     pending.Depth,
						Visited:   false,
					})
				}
				if err := a.store.AddToQueue(projectID, queueItems); err != nil {
					log.Printf("Failed to save pending URLs to queue: %v", err)
				}
			}

			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateCompleted); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				if err := a.store.UpdateRunState(*runID, store.RunStatePaused); err != nil {
					log.Printf("Failed to update run state to paused: %v", err)
				}
			}
			a.emitter.Emit(EventCrawlStopped, nil)
			log.Printf("Crawl completed (budget hit) for project %d, run paused", projectID)

		case bluesnake.CompletionReasonCancelled:
			// Manually stopped - crawl failed, run paused if pending URLs exist
			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateFailed); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				queueStats, err := a.store.GetQueueStats(projectID)
				if err == nil && queueStats.Pending > 0 {
					if err := a.store.UpdateRunState(*runID, store.RunStatePaused); err != nil {
						log.Printf("Failed to update run state to paused: %v", err)
					}
					log.Printf("Crawl stopped for project %d, run paused (pending URLs exist)", projectID)
				} else {
					if err := a.store.UpdateRunState(*runID, store.RunStateCompleted); err != nil {
						log.Printf("Failed to update run state to completed: %v", err)
					}
					log.Printf("Crawl stopped for project %d, run completed (no pending URLs)", projectID)
				}
			} else {
				log.Printf("Crawl stopped for project %d", projectID)
			}
			a.emitter.Emit(EventCrawlStopped, nil)

		case bluesnake.CompletionReasonExhausted:
			// Normal completion - all URLs crawled, both crawl and run completed
			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateCompleted); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				if err := a.store.UpdateRunState(*runID, store.RunStateCompleted); err != nil {
					log.Printf("Failed to update run state to completed: %v", err)
				}
			}
			a.emitter.Emit(EventCrawlCompleted, nil)
			log.Printf("Crawl completed normally for project %d", projectID)
		}
	})

	// Send crawl start event (indicational only, no payload)
	a.emitter.Emit(EventCrawlStarted, nil)

	// Start the crawl
	if err := crawler.Start(parsedURL.String()); err != nil {
		log.Printf("Failed to start crawl: %v", err)
		return
	}

	// Monitor for stop signals while crawling
	done := make(chan bool, 1)
	go func() {
		crawler.Wait()
		done <- true
	}()

	// Wait for either completion or stop signal
	select {
	case <-done:
		// Crawl completed normally (Wait() already called the completion callback)
		log.Printf("Crawl wait completed for project %d", projectID)
	case <-stopChan:
		// Stop requested - cancel the context to signal all goroutines to stop
		log.Printf("Stop signal received for project %d, forcing termination...", projectID)
		cancel()

		// Wait a maximum of 2 seconds for graceful shutdown
		timeout := time.NewTimer(2 * time.Second)
		select {
		case <-done:
			log.Printf("Crawl stopped gracefully for project %d", projectID)
			timeout.Stop()
		case <-timeout.C:
			log.Printf("Crawl force-stopped after timeout for project %d", projectID)
		}
	case <-crawlCtx.Done():
		// Context cancelled externally
		log.Printf("Crawl context cancelled for project %d", projectID)
		// Give a brief moment for cleanup
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
}

// setupURLDiscoveryHandler sets up URL discovery callback for categorizing URLs
func (a *App) setupURLDiscoveryHandler(crawler *bluesnake.Crawler) {
	crawler.SetOnURLDiscovered(func(urlStr string) bluesnake.URLAction {
		// No filtering applied - crawl all URLs
		// Potential filters that could be added in the future:
		// - Analytics: /g/collect, /gtm.js, /gtag/js, google-analytics, googletagmanager
		// - Next.js: /_next/data/, URLs with _rsc query param
		// - Nuxt.js: /_nuxt/
		// - WordPress: /wp-json/, /wp-admin/
		// - Tracking: /pixel, /beacon, /telemetry
		return bluesnake.URLActionCrawl
	})
}

// ResumeCrawl continues a paused run for a project
func (a *App) ResumeCrawl(projectID uint) (*types.ProjectInfo, error) {
	// Get the project
	project, err := a.store.GetProjectByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("project not found: %v", err)
	}

	// Check if there's a paused run
	pausedRun, err := a.store.GetPausedRun(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check for paused run: %v", err)
	}
	if pausedRun == nil {
		return nil, fmt.Errorf("no paused run to resume for this project")
	}

	// Check if there are pending URLs in the queue
	hasPending, err := a.store.HasPendingURLs(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check pending URLs: %v", err)
	}
	if !hasPending {
		return nil, fmt.Errorf("no pending URLs to crawl - queue is empty")
	}

	// Check if this project is already being crawled
	a.crawlsMutex.RLock()
	_, alreadyCrawling := a.activeCrawls[projectID]
	a.crawlsMutex.RUnlock()

	if alreadyCrawling {
		return nil, fmt.Errorf("crawl already in progress for this project")
	}

	// Parse the URL
	parsedURL, err := url.Parse(project.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project URL: %v", err)
	}

	// Mark run as in_progress
	if err := a.store.UpdateRunState(pausedRun.ID, store.RunStateInProgress); err != nil {
		return nil, fmt.Errorf("failed to update run state: %v", err)
	}

	// Create a new crawl record under this run
	crawl, err := a.store.CreateCrawlWithRun(projectID, pausedRun.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create crawl: %v", err)
	}

	// Run the crawler with resume
	runID := pausedRun.ID
	go a.runCrawlerWithResume(parsedURL, project.URL, project.Domain, projectID, crawl.ID, &runID)

	return &types.ProjectInfo{
		ID:            project.ID,
		URL:           project.URL,
		Domain:        project.Domain,
		LatestCrawlID: crawl.ID,
	}, nil
}

// GetQueueStatus returns the status of the crawl queue for a project
func (a *App) GetQueueStatus(projectID uint) (*types.QueueStatus, error) {
	// Get queue stats
	stats, err := a.store.GetQueueStats(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue stats: %v", err)
	}

	// Get last crawl to check state
	lastCrawl, err := a.store.GetLatestCrawl(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest crawl: %v", err)
	}

	var lastCrawlID uint
	var lastState string
	if lastCrawl != nil {
		lastCrawlID = lastCrawl.ID
		lastState = lastCrawl.State
	}

	// Check for paused run (run-based resume logic)
	pausedRun, err := a.store.GetPausedRun(projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check for paused run: %v", err)
	}

	// Can resume if there's a paused run and pending URLs
	canResume := pausedRun != nil && stats.Pending > 0

	// Get run state if there's an active/paused run
	var runState string
	var currentRunID *uint
	if pausedRun != nil {
		runState = pausedRun.State
		currentRunID = &pausedRun.ID
	} else {
		// Check for in-progress run
		inProgressRun, _ := a.store.GetInProgressRun(projectID)
		if inProgressRun != nil {
			runState = inProgressRun.State
			currentRunID = &inProgressRun.ID
		}
	}

	return &types.QueueStatus{
		ProjectID:    projectID,
		HasQueue:     stats.Total > 0,
		Visited:      stats.Visited,
		Pending:      stats.Pending,
		Total:        stats.Total,
		CanResume:    canResume,
		LastCrawlID:  lastCrawlID,
		LastState:    lastState,
		CurrentRunID: currentRunID,
		RunState:     runState,
	}, nil
}

// ClearCrawlQueue removes all URLs from the crawl queue for a project
func (a *App) ClearCrawlQueue(projectID uint) error {
	return a.store.ClearQueue(projectID)
}

// setupCrawlerCallbacks sets up all the crawler callbacks for both fresh and resume crawls
func (a *App) setupCrawlerCallbacks(crawler *bluesnake.Crawler, crawlCtx context.Context, stats *crawlStats, config *store.Config, domain string, projectID uint, crawlID uint, runID *uint) {
	// Set up callback for resource visits
	crawler.SetOnResourceVisit(func(result *bluesnake.ResourceResult) {
		select {
		case <-crawlCtx.Done():
			return
		default:
		}

		indexable := "-"
		if err := a.store.SaveDiscoveredUrl(stats.crawlID, result.URL, true, result.Status, "", "", "", "", "", 0, "", indexable, result.ContentType, result.Error, result.Depth); err != nil {
			log.Printf("Failed to save resource URL: %v", err)
		}

		if result.Error == "" {
			stats.totalURLsCrawled++
		}

		// For incremental crawling, add URL to queue and mark as visited
		// Using AddAndMarkVisited ensures ALL crawled URLs are tracked (including resources)
		if config.IncrementalCrawlingEnabled {
			if err := a.store.AddAndMarkVisited(projectID, result.URL, int64(bluesnake.URLHash(result.URL)), "resource"); err != nil {
				log.Printf("Failed to add/mark resource URL visited in queue: %v", err)
			}
		}
	})

	// Set up callback for page results
	crawler.SetOnPageCrawled(func(result *bluesnake.PageResult) {
		select {
		case <-crawlCtx.Done():
			return
		default:
		}

		stats.crawledURLs.Store(result.URL, true)

		if result.Links != nil {
			for _, link := range result.Links.Internal {
				stats.discoveredURLs.Store(link.URL, true)
			}
		}

		if result.Error == "" {
			stats.pagesCrawled++
			stats.totalURLsCrawled++
		}

		if err := a.store.SaveDiscoveredUrl(stats.crawlID, result.URL, true, result.Status, result.Title, result.MetaDescription, result.H1, result.H2, result.CanonicalURL, result.WordCount, result.ContentHash, result.Indexable, result.ContentType, result.Error, result.Depth); err != nil {
			log.Printf("Failed to save crawled URL: %v", err)
		}

		// For incremental crawling, add URL to queue and mark as visited, then add discovered URLs
		// Using AddAndMarkVisited ensures ALL crawled URLs are tracked (sitemap, redirects, etc.)
		if config.IncrementalCrawlingEnabled {
			if err := a.store.AddAndMarkVisited(projectID, result.URL, int64(bluesnake.URLHash(result.URL)), "crawled"); err != nil {
				log.Printf("Failed to add/mark URL visited in queue: %v", err)
			}

			// Add discovered URLs to queue
			if result.Links != nil {
				var queueItems []store.CrawlQueueItem
				for _, link := range result.Links.Internal {
					if link.Action == bluesnake.URLActionCrawl {
						queueItems = append(queueItems, store.CrawlQueueItem{
							ProjectID: projectID,
							URL:       link.URL,
							URLHash:   int64(bluesnake.URLHash(link.URL)),
							Source:    "spider",
							Depth:     0,
							Visited:   false,
						})
					}
				}
				if len(queueItems) > 0 {
					if err := a.store.AddToQueue(projectID, queueItems); err != nil {
						log.Printf("Failed to add discovered URLs to queue: %v", err)
					}
				}
			}
		}

		// Save page links
		if result.Links != nil {
			var outboundLinks []store.PageLinkData

			for _, link := range result.Links.Internal {
				status := 0
				if link.Status != nil {
					status = *link.Status
				}
				outboundLinks = append(outboundLinks, store.PageLinkData{
					URL:         link.URL,
					Type:        link.Type,
					Text:        link.Text,
					Context:     link.Context,
					IsInternal:  link.IsInternal,
					Status:      status,
					Title:       link.Title,
					ContentType: link.ContentType,
					Position:    link.Position,
					DOMPath:     link.DOMPath,
					URLAction:   string(link.Action),
					Follow:      link.Follow,
					Rel:         link.Rel,
					Target:      link.Target,
					PathType:    link.PathType,
				})

				if link.Action == bluesnake.URLActionRecordOnly {
					indexable := "-"
					if err := a.store.SaveDiscoveredUrl(stats.crawlID, link.URL, false, status, link.Title, "", "", "", "", 0, "", indexable, link.ContentType, "", result.Depth+1); err != nil {
						log.Printf("Failed to save unvisited URL: %v", err)
					}
				}
			}
			for _, link := range result.Links.External {
				status := 0
				if link.Status != nil {
					status = *link.Status
				}
				outboundLinks = append(outboundLinks, store.PageLinkData{
					URL:         link.URL,
					Type:        link.Type,
					Text:        link.Text,
					Context:     link.Context,
					IsInternal:  link.IsInternal,
					Status:      status,
					Title:       link.Title,
					ContentType: link.ContentType,
					Position:    link.Position,
					DOMPath:     link.DOMPath,
					URLAction:   string(link.Action),
					Follow:      link.Follow,
					Rel:         link.Rel,
					Target:      link.Target,
					PathType:    link.PathType,
				})

				if link.Action == bluesnake.URLActionRecordOnly {
					indexable := "-"
					if err := a.store.SaveDiscoveredUrl(stats.crawlID, link.URL, false, status, link.Title, "", "", "", "", 0, "", indexable, link.ContentType, "", result.Depth+1); err != nil {
						log.Printf("Failed to save unvisited URL: %v", err)
					}
				}
			}

			if err := a.store.SavePageLinks(stats.crawlID, result.URL, outboundLinks, nil); err != nil {
				log.Printf("Failed to save page links: %v", err)
			}
		}

		// Save text content to disk
		if result.Error == "" && strings.Contains(result.ContentType, "text/html") {
			textContent := result.GetTextContent()
			if textContent != "" {
				if err := saveContentToDisk(domain, stats.crawlID, result.URL, textContent); err != nil {
					log.Printf("Failed to save content for %s: %v", result.URL, err)
				}
			}
		}
	})

	// Set up callback for crawl completion
	// This is the single source of truth for all crawl/run state changes
	crawler.SetOnCrawlComplete(func(result *bluesnake.CrawlResult) {
		stats.totalDiscovered = result.TotalDiscovered
		crawlDuration := time.Since(stats.startTime).Milliseconds()

		// Handle based on completion reason
		switch result.Reason {
		case bluesnake.CompletionReasonBudgetReached:
			// Budget was hit - save pending URLs and pause run for resume
			if len(result.PendingURLs) > 0 {
				var queueItems []store.CrawlQueueItem
				for _, pending := range result.PendingURLs {
					queueItems = append(queueItems, store.CrawlQueueItem{
						ProjectID: projectID,
						URL:       pending.URL,
						URLHash:   int64(bluesnake.URLHash(pending.URL)),
						Source:    pending.Source,
						Depth:     pending.Depth,
						Visited:   false,
					})
				}
				if err := a.store.AddToQueue(projectID, queueItems); err != nil {
					log.Printf("Failed to save pending URLs to queue: %v", err)
				}
			}

			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateCompleted); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				if err := a.store.UpdateRunState(*runID, store.RunStatePaused); err != nil {
					log.Printf("Failed to update run state to paused: %v", err)
				}
			}
			a.emitter.Emit(EventCrawlStopped, nil)
			log.Printf("Crawl completed (budget hit) for project %d, run paused", projectID)

		case bluesnake.CompletionReasonCancelled:
			// Manually stopped - crawl failed, run paused if pending URLs exist
			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateFailed); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				queueStats, err := a.store.GetQueueStats(projectID)
				if err == nil && queueStats.Pending > 0 {
					if err := a.store.UpdateRunState(*runID, store.RunStatePaused); err != nil {
						log.Printf("Failed to update run state to paused: %v", err)
					}
					log.Printf("Crawl stopped for project %d, run paused (pending URLs exist)", projectID)
				} else {
					if err := a.store.UpdateRunState(*runID, store.RunStateCompleted); err != nil {
						log.Printf("Failed to update run state to completed: %v", err)
					}
					log.Printf("Crawl stopped for project %d, run completed (no pending URLs)", projectID)
				}
			} else {
				log.Printf("Crawl stopped for project %d", projectID)
			}
			a.emitter.Emit(EventCrawlStopped, nil)

		case bluesnake.CompletionReasonExhausted:
			// Normal completion - all URLs crawled, both crawl and run completed
			if err := a.store.UpdateCrawlStatsAndState(stats.crawlID, crawlDuration, stats.pagesCrawled, store.CrawlStateCompleted); err != nil {
				log.Printf("Failed to update crawl stats: %v", err)
			}
			if runID != nil {
				if err := a.store.UpdateRunState(*runID, store.RunStateCompleted); err != nil {
					log.Printf("Failed to update run state to completed: %v", err)
				}
			}
			a.emitter.Emit(EventCrawlCompleted, nil)
			log.Printf("Crawl completed normally for project %d", projectID)
		}
	})
}

// runCrawlerWithResume runs the crawler in resume mode, loading state from the queue
func (a *App) runCrawlerWithResume(parsedURL *url.URL, normalizedURL string, domain string, projectID uint, crawlID uint, runID *uint) {
	stats := &crawlStats{
		startTime:        time.Now(),
		pagesCrawled:     0,
		totalURLsCrawled: 0,
		totalDiscovered:  0,
		url:              normalizedURL,
		domain:           domain,
		projectID:        projectID,
		crawlID:          crawlID,
		discoveredURLs:   &sync.Map{},
		crawledURLs:      &sync.Map{},
	}

	crawlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopChan := make(chan struct{}, 1)
	activeCrawlInfo := &activeCrawl{
		projectID:   projectID,
		crawlID:     crawlID,
		domain:      domain,
		url:         normalizedURL,
		cancel:      cancel,
		stopChan:    stopChan,
		stats:       stats,
		statusMutex: sync.RWMutex{},
	}

	a.crawlsMutex.Lock()
	a.activeCrawls[projectID] = activeCrawlInfo
	a.crawlsMutex.Unlock()

	defer func() {
		a.crawlsMutex.Lock()
		delete(a.activeCrawls, projectID)
		a.crawlsMutex.Unlock()
	}()

	config, err := a.store.GetOrCreateConfig(projectID, domain)
	if err != nil {
		log.Printf("Failed to get config for project %d: %v", projectID, err)
		a.store.UpdateCrawlStatsAndState(crawlID, 0, 0, store.CrawlStateFailed)
		return
	}

	visitedHashesInt64, err := a.store.GetVisitedURLHashes(projectID)
	if err != nil {
		log.Printf("Failed to load visited hashes: %v", err)
		a.store.UpdateCrawlStatsAndState(crawlID, 0, 0, store.CrawlStateFailed)
		return
	}
	// Convert int64 to uint64 for bluesnake (SQLite stores as signed, bluesnake uses unsigned)
	preVisitedHashes := make([]uint64, len(visitedHashesInt64))
	for i, h := range visitedHashesInt64 {
		preVisitedHashes[i] = uint64(h)
	}

	pendingItems, err := a.store.GetPendingURLs(projectID)
	if err != nil {
		log.Printf("Failed to load pending URLs: %v", err)
		a.store.UpdateCrawlStatsAndState(crawlID, 0, 0, store.CrawlStateFailed)
		return
	}

	var seedURLs []bluesnake.URLDiscoveryRequest
	for _, item := range pendingItems {
		seedURLs = append(seedURLs, bluesnake.URLDiscoveryRequest{
			URL:    item.URL,
			Source: item.Source,
			Depth:  item.Depth,
		})
		// Pre-populate discoveredURLs with pending URLs so progress display is accurate
		// Without this, totalDiscovered would only count newly discovered URLs during this session
		stats.discoveredURLs.Store(item.URL, true)
	}

	// Initialize counts from queue stats so progress shows run totals, not just this session
	// This ensures the progress bar starts from where the previous session left off
	queueStats, err := a.store.GetQueueStats(projectID)
	if err == nil {
		stats.totalURLsCrawled = int(queueStats.Visited)
		stats.totalDiscovered = int(queueStats.Total)
	}

	mechanisms := []bluesnake.DiscoveryMechanism{}
	for _, m := range config.GetDiscoveryMechanismsArray() {
		mechanisms = append(mechanisms, bluesnake.DiscoveryMechanism(m))
	}

	domainFilter, err := buildDomainFilter(domain, config.IncludeSubdomains)
	if err != nil {
		log.Printf("Failed to build domain filter: %v", err)
		a.store.UpdateCrawlStatsAndState(crawlID, 0, 0, store.CrawlStateFailed)
		return
	}

	crawlerConfig := &bluesnake.CrawlerConfig{
		MaxDepth:            0,
		URLFilters:          []*regexp.Regexp{domainFilter},
		DiscoveryMechanisms: mechanisms,
		SitemapURLs:         config.GetSitemapURLsArray(),
		ResourceValidation: &bluesnake.ResourceValidationConfig{
			Enabled:       true,
			ResourceTypes: []string{"image", "script", "stylesheet", "font"},
			CheckExternal: config.CheckExternalResources,
		},
		HTTP: &bluesnake.HTTPConfig{
			UserAgent:       config.UserAgent,
			EnableRendering: config.JSRenderingEnabled,
		},
		MaxURLsToVisit:   config.CrawlBudget,
		PreVisitedHashes: preVisitedHashes,
		SeedURLs:         seedURLs,
	}

	crawler := bluesnake.NewCrawler(crawlCtx, crawlerConfig)

	if config.Parallelism > 0 {
		crawler.Collector.Limit(&bluesnake.LimitRule{
			DomainGlob:  "*",
			Parallelism: config.Parallelism,
		})
	}

	a.setupURLDiscoveryHandler(crawler)
	a.setupCrawlerCallbacks(crawler, crawlCtx, stats, config, domain, projectID, crawlID, runID)

	a.emitter.Emit(EventCrawlStarted, nil)

	if err := crawler.Start(parsedURL.String()); err != nil {
		log.Printf("Failed to start resume crawl: %v", err)
		a.store.UpdateCrawlStatsAndState(crawlID, 0, 0, store.CrawlStateFailed)
		return
	}

	done := make(chan bool, 1)
	go func() {
		crawler.Wait()
		done <- true
	}()

	select {
	case <-done:
		log.Printf("Resume crawl completed for project %d", projectID)
	case <-stopChan:
		log.Printf("Stop signal received for resume crawl project %d", projectID)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	case <-crawlCtx.Done():
		log.Printf("Resume crawl context cancelled for project %d", projectID)
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
}
