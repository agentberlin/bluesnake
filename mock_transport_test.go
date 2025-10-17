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
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMockTransport_RegisterHTML(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/"
	html := `<html><head><title>Test Page</title></head><body>Content</body></html>`

	mock.RegisterHTML(url, html)

	// Create a request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Perform the round trip
	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected Content-Type to contain 'text/html', got '%s'", contentType)
	}

	// Verify body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != html {
		t.Errorf("Expected body '%s', got '%s'", html, string(body))
	}
}

func TestMockTransport_RegisterJSON(t *testing.T) {
	mock := NewMockTransport()
	url := "https://api.example.com/data"
	json := `{"key": "value", "number": 42}`

	mock.RegisterJSON(url, json)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected Content-Type to contain 'application/json', got '%s'", contentType)
	}

	// Verify body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != json {
		t.Errorf("Expected body '%s', got '%s'", json, string(body))
	}
}

func TestMockTransport_RegisterResponse(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/redirect"

	headers := make(http.Header)
	headers.Set("Location", "https://example.com/new")

	mock.RegisterResponse(url, &MockResponse{
		StatusCode: 302,
		Body:       "Moved",
		Headers:    headers,
	})

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != 302 {
		t.Errorf("Expected status 302, got %d", resp.StatusCode)
	}

	// Verify location header
	location := resp.Header.Get("Location")
	if location != "https://example.com/new" {
		t.Errorf("Expected Location 'https://example.com/new', got '%s'", location)
	}

	// Verify body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Moved" {
		t.Errorf("Expected body 'Moved', got '%s'", string(body))
	}
}

func TestMockTransport_RegisterError(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/error"
	expectedErr := errors.New("network timeout")

	mock.RegisterError(url, expectedErr)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = mock.RoundTrip(req)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != expectedErr.Error() {
		t.Errorf("Expected error '%s', got '%s'", expectedErr.Error(), err.Error())
	}
}

func TestMockTransport_RegisterPattern(t *testing.T) {
	mock := NewMockTransport()

	// Register a pattern for all pages under /api/
	err := mock.RegisterPattern(`^https://example\.com/api/.*$`, &MockResponse{
		StatusCode: 200,
		Body:       `{"status": "ok"}`,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test multiple URLs that should match the pattern
	testURLs := []string{
		"https://example.com/api/users",
		"https://example.com/api/posts",
		"https://example.com/api/users/123",
	}

	for _, url := range testURLs {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := mock.RoundTrip(req)
		if err != nil {
			t.Fatalf("Error for URL %s: %v", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("URL %s: Expected status 200, got %d", url, resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if string(body) != `{"status": "ok"}` {
			t.Errorf("URL %s: Expected body '{\"status\": \"ok\"}', got '%s'", url, string(body))
		}
	}

	// Test a URL that should NOT match the pattern
	req, err := http.NewRequest("GET", "https://example.com/other", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should return 404 since no mock is registered for this URL
	if resp.StatusCode != 404 {
		t.Errorf("Expected status 404 for non-matching URL, got %d", resp.StatusCode)
	}
}

func TestMockTransport_Delay(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/slow"

	mock.RegisterResponse(url, &MockResponse{
		StatusCode: 200,
		Body:       "Slow response",
		Delay:      100 * time.Millisecond,
	})

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	resp, err := mock.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Verify the delay was applied
	if elapsed < 100*time.Millisecond {
		t.Errorf("Expected delay of at least 100ms, got %v", elapsed)
	}
}

func TestMockTransport_Fallback(t *testing.T) {
	// Create a mock transport with fallback
	mock := NewMockTransport()
	fallbackMock := NewMockTransport()

	// Register response in fallback
	fallbackMock.RegisterHTML("https://fallback.com/", "<html>Fallback</html>")

	// Set fallback
	mock.SetFallback(fallbackMock)

	// Register response in main mock
	mock.RegisterHTML("https://example.com/", "<html>Main</html>")

	// Test main mock URL (should use main mock)
	req, err := http.NewRequest("GET", "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "<html>Main</html>" {
		t.Errorf("Expected main mock response, got '%s'", string(body))
	}

	// Test fallback URL (should use fallback)
	req, err = http.NewRequest("GET", "https://fallback.com/", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err = mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != "<html>Fallback</html>" {
		t.Errorf("Expected fallback response, got '%s'", string(body))
	}
}

func TestMockTransport_Reset(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/"

	mock.RegisterHTML(url, "<html>Test</html>")

	// Verify mock is registered
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Reset the mock
	mock.Reset()

	// Verify mock is cleared (should return 404)
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err = mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("Expected status 404 after reset, got %d", resp.StatusCode)
	}
}

func TestMockTransport_WithCollector(t *testing.T) {
	// Create a mock transport with test data
	mock := NewMockTransport()
	mock.RegisterHTML("https://example.com/", `<html>
		<head><title>Home Page</title></head>
		<body>
			<a href="https://example.com/page1">Page 1</a>
			<a href="https://example.com/page2">Page 2</a>
		</body>
	</html>`)
	mock.RegisterHTML("https://example.com/page1", `<html>
		<head><title>Page 1</title></head>
		<body>Content 1</body>
	</html>`)
	mock.RegisterHTML("https://example.com/page2", `<html>
		<head><title>Page 2</title></head>
		<body>Content 2</body>
	</html>`)

	// Create low-level collector with mock transport
	c := NewCollector(context.Background(), nil)
	c.WithTransport(mock)

	// Track visited pages
	var mu sync.Mutex
	visitedTitles := []string{}

	// Register callback to follow links
	c.OnHTML("a[href]", func(e *HTMLElement) {
		link := e.Attr("href")
		e.Request.Visit(link)
	})

	// Register callback to capture titles
	c.OnHTML("title", func(e *HTMLElement) {
		mu.Lock()
		defer mu.Unlock()
		visitedTitles = append(visitedTitles, e.Text)
	})

	// Start crawling
	err := c.Visit("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}


	mu.Lock()
	defer mu.Unlock()

	// Verify all pages were visited
	if len(visitedTitles) != 3 {
		t.Errorf("Expected 3 pages visited, got %d", len(visitedTitles))
	}

	expectedTitles := map[string]bool{
		"Home Page": false,
		"Page 1":    false,
		"Page 2":    false,
	}

	for _, title := range visitedTitles {
		if _, ok := expectedTitles[title]; ok {
			expectedTitles[title] = true
		} else {
			t.Errorf("Unexpected title: %s", title)
		}
	}

	for title, visited := range expectedTitles {
		if !visited {
			t.Errorf("Expected to visit page with title '%s'", title)
		}
	}
}

func TestMockTransport_InvalidPattern(t *testing.T) {
	mock := NewMockTransport()

	// Try to register an invalid regex pattern
	err := mock.RegisterPattern(`[invalid(regex`, &MockResponse{
		StatusCode: 200,
		Body:       "test",
	})

	if err == nil {
		t.Error("Expected error for invalid regex pattern, got nil")
	}
}

func TestMockTransport_DefaultStatusCode(t *testing.T) {
	mock := NewMockTransport()
	url := "https://example.com/"

	// Register response without status code
	mock.RegisterResponse(url, &MockResponse{
		Body: "test",
	})

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should default to 200
	if resp.StatusCode != 200 {
		t.Errorf("Expected default status code 200, got %d", resp.StatusCode)
	}
}
