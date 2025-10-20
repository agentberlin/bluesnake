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
	"net/http"
	"sync"
	"testing"
)

// TestContextIsolationBetweenRequests verifies that when FetchURL is called
// with nil Context for different URLs, each request gets its own isolated Context.
//
// This test verifies the fix for the race condition discovered on 2025-10-20:
// At the Collector level, passing nil Context should create fresh Context for
// each request. The actual spider/resource discovery isolation is tested at
// the Crawler level (see integration_tests/).
//
// See analysis/race_condition/FIX_SUMMARY.md for full details.
func TestContextIsolationBetweenRequests(t *testing.T) {
	mock := NewMockTransport()
	mock.RegisterHTML("https://example.com/page1", "<html><body>Page 1</body></html>")
	mock.RegisterResponse("https://example.com/image.png", &MockResponse{
		StatusCode: 200,
		Body:       "fake image data",
		Headers:    http.Header{"Content-Type": []string{"image/png"}},
	})

	c := NewCollector(context.Background(), nil)
	c.WithTransport(mock)

	// Track Context objects for each URL
	contextsMutex := sync.Mutex{}
	capturedContexts := make(map[string]*Context)

	c.OnResponse(func(r *Response) {
		url := r.Request.URL.String()
		contentType := r.Headers.Get("Content-Type")

		contextsMutex.Lock()
		capturedContexts[url] = r.Request.Ctx
		contextsMutex.Unlock()

		// Store data in Context
		r.Request.Ctx.Put("contentType", contentType)
		r.Request.Ctx.Put("url", url)
	})

	// Make first request with nil Context
	err := c.FetchURL("https://example.com/page1", "GET", 1, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to fetch page1: %v", err)
	}

	// Make second request with nil Context
	err = c.FetchURL("https://example.com/image.png", "GET", 1, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to fetch image: %v", err)
	}

	contextsMutex.Lock()
	defer contextsMutex.Unlock()

	page1Ctx := capturedContexts["https://example.com/page1"]
	imageCtx := capturedContexts["https://example.com/image.png"]

	// Verify both contexts were created
	if page1Ctx == nil || imageCtx == nil {
		t.Fatal("One or both contexts were not created")
	}

	// CRITICAL: Verify contexts are different objects (isolation)
	if page1Ctx == imageCtx {
		t.Error("CONTEXT ISOLATION FAILURE: Two requests with nil Context got the SAME Context object!")
	}

	// Verify each context has its own data
	if page1Ctx.Get("url") != "https://example.com/page1" {
		t.Errorf("Page1 context has wrong URL: %v", page1Ctx.Get("url"))
	}
	if imageCtx.Get("url") != "https://example.com/image.png" {
		t.Errorf("Image context has wrong URL: %v", imageCtx.Get("url"))
	}

	// Verify contexts don't share data
	if imageCtx.Get("url") == "https://example.com/page1" {
		t.Error("CONTEXT ISOLATION FAILURE: Image context has page1's data!")
	}
}

// TestContextIsolationWithRedirects verifies that when following redirects,
// each redirect gets a fresh Context when FetchURL is called with nil.
//
// This is particularly important because the original bug was discovered
// in redirect scenarios where handbook.agentberlin.ai/ -> handbook.agentberlin.ai/intro
// and concurrent resource discovery would corrupt the Context.
func TestContextIsolationWithRedirects(t *testing.T) {
	mock := NewMockTransport()

	// Set up a simple redirect chain
	mock.RegisterRedirect("https://example.com/start", "https://example.com/final", 307)
	mock.RegisterHTML("https://example.com/final", "<html><body>Final</body></html>")

	c := NewCollector(context.Background(), nil)
	c.WithTransport(mock)

	// Track contexts for each URL in the redirect chain
	contextsMutex := sync.Mutex{}
	capturedContexts := make(map[string]*Context)

	c.OnResponse(func(r *Response) {
		url := r.Request.URL.String()

		contextsMutex.Lock()
		capturedContexts[url] = r.Request.Ctx
		contextsMutex.Unlock()

		r.Request.Ctx.Put("url", url)
	})

	// Visit the start URL (which redirects)
	err := c.Visit("https://example.com/start")
	if err != nil {
		t.Fatalf("Failed to visit: %v", err)
	}

	contextsMutex.Lock()
	defer contextsMutex.Unlock()

	// After redirect, we should have captured the final URL's context
	finalCtx := capturedContexts["https://example.com/final"]
	if finalCtx == nil {
		t.Fatal("Final URL context was not captured")
	}

	// Verify the context has the correct data
	if finalCtx.Get("url") != "https://example.com/final" {
		t.Errorf("Final context has wrong URL: %v", finalCtx.Get("url"))
	}

	// Note: The redirect source URL is not visited through FetchURL directly,
	// so we don't capture its context in this test. The Crawler layer handles
	// redirect chain processing and ensures each discovered URL gets nil Context.
}

// TestContextPreservationInRetry verifies that Request.Retry() correctly
// preserves Context across retry attempts, which is intentional behavior.
//
// This test ensures that while automatic discovery gets fresh Context,
// manual operations like Retry still maintain Context continuity.
func TestContextPreservationInRetry(t *testing.T) {
	mock := NewMockTransport()
	mock.RegisterHTML("https://example.com/test", "<html><body>Test</body></html>")

	c := NewCollector(context.Background(), nil)
	c.WithTransport(mock)

	retryCount := 0
	c.OnResponse(func(r *Response) {
		// First attempt: mark Context and retry
		if r.Ctx.Get("retryMarker") == "" {
			r.Ctx.Put("retryMarker", "attempted")
			retryCount++
			_ = r.Request.Retry()
			return
		}

		// Second attempt: verify Context was preserved
		if marker := r.Ctx.Get("retryMarker"); marker != "attempted" {
			t.Errorf("Context NOT preserved in Retry: expected 'attempted', got '%s'", marker)
		}
		retryCount++
	})

	err := c.Visit("https://example.com/test")
	if err != nil {
		t.Fatalf("Failed to visit: %v", err)
	}

	if retryCount != 2 {
		t.Errorf("Expected 2 attempts (original + retry), got %d", retryCount)
	}
}

// TestContextNilByDefault verifies that when nil Context is passed to FetchURL,
// a new Context is created, ensuring isolation.
func TestContextNilByDefault(t *testing.T) {
	mock := NewMockTransport()
	mock.RegisterHTML("https://example.com/test", "<html><body>Test</body></html>")

	c := NewCollector(context.Background(), nil)
	c.WithTransport(mock)

	contexts := []*Context{}
	var mu sync.Mutex

	c.OnResponse(func(r *Response) {
		mu.Lock()
		defer mu.Unlock()

		// First request: mark Context
		if len(contexts) == 0 {
			r.Request.Ctx.Put("testKey", "testValue")
		}

		// Capture all contexts
		contexts = append(contexts, r.Request.Ctx)
	})

	// Call FetchURL with nil Context (this is what Crawler does)
	err := c.FetchURL("https://example.com/test", "GET", 1, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to fetch: %v", err)
	}

	mu.Lock()
	if len(contexts) != 1 {
		mu.Unlock()
		t.Fatalf("Expected 1 context after first request, got %d", len(contexts))
	}
	firstContext := contexts[0]
	mu.Unlock()

	// Verify a Context was created
	if firstContext == nil {
		t.Fatal("Context was not created when nil was passed")
	}

	// Verify we can use the Context
	if val := firstContext.Get("testKey"); val != "testValue" {
		t.Errorf("Context not working: expected 'testValue', got '%v'", val)
	}

	// Make another request with nil Context and verify it's a different Context
	err = c.FetchURL("https://example.com/test", "GET", 1, nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to fetch second time: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(contexts) != 2 {
		t.Fatalf("Expected 2 contexts after second request, got %d", len(contexts))
	}
	secondContext := contexts[1]

	// Verify contexts are different (isolation)
	if firstContext == secondContext {
		t.Error("CONTEXT ISOLATION FAILURE: Two requests with nil Context got the SAME Context object!")
	}

	// Verify second Context doesn't have first Context's data
	if val := secondContext.Get("testKey"); val != "" {
		t.Errorf("CONTEXT ISOLATION FAILURE: Second Context has first Context's data: '%v'", val)
	}
}
