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
	"embed"
	"log"
	"os"

	"github.com/agentberlin/bluesnake/internal/app"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/version"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create desktop app adapter (will be initialized in OnStartup)
	desktopApp := &DesktopApp{}

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "Blue Snake | AI-ready Crawler. For those who never compromise | " + version.CurrentVersion,
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup: func(ctx context.Context) {
			// Initialize the database store
			log.Println("=== Starting database initialization ===")
			st, err := store.NewStore()
			if err != nil {
				log.Printf("FATAL: Failed to initialize database: %v\n", err)
				// Show error to user and exit
				println("FATAL ERROR: Failed to initialize database. Please check logs and restart the application.")
				println("Error:", err.Error())
				os.Exit(1)
			}
			log.Println("=== Database initialized successfully ===")

			// Create Wails-specific event emitter
			emitter := NewWailsEmitter(ctx)

			// Create core app with injected dependencies
			coreApp := app.NewApp(st, emitter)

			// Initialize the already-bound desktopApp
			desktopApp.app = coreApp
			desktopApp.ctx = ctx

			// Initialize the app and server manager
			desktopApp.Startup(ctx)
		},
		Bind: []interface{}{
			desktopApp,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
