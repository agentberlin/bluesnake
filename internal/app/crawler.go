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
	"encoding/json"
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
	"github.com/agentberlin/bluesnake/internal/framework"
	"github.com/agentberlin/bluesnake/internal/store"
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
	startTime       time.Time
	pagesCrawled    int
	totalDiscovered int // Total unique URLs discovered (from bluesnake)
	url             string
	domain          string
	projectID       uint
	crawlID         uint
	// Track discovered vs crawled URLs for UI display
	discoveredURLs *sync.Map // URLs discovered but not yet crawled (from bluesnake)
	crawledURLs    *sync.Map // URLs that have been crawled
}

// StartCrawl initiates a crawl for the given URL
func (a *App) StartCrawl(urlStr string) error {
	// Normalize the URL
	normalizedURL, domain, err := normalizeURL(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Parse the normalized URL
	parsedURL, err := url.Parse(normalizedURL)
	if err != nil {
		return fmt.Errorf("failed to parse normalized URL: %v", err)
	}

	// Get or create project to check if already crawling
	project, err := a.store.GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return fmt.Errorf("failed to get/create project: %v", err)
	}

	// Check if this project is already being crawled
	a.crawlsMutex.RLock()
	_, alreadyCrawling := a.activeCrawls[project.ID]
	a.crawlsMutex.RUnlock()

	if alreadyCrawling {
		return fmt.Errorf("crawl already in progress for this project")
	}

	// Run crawler in a goroutine to not block the UI
	go a.runCrawler(parsedURL, normalizedURL, domain, project.ID)

	return nil
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
	// Escape special regex characters in the domain
	escapedDomain := regexp.QuoteMeta(domain)

	var pattern string
	if includeSubdomains {
		// Match domain or any subdomain: (.*\.)?example\.com
		// Remove port if present for pattern matching
		domainWithoutPort := strings.Split(escapedDomain, ":")[0]
		pattern = fmt.Sprintf(`^https?://(.*\.)?%s(/|$|\?)`, domainWithoutPort)
	} else {
		// Match exact domain only: example\.com
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
	if strings.HasPrefix(fullPath, "/") {
		fullPath = fullPath[1:]
	}

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

func (a *App) runCrawler(parsedURL *url.URL, normalizedURL string, domain string, projectID uint) {
	// Create a new crawl
	crawl, err := a.store.CreateCrawl(projectID, time.Now().Unix(), 0, 0)
	if err != nil {
		log.Printf("Failed to create crawl: %v", err)
		return
	}

	// Initialize crawl stats
	stats := &crawlStats{
		startTime:       time.Now(),
		pagesCrawled:    0,
		totalDiscovered: 0,
		url:             normalizedURL,
		domain:          domain,
		projectID:       projectID,
		crawlID:         crawl.ID,
		discoveredURLs:  &sync.Map{}, // Initialize for tracking discovered URLs
		crawledURLs:     &sync.Map{}, // Initialize for tracking crawled URLs
	}

	// Create cancellation context
	crawlCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register this crawl as active
	stopChan := make(chan struct{}, 1)
	activeCrawlInfo := &activeCrawl{
		projectID:   projectID,
		crawlID:     crawl.ID,
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
			UserAgent:              "bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)",
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

	// Determine MaxDepth based on SinglePageMode
	maxDepth := 0 // 0 means unlimited depth (default)
	if config.SinglePageMode {
		maxDepth = 1 // Depth 1 = only crawl the starting URL, don't follow links
	}

	// Build crawler configuration based on database config
	crawlerConfig := &bluesnake.CollectorConfig{
		Context:             crawlCtx, // Pass context for proper cancellation support
		UserAgent:           config.UserAgent,
		MaxDepth:            maxDepth, // Set depth based on SinglePageMode
		URLFilters:          []*regexp.Regexp{domainFilter}, // Use URLFilters instead of AllowedDomains
		Async:               true,
		EnableRendering:     config.JSRenderingEnabled,
		DiscoveryMechanisms: mechanisms,
		SitemapURLs:         config.GetSitemapURLsArray(),
		ResourceValidation: &bluesnake.ResourceValidationConfig{
			Enabled:       true,
			ResourceTypes: []string{"image", "script", "stylesheet"},
			CheckExternal: config.CheckExternalResources,
		},
	}

	// Create the high-level crawler
	crawler := bluesnake.NewCrawler(crawlerConfig)

	// Apply parallelism limit to the underlying collector
	if config.Parallelism > 0 {
		crawler.Collector.Limit(&bluesnake.LimitRule{
			DomainGlob:  "*",
			Parallelism: config.Parallelism,
		})
	}

	// Framework detection and filtering
	a.setupFrameworkDetection(crawler, projectID, domain)

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
		if err := a.store.SaveCrawledUrl(stats.crawlID, result.URL, result.Status, "", "", "", indexable, result.ContentType, result.Error); err != nil {
			log.Printf("Failed to save resource URL: %v", err)
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
			stats.pagesCrawled++
		}

		// Save to database - all crawling logic handled by bluesnake
		if err := a.store.SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.MetaDescription, result.ContentHash, result.Indexable, result.ContentType, result.Error); err != nil {
			log.Printf("Failed to save crawled URL: %v", err)
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
				})
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
				})
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
	crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
		// Store the total discovered count from bluesnake
		stats.totalDiscovered = totalDiscovered

		// Calculate crawl duration
		crawlDuration := time.Since(stats.startTime).Milliseconds()

		// Update crawl statistics in database
		if err := a.store.UpdateCrawlStats(stats.crawlID, crawlDuration, stats.pagesCrawled); err != nil {
			log.Printf("Failed to update crawl stats: %v", err)
		}

		// Send appropriate completion event (indicational only, no payload)
		if wasStopped {
			a.emitter.Emit(EventCrawlStopped, nil)
			log.Printf("Crawl stopped for project %d", projectID)
		} else {
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

// setupFrameworkDetection sets up framework detection and applies filtering rules
// If framework is already known for this domain, apply filters immediately
// Otherwise, apply analytics filters initially, then detect on first page and add framework filters
func (a *App) setupFrameworkDetection(crawler *bluesnake.Crawler, projectID uint, domain string) {
	// Check if we already have a framework detected for this domain
	domainFW, err := a.store.GetDomainFramework(projectID, domain)

	if err == nil && domainFW != nil && domainFW.Framework != "" {
		// Framework already known, apply filters immediately
		log.Printf("Using known framework '%s' for domain '%s'", domainFW.Framework, domain)
		a.applyFrameworkFilters(crawler, framework.Framework(domainFW.Framework))
		return
	}

	// No framework detected yet, apply analytics filters immediately
	log.Printf("No framework known for domain '%s', will auto-detect", domain)
	analyticsPatterns := getAnalyticsFilterPatterns()
	crawler.SetCustomFilters(analyticsPatterns, []string{})

	// Use a sync.Once to ensure we only detect once
	var detectOnce sync.Once

	// Hook into OnHTML to detect framework on first page
	crawler.Collector.OnHTML("html", func(e *bluesnake.HTMLElement) {
		detectOnce.Do(func() {
			// Get HTML content
			html, _ := e.DOM.Html()

			// Get network-discovered URLs if available (from JS rendering)
			var networkURLs []string
			if networkURLsJSON := e.Request.Ctx.Get("networkDiscoveredURLs"); networkURLsJSON != "" {
				json.Unmarshal([]byte(networkURLsJSON), &networkURLs)
			}

			// Detect framework
			detector := framework.NewDetector()
			detectedFW := detector.Detect(html, networkURLs)

			log.Printf("Detected framework '%s' for domain '%s'", detectedFW, domain)

			// Save to database (manuallySet = false for auto-detection)
			if err := a.store.SaveDomainFramework(projectID, domain, string(detectedFW), false); err != nil {
				log.Printf("Failed to save detected framework: %v", err)
			}

			// Apply framework-specific filters for the rest of the crawl
			// (this will also re-apply analytics filters, which is fine)
			a.applyFrameworkFilters(crawler, detectedFW)
		})
	})
}

// getAnalyticsFilterPatterns returns common analytics and tracking URL patterns
// that should be filtered during crawling
func getAnalyticsFilterPatterns() []string {
	return []string{
		"/g/collect",        // Google Analytics
		"/gtm.js",           // Google Tag Manager
		"/gtag/js",          // Google Global Site Tag
		"/analytics.js",     // Generic analytics
		"/ga.js",            // Google Analytics legacy
		"google-analytics",  // Google Analytics domain
		"googletagmanager",  // Tag Manager domain
		"/pixel",            // Tracking pixels
		"/track",            // Generic tracking
		"/beacon",           // Beacon API
		"/telemetry",        // Telemetry endpoints
	}
}

// applyFrameworkFilters applies framework-specific and analytics filtering rules to the crawler
func (a *App) applyFrameworkFilters(crawler *bluesnake.Crawler, fw framework.Framework) {
	config := framework.GetFilterConfig(fw)

	// Start with analytics patterns (always filter these)
	allPatterns := append([]string{}, getAnalyticsFilterPatterns()...)

	// Add framework-specific URL patterns
	allPatterns = append(allPatterns, config.URLPatterns...)

	// Also add query params to patterns (e.g., "_rsc=" for detection in URLs)
	for _, param := range config.QueryParams {
		allPatterns = append(allPatterns, param+"=")
	}

	// Query params include only framework-specific params (analytics patterns are URL-based)
	queryParams := config.QueryParams

	log.Printf("Applying filters for '%s': %d URL patterns, %d query params",
		fw, len(allPatterns), len(queryParams))
	crawler.SetCustomFilters(allPatterns, queryParams)
}
