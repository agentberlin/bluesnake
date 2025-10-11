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

package store

import "fmt"

// SavePageLinks saves all links from a crawled page
func (s *Store) SavePageLinks(crawlID uint, sourceURL string, outboundLinks []PageLinkData, inboundLinks []PageLinkData) error {
	// Save outbound links (sourceURL -> targetURL)
	for _, link := range outboundLinks {
		pageLink := PageLink{
			CrawlID:     crawlID,
			SourceURL:   sourceURL,
			TargetURL:   link.URL,
			LinkType:    link.Type,
			LinkText:    link.Text,
			LinkContext: link.Context,
			IsInternal:  link.IsInternal,
			Status:      link.Status,
			Title:       link.Title,
			ContentType: link.ContentType,
			Position:    link.Position,
			DOMPath:     link.DOMPath,
		}
		if err := s.db.Create(&pageLink).Error; err != nil {
			return fmt.Errorf("failed to save outbound link: %v", err)
		}
	}

	// Note: Inbound links are already saved when those source pages were crawled
	// We don't need to save them again here

	return nil
}

// GetPageLinks retrieves inbound and outbound links for a specific URL in a crawl
func (s *Store) GetPageLinks(crawlID uint, pageURL string) (inlinks []PageLink, outlinks []PageLink, err error) {
	// Get outbound links (where this page is the source)
	if err := s.db.Where("crawl_id = ? AND source_url = ?", crawlID, pageURL).Find(&outlinks).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to get outbound links: %v", err)
	}

	// Get inbound links (where this page is the target)
	if err := s.db.Where("crawl_id = ? AND target_url = ?", crawlID, pageURL).Find(&inlinks).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to get inbound links: %v", err)
	}

	return inlinks, outlinks, nil
}
