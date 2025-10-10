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

// Package bluesnake provides tests that require a real HTTP server.
// These tests verify features like HTTP redirects, cookies, connection errors,
// trace support, CheckHead functionality, and context timeout handling that
// cannot be easily mocked.

package bluesnake

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newUnstartedTestServer creates an unstarted HTTP test server with all endpoints configured
func newUnstartedTestServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(serverIndexResponse)
	})

	mux.HandleFunc("/callback_test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		w.Write(callbackTestHTML)
	})

	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("no-content-type") != "" {
			w.Header()["Content-Type"] = nil
		} else {
			w.Header().Set("Content-Type", "text/html")
		}
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
</head>
<body>
<h1>Hello World</h1>
<p class="description">This is a test page</p>
<p class="description">This is a test paragraph</p>
</body>
</html>
		`))
	})

	mux.HandleFunc("/xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<page>
	<title>Test Page</title>
	<paragraph type="description">This is a test page</paragraph>
	<paragraph type="description">This is a test paragraph</paragraph>
</page>
		`))
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(r.FormValue("name")))
		}
	})

	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(robotsFile))
	})

	mux.HandleFunc("/allowed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("allowed"))
	})

	mux.HandleFunc("/disallowed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("disallowed"))
	})

	mux.Handle("/redirect", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destination := "/redirected/"
		if d := r.URL.Query().Get("d"); d != "" {
			destination = d
		}
		http.Redirect(w, r, destination, http.StatusSeeOther)

	}))

	mux.Handle("/redirected/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<a href="test">test</a>`)
	}))

	mux.HandleFunc("/set_cookie", func(w http.ResponseWriter, r *http.Request) {
		c := &http.Cookie{Name: "test", Value: "testv", HttpOnly: false}
		http.SetCookie(w, c)
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/check_cookie", func(w http.ResponseWriter, r *http.Request) {
		cs := r.Cookies()
		if len(cs) != 1 || r.Cookies()[0].Value != "testv" {
			w.WriteHeader(500)
			w.Write([]byte("nok"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/500", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(500)
		w.Write([]byte("<p>error</p>"))
	})

	mux.HandleFunc("/user_agent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(r.Header.Get("User-Agent")))
	})

	mux.HandleFunc("/host_header", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(r.Host))
	})

	mux.HandleFunc("/accept_header", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(r.Header.Get("Accept")))
	})

	mux.HandleFunc("/custom_header", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(r.Header.Get("Test")))
	})

	mux.HandleFunc("/base", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="http://xy.com/" />
</head>
<body>
<a href="z">link</a>
</body>
</html>
		`))
	})

	mux.HandleFunc("/base_relative", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="/foobar/" />
</head>
<body>
<a href="z">link</a>
</body>
</html>
		`))
	})

	mux.HandleFunc("/tabs_and_newlines", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
<base href="/foo	bar/" />
</head>
<body>
<a href="x
y">link</a>
</body>
</html>
		`))
	})

	mux.HandleFunc("/foobar/xy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<title>Test Page</title>
</head>
<body>
<p>hello</p>
</body>
</html>
		`))
	})

	mux.HandleFunc("/100%25", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("100 percent"))
	})

	mux.HandleFunc("/large_binary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		ww := bufio.NewWriter(w)
		defer ww.Flush()
		for {
			// have to check error to detect client aborting download
			if _, err := ww.Write([]byte{0x41}); err != nil {
				return
			}
		}
	})

	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		i := 0

		for {
			select {
			case <-r.Context().Done():
				return
			case t := <-ticker.C:
				fmt.Fprintf(w, "%s\n", t)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				i++
				if i == 10 {
					return
				}
			}
		}
	})

	return httptest.NewUnstartedServer(mux)
}

// newTestServer creates and starts a new HTTP test server
func newTestServer() *httptest.Server {
	srv := newUnstartedTestServer()
	srv.Start()
	return srv
}

// requireSessionCookieSimple is middleware that requires a session cookie,
// redirecting to set it if not present
func requireSessionCookieSimple(handler http.Handler) http.Handler {
	const cookieName = "session_id"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(cookieName); err == http.ErrNoCookie {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "1"})
			http.Redirect(w, r, r.RequestURI, http.StatusFound)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// requireSessionCookieAuthPage is middleware that requires a session cookie,
// redirecting through an auth page to set it if not present
func requireSessionCookieAuthPage(handler http.Handler) http.Handler {
	const setCookiePath = "/auth"
	const cookieName = "session_id"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == setCookiePath {
			destination := r.URL.Query().Get("return")
			http.Redirect(w, r, destination, http.StatusFound)
			return
		}
		if _, err := r.Cookie(cookieName); err == http.ErrNoCookie {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "1"})
			http.Redirect(w, r, setCookiePath+"?return="+url.QueryEscape(r.RequestURI), http.StatusFound)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// TestCollectorURLRevisitCheck tests URL revisit tracking with redirects
func TestCollectorURLRevisitCheck(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(nil)

	visited, err := c.HasVisited(ts.URL)

	if err != nil {
		t.Error(err.Error())
	}

	if visited != false {
		t.Error("Expected URL to NOT have been visited")
	}

	c.Visit(ts.URL)

	visited, err = c.HasVisited(ts.URL)

	if err != nil {
		t.Error(err.Error())
	}

	if visited != true {
		t.Error("Expected URL to have been visited")
	}

	errorTestCases := []struct {
		Path             string
		DestinationError string
	}{
		{"/", "/"},
		{"/redirect?d=/", "/"},
		// now that /redirect?d=/ itself is recorded as visited,
		// it's now returned in error
		{"/redirect?d=/", "/redirect?d=/"},
		{"/redirect?d=/redirect%3Fd%3D/", "/redirect?d=/"},
		{"/redirect?d=/redirect%3Fd%3D/", "/redirect?d=/redirect%3Fd%3D/"},
		{"/redirect?d=/redirect%3Fd%3D/&foo=bar", "/redirect?d=/"},
	}

	for i, testCase := range errorTestCases {
		err := c.Visit(ts.URL + testCase.Path)
		if testCase.DestinationError == "" {
			if err != nil {
				t.Errorf("got unexpected error in test %d: %q", i, err)
			}
		} else {
			var ave *AlreadyVisitedError
			if !errors.As(err, &ave) {
				t.Errorf("err=%q returned when trying to revisit, expected AlreadyVisitedError", err)
			} else {
				if got, want := ave.Destination.String(), ts.URL+testCase.DestinationError; got != want {
					t.Errorf("wrong destination in AlreadyVisitedError in test %d, got=%q want=%q", i, got, want)
				}
			}
		}
	}
}

// TestSetCookieRedirect tests cookie handling with redirects
func TestSetCookieRedirect(t *testing.T) {
	type middleware = func(http.Handler) http.Handler
	for _, m := range []middleware{
		requireSessionCookieSimple,
		requireSessionCookieAuthPage,
	} {
		t.Run("", func(t *testing.T) {
			ts := newUnstartedTestServer()
			ts.Config.Handler = m(ts.Config.Handler)
			ts.Start()
			defer ts.Close()
			c := NewCollector(nil)
			c.OnResponse(func(r *Response) {
				if got, want := r.Body, serverIndexResponse; !bytes.Equal(got, want) {
					t.Errorf("bad response body got=%q want=%q", got, want)
				}
				if got, want := r.StatusCode, http.StatusOK; got != want {
					t.Errorf("bad response code got=%d want=%d", got, want)
				}
			})
			if err := c.Visit(ts.URL); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRedirect tests basic HTTP redirect handling
func TestRedirect(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(nil)
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		if !strings.HasSuffix(u, "/redirected/test") {
			t.Error("Invalid URL after redirect: " + u)
		}
	})

	c.OnResponseHeaders(func(r *Response) {
		if !strings.HasSuffix(r.Request.URL.String(), "/redirected/") {
			t.Error("Invalid URL in Request after redirect (OnResponseHeaders): " + r.Request.URL.String())
		}
	})

	c.OnResponse(func(r *Response) {
		if !strings.HasSuffix(r.Request.URL.String(), "/redirected/") {
			t.Error("Invalid URL in Request after redirect (OnResponse): " + r.Request.URL.String())
		}
	})
	c.Visit(ts.URL + "/redirect")
}

// TestRedirectWithDisallowedURLs tests redirect handling with URL filtering
func TestRedirectWithDisallowedURLs(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(nil)
	c.DisallowedURLFilters = []*regexp.Regexp{regexp.MustCompile(ts.URL + "/redirected/test")}
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		err := c.Visit(u)
		if !errors.Is(err, ErrForbiddenURL) {
			t.Error("URL should have been forbidden: " + u)
		}
	})

	c.Visit(ts.URL + "/redirect")
}

// TestCollectorCookies tests cookie persistence across requests
func TestCollectorCookies(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(nil)

	if err := c.Visit(ts.URL + "/set_cookie"); err != nil {
		t.Fatal(err)
	}

	if err := c.Visit(ts.URL + "/check_cookie"); err != nil {
		t.Fatalf("Failed to use previously set cookies: %s", err)
	}
}

// TestConnectionErrorOnRobotsTxtResultsInError tests error handling when robots.txt connection fails
func TestConnectionErrorOnRobotsTxtResultsInError(t *testing.T) {
	ts := newTestServer()
	ts.Close() // immediately close the server to force a connection error

	c := NewCollector(nil)
	c.IgnoreRobotsTxt = false
	err := c.Visit(ts.URL)

	if err == nil {
		t.Fatal("Error expected")
	}
}

// TestCollectorVisitWithTrace tests HTTP trace support
func TestCollectorVisitWithTrace(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(&CollectorConfig{AllowedDomains: []string{"localhost", "127.0.0.1", "::1"}, TraceHTTP: true})
	c.OnResponse(func(resp *Response) {
		if resp.Trace == nil {
			t.Error("Failed to initialize trace")
		}
	})

	err := c.Visit(ts.URL)
	if err != nil {
		t.Errorf("Failed to visit url %s", ts.URL)
	}
}

// TestCollectorVisitWithCheckHead tests CheckHead functionality
func TestCollectorVisitWithCheckHead(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	c := NewCollector(&CollectorConfig{CheckHead: true})
	var requestMethodChain []string
	c.OnResponse(func(resp *Response) {
		requestMethodChain = append(requestMethodChain, resp.Request.Method)
	})

	err := c.Visit(ts.URL)
	if err != nil {
		t.Errorf("Failed to visit url %s", ts.URL)
	}
	if requestMethodChain[0] != "HEAD" && requestMethodChain[1] != "GET" {
		t.Errorf("Failed to perform a HEAD request before GET")
	}
}

// TestCollectorContext tests context timeout handling
func TestCollectorContext(t *testing.T) {
	// "/slow" takes 1 second to return the response.
	// If context does abort the transfer after 0.5 seconds as it should,
	// OnError will be called, and the test is passed. Otherwise, test is failed.

	ts := newTestServer()
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	c := NewCollector(&CollectorConfig{Context: ctx})

	onErrorCalled := false

	c.OnResponse(func(resp *Response) {
		t.Error("OnResponse was called, expected OnError")
	})

	c.OnError(func(resp *Response, err error) {
		onErrorCalled = true
		if err != context.DeadlineExceeded {
			t.Errorf("OnError got err=%#v, expected context.DeadlineExceeded", err)
		}
	})

	err := c.Visit(ts.URL + "/slow")
	if err != context.DeadlineExceeded {
		t.Errorf("Visit return err=%#v, expected context.DeadlineExceeded", err)
	}

	if !onErrorCalled {
		t.Error("OnError was not called")
	}

}

// TestRedirectErrorRetry tests retry behavior on redirect errors
func TestRedirectErrorRetry(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	c := NewCollector(nil)
	c.OnError(func(r *Response, err error) {
		if r.Ctx.Get("notFirst") == "" {
			r.Ctx.Put("notFirst", "first")
			_ = r.Request.Retry()
			return
		}
		if e := (&AlreadyVisitedError{}); errors.As(err, &e) {
			t.Error("loop AlreadyVisitedError")
		}

	})
	c.OnResponse(func(response *Response) {
		//println(1)
	})
	c.Visit(ts.URL + "/redirected/")
	c.Visit(ts.URL + "/redirect")
}
