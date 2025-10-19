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

// Storage is an interface which handles Collector's HTTP-level data.
// The default Storage of the Collector is the InMemoryStorage.
// Collector's storage can be changed by calling Collector.SetStorage() function.
//
// NOTE: This is separate from CrawlerStore (which handles crawl orchestration).
// Storage handles only HTTP client concerns:
//   - Cookies (HTTP session state)
//   - Content hashes (duplicate content detection)
//
// Visit tracking has been removed - Crawler owns all visit tracking via CrawlerStore.
type Storage interface {
	// Init initializes the storage
	Init() error

	// HTTP Session Management (Cookies)

	// Cookies retrieves stored cookies for a given host
	Cookies(u *url.URL) string
	// SetCookies stores cookies for a given host
	SetCookies(u *url.URL, cookies string)

	// Content Hash Management (Duplicate Detection)

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
// InMemoryStorage keeps cookies and content hashes in memory
// without persisting data on the disk.
type InMemoryStorage struct {
	contentHashes  map[string]string // url -> content hash
	visitedContent map[string]bool   // content hash -> visited
	lock           *sync.RWMutex
	jar            *cookiejar.Jar
}

// Init initializes InMemoryStorage
func (s *InMemoryStorage) Init() error {
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
