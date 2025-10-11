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

package bluesnake

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// Link represents a single outbound link discovered on a page
type Link struct {
	// URL is the target URL
	URL string `json:"url"`
	// Type is the link type: "anchor", "image", "script", "stylesheet", "iframe", "canonical", "video", "audio"
	Type string `json:"type"`
	// Text is the anchor text, alt text, or empty for other link types
	Text string `json:"text"`
	// IsInternal indicates if this link points to the same domain/subdomain
	IsInternal bool `json:"isInternal"`
	// Status is the HTTP status code if this URL has been crawled (200, 404, 301, etc.)
	Status *int `json:"status,omitempty"`
	// Title is the page title if this URL has been crawled
	Title string `json:"title,omitempty"`
	// ContentType is the MIME type if this URL has been crawled
	ContentType string `json:"contentType,omitempty"`
}

// Links contains outbound links from a page
type Links struct {
	// Internal links point to same domain/subdomain
	Internal []Link `json:"internal"`
	// External links point to different domains
	External []Link `json:"external"`
}

// PageResult contains all data collected from a single crawled page
type PageResult struct {
	// URL is the URL that was crawled
	URL string
	// Status is the HTTP status code (e.g., 200, 404, 500)
	Status int
	// Title is the page title extracted from the <title> tag (for HTML pages)
	Title string
	// Indexable indicates if search engines can index this page
	// Values: "Yes", "No", or "-" for non-HTML resources
	Indexable string
	// ContentType is the Content-Type header value (e.g., "text/html", "application/json")
	ContentType string
	// Error contains any error message if the crawl failed, empty otherwise
	Error string
	// Links contains all outbound links from this page (internal and external)
	Links *Links
}

// OnPageCrawledFunc is called after each individual page is successfully crawled or encounters an error.
// It receives the complete result of crawling that page including all discovered URLs.
type OnPageCrawledFunc func(*PageResult)

// OnCrawlCompleteFunc is called when the entire crawl finishes, either naturally or due to cancellation.
// Parameters:
//   - wasStopped: true if the crawl was stopped via context cancellation, false if it completed naturally
//   - totalPages: total number of pages that were successfully crawled (excludes errors)
//   - totalDiscovered: total number of unique URLs discovered during the crawl
type OnCrawlCompleteFunc func(wasStopped bool, totalPages int, totalDiscovered int)

// PageMetadata stores cached metadata for crawled pages
type PageMetadata struct {
	Status      int
	Title       string
	ContentType string
}

// Crawler provides a high-level interface for web crawling with callbacks for page results
type Crawler struct {
	// Collector is the underlying low-level collector (exported for advanced configuration)
	Collector       *Collector
	onPageCrawled   OnPageCrawledFunc
	onCrawlComplete OnCrawlCompleteFunc

	// Internal state tracking
	queuedURLs   *sync.Map // map[string]bool - tracks all discovered URLs
	crawledPages int
	mutex        sync.RWMutex

	// Link tracking
	rootDomain   string    // Root domain for internal/external classification
	pageMetadata *sync.Map // map[string]PageMetadata - cached page metadata

	// Discovery configuration
	discoveryMechanisms []DiscoveryMechanism // Enabled discovery mechanisms
	sitemapURLs         []string             // Custom sitemap URLs (nil = try defaults)
}

// NewCrawler creates a high-level crawler with the specified collector configuration.
// The returned crawler must have its callbacks set via SetOnPageCrawled and SetOnCrawlComplete
// before calling Start. If config is nil, default configuration is used.
func NewCrawler(config *CollectorConfig) *Crawler {
	if config == nil {
		config = NewDefaultConfig()
	}

	c := NewCollector(config)

	// Apply defaults for discovery mechanisms if not specified
	discoveryMechanisms := config.DiscoveryMechanisms
	if len(discoveryMechanisms) == 0 {
		discoveryMechanisms = []DiscoveryMechanism{DiscoverySpider} // Default to spider mode
	}

	crawler := &Crawler{
		Collector:           c,
		queuedURLs:          &sync.Map{},
		crawledPages:        0,
		discoveryMechanisms: discoveryMechanisms,
		sitemapURLs:         config.SitemapURLs,
		pageMetadata:        &sync.Map{},
	}

	// Configure sitemap fetching to use the same HTTP client as the collector
	// This ensures mocks and custom transports work for sitemap fetching too
	crawler.configureSitemapFetch()

	crawler.setupCallbacks()
	return crawler
}

// configureSitemapFetch configures the sitemap library to use the Collector's HTTP client.
// This ensures that sitemap fetching uses the same transport as regular crawling,
// which is crucial for testing with mock transports.
// Note: We access cr.Collector.backend.Client dynamically (not capturing it) so that
// changes to the transport (like calling WithTransport) are properly reflected.
func (cr *Crawler) configureSitemapFetch() {
	SetFetch(func(url string, options interface{}) ([]byte, error) {
		// Access the client dynamically to pick up any transport changes
		resp, err := cr.Collector.backend.Client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	})
}

// SetOnPageCrawled registers a callback function that will be called after each page is crawled.
// This callback receives complete page information including discovered URLs.
func (cr *Crawler) SetOnPageCrawled(f OnPageCrawledFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onPageCrawled = f
}

// SetOnCrawlComplete registers a callback function that will be called when the crawl finishes.
// This callback receives summary statistics about the completed crawl.
func (cr *Crawler) SetOnCrawlComplete(f OnCrawlCompleteFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onCrawlComplete = f
}

// Start begins crawling from the specified starting URL.
// It returns immediately if the crawler is in Async mode, or blocks until completion otherwise.
func (cr *Crawler) Start(url string) error {
	// Extract and set root domain for internal/external classification
	cr.rootDomain = cr.extractRootDomain(url)

	// ALWAYS queue the base URL first
	cr.queuedURLs.Store(url, true)

	// Start crawling from base URL first
	if err := cr.Collector.Visit(url); err != nil {
		return err
	}

	// If sitemap discovery is enabled, fetch and queue sitemap URLs
	if cr.hasDiscoveryMechanism(DiscoverySitemap) {
		sitemapURLs := cr.fetchSitemapURLs(url)
		for _, sitemapURL := range sitemapURLs {
			// De-duplicate automatically - only queue if not already seen
			if _, alreadyQueued := cr.queuedURLs.LoadOrStore(sitemapURL, true); !alreadyQueued {
				// Visit each sitemap URL (errors are logged but don't stop the crawl)
				cr.Collector.Visit(sitemapURL)
			}
		}
	}

	// Spider mode link following is handled by setupCallbacks if enabled
	return nil
}

// Wait blocks until all crawling operations complete.
// This is primarily useful when the crawler is in Async mode.
func (cr *Crawler) Wait() {
	cr.Collector.Wait()

	// Calculate totals
	totalDiscovered := 0
	cr.queuedURLs.Range(func(key, value interface{}) bool {
		totalDiscovered++
		return true
	})

	wasStopped := cr.Collector.IsCancelled()

	cr.mutex.RLock()
	totalPages := cr.crawledPages
	onComplete := cr.onCrawlComplete
	cr.mutex.RUnlock()

	// Call completion callback if set
	if onComplete != nil {
		onComplete(wasStopped, totalPages, totalDiscovered)
	}
}

// setupCallbacks configures the internal collector callbacks to aggregate page data
func (cr *Crawler) setupCallbacks() {
	// Store outbound links for each page (map[pageURL][]Link)
	pageOutboundLinks := &sync.Map{}

	// Extract all links from HTML pages and build link graph
	cr.Collector.OnHTML("html", func(e *HTMLElement) {
		// Extract ALL link types (anchors, images, scripts, etc.)
		allLinks := cr.extractAllLinks(e)

		// Store outbound links for this page
		pageOutboundLinks.Store(e.Request.URL.String(), allLinks)

		// If spider discovery is enabled, visit internal anchor links
		if cr.hasDiscoveryMechanism(DiscoverySpider) {
			for _, link := range allLinks {
				// Only visit anchor links (HTML pages)
				if link.Type != "anchor" {
					continue
				}

				// Only visit internal links
				if !link.IsInternal {
					continue
				}

				// Check if crawlable (filters, robots.txt)
				if !cr.isURLCrawlable(link.URL) {
					continue
				}

				// Only visit if not already queued
				if _, alreadyQueued := cr.queuedURLs.LoadOrStore(link.URL, true); !alreadyQueued {
					e.Request.Visit(link.URL)
				}
			}
		}
	})

	// Capture page metadata (title, indexability)
	cr.Collector.OnHTML("html", func(e *HTMLElement) {
		// Store title and meta robots in context for OnScraped to use
		title := e.ChildText("title")
		e.Request.Ctx.Put("title", title)

		// Check for meta robots noindex
		metaRobots := e.ChildAttr("meta[name='robots']", "content")
		if strings.Contains(strings.ToLower(metaRobots), "noindex") {
			e.Request.Ctx.Put("metaNoindex", "true")
		}
	})

	// OnScraped fires AFTER all OnHTML callbacks complete, ensuring all URLs are discovered
	cr.Collector.OnScraped(func(r *Response) {
		// Only process HTML pages here (non-HTML is handled in OnResponse)
		contentType := r.Ctx.Get("contentType")
		if !strings.Contains(contentType, "text/html") {
			return
		}

		// Get title from context (set by OnHTML)
		title := r.Ctx.Get("title")

		// Get indexability from context (set by OnResponse)
		isIndexable := r.Ctx.Get("isIndexable")
		if isIndexable == "" {
			isIndexable = "Yes"
		}

		// Check meta robots noindex flag
		if r.Ctx.Get("metaNoindex") == "true" {
			isIndexable = "No"
		}

		status := 200
		if statusStr := r.Ctx.Get("status"); statusStr != "" {
			fmt.Sscanf(statusStr, "%d", &status)
		}

		pageURL := r.Request.URL.String()

		// Store page metadata for future link population
		cr.pageMetadata.Store(pageURL, PageMetadata{
			Status:      status,
			Title:       title,
			ContentType: contentType,
		})

		// Build PageLinks structure
		pageLinks := cr.buildPageLinks(pageURL, pageOutboundLinks)

		result := &PageResult{
			URL:         pageURL,
			Status:      status,
			Title:       title,
			Indexable:   isIndexable,
			ContentType: contentType,
			Error:       "",
			Links:       pageLinks,
		}

		cr.incrementCrawledPages()
		cr.callOnPageCrawled(result)
	})

	// Handle all responses (HTML and non-HTML)
	cr.Collector.OnResponse(func(r *Response) {
		contentType := r.Headers.Get("Content-Type")
		xRobotsTag := r.Headers.Get("X-Robots-Tag")
		isIndexable := "Yes"
		if strings.Contains(strings.ToLower(xRobotsTag), "noindex") {
			isIndexable = "No"
		}

		// Store in context for OnHTML to use
		r.Request.Ctx.Put("isIndexable", isIndexable)
		r.Request.Ctx.Put("status", fmt.Sprintf("%d", r.StatusCode))
		r.Request.Ctx.Put("contentType", contentType)

		pageURL := r.Request.URL.String()

		// For non-HTML content, we need to create a result here since OnHTML won't fire
		if !strings.Contains(contentType, "text/html") {
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

			// Store page metadata for future link population
			cr.pageMetadata.Store(pageURL, PageMetadata{
				Status:      r.StatusCode,
				Title:       title,
				ContentType: contentType,
			})

			// Build PageLinks structure (non-HTML has no outbound links, only inbound)
			pageLinks := cr.buildPageLinks(pageURL, pageOutboundLinks)

			result := &PageResult{
				URL:         pageURL,
				Status:      r.StatusCode,
				Title:       title,
				Indexable:   isIndexable,
				ContentType: contentType,
				Error:       "",
				Links:       pageLinks,
			}

			cr.incrementCrawledPages()
			cr.callOnPageCrawled(result)
		}
	})

	// Handle errors
	cr.Collector.OnError(func(r *Response, err error) {
		// Skip already visited errors - these are handled by deduplication
		if strings.Contains(err.Error(), "already visited") {
			return
		}

		pageURL := r.Request.URL.String()

		// Build PageLinks structure (errors have no outbound links, but may have inbound)
		pageLinks := cr.buildPageLinks(pageURL, pageOutboundLinks)

		result := &PageResult{
			URL:         pageURL,
			Status:      0,
			Title:       "",
			Indexable:   "No",
			ContentType: "",
			Error:       err.Error(),
			Links:       pageLinks,
		}

		cr.callOnPageCrawled(result)
	})
}

// isURLCrawlable checks if a URL passes domain filters and robots.txt rules
// For now, we'll do a simple check - we consider a URL crawlable if it would pass
// the basic domain and filter checks. The actual robots.txt check happens during Visit().
func (cr *Crawler) isURLCrawlable(urlStr string) bool {
	parsedURL, err := urlParser.Parse(urlStr)
	if err != nil {
		return false
	}

	hostname := parsedURL.Hostname()

	// Check domain allowlist/blocklist
	if len(cr.Collector.DisallowedDomains) > 0 {
		for _, d := range cr.Collector.DisallowedDomains {
			if d == hostname {
				return false
			}
		}
	}

	if len(cr.Collector.AllowedDomains) > 0 {
		allowed := false
		for _, d := range cr.Collector.AllowedDomains {
			if d == hostname {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	// Check URL filters (DisallowedURLFilters and URLFilters)
	urlBytes := []byte(urlStr)

	if len(cr.Collector.DisallowedURLFilters) > 0 {
		for _, filter := range cr.Collector.DisallowedURLFilters {
			if filter.Match(urlBytes) {
				return false
			}
		}
	}

	if len(cr.Collector.URLFilters) > 0 {
		matched := false
		for _, filter := range cr.Collector.URLFilters {
			if filter.Match(urlBytes) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Note: We don't check robots.txt here because that requires a network request.
	// The robots.txt check will happen when Visit() is called.
	// If robots.txt blocks the URL, it will trigger OnError callback.

	return true
}

// incrementCrawledPages safely increments the crawled pages counter
func (cr *Crawler) incrementCrawledPages() {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.crawledPages++
}

// callOnPageCrawled safely calls the OnPageCrawled callback if set
func (cr *Crawler) callOnPageCrawled(result *PageResult) {
	cr.mutex.RLock()
	callback := cr.onPageCrawled
	cr.mutex.RUnlock()

	if callback != nil {
		callback(result)
	}
}

// hasDiscoveryMechanism checks if a specific discovery mechanism is enabled
func (cr *Crawler) hasDiscoveryMechanism(mechanism DiscoveryMechanism) bool {
	for _, m := range cr.discoveryMechanisms {
		if m == mechanism {
			return true
		}
	}
	return false
}

// fetchSitemapURLs fetches sitemap URLs based on configuration.
// Uses custom sitemap URLs if provided, otherwise tries default locations.
// Returns all discovered URLs from sitemaps (empty slice if none found).
func (cr *Crawler) fetchSitemapURLs(baseURL string) []string {
	var sitemapLocations []string

	// Use custom sitemap URLs if provided
	if len(cr.sitemapURLs) > 0 {
		sitemapLocations = cr.sitemapURLs
	} else {
		// Try default locations
		return TryDefaultSitemaps(baseURL)
	}

	// Fetch URLs from custom sitemap locations
	var allURLs []string
	for _, sitemapURL := range sitemapLocations {
		urls, err := FetchSitemapURLs(sitemapURL)
		if err != nil {
			// Log error but continue with other sitemaps
			continue
		}
		allURLs = append(allURLs, urls...)
	}

	return allURLs
}

// buildPageLinks constructs the Links structure for a given page
func (cr *Crawler) buildPageLinks(pageURL string, pageOutboundLinks *sync.Map) *Links {
	// Get outbound links for this page
	var outbound []Link
	if val, ok := pageOutboundLinks.Load(pageURL); ok {
		outbound = val.([]Link)
	}

	// Separate internal and external links
	var internal, external []Link
	for _, link := range outbound {
		if link.IsInternal {
			internal = append(internal, link)
		} else {
			external = append(external, link)
		}
	}

	return &Links{
		Internal: internal,
		External: external,
	}
}

// extractAllLinks extracts all links from an HTML element.
// Returns a list of links with their types, text, and internal/external classification.
func (cr *Crawler) extractAllLinks(e *HTMLElement) []Link {
	var links []Link

	// Helper function to add a link to the list
	addLink := func(url, linkType, text string) {
		absoluteURL := e.Request.AbsoluteURL(url)
		if absoluteURL == "" {
			return
		}

		// Skip fragment-only links
		if strings.HasPrefix(url, "#") {
			return
		}

		isInternal := cr.isInternalURL(absoluteURL)

		// Get metadata if this URL has been crawled
		var status *int
		var title, contentType string
		if meta, ok := cr.pageMetadata.Load(absoluteURL); ok {
			metadata := meta.(PageMetadata)
			status = &metadata.Status
			title = metadata.Title
			contentType = metadata.ContentType
		}

		links = append(links, Link{
			URL:         absoluteURL,
			Type:        linkType,
			Text:        text,
			IsInternal:  isInternal,
			Status:      status,
			Title:       title,
			ContentType: contentType,
		})
	}

	// Extract anchor links <a href="">
	e.ForEach("a[href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		text := strings.TrimSpace(elem.Text)
		addLink(href, "anchor", text)
	})

	// Extract image links <img src="">
	e.ForEach("img[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		alt := elem.Attr("alt")
		addLink(src, "image", alt)
	})

	// Extract script links <script src="">
	e.ForEach("script[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "script", "")
	})

	// Extract stylesheet links <link rel="stylesheet" href="">
	e.ForEach("link[rel='stylesheet'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "stylesheet", "")
	})

	// Extract canonical links <link rel="canonical" href="">
	e.ForEach("link[rel='canonical'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "canonical", "")
	})

	// Extract iframe links <iframe src="">
	e.ForEach("iframe[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "iframe", "")
	})

	// Extract video sources <video src=""> and <source src="">
	e.ForEach("video[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "")
	})
	e.ForEach("video source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "")
	})

	// Extract audio sources <audio src=""> and <source src="">
	e.ForEach("audio[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "")
	})
	e.ForEach("audio source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "")
	})

	return links
}

// extractRootDomain extracts the root domain from a URL for internal/external classification.
// Returns the hostname (including subdomain) in lowercase.
// Examples:
//   - https://example.com/path -> example.com
//   - https://blog.example.com/path -> blog.example.com
//   - https://example.com:8080/path -> example.com:8080
func (cr *Crawler) extractRootDomain(urlStr string) string {
	parsedURL, err := urlParser.Parse(urlStr)
	if err != nil {
		return ""
	}

	hostname := parsedURL.Hostname()
	port := parsedURL.Port()

	// Include port if non-standard
	if port != "" && port != "80" && port != "443" {
		return strings.ToLower(hostname + ":" + port)
	}

	return strings.ToLower(hostname)
}

// isInternalURL checks if a URL belongs to the same domain/subdomain as the root domain.
// Returns true if:
//   - URL has exact same domain as rootDomain
//   - URL is a subdomain of rootDomain (e.g., blog.example.com is internal to example.com)
//   - rootDomain is a subdomain of URL domain (e.g., example.com is internal to blog.example.com)
func (cr *Crawler) isInternalURL(urlStr string) bool {
	if cr.rootDomain == "" {
		return false
	}

	targetDomain := cr.extractRootDomain(urlStr)
	if targetDomain == "" {
		return false
	}

	// Exact match
	if targetDomain == cr.rootDomain {
		return true
	}

	// Check if one is a subdomain of the other
	// blog.example.com contains .example.com (subdomain)
	// example.com contains .example.com is false, but blog.example.com should be internal to example.com

	// Remove port for subdomain comparison
	rootWithoutPort := strings.Split(cr.rootDomain, ":")[0]
	targetWithoutPort := strings.Split(targetDomain, ":")[0]

	// Check if target is subdomain of root
	if strings.HasSuffix(targetWithoutPort, "."+rootWithoutPort) {
		return true
	}

	// Check if root is subdomain of target
	if strings.HasSuffix(rootWithoutPort, "."+targetWithoutPort) {
		return true
	}

	return false
}
