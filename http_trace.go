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
	"net/http"
	"net/http/httptrace"
	"time"
)

// HTTPTrace provides a datastructure for storing an http trace.
type HTTPTrace struct {
	start, connect    time.Time
	ConnectDuration   time.Duration
	FirstByteDuration time.Duration
}

// trace returns a httptrace.ClientTrace object to be used with an http
// request via httptrace.WithClientTrace() that fills in the HttpTrace.
func (ht *HTTPTrace) trace() *httptrace.ClientTrace {
	trace := &httptrace.ClientTrace{
		ConnectStart: func(network, addr string) { ht.connect = time.Now() },
		ConnectDone: func(network, addr string, err error) {
			ht.ConnectDuration = time.Since(ht.connect)
		},

		GetConn: func(hostPort string) { ht.start = time.Now() },
		GotFirstResponseByte: func() {
			ht.FirstByteDuration = time.Since(ht.start)
		},
	}
	return trace
}

// WithTrace returns the given HTTP Request with this HTTPTrace added to its
// context.
func (ht *HTTPTrace) WithTrace(req *http.Request) *http.Request {
	return req.WithContext(httptrace.WithClientTrace(req.Context(), ht.trace()))
}
