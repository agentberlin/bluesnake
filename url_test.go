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
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestBaseTag(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(context.Background(), nil)
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		if u != "http://xy.com/z" {
			t.Error("Invalid <base /> tag handling in OnHTML: expected https://xy.com/z, got " + u)
		}
	})
	c.Visit(testBaseURL + "/base")

	c2 := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("a[href]", func(e *HTMLElement) {
		u := e.Request.AbsoluteURL(e.Attr("href"))
		expected := testBaseURL + "/foobar/z"
		if u != expected {
			t.Errorf("Invalid <base /> tag handling in OnHTML: expected %q, got %q", expected, u)
		}
	})
	c.Visit(testBaseURL + "/base_relative")

	c2 := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
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
