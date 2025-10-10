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
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// MockResponse represents a mock HTTP response
type MockResponse struct {
	// StatusCode is the HTTP status code to return (default: 200)
	StatusCode int
	// Body is the response body content (used if BodyFunc is nil)
	Body string
	// BodyFunc is a function that generates the body dynamically based on the request
	// If set, this takes precedence over Body
	BodyFunc func(*http.Request) string
	// Headers are the HTTP headers to include in the response
	Headers http.Header
	// Delay simulates network latency before returning the response
	Delay time.Duration
	// Error simulates a network error
	Error error
}

// mockPattern represents a URL pattern matcher with associated response
type mockPattern struct {
	pattern  *regexp.Regexp
	response *MockResponse
}

// MockTransport implements http.RoundTripper for testing purposes.
// It allows you to register mock responses for specific URLs or URL patterns
// without needing to run an actual HTTP server.
type MockTransport struct {
	// responses maps exact URLs to their mock responses
	responses map[string]*MockResponse
	// patterns contains regex patterns for matching URLs
	patterns []mockPattern
	// fallback is an optional RoundTripper to use when no mock is registered
	fallback http.RoundTripper
	// mutex protects concurrent access to the maps
	mutex sync.RWMutex
}

// NewMockTransport creates a new MockTransport instance
func NewMockTransport() *MockTransport {
	return &MockTransport{
		responses: make(map[string]*MockResponse),
		patterns:  make([]mockPattern, 0),
	}
}

// RegisterResponse registers a mock response for an exact URL match
func (m *MockTransport) RegisterResponse(url string, response *MockResponse) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Set default status code if not provided
	if response.StatusCode == 0 {
		response.StatusCode = 200
	}

	// Initialize headers if nil
	if response.Headers == nil {
		response.Headers = make(http.Header)
	}

	m.responses[url] = response
}

// RegisterHTML is a convenience method to register an HTML response with status 200
func (m *MockTransport) RegisterHTML(url, html string) {
	headers := make(http.Header)
	headers.Set("Content-Type", "text/html; charset=utf-8")

	m.RegisterResponse(url, &MockResponse{
		StatusCode: 200,
		Body:       html,
		Headers:    headers,
	})
}

// RegisterJSON is a convenience method to register a JSON response with status 200
func (m *MockTransport) RegisterJSON(url, json string) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json; charset=utf-8")

	m.RegisterResponse(url, &MockResponse{
		StatusCode: 200,
		Body:       json,
		Headers:    headers,
	})
}

// RegisterError registers a mock error for a URL (simulates network failure)
func (m *MockTransport) RegisterError(url string, err error) {
	m.RegisterResponse(url, &MockResponse{
		Error: err,
	})
}

// RegisterPattern registers a mock response for URLs matching a regex pattern
func (m *MockTransport) RegisterPattern(pattern string, response *MockResponse) error {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Set default status code if not provided
	if response.StatusCode == 0 {
		response.StatusCode = 200
	}

	// Initialize headers if nil
	if response.Headers == nil {
		response.Headers = make(http.Header)
	}

	m.patterns = append(m.patterns, mockPattern{
		pattern:  regex,
		response: response,
	})

	return nil
}

// SetFallback sets a fallback RoundTripper to use when no mock is registered for a URL.
// This is useful for testing scenarios where you want to mock some URLs but allow
// real HTTP requests for others.
func (m *MockTransport) SetFallback(fallback http.RoundTripper) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.fallback = fallback
}

// Reset clears all registered responses and patterns
func (m *MockTransport) Reset() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.responses = make(map[string]*MockResponse)
	m.patterns = make([]mockPattern, 0)
}

// RoundTrip implements the http.RoundTripper interface
func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mutex.RLock()

	// First, try exact URL match
	url := req.URL.String()
	mockResp, found := m.responses[url]

	// If not found, try pattern matching
	if !found {
		for _, p := range m.patterns {
			if p.pattern.MatchString(url) {
				mockResp = p.response
				found = true
				break
			}
		}
	}

	// If still not found, try fallback
	if !found {
		fallback := m.fallback
		m.mutex.RUnlock()

		if fallback != nil {
			return fallback.RoundTrip(req)
		}

		// No mock and no fallback - return 404
		return &http.Response{
			StatusCode: 404,
			Body:       io.NopCloser(bytes.NewBufferString("Not Found")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}

	m.mutex.RUnlock()

	// Simulate delay if specified
	if mockResp.Delay > 0 {
		time.Sleep(mockResp.Delay)
	}

	// Return error if specified
	if mockResp.Error != nil {
		return nil, mockResp.Error
	}

	// Determine body content
	bodyContent := mockResp.Body
	if mockResp.BodyFunc != nil {
		bodyContent = mockResp.BodyFunc(req)
	}

	// Build the mock HTTP response
	resp := &http.Response{
		StatusCode: mockResp.StatusCode,
		Body:       io.NopCloser(bytes.NewBufferString(bodyContent)),
		Header:     cloneHeaders(mockResp.Headers),
		Request:    req,
		// Set other required fields
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}

	// Set Content-Length if not already set
	if resp.Header.Get("Content-Length") == "" {
		resp.ContentLength = int64(len(bodyContent))
	}

	return resp, nil
}

// cloneHeaders creates a copy of HTTP headers
func cloneHeaders(headers http.Header) http.Header {
	clone := make(http.Header)
	for key, values := range headers {
		clone[key] = append([]string{}, values...)
	}
	return clone
}

// ErrMockNotFound is returned when no mock is registered for a URL
var ErrMockNotFound = errors.New("no mock response registered for URL")
