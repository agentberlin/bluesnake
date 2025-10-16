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
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestCollectorVisit(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(nil)
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

func TestCollectorVisitResponseHeaders(t *testing.T) {
	mock := setupMockTransport()

	var onResponseHeadersCalled bool

	c := NewCollector(nil)
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

	c := NewCollector(nil)
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

	c := NewCollector(nil)
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

func TestCollectorPost(t *testing.T) {
	mock := setupMockTransport()

	postValue := "hello"
	c := NewCollector(nil)
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
	c := NewCollector(nil)
	c.SetClient(&http.Client{Transport: mock})

	c.OnResponse(func(r *Response) {
		if postValue != string(r.Body) {
			t.Error("Failed to send data with POST")
		}
	})

	c.PostRaw(testBaseURL+"/login", []byte("name="+postValue))
}

func TestIssue594(t *testing.T) {
	// This is a regression test for a data race bug. There's no
	// assertions because it's meant to be used with race detector
	mock := setupMockTransport()

	c := NewCollector(nil)
	// if timeout is set, this bug is not triggered
	client := &http.Client{Timeout: 0 * time.Second, Transport: mock}
	c.SetClient(client)

	c.Visit(testBaseURL)
}

func TestParseHTTPErrorResponse(t *testing.T) {
	contentCount := 0
	mock := setupMockTransport()

	c := NewCollector(&CollectorConfig{AllowURLRevisit: true})
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

	in := `<a href="http://go-bluesnake.org">BlueSnake</a>`
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
	if v.Text != "BlueSnake" {
		t.Errorf("element content mismatch. got %s, expected %s.\n", v.Text, "BlueSnake")
	}
	if v.Attr("href") != "http://go-bluesnake.org" {
		t.Errorf("element href mismatch. got %s, expected %s.\n", v.Attr("href"), "http://go-bluesnake.org")
	}
}

func TestCollectorOnXMLWithHtml(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(nil)
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

	c := NewCollector(nil)
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
	c1 := NewCollector(&CollectorConfig{MaxDepth: maxDepth, AllowURLRevisit: true})
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
	c1 := NewCollector(&CollectorConfig{MaxRequests: maxRequests, AllowURLRevisit: true})
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

	c := NewCollector(nil)
	c.SetClient(&http.Client{Transport: mock})
	c.OnHTML("p", func(_ *HTMLElement) {})

	for n := 0; n < b.N; n++ {
		c.Visit(fmt.Sprintf("%s/html?q=%d", testBaseURL, n))
	}
}

func BenchmarkOnXML(b *testing.B) {
	mock := setupMockTransport()

	c := NewCollector(nil)
	c.SetClient(&http.Client{Transport: mock})
	c.OnXML("//p", func(_ *XMLElement) {})

	for n := 0; n < b.N; n++ {
		c.Visit(fmt.Sprintf("%s/html?q=%d", testBaseURL, n))
	}
}

func BenchmarkOnResponse(b *testing.B) {
	mock := setupMockTransport()

	c := NewCollector(nil)
	c.SetClient(&http.Client{Transport: mock})
	c.AllowURLRevisit = true
	c.OnResponse(func(_ *Response) {})

	for n := 0; n < b.N; n++ {
		c.Visit(testBaseURL)
	}
}

func TestCallbackDetachment(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(nil)
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
	c := NewCollector(nil)
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

	c := NewCollector(nil)
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
	c := NewCollector(nil)
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

	c := NewCollector(nil)
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
