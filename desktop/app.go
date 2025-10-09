package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

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
		fmt.Printf("Failed to initialize database: %v\n", err)
	}
}

// StartCrawl initiates a crawl for the given URL
func (a *App) StartCrawl(urlStr string) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Run crawler in a goroutine to not block the UI
	go a.runCrawler(parsedURL)

	return nil
}

func (a *App) runCrawler(parsedURL *url.URL) {
	// Get configuration for this domain
	domain := parsedURL.Hostname()
	config, err := GetOrCreateConfig(domain)
	if err != nil {
		fmt.Printf("Failed to get config for domain %s: %v\n", domain, err)
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
		runtime.EventsEmit(a.ctx, "crawl:error", result)
	})

	c.Visit(parsedURL.String())
	c.Wait()

	// Send crawl complete event
	runtime.EventsEmit(a.ctx, "crawl:completed", map[string]string{
		"message": "Crawl completed",
	})
}

// GetConfigForDomain retrieves the configuration for a specific domain
func (a *App) GetConfigForDomain(urlStr string) (*Config, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	domain := parsedURL.Hostname()
	return GetOrCreateConfig(domain)
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(urlStr string, jsRendering bool, parallelism int) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	domain := parsedURL.Hostname()
	return UpdateConfig(domain, jsRendering, parallelism)
}
