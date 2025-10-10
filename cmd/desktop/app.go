//go:build desktop

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
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx          context.Context
	activeCrawls map[uint]*activeCrawl // Map of projectID -> active crawl info
	crawlsMutex  sync.RWMutex          // Protects activeCrawls map
}

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
	statusMutex sync.RWMutex  // Protects stats reads/writes
}

// CrawlProgress represents the progress of an active crawl
type CrawlProgress struct {
	ProjectID        uint     `json:"projectId"`
	CrawlID          uint     `json:"crawlId"`
	Domain           string   `json:"domain"`
	URL              string   `json:"url"`
	PagesCrawled     int      `json:"pagesCrawled"`
	TotalDiscovered  int      `json:"totalDiscovered"`  // Total URLs discovered (both crawled and queued)
	DiscoveredURLs   []string `json:"discoveredUrls"`   // URLs discovered but not yet crawled
	IsCrawling       bool     `json:"isCrawling"`
}

// CrawlResult represents a single crawl result
type CrawlResult struct {
	URL        string `json:"url"`
	Status     int    `json:"status"`
	Title      string `json:"title"`
	Indexable  string `json:"indexable"`
	Error      string `json:"error,omitempty"`
}

// ProjectInfo represents project information for the frontend
type ProjectInfo struct {
	ID            uint      `json:"id"`
	URL           string    `json:"url"`
	Domain        string    `json:"domain"`
	FaviconPath   string    `json:"faviconPath"`
	CrawlDateTime int64     `json:"crawlDateTime"`
	CrawlDuration int64     `json:"crawlDuration"`
	PagesCrawled  int       `json:"pagesCrawled"`
	LatestCrawlID uint      `json:"latestCrawlId"`
}

// CrawlInfo represents crawl information for the frontend
type CrawlInfo struct {
	ID            uint   `json:"id"`
	ProjectID     uint   `json:"projectId"`
	CrawlDateTime int64  `json:"crawlDateTime"`
	CrawlDuration int64  `json:"crawlDuration"`
	PagesCrawled  int    `json:"pagesCrawled"`
}

// CrawlResultDetailed represents a crawl with all its URLs
type CrawlResultDetailed struct {
	CrawlInfo CrawlInfo      `json:"crawlInfo"`
	Results   []CrawlResult  `json:"results"`
}

// crawlStats tracks crawl statistics for the desktop app
type crawlStats struct {
	startTime      time.Time
	pagesCrawled   int
	totalDiscovered int // Total unique URLs discovered (from bluesnake)
	url            string
	domain         string
	projectID      uint
	crawlID        uint
	// Track discovered vs crawled URLs for UI display
	discoveredURLs *sync.Map // URLs discovered but not yet crawled (from bluesnake)
	crawledURLs    *sync.Map // URLs that have been crawled
}

// normalizeURL normalizes a URL input and extracts the domain identifier
// Returns: (normalizedURL, domain, error)
func normalizeURL(input string) (string, string, error) {
	// Trim whitespace
	input = strings.TrimSpace(input)

	if input == "" {
		return "", "", fmt.Errorf("empty URL")
	}

	// Add https:// if no protocol is present
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse the URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %v", err)
	}

	// Extract hostname (includes subdomain, excludes port)
	hostname := parsedURL.Hostname()
	if hostname == "" {
		return "", "", fmt.Errorf("no hostname in URL")
	}

	// Convert hostname to lowercase for case-insensitive comparison
	hostname = strings.ToLower(hostname)

	// Build normalized URL
	// Keep port if it's non-standard (not 80 for http, not 443 for https)
	normalizedURL := "https://" + hostname
	if parsedURL.Port() != "" {
		port := parsedURL.Port()
		// Only keep port if it's not the default for https (443)
		if port != "443" {
			normalizedURL = "https://" + hostname + ":" + port
			// Include port in domain identifier for non-standard ports
			hostname = hostname + ":" + port
		}
	}

	return normalizedURL, hostname, nil
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		activeCrawls: make(map[uint]*activeCrawl),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize database
	if err := InitDB(); err != nil {
		runtime.LogErrorf(ctx, "Failed to initialize database: %v", err)
	}
}

// GetActiveCrawls returns the progress of all active crawls
func (a *App) GetActiveCrawls() []CrawlProgress {
	a.crawlsMutex.RLock()
	defer a.crawlsMutex.RUnlock()

	progress := make([]CrawlProgress, 0, len(a.activeCrawls))
	for _, ac := range a.activeCrawls {
		ac.statusMutex.RLock()

		// Get discovered URLs that haven't been crawled yet (from bluesnake callbacks)
		discoveredURLs := []string{}
		totalDiscovered := 0

		if ac.stats.discoveredURLs != nil {
			ac.stats.discoveredURLs.Range(func(key, value interface{}) bool {
				urlStr := key.(string)
				totalDiscovered++

				// Check if this URL has been crawled
				if ac.stats.crawledURLs != nil {
					if _, crawled := ac.stats.crawledURLs.Load(urlStr); !crawled {
						// URL discovered but not yet crawled
						discoveredURLs = append(discoveredURLs, urlStr)
					}
				} else {
					discoveredURLs = append(discoveredURLs, urlStr)
				}
				return true
			})
		}

		progress = append(progress, CrawlProgress{
			ProjectID:       ac.projectID,
			CrawlID:         ac.crawlID,
			Domain:          ac.domain,
			URL:             ac.url,
			PagesCrawled:    ac.stats.pagesCrawled,
			TotalDiscovered: totalDiscovered,
			DiscoveredURLs:  discoveredURLs,
			IsCrawling:      true,
		})
		ac.statusMutex.RUnlock()
	}

	return progress
}

// GetActiveCrawlData returns the data for an active crawl from database
func (a *App) GetActiveCrawlData(projectID uint) (*CrawlResultDetailed, error) {
	a.crawlsMutex.RLock()
	ac, exists := a.activeCrawls[projectID]
	a.crawlsMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active crawl found for project %d", projectID)
	}

	// Read stats
	ac.statusMutex.RLock()
	pagesCrawled := ac.stats.pagesCrawled
	crawlID := ac.crawlID
	ac.statusMutex.RUnlock()

	// Fetch crawled results from database
	urls, err := GetCrawlResults(crawlID)
	if err != nil {
		return nil, err
	}

	// Convert crawled URLs to CrawlResult for frontend
	results := make([]CrawlResult, len(urls))
	for i, u := range urls {
		results[i] = CrawlResult{
			URL:       u.URL,
			Status:    u.Status,
			Title:     u.Title,
			Indexable: u.Indexable,
			Error:     u.Error,
		}
	}

	return &CrawlResultDetailed{
		CrawlInfo: CrawlInfo{
			ID:            crawlID,
			ProjectID:     ac.projectID,
			CrawlDateTime: 0, // Not applicable for active crawl
			CrawlDuration: 0, // Not applicable for active crawl
			PagesCrawled:  pagesCrawled,
		},
		Results: results,
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
	runtime.LogInfof(a.ctx, "Stop signal sent for project %d", projectID)

	// Also cancel the context for cleanup
	if ac.cancel != nil {
		ac.cancel()
	}

	return nil
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
	project, err := GetOrCreateProject(normalizedURL, domain)
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

func (a *App) runCrawler(parsedURL *url.URL, normalizedURL string, domain string, projectID uint) {
	// Create a new crawl
	crawl, err := CreateCrawl(projectID, time.Now().Unix(), 0, 0)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to create crawl: %v", err)
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
	config, err := GetOrCreateConfig(projectID, domain)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to get config for project %d: %v", projectID, err)
		// Use defaults if config retrieval fails
		config = &Config{
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
		Context:              crawlCtx, // Pass context for proper cancellation support
		UserAgent:            config.UserAgent,
		AllowedDomains:       []string{domain},
		Async:                true,
		EnableRendering:      config.JSRenderingEnabled,
		DiscoveryMechanisms:  mechanisms,
		SitemapURLs:          config.GetSitemapURLsArray(),
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

		// Track all discovered URLs from this page (only crawlable ones for UI display)
		for _, discoveredURL := range result.DiscoveredURLs {
			// Only add crawlable URLs to our tracking
			if discoveredURL.IsCrawlable {
				stats.discoveredURLs.Store(discoveredURL.URL, true)
			}
		}

		// Only count successful crawls (not errors)
		if result.Error == "" {
			stats.pagesCrawled++
		}

		// Save to database - all crawling logic handled by bluesnake
		if err := SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, result.Error); err != nil {
			runtime.LogErrorf(a.ctx, "Failed to save crawled URL: %v", err)
		}
	})

	// Set up callback for crawl completion
	crawler.SetOnCrawlComplete(func(wasStopped bool, totalPages int, totalDiscovered int) {
		// Store the total discovered count from bluesnake
		stats.totalDiscovered = totalDiscovered

		// Calculate crawl duration
		crawlDuration := time.Since(stats.startTime).Milliseconds()

		// Update crawl statistics in database
		if err := UpdateCrawlStats(stats.crawlID, crawlDuration, stats.pagesCrawled); err != nil {
			runtime.LogErrorf(a.ctx, "Failed to update crawl stats: %v", err)
		}

		// Send appropriate completion event (indicational only, no payload)
		if wasStopped {
			runtime.EventsEmit(a.ctx, "crawl:stopped")
			runtime.LogInfof(a.ctx, "Crawl stopped for project %d", projectID)
		} else {
			runtime.EventsEmit(a.ctx, "crawl:completed")
			runtime.LogInfof(a.ctx, "Crawl completed normally for project %d", projectID)
		}
	})

	// Send crawl start event (indicational only, no payload)
	runtime.EventsEmit(a.ctx, "crawl:started")

	// Start the crawl
	if err := crawler.Start(parsedURL.String()); err != nil {
		runtime.LogErrorf(a.ctx, "Failed to start crawl: %v", err)
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
		runtime.LogInfof(a.ctx, "Crawl wait completed for project %d", projectID)
	case <-stopChan:
		// Stop requested - cancel the context to signal all goroutines to stop
		runtime.LogInfof(a.ctx, "Stop signal received for project %d, forcing termination...", projectID)
		cancel()

		// Wait a maximum of 2 seconds for graceful shutdown
		timeout := time.NewTimer(2 * time.Second)
		select {
		case <-done:
			runtime.LogInfof(a.ctx, "Crawl stopped gracefully for project %d", projectID)
			timeout.Stop()
		case <-timeout.C:
			runtime.LogInfof(a.ctx, "Crawl force-stopped after timeout for project %d", projectID)
		}
	case <-crawlCtx.Done():
		// Context cancelled externally
		runtime.LogInfof(a.ctx, "Crawl context cancelled for project %d", projectID)
		// Give a brief moment for cleanup
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}
}

// ConfigResponse represents the configuration response for the frontend
type ConfigResponse struct {
	Domain              string   `json:"domain"`
	JSRenderingEnabled  bool     `json:"jsRenderingEnabled"`
	Parallelism         int      `json:"parallelism"`
	UserAgent           string   `json:"userAgent"`
	DiscoveryMechanisms []string `json:"discoveryMechanisms"`
	SitemapURLs         []string `json:"sitemapURLs"`
}

// GetConfigForDomain retrieves the configuration for a specific domain
func (a *App) GetConfigForDomain(urlStr string) (*ConfigResponse, error) {
	// Normalize the URL to extract domain
	normalizedURL, domain, err := normalizeURL(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	// Get or create the project
	project, err := GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %v", err)
	}

	config, err := GetOrCreateConfig(project.ID, domain)
	if err != nil {
		return nil, err
	}

	// Convert to response struct with deserialized arrays
	return &ConfigResponse{
		Domain:              config.Domain,
		JSRenderingEnabled:  config.JSRenderingEnabled,
		Parallelism:         config.Parallelism,
		UserAgent:           config.UserAgent,
		DiscoveryMechanisms: config.GetDiscoveryMechanismsArray(),
		SitemapURLs:         config.GetSitemapURLsArray(),
	}, nil
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(
	urlStr string,
	jsRendering bool,
	parallelism int,
	userAgent string,
	spiderEnabled bool,
	sitemapEnabled bool,
	sitemapURLs []string,
) error {
	// Normalize the URL to extract domain
	normalizedURL, domain, err := normalizeURL(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Get or create the project
	project, err := GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return fmt.Errorf("failed to get project: %v", err)
	}

	// Desktop logic: Always make sitemap additive
	// When sitemap is enabled, ALWAYS include both spider and sitemap
	var mechanisms []string
	if spiderEnabled && !sitemapEnabled {
		mechanisms = []string{"spider"}
	} else if sitemapEnabled {
		// Sitemap mode always includes spider for additive behavior
		mechanisms = []string{"spider", "sitemap"}
	} else {
		// At least one must be enabled (should be validated in frontend)
		// Default to spider if somehow both are false
		mechanisms = []string{"spider"}
	}

	return UpdateConfig(project.ID, jsRendering, parallelism, userAgent, mechanisms, sitemapURLs)
}

// GetProjects returns all projects from the database with their latest crawl info
func (a *App) GetProjects() ([]ProjectInfo, error) {
	projects, err := GetAllProjects()
	if err != nil {
		return nil, err
	}

	// Convert to ProjectInfo for frontend
	projectInfos := make([]ProjectInfo, 0, len(projects))
	for _, p := range projects {
		projectInfo := ProjectInfo{
			ID:          p.ID,
			URL:         p.URL,
			Domain:      p.Domain,
			FaviconPath: p.FaviconPath,
		}

		// Get the latest crawl for this project if it exists
		if len(p.Crawls) > 0 {
			latestCrawl := p.Crawls[0]
			projectInfo.CrawlDateTime = latestCrawl.CrawlDateTime
			projectInfo.CrawlDuration = latestCrawl.CrawlDuration
			projectInfo.PagesCrawled = latestCrawl.PagesCrawled
			projectInfo.LatestCrawlID = latestCrawl.ID
		}
		// If no crawls, fields will be zero values (0 for int64/uint)

		projectInfos = append(projectInfos, projectInfo)
	}

	return projectInfos, nil
}

// GetCrawls returns all crawls for a project
func (a *App) GetCrawls(projectID uint) ([]CrawlInfo, error) {
	crawls, err := GetProjectCrawls(projectID)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlInfo for frontend
	crawlInfos := make([]CrawlInfo, len(crawls))
	for i, c := range crawls {
		crawlInfos[i] = CrawlInfo{
			ID:            c.ID,
			ProjectID:     c.ProjectID,
			CrawlDateTime: c.CrawlDateTime,
			CrawlDuration: c.CrawlDuration,
			PagesCrawled:  c.PagesCrawled,
		}
	}

	return crawlInfos, nil
}

// GetCrawlWithResults returns a specific crawl with all its results
func (a *App) GetCrawlWithResults(crawlID uint) (*CrawlResultDetailed, error) {
	// Get crawl info
	crawl, err := GetCrawlByID(crawlID)
	if err != nil {
		return nil, err
	}

	// Get crawled URLs
	urls, err := GetCrawlResults(crawlID)
	if err != nil {
		return nil, err
	}

	// Convert to CrawlResult for frontend
	results := make([]CrawlResult, len(urls))
	for i, u := range urls {
		results[i] = CrawlResult{
			URL:       u.URL,
			Status:    u.Status,
			Title:     u.Title,
			Indexable: u.Indexable,
			Error:     u.Error,
		}
	}

	return &CrawlResultDetailed{
		CrawlInfo: CrawlInfo{
			ID:            crawl.ID,
			ProjectID:     crawl.ProjectID,
			CrawlDateTime: crawl.CrawlDateTime,
			CrawlDuration: crawl.CrawlDuration,
			PagesCrawled:  crawl.PagesCrawled,
		},
		Results: results,
	}, nil
}

// DeleteCrawlByID deletes a specific crawl
func (a *App) DeleteCrawlByID(crawlID uint) error {
	return DeleteCrawl(crawlID)
}

// DeleteProjectByID deletes a project and all its crawls
func (a *App) DeleteProjectByID(projectID uint) error {
	return DeleteProject(projectID)
}

// GetFaviconData reads a favicon file and returns it as a base64 data URL
func (a *App) GetFaviconData(faviconPath string) (string, error) {
	if faviconPath == "" {
		return "", fmt.Errorf("empty favicon path")
	}

	// Read the file
	data, err := os.ReadFile(faviconPath)
	if err != nil {
		return "", fmt.Errorf("failed to read favicon: %v", err)
	}

	// Convert to base64 data URL
	// Determine content type based on file extension
	contentType := "image/png"
	if strings.HasSuffix(strings.ToLower(faviconPath), ".jpg") || strings.HasSuffix(strings.ToLower(faviconPath), ".jpeg") {
		contentType = "image/jpeg"
	} else if strings.HasSuffix(strings.ToLower(faviconPath), ".ico") {
		contentType = "image/x-icon"
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", contentType, base64Data), nil
}
