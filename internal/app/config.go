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
		Domain:                   config.Domain,
		JSRenderingEnabled:       config.JSRenderingEnabled,
		InitialWaitMs:            config.InitialWaitMs,
		ScrollWaitMs:             config.ScrollWaitMs,
		FinalWaitMs:              config.FinalWaitMs,
		Parallelism:              config.Parallelism,
		UserAgent:                config.UserAgent,
		IncludeSubdomains:        config.IncludeSubdomains,
		DiscoveryMechanisms:      config.GetDiscoveryMechanismsArray(),
		SitemapURLs:              config.GetSitemapURLsArray(),
		CheckExternalResources:   config.CheckExternalResources,
		RobotsTxtMode:            config.RobotsTxtMode,
		FollowInternalNofollow:   config.FollowInternalNofollow,
		FollowExternalNofollow:   config.FollowExternalNofollow,
		RespectMetaRobotsNoindex: config.RespectMetaRobotsNoindex,
		RespectNoindex:           config.RespectNoindex,
	}, nil
}

// UpdateConfigForDomain updates the configuration for a specific domain
func (a *App) UpdateConfigForDomain(
	urlStr string,
	jsRendering bool,
	initialWaitMs, scrollWaitMs, finalWaitMs int,
	parallelism int,
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

	return a.store.UpdateConfig(project.ID, jsRendering, initialWaitMs, scrollWaitMs, finalWaitMs, parallelism, userAgent, includeSubdomains, mechanisms, sitemapURLs, checkExternalResources, robotsTxtMode, followInternalNofollow, followExternalNofollow, respectMetaRobotsNoindex, respectNoindex)
}
