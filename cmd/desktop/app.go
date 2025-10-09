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
	ctx context.Context
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
	return &App{}
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

	// Run crawler in a goroutine to not block the UI
	go a.runCrawler(parsedURL, normalizedURL, domain)

	return nil
}

func (a *App) runCrawler(parsedURL *url.URL, normalizedURL string, domain string) {
	// Get or create project
	project, err := GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to get/create project: %v", err)
		runtime.EventsEmit(a.ctx, "crawl:error", map[string]string{
			"message": "Failed to create project",
		})
		return
	}

	// Create a new crawl
	crawl, err := CreateCrawl(project.ID, time.Now().Unix(), 0, 0)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to create crawl: %v", err)
		runtime.EventsEmit(a.ctx, "crawl:error", map[string]string{
			"message": "Failed to create crawl record",
		})
		return
	}

	// Initialize crawl stats
	stats := &crawlStats{
		startTime:    time.Now(),
		pagesCrawled: 0,
		url:          normalizedURL,
		domain:       domain,
		projectID:    project.ID,
		crawlID:      crawl.ID,
		queuedURLs:   &sync.Map{},
	}

	// Get configuration for this domain
	config, err := GetOrCreateConfig(domain)
	if err != nil {
		runtime.LogErrorf(a.ctx, "Failed to get config for domain %s: %v", domain, err)
		// Use defaults if config retrieval fails
		config = &Config{
			JSRenderingEnabled: false,
			Parallelism:        5,
		}
	}

	// Build collector options based on config
	options := []bluesnake.CollectorOption{
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

	// Send crawl start event
	runtime.EventsEmit(a.ctx, "crawl:started", map[string]string{
		"url": parsedURL.String(),
	})

	c.OnRequest(func(r *bluesnake.Request) {
		// Notify frontend that we're crawling this URL
		runtime.EventsEmit(a.ctx, "crawl:request", map[string]string{
			"url": r.URL.String(),
		})
	})

	c.OnResponse(func(r *bluesnake.Response) {
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

			// Increment pages crawled
			stats.pagesCrawled++

			// Save to database
			if err := SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, ""); err != nil {
				runtime.LogErrorf(a.ctx, "Failed to save crawled URL: %v", err)
			}

			// Emit result to frontend
			runtime.EventsEmit(a.ctx, "crawl:result", result)
		}
	})

	c.OnHTML("title", func(e *bluesnake.HTMLElement) {
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

		// Increment pages crawled
		stats.pagesCrawled++

		// Save to database
		if err := SaveCrawledUrl(stats.crawlID, result.URL, result.Status, result.Title, result.Indexable, ""); err != nil {
			runtime.LogErrorf(a.ctx, "Failed to save crawled URL: %v", err)
		}

		// Emit result to frontend
		runtime.EventsEmit(a.ctx, "crawl:result", result)
	})

	c.OnHTML("a[href]", func(e *bluesnake.HTMLElement) {
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

		runtime.EventsEmit(a.ctx, "crawl:error", result)
	})

	// Mark the starting URL as queued (normalized)
	stats.queuedURLs.Store(normalizeURLForDedup(parsedURL.String()), true)

	c.Visit(parsedURL.String())
	c.Wait()

	// Calculate crawl duration
	crawlDuration := time.Since(stats.startTime).Milliseconds()

	// Update crawl statistics in database
	if err := UpdateCrawlStats(stats.crawlID, crawlDuration, stats.pagesCrawled); err != nil {
		runtime.LogErrorf(a.ctx, "Failed to update crawl stats: %v", err)
	}

	// Send crawl complete event
	runtime.EventsEmit(a.ctx, "crawl:completed", map[string]string{
		"message": "Crawl completed",
	})
}

// GetConfigForDomain retrieves the configuration for a specific domain
func (a *App) GetConfigForDomain(urlStr string) (*Config, error) {
	// Normalize the URL to extract domain
	_, domain, err := normalizeURL(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	return GetOrCreateConfig(domain)
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(urlStr string, jsRendering bool, parallelism int) error {
	// Normalize the URL to extract domain
	_, domain, err := normalizeURL(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	return UpdateConfig(domain, jsRendering, parallelism)
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
