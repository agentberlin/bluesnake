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
	"os"
	"testing"
)

func TestEnvSettings(t *testing.T) {
	mock := setupMockTransport()

	os.Setenv("BLUESNAKE_USER_AGENT", "test")
	defer os.Unsetenv("BLUESNAKE_USER_AGENT")

	c := NewCollector(context.Background(), nil)
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
	const defaultUserAgent = "bluesnake/1.0 (+https://snake.blue)"

	mock := setupMockTransport()

	var receivedUserAgent string

	func() {
		c := NewCollector(context.Background(), nil)
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
		c := NewCollector(context.Background(), &CollectorConfig{UserAgent: exampleUserAgent1})
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
		c := NewCollector(context.Background(), &CollectorConfig{UserAgent: exampleUserAgent1})
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
		c := NewCollector(context.Background(), &CollectorConfig{UserAgent: exampleUserAgent1})
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
		c := NewCollector(context.Background(), &CollectorConfig{UserAgent: exampleUserAgent1})
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
		c := NewCollector(context.Background(), &CollectorConfig{UserAgent: exampleUserAgent1})
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
		c := NewCollector(context.Background(), &CollectorConfig{Headers: map[string]string{"Host": exampleHostHeader}})
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
		c := NewCollector(context.Background(), &CollectorConfig{Headers: map[string]string{"Test": exampleTestHeader}})
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
