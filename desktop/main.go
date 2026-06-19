// bluesnake desktop is the Wails GUI over the same internal crawl engine the
// CLI uses. The frontend (desktop/frontend) is a Vite+React port of the
// Claude Design handoff bundle; realtime crawl progress flows over Wails
// runtime events (see session.go).
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "bluesnake",
		Width:     1320,
		Height:    860,
		MinWidth:  980,
		MinHeight: 620,
		// Window chrome is intentionally NOT frameless. Windows and Linux get their
		// standard native title bar, so minimise/maximise/restore, Win11 Snap
		// Layouts, double-click-to-maximise and every other window behaviour are
		// exactly what users expect from any native app — no hand-drawn caption
		// buttons to keep in sync. macOS instead uses TitleBarHiddenInset (below):
		// the traffic lights overlay the content and our custom title bar hosts
		// them in its left inset. The custom bar (desktop/frontend) renders as the
		// app title bar on macOS and as a plain toolbar beneath the OS title bar on
		// Windows/Linux.
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []interface{}{app},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			Appearance:           mac.DefaultAppearance,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		panic(err)
	}
}
