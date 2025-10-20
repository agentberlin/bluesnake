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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/testutil"
)

// TestSetCookieRedirect tests cookie handling with redirects
func TestSetCookieRedirect(t *testing.T) {
	type middleware = func(http.Handler) http.Handler
	for _, m := range []middleware{
		testutil.RequireSessionCookieSimple,
		testutil.RequireSessionCookieAuthPage,
	} {
		t.Run("", func(t *testing.T) {
			ts := testutil.NewUnstartedTestServer()
			ts.Config.Handler = m(ts.Config.Handler)
			ts.Start()
			defer ts.Close()
			c := NewCollector(context.Background(), nil)
			c.OnResponse(func(r *Response) {
				if got, want := r.Body, testutil.ServerIndexResponse; !bytes.Equal(got, want) {
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
	ts := testutil.NewTestServer()
	defer ts.Close()

	c := NewCollector(context.Background(), nil)
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

// TestOnRedirect tests the OnRedirect callback functionality
func TestOnRedirect(t *testing.T) {
	ts := testutil.NewTestServer()
	defer ts.Close()

	t.Run("callback is called with correct parameters", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		callbackCalled := false
		var capturedURL string
		var viaChainLength int

		c.OnRedirect(func(req *http.Request, via []*http.Request) error {
			callbackCalled = true
			capturedURL = req.URL.String()
			viaChainLength = len(via)
			return nil // allow redirect
		})

		c.OnResponse(func(r *Response) {
			if !strings.HasSuffix(r.Request.URL.String(), "/redirected/") {
				t.Error("Redirect should have been followed")
			}
		})

		if err := c.Visit(ts.URL + "/redirect"); err != nil {
			t.Fatal(err)
		}

		if !callbackCalled {
			t.Error("OnRedirect callback was not called")
		}
		if !strings.HasSuffix(capturedURL, "/redirected/") {
			t.Errorf("OnRedirect received wrong URL: %s", capturedURL)
		}
		if viaChainLength != 1 {
			t.Errorf("Expected via chain length 1, got %d", viaChainLength)
		}
	})

	t.Run("returning error blocks redirect", func(t *testing.T) {
		c := NewCollector(context.Background(), nil)

		c.OnRedirect(func(req *http.Request, via []*http.Request) error {
			// Block redirect to /redirected/
			if strings.HasSuffix(req.URL.String(), "/redirected/") {
				return fmt.Errorf("redirect blocked by callback")
			}
			return nil
		})

		errorReceived := false
		c.OnError(func(r *Response, err error) {
			errorReceived = true
			if !strings.Contains(err.Error(), "redirect blocked by callback") {
				t.Errorf("Expected redirect blocked error, got: %v", err)
			}
		})

		c.Visit(ts.URL + "/redirect")

		if !errorReceived {
			t.Error("Expected OnError to be called when redirect is blocked")
		}
	})
}

// TestCollectorCookies tests cookie persistence across requests
func TestCollectorCookies(t *testing.T) {
	ts := testutil.NewTestServer()
	defer ts.Close()

	c := NewCollector(context.Background(), nil)

	if err := c.Visit(ts.URL + "/set_cookie"); err != nil {
		t.Fatal(err)
	}

	if err := c.Visit(ts.URL + "/check_cookie"); err != nil {
		t.Fatalf("Failed to use previously set cookies: %s", err)
	}
}

// TestConnectionErrorOnRobotsTxtResultsInError tests error handling when robots.txt connection fails
func TestConnectionErrorOnRobotsTxtResultsInError(t *testing.T) {
	ts := testutil.NewTestServer()
	ts.Close() // immediately close the server to force a connection error

	c := NewCollector(context.Background(), nil)
	c.IgnoreRobotsTxt = false
	err := c.Visit(ts.URL)

	if err == nil {
		t.Fatal("Error expected")
	}
}

// TestCollectorVisitWithTrace tests HTTP trace support
func TestCollectorVisitWithTrace(t *testing.T) {
	ts := testutil.NewTestServer()
	defer ts.Close()

	c := NewCollector(context.Background(), &HTTPConfig{TraceHTTP: true})
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
	ts := testutil.NewTestServer()
	defer ts.Close()

	c := NewCollector(context.Background(), &HTTPConfig{CheckHead: true})
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

	ts := testutil.NewTestServer()
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	c := NewCollector(ctx, nil)

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
	ts := testutil.NewTestServer()
	defer ts.Close()
	c := NewCollector(context.Background(), nil)
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
