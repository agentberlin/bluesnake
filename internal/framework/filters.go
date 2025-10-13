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

package framework

// FilterConfig contains URL patterns and query params to filter for a framework
type FilterConfig struct {
	URLPatterns []string // Patterns to match in full URL
	QueryParams []string // Query parameter keys to filter
}

// GetFilterConfig returns filtering rules for a specific framework
func GetFilterConfig(fw Framework) FilterConfig {
	configs := map[Framework]FilterConfig{
		FrameworkNextJS: {
			URLPatterns: []string{"/_next/static/", "/_next/data/"},
			QueryParams: []string{"_rsc"},
		},
		FrameworkNuxtJS: {
			URLPatterns: []string{"/_nuxt/"},
			QueryParams: []string{"__nuxt_"},
		},
		FrameworkWordPress: {
			// Note: /wp-json/ is filtered for traditional WordPress sites.
			// For headless WordPress setups where the REST API serves content,
			// this filter may need to be disabled via configuration.
			URLPatterns: []string{"/wp-json/", "/wp-admin/", "/wp-login.php"},
			QueryParams: []string{"ver"},
		},
		FrameworkShopify: {
			// Note: Not filtering generic "v" param as it often represents product variants
			// (colors, sizes, etc.) which are important for e-commerce SEO
			QueryParams: []string{},
		},
		FrameworkWebflow: {
			// Webflow has internal asset versioning but typically no special filtering needed
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkWix: {
			// Wix has internal tracking URLs
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkGatsby: {
			// Gatsby generates static sites, no special filtering needed
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkAngular: {
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkVue: {
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkReact: {
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkDrupal: {
			URLPatterns: []string{},
			QueryParams: []string{},
		},
		FrameworkJoomla: {
			URLPatterns: []string{},
			QueryParams: []string{},
		},
	}

	if config, ok := configs[fw]; ok {
		return config
	}

	// Return empty config for unknown frameworks
	return FilterConfig{
		URLPatterns: []string{},
		QueryParams: []string{},
	}
}

// GetAllFrameworks returns a list of all supported frameworks with their metadata
func GetAllFrameworks() []FrameworkInfo {
	return []FrameworkInfo{
		{
			ID:          string(FrameworkNextJS),
			Name:        "Next.js",
			Category:    "JavaScript Framework",
			Description: "React framework with SSR/SSG",
		},
		{
			ID:          string(FrameworkNuxtJS),
			Name:        "Nuxt.js",
			Category:    "JavaScript Framework",
			Description: "Vue framework with SSR/SSG",
		},
		{
			ID:          string(FrameworkGatsby),
			Name:        "Gatsby",
			Category:    "Static Site Generator",
			Description: "React-based static site generator",
		},
		{
			ID:          string(FrameworkReact),
			Name:        "React",
			Category:    "JavaScript Framework",
			Description: "Client-side React application",
		},
		{
			ID:          string(FrameworkVue),
			Name:        "Vue.js",
			Category:    "JavaScript Framework",
			Description: "Client-side Vue application",
		},
		{
			ID:          string(FrameworkAngular),
			Name:        "Angular",
			Category:    "JavaScript Framework",
			Description: "Google's SPA framework",
		},
		{
			ID:          string(FrameworkWordPress),
			Name:        "WordPress",
			Category:    "CMS",
			Description: "PHP-based content management system",
		},
		{
			ID:          string(FrameworkWebflow),
			Name:        "Webflow",
			Category:    "No-Code Platform",
			Description: "Visual web design tool",
		},
		{
			ID:          string(FrameworkShopify),
			Name:        "Shopify",
			Category:    "E-commerce",
			Description: "E-commerce platform",
		},
		{
			ID:          string(FrameworkWix),
			Name:        "Wix",
			Category:    "No-Code Platform",
			Description: "Website builder platform",
		},
		{
			ID:          string(FrameworkDrupal),
			Name:        "Drupal",
			Category:    "CMS",
			Description: "PHP-based content management system",
		},
		{
			ID:          string(FrameworkJoomla),
			Name:        "Joomla",
			Category:    "CMS",
			Description: "PHP-based content management system",
		},
	}
}

// FrameworkInfo contains metadata about a framework
type FrameworkInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}
