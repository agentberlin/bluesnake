package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

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
			r.Request.Ctx.Put("isIndexable", isIndexable)
			r.Request.Ctx.Put("status", fmt.Sprintf("%d", r.StatusCode))
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
			c.Visit(link)
		}
	})

	c.OnError(func(r *bluesnake.Response, err error) {
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
