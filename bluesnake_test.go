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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/agentberlin/bluesnake/debug"
)

var serverIndexResponse = []byte("hello world\n")
var callbackTestHTML = []byte(`
<!DOCTYPE html>
<html>
<head>
<title>Callback Test Page</title>
</head>
<body>
<div id="firstElem">First</div>
<div id="secondElem">Second</div>
<div id="thirdElem">Third</div>
</body>
</html>
`)
var robotsFile = `
User-agent: *
Allow: /allowed
Disallow: /disallowed
Disallow: /allowed*q=
`

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
			c := NewCollector(UserAgent(ua))

			if got, want := c.UserAgent, ua; got != want {
				t.Fatalf("c.UserAgent = %q, want %q", got, want)
			}
		}
	},
	"MaxDepth": func(t *testing.T) {
		for _, depth := range []int{
			12,
			34,
			0,
		} {
			c := NewCollector(MaxDepth(depth))

			if got, want := c.MaxDepth, depth; got != want {
				t.Fatalf("c.MaxDepth = %d, want %d", got, want)
			}
		}
	},
	"AllowedDomains": func(t *testing.T) {
		for _, domains := range [][]string{
			{"example.com", "example.net"},
			{"example.net"},
			{},
			nil,
		} {
			c := NewCollector(AllowedDomains(domains...))

			if got, want := c.AllowedDomains, domains; !reflect.DeepEqual(got, want) {
				t.Fatalf("c.AllowedDomains = %q, want %q", got, want)
			}
		}
	},
	"DisallowedDomains": func(t *testing.T) {
		for _, domains := range [][]string{
			{"example.com", "example.net"},
			{"example.net"},
			{},
			nil,
		} {
			c := NewCollector(DisallowedDomains(domains...))

			if got, want := c.DisallowedDomains, domains; !reflect.DeepEqual(got, want) {
				t.Fatalf("c.DisallowedDomains = %q, want %q", got, want)
			}
		}
	},
	"DisallowedURLFilters": func(t *testing.T) {
		for _, filters := range [][]*regexp.Regexp{
			{regexp.MustCompile(`.*not_allowed.*`)},
		} {
			c := NewCollector(DisallowedURLFilters(filters...))

			if got, want := c.DisallowedURLFilters, filters; !reflect.DeepEqual(got, want) {
				t.Fatalf("c.DisallowedURLFilters = %v, want %v", got, want)
			}
		}
	},
	"URLFilters": func(t *testing.T) {
		for _, filters := range [][]*regexp.Regexp{
			{regexp.MustCompile(`\w+`)},
			{regexp.MustCompile(`\d+`)},
			{},
			nil,
		} {
			c := NewCollector(URLFilters(filters...))

			if got, want := c.URLFilters, filters; !reflect.DeepEqual(got, want) {
				t.Fatalf("c.URLFilters = %v, want %v", got, want)
			}
		}
	},
	"AllowURLRevisit": func(t *testing.T) {
		c := NewCollector(AllowURLRevisit())

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
			c := NewCollector(MaxBodySize(sizeInBytes))

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
			c := NewCollector(CacheDir(path))

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
			c := NewCollector(CacheExpiration(d))

			if got, want := c.CacheExpiration, d; got != want {
				t.Fatalf("c.CacheExpiration = %v, want %v", got, want)
			}
		}
	},
	"IgnoreRobotsTxt": func(t *testing.T) {
		c := NewCollector(IgnoreRobotsTxt())

		if !c.IgnoreRobotsTxt {
			t.Fatal("c.IgnoreRobotsTxt = false, want true")
		}
	},
	"ID": func(t *testing.T) {
		for _, id := range []uint32{
			0,
			1,
			2,
		} {
			c := NewCollector(ID(id))

			if got, want := c.ID, id; got != want {
				t.Fatalf("c.ID = %d, want %d", got, want)
			}
		}
	},
	"DetectCharset": func(t *testing.T) {
		c := NewCollector(DetectCharset())

		if !c.DetectCharset {
			t.Fatal("c.DetectCharset = false, want true")
		}
	},
	"Debugger": func(t *testing.T) {
		d := &debug.LogDebugger{}
		c := NewCollector(Debugger(d))

		if got, want := c.debugger, d; got != want {
			t.Fatalf("c.debugger = %v, want %v", got, want)
		}
	},
	"CheckHead": func(t *testing.T) {
		c := NewCollector(CheckHead())

		if !c.CheckHead {
			t.Fatal("c.CheckHead = false, want true")
		}
	},
	"Async": func(t *testing.T) {
		c := NewCollector(Async())

		if !c.Async {
			t.Fatal("c.Async = false, want true")
		}
	},
}

func TestNoAcceptHeader(t *testing.T) {
	mock := setupMockTransport()

	var receivedHeader string
	// checks if Accept is enabled by default
	func() {
		c := NewCollector()
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/accept_header")
		if receivedHeader != "*/*" {
			t.Errorf("default Accept header isn't */*. got: %v", receivedHeader)
		}
	}()

	// checks if Accept can be disabled
	func() {
		c := NewCollector()
		c.SetClient(&http.Client{Transport: mock})
		c.OnRequest(func(r *Request) {
			r.Headers.Del("Accept")
		})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/accept_header")
		if receivedHeader != "" {
			t.Errorf("failed to pass request with no Accept header. got: %v", receivedHeader)
		}
	}()
}

func TestNewCollector(t *testing.T) {
	t.Run("Functional Options", func(t *testing.T) {
		for name, test := range newCollectorTests {
			t.Run(name, test)
		}
	})
}

func TestCollectorVisit(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	onRequestCalled := false
	onResponseCalled := false
	onScrapedCalled := false

	c.OnRequest(func(r *Request) {
		onRequestCalled = true
		r.Ctx.Put("x", "y")
	})

	c.OnResponse(func(r *Response) {
		onResponseCalled = true

		if r.Ctx.Get("x") != "y" {
			t.Error("Failed to retrieve context value for key 'x'")
		}

		if !bytes.Equal(r.Body, serverIndexResponse) {
			t.Error("Response body does not match with the original content")
		}
	})

	c.OnScraped(func(r *Response) {
		if !onResponseCalled {
			t.Error("OnScraped called before OnResponse")
		}

		if !onRequestCalled {
			t.Error("OnScraped called before OnRequest")
		}

		onScrapedCalled = true
	})

	c.Visit(testBaseURL + "/")

	if !onRequestCalled {
		t.Error("Failed to call OnRequest callback")
	}

	if !onResponseCalled {
		t.Error("Failed to call OnResponse callback")
	}

	if !onScrapedCalled {
		t.Error("Failed to call OnScraped callback")
	}
}

func TestCollectorVisitWithAllowedDomains(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(AllowedDomains("test.local"))
	c.SetClient(&http.Client{Transport: mock})

	err := c.Visit(testBaseURL + "/")
	if err != nil {
		t.Errorf("Failed to visit url %s", testBaseURL)
	}

	err = c.Visit("http://example.com")
	if err != ErrForbiddenDomain {
		t.Errorf("c.Visit should return ErrForbiddenDomain, but got %v", err)
	}
}

func TestCollectorVisitWithDisallowedDomains(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(DisallowedDomains("test.local"))
	c.SetClient(&http.Client{Transport: mock})

	err := c.Visit(testBaseURL + "/")
	if err != ErrForbiddenDomain {
		t.Errorf("c.Visit should return ErrForbiddenDomain, but got %v", err)
	}

	c2 := NewCollector(DisallowedDomains("example.com"))
	c2.SetClient(&http.Client{Transport: mock})

	err = c2.Visit("http://example.com:8080")
	if err != ErrForbiddenDomain {
		t.Errorf("c.Visit should return ErrForbiddenDomain, but got %v", err)
	}
	err = c2.Visit(testBaseURL + "/")
	if err != nil {
		t.Errorf("Failed to visit url %s", testBaseURL)
	}
}

func TestCollectorVisitResponseHeaders(t *testing.T) {
	mock := setupMockTransport()

	var onResponseHeadersCalled bool

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponseHeaders(func(r *Response) {
		onResponseHeadersCalled = true
		if r.Headers.Get("Content-Type") == "application/octet-stream" {
			r.Request.Abort()
		}
	})
	c.OnResponse(func(r *Response) {
		t.Error("OnResponse was called")
	})
	c.Visit(testBaseURL + "/large_binary")
	if !onResponseHeadersCalled {
		t.Error("OnResponseHeaders was not called")
	}
}

func TestCollectorOnHTML(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	titleCallbackCalled := false
	paragraphCallbackCount := 0

	c.OnHTML("title", func(e *HTMLElement) {
		titleCallbackCalled = true
		if e.Text != "Test Page" {
			t.Error("Title element text does not match, got", e.Text)
		}
	})

	c.OnHTML("p", func(e *HTMLElement) {
		paragraphCallbackCount++
		if e.Attr("class") != "description" {
			t.Error("Failed to get paragraph's class attribute")
		}
	})

	c.OnHTML("body", func(e *HTMLElement) {
		if e.ChildAttr("p", "class") != "description" {
			t.Error("Invalid class value")
		}
		classes := e.ChildAttrs("p", "class")
		if len(classes) != 2 {
			t.Error("Invalid class values")
		}
	})

	c.Visit(testBaseURL + "/html")

	if !titleCallbackCalled {
		t.Error("Failed to call OnHTML callback for <title> tag")
	}

	if paragraphCallbackCount != 2 {
		t.Error("Failed to find all <p> tags")
	}
}

func TestCollectorContentSniffing(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	htmlCallbackCalled := false

	c.OnResponse(func(r *Response) {
		if (*r.Headers)["Content-Type"] != nil {
			t.Error("Content-Type unexpectedly not nil")
		}
	})

	c.OnHTML("html", func(e *HTMLElement) {
		htmlCallbackCalled = true
	})

	err := c.Visit(testBaseURL + "/html?no-content-type=yes")
	if err != nil {
		t.Fatal(err)
	}

	if !htmlCallbackCalled {
		t.Error("OnHTML was not called")
	}
}

func TestCollectorURLRevisit(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	visitCount := 0

	c.OnRequest(func(r *Request) {
		visitCount++
	})

	c.Visit(testBaseURL + "/")
	c.Visit(testBaseURL + "/")

	if visitCount != 1 {
		t.Error("URL revisited")
	}

	c.AllowURLRevisit = true

	c.Visit(testBaseURL + "/")
	c.Visit(testBaseURL + "/")

	if visitCount != 3 {
		t.Error("URL not revisited")
	}
}

func TestCollectorPostRevisit(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	postData := map[string]string{
		"name": postValue,
	}
	visitCount := 0

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}
		visitCount++
	})

	c.Post(testBaseURL+"/login", postData)
	c.Post(testBaseURL+"/login", postData)
	c.Post(testBaseURL+"/login", map[string]string{
		"name":     postValue,
		"lastname": "world",
	})

	if visitCount != 2 {
		t.Error("URL POST revisited")
	}

	c.AllowURLRevisit = true

	c.Post(testBaseURL+"/login", postData)
	c.Post(testBaseURL+"/login", postData)

	if visitCount != 4 {
		t.Error("URL POST not revisited")
	}
}

func TestCollectorPostURLRevisitCheck(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	postValue := "hello"
	postData := map[string]string{
		"name": postValue,
	}

	posted, err := c.HasPosted(testBaseURL+"/login", postData)

	if err != nil {
		t.Error(err.Error())
	}

	if posted != false {
		t.Error("Expected URL to NOT have been visited")
	}

	c.Post(testBaseURL+"/login", postData)

	posted, err = c.HasPosted(testBaseURL+"/login", postData)

	if err != nil {
		t.Error(err.Error())
	}

	if posted != true {
		t.Error("Expected URL to have been visited")
	}

	postData["lastname"] = "world"
	posted, err = c.HasPosted(testBaseURL+"/login", postData)

	if err != nil {
		t.Error(err.Error())
	}

	if posted != false {
		t.Error("Expected URL to NOT have been visited")
	}

	c.Post(testBaseURL+"/login", postData)

	posted, err = c.HasPosted(testBaseURL+"/login", postData)

	if err != nil {
		t.Error(err.Error())
	}

	if posted != true {
		t.Error("Expected URL to have been visited")
	}
}

// TestCollectorURLRevisitDomainDisallowed ensures that disallowed URL is not considered visited.
func TestCollectorURLRevisitDomainDisallowed(t *testing.T) {
	mock := setupMockTransport()

	parsedURL, err := url.Parse(testBaseURL)
	if err != nil {
		t.Fatal(err)
	}

	c := NewCollector(DisallowedDomains(parsedURL.Hostname()))
	c.SetClient(&http.Client{Transport: mock})
	err = c.Visit(testBaseURL)
	if got, want := err, ErrForbiddenDomain; got != want {
		t.Fatalf("wrong error on first visit: got=%v want=%v", got, want)
	}
	err = c.Visit(testBaseURL)
	if got, want := err, ErrForbiddenDomain; got != want {
		t.Fatalf("wrong error on second visit: got=%v want=%v", got, want)
	}

}

func TestCollectorPost(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}
	})

	c.Post(testBaseURL+"/login", map[string]string{
		"name": postValue,
	})
}

func TestCollectorPostRaw(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}
	})

	c.PostRaw(testBaseURL+"/login", []byte("name="+postValue))
}

func TestCollectorPostRawRevisit(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	postData := "name=" + postValue
	visitCount := 0

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST RAW")
		}
		visitCount++
	})

	c.PostRaw(testBaseURL+"/login", []byte(postData))
	c.PostRaw(testBaseURL+"/login", []byte(postData))
	c.PostRaw(testBaseURL+"/login", []byte(postData+"&lastname=world"))

	if visitCount != 2 {
		t.Error("URL POST RAW revisited")
	}

	c.AllowURLRevisit = true

	c.PostRaw(testBaseURL+"/login", []byte(postData))
	c.PostRaw(testBaseURL+"/login", []byte(postData))

	if visitCount != 4 {
		t.Error("URL POST RAW not revisited")
	}
}

func TestIssue594(t *testing.T) {
	// This is a regression test for a data race bug. There's no
	// assertions because it's meant to be used with race detector
	mock := setupMockTransport()

	c := NewCollector()
	// if timeout is set, this bug is not triggered
	client := &http.Client{Timeout: 0 * time.Second, Transport: mock}
	c.SetClient(client)

	c.Visit(testBaseURL)
}

func TestBaseTag(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		if u != "http://xy.com/z" {
			t.Error("Invalid <base /> tag handling in OnHTML: expected https://xy.com/z, got " + u)
		}
	})
	c.Visit(testBaseURL + "/base")

	c2 := NewCollector()
	c2.SetClient(&http.Client{Transport: mock})
	c2.OnXML("//a", func(e *XMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		if u != "http://xy.com/z" {
			t.Error("Invalid <base /> tag handling in OnXML: expected https://xy.com/z, got " + u)
		}
	})
	c2.Visit(testBaseURL + "/base")
}

func TestBaseTagRelative(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		expected := testBaseURL + "/foobar/z"
		if u != expected {
			t.Errorf("Invalid <base /> tag handling in OnHTML: expected %q, got %q", expected, u)
		}
	})
	c.Visit(testBaseURL + "/base_relative")

	c2 := NewCollector()
	c2.SetClient(&http.Client{Transport: mock})
	c2.OnXML("//a", func(e *XMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		expected := testBaseURL + "/foobar/z"
		if u != expected {
			t.Errorf("Invalid <base /> tag handling in OnXML: expected %q, got %q", expected, u)
		}
	})
	c2.Visit(testBaseURL + "/base_relative")
}

func TestTabsAndNewlines(t *testing.T) {
	// this test might look odd, but see step 3 of
	// https://url.spec.whatwg.org/#concept-basic-url-parser

	mock := setupMockTransport()

	visited := map[string]struct{}{}
	expected := map[string]struct{}{
		"/tabs_and_newlines": {},
		"/foobar/xy":         {},
	}

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnResponse(func(res *Response) {
		visited[res.Request.URL.EscapedPath()] = struct{}{}
	})
	c.OnHTML("a[href]", func(e *HTMLElement) {
		if err := e.Request.Visit(e.Attr("href")); err != nil {
			t.Errorf("visit failed: %v", err)
		}
	})

	if err := c.Visit(testBaseURL + "/tabs_and_newlines"); err != nil {
		t.Errorf("visit failed: %v", err)
	}

	if !reflect.DeepEqual(visited, expected) {
		t.Errorf("visited=%v expected=%v", visited, expected)
	}
}

func TestLonePercent(t *testing.T) {
	mock := setupMockTransport()

	var visitedPath string

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnResponse(func(res *Response) {
		visitedPath = res.Request.URL.RequestURI()
	})
	if err := c.Visit(testBaseURL + "/100%"); err != nil {
		t.Errorf("visit failed: %v", err)
	}
	// Automatic encoding is not really correct: browsers
	// would send bare percent here. However, Go net/http
	// cannot send such requests due to
	// https://github.com/golang/go/issues/29808. So we have two
	// alternatives really: return an error when attempting
	// to fetch such URLs, or at least try the encoded variant.
	// This test checks that the latter is attempted.
	if got, want := visitedPath, "/100%25"; got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
	// invalid URL escape in query component is not a problem,
	// but check it anyway
	if err := c.Visit(testBaseURL + "/?a=100%zz"); err != nil {
		t.Errorf("visit failed: %v", err)
	}
	if got, want := visitedPath, "/?a=100%zz"; got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestRobotsWhenAllowed(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.IgnoreRobotsTxt = false

	c.OnResponse(func(resp *Response) {
		if resp.StatusCode != 200 {
			t.Fatalf("Wrong response code: %d", resp.StatusCode)
		}
	})

	err := c.Visit(testBaseURL + "/allowed")

	if err != nil {
		t.Fatal(err)
	}
}

func TestRobotsWhenDisallowed(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.IgnoreRobotsTxt = false

	c.OnResponse(func(resp *Response) {
		t.Fatalf("Received response: %d", resp.StatusCode)
	})

	err := c.Visit(testBaseURL + "/disallowed")
	if err.Error() != "URL blocked by robots.txt" {
		t.Fatalf("wrong error message: %v", err)
	}
}

func TestRobotsWhenDisallowedWithQueryParameter(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.IgnoreRobotsTxt = false

	c.OnResponse(func(resp *Response) {
		t.Fatalf("Received response: %d", resp.StatusCode)
	})

	err := c.Visit(testBaseURL + "/allowed?q=1")
	if err.Error() != "URL blocked by robots.txt" {
		t.Fatalf("wrong error message: %v", err)
	}
}

func TestIgnoreRobotsWhenDisallowed(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.IgnoreRobotsTxt = true

	c.OnResponse(func(resp *Response) {
		if resp.StatusCode != 200 {
			t.Fatalf("Wrong response code: %d", resp.StatusCode)
		}
	})

	err := c.Visit(testBaseURL + "/disallowed")

	if err != nil {
		t.Fatal(err)
	}

}

func TestEnvSettings(t *testing.T) {
	mock := setupMockTransport()

	os.Setenv("COLLY_USER_AGENT", "test")
	defer os.Unsetenv("COLLY_USER_AGENT")

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	valid := false

	c.OnResponse(func(resp *Response) {
		if string(resp.Body) == "test" {
			valid = true
		}
	})

	c.Visit(testBaseURL + "/user_agent")

	if !valid {
		t.Fatalf("Wrong user-agent from environment")
	}
}

func TestUserAgent(t *testing.T) {
	const exampleUserAgent1 = "Example/1.0"
	const exampleUserAgent2 = "Example/2.0"
	const defaultUserAgent = "bluesnake - https://github.com/agentberlin/bluesnake"

	mock := setupMockTransport()

	var receivedUserAgent string

	func() {
		c := NewCollector()
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})
		c.Visit(testBaseURL + "/user_agent")
		if got, want := receivedUserAgent, defaultUserAgent; got != want {
			t.Errorf("mismatched User-Agent: got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(UserAgent(exampleUserAgent1))
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})
		c.Visit(testBaseURL + "/user_agent")
		if got, want := receivedUserAgent, exampleUserAgent1; got != want {
			t.Errorf("mismatched User-Agent: got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(UserAgent(exampleUserAgent1))
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})

		c.Request("GET", testBaseURL+"/user_agent", nil, nil, nil)
		if got, want := receivedUserAgent, exampleUserAgent1; got != want {
			t.Errorf("mismatched User-Agent (nil hdr): got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(UserAgent(exampleUserAgent1))
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})

		c.Request("GET", testBaseURL+"/user_agent", nil, nil, http.Header{})
		if got, want := receivedUserAgent, exampleUserAgent1; got != want {
			t.Errorf("mismatched User-Agent (non-nil hdr): got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(UserAgent(exampleUserAgent1))
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})
		hdr := http.Header{}
		hdr.Set("User-Agent", "")

		c.Request("GET", testBaseURL+"/user_agent", nil, nil, hdr)
		if got, want := receivedUserAgent, ""; got != want {
			t.Errorf("mismatched User-Agent (hdr with empty UA): got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(UserAgent(exampleUserAgent1))
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedUserAgent = string(resp.Body)
		})
		hdr := http.Header{}
		hdr.Set("User-Agent", exampleUserAgent2)

		c.Request("GET", testBaseURL+"/user_agent", nil, nil, hdr)
		if got, want := receivedUserAgent, exampleUserAgent2; got != want {
			t.Errorf("mismatched User-Agent (hdr with UA): got=%q want=%q", got, want)
		}
	}()
}

func TestHeaders(t *testing.T) {
	const exampleHostHeader = "example.com"
	const exampleTestHeader = "Testing"

	mock := setupMockTransport()

	var receivedHeader string

	func() {
		c := NewCollector(
			Headers(map[string]string{"Host": exampleHostHeader}),
		)
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/host_header")
		if got, want := receivedHeader, exampleHostHeader; got != want {
			t.Errorf("mismatched Host header: got=%q want=%q", got, want)
		}
	}()
	func() {
		c := NewCollector(
			Headers(map[string]string{"Test": exampleTestHeader}),
		)
		c.SetClient(&http.Client{Transport: mock})
		c.OnResponse(func(resp *Response) {
			receivedHeader = string(resp.Body)
		})
		c.Visit(testBaseURL + "/custom_header")
		if got, want := receivedHeader, exampleTestHeader; got != want {
			t.Errorf("mismatched custom header: got=%q want=%q", got, want)
		}
	}()
}

func TestParseHTTPErrorResponse(t *testing.T) {
	contentCount := 0
	mock := setupMockTransport()

	c := NewCollector(
		AllowURLRevisit(),
	)
	c.SetClient(&http.Client{Transport: mock})

	c.OnHTML("p", func(e *HTMLElement) {
		if e.Text == "error" {
			contentCount++
		}
	})

	c.Visit(testBaseURL + "/500")

	if contentCount != 0 {
		t.Fatal("Content is parsed without ParseHTTPErrorResponse enabled")
	}

	c.ParseHTTPErrorResponse = true

	c.Visit(testBaseURL + "/500")

	if contentCount != 1 {
		t.Fatal("Content isn't parsed with ParseHTTPErrorResponse enabled")
	}

}

func TestHTMLElement(t *testing.T) {
	ctx := &Context{}
	resp := &Response{
		Request: &Request{
			Ctx: ctx,
		},
		Ctx: ctx,
	}

	in := `<a href="http://go-bluesnake.org">Colly</a>`
	sel := "a[href]"
	doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer([]byte(in)))
	if err != nil {
		t.Fatal(err)
	}
	elements := []*HTMLElement{}
	i := 0
	doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
		for _, n := range s.Nodes {
			elements = append(elements, NewHTMLElementFromSelectionNode(resp, s, n, i))
			i++
		}
	})
	elementsLen := len(elements)
	if elementsLen != 1 {
		t.Errorf("element length mismatch. got %d, expected %d.\n", elementsLen, 1)
	}
	v := elements[0]
	if v.Name != "a" {
		t.Errorf("element tag mismatch. got %s, expected %s.\n", v.Name, "a")
	}
	if v.Text != "Colly" {
		t.Errorf("element content mismatch. got %s, expected %s.\n", v.Text, "Colly")
	}
	if v.Attr("href") != "http://go-bluesnake.org" {
		t.Errorf("element href mismatch. got %s, expected %s.\n", v.Attr("href"), "http://go-bluesnake.org")
	}
}

func TestCollectorOnXMLWithHtml(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	titleCallbackCalled := false
	paragraphCallbackCount := 0

	c.OnXML("/html/head/title", func(e *XMLElement) {
		titleCallbackCalled = true
		if e.Text != "Test Page" {
			t.Error("Title element text does not match, got", e.Text)
		}
	})

	c.OnXML("/html/body/p", func(e *XMLElement) {
		paragraphCallbackCount++
		if e.Attr("class") != "description" {
			t.Error("Failed to get paragraph's class attribute")
		}
	})

	c.OnXML("/html/body", func(e *XMLElement) {
		if e.ChildAttr("p", "class") != "description" {
			t.Error("Invalid class value")
		}
		classes := e.ChildAttrs("p", "class")
		if len(classes) != 2 {
			t.Error("Invalid class values")
		}
	})

	c.Visit(testBaseURL + "/html")

	if !titleCallbackCalled {
		t.Error("Failed to call OnXML callback for <title> tag")
	}

	if paragraphCallbackCount != 2 {
		t.Error("Failed to find all <p> tags")
	}
}

func TestCollectorOnXMLWithXML(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	titleCallbackCalled := false
	paragraphCallbackCount := 0

	c.OnXML("//page/title", func(e *XMLElement) {
		titleCallbackCalled = true
		if e.Text != "Test Page" {
			t.Error("Title element text does not match, got", e.Text)
		}
	})

	c.OnXML("//page/paragraph", func(e *XMLElement) {
		paragraphCallbackCount++
		if e.Attr("type") != "description" {
			t.Error("Failed to get paragraph's type attribute")
		}
	})

	c.OnXML("/page", func(e *XMLElement) {
		if e.ChildAttr("paragraph", "type") != "description" {
			t.Error("Invalid type value")
		}
		classes := e.ChildAttrs("paragraph", "type")
		if len(classes) != 2 {
			t.Error("Invalid type values")
		}
	})

	c.Visit(testBaseURL + "/xml")

	if !titleCallbackCalled {
		t.Error("Failed to call OnXML callback for <title> tag")
	}

	if paragraphCallbackCount != 2 {
		t.Error("Failed to find all <paragraph> tags")
	}
}

func TestCollectorDepth(t *testing.T) {
	mock := setupMockTransport()
	maxDepth := 2
	c1 := NewCollector(
		MaxDepth(maxDepth),
		AllowURLRevisit(),
	)
	c1.SetClient(&http.Client{Transport: mock})
	requestCount := 0
	c1.OnResponse(func(resp *Response) {
		requestCount++
		if requestCount >= 10 {
			return
		}
		c1.Visit(testBaseURL)
	})
	c1.Visit(testBaseURL)
	if requestCount < 10 {
		t.Errorf("Invalid number of requests: %d (expected 10) without using MaxDepth", requestCount)
	}

	c2 := c1.Clone()
	requestCount = 0
	c2.OnResponse(func(resp *Response) {
		requestCount++
		resp.Request.Visit(testBaseURL)
	})
	c2.Visit(testBaseURL)
	if requestCount != 2 {
		t.Errorf("Invalid number of requests: %d (expected 2) with using MaxDepth 2", requestCount)
	}

	c1.Visit(testBaseURL)
	if requestCount < 10 {
		t.Errorf("Invalid number of requests: %d (expected 10) without using MaxDepth again", requestCount)
	}

	requestCount = 0
	c2.Visit(testBaseURL)
	if requestCount != 2 {
		t.Errorf("Invalid number of requests: %d (expected 2) with using MaxDepth 2 again", requestCount)
	}
}

func TestCollectorRequests(t *testing.T) {
	mock := setupMockTransport()
	maxRequests := uint32(5)
	c1 := NewCollector(
		MaxRequests(maxRequests),
		AllowURLRevisit(),
	)
	c1.SetClient(&http.Client{Transport: mock})
	requestCount := 0
	c1.OnResponse(func(resp *Response) {
		requestCount++
		c1.Visit(testBaseURL)
	})
	c1.Visit(testBaseURL)
	if requestCount != 5 {
		t.Errorf("Invalid number of requests: %d (expected 5) with MaxRequests", requestCount)
	}
}

func BenchmarkOnHTML(b *testing.B) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("p", func(_ *HTMLElement) {})

	for n := 0; n < b.N; n++ {
		c.Visit(fmt.Sprintf("%s/html?q=%d", testBaseURL, n))
	}
}

func BenchmarkOnXML(b *testing.B) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.OnXML("//p", func(_ *XMLElement) {})

	for n := 0; n < b.N; n++ {
		c.Visit(fmt.Sprintf("%s/html?q=%d", testBaseURL, n))
	}
}

func BenchmarkOnResponse(b *testing.B) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.AllowURLRevisit = true
	c.OnResponse(func(_ *Response) {})

	for n := 0; n < b.N; n++ {
		c.Visit(testBaseURL)
	}
}

func TestCallbackDetachment(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	c.AllowURLRevisit = true

	var executions [3]int // tracks number of executions of each callback

	c.OnHTML("#firstElem", func(e *HTMLElement) {
		executions[0]++
		// Detach this callback after first execution
		c.OnHTMLDetach("#firstElem")
	})
	c.OnHTML("#secondElem", func(e *HTMLElement) {
		executions[1]++
	})
	c.OnHTML("#thirdElem", func(e *HTMLElement) {
		executions[2]++
	})

	// First visit - all callbacks should execute
	c.Visit(testBaseURL + "/callback_test")
	// Second visit - first callback should NOT execute
	c.Visit(testBaseURL + "/callback_test")

	// Verify callback counts
	if executions[0] != 1 {
		t.Errorf("firstElem callback executed %d times, expected 1", executions[0])
	}
	if executions[1] != 2 {
		t.Errorf("secondElem callback executed %d times, expected 2", executions[1])
	}
	if executions[2] != 2 {
		t.Errorf("thirdElem callback executed %d times, expected 2", executions[2])
	}
}

func TestCollectorPostRetry(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})
	try := false
	c.OnResponse(func(r *Response) {
		if r.Ctx.Get("notFirst") == "" {
			r.Ctx.Put("notFirst", "first")
			_ = r.Request.Retry()
			return
		}
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}
		try = true
	})

	c.Post(testBaseURL+"/login", map[string]string{
		"name": postValue,
	})
	if !try {
		t.Error("OnResponse Retry was not called")
	}
}
func TestCollectorGetRetry(t *testing.T) {
	mock := setupMockTransport()
	try := false

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if r.Ctx.Get("notFirst") == "" {
			r.Ctx.Put("notFirst", "first")
			_ = r.Request.Retry()
			return
		}
		if !bytes.Equal(r.Body, serverIndexResponse) {
			t.Error("Response body does not match with the original content")
		}
		try = true
	})

	c.Visit(testBaseURL)
	if !try {
		t.Error("OnResponse Retry was not called")
	}
}

func TestCollectorPostRetryUnseekable(t *testing.T) {
	mock := setupMockTransport()
	try := false
	postValue := "hello"
	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}

		if r.Ctx.Get("notFirst") == "" {
			r.Ctx.Put("notFirst", "first")
			err := r.Request.Retry()
			if !errors.Is(err, ErrRetryBodyUnseekable) {
				t.Errorf("Unexpected error Type ErrRetryBodyUnseekable : %v", err)
			}
			return
		}
		try = true
	})
	c.Request("POST", testBaseURL+"/login", bytes.NewBuffer([]byte("name="+postValue)), nil, nil)
	if try {
		t.Error("OnResponse Retry was called but BodyUnseekable")
	}
}

func TestCheckRequestHeadersFunc(t *testing.T) {
	mock := setupMockTransport()
	try := false

	c := NewCollector()
	c.SetClient(&http.Client{Transport: mock})

	c.OnRequestHeaders(func(r *Request) {
		try = true
		r.Abort()
	})
	c.OnScraped(func(r *Response) {
		try = false
	})
	c.Visit(testBaseURL)
	if try == false {
		t.Error("TestCheckRequestHeadersFunc failed")
	}
}
