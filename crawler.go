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
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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
	// Context is the surrounding text context where the link appears
	Context string `json:"context,omitempty"`
	// IsInternal indicates if this link points to the same domain/subdomain
	IsInternal bool `json:"isInternal"`
	// Status is the HTTP status code if this URL has been crawled (200, 404, 301, etc.)
	Status *int `json:"status,omitempty"`
	// Title is the page title if this URL has been crawled
	Title string `json:"title,omitempty"`
	// ContentType is the MIME type if this URL has been crawled
	ContentType string `json:"contentType,omitempty"`
	// Position indicates the semantic location of the link on the page
	// Values: "content", "navigation", "header", "footer", "sidebar", "breadcrumbs", "pagination", "unknown"
	Position string `json:"position,omitempty"`
	// DOMPath is a simplified DOM path showing the link's location in the HTML structure
	// Example: "body > main > article > p > a"
	DOMPath string `json:"domPath,omitempty"`
	// Action indicates how this URL should be handled
	// Values: "crawl" (normal), "record" (framework-specific, don't crawl), "skip" (ignored)
	Action URLAction `json:"action,omitempty"`
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
	// MetaDescription is the content of the <meta name="description"> tag
	MetaDescription string
	// Indexable indicates if search engines can index this page
	// Values: "Yes", "No", or "-" for non-HTML resources
	Indexable string
	// ContentType is the Content-Type header value (e.g., "text/html", "application/json")
	ContentType string
	// Error contains any error message if the crawl failed, empty otherwise
	Error string
	// Links contains all outbound links from this page (internal and external)
	Links *Links
	// ContentHash is the hash of the normalized page content (empty if content hashing is disabled)
	ContentHash string
	// IsDuplicateContent indicates if this content hash has been seen before on a different URL
	IsDuplicateContent bool

	// response stores the Response object for lazy content extraction via getter methods
	response *Response
}

// ResourceResult contains data from a visited resource (non-HTML asset)
type ResourceResult struct {
	// URL is the resource URL that was visited
	URL string
	// Status is the HTTP status code (e.g., 200, 404, 500)
	Status int
	// ContentType is the MIME type (e.g., "image/png", "text/css", "application/javascript")
	ContentType string
	// Error contains any error message if the visit failed, empty otherwise
	Error string
}

// OnPageCrawledFunc is called after each HTML page is successfully crawled or encounters an error.
// This callback receives HTML pages only, not resources.
// For resources (images, CSS, JS), use SetOnResourceVisit instead.
type OnPageCrawledFunc func(*PageResult)

// OnResourceVisitFunc is called for each resource (non-HTML asset) visited during crawling.
// Resources include images, stylesheets, scripts, and other non-HTML content.
// Use this for resource validation/checking without the overhead of PageResult.
type OnResourceVisitFunc func(*ResourceResult)

// OnCrawlCompleteFunc is called when the entire crawl finishes, either naturally or due to cancellation.
// Parameters:
//   - wasStopped: true if the crawl was stopped via context cancellation, false if it completed naturally
//   - totalPages: total number of pages that were successfully crawled (excludes errors)
//   - totalDiscovered: total number of unique URLs discovered during the crawl
type OnCrawlCompleteFunc func(wasStopped bool, totalPages int, totalDiscovered int)

// URLAction represents the action to take when a URL is discovered during crawling
type URLAction string

const (
	// URLActionCrawl indicates the URL should be added to links and crawled
	URLActionCrawl URLAction = "crawl"
	// URLActionRecordOnly indicates the URL should be added to links but NOT crawled (e.g., framework-specific paths)
	URLActionRecordOnly URLAction = "record"
	// URLActionSkip indicates the URL should be ignored completely (e.g., analytics/tracking URLs)
	URLActionSkip URLAction = "skip"
)

// OnURLDiscoveredFunc is called when a new URL is discovered during crawling.
// This callback is invoked exactly once per unique URL to determine how it should be handled.
// The return value indicates whether the URL should be crawled, recorded only, or skipped entirely.
// Use cases:
//   - Return URLActionCrawl for normal URLs that should be crawled
//   - Return URLActionRecordOnly for framework-specific paths that should appear in links but not be crawled
//   - Return URLActionSkip for analytics/tracking URLs that should be ignored completely
type OnURLDiscoveredFunc func(url string) URLAction

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
	onResourceVisit OnResourceVisitFunc
	onCrawlComplete OnCrawlCompleteFunc
	onURLDiscovered OnURLDiscoveredFunc

	// Internal state tracking
	queuedURLs   *sync.Map // map[string]URLAction - tracks discovered URLs and their actions
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

// SetOnPageCrawled registers a callback function that will be called after each HTML page is crawled.
// This callback receives complete page information including discovered URLs.
// Note: This is only called for HTML pages. For resources, use SetOnResourceVisit.
func (cr *Crawler) SetOnPageCrawled(f OnPageCrawledFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onPageCrawled = f
}

// SetOnResourceVisit registers a callback function that will be called after each resource is visited.
// This callback receives resource information (URL, status, content type) for non-HTML assets
// such as images, stylesheets, scripts, etc.
func (cr *Crawler) SetOnResourceVisit(f OnResourceVisitFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onResourceVisit = f
}

// SetOnCrawlComplete registers a callback function that will be called when the crawl finishes.
// This callback receives summary statistics about the completed crawl.
func (cr *Crawler) SetOnCrawlComplete(f OnCrawlCompleteFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onCrawlComplete = f
}

// SetOnURLDiscovered registers a callback function that will be called when a new URL is discovered.
// This callback is invoked exactly once per unique URL to determine the action to take.
// The callback should return:
//   - URLActionCrawl to crawl the URL normally
//   - URLActionRecordOnly to add the URL to links but not crawl it (e.g., framework-specific paths)
//   - URLActionSkip to ignore the URL completely (e.g., analytics/tracking URLs)
func (cr *Crawler) SetOnURLDiscovered(f OnURLDiscoveredFunc) {
	cr.mutex.Lock()
	defer cr.mutex.Unlock()
	cr.onURLDiscovered = f
}

// getOrDetermineURLAction determines the action for a URL by calling the OnURLDiscovered callback.
// The callback is invoked only once per unique URL (results are memoized in queuedURLs).
// On subsequent calls for the same URL, the cached action is returned.
// This ensures deduplication - the application callback won't be called multiple times for the same URL.
func (cr *Crawler) getOrDetermineURLAction(urlStr string) URLAction {
	// Check if we've already determined action for this URL
	if actionInterface, exists := cr.queuedURLs.Load(urlStr); exists {
		return actionInterface.(URLAction)
	}

	// First time seeing this URL - determine action via callback
	action := URLActionCrawl // default action if no callback is set
	cr.mutex.RLock()
	callback := cr.onURLDiscovered
	cr.mutex.RUnlock()

	if callback != nil {
		action = callback(urlStr)
	}

	// Store the action in queuedURLs (memoization - callback won't be called again for this URL)
	cr.queuedURLs.Store(urlStr, action)

	return action
}

// Start begins crawling from the specified starting URL.
// It returns immediately if the crawler is in Async mode, or blocks until completion otherwise.
func (cr *Crawler) Start(url string) error {
	// Extract and set root domain for internal/external classification
	cr.rootDomain = cr.extractRootDomain(url)

	// Determine action for base URL (always crawl it, but track it)
	cr.getOrDetermineURLAction(url)

	// Start crawling from base URL first
	if err := cr.Collector.Visit(url); err != nil {
		return err
	}

	// If sitemap discovery is enabled, fetch and queue sitemap URLs
	if cr.hasDiscoveryMechanism(DiscoverySitemap) {
		sitemapURLs := cr.fetchSitemapURLs(url)
		for _, sitemapURL := range sitemapURLs {
			// Determine action for this sitemap URL
			action := cr.getOrDetermineURLAction(sitemapURL)

			// Only visit URLs marked for crawling
			if action == URLActionCrawl {
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

		// Add network-discovered URLs from browser network monitoring (if JS rendering is enabled)
		if networkURLsJSON := e.Request.Ctx.Get("networkDiscoveredURLs"); networkURLsJSON != "" {
			var networkURLs []string
			if err := json.Unmarshal([]byte(networkURLsJSON), &networkURLs); err == nil {
				// Convert network URLs to Link objects and add them to allLinks
				for _, networkURL := range networkURLs {
					// Determine action for this URL (callback called once, result memoized)
					action := cr.getOrDetermineURLAction(networkURL)

					// Skip URLs marked for complete skip
					if action == URLActionSkip {
						continue
					}

					// Determine resource type from URL
					linkType := inferResourceType(networkURL)
					isInternal := cr.isInternalURL(networkURL)

					// Create link object with action
					link := Link{
						URL:        networkURL,
						Type:       linkType,
						Text:       "",
						Context:    "network",
						IsInternal: isInternal,
						Action:     action,
					}

					// Add to allLinks for reporting (both Crawl and RecordOnly)
					allLinks = append(allLinks, link)

					// Only queue for crawling if action is Crawl
					shouldCrawl := isInternal || cr.shouldValidateResource(link)
					if action == URLActionCrawl && shouldCrawl && cr.isURLCrawlable(networkURL) {
						// Visit this URL (action already stored in queuedURLs by getOrDetermineURLAction)
						e.Request.Visit(networkURL)
					}
				}
			}
		}

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

				// Only crawl links with Crawl action (skip RecordOnly and Skip)
				if link.Action != URLActionCrawl {
					continue
				}

				// Check if crawlable (filters, robots.txt)
				if !cr.isURLCrawlable(link.URL) {
					continue
				}

				// Visit this URL (action already stored in queuedURLs)
				e.Request.Visit(link.URL)
			}
		}

		// Resource validation: check configured resource types for broken links
		for _, link := range allLinks {
			if cr.shouldValidateResource(link) {
				// Only crawl resources with Crawl action
				if link.Action != URLActionCrawl {
					continue
				}

				// Check if crawlable (filters, robots.txt)
				if !cr.isURLCrawlable(link.URL) {
					continue
				}

				// Visit this resource (action already stored in queuedURLs)
				e.Request.Visit(link.URL)
			}
		}
	})

	// Capture page metadata (title, meta description, indexability)
	cr.Collector.OnHTML("html", func(e *HTMLElement) {
		// Store title in context for OnScraped to use
		title := e.ChildText("title")
		e.Request.Ctx.Put("title", title)

		// Extract meta description
		metaDesc := e.ChildAttr("meta[name='description']", "content")
		e.Request.Ctx.Put("metaDescription", metaDesc)

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

		// Get meta description from context (set by OnHTML)
		metaDescription := r.Ctx.Get("metaDescription")

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

		// Get content hash and duplicate status from context
		contentHash := r.Ctx.Get("contentHash")
		isDuplicateStr := r.Ctx.Get("isContentDuplicate")
		isDuplicate := isDuplicateStr == "true"

		result := &PageResult{
			URL:                pageURL,
			Status:             status,
			Title:              title,
			MetaDescription:    metaDescription,
			Indexable:          isIndexable,
			ContentType:        contentType,
			Error:              "",
			Links:              pageLinks,
			ContentHash:        contentHash,
			IsDuplicateContent: isDuplicate,
			response:           r,
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

		// For non-HTML content, route to OnResourceVisit callback instead of OnPageCrawled
		if !strings.Contains(contentType, "text/html") {
			// Note: We keep the body intact for backwards compatibility with any existing
			// OnResponse callbacks. Memory optimization can be added later if needed.

			// Store minimal metadata for link population (so links can show status/type)
			cr.pageMetadata.Store(pageURL, PageMetadata{
				Status:      r.StatusCode,
				Title:       "", // Resources don't have titles
				ContentType: contentType,
			})

			// Create ResourceResult (lightweight - no links, no content hash, no page fields)
			result := &ResourceResult{
				URL:         pageURL,
				Status:      r.StatusCode,
				ContentType: contentType,
				Error:       "",
			}

			// Call OnResourceVisit callback if set
			cr.callOnResourceVisit(result)

			// Extract URLs from CSS files for font and resource discovery
			if strings.Contains(contentType, "text/css") || strings.Contains(contentType, "stylesheet") {
				cssContent := string(r.Body)
				cssURLs := extractURLsFromCSS(cssContent)

				// Queue extracted URLs for crawling (if resource validation is enabled)
				for _, cssURL := range cssURLs {
					// Convert to absolute URL
					absoluteURL := r.Request.AbsoluteURL(cssURL)
					if absoluteURL == "" {
						continue
					}

					// Determine action for this URL (callback called once, result memoized)
					action := cr.getOrDetermineURLAction(absoluteURL)

					// Skip URLs marked for complete skip
					if action == URLActionSkip {
						continue
					}

					// Determine resource type from URL/extension
					resourceType := inferResourceType(absoluteURL)

					// Create a link object to check if we should validate this resource
					isInternal := cr.isInternalURL(absoluteURL)
					link := Link{
						URL:        absoluteURL,
						Type:       resourceType,
						IsInternal: isInternal,
						Action:     action,
					}

					// Always crawl internal resources from CSS (like fonts, images)
					// For external resources, only crawl if resource validation is enabled
					shouldCrawl := isInternal || cr.shouldValidateResource(link)

					// Only visit URLs marked for crawling
					if action == URLActionCrawl && shouldCrawl && cr.isURLCrawlable(absoluteURL) {
						// Visit this URL (action already stored in queuedURLs)
						r.Request.Visit(absoluteURL)
					}
				}
			}

			// Note: We don't return here to allow extensions and low-level code that
			// registers OnResponse callbacks on crawler.Collector to process all responses.
			// For example, the Referer extension (extensions/referer.go) tracks all responses
			// to set referer headers. OnScraped will return early for non-HTML anyway,
			// so no duplicate processing occurs
		}
	})

	// Handle errors (both pages and resources)
	cr.Collector.OnError(func(r *Response, err error) {
		// Skip already visited errors - these are handled by deduplication
		if strings.Contains(err.Error(), "already visited") {
			return
		}

		// Handle nil response
		if r == nil || r.Request == nil || r.Request.URL == nil {
			return
		}

		pageURL := r.Request.URL.String()

		// Determine if this error is for a resource or a page
		// 1. Try to get Content-Type from response headers (if available)
		contentType := ""
		if r.Headers != nil {
			contentType = r.Headers.Get("Content-Type")
		}

		// 2. Fall back to URL extension heuristic if no Content-Type
		isResource := false
		if contentType != "" {
			isResource = !strings.Contains(contentType, "text/html")
		} else {
			isResource = cr.isLikelyResource(pageURL)
		}

		// Route to appropriate callback
		if isResource {
			// Resource error - send to OnResourceVisit
			result := &ResourceResult{
				URL:         pageURL,
				Status:      0, // 0 indicates network/request error
				ContentType: contentType,
				Error:       err.Error(),
			}
			cr.callOnResourceVisit(result)
		} else {
			// Page error - send to OnPageCrawled
			pageLinks := cr.buildPageLinks(pageURL, pageOutboundLinks)

			result := &PageResult{
				URL:                pageURL,
				Status:             0,
				Title:              "",
				MetaDescription:    "",
				Indexable:          "No",
				ContentType:        contentType,
				Error:              err.Error(),
				Links:              pageLinks,
				ContentHash:        "",
				IsDuplicateContent: false,
				response:           r,
			}
			cr.callOnPageCrawled(result)
		}
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

// isLikelyResource checks if a URL is likely a resource (non-HTML) based on its extension.
// Used as a fallback when Content-Type is not available (e.g., network errors).
func (cr *Crawler) isLikelyResource(urlStr string) bool {
	urlLower := strings.ToLower(urlStr)

	// Common resource extensions
	resourceExtensions := []string{
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico", // Images
		".css",                                    // Stylesheets
		".js",                                     // Scripts
		".woff", ".woff2", ".ttf", ".eot", ".otf", // Fonts
		".mp4", ".webm", ".ogg", ".mp3", ".wav", // Media
		".pdf", ".zip", ".gz", ".tar", // Documents/Archives
	}

	for _, ext := range resourceExtensions {
		if strings.HasSuffix(urlLower, ext) {
			return true
		}
	}

	return false
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

// callOnResourceVisit safely calls the OnResourceVisit callback if set
func (cr *Crawler) callOnResourceVisit(result *ResourceResult) {
	cr.mutex.RLock()
	callback := cr.onResourceVisit
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
	addLink := func(url, linkType, text, context string, elem *HTMLElement) {
		absoluteURL := e.Request.AbsoluteURL(url)
		if absoluteURL == "" {
			return
		}

		// Skip fragment-only links
		if strings.HasPrefix(url, "#") {
			return
		}

		// Determine action for this URL (callback called once, result memoized)
		action := cr.getOrDetermineURLAction(absoluteURL)

		// Skip URLs marked for complete skip
		if action == URLActionSkip {
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

		// Extract link position and DOM path
		position, domPath := extractLinkPosition(elem)

		links = append(links, Link{
			URL:         absoluteURL,
			Type:        linkType,
			Text:        text,
			Context:     context,
			IsInternal:  isInternal,
			Status:      status,
			Title:       title,
			ContentType: contentType,
			Position:    position,
			DOMPath:     domPath,
			Action:      action,
		})
	}

	// Extract anchor links <a href="">
	e.ForEach("a[href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		text := strings.TrimSpace(elem.Text)
		context := extractLinkContext(elem)
		addLink(href, "anchor", text, context, elem)
	})

	// Extract image links <img src="">
	e.ForEach("img[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		alt := elem.Attr("alt")
		addLink(src, "image", alt, "", elem)
	})

	// Extract script links <script src="">
	e.ForEach("script[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "script", "", "", elem)
	})

	// Extract stylesheet links <link rel="stylesheet" href="">
	e.ForEach("link[rel='stylesheet'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "stylesheet", "", "", elem)
	})

	// Extract canonical links <link rel="canonical" href="">
	e.ForEach("link[rel='canonical'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "canonical", "", "", elem)
	})

	// Extract preload hints <link rel="preload" href="">
	// Modern frameworks (Next.js, etc.) use these extensively for critical resources
	e.ForEach("link[rel='preload'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		as := elem.Attr("as") // Get the resource type hint

		// Map "as" attribute to link type
		linkType := "other"
		switch as {
		case "script":
			linkType = "script"
		case "style":
			linkType = "stylesheet"
		case "image":
			linkType = "image"
		case "font":
			linkType = "font"
		case "video":
			linkType = "video"
		case "audio":
			linkType = "audio"
		}

		addLink(href, linkType, "", "", elem)
	})

	// Extract modulepreload hints <link rel="modulepreload" href="">
	// Used for ES modules that should be preloaded
	e.ForEach("link[rel='modulepreload'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "script", "", "", elem)
	})

	// Extract prefetch hints <link rel="prefetch" href="">
	// Used for resources that might be needed for future navigation
	e.ForEach("link[rel='prefetch'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		as := elem.Attr("as") // Get the resource type hint

		// Map "as" attribute to link type
		linkType := "other"
		switch as {
		case "script":
			linkType = "script"
		case "style":
			linkType = "stylesheet"
		case "image":
			linkType = "image"
		case "document":
			linkType = "anchor" // Prefetched HTML pages
		}

		addLink(href, linkType, "", "", elem)
	})

	// Extract iframe links <iframe src="">
	e.ForEach("iframe[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "iframe", "", "", elem)
	})

	// Extract video sources <video src=""> and <source src="">
	e.ForEach("video[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "", "", elem)
	})
	e.ForEach("video source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "", "", elem)
	})

	// Extract audio sources <audio src=""> and <source src="">
	e.ForEach("audio[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "", "", elem)
	})
	e.ForEach("audio source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "", "", elem)
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

// shouldValidateResource checks if a resource link should be validated for broken links.
// Returns true if the resource should be crawled to check its status.
func (cr *Crawler) shouldValidateResource(link Link) bool {
	config := cr.Collector.ResourceValidation

	if config == nil || !config.Enabled {
		return false
	}

	// Don't validate anchors (handled separately by spider mode)
	if link.Type == "anchor" {
		return false
	}

	// Check external filter
	if !config.CheckExternal && !link.IsInternal {
		return false
	}

	// Check resource type filter
	if len(config.ResourceTypes) > 0 {
		allowed := false
		for _, rt := range config.ResourceTypes {
			if rt == link.Type {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	return true
}

// GetHTML returns the full HTML content of the page.
// Returns empty string if the response is not available.
func (pr *PageResult) GetHTML() string {
	if pr.response == nil {
		return ""
	}
	return string(pr.response.Body)
}

// GetTextFull returns all visible text from the entire page (including navigation, headers, footers).
// HTML tags are stripped, leaving only the text content.
// Returns empty string if the response is not available or is not HTML.
func (pr *PageResult) GetTextFull() string {
	if pr.response == nil || !strings.Contains(pr.ContentType, "text/html") {
		return ""
	}
	return extractAllText(pr.response.Body)
}

// GetTextContent returns text from the main content area only (excluding navigation, headers, footers).
// Extracts text from semantic HTML5 elements like <article>, <main>, or [role="main"].
// Returns empty string if the response is not available or is not HTML.
func (pr *PageResult) GetTextContent() string {
	if pr.response == nil || !strings.Contains(pr.ContentType, "text/html") {
		return ""
	}
	return extractMainContentText(pr.response.Body)
}

// extractURLsFromCSS extracts resource URLs from CSS content.
// It finds URLs from url() functions in CSS, commonly used for:
// - Font files (@font-face declarations)
// - Background images
// - Other resources
// Returns a list of URLs found in the CSS.
func extractURLsFromCSS(cssContent string) []string {
	// Strip CSS comments (/* ... */) before extracting URLs
	// This prevents extracting URLs from commented-out code
	commentRegex := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	cssContent = commentRegex.ReplaceAllString(cssContent, "")

	// Regex to match url() in CSS
	// Matches: url("path"), url('path'), url(path)
	re := regexp.MustCompile(`url\s*\(\s*['"]?([^'")]+)['"]?\s*\)`)
	matches := re.FindAllStringSubmatch(cssContent, -1)

	var urls []string
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) > 1 {
			url := strings.TrimSpace(match[1])
			// Skip data URLs and empty strings
			if url != "" && !strings.HasPrefix(url, "data:") && !seen[url] {
				urls = append(urls, url)
				seen[url] = true
			}
		}
	}

	return urls
}

// inferResourceType determines the resource type from a URL based on its extension or path patterns
func inferResourceType(urlStr string) string {
	urlLower := strings.ToLower(urlStr)

	// Check for common resource patterns and extensions
	switch {
	// Images
	case strings.Contains(urlLower, ".jpg") || strings.Contains(urlLower, ".jpeg") ||
		strings.Contains(urlLower, ".png") || strings.Contains(urlLower, ".gif") ||
		strings.Contains(urlLower, ".webp") || strings.Contains(urlLower, ".svg") ||
		strings.Contains(urlLower, ".ico") || strings.Contains(urlLower, "/image?"):
		return "image"

	// JavaScript
	case strings.Contains(urlLower, ".js") || strings.Contains(urlLower, ".mjs"):
		return "script"

	// Stylesheets
	case strings.Contains(urlLower, ".css"):
		return "stylesheet"

	// Fonts
	case strings.Contains(urlLower, ".woff") || strings.Contains(urlLower, ".woff2") ||
		strings.Contains(urlLower, ".ttf") || strings.Contains(urlLower, ".eot") ||
		strings.Contains(urlLower, ".otf"):
		return "font"

	// Video
	case strings.Contains(urlLower, ".mp4") || strings.Contains(urlLower, ".webm") ||
		strings.Contains(urlLower, ".ogg") || strings.Contains(urlLower, ".avi"):
		return "video"

	// Audio
	case strings.Contains(urlLower, ".mp3") || strings.Contains(urlLower, ".wav") ||
		strings.Contains(urlLower, ".flac") || strings.Contains(urlLower, ".aac"):
		return "audio"

	// API endpoints (heuristic: contains /api/ in path)
	case strings.Contains(urlLower, "/api/"):
		return "api"

	// Default to "other" for unknown types
	default:
		return "other"
	}
}

