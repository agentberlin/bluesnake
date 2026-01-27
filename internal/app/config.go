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
	"fmt"

	"github.com/agentberlin/bluesnake/internal/types"
)

// GetConfigForDomain retrieves the configuration for a specific domain
func (a *App) GetConfigForDomain(urlStr string) (*types.ConfigResponse, error) {
	// Resolve redirects to get canonical URL (e.g., amahahealth.com -> www.amahahealth.com)
	resolvedURL := resolveURL(urlStr)

	// Normalize the resolved URL to extract domain
	normalizedURL, domain, err := normalizeURL(resolvedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	// Get or create the project using the canonical domain
	project, err := a.store.GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %v", err)
	}

	config, err := a.store.GetOrCreateConfig(project.ID, domain)
	if err != nil {
		return nil, err
	}

	// Convert to response struct with deserialized arrays
	return &types.ConfigResponse{
		Domain:                     config.Domain,
		JSRenderingEnabled:         config.JSRenderingEnabled,
		InitialWaitMs:              config.InitialWaitMs,
		ScrollWaitMs:               config.ScrollWaitMs,
		FinalWaitMs:                config.FinalWaitMs,
		Parallelism:                config.Parallelism,
		RequestTimeoutSecs:         config.RequestTimeoutSecs,
		UserAgent:                  config.UserAgent,
		IncludeSubdomains:          config.IncludeSubdomains,
		DiscoveryMechanisms:        config.GetDiscoveryMechanismsArray(),
		SitemapURLs:                config.GetSitemapURLsArray(),
		CheckExternalResources:     config.CheckExternalResources,
		RobotsTxtMode:              config.RobotsTxtMode,
		FollowInternalNofollow:     config.FollowInternalNofollow,
		FollowExternalNofollow:     config.FollowExternalNofollow,
		RespectMetaRobotsNoindex:   config.RespectMetaRobotsNoindex,
		RespectNoindex:             config.RespectNoindex,
		IncrementalCrawlingEnabled: config.IncrementalCrawlingEnabled,
		CrawlBudget:                config.CrawlBudget,
	}, nil
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(
	urlStr string,
	jsRendering bool,
	initialWaitMs, scrollWaitMs, finalWaitMs int,
	parallelism int,
	requestTimeoutSecs int,
	userAgent string,
	includeSubdomains bool,
	spiderEnabled bool,
	sitemapEnabled bool,
	sitemapURLs []string,
	checkExternalResources bool,
	robotsTxtMode string,
	followInternalNofollow bool,
	followExternalNofollow bool,
	respectMetaRobotsNoindex bool,
	respectNoindex bool,
) error {
	// Resolve redirects to get canonical URL (e.g., amahahealth.com -> www.amahahealth.com)
	resolvedURL := resolveURL(urlStr)

	// Normalize the resolved URL to extract domain
	normalizedURL, domain, err := normalizeURL(resolvedURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Get or create the project using the canonical domain
	project, err := a.store.GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return fmt.Errorf("failed to get project: %v", err)
	}

	// Desktop logic: Always make sitemap additive
	// When sitemap is enabled, ALWAYS include both spider and sitemap
	var mechanisms []string
	if spiderEnabled && !sitemapEnabled {
		mechanisms = []string{"spider"}
	} else if sitemapEnabled {
		// Sitemap mode always includes spider for additive behavior
		mechanisms = []string{"spider", "sitemap"}
	} else {
		// At least one must be enabled (should be validated in frontend)
		// Default to spider if somehow both are false
		mechanisms = []string{"spider"}
	}

	// Get current config to check if incremental crawling is enabled
	currentConfig, err := a.store.GetOrCreateConfig(project.ID, domain)
	if err != nil {
		return fmt.Errorf("failed to get current config: %v", err)
	}

	// If incremental crawling is enabled and there's a queue, validate that certain settings haven't changed
	if currentConfig.IncrementalCrawlingEnabled {
		hasPending, _ := a.store.HasPendingURLs(project.ID)
		if hasPending {
			// Check for changes to settings that would invalidate the queue
			if currentConfig.IncludeSubdomains != includeSubdomains {
				return fmt.Errorf("cannot change 'Include Subdomains' while incremental crawling is enabled and there are pending URLs. Clear the queue first or disable incremental crawling")
			}
			currentMechs := currentConfig.GetDiscoveryMechanismsArray()
			if !stringSliceEqual(currentMechs, mechanisms) {
				return fmt.Errorf("cannot change 'Discovery Mechanisms' while incremental crawling is enabled and there are pending URLs. Clear the queue first or disable incremental crawling")
			}
			if currentConfig.RobotsTxtMode != robotsTxtMode {
				return fmt.Errorf("cannot change 'Robots.txt Mode' while incremental crawling is enabled and there are pending URLs. Clear the queue first or disable incremental crawling")
			}
		}
	}

	return a.store.UpdateConfig(project.ID, jsRendering, initialWaitMs, scrollWaitMs, finalWaitMs, parallelism, requestTimeoutSecs, userAgent, includeSubdomains, mechanisms, sitemapURLs, checkExternalResources, robotsTxtMode, followInternalNofollow, followExternalNofollow, respectMetaRobotsNoindex, respectNoindex)
}

// stringSliceEqual checks if two string slices are equal
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// UpdateIncrementalConfigForDomain updates the incremental crawling settings for a domain
func (a *App) UpdateIncrementalConfigForDomain(urlStr string, enabled bool, budget int) error {
	// Resolve redirects to get canonical URL
	resolvedURL := resolveURL(urlStr)

	// Normalize the resolved URL to extract domain
	normalizedURL, domain, err := normalizeURL(resolvedURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	// Get or create the project using the canonical domain
	project, err := a.store.GetOrCreateProject(normalizedURL, domain)
	if err != nil {
		return fmt.Errorf("failed to get project: %v", err)
	}

	// If disabling incremental crawling, also clear the queue
	if !enabled {
		if err := a.store.ClearQueue(project.ID); err != nil {
			return fmt.Errorf("failed to clear queue: %v", err)
		}
	}

	return a.store.UpdateIncrementalConfig(project.ID, enabled, budget)
}
