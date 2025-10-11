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

package app

import (
	"github.com/agentberlin/bluesnake/internal/types"
)

// GetPageLinksForURL retrieves inbound and outbound links for a specific URL in a crawl
func (a *App) GetPageLinksForURL(crawlID uint, pageURL string) (*types.PageLinksResponse, error) {
	inlinks, outlinks, err := a.store.GetPageLinks(crawlID, pageURL)
	if err != nil {
		return nil, err
	}

	// Convert to frontend format
	inlinkInfos := make([]types.LinkInfo, 0, len(inlinks))
	for _, link := range inlinks {
		var status *int
		if link.Status != 0 {
			status = &link.Status
		}
		inlinkInfos = append(inlinkInfos, types.LinkInfo{
			URL:        link.SourceURL, // For inlinks, show the source URL
			AnchorText: link.LinkText,
			Status:     status,
		})
	}

	outlinkInfos := make([]types.LinkInfo, 0, len(outlinks))
	for _, link := range outlinks {
		var status *int
		if link.Status != 0 {
			status = &link.Status
		}
		outlinkInfos = append(outlinkInfos, types.LinkInfo{
			URL:        link.TargetURL, // For outlinks, show the target URL
			AnchorText: link.LinkText,
			Status:     status,
		})
	}

	return &types.PageLinksResponse{
		PageURL:  pageURL,
		Inlinks:  inlinkInfos,
		Outlinks: outlinkInfos,
	}, nil
}
