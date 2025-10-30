// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// This file includes modifications to code originally developed by Adam Tauber,
// licensed under the Apache License, Version 2.0.
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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/agentberlin/bluesnake/debug"
	"github.com/agentberlin/bluesnake/storage"
	"github.com/antchfx/htmlquery"
	"github.com/antchfx/xmlquery"
	"github.com/kennygrant/sanitize"
	whatwgUrl "github.com/nlnwa/whatwg-url/url"
	"google.golang.org/appengine/urlfetch"
)

// ContentHashConfig contains configuration for content-based duplicate detection
type ContentHashConfig struct {
	// ExcludeTags specifies HTML tags to exclude from content hashing
	// Default: ["script", "style", "nav", "footer"]
	ExcludeTags []string
	// IncludeOnlyTags specifies to only include specific tags in content hashing
	// If empty, all content (minus ExcludeTags) is included
	// Example: ["article", "main"] to focus only on main content
	IncludeOnlyTags []string
	// StripTimestamps removes timestamp patterns from content before hashing
	StripTimestamps bool
	// StripAnalytics removes analytics and tracking code from content
	StripAnalytics bool
	// StripComments removes HTML comments from content
	StripComments bool
	// CollapseWhitespace normalizes whitespace (multiple spaces/newlines to single)
	CollapseWhitespace bool
}

// ResourceValidationConfig controls checking of non-HTML resources for broken links
type ResourceValidationConfig struct {
	// Enabled turns on resource validation
	// Default: true
	Enabled bool
	// ResourceTypes specifies which resource types to check
	// Options: "image", "script", "stylesheet", "video", "audio", "iframe"
	// Default: ["image", "script", "stylesheet"]
	// Empty array = check all types
	ResourceTypes []string
	// CheckExternal controls whether to check external resources
	// Default: true
	CheckExternal bool
}

// RenderingConfig controls JavaScript rendering behavior with headless Chrome
type RenderingConfig struct {
	// InitialWaitMs is the initial wait time after page load (in milliseconds)
	// This allows time for JavaScript frameworks (React/Next.js) to hydrate
	// Default: 1500ms (matching ScreamingFrog's 5s AJAX timeout approach)
	InitialWaitMs int
	// ScrollWaitMs is the wait time after scrolling to bottom (in milliseconds)
	// This triggers lazy-loaded images and content
	// Default: 2000ms
	ScrollWaitMs int
	// FinalWaitMs is the final wait time before capturing HTML (in milliseconds)
	// This allows remaining network requests and DOM updates to complete
	// Default: 1000ms
	FinalWaitMs int
}

// HTTPConfig contains HTTP client configuration options for the Collector
type HTTPConfig struct {
	// UserAgent is the User-Agent string used by HTTP requests
	UserAgent string
	// Headers contains custom headers for HTTP requests
	Headers map[string]string
	// AllowURLRevisit allows multiple downloads of the same URL
	AllowURLRevisit bool
	// MaxBodySize is the limit of the retrieved response body in bytes.
	// 0 means unlimited.
	// The default value for MaxBodySize is 10MB (10 * 1024 * 1024 bytes).
	MaxBodySize int
	// CacheDir specifies a location where GET requests are cached as files.
	// When it's not defined, caching is disabled.
	CacheDir string
	// CacheExpiration sets the maximum age for cache files.
	CacheExpiration time.Duration
	// IgnoreRobotsTxt allows the Collector to ignore any restrictions set by
	// the target host's robots.txt file.
	IgnoreRobotsTxt bool
	// ParseHTTPErrorResponse allows parsing HTTP responses with non 2xx status codes.
	ParseHTTPErrorResponse bool
	// ID is the unique identifier of a collector (auto-assigned if 0)
	ID uint32
	// DetectCharset can enable character encoding detection for non-utf8 response bodies
	DetectCharset bool
	// CheckHead performs a HEAD request before every GET to pre-validate the response
	CheckHead bool
	// TraceHTTP enables capturing and reporting request performance.
	TraceHTTP bool
	// MaxRequests limit the number of requests done by the instance.
	// Set it to 0 for infinite requests (default).
	MaxRequests uint32
	// EnableRendering enables JavaScript rendering using headless Chrome.
	EnableRendering bool
	// RenderingConfig contains configuration for JavaScript rendering wait times
	// Only applies when EnableRendering is true
	RenderingConfig *RenderingConfig
	// EnableContentHash enables content-based duplicate detection
	// When true, pages with identical content will be detected even if URLs differ
	EnableContentHash bool
	// ContentHashAlgorithm specifies the hash algorithm to use
	// Options: "xxhash" (fastest, default), "md5", "sha256"
	ContentHashAlgorithm string
	// ContentHashConfig contains detailed configuration for content hashing
	ContentHashConfig *ContentHashConfig
	// Debugger is the debugger instance to use
	Debugger debug.Debugger
}

// CrawlerConfig contains all configuration options for a Crawler
type CrawlerConfig struct {
	// MaxDepth limits the recursion depth of visited URLs.
	// Set it to 0 for infinite recursion (default).
	MaxDepth int
	// AllowedDomains is a domain whitelist.
	// Leave it blank to allow any domains to be visited
	AllowedDomains []string
	// DisallowedDomains is a domain blacklist.
	DisallowedDomains []string
	// DisallowedURLFilters is a list of regular expressions which restricts
	// visiting URLs. If any of the rules matches to a URL the
	// request will be stopped. DisallowedURLFilters will
	// be evaluated before URLFilters
	DisallowedURLFilters []*regexp.Regexp
	// URLFilters is a list of regular expressions which restricts
	// visiting URLs. If any of the rules matches to a URL the
	// request won't be stopped. DisallowedURLFilters will
	// be evaluated before URLFilters
	URLFilters []*regexp.Regexp
	// DiscoveryMechanisms specifies which mechanisms to use for URL discovery.
	// Can be any combination: ["spider"], ["sitemap"], or ["spider", "sitemap"].
	// Default is ["spider"].
	DiscoveryMechanisms []DiscoveryMechanism
	// SitemapURLs specifies custom sitemap URLs to fetch (optional).
	// If nil/empty when sitemap discovery is enabled, tries default locations
	// (/sitemap.xml, /sitemap_index.xml).
	SitemapURLs []string
	// ResourceValidation configures checking of non-HTML resources for broken links
	ResourceValidation *ResourceValidationConfig
	// RobotsTxtMode controls how robots.txt is handled
	// Options: "respect", "ignore", or "ignore-report"
	// Default: "respect"
	RobotsTxtMode string
	// FollowInternalNofollow allows following links with rel="nofollow" on same domain
	// Default: false
	FollowInternalNofollow bool
	// FollowExternalNofollow allows following links with rel="nofollow" on external domains
	// Default: false
	FollowExternalNofollow bool
	// RespectMetaRobotsNoindex respects <meta name="robots" content="noindex">
	// Default: true
	RespectMetaRobotsNoindex bool
	// RespectNoindex respects X-Robots-Tag: noindex headers
	// Default: true
	RespectNoindex bool

	// DiscoveryChannelSize is the buffer size for the URL discovery channel
	// Larger values reduce blocking but use more memory
	// Default: 50000
	DiscoveryChannelSize int

	// WorkQueueSize is the buffer size for the worker pool work queue
	// Should be smaller than DiscoveryChannelSize
	// Default: 1000
	WorkQueueSize int

	// Parallelism is the number of concurrent HTTP requests (worker pool size)
	// This replaces/complements the existing async goroutine model
	// Default: 10
	Parallelism int

	// DebugURLs contains exact URLs to enable detailed logging for (scheme and trailing slash ignored)
	// Used for debugging race conditions by filtering logs to specific problematic URLs
	// Uses exact matching to avoid logging all subpaths
	// Example: []string{"handbook.agentberlin.ai/intro", "handbook.agentberlin.ai"}
	DebugURLs []string

	// HTTP contains HTTP client configuration for the underlying Collector
	HTTP *HTTPConfig
}

// Collector provides the scraper instance for a scraping job
type Collector struct {
	// UserAgent is the User-Agent string used by HTTP requests
	UserAgent string
	// Custom headers for the request
	Headers *http.Header
	// AllowURLRevisit allows multiple downloads of the same URL
	AllowURLRevisit bool
	// MaxBodySize is the limit of the retrieved response body in bytes.
	// 0 means unlimited.
	// The default value for MaxBodySize is 10MB (10 * 1024 * 1024 bytes).
	MaxBodySize int
	// CacheDir specifies a location where GET requests are cached as files.
	// When it's not defined, caching is disabled.
	CacheDir string
	// IgnoreRobotsTxt allows the Collector to ignore any restrictions set by
	// the target host's robots.txt file.  See http://www.robotstxt.org/ for more
	// information.
	IgnoreRobotsTxt bool
	// ParseHTTPErrorResponse allows parsing HTTP responses with non 2xx status codes.
	// By default, BlueSnake parses only successful HTTP responses. Set ParseHTTPErrorResponse
	// to true to enable it.
	ParseHTTPErrorResponse bool
	// ID is the unique identifier of a collector
	ID uint32
	// DetectCharset can enable character encoding detection for non-utf8 response bodies
	// without explicit charset declaration. This feature uses https://github.com/saintfish/chardet
	DetectCharset bool
	// redirectCallback is called when a redirect is encountered
	// Set via OnRedirect to handle or filter redirects
	redirectCallback RedirectCallback
	// CheckHead performs a HEAD request before every GET to pre-validate the response
	CheckHead bool
	// TraceHTTP enables capturing and reporting request performance for crawler tuning.
	// When set to true, the Response.Trace will be filled in with an HTTPTrace object.
	TraceHTTP bool
	// MaxRequests limit the number of requests done by the instance.
	// Set it to 0 for infinite requests (default).
	MaxRequests uint32
	// EnableRendering enables JavaScript rendering using headless Chrome.
	// When set to true, pages will be rendered with chromedp before parsing.
	EnableRendering bool
	// RenderingConfig contains configuration for JavaScript rendering wait times
	// Only applies when EnableRendering is true
	RenderingConfig *RenderingConfig
	// EnableContentHash enables content-based duplicate detection
	EnableContentHash bool
	// ContentHashAlgorithm specifies the hash algorithm to use ("xxhash", "md5", "sha256")
	ContentHashAlgorithm string
	// ContentHashConfig contains detailed configuration for content hashing
	ContentHashConfig *ContentHashConfig

	ctx                      context.Context // Lifecycle context for cancellation
	store                    storage.Storage
	debugger                 debug.Debugger
	htmlCallbacks            []*htmlCallbackContainer
	xmlCallbacks             []*xmlCallbackContainer
	requestCallbacks         []RequestCallback
	responseCallbacks        []ResponseCallback
	responseHeadersCallbacks []ResponseHeadersCallback
	requestHeadersCallbacks  []RequestCallback
	errorCallbacks           []ErrorCallback
	scrapedCallbacks         []ScrapedCallback
	requestCount             uint32
	responseCount            uint32
	backend                  *httpBackend
	lock                     *sync.RWMutex
	// CacheExpiration sets the maximum age for cache files.
	// If a cached file is older than this duration, it will be ignored and refreshed.
	CacheExpiration time.Duration
}

// RequestCallback is a type alias for OnRequest callback functions
type RequestCallback func(*Request)

// ResponseHeadersCallback is a type alias for OnResponseHeaders callback functions
type ResponseHeadersCallback func(*Response)

// ResponseCallback is a type alias for OnResponse callback functions
type ResponseCallback func(*Response)

// HTMLCallback is a type alias for OnHTML callback functions
type HTMLCallback func(*HTMLElement)

// XMLCallback is a type alias for OnXML callback functions
type XMLCallback func(*XMLElement)

// ErrorCallback is a type alias for OnError callback functions
type ErrorCallback func(*Response, error)

// ScrapedCallback is a type alias for OnScraped callback functions
type ScrapedCallback func(*Response)

// RedirectCallback is a type alias for OnRedirect callback functions.
// It receives the redirect request and the chain of previous requests.
// Return nil to allow the redirect, or an error to block it.
type RedirectCallback func(req *http.Request, via []*http.Request) error

// AlreadyVisitedError is the error type for already visited URLs.
//
// It's returned synchronously by Visit when the URL passed to Visit
// is already visited.
//
// When already visited URL is encountered after following
// redirects, this error appears in OnError callback, and if Async
// mode is not enabled, is also returned by Visit.
type AlreadyVisitedError struct {
	// Destination is the URL that was attempted to be visited.
	// It might not match the URL passed to Visit if redirect
	// was followed.
	Destination *url.URL
}

// Error implements error interface.
func (e *AlreadyVisitedError) Error() string {
	return fmt.Sprintf("%q already visited", e.Destination)
}

type htmlCallbackContainer struct {
	Selector string
	Function HTMLCallback
	active   atomic.Bool
}

type xmlCallbackContainer struct {
	Query    string
	Function XMLCallback
	active   atomic.Bool
}

type cookieJarSerializer struct {
	store storage.Storage
	lock  *sync.RWMutex
}

var collectorCounter uint32

var (
	// ErrForbiddenDomain is the error thrown if visiting
	// a domain which is not allowed in AllowedDomains
	ErrForbiddenDomain = errors.New("forbidden domain")
	// ErrMissingURL is the error type for missing URL errors
	ErrMissingURL = errors.New("missing URL")
	// ErrMaxDepth is the error type for exceeding max depth
	ErrMaxDepth = errors.New("max depth limit reached")
	// ErrForbiddenURL is the error thrown if visiting
	// a URL which is not allowed by URLFilters
	ErrForbiddenURL = errors.New("forbidden URL")

	// ErrNoURLFiltersMatch is the error thrown if visiting
	// a URL which is not allowed by URLFilters
	ErrNoURLFiltersMatch = errors.New("no URLFilters match")
	// ErrRobotsTxtBlocked is the error type for robots.txt errors
	ErrRobotsTxtBlocked = errors.New("URL blocked by robots.txt")
	// ErrNoCookieJar is the error type for missing cookie jar
	ErrNoCookieJar = errors.New("cookie jar is not available")
	// ErrNoPattern is the error type for LimitRules without patterns
	ErrNoPattern = errors.New("no pattern defined in LimitRule")
	// ErrAbortedAfterHeaders is the error returned when OnResponseHeaders aborts the transfer.
	ErrAbortedAfterHeaders = errors.New("aborted after receiving response headers")
	// ErrAbortedBeforeRequest is the error returned when OnResponseHeaders aborts the transfer.
	ErrAbortedBeforeRequest = errors.New("aborted before Do Request")
	// ErrQueueFull is the error returned when the queue is full
	ErrQueueFull = errors.New("queue MaxSize reached")
	// ErrMaxRequests is the error returned when exceeding max requests
	ErrMaxRequests = errors.New("max requests limit reached")
	// ErrRetryBodyUnseekable is the error when retry with not seekable body
	ErrRetryBodyUnseekable = errors.New("retry body unseekable")
)

var envMap = map[string]func(*Collector, string){
	"CACHE_DIR": func(c *Collector, val string) {
		c.CacheDir = val
	},
	"DETECT_CHARSET": func(c *Collector, val string) {
		c.DetectCharset = isYesString(val)
	},
	"DISABLE_COOKIES": func(c *Collector, _ string) {
		c.backend.Client.Jar = nil
	},
	"IGNORE_ROBOTSTXT": func(c *Collector, val string) {
		c.IgnoreRobotsTxt = isYesString(val)
	},
	"MAX_BODY_SIZE": func(c *Collector, val string) {
		size, err := strconv.Atoi(val)
		if err == nil {
			c.MaxBodySize = size
		}
	},
	"MAX_REQUESTS": func(c *Collector, val string) {
		maxRequests, err := strconv.ParseUint(val, 0, 32)
		if err == nil {
			c.MaxRequests = uint32(maxRequests)
		}
	},
	"PARSE_HTTP_ERROR_RESPONSE": func(c *Collector, val string) {
		c.ParseHTTPErrorResponse = isYesString(val)
	},
	"TRACE_HTTP": func(c *Collector, val string) {
		c.TraceHTTP = isYesString(val)
	},
	"USER_AGENT": func(c *Collector, val string) {
		c.UserAgent = val
	},
}

var urlParser = whatwgUrl.NewParser(whatwgUrl.WithPercentEncodeSinglePercentSign())

// DiscoveryMechanism specifies how URLs are discovered during crawling
type DiscoveryMechanism string

const (
	// DiscoverySpider discovers URLs by following links in HTML pages
	DiscoverySpider DiscoveryMechanism = "spider"
	// DiscoverySitemap discovers URLs from sitemap.xml files
	DiscoverySitemap DiscoveryMechanism = "sitemap"
)

// NewDefaultConfig returns a CrawlerConfig with sensible defaults
func NewDefaultConfig() *CrawlerConfig {
	return &CrawlerConfig{
		MaxDepth:                 0,
		AllowedDomains:           nil,
		DisallowedDomains:        nil,
		DisallowedURLFilters:     nil,
		URLFilters:               nil,
		DiscoveryMechanisms:      []DiscoveryMechanism{DiscoverySpider}, // Default to spider mode
		SitemapURLs:              nil,
		ResourceValidation: &ResourceValidationConfig{
			Enabled:       true,
			ResourceTypes: []string{"image", "script", "stylesheet", "font"},
			CheckExternal: true,
		},
		// Crawler directive defaults (following ScreamingFrog's defaults)
		RobotsTxtMode:            "respect", // Default to respecting robots.txt
		FollowInternalNofollow:   false,     // Default to NOT following internal nofollow links
		FollowExternalNofollow:   false,     // Default to NOT following external nofollow links
		RespectMetaRobotsNoindex: true,      // Default to respecting meta robots noindex
		RespectNoindex:           true,      // Default to respecting X-Robots-Tag noindex
		DiscoveryChannelSize:     50000,     // Default: 50k URLs
		WorkQueueSize:            1000,      // Default: 1k pending work items
		Parallelism:              10,        // Default: 10 concurrent fetches
		HTTP: &HTTPConfig{
			UserAgent:              "bluesnake/1.0 (+https://snake.blue)",
			MaxBodySize:            10 * 1024 * 1024, // 10MB
			IgnoreRobotsTxt:        false,           // Default to respecting robots.txt (controlled by RobotsTxtMode)
			MaxRequests:            0,
			AllowURLRevisit:        false,
			DetectCharset:          false,
			CheckHead:              false,
			TraceHTTP:              false,
			ParseHTTPErrorResponse: false,
			EnableRendering:        false,
			RenderingConfig: &RenderingConfig{
				InitialWaitMs: 1500, // 1.5s for React/Next.js hydration
				ScrollWaitMs:  2000, // 2s for lazy-loaded content
				FinalWaitMs:   1000, // 1s for remaining DOM updates
			},
			EnableContentHash:    false,
			ContentHashAlgorithm: "xxhash",
			ContentHashConfig: &ContentHashConfig{
				ExcludeTags:        []string{"script", "style", "nav", "footer"},
				IncludeOnlyTags:    nil,
				StripTimestamps:    true,
				StripAnalytics:     true,
				StripComments:      true,
				CollapseWhitespace: true,
			},
		},
	}
}

// NewCollector creates a new Collector instance with the provided context and HTTP configuration.
// The context is used for request cancellation and lifecycle management.
// If config is nil, default HTTPConfig from NewDefaultConfig() is used.
func NewCollector(ctx context.Context, config *HTTPConfig) *Collector {
	// Start with defaults
	defaultHTTP := NewDefaultConfig().HTTP

	// Merge user config with defaults (user config takes precedence for non-zero values)
	if config != nil {
		if config.UserAgent != "" {
			defaultHTTP.UserAgent = config.UserAgent
		}
		if config.Headers != nil {
			defaultHTTP.Headers = config.Headers
		}
		if config.AllowURLRevisit {
			defaultHTTP.AllowURLRevisit = true
		}
		// MaxBodySize: Always use the user's value, even if it's 0 (which means unlimited)
		defaultHTTP.MaxBodySize = config.MaxBodySize
		if config.CacheDir != "" {
			defaultHTTP.CacheDir = config.CacheDir
		}
		if config.CacheExpiration != 0 {
			defaultHTTP.CacheExpiration = config.CacheExpiration
		}
		if config.IgnoreRobotsTxt {
			defaultHTTP.IgnoreRobotsTxt = true
		}
		if config.ParseHTTPErrorResponse {
			defaultHTTP.ParseHTTPErrorResponse = true
		}
		if config.ID != 0 {
			defaultHTTP.ID = config.ID
		}
		if config.DetectCharset {
			defaultHTTP.DetectCharset = true
		}
		if config.CheckHead {
			defaultHTTP.CheckHead = true
		}
		if config.TraceHTTP {
			defaultHTTP.TraceHTTP = true
		}
		if config.MaxRequests != 0 {
			defaultHTTP.MaxRequests = config.MaxRequests
		}
		if config.EnableRendering {
			defaultHTTP.EnableRendering = true
		}
		if config.RenderingConfig != nil {
			defaultHTTP.RenderingConfig = config.RenderingConfig
		}
		if config.EnableContentHash {
			defaultHTTP.EnableContentHash = true
		}
		if config.ContentHashAlgorithm != "" {
			defaultHTTP.ContentHashAlgorithm = config.ContentHashAlgorithm
		}
		if config.ContentHashConfig != nil {
			defaultHTTP.ContentHashConfig = config.ContentHashConfig
		}
		if config.Debugger != nil {
			defaultHTTP.Debugger = config.Debugger
		}
	}
	config = defaultHTTP

	c := &Collector{}
	c.Init()

	// Apply HTTP configuration
	c.UserAgent = config.UserAgent
	if len(config.Headers) > 0 {
		customHeaders := make(http.Header)
		for header, value := range config.Headers {
			customHeaders.Add(header, value)
		}
		c.Headers = &customHeaders
	}
	c.AllowURLRevisit = config.AllowURLRevisit
	c.MaxBodySize = config.MaxBodySize
	c.CacheDir = config.CacheDir
	c.CacheExpiration = config.CacheExpiration
	c.IgnoreRobotsTxt = config.IgnoreRobotsTxt
	c.ParseHTTPErrorResponse = config.ParseHTTPErrorResponse
	if config.ID != 0 {
		c.ID = config.ID
	}
	c.DetectCharset = config.DetectCharset
	c.CheckHead = config.CheckHead
	c.TraceHTTP = config.TraceHTTP
	// Set context for lifecycle management
	c.ctx = ctx
	c.MaxRequests = config.MaxRequests
	c.EnableRendering = config.EnableRendering
	c.RenderingConfig = config.RenderingConfig
	if config.Debugger != nil {
		config.Debugger.Init()
		c.debugger = config.Debugger
	}
	c.EnableContentHash = config.EnableContentHash
	c.ContentHashAlgorithm = config.ContentHashAlgorithm
	c.ContentHashConfig = config.ContentHashConfig

	c.parseSettingsFromEnv()

	return c
}

// Init initializes the Collector's private variables and sets default
// configuration for the Collector
func (c *Collector) Init() {
	c.UserAgent = "bluesnake/1.0 (+https://snake.blue)"
	c.Headers = nil
	c.MaxRequests = 0
	c.store = &storage.InMemoryStorage{}
	c.store.Init()
	c.MaxBodySize = 10 * 1024 * 1024
	c.backend = &httpBackend{}
	jar, _ := cookiejar.New(nil)
	c.backend.Init(jar)
	c.backend.Client.CheckRedirect = c.checkRedirectFunc()
	c.lock = &sync.RWMutex{}
	c.IgnoreRobotsTxt = false // Default to respecting robots.txt
	c.ID = atomic.AddUint32(&collectorCounter, 1)
	c.TraceHTTP = false
	c.ctx = context.Background()
}

// Appengine will replace the Collector's backend http.Client
// With an Http.Client that is provided by appengine/urlfetch
// This function should be used when the scraper is run on
// Google App Engine. Example:
//
//	func startScraper(w http.ResponseWriter, r *http.Request) {
//	  ctx := appengine.NewContext(r)
//	  c := bluesnake.NewCollector()
//	  c.Appengine(ctx)
//	   ...
//	  c.Visit("https://google.ca")
//	}
func (c *Collector) Appengine(ctx context.Context) {
	client := urlfetch.Client(ctx)
	client.Jar = c.backend.Client.Jar
	client.CheckRedirect = c.backend.Client.CheckRedirect
	client.Timeout = c.backend.Client.Timeout

	c.backend.Client = client
}

// Visit starts Collector's collecting job by creating a
// request to the URL specified in parameter.
// Visit also calls the previously provided callbacks
func (c *Collector) Visit(URL string) error {
	if c.CheckHead {
		if check := c.FetchURL(URL, "HEAD", 1, nil, nil, nil); check != nil {
			return check
		}
	}
	return c.FetchURL(URL, "GET", 1, nil, nil, nil)
}

// Head starts a collector job by creating a HEAD request.
func (c *Collector) Head(URL string) error {
	return c.FetchURL(URL, "HEAD", 1, nil, nil, nil)
}

// Post starts a collector job by creating a POST request.
// Post also calls the previously provided callbacks
func (c *Collector) Post(URL string, requestData map[string]string) error {
	return c.FetchURL(URL, "POST", 1, createFormReader(requestData), nil, nil)
}

// PostRaw starts a collector job by creating a POST request with raw binary data.
// Post also calls the previously provided callbacks
func (c *Collector) PostRaw(URL string, requestData []byte) error {
	return c.FetchURL(URL, "POST", 1, bytes.NewReader(requestData), nil, nil)
}

// PostMultipart starts a collector job by creating a Multipart POST request
// with raw binary data.  PostMultipart also calls the previously provided callbacks
func (c *Collector) PostMultipart(URL string, requestData map[string][]byte) error {
	boundary := randomBoundary()
	hdr := http.Header{}
	hdr.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	hdr.Set("User-Agent", c.UserAgent)
	return c.FetchURL(URL, "POST", 1, createMultipartReader(boundary, requestData), nil, hdr)
}

// Request starts a collector job by creating a custom HTTP request
// where method, context, headers and request data can be specified.
// Set requestData, ctx, hdr parameters to nil if you don't want to use them.
// Valid methods:
//   - "GET"
//   - "HEAD"
//   - "POST"
//   - "PUT"
//   - "DELETE"
//   - "PATCH"
//   - "OPTIONS"
func (c *Collector) Request(method, URL string, requestData io.Reader, ctx *Context, hdr http.Header) error {
	return c.FetchURL(URL, method, 1, requestData, ctx, hdr)
}

// SetDebugger attaches a debugger to the collector
func (c *Collector) SetDebugger(d debug.Debugger) {
	d.Init()
	c.debugger = d
}

// UnmarshalRequest creates a Request from serialized data
func (c *Collector) UnmarshalRequest(r []byte) (*Request, error) {
	req := &serializableRequest{}
	err := json.Unmarshal(r, req)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}

	ctx := NewContext()
	for k, v := range req.Ctx {
		ctx.Put(k, v)
	}

	return &Request{
		Method:    req.Method,
		URL:       u,
		Depth:     req.Depth,
		Body:      bytes.NewReader(req.Body),
		Ctx:       ctx,
		ID:        atomic.AddUint32(&c.requestCount, 1),
		Headers:   &req.Headers,
		collector: c,
	}, nil
}

// FetchURL performs an HTTP request and processes the response synchronously.
// This method is intentionally exported for use by Crawler.
// It handles HTTP fetch, HTML parsing, and executes all registered callbacks.
//
// IMPORTANT - Context Parameter:
// In most cases, pass nil for ctx to create a fresh Context for each request.
// This ensures proper isolation and prevents race conditions where concurrent
// requests overwrite shared Context data (contentType, title, etc.).
//
// Only pass a non-nil Context when you explicitly need to preserve data from
// a previous request, such as:
//   - Request.Retry() - Preserving state across retry attempts
//   - Request.Visit() - Manual navigation with session continuity
//   - Custom request chaining where you need to pass authentication tokens or session data
//
// The Crawler automatically passes nil to ensure each discovered URL gets its own Context.
func (c *Collector) FetchURL(u, method string, depth int, requestData io.Reader, ctx *Context, hdr http.Header) error {
	parsedWhatwgURL, err := urlParser.Parse(u)
	if err != nil {
		return err
	}
	parsedURL, err := url.Parse(parsedWhatwgURL.Href(false))
	if err != nil {
		return err
	}
	if hdr == nil {
		hdr = http.Header{}
		if c.Headers != nil {
			for k, v := range *c.Headers {
				for _, value := range v {
					hdr.Add(k, value)
				}
			}
		}
	}
	if _, ok := hdr["User-Agent"]; !ok {
		hdr.Set("User-Agent", c.UserAgent)
	}
	if seeker, ok := requestData.(io.ReadSeeker); ok {
		_, err := seeker.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequest(method, parsedURL.String(), requestData)
	if err != nil {
		return err
	}
	req.Header = hdr
	// The Go HTTP API ignores "Host" in the headers, preferring the client
	// to use the Host field on Request.
	if hostHeader := hdr.Get("Host"); hostHeader != "" {
		req.Host = hostHeader
	}
	// note: once 1.13 is minimum supported Go version,
	// replace this with http.NewRequestWithContext
	req = req.WithContext(c.ctx)

	if err := c.requestCheck(parsedURL, method, req.GetBody, depth); err != nil {
		return err
	}
	u = parsedURL.String()

	// Check context before processing
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
	}

	// Always run synchronously - concurrency is managed by Crawler's worker pool
	return c.fetch(u, method, depth, requestData, ctx, hdr, req)
}

func (c *Collector) fetch(u, method string, depth int, requestData io.Reader, ctx *Context, hdr http.Header, req *http.Request) error {
	// Check cancellation before processing
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
	}

	// Create fresh Context if not provided
	// This is the normal case - each request gets its own isolated Context
	// to prevent race conditions from concurrent requests sharing state
	if ctx == nil {
		ctx = NewContext()
	}
	request := &Request{
		URL:       req.URL,
		Headers:   &req.Header,
		Host:      req.Host,
		Ctx:       ctx,
		Depth:     depth,
		Method:    method,
		Body:      requestData,
		collector: c,
		ID:        atomic.AddUint32(&c.requestCount, 1),
	}

	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}

	c.handleOnRequest(request)

	if request.abort {
		return nil
	}

	if method == "POST" && req.Header.Get("Content-Type") == "" {
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	}

	var hTrace *HTTPTrace
	if c.TraceHTTP {
		hTrace = &HTTPTrace{}
		req = hTrace.WithTrace(req)
	}
	origURL := req.URL
	checkResponseHeadersFunc := func(req *http.Request, statusCode int, headers http.Header) bool {
		if req.URL != origURL {
			request.URL = req.URL
			request.Headers = &req.Header
		}
		c.handleOnResponseHeaders(&Response{Ctx: ctx, Request: request, StatusCode: statusCode, Headers: &headers})
		return !request.abort
	}
	checkRequestHeadersFunc := func(req *http.Request) bool {
		c.handleOnRequestHeaders(request)
		return !request.abort
	}
	response, err := c.backend.Cache(req, c.MaxBodySize, checkRequestHeadersFunc, checkResponseHeadersFunc, c.CacheDir, c.CacheExpiration)
	if err := c.handleOnError(response, err, request, ctx); err != nil {
		return err
	}
	atomic.AddUint32(&c.responseCount, 1)
	response.Ctx = ctx
	response.Request = request
	response.Trace = hTrace

	err = response.fixCharset(c.DetectCharset, request.ResponseCharacterEncoding)
	if err != nil {
		return err
	}

	// Compute and store content hash if enabled
	if c.EnableContentHash && len(response.Body) > 0 {
		contentHash, err := ComputeContentHashWithConfig(
			response.Body,
			c.ContentHashAlgorithm,
			c.ContentHashConfig,
		)
		if err == nil {
			// Store the content hash for this URL
			c.store.SetContentHash(request.URL.String(), contentHash)

			// Store in response context for use in callbacks
			response.Ctx.Put("contentHash", contentHash)

			// Check if we've seen this content before
			isContentVisited, _ := c.store.IsContentVisited(contentHash)
			response.Ctx.Put("isContentDuplicate", fmt.Sprintf("%t", isContentVisited))

			// Mark this content hash as visited
			if !isContentVisited {
				c.store.VisitedContent(contentHash)
			}
		}
	}

	c.handleOnResponse(response)

	err = c.handleOnHTML(response)
	if err != nil {
		c.handleOnError(response, err, request, ctx)
	}

	err = c.handleOnXML(response)
	if err != nil {
		c.handleOnError(response, err, request, ctx)
	}

	c.handleOnScraped(response)

	return err
}

func (c *Collector) requestCheck(parsedURL *url.URL, method string, getBody func() (io.ReadCloser, error), depth int) error {
	// Check at start
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
	}

	if c.MaxRequests > 0 && c.requestCount >= c.MaxRequests {
		return ErrMaxRequests
	}

	// URL and domain filtering has been removed from Collector.
	// The Crawler is now responsible for all filtering (via isURLCrawlable).
	// This eliminates duplicate filtering and centralizes filtering logic in Crawler.

	// robots.txt checking has been moved to Crawler.
	// The Crawler checks robots.txt BEFORE enqueueing URLs (via isURLCrawlable).
	// This eliminates duplicate checks and maintains proper architectural separation:
	// Crawler = policy enforcement, Collector = HTTP mechanics.

	// Visit checking has been removed from Collector.
	// The Crawler is now responsible for all visit tracking (single-threaded processor).
	// This eliminates race conditions where multiple goroutines could mark the same URL
	// as visited but never actually crawl it.
	//
	// If checkRevisit is true and you need visit checking, you must handle it externally
	// before calling this method (see Crawler.processDiscoveredURL for the proper pattern).
	return nil
}

// String is the text representation of the collector.
// It contains useful debug information about the collector's internals
func (c *Collector) String() string {
	return fmt.Sprintf(
		"Requests made: %d (%d responses) | Callbacks: OnRequest: %d, OnHTML: %d, OnResponse: %d, OnError: %d",
		atomic.LoadUint32(&c.requestCount),
		atomic.LoadUint32(&c.responseCount),
		len(c.requestCallbacks),
		len(c.htmlCallbacks),
		len(c.responseCallbacks),
		len(c.errorCallbacks),
	)
}

// IsCancelled returns true if the collector's context is cancelled
func (c *Collector) IsCancelled() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

// OnRequest registers a function. Function will be executed on every
// request made by the Collector
func (c *Collector) OnRequest(f RequestCallback) {
	c.lock.Lock()
	if c.requestCallbacks == nil {
		c.requestCallbacks = make([]RequestCallback, 0, 4)
	}
	c.requestCallbacks = append(c.requestCallbacks, f)
	c.lock.Unlock()
}

// OnResponseHeaders registers a function. Function will be executed on every response
// when headers and status are already received, but body is not yet read.
//
// Like in OnRequest, you can call Request.Abort to abort the transfer. This might be
// useful if, for example, you're following all hyperlinks, but want to avoid
// downloading files.
//
// Be aware that using this will prevent HTTP/1.1 connection reuse, as
// the only way to abort a download is to immediately close the connection.
// HTTP/2 doesn't suffer from this problem, as it's possible to close
// specific stream inside the connection.
func (c *Collector) OnResponseHeaders(f ResponseHeadersCallback) {
	c.lock.Lock()
	c.responseHeadersCallbacks = append(c.responseHeadersCallbacks, f)
	c.lock.Unlock()
}

// OnRequestHeaders registers a function. Function will be executed on every
// request made by the Collector before Request Do
func (c *Collector) OnRequestHeaders(f RequestCallback) {
	c.lock.Lock()
	c.requestHeadersCallbacks = append(c.requestHeadersCallbacks, f)
	c.lock.Unlock()
}

// OnResponse registers a function. Function will be executed on every response
func (c *Collector) OnResponse(f ResponseCallback) {
	c.lock.Lock()
	if c.responseCallbacks == nil {
		c.responseCallbacks = make([]ResponseCallback, 0, 4)
	}
	c.responseCallbacks = append(c.responseCallbacks, f)
	c.lock.Unlock()
}

// OnHTML registers a function. Function will be executed on every HTML
// element matched by the GoQuery Selector parameter.
// GoQuery Selector is a selector used by https://github.com/PuerkitoBio/goquery
func (c *Collector) OnHTML(goquerySelector string, f HTMLCallback) {
	c.lock.Lock()
	if c.htmlCallbacks == nil {
		c.htmlCallbacks = make([]*htmlCallbackContainer, 0, 4)
	}
	cc := &htmlCallbackContainer{
		Selector: goquerySelector,
		Function: f,
	}
	cc.active.Store(true)
	c.htmlCallbacks = append(c.htmlCallbacks, cc)
	c.lock.Unlock()
}

// OnXML registers a function. Function will be executed on every XML
// element matched by the xpath Query parameter.
// xpath Query is used by https://github.com/antchfx/xmlquery
func (c *Collector) OnXML(xpathQuery string, f XMLCallback) {
	c.lock.Lock()
	if c.xmlCallbacks == nil {
		c.xmlCallbacks = make([]*xmlCallbackContainer, 0, 4)
	}
	cc := &xmlCallbackContainer{
		Query:    xpathQuery,
		Function: f,
	}
	cc.active.Store(true)
	c.xmlCallbacks = append(c.xmlCallbacks, cc)
	c.lock.Unlock()
}

// OnHTMLDetach deregister a function. Function will not be execute after detached
func (c *Collector) OnHTMLDetach(goquerySelector string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, cc := range c.htmlCallbacks {
		if cc.Selector == goquerySelector {
			cc.active.Store(false)
		}
	}
}

// OnXMLDetach deregister a function. Function will not be execute after detached
func (c *Collector) OnXMLDetach(xpathQuery string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, cc := range c.xmlCallbacks {
		if cc.Query == xpathQuery {
			cc.active.Store(false)
		}
	}
}

// OnError registers a function. Function will be executed if an error
// occurs during the HTTP request.
func (c *Collector) OnError(f ErrorCallback) {
	c.lock.Lock()
	if c.errorCallbacks == nil {
		c.errorCallbacks = make([]ErrorCallback, 0, 4)
	}
	c.errorCallbacks = append(c.errorCallbacks, f)
	c.lock.Unlock()
}

// OnScraped registers a function that will be executed as the final part of
// the scraping, after OnHTML and OnXML have finished.
func (c *Collector) OnScraped(f ScrapedCallback) {
	c.lock.Lock()
	if c.scrapedCallbacks == nil {
		c.scrapedCallbacks = make([]ScrapedCallback, 0, 4)
	}
	c.scrapedCallbacks = append(c.scrapedCallbacks, f)
	c.lock.Unlock()
}

// OnRedirect registers a function that will be called when a redirect is encountered.
// The callback receives the redirect request and the chain of previous requests.
// Return nil to allow the redirect, or an error to block it.
// This allows external components (like Crawler) to inject redirect handling logic into the Collector.
func (c *Collector) OnRedirect(f RedirectCallback) {
	c.lock.Lock()
	c.redirectCallback = f
	c.lock.Unlock()
}

// SetClient will override the previously set http.Client
func (c *Collector) SetClient(client *http.Client) {
	c.backend.Client = client
}

// WithTransport allows you to set a custom http.RoundTripper (transport)
func (c *Collector) WithTransport(transport http.RoundTripper) {
	c.backend.Client.Transport = transport
}

// DisableCookies turns off cookie handling
func (c *Collector) DisableCookies() {
	c.backend.Client.Jar = nil
}

// SetCookieJar overrides the previously set cookie jar
func (c *Collector) SetCookieJar(j http.CookieJar) {
	c.backend.Client.Jar = j
}

// SetRequestTimeout overrides the default timeout (10 seconds) for this collector
func (c *Collector) SetRequestTimeout(timeout time.Duration) {
	c.backend.Client.Timeout = timeout
}

// SetStorage overrides the default in-memory storage.
// Storage stores scraping related data like cookies and visited urls
func (c *Collector) SetStorage(s storage.Storage) error {
	if err := s.Init(); err != nil {
		return err
	}
	c.store = s
	c.backend.Client.Jar = createJar(s)
	return nil
}


func createEvent(eventType string, requestID, collectorID uint32, kvargs map[string]string) *debug.Event {
	return &debug.Event{
		CollectorID: collectorID,
		RequestID:   requestID,
		Type:        eventType,
		Values:      kvargs,
	}
}

func (c *Collector) handleOnRequest(r *Request) {
	if c.debugger != nil {
		c.debugger.Event(createEvent("request", r.ID, c.ID, map[string]string{
			"url": r.URL.String(),
		}))
	}
	for _, f := range c.requestCallbacks {
		f(r)
	}
}

func (c *Collector) handleOnResponse(r *Response) {
	if c.debugger != nil {
		c.debugger.Event(createEvent("response", r.Request.ID, c.ID, map[string]string{
			"url":    r.Request.URL.String(),
			"status": http.StatusText(r.StatusCode),
		}))
	}
	for _, f := range c.responseCallbacks {
		f(r)
	}
}

func (c *Collector) handleOnResponseHeaders(r *Response) {
	if c.debugger != nil {
		c.debugger.Event(createEvent("responseHeaders", r.Request.ID, c.ID, map[string]string{
			"url":    r.Request.URL.String(),
			"status": http.StatusText(r.StatusCode),
		}))
	}
	for _, f := range c.responseHeadersCallbacks {
		f(r)
	}
}
func (c *Collector) handleOnRequestHeaders(r *Request) {
	if c.debugger != nil {
		c.debugger.Event(createEvent("requestHeaders", r.ID, c.ID, map[string]string{
			"url": r.URL.String(),
		}))
	}
	for _, f := range c.requestHeadersCallbacks {
		f(r)
	}
}

func (c *Collector) handleOnHTML(resp *Response) error {
	c.lock.RLock()
	htmlCallbacks := slices.Clone(c.htmlCallbacks)
	c.lock.RUnlock()

	if len(htmlCallbacks) == 0 {
		return nil
	}

	contentType := resp.Headers.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(resp.Body)
	}
	// implementation of mime.ParseMediaType without parsing the params
	// part
	mediatype, _, _ := strings.Cut(contentType, ";")
	mediatype = strings.TrimSpace(strings.ToLower(mediatype))

	// TODO we also want to parse application/xml as XHTML if it has
	// appropriate doctype
	switch mediatype {
	case "text/html", "application/xhtml+xml":
	default:
		return nil
	}

	// If rendering is enabled, use chromedp to get the rendered HTML
	var bodyReader *bytes.Buffer
	if c.EnableRendering {
		renderer, err := getRenderer()
		if err != nil {
			// Critical error: Chrome not available
			return fmt.Errorf("FATAL: Chrome browser not available for JavaScript rendering: %w. Please install Google Chrome or set CHROME_EXECUTABLE_PATH environment variable", err)
		}
		renderedHTML, discoveredURLs, err := renderer.RenderPage(resp.Request.URL.String(), c.RenderingConfig)
		if err != nil {
			// If rendering fails, fall back to original HTML
			log.Printf("Chromedp rendering failed for %s: %v. Falling back to non-rendered HTML", resp.Request.URL.String(), err)
			bodyReader = bytes.NewBuffer(resp.Body)
		} else {
			bodyReader = bytes.NewBufferString(renderedHTML)
			// Store discovered URLs in context for crawler to use
			if len(discoveredURLs) > 0 {
				// Convert to JSON for storage in context
				urlsJSON, _ := json.Marshal(discoveredURLs)
				resp.Ctx.Put("networkDiscoveredURLs", string(urlsJSON))
			}
		}
	} else {
		bodyReader = bytes.NewBuffer(resp.Body)
	}

	doc, err := goquery.NewDocumentFromReader(bodyReader)
	if err != nil {
		return err
	}
	if href, found := doc.Find("base[href]").Attr("href"); found {
		u, err := urlParser.ParseRef(resp.Request.URL.String(), href)
		if err == nil {
			baseURL, err := url.Parse(u.Href(false))
			if err == nil {
				resp.Request.baseURL = baseURL
			}
		}

	}
	for _, cc := range htmlCallbacks {
		if !cc.active.Load() {
			continue
		}
		i := 0
		doc.Find(cc.Selector).Each(func(_ int, s *goquery.Selection) {
			for _, n := range s.Nodes {
				e := NewHTMLElementFromSelectionNode(resp, s, n, i)
				i++
				if c.debugger != nil {
					c.debugger.Event(createEvent("html", resp.Request.ID, c.ID, map[string]string{
						"selector": cc.Selector,
						"url":      resp.Request.URL.String(),
					}))
				}
				cc.Function(e)
			}
		})
	}
	return nil
}

func (c *Collector) handleOnXML(resp *Response) error {
	c.lock.RLock()
	xmlCallbacks := slices.Clone(c.xmlCallbacks)
	c.lock.RUnlock()

	if len(xmlCallbacks) == 0 {
		return nil
	}
	contentType := strings.ToLower(resp.Headers.Get("Content-Type"))
	isXMLFile := strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".xml") || strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".xml.gz")
	if !strings.Contains(contentType, "html") && (!strings.Contains(contentType, "xml") && !isXMLFile) {
		return nil
	}

	if strings.Contains(contentType, "html") {
		doc, err := htmlquery.Parse(bytes.NewBuffer(resp.Body))
		if err != nil {
			return err
		}
		if e := htmlquery.FindOne(doc, "//base"); e != nil {
			for _, a := range e.Attr {
				if a.Key == "href" {
					baseURL, err := resp.Request.URL.Parse(a.Val)
					if err == nil {
						resp.Request.baseURL = baseURL
					}
					break
				}
			}
		}

		for _, cc := range xmlCallbacks {
			if !cc.active.Load() {
				continue
			}
			for i, n := range htmlquery.Find(doc, cc.Query) {
				e := NewXMLElementFromHTMLNode(resp, n)
				e.Index = i
				if c.debugger != nil {
					c.debugger.Event(createEvent("xml", resp.Request.ID, c.ID, map[string]string{
						"selector": cc.Query,
						"url":      resp.Request.URL.String(),
					}))
				}
				cc.Function(e)
			}
		}
	} else if strings.Contains(contentType, "xml") || isXMLFile {
		doc, err := xmlquery.Parse(bytes.NewBuffer(resp.Body))
		if err != nil {
			return err
		}

		for _, cc := range xmlCallbacks {
			if !cc.active.Load() {
				continue
			}
			xmlquery.FindEach(doc, cc.Query, func(i int, n *xmlquery.Node) {
				e := NewXMLElementFromXMLNode(resp, n)
				if c.debugger != nil {
					c.debugger.Event(createEvent("xml", resp.Request.ID, c.ID, map[string]string{
						"selector": cc.Query,
						"url":      resp.Request.URL.String(),
					}))
				}
				cc.Function(e)
			})
		}
	}
	return nil
}

func (c *Collector) handleOnError(response *Response, err error, request *Request, ctx *Context) error {
	if err == nil && (c.ParseHTTPErrorResponse || response.StatusCode < 203) {
		return nil
	}
	if err == nil && response.StatusCode >= 203 {
		err = errors.New(http.StatusText(response.StatusCode))
	}
	if response == nil {
		response = &Response{
			Request: request,
			Ctx:     ctx,
		}
	}
	if c.debugger != nil {
		c.debugger.Event(createEvent("error", request.ID, c.ID, map[string]string{
			"url":    request.URL.String(),
			"status": http.StatusText(response.StatusCode),
		}))
	}
	if response.Request == nil {
		response.Request = request
	}
	if response.Ctx == nil {
		response.Ctx = request.Ctx
	}
	for _, f := range c.errorCallbacks {
		f(response, err)
	}
	return err
}

func (c *Collector) cleanupCallbacks() {
	c.lock.Lock()
	defer c.lock.Unlock()

	// Clean HTML callbacks
	c.htmlCallbacks = slices.DeleteFunc(c.htmlCallbacks, func(cc *htmlCallbackContainer) bool {
		return !cc.active.Load()
	})

	// Clean XML callbacks
	c.xmlCallbacks = slices.DeleteFunc(c.xmlCallbacks, func(cc *xmlCallbackContainer) bool {
		return !cc.active.Load()
	})
}

func (c *Collector) handleOnScraped(r *Response) {
	if c.debugger != nil {
		c.debugger.Event(createEvent("scraped", r.Request.ID, c.ID, map[string]string{
			"url": r.Request.URL.String(),
		}))
	}
	for _, f := range c.scrapedCallbacks {
		f(r)
	}

	// Cleanup inactive callbacks after processing each response
	c.cleanupCallbacks()
}

// Limit adds a new LimitRule to the collector
func (c *Collector) Limit(rule *LimitRule) error {
	return c.backend.Limit(rule)
}

// Limits adds new LimitRules to the collector
func (c *Collector) Limits(rules []*LimitRule) error {
	return c.backend.Limits(rules)
}

// SetCookies handles the receipt of the cookies in a reply for the given URL
func (c *Collector) SetCookies(URL string, cookies []*http.Cookie) error {
	if c.backend.Client.Jar == nil {
		return ErrNoCookieJar
	}
	u, err := url.Parse(URL)
	if err != nil {
		return err
	}
	c.backend.Client.Jar.SetCookies(u, cookies)
	return nil
}

// Cookies returns the cookies to send in a request for the given URL.
func (c *Collector) Cookies(URL string) []*http.Cookie {
	if c.backend.Client.Jar == nil {
		return nil
	}
	u, err := url.Parse(URL)
	if err != nil {
		return nil
	}
	return c.backend.Client.Jar.Cookies(u)
}

// Clone creates an exact copy of a Collector without callbacks.
// HTTP backend and cookie jar are shared between collectors.
func (c *Collector) Clone() *Collector {
	return &Collector{
		AllowURLRevisit:        c.AllowURLRevisit,
		CacheDir:               c.CacheDir,
		CacheExpiration:        c.CacheExpiration,
		DetectCharset:          c.DetectCharset,
		ID:                     atomic.AddUint32(&collectorCounter, 1),
		IgnoreRobotsTxt:        c.IgnoreRobotsTxt,
		MaxBodySize:            c.MaxBodySize,
		MaxRequests:            c.MaxRequests,
		CheckHead:              c.CheckHead,
		ParseHTTPErrorResponse: c.ParseHTTPErrorResponse,
		UserAgent:              c.UserAgent,
		Headers:                c.Headers,
		TraceHTTP:              c.TraceHTTP,
		ctx:                    c.ctx,
		store:                  c.store,
		backend:                c.backend,
		debugger:               c.debugger,
		errorCallbacks:         make([]ErrorCallback, 0, 8),
		htmlCallbacks:          make([]*htmlCallbackContainer, 0, 8),
		xmlCallbacks:           make([]*xmlCallbackContainer, 0, 8),
		scrapedCallbacks:       make([]ScrapedCallback, 0, 8),
		lock:                   c.lock,
		requestCallbacks:       make([]RequestCallback, 0, 8),
		responseCallbacks:      make([]ResponseCallback, 0, 8),
	}
}

func (c *Collector) checkRedirectFunc() func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		// Call redirect callback if set (allows Crawler to inject redirect handling)
		c.lock.RLock()
		callback := c.redirectCallback
		c.lock.RUnlock()

		if callback != nil {
			if err := callback(req, via); err != nil {
				return err
			}
		}

		// IMPORTANT: Always return http.ErrUseLastResponse to disable automatic redirect following.
		// This allows us to manually handle redirects in http_backend.go:Do() and capture
		// intermediate responses with their actual status codes (301, 302, 307, 308).
		// The Crawler's OnRedirect callback (above) handles URL filtering and visit tracking.
		return http.ErrUseLastResponse
	}
}

func (c *Collector) parseSettingsFromEnv() {
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "BLUESNAKE_") {
			continue
		}
		pair := strings.SplitN(e[10:], "=", 2)
		if f, ok := envMap[pair[0]]; ok {
			f(c, pair[1])
		} else {
			log.Println("Unknown environment variable:", pair[0])
		}
	}
}

// SanitizeFileName replaces dangerous characters in a string
// so the return value can be used as a safe file name.
func SanitizeFileName(fileName string) string {
	ext := filepath.Ext(fileName)
	cleanExt := sanitize.BaseName(ext)
	if cleanExt == "" {
		cleanExt = ".unknown"
	}
	return strings.Replace(fmt.Sprintf(
		"%s.%s",
		sanitize.BaseName(fileName[:len(fileName)-len(ext)]),
		cleanExt[1:],
	), "-", "_", -1)
}

func createFormReader(data map[string]string) io.Reader {
	form := url.Values{}
	for k, v := range data {
		form.Add(k, v)
	}
	return strings.NewReader(form.Encode())
}

func createMultipartReader(boundary string, data map[string][]byte) io.Reader {
	dashBoundary := "--" + boundary

	body := []byte{}
	buffer := bytes.NewBuffer(body)

	buffer.WriteString("Content-type: multipart/form-data; boundary=" + boundary + "\n\n")
	for contentType, content := range data {
		buffer.WriteString(dashBoundary + "\n")
		buffer.WriteString("Content-Disposition: form-data; name=" + contentType + "\n")
		buffer.WriteString(fmt.Sprintf("Content-Length: %d \n\n", len(content)))
		buffer.Write(content)
		buffer.WriteString("\n")
	}
	buffer.WriteString(dashBoundary + "--\n\n")
	return bytes.NewReader(buffer.Bytes())

}

// randomBoundary was borrowed from
// github.com/golang/go/mime/multipart/writer.go#randomBoundary
func randomBoundary() string {
	var buf [30]byte
	_, err := io.ReadFull(rand.Reader, buf[:])
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", buf[:])
}

func isYesString(s string) bool {
	switch strings.ToLower(s) {
	case "1", "yes", "true", "y":
		return true
	}
	return false
}

func createJar(s storage.Storage) http.CookieJar {
	return &cookieJarSerializer{store: s, lock: &sync.RWMutex{}}
}

func (j *cookieJarSerializer) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.lock.Lock()
	defer j.lock.Unlock()
	cookieStr := j.store.Cookies(u)

	// Merge existing cookies, new cookies have precedence.
	cnew := make([]*http.Cookie, len(cookies))
	copy(cnew, cookies)
	existing := storage.UnstringifyCookies(cookieStr)
	for _, c := range existing {
		if !storage.ContainsCookie(cnew, c.Name) {
			cnew = append(cnew, c)
		}
	}
	j.store.SetCookies(u, storage.StringifyCookies(cnew))
}

func (j *cookieJarSerializer) Cookies(u *url.URL) []*http.Cookie {
	cookies := storage.UnstringifyCookies(j.store.Cookies(u))
	// Filter.
	now := time.Now()
	cnew := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		// Drop expired cookies.
		if c.RawExpires != "" && c.Expires.Before(now) {
			continue
		}
		// Drop secure cookies if not over https.
		if c.Secure && u.Scheme != "https" {
			continue
		}
		cnew = append(cnew, c)
	}
	return cnew
}

// FetchSitemapURLs fetches URLs from a sitemap using the Collector's HTTP client.
// This ensures sitemap fetching uses the same transport/configuration as regular crawling.
// Handles both regular sitemaps and sitemap indexes automatically.
// Returns all discovered URLs from the sitemap (empty slice if sitemap cannot be fetched).
// This is the proper way for Crawler to access sitemap data without touching backend.Client directly.
func (c *Collector) FetchSitemapURLs(sitemapURL string) ([]string, error) {
	// Use ForceGet with a custom fetch function that uses the Collector's HTTP client
	// This ensures we use the same transport as regular crawling (for mocks, custom transports, etc.)
	SetFetch(func(url string, options interface{}) ([]byte, error) {
		resp, err := c.backend.Client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	})

	// Use ForceGet to be resilient to partial errors in sitemap indexes
	sitemap, err := ForceGet(sitemapURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sitemap from %s: %w", sitemapURL, err)
	}

	// Extract all URLs from the sitemap
	urls := make([]string, 0, len(sitemap.URL))
	for _, u := range sitemap.URL {
		if u.Loc != "" {
			urls = append(urls, u.Loc)
		}
	}

	return urls, nil
}

// TryDefaultSitemaps tries to fetch sitemaps from common default locations using the Collector's HTTP client.
// It tries /sitemap.xml first, then /sitemap_index.xml.
// Returns all discovered URLs from available sitemaps (empty slice if none found).
// This method does not return errors - it returns an empty slice if no sitemaps are found.
func (c *Collector) TryDefaultSitemaps(baseURL string) []string {
	// Ensure baseURL doesn't have trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Common sitemap locations to try
	sitemapLocations := []string{
		baseURL + "/sitemap.xml",
		baseURL + "/sitemap_index.xml",
	}

	var allURLs []string
	for _, location := range sitemapLocations {
		urls, err := c.FetchSitemapURLs(location)
		if err == nil && len(urls) > 0 {
			allURLs = append(allURLs, urls...)
		}
		// Continue trying other locations even if one fails
	}

	return allURLs
}

func isMatchingFilter(fs []*regexp.Regexp, d []byte) bool {
	for _, r := range fs {
		if r.Match(d) {
			return true
		}
	}
	return false
}

func normalizeURL(u string) string {
	parsed, err := urlParser.Parse(u)
	if err != nil {
		return u
	}
	return parsed.String()
}

func requestHash(url string, body io.Reader) uint64 {
	h := fnv.New64a()
	// reparse the url to fix ambiguities such as
	// "http://example.com" vs "http://example.com/"
	io.WriteString(h, normalizeURL(url))
	if body != nil {
		io.Copy(h, body)
	}
	return h.Sum64()
}
