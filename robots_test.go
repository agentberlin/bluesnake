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
	"testing"
)

func TestRobotsWhenAllowed(t *testing.T) {
	mock := setupMockTransport()

	c := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
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

	c := NewCollector(context.Background(), nil)
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
