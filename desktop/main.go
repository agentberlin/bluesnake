// acrawler desktop is the Wails GUI over the same internal crawl engine the
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
		Title:     "acrawler",
		Width:     1320,
		Height:    860,
		MinWidth:  980,
		MinHeight: 620,
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
