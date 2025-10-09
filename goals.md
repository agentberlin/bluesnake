Save & Load Crawls: Store crawl data for historical comparison and view
JavaScript Rendering: Crawl JS-rendered content to see what search engines see
Google Analytics Integration: Fetch user and performance data from GA API
Search Console Integration: Connect to GSC API for search data
PageSpeed Insights Integration: Performance metrics integration
Structured Data Validation: Advanced Schema.org markup validation

Internal Hyperlinks: Configure crawling and storage of internal links
External Links: Store and crawl external domain links
Canonicals: Store and crawl canonical link elements
Pagination (rel next/prev): Crawl pagination attributes
Hreflang: Extract and crawl hreflang attributes
Crawl all subdomain
Crawl external links as well
Follow Internal/External "Nofollow": Override nofollow attributes
Crawl Linked XML Sitemaps: Discover and crawl XML sitemaps

Spider Extraction Configuration
    Page Titles: Extract and analyze page titles
    Meta Descriptions: Extract meta description tags
    H1 Tags: Extract H1 heading tags
    H2 Tags: Extract H2 heading tags
    Indexability Status: Determine page indexability
    Word Count: Calculate page word count
    Hash Value: Generate page hash for duplicate detection

URL & Response Data
    Response Time: Track page load times
    Last-Modified: Extract last-modified headers
    HTTP Headers: Store full request/response headers
    Cookies: Store and analyze cookies

Content Storage
    Store HTML: Save original HTML source code
    Store Rendered HTML: Save post-JavaScript rendered HTML

Rendering Modes
    Text Only: Crawl raw HTML only
    JavaScript: Execute client-side JavaScript with headless Chrome

JavaScript Settings
    Rendered Page Screenshots: Capture screenshots of rendered pages
    Screenshot Window Sizes: Customize viewport dimensions
    JavaScript Error Reporting: Capture and report JS errors

Performance Settings
    Response Timeout: Set HTTP response timeout (default 20 seconds)
    5XX Response Retries: Automatically retry server errors

Storage & Performance
    Memory Allocation: Adjust RAM allocation for crawls
    Storage Mode: Choose between Memory and Database storage
    Max Threads: Control crawling speed with thread count
    Max URLs per Second: Throttle crawl speed

Network & Authentication
    Proxy Configuration: Configure proxy settings
    Custom User-Agent: Set custom user-agent strings
    Robots.txt Handling: Configure robots.txt compliance

Export
    Ability to export all the crawled URLs, inlinks and outlinks

## thinking aloud about other features
Ability for a user to trigger a new crawl by just putting the url and the ability to move to any project quickly, from home page itself. Ability to select the particular crawl. Save all the important information in mysql. May be it make sense to integrate with the storage package in the library as well.

We also need the ability to run a local LLM in the future using ollama or something and then we should be able to make decisions about some data using calling this ollama api.

Also, we could use this LLM and expose it to the users so they can create custom charts or custom python code and report generation etc. We essentially need to be able to run python code and they can then just use an interface I'd build to just describe the need and internally we build the python program and execute.