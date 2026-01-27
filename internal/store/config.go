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

import (
	"fmt"

	"gorm.io/gorm"
)

// GetOrCreateConfig retrieves the config for a project or creates one with defaults
func (s *Store) GetOrCreateConfig(projectID uint, domain string) (*Config, error) {
	var config Config

	result := s.db.Where("project_id = ?", projectID).First(&config)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new config with defaults
		config = Config{
			ProjectID:              projectID,
			Domain:                 domain,
			JSRenderingEnabled:     false,
			InitialWaitMs:          1500, // 1.5s for React/Next.js hydration
			ScrollWaitMs:           2000, // 2s for lazy-loaded content
			FinalWaitMs:            1000, // 1s for remaining DOM updates
			Parallelism:            5,
			UserAgent:              "bluesnake/1.0 (+https://snake.blue)",
			IncludeSubdomains:      true,                       // Default to including subdomains
			DiscoveryMechanisms:    "[\"spider\",\"sitemap\"]", // Default to both spider and sitemap mode
			SitemapURLs:            "",                         // Empty = use defaults when sitemap enabled
			CheckExternalResources: true,                       // Default to checking external resources
			// Crawler directive defaults (following ScreamingFrog's defaults)
			RobotsTxtMode:            "respect", // Default to respecting robots.txt
			FollowInternalNofollow:   false,     // Default to NOT following internal nofollow links
			FollowExternalNofollow:   false,     // Default to NOT following external nofollow links
			RespectMetaRobotsNoindex: true,      // Default to respecting meta robots noindex
			RespectNoindex:           true,      // Default to respecting X-Robots-Tag noindex
		}

		if err := s.db.Create(&config).Error; err != nil {
			return nil, fmt.Errorf("failed to create config: %v", err)
		}

		return &config, nil
	}

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get config: %v", result.Error)
	}

	return &config, nil
}

// UpdateConfig updates the configuration for a project
func (s *Store) UpdateConfig(projectID uint, jsRendering bool, initialWaitMs, scrollWaitMs, finalWaitMs int, parallelism int, requestTimeoutSecs int, userAgent string, includeSubdomains bool, discoveryMechanisms []string, sitemapURLs []string, checkExternalResources bool, robotsTxtMode string, followInternalNofollow, followExternalNofollow, respectMetaRobotsNoindex, respectNoindex bool) error {
	var config Config

	result := s.db.Where("project_id = ?", projectID).First(&config)

	if result.Error != nil {
		return fmt.Errorf("failed to get config: %v", result.Error)
	}

	// Update existing config
	config.JSRenderingEnabled = jsRendering
	config.InitialWaitMs = initialWaitMs
	config.ScrollWaitMs = scrollWaitMs
	config.FinalWaitMs = finalWaitMs
	config.Parallelism = parallelism
	config.RequestTimeoutSecs = requestTimeoutSecs
	config.UserAgent = userAgent
	config.IncludeSubdomains = includeSubdomains
	config.CheckExternalResources = checkExternalResources
	config.RobotsTxtMode = robotsTxtMode
	config.FollowInternalNofollow = followInternalNofollow
	config.FollowExternalNofollow = followExternalNofollow
	config.RespectMetaRobotsNoindex = respectMetaRobotsNoindex
	config.RespectNoindex = respectNoindex

	// Update discovery mechanisms
	if err := config.SetDiscoveryMechanismsArray(discoveryMechanisms); err != nil {
		return fmt.Errorf("failed to set discovery mechanisms: %v", err)
	}

	// Update sitemap URLs
	if err := config.SetSitemapURLsArray(sitemapURLs); err != nil {
		return fmt.Errorf("failed to set sitemap URLs: %v", err)
	}

	return s.db.Save(&config).Error
}

// UpdateIncrementalConfig updates only the incremental crawling settings for a project
func (s *Store) UpdateIncrementalConfig(projectID uint, enabled bool, budget int) error {
	return s.db.Model(&Config{}).
		Where("project_id = ?", projectID).
		Updates(map[string]interface{}{
			"incremental_crawling_enabled": enabled,
			"crawl_budget":                 budget,
		}).Error
}

// GetIncrementalConfig retrieves just the incremental crawling settings for a project
func (s *Store) GetIncrementalConfig(projectID uint) (enabled bool, budget int, err error) {
	var config Config
	if err := s.db.Where("project_id = ?", projectID).First(&config).Error; err != nil {
		return false, 0, err
	}
	return config.IncrementalCrawlingEnabled, config.CrawlBudget, nil
}
