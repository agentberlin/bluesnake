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

import (
	"strings"
)

// Framework represents a web framework/platform
type Framework string

const (
	FrameworkOther     Framework = "other"
	FrameworkNextJS    Framework = "nextjs"
	FrameworkNuxtJS    Framework = "nuxtjs"
	FrameworkGatsby    Framework = "gatsby"
	FrameworkReact     Framework = "react"
	FrameworkVue       Framework = "vue"
	FrameworkAngular   Framework = "angular"
	FrameworkWordPress Framework = "wordpress"
	FrameworkWebflow   Framework = "webflow"
	FrameworkShopify   Framework = "shopify"
	FrameworkWix       Framework = "wix"
	FrameworkDrupal    Framework = "drupal"
	FrameworkJoomla    Framework = "joomla"
)

// Detector handles framework detection from HTML content
type Detector struct {
	detectedFramework Framework
	signals           []string
}

// NewDetector creates a new framework detector
func NewDetector() *Detector {
	return &Detector{
		detectedFramework: FrameworkOther,
		signals:           []string{},
	}
}

// Detect analyzes HTML content and network URLs to detect the framework
func (d *Detector) Detect(html string, networkURLs []string) Framework {
	htmlLower := strings.ToLower(html)
	allURLs := strings.ToLower(strings.Join(networkURLs, " "))

	// Reset state
	d.detectedFramework = FrameworkOther
	d.signals = []string{}

	// Check for Next.js
	if d.detectNextJS(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Nuxt.js
	if d.detectNuxtJS(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for WordPress
	if d.detectWordPress(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Shopify
	if d.detectShopify(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Webflow
	if d.detectWebflow(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Wix
	if d.detectWix(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Gatsby
	if d.detectGatsby(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Angular
	if d.detectAngular(htmlLower) {
		return d.detectedFramework
	}

	// Check for Vue (client-side)
	if d.detectVue(htmlLower) {
		return d.detectedFramework
	}

	// Check for React (client-side)
	if d.detectReact(htmlLower) {
		return d.detectedFramework
	}

	// Check for Drupal
	if d.detectDrupal(htmlLower, allURLs) {
		return d.detectedFramework
	}

	// Check for Joomla
	if d.detectJoomla(htmlLower, allURLs) {
		return d.detectedFramework
	}

	return FrameworkOther
}

// GetSignals returns the detection signals found
func (d *Detector) GetSignals() []string {
	return d.signals
}

// detectNextJS checks for Next.js framework
func (d *Detector) detectNextJS(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/_next/static/") || strings.Contains(urls, "/_next/static/") {
		score += 3
		d.signals = append(d.signals, "Found /_next/static/ in HTML or network requests")
	}

	if strings.Contains(html, `<div id="__next"`) || strings.Contains(html, `<div id='__next'`) {
		score += 2
		d.signals = append(d.signals, "Found <div id=\"__next\">")
	}

	if strings.Contains(urls, "_rsc=") {
		score += 2
		d.signals = append(d.signals, "Found _rsc= query params in network requests")
	}

	if strings.Contains(html, "next-data") || strings.Contains(html, "__next_data__") {
		score += 2
		d.signals = append(d.signals, "Found Next.js data script")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkNextJS
		return true
	}

	return false
}

// detectNuxtJS checks for Nuxt.js framework
func (d *Detector) detectNuxtJS(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/_nuxt/") || strings.Contains(urls, "/_nuxt/") {
		score += 3
		d.signals = append(d.signals, "Found /_nuxt/ in HTML or network requests")
	}

	if strings.Contains(html, `<div id="__nuxt"`) || strings.Contains(html, `<div id='__nuxt'`) {
		score += 2
		d.signals = append(d.signals, "Found <div id=\"__nuxt\">")
	}

	if strings.Contains(html, "__nuxt") {
		score += 1
		d.signals = append(d.signals, "Found __nuxt reference")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkNuxtJS
		return true
	}

	return false
}

// detectWordPress checks for WordPress CMS
func (d *Detector) detectWordPress(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/wp-content/") || strings.Contains(urls, "/wp-content/") {
		score += 3
		d.signals = append(d.signals, "Found /wp-content/ in HTML or network requests")
	}

	if strings.Contains(html, "/wp-includes/") || strings.Contains(urls, "/wp-includes/") {
		score += 2
		d.signals = append(d.signals, "Found /wp-includes/ in HTML or network requests")
	}

	if strings.Contains(html, `name="generator" content="wordpress`) {
		score += 2
		d.signals = append(d.signals, "Found WordPress generator meta tag")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkWordPress
		return true
	}

	return false
}

// detectShopify checks for Shopify e-commerce platform
func (d *Detector) detectShopify(html, urls string) bool {
	score := 0

	if strings.Contains(html, "cdn.shopify.com") || strings.Contains(urls, "cdn.shopify.com") {
		score += 3
		d.signals = append(d.signals, "Found cdn.shopify.com in HTML or network requests")
	}

	if strings.Contains(html, "shopify.theme") || strings.Contains(html, "shopify.") {
		score += 2
		d.signals = append(d.signals, "Found Shopify theme object")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkShopify
		return true
	}

	return false
}

// detectWebflow checks for Webflow
func (d *Detector) detectWebflow(html, urls string) bool {
	score := 0

	if strings.Contains(html, "webflow.js") || strings.Contains(urls, "webflow.js") {
		score += 3
		d.signals = append(d.signals, "Found webflow.js")
	}

	if strings.Contains(html, `class="w-`) {
		score += 2
		d.signals = append(d.signals, "Found Webflow CSS classes (.w-)")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkWebflow
		return true
	}

	return false
}

// detectWix checks for Wix website builder
func (d *Detector) detectWix(html, urls string) bool {
	score := 0

	if strings.Contains(html, "wix.com") || strings.Contains(urls, "wix.com") {
		score += 3
		d.signals = append(d.signals, "Found wix.com in HTML or network requests")
	}

	if strings.Contains(html, "_wix") || strings.Contains(html, "data-wix-") {
		score += 2
		d.signals = append(d.signals, "Found Wix attributes")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkWix
		return true
	}

	return false
}

// detectGatsby checks for Gatsby static site generator
func (d *Detector) detectGatsby(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/___gatsby") || strings.Contains(urls, "/___gatsby") {
		score += 3
		d.signals = append(d.signals, "Found /___gatsby in HTML or network requests")
	}

	if strings.Contains(html, "gatsby-") {
		score += 1
		d.signals = append(d.signals, "Found gatsby- classes")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkGatsby
		return true
	}

	return false
}

// detectAngular checks for Angular framework
func (d *Detector) detectAngular(html string) bool {
	score := 0

	if strings.Contains(html, "ng-") || strings.Contains(html, "data-ng-") {
		score += 2
		d.signals = append(d.signals, "Found Angular directives (ng-)")
	}

	if strings.Contains(html, `<app-root`) {
		score += 2
		d.signals = append(d.signals, "Found Angular app-root component")
	}

	if score >= 2 {
		d.detectedFramework = FrameworkAngular
		return true
	}

	return false
}

// detectVue checks for Vue.js (client-side)
func (d *Detector) detectVue(html string) bool {
	score := 0

	if strings.Contains(html, "v-") && (strings.Contains(html, "v-if") || strings.Contains(html, "v-for") || strings.Contains(html, "v-bind")) {
		score += 2
		d.signals = append(d.signals, "Found Vue directives (v-)")
	}

	if strings.Contains(html, `<div id="app"`) && strings.Contains(html, "v-") {
		score += 2
		d.signals = append(d.signals, "Found Vue app div with directives")
	}

	if score >= 2 {
		d.detectedFramework = FrameworkVue
		return true
	}

	return false
}

// detectReact checks for React (client-side)
func (d *Detector) detectReact(html string) bool {
	score := 0

	if strings.Contains(html, `<div id="root"`) {
		score += 1
		d.signals = append(d.signals, "Found <div id=\"root\">")
	}

	if strings.Contains(html, "data-reactroot") || strings.Contains(html, "data-react-") {
		score += 2
		d.signals = append(d.signals, "Found React data attributes")
	}

	// Only detect React if we have clear signals and it's not another framework
	if score >= 2 && d.detectedFramework == FrameworkOther {
		d.detectedFramework = FrameworkReact
		return true
	}

	return false
}

// detectDrupal checks for Drupal CMS
func (d *Detector) detectDrupal(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/sites/all/") || strings.Contains(urls, "/sites/all/") {
		score += 3
		d.signals = append(d.signals, "Found /sites/all/ in HTML or network requests")
	}

	if strings.Contains(html, "drupal.settings") || strings.Contains(html, "drupal.") {
		score += 2
		d.signals = append(d.signals, "Found Drupal.settings JS object")
	}

	if strings.Contains(html, `name="generator" content="drupal`) {
		score += 2
		d.signals = append(d.signals, "Found Drupal generator meta tag")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkDrupal
		return true
	}

	return false
}

// detectJoomla checks for Joomla CMS
func (d *Detector) detectJoomla(html, urls string) bool {
	score := 0

	if strings.Contains(html, "/media/joomla/") || strings.Contains(urls, "/media/joomla/") {
		score += 3
		d.signals = append(d.signals, "Found /media/joomla/ in HTML or network requests")
	}

	if strings.Contains(html, `name="generator" content="joomla`) {
		score += 2
		d.signals = append(d.signals, "Found Joomla generator meta tag")
	}

	if score >= 3 {
		d.detectedFramework = FrameworkJoomla
		return true
	}

	return false
}
