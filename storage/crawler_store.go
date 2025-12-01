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

package storage

import "sync"

// CrawlerStore manages crawler-specific state during a crawl session.
// This is separate from the Collector's Storage (which handles HTTP-level concerns
// like cookies and content hashes). CrawlerStore handles crawl orchestration concerns.
type CrawlerStore struct {
	// visited tracks which URL hashes have been visited (thread-safe)
	visited map[uint64]bool
	// queuedURLs tracks discovered URLs and their actions (for memoization)
	queuedURLs map[string]interface{} // map[string]URLAction - using interface{} to avoid circular import
	// pageMetadata caches metadata for crawled pages (for link population)
	pageMetadata map[string]interface{} // map[string]PageMetadata - using interface{} to avoid circular import
	// redirectDestinations tracks ALL redirect destinations for a request
	// map[finalURL][]intermediateURLs - stores all intermediate redirect URLs
	// Cleaned up after OnResponse processes the final URL
	redirectDestinations map[string][]string
	// mu protects all maps
	mu sync.RWMutex
}

// NewCrawlerStore creates a new CrawlerStore instance
func NewCrawlerStore() *CrawlerStore {
	return &CrawlerStore{
		visited:              make(map[uint64]bool),
		queuedURLs:           make(map[string]interface{}),
		pageMetadata:         make(map[string]interface{}),
		redirectDestinations: make(map[string][]string),
	}
}

// VisitIfNotVisited atomically checks if a URL hash has been visited and marks it as visited.
// Returns true if already visited, false if newly visited.
// This is the CRITICAL method for preventing race conditions in URL visit tracking.
func (s *CrawlerStore) VisitIfNotVisited(hash uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.visited[hash] {
		return true, nil // Already visited
	}
	s.visited[hash] = true
	return false, nil // Newly visited
}

// PreMarkVisited marks a URL hash as visited without checking first.
// Used for pre-populating the visited set when resuming a crawl.
func (s *CrawlerStore) PreMarkVisited(hash uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.visited[hash] = true
}

// CountVisited returns the number of URLs marked as visited.
func (s *CrawlerStore) CountVisited() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.visited)
}

// IsVisited checks if a URL hash has been visited (read-only check)
func (s *CrawlerStore) IsVisited(hash uint64) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visited[hash], nil
}

// StoreAction stores the action for a discovered URL (for memoization)
func (s *CrawlerStore) StoreAction(url string, action interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuedURLs[url] = action
}

// GetAction retrieves the action for a URL (returns nil if not found)
func (s *CrawlerStore) GetAction(url string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	action, exists := s.queuedURLs[url]
	return action, exists
}

// CountActions returns the total number of URLs with stored actions
func (s *CrawlerStore) CountActions() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.queuedURLs)
}

// StoreMetadata stores metadata for a crawled page (for link population)
func (s *CrawlerStore) StoreMetadata(url string, metadata interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageMetadata[url] = metadata
}

// GetMetadata retrieves metadata for a URL (returns nil if not found)
func (s *CrawlerStore) GetMetadata(url string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	metadata, exists := s.pageMetadata[url]
	return metadata, exists
}

// AddRedirectDestination adds a redirect destination to the list for a final URL
// For redirect chains A→B→C, this is called twice:
// - First with finalURL=B (since we don't know C yet)
// - Then with finalURL=C, and we add B to C's list
func (s *CrawlerStore) AddRedirectDestination(finalURL, intermediateURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirectDestinations[finalURL] = append(s.redirectDestinations[finalURL], intermediateURL)
}

// GetAndClearRedirectDestinations retrieves and removes all redirect destinations for a URL
// Returns the list of intermediate URLs and true if found, empty list and false otherwise
func (s *CrawlerStore) GetAndClearRedirectDestinations(finalURL string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	urls, exists := s.redirectDestinations[finalURL]
	if exists {
		delete(s.redirectDestinations, finalURL)
	}
	return urls, exists
}

// Clear resets all stored data (useful for testing or restarting crawls)
func (s *CrawlerStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.visited = make(map[uint64]bool)
	s.queuedURLs = make(map[string]interface{})
	s.pageMetadata = make(map[string]interface{})
	s.redirectDestinations = make(map[string][]string)
}
