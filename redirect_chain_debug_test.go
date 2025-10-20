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
	"sync"
	"testing"
)

// TestRedirectChainDebug tests redirect chain with debug output
func TestRedirectChainDebug(t *testing.T) {
	mock := NewMockTransport()

	// Register redirect chain: A→B
	mock.RegisterRedirect("https://example.com/page-a", "https://example.com/page-b", 301)

	// Register the final destination
	mock.RegisterHTML("https://example.com/page-b", `<html>
		<head><title>Page B</title></head>
		<body><h1>This is page B</h1></body>
	</html>`)

	var mu sync.Mutex
	crawledPages := make(map[string]*PageResult)
	responseCount := 0

	crawler := NewCrawler(context.Background(), &CrawlerConfig{
		AllowedDomains: []string{"example.com"},
	})
	crawler.Collector.WithTransport(mock)

	// Add a direct OnResponse callback to see what we're getting
	crawler.Collector.OnResponse(func(r *Response) {
		mu.Lock()
		defer mu.Unlock()
		responseCount++
		t.Logf("OnResponse #%d: URL=%s, Status=%d, RedirectChain len=%d",
			responseCount, r.Request.URL.String(), r.StatusCode, len(r.RedirectChain))
		for i, redir := range r.RedirectChain {
			t.Logf("  RedirectChain[%d]: URL=%s, Status=%d", i, redir.URL, redir.StatusCode)
		}
	})

	crawler.SetOnPageCrawled(func(result *PageResult) {
		mu.Lock()
		defer mu.Unlock()
		crawledPages[result.URL] = result
		t.Logf("OnPageCrawled: URL=%s, Status=%d, Title=%s", result.URL, result.Status, result.Title)
	})

	err := crawler.Start("https://example.com/page-a")
	if err != nil {
		t.Fatalf("Failed to start crawler: %v", err)
	}

	crawler.Wait()

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Total responses: %d", responseCount)
	t.Logf("Total pages crawled: %d", len(crawledPages))
	for url, result := range crawledPages {
		t.Logf("  - %s: Status=%d, Title=%s", url, result.Status, result.Title)
	}
}
