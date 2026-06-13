// bluesnake desktop is the Wails GUI over the same internal crawl engine the
// CLI uses. The frontend (desktop/frontend) is a Vite+React port of the
// Claude Design handoff bundle; realtime crawl progress flows over Wails
// runtime events (see session.go).
package main

import (
	"embed"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
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
		// macOS keeps its native traffic lights via TitleBarHiddenInset (below),
		// which lets our custom title bar (desktop/frontend) host them in its left
		// inset. Windows has no equivalent "hidden inset" style, so to keep that
		// single custom title bar — rather than stacking a native caption bar on
		// top of it — we go frameless on Windows and draw our own min/max/close
		// controls in the frontend (see .titlebar.win + WinControls).
		Frameless: runtime.GOOS == "windows",
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
		Windows: &windows.Options{
			// Keep the Win11 drop-shadow and rounded corners on the frameless
			// window (false = decorations kept). The window stays resizable via
			// the OS border; dragging/maximise are wired through --wails-draggable
			// and WinControls in the frontend.
			DisableFramelessWindowDecorations: false,
		},
	})
	if err != nil {
		panic(err)
	}
}
