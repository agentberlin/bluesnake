
# Colly Exposed Functional APIs

This document provides a comprehensive overview of the exposed functional APIs in the Colly Go package.

## Collector

The `Collector` is the main entity in Colly and manages the scraping job.

### NewCollector

`NewCollector(options ...CollectorOption) *Collector`

Creates a new `Collector` instance with default configuration. It can be configured with `CollectorOption` functions.

### Collector Methods

The `Collector` struct has several methods to control the scraping process:

- **`Init()`**: Initializes the `Collector`'s private variables and sets default configuration.
- **`Visit(URL string) error`**: Starts the collecting job by creating a GET request to the specified URL.
- **`Head(URL string) error`**: Starts a collector job by creating a HEAD request.
- **`Post(URL string, requestData map[string]string) error`**: Starts a collector job by creating a POST request with form data.
- **`PostRaw(URL string, requestData []byte) error`**: Starts a collector job by creating a POST request with raw binary data.
- **`PostMultipart(URL string, requestData map[string][]byte) error`**: Starts a collector job by creating a Multipart POST request.
- **`Request(method, URL string, requestData io.Reader, ctx *Context, hdr http.Header) error`**: Starts a collector job by creating a custom HTTP request.
- **`HasVisited(URL string) (bool, error)`**: Checks if the provided URL has been visited.
- **`HasPosted(URL string, requestData map[string]string) (bool, error)`**: Checks if the provided URL and requestData has been visited.
- **`Wait()`**: Blocks until all collector jobs are finished.
- **`OnRequest(f RequestCallback)`**: Registers a callback function to be executed on every request.
- **`OnResponse(f ResponseCallback)`**: Registers a callback function to be executed on every response.
- **`OnResponseHeaders(f ResponseHeadersCallback)`**: Registers a callback function to be executed on every response when headers and status are already received.
- **`OnHTML(goquerySelector string, f HTMLCallback)`**: Registers a callback function to be executed on every HTML element matched by the GoQuery Selector.
- **`OnXML(xpathQuery string, f XMLCallback)`**: Registers a callback function to be executed on every XML element matched by the xpath Query.
- **`OnHTMLDetach(goquerySelector string)`**: Deregisters a HTML callback.
- **`OnXMLDetach(xpathQuery string)`**: Deregisters a XML callback.
- **`OnError(f ErrorCallback)`**: Registers a callback function to be executed if an error occurs during the HTTP request.
- **`OnScraped(f ScrapedCallback)`**: Registers a function that will be executed as the final part of the scraping.
- **`SetClient(client *http.Client)`**: Overrides the previously set http.Client.
- **`WithTransport(transport http.RoundTripper)`**: Sets a custom http.RoundTripper (transport).
- **`DisableCookies()`**: Turns off cookie handling.
- **`SetCookieJar(j http.CookieJar)`**: Overrides the previously set cookie jar.
- **`SetRequestTimeout(timeout time.Duration)`**: Overrides the default timeout for this collector.
- **`SetStorage(s storage.Storage) error`**: Overrides the default in-memory storage.
- **`SetProxy(proxyURL string) error`**: Sets a proxy for the collector.
- **`SetProxyFunc(p ProxyFunc)`**: Sets a custom proxy setter/switcher function.
- **`Limit(rule *LimitRule) error`**: Adds a new `LimitRule` to the collector.
- **`Limits(rules []*LimitRule) error`**: Adds new `LimitRule`s to the collector.
- **`SetRedirectHandler(f func(req *http.Request, via []*http.Request) error)`**: Sets a custom redirect handler.
- **`SetCookies(URL string, cookies []*http.Cookie) error`**: Handles the receipt of the cookies in a reply for the given URL.
- **`Cookies(URL string) []*http.Cookie`**: Returns the cookies to send in a request for the given URL.
- **`Clone() *Collector`**: Creates an exact copy of a `Collector` without callbacks.
- **`Appengine(ctx context.Context)`**: Replaces the `Collector`'s backend http.Client with an App Engine urlfetch client.
- **`String() string`**: Returns the text representation of the collector.
- **`SetDebugger(d debug.Debugger)`**: Attaches a debugger to the collector.
- **`UnmarshalRequest(r []byte) (*Request, error)`**: Creates a `Request` from serialized data.

## Collector Options

These functions can be passed to `NewCollector` to configure it.

- **`UserAgent(ua string) CollectorOption`**: Sets the user agent.
- **`Headers(headers map[string]string) CollectorOption`**: Sets the custom headers.
- **`MaxDepth(depth int) CollectorOption`**: Limits the recursion depth of visited URLs.
- **`MaxRequests(max uint32) CollectorOption`**: Limits the number of requests.
- **`AllowedDomains(domains ...string) CollectorOption`**: Sets the domain whitelist.
- **`DisallowedDomains(domains ...string) CollectorOption`**: Sets the domain blacklist.
- **`DisallowedURLFilters(filters ...*regexp.Regexp) CollectorOption`**: Sets a list of regular expressions to restrict visiting URLs.
- **`URLFilters(filters ...*regexp.Regexp) CollectorOption`**: Sets a list of regular expressions to restrict visiting URLs.
- **`AllowURLRevisit() CollectorOption`**: Allows multiple downloads of the same URL.
- **`MaxBodySize(sizeInBytes int) CollectorOption`**: Sets the limit of the retrieved response body in bytes.
- **`CacheDir(path string) CollectorOption`**: Specifies a location where GET requests are cached.
- **`IgnoreRobotsTxt() CollectorOption`**: Instructs the Collector to ignore `robots.txt`.
- **`Async(a ...bool) CollectorOption`**: Turns on asynchronous network communication.
- **`ParseHTTPErrorResponse() CollectorOption`**: Allows parsing HTTP responses with non 2xx status codes.
- **`DetectCharset() CollectorOption`**: Enables character encoding detection for non-utf8 response bodies.
- **`Debugger(d debug.Debugger) CollectorOption`**: Sets the debugger.
- **`CheckHead() CollectorOption`**: Performs a HEAD request before every GET to pre-validate the response.
- **`CacheExpiration(d time.Duration) CollectorOption`**: Sets the maximum age for cache files.
- **`TraceHTTP() CollectorOption`**: Enables capturing and reporting request performance.
- **`StdlibContext(ctx context.Context) CollectorOption`**: Sets the context that will be used for HTTP requests.
- **`ID(id uint32) CollectorOption`**: Sets the unique identifier of the Collector.

## Request

The `Request` object represents an HTTP request made by a `Collector`.

### Request Methods

- **`New(method, URL string, body io.Reader) (*Request, error)`**: Creates a new request with the context of the original request.
- **`Abort()`**: Cancels the HTTP request when called in an `OnRequest` callback.
- **`IsAbort() bool`**: Returns true if the request has been aborted.
- **`AbsoluteURL(u string) string`**: Returns the resolved absolute URL of an URL chunk.
- **`Visit(URL string) error`**: Continues the collecting job by creating a new request and preserving the context.
- **`HasVisited(URL string) (bool, error)`**: Checks if the provided URL has been visited.
- **`Post(URL string, requestData map[string]string) error`**: Continues a collector job by creating a POST request.
- **`PostRaw(URL string, requestData []byte) error`**: Continues a collector job by creating a POST request with raw binary data.
- **`PostMultipart(URL string, requestData map[string][]byte) error`**: Continues a collector job by creating a Multipart POST request.
- **`Retry() error`**: Submits the HTTP request again with the same parameters.
- **`Do() error`**: Submits the request.
- **`Marshal() ([]byte, error)`**: Serializes the `Request`.

## Response

The `Response` object represents an HTTP response.

### Response Methods

- **`Save(fileName string) error`**: Writes response body to disk.
- **`FileName() string`**: Returns the sanitized file name parsed from "Content-Disposition" header or from URL.

## HTMLElement

The `HTMLElement` object represents an HTML tag.

### HTMLElement Methods

- **`Attr(k string) string`**: Returns the selected attribute of a `HTMLElement`.
- **`ChildText(goquerySelector string) string`**: Returns the concatenated and stripped text content of the matching elements.
- **`ChildTexts(goquerySelector string) []string`**: Returns the stripped text content of all the matching elements.
- **`ChildAttr(goquerySelector, attrName string) string`**: Returns the stripped text content of the first matching element's attribute.
- **`ChildAttrs(goquerySelector, attrName string) []string`**: Returns the stripped text content of all the matching element's attributes.
- **`ForEach(goquerySelector string, callback func(int, *HTMLElement))`**: Iterates over the elements matched by the selector and calls the callback function on every match.
- **`ForEachWithBreak(goquerySelector string, callback func(int, *HTMLElement) bool)`**: Similar to `ForEach`, but allows breaking the loop.
- **`Unmarshal(v interface{}) error`**: Declaratively extracts data to a struct from an HTML response.
- **`UnmarshalWithMap(v interface{}, structMap map[string]string) error`**: Similar to `Unmarshal`, but allows a map to be passed in.

## XMLElement

The `XMLElement` object represents an XML tag.

### XMLElement Methods

- **`Attr(k string) string`**: Returns the selected attribute of a `XMLElement`.
- **`ChildText(xpathQuery string) string`**: Returns the concatenated and stripped text content of the matching elements.
- **`ChildTexts(xpathQuery string) []string`**: Returns an array of strings corresponding to child elements that match the xpath query.
- **`ChildAttr(xpathQuery, attrName string) string`**: Returns the stripped text content of the first matching element's attribute.
- **`ChildAttrs(xpathQuery, attrName string) []string`**: Returns the stripped text content of all the matching element's attributes.

## Context

The `Context` object provides a way to pass data between callbacks.

### Context Methods

- **`NewContext() *Context`**: Initializes a new `Context` instance.
- **`Put(key string, value interface{})`**: Stores a value in the `Context`.
- **`Get(key string) string`**: Retrieves a string value from the `Context`.
- **`GetAny(key string) interface{}`**: Retrieves a value of any type from the `Context`.
- **`ForEach(fn func(k string, v interface{}) interface{}) []interface{}`**: Iterates over the context.
- **`Clone() *Context`**: Clones the context.

## Callbacks

- **`RequestCallback`**: `func(*Request)`
- **`ResponseCallback`**: `func(*Response)`
- **`ResponseHeadersCallback`**: `func(*Response)`
- **`HTMLCallback`**: `func(*HTMLElement)`
- **`XMLCallback`**: `func(*XMLElement)`
- **`ErrorCallback`**: `func(*Response, error)`
- **`ScrapedCallback`**: `func(*Response)`

## Error Handling

- **`ErrForbiddenDomain`**: Error for visiting a domain not in `AllowedDomains`.
- **`ErrMissingURL`**: Error for a missing URL.
- **`ErrMaxDepth`**: Error for exceeding the max depth.
- **`ErrForbiddenURL`**: Error for visiting a URL disallowed by `URLFilters`.
- **`ErrNoURLFiltersMatch`**: Error for a URL not matching any `URLFilters`.
- **`ErrRobotsTxtBlocked`**: Error for a URL blocked by `robots.txt`.
- **`ErrNoCookieJar`**: Error for a missing cookie jar.
- **`ErrNoPattern`**: Error for a `LimitRule` without a pattern.
- **`ErrEmptyProxyURL`**: Error for an empty Proxy URL list.
- **`ErrAbortedAfterHeaders`**: Error when `OnResponseHeaders` aborts the transfer.
- **`ErrAbortedBeforeRequest`**: Error when `OnRequest` aborts the transfer.
- **`ErrQueueFull`**: Error when the queue is full.
- **`ErrMaxRequests`**: Error for exceeding max requests.
- **`ErrRetryBodyUnseekable`**: Error when retrying with a non-seekable body.

## Rate Limiting

- **`LimitRule`**: Provides connection restrictions for domains.
  - **`Init() error`**: Initializes the `LimitRule`.
  - **`Match(domain string) bool`**: Checks if a domain matches the rule.

## Utilities

- **`SanitizeFileName(fileName string) string`**: Replaces dangerous characters in a string to make it a safe file name.
- **`UnmarshalHTML(v interface{}, s *goquery.Selection, structMap map[string]string) error`**: Declaratively extracts data to a struct from an HTML response.
- **`HTTPTrace`**: Provides a datastructure for storing an http trace.
  - **`WithTrace(req *http.Request) *http.Request`**: Returns the given HTTP Request with this HTTPTrace added to its context.
