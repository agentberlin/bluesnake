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
	"net/http"
	"testing"
)

func TestNoAcceptHeader(t *testing.T) {
	mock := setupMockTransport()

	var receivedHeader string
	// checks if Accept is enabled by default
	func() {
		c := NewCollector(nil)
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
		c := NewCollector(nil)
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
