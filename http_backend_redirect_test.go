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
	"net/http/httptest"
	"testing"
)

// TestHttpBackendManualRedirect tests that the http_backend.Do function manually follows redirects
func TestHttpBackendManualRedirect(t *testing.T) {
	// Create a test HTTP server with redirects
	mux := http.NewServeMux()

	mux.HandleFunc("/redirect-1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirect-2", http.StatusMovedPermanently)
	})

	mux.HandleFunc("/redirect-2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})

	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>Final</body></html>"))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Create a Collector with CheckRedirect returning ErrUseLastResponse
	c := NewCollector(context.Background(), &HTTPConfig{
		UserAgent: "test",
	})

	// Test that redirects are followed and RedirectChain is populated
	var capturedResponse *Response
	c.OnResponse(func(r *Response) {
		capturedResponse = r
	})

	err := c.Visit(server.URL + "/redirect-1")
	if err != nil {
		t.Fatalf("Visit failed: %v", err)
	}

	if capturedResponse == nil {
		t.Fatal("No response captured")
	}

	// Verify the final response is from /final
	if capturedResponse.Request.URL.Path != "/final" {
		t.Errorf("Expected final URL to be /final, got %s", capturedResponse.Request.URL.Path)
	}

	// Verify RedirectChain contains both intermediate redirects
	if len(capturedResponse.RedirectChain) != 2 {
		t.Errorf("Expected RedirectChain to have 2 entries, got %d", len(capturedResponse.RedirectChain))
		for i, redir := range capturedResponse.RedirectChain {
			t.Logf("  RedirectChain[%d]: URL=%s, Status=%d", i, redir.URL, redir.StatusCode)
		}
	} else {
		// Verify first redirect
		if capturedResponse.RedirectChain[0].StatusCode != 301 {
			t.Errorf("Expected first redirect to have status 301, got %d", capturedResponse.RedirectChain[0].StatusCode)
		}
		if capturedResponse.RedirectChain[0].URL != server.URL+"/redirect-1" {
			t.Errorf("Expected first redirect URL to be /redirect-1, got %s", capturedResponse.RedirectChain[0].URL)
		}

		// Verify second redirect
		if capturedResponse.RedirectChain[1].StatusCode != 302 {
			t.Errorf("Expected second redirect to have status 302, got %d", capturedResponse.RedirectChain[1].StatusCode)
		}
		if capturedResponse.RedirectChain[1].URL != server.URL+"/redirect-2" {
			t.Errorf("Expected second redirect URL to be /redirect-2, got %s", capturedResponse.RedirectChain[1].URL)
		}
	}

	t.Log("✓ HTTP backend manual redirect handling working correctly")
}
