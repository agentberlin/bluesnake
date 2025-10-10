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

package bluesnake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// chromedpRenderer handles browser-based page rendering
type chromedpRenderer struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	timeout     time.Duration
}

var (
	globalRenderer     *chromedpRenderer
	globalRendererOnce sync.Once
)

// getRenderer returns the global chromedp renderer instance
func getRenderer() *chromedpRenderer {
	globalRendererOnce.Do(func() {
		globalRenderer = &chromedpRenderer{
			timeout: 30 * time.Second,
		}
		globalRenderer.init()
	})
	return globalRenderer
}

// init initializes the browser allocator context
func (r *chromedpRenderer) init() {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	r.allocCtx, r.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
}

// Close cleans up the renderer resources
func (r *chromedpRenderer) Close() {
	if r.allocCancel != nil {
		r.allocCancel()
	}
}

// RenderPage renders a page using headless Chrome and returns the HTML
// NOTE: This function has no internal rate limiting. Parallelism is controlled by
// the LimitRule in http_backend.go. Setting very high parallelism (>10) may cause
// high memory/CPU usage as each browser context consumes ~100-200MB RAM.
func (r *chromedpRenderer) RenderPage(url string) (string, error) {
	// Create a new browser context
	ctx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()

	// Set timeout
	ctx, cancel = context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var htmlContent string

	// Run the chromedp tasks
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		// Wait for network to be idle (most dynamic content loaded)
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Additional wait for JavaScript to execute
		chromedp.Sleep(500*time.Millisecond),
		// Get the rendered HTML
		chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery),
	)

	if err != nil {
		return "", fmt.Errorf("chromedp rendering failed: %w", err)
	}

	return htmlContent, nil
}

// CloseGlobalRenderer closes the global renderer instance
// This should be called when the application exits
func CloseGlobalRenderer() {
	if globalRenderer != nil {
		globalRenderer.Close()
	}
}
