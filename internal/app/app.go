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
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"

	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/types"
)

// App represents the core application logic
type App struct {
	ctx          context.Context
	store        *store.Store
	emitter      EventEmitter
	activeCrawls map[uint]*activeCrawl
	crawlsMutex  sync.RWMutex
}

// NewApp creates a new App instance with dependencies injected
func NewApp(st *store.Store, emitter EventEmitter) *App {
	if emitter == nil {
		emitter = &NoOpEmitter{}
	}

	return &App{
		store:        st,
		emitter:      emitter,
		activeCrawls: make(map[uint]*activeCrawl),
	}
}

// Startup initializes the app with a context
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// CheckSystemHealth checks if all required dependencies are available
func (a *App) CheckSystemHealth() *types.SystemHealthCheck {
	// Check if Chrome/Chromium is available for JS rendering
	chromeAvailable := isChromeBrowserAvailable()

	if !chromeAvailable {
		return &types.SystemHealthCheck{
			IsHealthy:  false,
			ErrorTitle: "Chrome Browser Required",
			ErrorMsg:   "Google Chrome or Chromium is required for JavaScript rendering but was not found on your system.",
			Suggestion: "Please install Google Chrome from https://www.google.com/chrome/\n\nAlternatively, you can set the CHROME_EXECUTABLE_PATH environment variable to point to your Chrome installation.\n\nNote: You can still use BlueSnake without Chrome, but JavaScript rendering will not be available.",
		}
	}

	// All checks passed
	return &types.SystemHealthCheck{
		IsHealthy: true,
	}
}

// isChromeBrowserAvailable checks if Chrome or Chromium is available
func isChromeBrowserAvailable() bool {
	// Check if custom Chrome path is set
	if customPath := os.Getenv("CHROME_EXECUTABLE_PATH"); customPath != "" {
		if _, err := os.Stat(customPath); err == nil {
			return true
		}
	}

	// Try common Chrome locations based on OS
	var chromePaths []string

	switch runtime.GOOS {
	case "darwin": // macOS
		chromePaths = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			os.Getenv("HOME") + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	case "windows":
		chromePaths = []string{
			os.Getenv("ProgramFiles") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("ProgramFiles(x86)") + "\\Google\\Chrome\\Application\\chrome.exe",
			os.Getenv("LocalAppData") + "\\Google\\Chrome\\Application\\chrome.exe",
		}
	case "linux":
		chromePaths = []string{
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}

	// Check if any of the common paths exist
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	// Try to find Chrome in PATH
	if _, err := exec.LookPath("google-chrome"); err == nil {
		return true
	}
	if _, err := exec.LookPath("chromium"); err == nil {
		return true
	}
	if _, err := exec.LookPath("chromium-browser"); err == nil {
		return true
	}

	return false
}
