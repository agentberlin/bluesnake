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
	"net/http"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/debug"
	"github.com/agentberlin/bluesnake/testutil"
)

// Use shared test data from testutil package
var (
	serverIndexResponse = testutil.ServerIndexResponse
	callbackTestHTML    = testutil.CallbackTestHTML
	robotsFile          = testutil.RobotsFile
)

const testBaseURL = "http://test.local"

// setupMockTransport creates a new MockTransport with all test endpoints registered
func setupMockTransport() *MockTransport {
	mock := NewMockTransport()

	// Index page
	mock.RegisterResponse(testBaseURL+"/", &MockResponse{
		StatusCode: 200,
		Body:       string(serverIndexResponse),
	})

	// Callback test page
	mock.RegisterHTML(testBaseURL+"/callback_test", string(callbackTestHTML))

	// HTML page
	htmlContent := `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
</head>
<body>
<h1>Hello World</h1>
<p class="description">This is a test page</p>
<p class="description">This is a test paragraph</p>
</body>
</html>`
	mock.RegisterHTML(testBaseURL+"/html", htmlContent)

	// HTML page without content-type
	mock.RegisterResponse(testBaseURL+"/html?no-content-type=yes", &MockResponse{
		StatusCode: 200,
		Body:       htmlContent,
		Headers:    make(http.Header), // No Content-Type header
	})

	// XML page
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<page>
	<title>Test Page</title>
	<paragraph type="description">This is a test page</paragraph>
	<paragraph type="description">This is a test paragraph</paragraph>
</page>`
	headers := make(http.Header)
	headers.Set("Content-Type", "application/xml")
	mock.RegisterResponse(testBaseURL+"/xml", &MockResponse{
		StatusCode: 200,
		Body:       xmlContent,
		Headers:    headers,
	})

	// Login endpoint (POST)
	headersLogin := make(http.Header)
	headersLogin.Set("Content-Type", "text/html")
	mock.RegisterResponse(testBaseURL+"/login", &MockResponse{
		StatusCode: 200,
		Body:       "hello", // Most tests expect this value
		Headers:    headersLogin,
	})

	// Robots.txt
	mock.RegisterResponse(testBaseURL+"/robots.txt", &MockResponse{
		StatusCode: 200,
		Body:       robotsFile,
	})

	// Allowed/disallowed pages
	mock.RegisterResponse(testBaseURL+"/allowed", &MockResponse{
		StatusCode: 200,
		Body:       "allowed",
	})
	mock.RegisterResponse(testBaseURL+"/disallowed", &MockResponse{
		StatusCode: 200,
		Body:       "disallowed",
	})

	// Redirected page
	mock.RegisterHTML(testBaseURL+"/redirected/", `<a href="test">test</a>`)

	// 500 error page
	headers500 := make(http.Header)
	headers500.Set("Content-Type", "text/html")
	mock.RegisterResponse(testBaseURL+"/500", &MockResponse{
		StatusCode: 500,
		Body:       "<p>error</p>",
		Headers:    headers500,
	})

	// User agent echo endpoint
	mock.RegisterResponse(testBaseURL+"/user_agent", &MockResponse{
		StatusCode: 200,
		BodyFunc: func(req *http.Request) string {
			return req.Header.Get("User-Agent")
		},
	})

	// Accept header echo endpoint
	mock.RegisterResponse(testBaseURL+"/accept_header", &MockResponse{
		StatusCode: 200,
		BodyFunc: func(req *http.Request) string {
			return req.Header.Get("Accept")
		},
	})

	// Custom header echo endpoint
	mock.RegisterResponse(testBaseURL+"/custom_header", &MockResponse{
		StatusCode: 200,
		BodyFunc: func(req *http.Request) string {
			return req.Header.Get("Test")
		},
	})

	// Host header echo endpoint
	mock.RegisterResponse(testBaseURL+"/host_header", &MockResponse{
		StatusCode: 200,
		BodyFunc: func(req *http.Request) string {
			return req.Host
		},
	})

	// Base tag pages
	mock.RegisterHTML(testBaseURL+"/base", `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="http://xy.com/" />
</head>
<body>
<a href="z">link</a>
</body>
</html>`)

	mock.RegisterHTML(testBaseURL+"/base_relative", `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="/foobar/" />
</head>
<body>
<a href="z">link</a>
</body>
</html>`)

	mock.RegisterHTML(testBaseURL+"/tabs_and_newlines", `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="/foo	bar/" />
</head>
<body>
<a href="x
y">link</a>
</body>
</html>`)

	mock.RegisterHTML(testBaseURL+"/foobar/xy", `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
</head>
<body>
<p>hello</p>
</body>
</html>`)

	// Percent encoding test
	mock.RegisterResponse(testBaseURL+"/100%25", &MockResponse{
		StatusCode: 200,
		Body:       "100 percent",
	})

	// Large binary
	headersBinary := make(http.Header)
	headersBinary.Set("Content-Type", "application/octet-stream")
	mock.RegisterResponse(testBaseURL+"/large_binary", &MockResponse{
		StatusCode: 200,
		Body:       string(bytes.Repeat([]byte{0x41}, 1000)), // Simulate large content
		Headers:    headersBinary,
	})

	// Slow endpoint - we'll handle this in specific tests

	// Set/check cookie - we'll handle these in cookie tests

	// Catch-all pattern for root with query parameters
	mock.RegisterPattern(`^http://test\.local/\?`, &MockResponse{
		StatusCode: 200,
		Body:       string(serverIndexResponse),
	})

	// Catch-all pattern for /html with query parameters (for benchmarks)
	mock.RegisterPattern(`^http://test\.local/html\?`, &MockResponse{
		StatusCode: 200,
		Body: `<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
</head>
<body>
<h1>Hello World</h1>
<p class="description">This is a test page</p>
<p class="description">This is a test paragraph</p>
</body>
</html>`,
		Headers: func() http.Header {
			h := make(http.Header)
			h.Set("Content-Type", "text/html")
			return h
		}(),
	})

	return mock
}

var newCollectorTests = map[string]func(*testing.T){
	"UserAgent": func(t *testing.T) {
		for _, ua := range []string{
			"foo",
			"bar",
		} {
			c := NewCollector(context.Background(), &HTTPConfig{UserAgent: ua})

			if got, want := c.UserAgent, ua; got != want {
				t.Fatalf("c.UserAgent = %q, want %q", got, want)
			}
		}
	},
	"AllowURLRevisit": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{AllowURLRevisit: true})

		if !c.AllowURLRevisit {
			t.Fatal("c.AllowURLRevisit = false, want true")
		}
	},
	"MaxBodySize": func(t *testing.T) {
		for _, sizeInBytes := range []int{
			1024 * 1024,
			1024,
			0,
		} {
			c := NewCollector(context.Background(), &HTTPConfig{MaxBodySize: sizeInBytes})

			if got, want := c.MaxBodySize, sizeInBytes; got != want {
				t.Fatalf("c.MaxBodySize = %d, want %d", got, want)
			}
		}
	},
	"CacheDir": func(t *testing.T) {
		for _, path := range []string{
			"/tmp/",
			"/var/cache/",
		} {
			c := NewCollector(context.Background(), &HTTPConfig{CacheDir: path})

			if got, want := c.CacheDir, path; got != want {
				t.Fatalf("c.CacheDir = %q, want %q", got, want)
			}
		}
	},
	"CacheExpiration": func(t *testing.T) {
		for _, d := range []time.Duration{
			5 * time.Second,
			10 * time.Minute,
			0,
		} {
			c := NewCollector(context.Background(), &HTTPConfig{CacheExpiration: d})

			if got, want := c.CacheExpiration, d; got != want {
				t.Fatalf("c.CacheExpiration = %v, want %v", got, want)
			}
		}
	},
	"ID": func(t *testing.T) {
		// Test ID=0 triggers auto-assignment
		c0 := NewCollector(context.Background(), &HTTPConfig{ID: 0})
		if c0.ID == 0 {
			t.Fatal("c.ID = 0, expected auto-assignment to non-zero value")
		}

		// Test explicit non-zero IDs are preserved
		for _, id := range []uint32{
			1,
			2,
		} {
			c := NewCollector(context.Background(), &HTTPConfig{ID: id})

			if got, want := c.ID, id; got != want {
				t.Fatalf("c.ID = %d, want %d", got, want)
			}
		}
	},
	"DetectCharset": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{DetectCharset: true})

		if !c.DetectCharset {
			t.Fatal("c.DetectCharset = false, want true")
		}
	},
	"Debugger": func(t *testing.T) {
		d := &debug.LogDebugger{}
		c := NewCollector(context.Background(), &HTTPConfig{Debugger: d})

		if got, want := c.debugger, d; got != want {
			t.Fatalf("c.debugger = %v, want %v", got, want)
		}
	},
	"CheckHead": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{CheckHead: true})

		if !c.CheckHead {
			t.Fatal("c.CheckHead = false, want true")
		}
	},
	"Headers": func(t *testing.T) {
		headers := map[string]string{
			"X-Custom-Header": "test-value",
			"Accept":          "application/json",
		}
		c := NewCollector(context.Background(), &HTTPConfig{Headers: headers})

		if c.Headers == nil {
			t.Fatal("c.Headers = nil, want headers map")
		}

		if got := c.Headers.Get("X-Custom-Header"); got != "test-value" {
			t.Fatalf("c.Headers.Get(\"X-Custom-Header\") = %q, want %q", got, "test-value")
		}

		if got := c.Headers.Get("Accept"); got != "application/json" {
			t.Fatalf("c.Headers.Get(\"Accept\") = %q, want %q", got, "application/json")
		}
	},
	"IgnoreRobotsTxt": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{IgnoreRobotsTxt: true})

		if !c.IgnoreRobotsTxt {
			t.Fatal("c.IgnoreRobotsTxt = false, want true")
		}
	},
	"ParseHTTPErrorResponse": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{ParseHTTPErrorResponse: true})

		if !c.ParseHTTPErrorResponse {
			t.Fatal("c.ParseHTTPErrorResponse = false, want true")
		}
	},
	"TraceHTTP": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{TraceHTTP: true})

		if !c.TraceHTTP {
			t.Fatal("c.TraceHTTP = false, want true")
		}
	},
	"MaxRequests": func(t *testing.T) {
		for _, maxReq := range []uint32{
			10,
			100,
			1000,
		} {
			c := NewCollector(context.Background(), &HTTPConfig{MaxRequests: maxReq})

			if got, want := c.MaxRequests, maxReq; got != want {
				t.Fatalf("c.MaxRequests = %d, want %d", got, want)
			}
		}
	},
	"EnableRendering": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{EnableRendering: true})

		if !c.EnableRendering {
			t.Fatal("c.EnableRendering = false, want true")
		}
	},
	"RenderingConfig": func(t *testing.T) {
		renderConfig := &RenderingConfig{
			InitialWaitMs: 2000,
			ScrollWaitMs:  3000,
			FinalWaitMs:   1500,
		}
		c := NewCollector(context.Background(), &HTTPConfig{RenderingConfig: renderConfig})

		if c.RenderingConfig == nil {
			t.Fatal("c.RenderingConfig = nil, want config")
		}

		if got, want := c.RenderingConfig.InitialWaitMs, 2000; got != want {
			t.Fatalf("c.RenderingConfig.InitialWaitMs = %d, want %d", got, want)
		}

		if got, want := c.RenderingConfig.ScrollWaitMs, 3000; got != want {
			t.Fatalf("c.RenderingConfig.ScrollWaitMs = %d, want %d", got, want)
		}

		if got, want := c.RenderingConfig.FinalWaitMs, 1500; got != want {
			t.Fatalf("c.RenderingConfig.FinalWaitMs = %d, want %d", got, want)
		}
	},
	"EnableContentHash": func(t *testing.T) {
		c := NewCollector(context.Background(), &HTTPConfig{EnableContentHash: true})

		if !c.EnableContentHash {
			t.Fatal("c.EnableContentHash = false, want true")
		}
	},
	"ContentHashAlgorithm": func(t *testing.T) {
		for _, algo := range []string{
			"xxhash",
			"md5",
			"sha256",
		} {
			c := NewCollector(context.Background(), &HTTPConfig{ContentHashAlgorithm: algo})

			if got, want := c.ContentHashAlgorithm, algo; got != want {
				t.Fatalf("c.ContentHashAlgorithm = %q, want %q", got, want)
			}
		}
	},
	"ContentHashConfig": func(t *testing.T) {
		hashConfig := &ContentHashConfig{
			ExcludeTags:        []string{"script", "style"},
			IncludeOnlyTags:    []string{"article", "main"},
			StripTimestamps:    true,
			StripAnalytics:     true,
			StripComments:      true,
			CollapseWhitespace: false,
		}
		c := NewCollector(context.Background(), &HTTPConfig{ContentHashConfig: hashConfig})

		if c.ContentHashConfig == nil {
			t.Fatal("c.ContentHashConfig = nil, want config")
		}

		if got, want := len(c.ContentHashConfig.ExcludeTags), 2; got != want {
			t.Fatalf("len(c.ContentHashConfig.ExcludeTags) = %d, want %d", got, want)
		}

		if got, want := len(c.ContentHashConfig.IncludeOnlyTags), 2; got != want {
			t.Fatalf("len(c.ContentHashConfig.IncludeOnlyTags) = %d, want %d", got, want)
		}

		if !c.ContentHashConfig.StripTimestamps {
			t.Fatal("c.ContentHashConfig.StripTimestamps = false, want true")
		}

		if c.ContentHashConfig.CollapseWhitespace {
			t.Fatal("c.ContentHashConfig.CollapseWhitespace = true, want false")
		}
	},
}
