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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentberlin/bluesnake/storage"
	"github.com/temoto/robotstxt"
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
	// Follow indicates if search engines should follow this link
	// true if no nofollow/sponsored/ugc in rel attribute
	Follow bool `json:"follow"`
	// Rel is the full rel attribute value (e.g., "nofollow", "noopener noreferrer")
	Rel string `json:"rel,omitempty"`
	// Target is the target attribute value (_blank, _self, etc.)
	Target string `json:"target,omitempty"`
	// PathType indicates the URL format: "Absolute", "Root-Relative", or "Relative"
	PathType string `json:"pathType,omitempty"`
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
	// H1 is the text of the first <h1> tag on the page
	H1 string
	// H2 is the text of the first <h2> tag on the page
	H2 string
	// CanonicalURL is the canonical URL specified in <link rel="canonical">
	CanonicalURL string
	// WordCount is the approximate word count of visible text on the page
	WordCount int
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
	// Depth is the crawl depth (0 = start URL, 1 = discovered from start, etc.)
	Depth int

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
	// Depth is the crawl depth (0 = start URL, 1 = discovered from start, etc.)
	Depth int
}

// OnPageCrawledFunc is called after each HTML page is successfully crawled or encounters an error.
// This callback receives HTML pages only, not resources.
// For resources (images, CSS, JS), use SetOnResourceVisit instead.
type OnPageCrawledFunc func(*PageResult)

// OnResourceVisitFunc is called for each resource (non-HTML asset) visited during crawling.
// Resources include images, stylesheets, scripts, and other non-HTML content.
// Use this for resource validation/checking without the overhead of PageResult.
type OnResourceVisitFunc func(*ResourceResult)

// CrawlCompletionReason indicates why a crawl completed
type CrawlCompletionReason string

const (
	// CompletionReasonExhausted means all discoverable URLs have been crawled
	CompletionReasonExhausted CrawlCompletionReason = "exhausted"
	// CompletionReasonBudgetReached means the MaxURLsToVisit limit was reached
	CompletionReasonBudgetReached CrawlCompletionReason = "budget_reached"
	// CompletionReasonCancelled means the crawl was stopped via context cancellation
	CompletionReasonCancelled CrawlCompletionReason = "cancelled"
)

// CrawlResult contains comprehensive information about a completed crawl
type CrawlResult struct {
	// Reason indicates why the crawl completed
	Reason CrawlCompletionReason
	// TotalPages is the total number of HTML pages successfully crawled
	TotalPages int
	// TotalDiscovered is the total number of unique URLs discovered
	TotalDiscovered int
	// URLsVisited is the number of URLs visited in this session
	URLsVisited int
	// PendingURLs contains URLs that were queued but not visited (for resume)
	PendingURLs []URLDiscoveryRequest
}

// OnCrawlCompleteFunc is called when the entire crawl finishes.
// The CrawlResult contains comprehensive information about how and why the crawl completed,
// including the completion reason (exhausted, budget_reached, or cancelled) and pending URLs
// for incremental crawling support.
type OnCrawlCompleteFunc func(result *CrawlResult)

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

// URLDiscoveryRequest represents a URL discovered during crawling
type URLDiscoveryRequest struct {
	URL       string   // The discovered URL
	Source    string   // Discovery source: "initial", "sitemap", "spider", "network", "resource"
	ParentURL string   // URL where this was discovered (for spider/network)
	Depth     int      // Crawl depth
	Context   *Context // Request context for passing metadata
}

// Crawler provides a high-level interface for web crawling with callbacks for page results
type Crawler struct {
	// Collector is the underlying low-level collector (exported for advanced configuration)
	Collector       *Collector
	ctx             context.Context // Lifecycle context for crawl management
	onPageCrawled   OnPageCrawledFunc
	onResourceVisit OnResourceVisitFunc
	onCrawlComplete OnCrawlCompleteFunc
	onURLDiscovered OnURLDiscoveredFunc

	// Internal state tracking
	store        *storage.CrawlerStore // Crawler's own storage (visit tracking, URL actions, page metadata)
	crawledPages int
	mutex        sync.RWMutex

	// Link tracking
	rootDomain string // Root domain for internal/external classification

	// Discovery configuration
	discoveryMechanisms []DiscoveryMechanism // Enabled discovery mechanisms
	sitemapURLs         []string             // Custom sitemap URLs (nil = try defaults)

	// URL filtering configuration (owned by Crawler, not Collector)
	allowedDomains         []string          // Domain whitelist
	disallowedDomains      []string          // Domain blacklist
	urlFilters             []*regexp.Regexp  // URL pattern whitelist
	disallowedURLFilters   []*regexp.Regexp  // URL pattern blacklist
	maxDepth               int               // Maximum crawl depth (0 = infinite)

	// Channel-based URL processing (eliminates race conditions)
	discoveryChannel chan URLDiscoveryRequest // URLs to process
	processorDone    chan struct{}            // Signals processor completion
	workerPool       *WorkerPool              // Controlled worker pool
	droppedURLs      uint64                   // Count of URLs dropped due to full channel

	// Work coordination
	wg sync.WaitGroup // Tracks pending work items (queued + processing)

	// Crawler directive fields (policy enforcement)
	resourceValidation         *ResourceValidationConfig // Resource validation configuration
	robotsTxtMode              string                    // robots.txt handling mode: "respect", "ignore", "ignore-report"

	// Debug configuration
	debugURLs []string // URLs to log detailed processing information for (race condition debugging)
	followInternalNofollow     bool                      // Allow following internal nofollow links
	followExternalNofollow     bool                      // Allow following external nofollow links
	respectMetaRobotsNoindex   bool                      // Respect meta robots noindex
	respectNoindex             bool                      // Respect X-Robots-Tag noindex
	robotsMap                  map[string]*robotstxt.RobotsData // robots.txt cache

	// Incremental crawling support
	maxURLsToVisit int64             // Max URLs to visit before pausing (0 = unlimited)
	visitedCount   int64             // Atomic counter for URLs visited in this session
	seedURLs       []URLDiscoveryRequest // URLs to queue at start (for resume)
}

// NewCrawler creates a high-level crawler with the specified context and crawler configuration.
// The context is used for crawl lifecycle management and cancellation.
// The returned crawler must have its callbacks set via SetOnPageCrawled and SetOnCrawlComplete
// before calling Start. If config is nil, default configuration is used.
func NewCrawler(ctx context.Context, config *CrawlerConfig) *Crawler {
	if config == nil {
		config = NewDefaultConfig()
	}

	// Create the underlying Collector with HTTP configuration
	c := NewCollector(ctx, config.HTTP)

	// Apply defaults for discovery mechanisms if not specified
	discoveryMechanisms := config.DiscoveryMechanisms
	if len(discoveryMechanisms) == 0 {
		discoveryMechanisms = []DiscoveryMechanism{DiscoverySpider} // Default to spider mode
	}

	// Determine channel sizes with defaults
	// Use larger buffer for incremental crawling to avoid dropping URLs
	discoveryChannelSize := config.DiscoveryChannelSize
	if discoveryChannelSize == 0 {
		if config.MaxURLsToVisit > 0 {
			discoveryChannelSize = 500000 // Default: 500k URLs for incremental crawling
		} else {
			discoveryChannelSize = 50000 // Default: 50k URLs for regular crawling
		}
	}

	workQueueSize := config.WorkQueueSize
	if workQueueSize == 0 {
		workQueueSize = 1000 // Default: 1k pending work items
	}

	parallelism := config.Parallelism
	if parallelism == 0 {
		parallelism = 10 // Default: 10 concurrent fetches
	}

	crawler := &Crawler{
		Collector:            c,
		ctx:                  ctx,
		store:                storage.NewCrawlerStore(),
		crawledPages:         0,
		discoveryMechanisms:  discoveryMechanisms,
		sitemapURLs:          config.SitemapURLs,
		discoveryChannel:     make(chan URLDiscoveryRequest, discoveryChannelSize),
		processorDone:        make(chan struct{}),
		workerPool:           NewWorkerPool(ctx, parallelism, workQueueSize),
		droppedURLs:          0,
		allowedDomains:       config.AllowedDomains,
		disallowedDomains:    config.DisallowedDomains,
		urlFilters:           config.URLFilters,
		disallowedURLFilters: config.DisallowedURLFilters,
		maxDepth:             config.MaxDepth,
		// Crawler directive fields (policy enforcement - moved from Collector)
		resourceValidation:       config.ResourceValidation,
		robotsTxtMode:            config.RobotsTxtMode,
		followInternalNofollow:   config.FollowInternalNofollow,
		followExternalNofollow:   config.FollowExternalNofollow,
		respectMetaRobotsNoindex: config.RespectMetaRobotsNoindex,
		respectNoindex:           config.RespectNoindex,
		robotsMap:                make(map[string]*robotstxt.RobotsData),
		// Debug configuration
		debugURLs: config.DebugURLs,
		// Incremental crawling configuration
		maxURLsToVisit: int64(config.MaxURLsToVisit),
		visitedCount:   0,
		seedURLs:       config.SeedURLs,
	}

	// Pre-populate visited set for resume (skip URLs already crawled in previous sessions)
	if len(config.PreVisitedHashes) > 0 {
		for _, hash := range config.PreVisitedHashes {
			crawler.store.PreMarkVisited(hash)
		}
	}

	// Set IgnoreRobotsTxt on Collector based on RobotsTxtMode
	// This is a HTTP-level setting that tells Collector not to check robots.txt
	// (Crawler will handle robots.txt checking before URL enqueueing)
	switch config.RobotsTxtMode {
	case "ignore":
		c.IgnoreRobotsTxt = true
	case "respect", "ignore-report":
		c.IgnoreRobotsTxt = false
	default:
		// Default to "respect" mode
		c.IgnoreRobotsTxt = false
	}

	// Set up redirect handler to inject Crawler's URL filtering and visit tracking
	crawler.setupRedirectHandler()

	crawler.setupCallbacks()
	return crawler
}

// shouldDebugURL checks if detailed logging should be enabled for this URL
// Used for race condition debugging by filtering logs to specific problematic URLs
// Uses exact matching to avoid logging all subpaths
func (cr *Crawler) shouldDebugURL(urlStr string) bool {
	if len(cr.debugURLs) == 0 {
		return false
	}

	// Normalize URL for comparison (remove trailing slash, scheme variations)
	normalizedURL := strings.TrimSuffix(urlStr, "/")
	normalizedURL = strings.TrimPrefix(normalizedURL, "https://")
	normalizedURL = strings.TrimPrefix(normalizedURL, "http://")

	for _, debugURL := range cr.debugURLs {
		normalizedDebug := strings.TrimSuffix(debugURL, "/")
		normalizedDebug = strings.TrimPrefix(normalizedDebug, "https://")
		normalizedDebug = strings.TrimPrefix(normalizedDebug, "http://")

		// Exact match only
		if normalizedURL == normalizedDebug {
			return true
		}
	}
	return false
}

// setupRedirectHandler configures the Collector's redirect handling to integrate with Crawler's
// architecture. This callback is invoked by the HTTP client for each redirect before following it.
//
// Responsibilities:
// 1. URL Validation - Applies Crawler's domain filters and URL patterns to redirect destinations
//
// Note: Visit tracking for redirect destinations happens in the normal response flow (FetchURL),
// not here. This ensures redirect destinations are properly crawled and their callbacks are called.
// This maintains architectural separation: Crawler handles business logic (filtering, tracking),
// while Collector handles HTTP mechanics (following redirects, managing connections).
func (cr *Crawler) setupRedirectHandler() {
	// Set up URL filtering for redirects
	// The manual redirect loop in http_backend.go:Do() calls CheckRedirect for each redirect (line 237),
	// which invokes this OnRedirect callback, allowing us to filter redirect destinations
	cr.Collector.OnRedirect(func(req *http.Request, via []*http.Request) error {
		// Apply Crawler's URL filtering logic (domain filters, URL patterns)
		if !cr.isURLCrawlable(req.URL.String()) {
			return fmt.Errorf("redirect destination blocked by URL filters")
		}
		// URL is allowed - return nil to continue
		return nil
	})

	// Process intermediate redirects BEFORE main response processing completes
	// We register this callback early so it fires first and reports redirects before the final destination
	cr.Collector.OnResponse(func(r *Response) {
		// Check if this response has a redirect chain
		if len(r.RedirectChain) == 0 {
			return
		}

		// Determine content type from the FINAL destination (not the redirect itself)
		// This is because redirects don't have content, so we use the final destination's type
		// to categorize all URLs in the chain
		finalContentType := r.Headers.Get("Content-Type")
		isFinalHTML := strings.Contains(finalContentType, "text/html")

		// Mark the FINAL destination as visited first (the URL we actually fetched)
		// This ensures the final URL is marked even though it wasn't explicitly queued
		finalURL := r.Request.URL.String()
		finalHash := requestHash(finalURL, nil)
		alreadyVisited, _ := cr.store.VisitIfNotVisited(finalHash)

		debugFinal := cr.shouldDebugURL(finalURL)
		if debugFinal {
			log.Printf("[DEBUG-REDIRECT] Final destination: %s (already_visited=%v, content_type=%s, status=%d)",
				finalURL, alreadyVisited, finalContentType, r.StatusCode)
		}

		// Process each intermediate redirect in the chain
		// For redirect chain A→B→C, RedirectChain contains [A, B]
		// The final response (C) is processed normally via OnHTML/OnScraped
		for i, redirectResp := range r.RedirectChain {
			// Mark the redirect URL as visited to prevent re-crawling
			redirectHash := requestHash(redirectResp.URL, nil)
			alreadyVisited, _ := cr.store.VisitIfNotVisited(redirectHash)

			if cr.shouldDebugURL(redirectResp.URL) || cr.shouldDebugURL(finalURL) {
				log.Printf("[DEBUG-REDIRECT] Chain[%d]: %s -> status=%d (already_visited=%v)",
					i, redirectResp.URL, redirectResp.StatusCode, alreadyVisited)
			}

			// Get the Content-Type from the redirect response itself (may be empty)
			redirectContentType := redirectResp.Headers.Get("Content-Type")

			// If redirect doesn't have a Content-Type, use the final destination's type
			// This handles the common case where redirects don't specify Content-Type
			if redirectContentType == "" {
				redirectContentType = finalContentType
			}

			// Store metadata for the redirect (for link population)
			cr.store.StoreMetadata(redirectResp.URL, PageMetadata{
				Status:      redirectResp.StatusCode,
				Title:       "", // Redirects don't have page content
				ContentType: redirectContentType,
			})

			// Report redirect based on final destination type
			// If final destination is HTML, report redirect as PageResult
			// If final destination is resource, report redirect as ResourceResult
			if isFinalHTML {
				// Create PageResult for HTML redirect
				result := &PageResult{
					URL:                redirectResp.URL,
					Status:             redirectResp.StatusCode,
					Title:              "",
					MetaDescription:    "",
					Indexable:          "Yes", // Redirects are typically indexable (search engines follow them)
					ContentType:        redirectContentType,
					Error:              "",
					Links:              &Links{Internal: []Link{}, External: []Link{}}, // No links for redirects
					ContentHash:        "",
					IsDuplicateContent: false,
					Depth:              r.Request.Depth,
					response:           nil,
				}
				cr.callOnPageCrawled(result)
			} else {
				// Create ResourceResult for non-HTML redirect
				result := &ResourceResult{
					URL:         redirectResp.URL,
					Status:      redirectResp.StatusCode,
					ContentType: redirectContentType,
					Error:       "",
					Depth:       r.Request.Depth,
				}
				cr.callOnResourceVisit(result)
			}
		}
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


// queueURL sends a URL to the discovery channel for processing.
// This is called by all discovery mechanisms (initial, sitemap, spider, network, resources).
// Uses non-blocking sends to prevent deadlocks.
//
// CRITICAL: This increments the Crawler's WaitGroup to track pending work.
// The WaitGroup is decremented when processing completes (in the worker function).
// This ensures Wait() doesn't return while work is still queued or being processed.
func (cr *Crawler) queueURL(req URLDiscoveryRequest) {
	debugThis := cr.shouldDebugURL(req.URL)
	if debugThis {
		log.Printf("[DEBUG-QUEUE] ATTEMPT: url=%s, source=%s, parent=%s, depth=%d",
			req.URL, req.Source, req.ParentURL, req.Depth)
	}

	// Add to WaitGroup BEFORE queuing to track pending work
	// This ensures wg counts both "queued" and "processing" work
	cr.wg.Add(1)

	select {
	case cr.discoveryChannel <- req:
		// Successfully queued (non-blocking if channel has buffer space)
		if debugThis {
			log.Printf("[DEBUG-QUEUE] QUEUED: url=%s", req.URL)
		}

	case <-cr.ctx.Done():
		// Failed to queue due to cancellation - undo the Add
		if debugThis {
			log.Printf("[DEBUG-QUEUE] CANCELLED: url=%s", req.URL)
		}
		cr.wg.Done()

	default:
		// Failed to queue due to full channel - undo the Add
		if debugThis {
			log.Printf("[DEBUG-QUEUE] DROPPED: url=%s (channel full)", req.URL)
		}
		cr.wg.Done()
		if req.Source == "initial" {
			// Never drop the initial URL - log error
			log.Printf("ERROR: Dropped initial URL due to full channel: %s", req.URL)
		}
		atomic.AddUint64(&cr.droppedURLs, 1)
	}
}

// runURLProcessor is the SINGLE goroutine that processes all discovered URLs.
// This eliminates race conditions by serializing all visit decisions.
func (cr *Crawler) runURLProcessor() {
	defer close(cr.processorDone)

	for {
		select {
		case req, ok := <-cr.discoveryChannel:
			if !ok {
				// Channel closed - no more URLs to process
				return
			}
			// Process this URL (serialized - no race possible)
			cr.processDiscoveredURL(req)

		case <-cr.ctx.Done():
			// Context cancelled - drain remaining URLs
			for {
				select {
				case req := <-cr.discoveryChannel:
					cr.processDiscoveredURL(req)
				default:
					return // Channel empty
				}
			}
		}
	}
}

// processDiscoveredURL handles a single discovered URL.
// This runs in the processor goroutine, so only ONE instance executes at a time.
//
// CRITICAL: This function is responsible for ensuring wg.Done() is called.
// wg.Done() must be called for every URL that was queued (wg.Add in queueURL).
func (cr *Crawler) processDiscoveredURL(req URLDiscoveryRequest) {
	debugThis := cr.shouldDebugURL(req.URL)
	if debugThis {
		log.Printf("[DEBUG-PROCESS] START: url=%s, source=%s, parent=%s, depth=%d",
			req.URL, req.Source, req.ParentURL, req.Depth)
	}

	// Step 1: Determine action for this URL (callback called once, memoized)
	action := cr.getOrDetermineURLAction(req.URL)
	if debugThis {
		log.Printf("[DEBUG-PROCESS] Action determined: url=%s, action=%s", req.URL, action)
	}
	if action == URLActionSkip {
		if debugThis {
			log.Printf("[DEBUG-PROCESS] SKIP: url=%s (action=skip)", req.URL)
		}
		cr.wg.Done() // Work item complete (skipped)
		return
	}

	// Step 2: Check max depth
	if cr.maxDepth > 0 && req.Depth > cr.maxDepth {
		cr.wg.Done() // Work item complete (max depth exceeded)
		return
	}

	// Step 3: Pre-filtering (domain checks, URL filters)
	if !cr.isURLCrawlable(req.URL) {
		cr.wg.Done() // Work item complete (filtered)
		return
	}

	// Step 3.5: Check robots.txt (in async context, safe to make HTTP requests)
	if !cr.shouldIgnoreRobotsTxt() {
		parsedURL, err := url.Parse(req.URL)
		if err == nil {
			if err := cr.checkRobots(parsedURL); err != nil {
				// URL blocked by robots.txt
				cr.wg.Done() // Work item complete (robots.txt blocked)
				return
			}
		}
		// If parse error, allow URL (don't block due to URL parsing issues)
	}

	// Step 4: Check if already visited and mark as visited (ATOMIC)
	// This is the CRITICAL section - only ONE goroutine executes this
	uHash := requestHash(req.URL, nil)
	alreadyVisited, err := cr.store.VisitIfNotVisited(uHash)
	if debugThis {
		log.Printf("[DEBUG-PROCESS] Visit check: url=%s, already_visited=%v, err=%v",
			req.URL, alreadyVisited, err)
	}
	if err != nil {
		if debugThis {
			log.Printf("[DEBUG-PROCESS] ERROR: url=%s, err=%v", req.URL, err)
		}
		cr.wg.Done() // Work item complete (error)
		return
	}
	if alreadyVisited {
		if debugThis {
			log.Printf("[DEBUG-PROCESS] ALREADY_VISITED: url=%s", req.URL)
		}
		cr.wg.Done() // Work item complete (already visited)
		return
	}

	if debugThis {
		log.Printf("[DEBUG-PROCESS] MARKED_VISITED: url=%s (first time)", req.URL)
	}

	// Step 4.5: Check MaxURLsToVisit limit (incremental crawling)
	// Only count URLs we're actually going to crawl (not record-only)
	if action == URLActionCrawl && cr.maxURLsToVisit > 0 {
		newCount := atomic.AddInt64(&cr.visitedCount, 1)
		if newCount > cr.maxURLsToVisit {
			// We've exceeded the limit - this URL won't be crawled
			// Decrement the counter since we won't actually visit it
			atomic.AddInt64(&cr.visitedCount, -1)
			if debugThis {
				log.Printf("[DEBUG-PROCESS] LIMIT_REACHED: url=%s (visited=%d, limit=%d)",
					req.URL, newCount-1, cr.maxURLsToVisit)
			}
			cr.wg.Done() // Work item complete (limit reached)
			return
		}
		if debugThis {
			log.Printf("[DEBUG-PROCESS] VISIT_COUNT: url=%s (count=%d, limit=%d)",
				req.URL, newCount, cr.maxURLsToVisit)
		}
	}

	// Step 5: URL is now marked as visited - we own it
	// Only crawl if action is "crawl" (not "record")
	if action != URLActionCrawl {
		if debugThis {
			log.Printf("[DEBUG-PROCESS] RECORD_ONLY: url=%s (action=%s)", req.URL, action)
		}
		cr.wg.Done() // Work item complete (record-only)
		return
	}

	// Step 6: Submit to worker pool for actual fetching
	// CRITICAL: This blocks if worker pool queue is full
	// This backpressure prevents the processor from racing ahead
	if debugThis {
		log.Printf("[DEBUG-PROCESS] SUBMITTING_TO_WORKER: url=%s", req.URL)
	}
	err = cr.workerPool.Submit(func() {
		// Ensure wg.Done() is called when this worker finishes
		// This happens AFTER FetchURL() and ALL callbacks complete
		defer cr.wg.Done()

		if debugThis {
			log.Printf("[DEBUG-PROCESS] WORKER_START: url=%s", req.URL)
		}

		// We already marked this URL as visited in step 4 above.
		// Call FetchURL() directly - no visit checking needed.
		cr.Collector.FetchURL(req.URL, "GET", req.Depth, nil, req.Context, nil)

		if debugThis {
			log.Printf("[DEBUG-PROCESS] WORKER_COMPLETE: url=%s", req.URL)
		}
		// wg.Done() via defer happens here
	})

	if err != nil {
		// Submit failed - worker will never run, so we must call Done() here
		cr.wg.Done()
		return
	}

	// Successfully submitted to worker pool
	// Worker will fetch it and call wg.Done() when complete
}

// getOrDetermineURLAction determines the action for a URL by calling the OnURLDiscovered callback.
// The callback is invoked only once per unique URL (results are memoized in store).
// On subsequent calls for the same URL, the cached action is returned.
// This ensures deduplication - the application callback won't be called multiple times for the same URL.
func (cr *Crawler) getOrDetermineURLAction(urlStr string) URLAction {
	// Check if we've already determined action for this URL
	if actionInterface, exists := cr.store.GetAction(urlStr); exists {
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

	// Store the action in store (memoization - callback won't be called again for this URL)
	cr.store.StoreAction(urlStr, action)

	return action
}

// Start begins crawling from the specified starting URL.
// It returns immediately if the crawler is in Async mode, or blocks until completion otherwise.
func (cr *Crawler) Start(url string) error {
	// Extract and set root domain for internal/external classification
	cr.rootDomain = cr.extractRootDomain(url)

	// Start the URL processor goroutine
	go cr.runURLProcessor()

	// Queue seed URLs for resume (before the initial URL to maintain queue order)
	// These are URLs from a previous paused crawl that need to be re-processed
	for _, seed := range cr.seedURLs {
		cr.queueURL(seed)
	}

	// Queue the initial URL
	cr.queueURL(URLDiscoveryRequest{
		URL:    url,
		Source: "initial",
		Depth:  0,
	})

	// If sitemap discovery is enabled, fetch and queue sitemap URLs asynchronously
	// This prevents blocking the crawler startup and avoids race conditions where
	// slow/failed sitemap fetches cause URLs to be missed
	if cr.hasDiscoveryMechanism(DiscoverySitemap) {
		cr.wg.Add(1) // Track sitemap fetch as pending work
		go func() {
			defer cr.wg.Done()
			sitemapURLs := cr.fetchSitemapURLs(url)
			for _, sitemapURL := range sitemapURLs {
				cr.queueURL(URLDiscoveryRequest{
					URL:    sitemapURL,
					Source: "sitemap",
					Depth:  1,
				})
			}
		}()
	}

	// Spider mode link following is handled by setupCallbacks if enabled
	return nil
}

// Wait blocks until all crawling operations complete.
// The crawl naturally completes when all work is done (no more pending URLs, all workers idle).
// For incremental crawling, the crawl may complete when MaxURLsToVisit is reached.
func (cr *Crawler) Wait() {
	// Step 1: Wait for ALL pending work items to complete
	// This includes: queued URLs + URLs being processed by workers
	// When wg reaches zero, it means:
	// - No URLs in discovery channel
	// - No worker functions running
	// - No future URLs will be queued (all callbacks finished)
	cr.wg.Wait()

	// Step 2: Collect pending URLs BEFORE closing the channel
	// These are URLs that were queued but not processed (for incremental crawling)
	pendingURLs := cr.drainPendingURLs()

	// Step 3: Close discovery channel (safe now - no more URLs will be queued)
	close(cr.discoveryChannel)

	// Step 4: Wait for the processor to finish draining the discovery channel
	<-cr.processorDone

	// Step 5: Close the worker pool (should already be idle)
	cr.workerPool.Close()

	// Calculate totals
	totalDiscovered := cr.store.CountActions()
	visitedThisSession := int(atomic.LoadInt64(&cr.visitedCount))

	// Determine completion reason
	var reason CrawlCompletionReason
	select {
	case <-cr.ctx.Done():
		// Context was cancelled (manual stop)
		reason = CompletionReasonCancelled
	default:
		// Check if we hit the budget limit
		if cr.maxURLsToVisit > 0 && visitedThisSession >= int(cr.maxURLsToVisit) {
			reason = CompletionReasonBudgetReached
		} else {
			// All URLs were crawled naturally
			reason = CompletionReasonExhausted
		}
	}

	cr.mutex.RLock()
	totalPages := cr.crawledPages
	onComplete := cr.onCrawlComplete
	cr.mutex.RUnlock()

	// Build the result
	result := &CrawlResult{
		Reason:          reason,
		TotalPages:      totalPages,
		TotalDiscovered: totalDiscovered,
		URLsVisited:     visitedThisSession,
		PendingURLs:     pendingURLs,
	}

	// Call completion callback with full result
	if onComplete != nil {
		onComplete(result)
	}
}

// drainPendingURLs collects all URLs remaining in the discovery channel.
// Called before closing the channel to preserve pending URLs for incremental crawling.
func (cr *Crawler) drainPendingURLs() []URLDiscoveryRequest {
	var pending []URLDiscoveryRequest
	for {
		select {
		case req, ok := <-cr.discoveryChannel:
			if !ok {
				return pending
			}
			pending = append(pending, req)
			cr.wg.Done() // These were counted, need to decrement
		default:
			return pending
		}
	}
}

// URLHash computes the hash for a URL string.
// This is the same hash used internally for visit tracking.
// Exported for use by the application layer when persisting/restoring crawl state.
func URLHash(urlStr string) uint64 {
	return requestHash(urlStr, nil)
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
						// Queue this URL for processing (action already stored in queuedURLs by getOrDetermineURLAction)
						// IMPORTANT: Pass nil Context so each request gets its own independent context
						// to prevent race conditions where concurrent requests overwrite shared context data
						cr.queueURL(URLDiscoveryRequest{
							URL:       networkURL,
							Source:    "network",
							ParentURL: e.Request.URL.String(),
							Depth:     e.Request.Depth,
							Context:   nil, // Each request needs its own context
						})
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

				// Check nofollow status and respect configuration
				if strings.HasPrefix(link.Context, "nofollow:") {
					// Link has nofollow attribute - check if we should respect it
					// For internal links, check followInternalNofollow setting
					if !cr.followInternalNofollow {
						// We should respect nofollow for internal links - skip this link
						continue
					}
				}

				// Only crawl links with Crawl action (skip RecordOnly and Skip)
				if link.Action != URLActionCrawl {
					continue
				}

				// Check if crawlable (filters, robots.txt)
				if !cr.isURLCrawlable(link.URL) {
					continue
				}

				// Queue this URL for processing (action already stored in queuedURLs)
				// IMPORTANT: Pass nil Context so each request gets its own independent context
				// to prevent race conditions where concurrent requests overwrite shared context data
				cr.queueURL(URLDiscoveryRequest{
					URL:       link.URL,
					Source:    "spider",
					ParentURL: e.Request.URL.String(),
					Depth:     e.Request.Depth + 1,
					Context:   nil, // Each request needs its own context
				})
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

				// Queue this resource for processing (action already stored in queuedURLs)
				// IMPORTANT: Pass nil Context so each request gets its own independent context
				// to prevent race conditions where concurrent requests overwrite shared context data
				cr.queueURL(URLDiscoveryRequest{
					URL:       link.URL,
					Source:    "resource",
					ParentURL: e.Request.URL.String(),
					Depth:     e.Request.Depth,
					Context:   nil, // Each request needs its own context
				})
			}
		}
	})

	// Capture page metadata (title, meta description, headings, canonical, indexability)
	cr.Collector.OnHTML("html", func(e *HTMLElement) {
		// Store title in context for OnScraped to use
		title := e.ChildText("title")
		e.Request.Ctx.Put("title", title)

		// Extract meta description
		metaDesc := e.ChildAttr("meta[name='description']", "content")
		e.Request.Ctx.Put("metaDescription", metaDesc)

		// Extract first H1 heading (only the first one, not concatenated)
		var h1 string
		e.ForEach("h1", func(i int, elem *HTMLElement) {
			if i == 0 {
				h1 = strings.TrimSpace(elem.Text)
			}
		})
		e.Request.Ctx.Put("h1", h1)

		// Extract first H2 heading (only the first one, not concatenated)
		var h2 string
		e.ForEach("h2", func(i int, elem *HTMLElement) {
			if i == 0 {
				h2 = strings.TrimSpace(elem.Text)
			}
		})
		e.Request.Ctx.Put("h2", h2)

		// Extract canonical URL
		canonical := e.ChildAttr("link[rel='canonical']", "href")
		if canonical != "" {
			// Resolve to absolute URL
			canonical = e.Request.AbsoluteURL(canonical)
		}
		e.Request.Ctx.Put("canonicalURL", canonical)

		// Calculate word count from visible text (excluding scripts and styles)
		// Clone the DOM and remove non-visible elements before counting
		// This matches ScreamingFrog's approach of counting only visible text
		wordCount := 0
		if bodySelection := e.DOM.Find("body"); bodySelection.Length() > 0 {
			// Clone to avoid modifying the original DOM
			bodyClone := bodySelection.Clone()
			// Remove script and style elements (not visible text)
			bodyClone.Find("script, style, noscript").Remove()
			// Get text and count words
			bodyText := bodyClone.Text()
			wordCount = len(strings.Fields(bodyText))
		}
		e.Request.Ctx.Put("wordCount", fmt.Sprintf("%d", wordCount))

		// Check for meta robots noindex
		metaRobots := e.ChildAttr("meta[name='robots']", "content")
		if strings.Contains(strings.ToLower(metaRobots), "noindex") {
			e.Request.Ctx.Put("metaNoindex", "true")
		}
	})

	// OnScraped fires AFTER all OnHTML callbacks complete, ensuring all URLs are discovered
	cr.Collector.OnScraped(func(r *Response) {
		pageURL := r.Request.URL.String()
		debugThis := cr.shouldDebugURL(pageURL)

		if debugThis {
			log.Printf("[DEBUG-ONSCRAPED] START: url=%s", pageURL)
		}

		// Only process HTML pages here (non-HTML is handled in OnResponse)
		contentType := r.Ctx.Get("contentType")

		if debugThis {
			log.Printf("[DEBUG-ONSCRAPED] contentType from context: url=%s, contentType=%s", pageURL, contentType)
		}

		if !strings.Contains(contentType, "text/html") {
			if debugThis {
				log.Printf("[DEBUG-ONSCRAPED] SKIP: url=%s, not HTML (contentType=%s)", pageURL, contentType)
			}
			return
		}

		if debugThis {
			log.Printf("[DEBUG-ONSCRAPED] Processing HTML page: url=%s", pageURL)
		}

		// Get title from context (set by OnHTML)
		title := r.Ctx.Get("title")

		// Get meta description from context (set by OnHTML)
		metaDescription := r.Ctx.Get("metaDescription")

		// Get H1, H2, canonical URL, and word count from context (set by OnHTML)
		h1 := r.Ctx.Get("h1")
		h2 := r.Ctx.Get("h2")
		canonicalURL := r.Ctx.Get("canonicalURL")
		wordCount := 0
		if wc := r.Ctx.Get("wordCount"); wc != "" {
			fmt.Sscanf(wc, "%d", &wordCount)
		}

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

		// Store page metadata for future link population
		cr.store.StoreMetadata(pageURL, PageMetadata{
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
			H1:                 h1,
			H2:                 h2,
			CanonicalURL:       canonicalURL,
			WordCount:          wordCount,
			Indexable:          isIndexable,
			ContentType:        contentType,
			Error:              "",
			Links:              pageLinks,
			ContentHash:        contentHash,
			IsDuplicateContent: isDuplicate,
			Depth:              r.Request.Depth,
			response:           r,
		}

		cr.incrementCrawledPages()
		cr.callOnPageCrawled(result)
	})

	// Handle all responses (HTML and non-HTML)
	cr.Collector.OnResponse(func(r *Response) {
		pageURL := r.Request.URL.String()
		debugThis := cr.shouldDebugURL(pageURL)

		if debugThis {
			log.Printf("[DEBUG-ONRESPONSE] General handler: url=%s, status=%d, hasRedirectChain=%v",
				pageURL, r.StatusCode, len(r.RedirectChain) > 0)
		}

		contentType := r.Headers.Get("Content-Type")
		xRobotsTag := r.Headers.Get("X-Robots-Tag")
		isIndexable := "Yes"
		if strings.Contains(strings.ToLower(xRobotsTag), "noindex") {
			isIndexable = "No"
		}

		if debugThis {
			log.Printf("[DEBUG-ONRESPONSE] Setting context: url=%s, contentType=%s, status=%d, isIndexable=%s",
				pageURL, contentType, r.StatusCode, isIndexable)
		}

		// Store in context for OnHTML to use
		r.Request.Ctx.Put("isIndexable", isIndexable)
		r.Request.Ctx.Put("status", fmt.Sprintf("%d", r.StatusCode))
		r.Request.Ctx.Put("contentType", contentType)

		// For non-HTML content, route to OnResourceVisit callback instead of OnPageCrawled
		if !strings.Contains(contentType, "text/html") {
			// Note: We keep the body intact for backwards compatibility with any existing
			// OnResponse callbacks. Memory optimization can be added later if needed.

			// Store minimal metadata for link population (so links can show status/type)
			cr.store.StoreMetadata(pageURL, PageMetadata{
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
				Depth:       r.Request.Depth,
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

					// Only queue URLs marked for crawling
					if action == URLActionCrawl && shouldCrawl && cr.isURLCrawlable(absoluteURL) {
						// Queue this URL for processing (action already stored in queuedURLs)
						// IMPORTANT: Pass nil Context so each request gets its own independent context
						// to prevent race conditions where concurrent requests overwrite shared context data
						cr.queueURL(URLDiscoveryRequest{
							URL:       absoluteURL,
							Source:    "resource",
							ParentURL: r.Request.URL.String(),
							Depth:     r.Request.Depth,
							Context:   nil, // Each request needs its own context
						})
					}
				}
			}

			// Note: We don't return here to allow low-level code that
			// registers OnResponse callbacks on crawler.Collector to process all responses.
			// OnScraped will return early for non-HTML anyway,
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
				Depth:       r.Request.Depth,
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
				Depth:              r.Request.Depth,
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

	// Check domain allowlist/blocklist using Crawler's own fields
	if len(cr.disallowedDomains) > 0 {
		for _, d := range cr.disallowedDomains {
			if d == hostname {
				return false
			}
		}
	}

	if len(cr.allowedDomains) > 0 {
		allowed := false
		for _, d := range cr.allowedDomains {
			if d == hostname {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	// Check URL filters using Crawler's own fields
	urlBytes := []byte(urlStr)

	if len(cr.disallowedURLFilters) > 0 {
		for _, filter := range cr.disallowedURLFilters {
			if filter.Match(urlBytes) {
				return false
			}
		}
	}

	if len(cr.urlFilters) > 0 {
		matched := false
		for _, filter := range cr.urlFilters {
			if filter.Match(urlBytes) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Note: robots.txt checking is NOT done here because isURLCrawlable() is called
	// from synchronous callbacks (OnHTML). Making HTTP requests here would block callbacks.
	// robots.txt is checked later in processDiscoveredURL() in an async context.

	return true
}

// shouldIgnoreRobotsTxt returns true if robots.txt should be completely ignored
func (cr *Crawler) shouldIgnoreRobotsTxt() bool {
	return cr.robotsTxtMode == "ignore"
}

// checkRobots checks if a URL is allowed by robots.txt
// This method is called by Crawler BEFORE enqueueing URLs (policy layer)
// It caches robots.txt data per host to avoid repeated fetches
func (cr *Crawler) checkRobots(u *url.URL) error {
	cr.mutex.RLock()
	robot, ok := cr.robotsMap[u.Host]
	cr.mutex.RUnlock()

	if !ok {
		// no robots file cached - fetch it

		// Prepare request
		robotsURL := u.Scheme + "://" + u.Host + "/robots.txt"
		req, err := http.NewRequest("GET", robotsURL, nil)
		if err != nil {
			return err
		}
		hdr := http.Header{}
		if cr.Collector.Headers != nil {
			for k, v := range *cr.Collector.Headers {
				for _, value := range v {
					hdr.Add(k, value)
				}
			}
		}
		if _, ok := hdr["User-Agent"]; !ok {
			hdr.Set("User-Agent", cr.Collector.UserAgent)
		}
		req.Header = hdr
		// The Go HTTP API ignores "Host" in the headers, preferring the client
		// to use the Host field on Request.
		if hostHeader := hdr.Get("Host"); hostHeader != "" {
			req.Host = hostHeader
		}

		// Use a client that follows redirects for robots.txt but uses Collector's transport
		// The Collector's client is configured to NOT follow redirects (for tracking),
		// but robots.txt fetching should follow redirects to the canonical location.
		// We use the Collector's transport so that mock transports work in tests.
		robotsClient := &http.Client{
			Timeout:   30 * time.Second,
			Transport: cr.Collector.GetTransport(), // Use Collector's transport (supports mocks)
			// Default CheckRedirect follows up to 10 redirects
		}
		resp, err := robotsClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		robot, err = robotstxt.FromResponse(resp)
		if err != nil {
			return err
		}
		cr.mutex.Lock()
		cr.robotsMap[u.Host] = robot
		cr.mutex.Unlock()
	}

	uaGroup := robot.FindGroup(cr.Collector.UserAgent)
	if uaGroup == nil {
		return nil
	}

	eu := u.EscapedPath()
	if u.RawQuery != "" {
		eu += "?" + u.Query().Encode()
	}
	if !uaGroup.Test(eu) {
		// URL is blocked by robots.txt
		// In "ignore-report" mode, log the block but don't return error
		if cr.robotsTxtMode == "ignore-report" {
			log.Printf("[robots.txt] Would block %s (ignored due to ignore-report mode)", u.String())
			return nil
		}
		// In "respect" mode, return error to block the URL
		return ErrRobotsTxtBlocked
	}
	return nil
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
	if cr.shouldDebugURL(result.URL) {
		log.Printf("[DEBUG-CALLBACK] OnPageCrawled: url=%s, status=%d, title=%s, error=%s",
			result.URL, result.Status, result.Title, result.Error)
	}

	cr.mutex.RLock()
	callback := cr.onPageCrawled
	cr.mutex.RUnlock()

	if callback != nil {
		callback(result)
		if cr.shouldDebugURL(result.URL) {
			log.Printf("[DEBUG-CALLBACK] OnPageCrawled completed: url=%s", result.URL)
		}
	}
}

// callOnResourceVisit safely calls the OnResourceVisit callback if set
func (cr *Crawler) callOnResourceVisit(result *ResourceResult) {
	if cr.shouldDebugURL(result.URL) {
		log.Printf("[DEBUG-CALLBACK] OnResourceVisit: url=%s, status=%d, content_type=%s, error=%s",
			result.URL, result.Status, result.ContentType, result.Error)
	}

	cr.mutex.RLock()
	callback := cr.onResourceVisit
	cr.mutex.RUnlock()

	if callback != nil {
		callback(result)
		if cr.shouldDebugURL(result.URL) {
			log.Printf("[DEBUG-CALLBACK] OnResourceVisit completed: url=%s", result.URL)
		}
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
// Returns all discovered URLs from sitemaps including the sitemap URLs themselves.
func (cr *Crawler) fetchSitemapURLs(baseURL string) []string {
	// Determine which sitemap URLs to try
	sitemapLocations := cr.sitemapURLs
	if len(sitemapLocations) == 0 {
		// Use default locations
		baseURL = strings.TrimSuffix(baseURL, "/")
		sitemapLocations = []string{
			baseURL + "/sitemap.xml",
			baseURL + "/sitemap_index.xml",
		}
	}

	// Fetch URLs from each sitemap location
	var allURLs []string
	for _, sitemapURL := range sitemapLocations {
		urls, err := cr.Collector.FetchSitemapURLs(sitemapURL)
		if err != nil {
			// Log error but continue with other sitemaps
			continue
		}
		if len(urls) > 0 {
			// Include the sitemap URL itself as a discovered resource
			allURLs = append(allURLs, sitemapURL)
			allURLs = append(allURLs, urls...)
		}
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

// determinePathType classifies the href format as Absolute, Root-Relative, or Relative
func determinePathType(href string) string {
	if href == "" {
		return ""
	}
	// Absolute: starts with http://, https://, or protocol-relative //
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "//") {
		return "Absolute"
	}
	// Root-Relative: starts with /
	if strings.HasPrefix(href, "/") {
		return "Root-Relative"
	}
	// Relative: everything else (including ./path, ../path, path)
	return "Relative"
}

// containsNoFollow checks if rel attribute contains nofollow, sponsored, or ugc
func containsNoFollow(rel string) bool {
	relLower := strings.ToLower(rel)
	return strings.Contains(relLower, "nofollow") ||
		strings.Contains(relLower, "sponsored") ||
		strings.Contains(relLower, "ugc")
}

// extractAllLinks extracts all links from an HTML element.
// Returns a list of links with their types, text, and internal/external classification.
func (cr *Crawler) extractAllLinks(e *HTMLElement) []Link {
	var links []Link

	// Helper function to add a link to the list
	// follow: true if link should be followed (no nofollow/sponsored/ugc)
	// rel: full rel attribute value
	// target: target attribute value
	// pathType: "Absolute", "Root-Relative", or "Relative"
	addLink := func(rawHref, linkType, text, context string, elem *HTMLElement, follow bool, rel, target, pathType string) {
		absoluteURL := e.Request.AbsoluteURL(rawHref)
		if absoluteURL == "" {
			return
		}

		// Skip fragment-only links
		if strings.HasPrefix(rawHref, "#") {
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
		if meta, ok := cr.store.GetMetadata(absoluteURL); ok {
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
			Follow:      follow,
			Rel:         rel,
			Target:      target,
			PathType:    pathType,
		})
	}

	// Extract anchor links <a href="">
	e.ForEach("a[href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		text := strings.TrimSpace(elem.Text)
		context := extractLinkContext(elem)

		// Extract link attributes
		rel := elem.Attr("rel")
		target := elem.Attr("target")
		pathType := determinePathType(href)
		follow := !containsNoFollow(rel)

		// Keep nofollow prefix in context for backwards compatibility with crawler filtering
		if !follow {
			context = "nofollow:" + context
		}

		addLink(href, "anchor", text, context, elem, follow, rel, target, pathType)
	})

	// Extract image links <img src="">
	e.ForEach("img[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		alt := elem.Attr("alt")
		addLink(src, "image", alt, "", elem, true, "", "", determinePathType(src))
	})

	// Extract script links <script src="">
	e.ForEach("script[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "script", "", "", elem, true, "", "", determinePathType(src))
	})

	// Extract stylesheet links <link rel="stylesheet" href="">
	e.ForEach("link[rel='stylesheet'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "stylesheet", "", "", elem, true, "", "", determinePathType(href))
	})

	// Extract canonical links <link rel="canonical" href="">
	e.ForEach("link[rel='canonical'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "canonical", "", "", elem, true, "", "", determinePathType(href))
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

		addLink(href, linkType, "", "", elem, true, "", "", determinePathType(href))
	})

	// Extract modulepreload hints <link rel="modulepreload" href="">
	// Used for ES modules that should be preloaded
	e.ForEach("link[rel='modulepreload'][href]", func(_ int, elem *HTMLElement) {
		href := elem.Attr("href")
		addLink(href, "script", "", "", elem, true, "", "", determinePathType(href))
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

		addLink(href, linkType, "", "", elem, true, "", "", determinePathType(href))
	})

	// Extract iframe links <iframe src="">
	e.ForEach("iframe[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "iframe", "", "", elem, true, "", "", determinePathType(src))
	})

	// Extract video sources <video src=""> and <source src="">
	e.ForEach("video[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "", "", elem, true, "", "", determinePathType(src))
	})
	e.ForEach("video source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "video", "", "", elem, true, "", "", determinePathType(src))
	})

	// Extract audio sources <audio src=""> and <source src="">
	e.ForEach("audio[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "", "", elem, true, "", "", determinePathType(src))
	})
	e.ForEach("audio source[src]", func(_ int, elem *HTMLElement) {
		src := elem.Attr("src")
		addLink(src, "audio", "", "", elem, true, "", "", determinePathType(src))
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
	config := cr.resourceValidation

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
