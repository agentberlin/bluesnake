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
	"compress/gzip"
	"crypto/sha1"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
)

type httpBackend struct {
	LimitRules []*LimitRule
	Client     *http.Client
	lock       *sync.RWMutex
}

type checkResponseHeadersFunc func(req *http.Request, statusCode int, header http.Header) bool
type checkRequestHeadersFunc func(req *http.Request) bool

// LimitRule provides connection restrictions for domains.
// Both DomainRegexp and DomainGlob can be used to specify
// the included domains patterns, but at least one is required.
// There can be two kind of limitations:
//   - Parallelism: Set limit for the number of concurrent requests to matching domains
//   - Delay: Wait specified amount of time between requests (parallelism is 1 in this case)
type LimitRule struct {
	// DomainRegexp is a regular expression to match against domains
	DomainRegexp string
	// DomainGlob is a glob pattern to match against domains
	DomainGlob string
	// Delay is the duration to wait before creating a new request to the matching domains
	Delay time.Duration
	// RandomDelay is the extra randomized duration to wait added to Delay before creating a new request
	RandomDelay time.Duration
	// Parallelism is the number of the maximum allowed concurrent requests of the matching domains
	Parallelism    int
	waitChan       chan bool
	compiledRegexp *regexp.Regexp
	compiledGlob   glob.Glob
}

// Init initializes the private members of LimitRule
func (r *LimitRule) Init() error {
	waitChanSize := 1
	if r.Parallelism > 1 {
		waitChanSize = r.Parallelism
	}
	r.waitChan = make(chan bool, waitChanSize)
	hasPattern := false
	if r.DomainRegexp != "" {
		c, err := regexp.Compile(r.DomainRegexp)
		if err != nil {
			return err
		}
		r.compiledRegexp = c
		hasPattern = true
	}
	if r.DomainGlob != "" {
		c, err := glob.Compile(r.DomainGlob)
		if err != nil {
			return err
		}
		r.compiledGlob = c
		hasPattern = true
	}
	if !hasPattern {
		return ErrNoPattern
	}
	return nil
}

func (h *httpBackend) Init(jar http.CookieJar) {
	rand.Seed(time.Now().UnixNano())
	h.Client = &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
	}
	h.lock = &sync.RWMutex{}
}

// Match checks that the domain parameter triggers the rule
func (r *LimitRule) Match(domain string) bool {
	match := false
	if r.compiledRegexp != nil && r.compiledRegexp.MatchString(domain) {
		match = true
	}
	if r.compiledGlob != nil && r.compiledGlob.Match(domain) {
		match = true
	}
	return match
}

func (h *httpBackend) GetMatchingRule(domain string) *LimitRule {
	if h.LimitRules == nil {
		return nil
	}
	h.lock.RLock()
	defer h.lock.RUnlock()
	for _, r := range h.LimitRules {
		if r.Match(domain) {
			return r
		}
	}
	return nil
}

func (h *httpBackend) Cache(request *http.Request, bodySize int, checkRequestHeadersFunc checkRequestHeadersFunc, checkResponseHeadersFunc checkResponseHeadersFunc, cacheDir string, cacheExpiration time.Duration) (*Response, error) {
	if cacheDir == "" || request.Method != "GET" || request.Header.Get("Cache-Control") == "no-cache" {
		return h.Do(request, bodySize, checkRequestHeadersFunc, checkResponseHeadersFunc)
	}
	sum := sha1.Sum([]byte(request.URL.String()))
	hash := hex.EncodeToString(sum[:])
	dir := path.Join(cacheDir, hash[:2])
	filename := path.Join(dir, hash)

	if fileInfo, err := os.Stat(filename); err == nil && cacheExpiration > 0 {
		if time.Since(fileInfo.ModTime()) > cacheExpiration {
			_ = os.Remove(filename)
		}
	}

	if file, err := os.Open(filename); err == nil {
		resp := new(Response)
		err := gob.NewDecoder(file).Decode(resp)
		file.Close()
		if resp.Headers != nil {
			checkResponseHeadersFunc(request, resp.StatusCode, *resp.Headers)
		}
		if resp.StatusCode < 500 {
			return resp, err
		}
	}
	resp, err := h.Do(request, bodySize, checkRequestHeadersFunc, checkResponseHeadersFunc)
	if err != nil || resp.StatusCode >= 500 {
		return resp, err
	}
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return resp, err
		}
	}
	file, err := os.Create(filename + "~")
	if err != nil {
		return resp, err
	}
	if err := gob.NewEncoder(file).Encode(resp); err != nil {
		file.Close()
		return resp, err
	}
	file.Close()
	return resp, os.Rename(filename+"~", filename)
}

func (h *httpBackend) Do(request *http.Request, bodySize int, checkRequestHeadersFunc checkRequestHeadersFunc, checkResponseHeadersFunc checkResponseHeadersFunc) (*Response, error) {
	r := h.GetMatchingRule(request.URL.Host)
	if r != nil {
		r.waitChan <- true
		defer func(r *LimitRule) {
			randomDelay := time.Duration(0)
			if r.RandomDelay != 0 {
				randomDelay = time.Duration(rand.Int63n(int64(r.RandomDelay)))
			}
			time.Sleep(r.Delay + randomDelay)
			<-r.waitChan
		}(r)
	}
	if !checkRequestHeadersFunc(request) {
		return nil, ErrAbortedBeforeRequest
	}

	// Manual redirect handling to capture intermediate responses
	// Since CheckRedirect returns http.ErrUseLastResponse, we need to follow redirects manually
	var redirectChain []*RedirectResponse
	var via []*http.Request // Track the chain of requests for CheckRedirect callback
	currentRequest := request
	maxRedirects := 10

	for redirectCount := 0; redirectCount < maxRedirects; redirectCount++ {
		res, err := h.Client.Do(currentRequest)
		if err != nil {
			return nil, err
		}

		// Check if this is a redirect (3xx status code)
		isRedirect := res.StatusCode >= 300 && res.StatusCode < 400
		location := res.Header.Get("Location")

		if isRedirect && location != "" {
			// Parse the redirect location
			redirectURL, err := currentRequest.URL.Parse(location)
			if err != nil {
				res.Body.Close()
				return nil, err
			}

			// Call CheckRedirect to validate the redirect (allows Crawler to filter URLs)
			// Build the via chain for the callback
			via = append(via, currentRequest)

			// Create a temporary request for the redirect destination
			tempReq, err := http.NewRequest("GET", redirectURL.String(), nil)
			if err != nil {
				res.Body.Close()
				return nil, err
			}

			// Call CheckRedirect if it's set
			if h.Client.CheckRedirect != nil {
				err = h.Client.CheckRedirect(tempReq, via)
				// If CheckRedirect returns an error OTHER than ErrUseLastResponse, stop the redirect
				if err != nil && err != http.ErrUseLastResponse {
					res.Body.Close()
					// Return the error to indicate redirect was blocked
					return nil, err
				}
			}
			// This is an intermediate redirect - store it in the chain
			redirectChain = append(redirectChain, &RedirectResponse{
				URL:        currentRequest.URL.String(),
				StatusCode: res.StatusCode,
				Headers:    &res.Header,
				Location:   location,
			})

			// Close the current response body (we don't need it for redirects)
			res.Body.Close()

			// Create new request for the redirect destination
			// Preserve method and body based on redirect type
			var newMethod string
			var newBody io.Reader

			if res.StatusCode == 307 || res.StatusCode == 308 {
				// 307/308: Preserve method and body
				newMethod = currentRequest.Method
				newBody = currentRequest.Body
			} else {
				// 301/302/303: Convert to GET and drop body
				newMethod = "GET"
				newBody = nil
			}

			newRequest, err := http.NewRequest(newMethod, redirectURL.String(), newBody)
			if err != nil {
				return nil, err
			}

			// Copy headers from original request
			for key, values := range currentRequest.Header {
				for _, value := range values {
					newRequest.Header.Add(key, value)
				}
			}

			// Drop Authorization header if host changes (security measure)
			if newRequest.URL.Host != currentRequest.URL.Host {
				newRequest.Header.Del("Authorization")
			}

			// Continue with the redirect
			currentRequest = newRequest
			continue
		}

		// Not a redirect or no more redirects - this is the final response
		defer res.Body.Close()

		finalRequest := currentRequest
		if res.Request != nil {
			finalRequest = res.Request
		}
		if !checkResponseHeadersFunc(finalRequest, res.StatusCode, res.Header) {
			// closing res.Body (see defer above) without reading it aborts
			// the download
			return nil, ErrAbortedAfterHeaders
		}

		var bodyReader io.Reader = res.Body
		if bodySize > 0 {
			bodyReader = io.LimitReader(bodyReader, int64(bodySize))
		}
		contentEncoding := strings.ToLower(res.Header.Get("Content-Encoding"))
		if !res.Uncompressed && (strings.Contains(contentEncoding, "gzip") || (contentEncoding == "" && strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "gzip")) || strings.HasSuffix(strings.ToLower(finalRequest.URL.Path), ".xml.gz")) {
			bodyReader, err = gzip.NewReader(bodyReader)
			if err != nil {
				return nil, err
			}
			defer bodyReader.(*gzip.Reader).Close()
		}
		body, err := io.ReadAll(bodyReader)
		if err != nil {
			return nil, err
		}
		return &Response{
			StatusCode:    res.StatusCode,
			Body:          body,
			Headers:       &res.Header,
			RedirectChain: redirectChain,
		}, nil
	}

	// Exceeded maximum redirects
	return nil, errors.New("stopped after 10 redirects")
}

func (h *httpBackend) Limit(rule *LimitRule) error {
	h.lock.Lock()
	if h.LimitRules == nil {
		h.LimitRules = make([]*LimitRule, 0, 8)
	}
	h.LimitRules = append(h.LimitRules, rule)
	h.lock.Unlock()
	return rule.Init()
}

func (h *httpBackend) Limits(rules []*LimitRule) error {
	for _, r := range rules {
		if err := h.Limit(r); err != nil {
			return err
		}
	}
	return nil
}
