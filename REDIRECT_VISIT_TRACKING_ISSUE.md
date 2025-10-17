# Redirect Visit Tracking Issue

## Issue Summary

After the crawler/collector refactoring, there's an inconsistency in how redirect destination URLs are tracked for visited status. The Collector's `checkRedirectFunc` still performs visit tracking (marking redirect destinations as visited), but this creates a race condition with the Crawler's centralized visit tracking.

## Current Behavior

### When URL A redirects to URL B:

1. **Crawler marks A as visited** (crawler.go:440)
   ```go
   uHash := requestHash(req.URL, nil)
   alreadyVisited, err := cr.Collector.store.VisitIfNotVisited(uHash)
   ```

2. **Crawler submits A to worker pool** with `checkRevisit=false` (crawler.go:43)
   ```go
   cr.Collector.scrape(req.URL, "GET", req.Depth, nil, req.Context, nil, false)
   ```

3. **Collector sets context** (collector.go:831)
   ```go
   req = req.WithContext(context.WithValue(c.ctx, CheckRevisitKey, checkRevisit))
   ```

4. **HTTP client follows redirect** A → B

5. **Collector's redirect handler runs** (collector.go:573-586)
   ```go
   uHash := requestHash(req.URL.String(), body)
   visited, err := c.store.IsVisited(uHash)
   if err != nil {
       return err
   }
   if visited {
       if checkRevisit, ok := req.Context().Value(CheckRevisitKey).(bool); !ok || checkRevisit {
           return &AlreadyVisitedError{req.URL}
       }
   }
   err = c.store.Visited(uHash)  // ← Marks B as visited!
   ```

6. **Response from B is returned** to Crawler

7. **Crawler processes response** (crawler.go:761)
   - `pageURL = r.Request.URL.String()` ← This is B (the final URL)
   - Stores metadata for B
   - **BUT: Never marks B in Crawler's tracking!**

### Result:
- Crawler's visit tracking: `{A: visited}`
- Collector's visit tracking: `{A: visited, B: visited}` ← Inconsistency!
- Crawler's page metadata: `{B: PageMetadata}`
- Crawler's queuedURLs: `{A: crawl}` ← B is not tracked here

## The Problem

### Race Condition Scenario:

**Thread 1:**
1. Discovers link to A
2. Marks A as visited
3. Fetches A → redirects to B
4. Collector marks B as visited
5. Processes response from B

**Thread 2 (happens concurrently):**
1. Discovers link to B directly
2. Checks if B is visited → YES (marked by Collector in Thread 1)
3. Skips B → **But B was never actually crawled by Crawler!**

This is exactly the race condition the refactoring was meant to eliminate!

## Why This Exists

The code at collector.go:918 and collector.go:573-586 still has the old visit tracking logic because:

1. The original comment says: "We already marked this URL as visited in step 3 above. Call scrape() directly with checkRevisit=false to bypass any visit checking."

2. The `checkRevisit=false` parameter and `CheckRevisitKey` context were meant to tell the Collector: "Don't check if this URL is visited - the Crawler already checked it."

3. **BUT**: The redirect handler still marks redirect destinations (B) as visited, which the Crawler doesn't know about.

## Questions to Resolve

### 1. Should the Collector's redirect handler perform ANY visit tracking?

The architectural principle after refactoring is:
- **Crawler** = Responsible for ALL visit tracking (single-threaded, no races)
- **Collector** = Low-level HTTP fetch, no business logic

Based on this principle, the redirect handler should NOT mark URLs as visited.

### 2. How should redirect destinations be handled?

**Option A: Treat redirects as discovered URLs**
- Remove visit tracking from `checkRedirectFunc` completely
- When a redirect occurs, the Collector calls the OnRedirect callback (set by Crawler)
- OnRedirect callback checks if destination is crawlable (domain filters, etc.)
- The final URL is NOT marked as visited by Collector
- After response, Crawler marks the FINAL URL as visited (needs new mechanism)

**Option B: Let Collector mark redirect destinations**
- Keep current behavior
- Accept that Collector marks redirect destinations
- Risk: Race conditions remain

**Option C: Pass redirect info back to Crawler**
- Remove visit tracking from redirect handler
- Track all redirects that occurred during the request
- After response, pass redirect chain to Crawler
- Crawler marks all URLs in redirect chain as visited

## Recommended Solution

**Remove visit tracking from `checkRedirectFunc` entirely:**

1. Delete lines 563-587 from collector.go (the visit checking/marking in redirect handler)

2. Keep only the OnRedirect callback and same-page redirect check:
   ```go
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

           if c.redirectHandler != nil {
               return c.redirectHandler(req, via)
           }

           // Honor golangs default of maximum of 10 redirects
           if len(via) >= 10 {
               return http.ErrUseLastResponse
           }

           lastRequest := via[len(via)-1]

           // If domain has changed, remove the Authorization-header if it exists
           if req.URL.Host != lastRequest.URL.Host {
               req.Header.Del("Authorization")
           }

           return nil
       }
   }
   ```

3. In Crawler, after processing response, mark the final URL as visited:
   - Check if `response.Request.URL != original queued URL`
   - If different (redirect occurred), mark final URL as visited
   - This ensures both A and B are in Crawler's tracking

4. Remove the `checkRevisit` parameter entirely from `scrape()` since it's no longer needed

5. Remove the `CheckRevisitKey` context value since it's no longer needed

## Testing Needed

After implementing the fix:

1. Test redirect chains: A → B → C (ensure all are marked visited)
2. Test same-page redirects (ensure they work)
3. Test concurrent discovery of redirect destination (ensure no duplicates)
4. Test external redirects (ensure domain filters still work)

## Code References

- Crawler visit tracking: crawler.go:440
- Crawler submits with checkRevisit=false: crawler.go:43
- Collector sets context: collector.go:831
- Collector redirect handler visit tracking: collector.go:573-586
- OnRedirect callback: crawler.go:305-312
- checkRevisitKey definition: collector.go:383
