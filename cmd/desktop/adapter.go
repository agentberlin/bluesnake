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

package main

import (
	"context"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/types"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// WailsEmitter implements the EventEmitter interface for Wails
type WailsEmitter struct {
	ctx context.Context
}

// NewWailsEmitter creates a new WailsEmitter
func NewWailsEmitter(ctx context.Context) *WailsEmitter {
	return &WailsEmitter{ctx: ctx}
}

// Emit sends an event through Wails runtime
func (w *WailsEmitter) Emit(eventType app.EventType, data interface{}) {
	runtime.EventsEmit(w.ctx, string(eventType), data)
	runtime.LogDebugf(w.ctx, "Event emitted: %s", eventType)
}

// DesktopApp is the Wails-specific adapter that wraps the core App
// All methods here are thin wrappers that simply delegate to the core app
type DesktopApp struct {
	app *app.App
	ctx context.Context
}

// NewDesktopApp creates a new DesktopApp adapter
func NewDesktopApp(coreApp *app.App) *DesktopApp {
	return &DesktopApp{
		app: coreApp,
	}
}

// Startup is called by Wails when the app starts
func (d *DesktopApp) Startup(ctx context.Context) {
	d.ctx = ctx
	d.app.Startup(ctx)
}

// GetProjects wraps app.GetProjects
func (d *DesktopApp) GetProjects() ([]types.ProjectInfo, error) {
	return d.app.GetProjects()
}

// GetCrawls wraps app.GetCrawls
func (d *DesktopApp) GetCrawls(projectID uint) ([]types.CrawlInfo, error) {
	return d.app.GetCrawls(projectID)
}

// StartCrawl wraps app.StartCrawl
func (d *DesktopApp) StartCrawl(url string) error {
	return d.app.StartCrawl(url)
}

// StopCrawl wraps app.StopCrawl
func (d *DesktopApp) StopCrawl(projectID uint) error {
	return d.app.StopCrawl(projectID)
}

// GetActiveCrawls wraps app.GetActiveCrawls
func (d *DesktopApp) GetActiveCrawls() []types.CrawlProgress {
	return d.app.GetActiveCrawls()
}

// GetActiveCrawlStats wraps app.GetActiveCrawlStats
func (d *DesktopApp) GetActiveCrawlStats(projectID uint) (*types.ActiveCrawlStats, error) {
	return d.app.GetActiveCrawlStats(projectID)
}

// GetCrawlStats wraps app.GetCrawlStats
func (d *DesktopApp) GetCrawlStats(crawlID uint) (*types.ActiveCrawlStats, error) {
	return d.app.GetCrawlStats(crawlID)
}

// GetConfigForDomain wraps app.GetConfigForDomain
func (d *DesktopApp) GetConfigForDomain(url string) (*types.ConfigResponse, error) {
	return d.app.GetConfigForDomain(url)
}

// UpdateConfigForDomain wraps app.UpdateConfigForDomain
func (d *DesktopApp) UpdateConfigForDomain(
	url string,
	jsRendering bool,
	initialWaitMs, scrollWaitMs, finalWaitMs int,
	parallelism int,
	userAgent string,
	includeSubdomains bool,
	spiderEnabled bool,
	sitemapEnabled bool,
	sitemapURLs []string,
	checkExternalResources bool,
	singlePageMode bool,
	robotsTxtMode string,
	followInternalNofollow bool,
	followExternalNofollow bool,
	respectMetaRobotsNoindex bool,
	respectNoindex bool,
) error {
	return d.app.UpdateConfigForDomain(url, jsRendering, initialWaitMs, scrollWaitMs, finalWaitMs, parallelism, userAgent, includeSubdomains, spiderEnabled, sitemapEnabled, sitemapURLs, checkExternalResources, singlePageMode, robotsTxtMode, followInternalNofollow, followExternalNofollow, respectMetaRobotsNoindex, respectNoindex)
}

// GetPageLinksForURL wraps app.GetPageLinksForURL
func (d *DesktopApp) GetPageLinksForURL(crawlID uint, pageURL string) (*types.PageLinksResponse, error) {
	return d.app.GetPageLinksForURL(crawlID, pageURL)
}

// GetPageContent wraps app.GetPageContent
func (d *DesktopApp) GetPageContent(crawlID uint, pageURL string) (string, error) {
	return d.app.GetPageContent(crawlID, pageURL)
}

// DeleteProjectByID wraps app.DeleteProjectByID
func (d *DesktopApp) DeleteProjectByID(projectID uint) error {
	return d.app.DeleteProjectByID(projectID)
}

// DeleteCrawlByID wraps app.DeleteCrawlByID
func (d *DesktopApp) DeleteCrawlByID(crawlID uint) error {
	return d.app.DeleteCrawlByID(crawlID)
}

// GetFaviconData wraps app.GetFaviconData
func (d *DesktopApp) GetFaviconData(faviconPath string) (string, error) {
	return d.app.GetFaviconData(faviconPath)
}

// CheckForUpdate wraps app.CheckForUpdate
func (d *DesktopApp) CheckForUpdate() (*types.UpdateInfo, error) {
	return d.app.CheckForUpdate()
}

// DownloadAndInstallUpdate wraps app.DownloadAndInstallUpdate
func (d *DesktopApp) DownloadAndInstallUpdate() error {
	return d.app.DownloadAndInstallUpdate()
}

// GetVersion wraps app.GetVersion
func (d *DesktopApp) GetVersion() string {
	return d.app.GetVersion()
}

// DetectJSRenderingNeed detects if a URL needs JavaScript rendering
func (d *DesktopApp) DetectJSRenderingNeed(url string) (bool, error) {
	return detectJSRenderingNeed(url)
}

// GetCrawlWithResultsPaginated wraps app.GetCrawlWithResultsPaginated
func (d *DesktopApp) GetCrawlWithResultsPaginated(crawlID uint, limit int, cursor uint, contentTypeFilter string) (*types.CrawlResultPaginated, error) {
	return d.app.GetCrawlWithResultsPaginated(crawlID, limit, cursor, contentTypeFilter)
}

// SearchCrawlResultsPaginated wraps app.SearchCrawlResultsPaginated
func (d *DesktopApp) SearchCrawlResultsPaginated(crawlID uint, query string, contentTypeFilter string, limit int, cursor uint) (*types.CrawlResultPaginated, error) {
	return d.app.SearchCrawlResultsPaginated(crawlID, query, contentTypeFilter, limit, cursor)
}

// GetAICrawlerData wraps app.GetAICrawlerData
func (d *DesktopApp) GetAICrawlerData(projectURL string) (*types.AICrawlerResponse, error) {
	return d.app.GetAICrawlerData(projectURL)
}

// SaveAICrawlerData wraps app.SaveAICrawlerData
func (d *DesktopApp) SaveAICrawlerData(projectURL string, data *types.AICrawlerData, ssrScreenshot, jsScreenshot, noJSScreenshot string) error {
	return d.app.SaveAICrawlerData(projectURL, data, ssrScreenshot, jsScreenshot, noJSScreenshot)
}

// RunAICrawlerChecks wraps app.RunAICrawlerChecks
func (d *DesktopApp) RunAICrawlerChecks(projectURL string) error {
	return d.app.RunAICrawlerChecks(projectURL)
}

// CheckSystemHealth wraps app.CheckSystemHealth
func (d *DesktopApp) CheckSystemHealth() *types.SystemHealthCheck {
	return d.app.CheckSystemHealth()
}
