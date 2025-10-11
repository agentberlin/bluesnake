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
	"sync"
	"time"

	"github.com/agentberlin/bluesnake"
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
			JSRenderingEnabled:  false,
			Parallelism:         5,
			UserAgent:           "bluesnake/1.0 (+https://github.com/agentberlin/bluesnake)",
			DiscoveryMechanisms: "[\"spider\"]",
			SitemapURLs:         "",
		}
	}

	// Convert discovery mechanisms to bluesnake types
	mechanisms := []bluesnake.DiscoveryMechanism{}
	for _, m := range config.GetDiscoveryMechanismsArray() {
		mechanisms = append(mechanisms, bluesnake.DiscoveryMechanism(m))
	}

	// Build crawler configuration based on database config
	crawlerConfig := &bluesnake.CollectorConfig{
		Context:             crawlCtx, // Pass context for proper cancellation support
		UserAgent:           config.UserAgent,
		AllowedDomains:      []string{domain},
		Async:               true,
		EnableRendering:     config.JSRenderingEnabled,
		DiscoveryMechanisms: mechanisms,
		SitemapURLs:         config.GetSitemapURLsArray(),
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

	// Add the starting URL to discovered URLs so it shows up in the UI immediately
	stats.discoveredURLs.Store(parsedURL.String(), true)

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
		if err := a.store.SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, result.Error); err != nil {
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
