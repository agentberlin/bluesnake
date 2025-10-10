package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Compile regex once for performance
var duplicateSlashesRegex = regexp.MustCompile(`/+`)

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

// crawlStats tracks crawl statistics
type crawlStats struct {
	startTime    time.Time
	pagesCrawled int
	url          string
	domain       string
	projectID    uint
	crawlID      uint
	queuedURLs   *sync.Map // Track URLs that have been queued to prevent duplicates
	crawledURLs  *sync.Map // Track URLs that have been crawled (for discovered URLs list)
}

// normalizeURLForDedup normalizes a URL for deduplication to prevent crawling the same page multiple times
// This handles common URL variations that point to the same resource
// The url.Parse() + modifications + String() approach automatically handles edge cases like:
// - Empty query strings: /path/? → /path/
// - Empty fragments: /path/# → /path/
// - Malformed URLs that Go's URL parser can clean up
func normalizeURLForDedup(urlStr string) string {
	// Parse the URL - this automatically normalizes many edge cases
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		// If parsing fails, return as-is
		return urlStr
	}

	// 1. Remove fragment (#section) - fragments are client-side only and don't create new pages
	// This also handles empty fragments like /path/#
	parsedURL.Fragment = ""

	// 2. Lowercase scheme and hostname (these are case-insensitive per RFC 3986)
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	parsedURL.Host = strings.ToLower(parsedURL.Host)

	// 3. Remove default ports (80 for http, 443 for https)
	if (parsedURL.Scheme == "http" && parsedURL.Port() == "80") ||
		(parsedURL.Scheme == "https" && parsedURL.Port() == "443") {
		parsedURL.Host = parsedURL.Hostname() // Remove port from host
	}

	// 4. Normalize path: handle trailing slashes for directory-like paths
	path := parsedURL.Path
	if path != "" && path != "/" {
		// Remove duplicate slashes: //foo → /foo
		path = duplicateSlashesRegex.ReplaceAllString(path, "/")

		// For paths not ending in a file extension, ensure trailing slash
		if !strings.Contains(filepath.Base(path), ".") {
			if !strings.HasSuffix(path, "/") {
				path = path + "/"
			}
		}
		parsedURL.Path = path
	}

	// 5. Normalize query string
	if parsedURL.RawQuery != "" {
		// Sort query parameters alphabetically for consistent comparison
		// This treats ?a=1&b=2 and ?b=2&a=1 as the same URL
		query := parsedURL.Query()
		parsedURL.RawQuery = query.Encode() // Encode() sorts keys alphabetically
	} else {
		// Explicitly clear RawQuery to ensure empty query strings (trailing ?)
		// are removed when String() reconstructs the URL
		parsedURL.RawQuery = ""
	}

	// Reconstruct the URL - this will automatically:
	// - Remove trailing ? if RawQuery is empty
	// - Remove trailing # if Fragment is empty
	// - Apply proper URL encoding
	reconstructed := parsedURL.String()

	// Edge case: Some URLs might still have trailing ? or # after reconstruction
	// Strip them explicitly if they're at the end with nothing after them
	reconstructed = strings.TrimSuffix(reconstructed, "?")
	reconstructed = strings.TrimSuffix(reconstructed, "#")

	return reconstructed
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
		totalDiscovered := 0
		discoveredURLs := []string{}

		if ac.stats != nil {
			// Count total discovered URLs
			if ac.stats.queuedURLs != nil {
				ac.stats.queuedURLs.Range(func(key, value interface{}) bool {
					totalDiscovered++

					// Check if this URL has been crawled
					urlStr := key.(string)
					if ac.stats.crawledURLs != nil {
						if _, crawled := ac.stats.crawledURLs.Load(urlStr); !crawled {
							// URL is queued but not crawled yet
							discoveredURLs = append(discoveredURLs, urlStr)
						}
					} else {
						// If crawledURLs not initialized, assume all queued URLs are discovered
						discoveredURLs = append(discoveredURLs, urlStr)
					}
					return true
				})
			}
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

// GetActiveCrawlData returns the data for an active crawl from database + memory
func (a *App) GetActiveCrawlData(projectID uint) (*CrawlResultDetailed, error) {
	a.crawlsMutex.RLock()
	ac, exists := a.activeCrawls[projectID]
	a.crawlsMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no active crawl found for project %d", projectID)
	}

	// Read stats and get discovered URLs from memory
	ac.statusMutex.RLock()
	pagesCrawled := 0
	discoveredURLs := []string{}
	if ac.stats != nil {
		pagesCrawled = ac.stats.pagesCrawled

		// Get URLs that have been discovered but not yet crawled
		if ac.stats.queuedURLs != nil {
			ac.stats.queuedURLs.Range(func(key, value interface{}) bool {
				urlStr := key.(string)
				if ac.stats.crawledURLs != nil {
					if _, crawled := ac.stats.crawledURLs.Load(urlStr); !crawled {
						discoveredURLs = append(discoveredURLs, urlStr)
					}
				}
				return true
			})
		}
	}
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

	// Add discovered (in-progress) URLs to results
	for _, url := range discoveredURLs {
		results = append(results, CrawlResult{
			URL:       url,
			Status:    0,
			Title:     "In progress...",
			Indexable: "-",
		})
	}

	return &CrawlResultDetailed{
		CrawlInfo: CrawlInfo{
			ID:            crawlID,
			ProjectID:     ac.projectID,
			CrawlDateTime: 0,        // Not applicable for active crawl
			CrawlDuration: 0,        // Not applicable for active crawl
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
		startTime:    time.Now(),
		pagesCrawled: 0,
		url:          normalizedURL,
		domain:       domain,
		projectID:    projectID,
		crawlID:      crawl.ID,
		queuedURLs:   &sync.Map{},
		crawledURLs:  &sync.Map{},
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
			JSRenderingEnabled: false,
			Parallelism:        5,
		}
	}

	// Build collector options based on config
	options := []bluesnake.CollectorOption{
		bluesnake.StdlibContext(crawlCtx), // Pass context for proper cancellation support
		bluesnake.AllowedDomains(domain),
		bluesnake.Async(),
	}

	if config.JSRenderingEnabled {
		options = append(options, bluesnake.EnableJSRendering())
	}

	c := bluesnake.NewCollector(options...)

	// Apply parallelism limit
	if config.Parallelism > 0 {
		c.Limit(&bluesnake.LimitRule{
			DomainGlob:  "*",
			Parallelism: config.Parallelism,
		})
	}

	// Send crawl start event (indicational only, no payload)
	runtime.EventsEmit(a.ctx, "crawl:started")

	c.OnResponse(func(r *bluesnake.Response) {
		// Check if crawl was stopped
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't process
		default:
		}

		contentType := r.Headers.Get("Content-Type")
		xRobotsTag := r.Headers.Get("X-Robots-Tag")
		isIndexable := "Yes"
		if strings.Contains(strings.ToLower(xRobotsTag), "noindex") {
			isIndexable = "No"
		}

		if strings.Contains(contentType, "text/html") {
			// HTML content - will be processed by OnHTML handler
			r.Request.Ctx.Put("isIndexable", isIndexable)
			r.Request.Ctx.Put("status", fmt.Sprintf("%d", r.StatusCode))
		} else {
			// Non-HTML content (XML, JSON, PDF, images, etc.)
			// Determine a descriptive title based on content type
			title := "Unknown Resource"
			if strings.Contains(contentType, "xml") || strings.Contains(contentType, "rss") || strings.Contains(contentType, "atom") {
				title = "XML Feed"
			} else if strings.Contains(contentType, "json") {
				title = "JSON Resource"
			} else if strings.Contains(contentType, "pdf") {
				title = "PDF Document"
			} else if strings.Contains(contentType, "image/") {
				title = "Image"
			} else if strings.Contains(contentType, "javascript") {
				title = "JavaScript File"
			} else if strings.Contains(contentType, "css") {
				title = "Stylesheet"
			}

			result := CrawlResult{
				URL:       r.Request.URL.String(),
				Status:    r.StatusCode,
				Title:     title,
				Indexable: isIndexable,
			}

			// Mark URL as crawled
			normalizedURL := normalizeURLForDedup(result.URL)
			stats.crawledURLs.Store(normalizedURL, true)

			// Increment pages crawled
			stats.pagesCrawled++

			// Save to database
			if err := SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, ""); err != nil {
				runtime.LogErrorf(a.ctx, "Failed to save crawled URL: %v", err)
			}
		}
	})

	c.OnHTML("title", func(e *bluesnake.HTMLElement) {
		// Check if crawl was stopped
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't process
		default:
		}

		isIndexable := e.Request.Ctx.Get("isIndexable")
		if isIndexable == "" {
			isIndexable = "Yes"
		}
		if strings.Contains(strings.ToLower(e.Text), "noindex") {
			isIndexable = "No"
		}

		status := 200
		if statusStr := e.Request.Ctx.Get("status"); statusStr != "" {
			fmt.Sscanf(statusStr, "%d", &status)
		}

		result := CrawlResult{
			URL:       e.Request.URL.String(),
			Status:    status,
			Title:     e.Text,
			Indexable: isIndexable,
		}

		// Mark URL as crawled
		normalizedURL := normalizeURLForDedup(result.URL)
		stats.crawledURLs.Store(normalizedURL, true)

		// Increment pages crawled
		stats.pagesCrawled++

		// Save to database
		if err := SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, ""); err != nil {
			runtime.LogErrorf(a.ctx, "Failed to save crawled URL: %v", err)
		}
	})

	c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
		// Check if crawl was stopped - CRITICAL: prevents queuing new URLs
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't queue new URLs
		default:
		}

		link := e.Request.AbsoluteURL(e.Attr("href"))
		if link != "" {
			// Normalize URL for deduplication (handles trailing slashes, query params, etc.)
			normalizedLink := normalizeURLForDedup(link)

			// Use LoadOrStore to atomically check and mark URL as queued
			// This prevents duplicate Visit() calls for the same URL
			_, alreadyQueued := stats.queuedURLs.LoadOrStore(normalizedLink, true)

			if !alreadyQueued {
				// IMPORTANT: Visit the normalized URL, not the original!
				// This ensures the UI and database get consistent normalized URLs
				c.Visit(normalizedLink)
			}
		}
	})

	c.OnError(func(r *bluesnake.Response, err error) {
		// Check if crawl was stopped
		select {
		case <-crawlCtx.Done():
			return // Crawl stopped, don't process errors
		default:
		}

		// Skip AlreadyVisitedError - these should not occur now that we're using
		// LoadOrStore to prevent duplicate Visit() calls, but keep as a safety net.
		if strings.Contains(err.Error(), "already visited") {
			return
		}

		result := CrawlResult{
			URL:   r.Request.URL.String(),
			Error: err.Error(),
		}

		// Save error to database
		if saveErr := SaveCrawledUrl(stats.crawlID, result.URL, 0, "", "No", err.Error()); saveErr != nil {
			runtime.LogErrorf(a.ctx, "Failed to save crawl error: %v", saveErr)
		}
	})

	// Mark the starting URL as queued (normalized)
	normalizedStartURL := normalizeURLForDedup(parsedURL.String())
	stats.queuedURLs.Store(normalizedStartURL, true)

	c.Visit(parsedURL.String())

	// Monitor for stop signals while crawling
	done := make(chan bool, 1)
	go func() {
		c.Wait()
		done <- true
	}()

	// Wait for either completion or stop signal
	wasStopped := false
	select {
	case <-done:
		// Crawl completed normally
		runtime.LogInfof(a.ctx, "Crawl completed normally for project %d", projectID)
	case <-stopChan:
		// Stop requested - give it a short grace period, then force stop
		runtime.LogInfof(a.ctx, "Stop signal received for project %d, forcing termination...", projectID)
		wasStopped = true

		// Cancel the context to signal all goroutines to stop
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
		wasStopped = true
		// Give a brief moment for cleanup
		select {
		case <-done:
		case <-time.After(1 * time.Second):
		}
	}

	// Calculate crawl duration
	crawlDuration := time.Since(stats.startTime).Milliseconds()

	// Update crawl statistics in database
	if err := UpdateCrawlStats(stats.crawlID, crawlDuration, stats.pagesCrawled); err != nil {
		runtime.LogErrorf(a.ctx, "Failed to update crawl stats: %v", err)
	}

	// Send appropriate completion event (indicational only, no payload)
	if wasStopped {
		runtime.EventsEmit(a.ctx, "crawl:stopped")
	} else {
		runtime.EventsEmit(a.ctx, "crawl:completed")
	}
}

// GetConfigForDomain retrieves the configuration for a specific domain
func (a *App) GetConfigForDomain(urlStr string) (*Config, error) {
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

	return GetOrCreateConfig(project.ID, domain)
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(urlStr string, jsRendering bool, parallelism int) error {
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

	return UpdateConfig(project.ID, jsRendering, parallelism)
}

// GetProjects returns all projects from the database with their latest crawl info
func (a *App) GetProjects() ([]ProjectInfo, error) {
	projects, err := GetAllProjects()
	if err != nil {
		return nil, err
	}

	// Convert to ProjectInfo for frontend
	projectInfos := make([]ProjectInfo, 0)
	for _, p := range projects {
		// Get the latest crawl for this project
		if len(p.Crawls) > 0 {
			latestCrawl := p.Crawls[0]
			projectInfos = append(projectInfos, ProjectInfo{
				ID:            p.ID,
				URL:           p.URL,
				Domain:        p.Domain,
				FaviconPath:   p.FaviconPath,
				CrawlDateTime: latestCrawl.CrawlDateTime,
				CrawlDuration: latestCrawl.CrawlDuration,
				PagesCrawled:  latestCrawl.PagesCrawled,
				LatestCrawlID: latestCrawl.ID,
			})
		}
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
