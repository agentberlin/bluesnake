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
	app           *app.App
	ctx           context.Context
	serverManager *ServerManager
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
	d.serverManager = NewServerManager(ctx, d.app)
}

// GetProjects wraps app.GetProjects
func (d *DesktopApp) GetProjects() ([]types.ProjectInfo, error) {
	return d.app.GetProjects()
}

// GetCrawls wraps app.GetCrawls
func (d *DesktopApp) GetCrawls(projectID uint) ([]types.CrawlInfo, error) {
	return d.app.GetCrawls(projectID)
}

// GetCrawlWithResults wraps app.GetCrawlWithResults
func (d *DesktopApp) GetCrawlWithResults(crawlID uint) (*types.CrawlResultDetailed, error) {
	return d.app.GetCrawlWithResults(crawlID)
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

// GetActiveCrawlData wraps app.GetActiveCrawlData
func (d *DesktopApp) GetActiveCrawlData(projectID uint) (*types.CrawlResultDetailed, error) {
	return d.app.GetActiveCrawlData(projectID)
}

// GetConfigForDomain wraps app.GetConfigForDomain
func (d *DesktopApp) GetConfigForDomain(url string) (*types.ConfigResponse, error) {
	return d.app.GetConfigForDomain(url)
}

// UpdateConfigForDomain wraps app.UpdateConfigForDomain
func (d *DesktopApp) UpdateConfigForDomain(
	url string,
	jsRendering bool,
	parallelism int,
	userAgent string,
	includeSubdomains bool,
	spiderEnabled bool,
	sitemapEnabled bool,
	sitemapURLs []string,
	checkExternalResources bool,
	singlePageMode bool,
) error {
	return d.app.UpdateConfigForDomain(url, jsRendering, parallelism, userAgent, includeSubdomains, spiderEnabled, sitemapEnabled, sitemapURLs, checkExternalResources, singlePageMode)
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

// StartServerWithTunnel starts the HTTP server with cloudflared tunnel
func (d *DesktopApp) StartServerWithTunnel() (*types.ServerInfo, error) {
	return d.serverManager.StartWithTunnel()
}

// StopServerWithTunnel stops the HTTP server and tunnel
func (d *DesktopApp) StopServerWithTunnel() error {
	return d.serverManager.Stop()
}

// GetServerStatus returns the current server status
func (d *DesktopApp) GetServerStatus() *types.ServerStatus {
	return d.serverManager.GetStatus()
}
