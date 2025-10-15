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

package storage

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
)

// Storage is an interface which handles Collector's internal data,
// like visited urls and cookies.
// The default Storage of the Collector is the InMemoryStorage.
// Collector's storage can be changed by calling Collector.SetStorage()
// function.
type Storage interface {
	// Init initializes the storage
	Init() error
	// Visited receives and stores a request ID that is visited by the Collector
	Visited(requestID uint64) error
	// IsVisited returns true if the request was visited before IsVisited
	// is called
	IsVisited(requestID uint64) (bool, error)
	// VisitIfNotVisited atomically checks if a request ID has been visited,
	// and if not, marks it as visited. Returns true if the URL was already visited.
	// This is the atomic equivalent of IsVisited() + Visited() and prevents race conditions.
	VisitIfNotVisited(requestID uint64) (bool, error)
	// Cookies retrieves stored cookies for a given host
	Cookies(u *url.URL) string
	// SetCookies stores cookies for a given host
	SetCookies(u *url.URL, cookies string)
	// SetContentHash stores a content hash for a given URL
	SetContentHash(url string, contentHash string) error
	// GetContentHash retrieves the stored content hash for a given URL
	GetContentHash(url string) (string, error)
	// IsContentVisited returns true if content with the given hash has been visited
	IsContentVisited(contentHash string) (bool, error)
	// VisitedContent marks content with the given hash as visited
	VisitedContent(contentHash string) error
}

// InMemoryStorage is the default storage backend of bluesnake.
// InMemoryStorage keeps cookies and visited urls in memory
// without persisting data on the disk.
type InMemoryStorage struct {
	visitedURLs     map[uint64]bool
	contentHashes   map[string]string // url -> content hash
	visitedContent  map[string]bool   // content hash -> visited
	lock            *sync.RWMutex
	jar             *cookiejar.Jar
}

// Init initializes InMemoryStorage
func (s *InMemoryStorage) Init() error {
	if s.visitedURLs == nil {
		s.visitedURLs = make(map[uint64]bool)
	}
	if s.contentHashes == nil {
		s.contentHashes = make(map[string]string)
	}
	if s.visitedContent == nil {
		s.visitedContent = make(map[string]bool)
	}
	if s.lock == nil {
		s.lock = &sync.RWMutex{}
	}
	if s.jar == nil {
		var err error
		s.jar, err = cookiejar.New(nil)
		return err
	}
	return nil
}

// Visited implements Storage.Visited()
func (s *InMemoryStorage) Visited(requestID uint64) error {
	s.lock.Lock()
	s.visitedURLs[requestID] = true
	s.lock.Unlock()
	return nil
}

// IsVisited implements Storage.IsVisited()
func (s *InMemoryStorage) IsVisited(requestID uint64) (bool, error) {
	s.lock.RLock()
	visited := s.visitedURLs[requestID]
	s.lock.RUnlock()
	return visited, nil
}

// VisitIfNotVisited implements Storage.VisitIfNotVisited()
// Atomically checks if a request ID has been visited, and if not, marks it as visited.
// Returns true if the URL was already visited, false if it was newly marked as visited.
func (s *InMemoryStorage) VisitIfNotVisited(requestID uint64) (bool, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	// Check if already visited
	if s.visitedURLs[requestID] {
		return true, nil // Already visited
	}

	// Mark as visited
	s.visitedURLs[requestID] = true
	return false, nil // Newly marked as visited
}

// Cookies implements Storage.Cookies()
func (s *InMemoryStorage) Cookies(u *url.URL) string {
	return StringifyCookies(s.jar.Cookies(u))
}

// SetCookies implements Storage.SetCookies()
func (s *InMemoryStorage) SetCookies(u *url.URL, cookies string) {
	s.jar.SetCookies(u, UnstringifyCookies(cookies))
}

// Close implements Storage.Close()
func (s *InMemoryStorage) Close() error {
	return nil
}

// StringifyCookies serializes list of http.Cookies to string
func StringifyCookies(cookies []*http.Cookie) string {
	// Stringify cookies.
	cs := make([]string, len(cookies))
	for i, c := range cookies {
		cs[i] = c.String()
	}
	return strings.Join(cs, "\n")
}

// UnstringifyCookies deserializes a cookie string to http.Cookies
func UnstringifyCookies(s string) []*http.Cookie {
	h := http.Header{}
	for _, c := range strings.Split(s, "\n") {
		h.Add("Set-Cookie", c)
	}
	r := http.Response{Header: h}
	return r.Cookies()
}

// ContainsCookie checks if a cookie name is represented in cookies
func ContainsCookie(cookies []*http.Cookie, name string) bool {
	for _, c := range cookies {
		if c.Name == name {
			return true
		}
	}
	return false
}

// SetContentHash implements Storage.SetContentHash()
func (s *InMemoryStorage) SetContentHash(url string, contentHash string) error {
	s.lock.Lock()
	s.contentHashes[url] = contentHash
	s.lock.Unlock()
	return nil
}

// GetContentHash implements Storage.GetContentHash()
func (s *InMemoryStorage) GetContentHash(url string) (string, error) {
	s.lock.RLock()
	hash := s.contentHashes[url]
	s.lock.RUnlock()
	return hash, nil
}

// IsContentVisited implements Storage.IsContentVisited()
func (s *InMemoryStorage) IsContentVisited(contentHash string) (bool, error) {
	s.lock.RLock()
	visited := s.visitedContent[contentHash]
	s.lock.RUnlock()
	return visited, nil
}

// VisitedContent implements Storage.VisitedContent()
func (s *InMemoryStorage) VisitedContent(contentHash string) error {
	s.lock.Lock()
	s.visitedContent[contentHash] = true
	s.lock.Unlock()
	return nil
}
