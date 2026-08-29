// Aeronautes desktop shell: assembles the Wails v3 application around the
// typed services in internal/app (DESIGN.md §55, ADR 0011). This file is
// the only place that touches the webview/CGO layer.
package main

import (
	"embed"
	"log"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is stamped by release builds via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	a := application.New(application.Options{
		Name:        "Aeronautes",
		Description: "Desktop operator workbench for OpenStack Swift",
		Services: []application.Service{
			application.NewService(app.NewAppService(version)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	a.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Aeronautes",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 600,
		// §55 release hardening: DevTools are never enabled implicitly, and
		// production builds (-tags production) additionally compile the
		// capability out; scripts/webview-spike.sh verifies the built
		// binary. The default context menu is disabled so the browser-style
		// inspect path is closed in every build mode.
		DevToolsEnabled:            false,
		DefaultContextMenuDisabled: true,
		BackgroundColour:           application.NewRGB(32, 27, 23),
		URL:                        "/",
	})

	if err := a.Run(); err != nil {
		log.Fatal(err)
	}
}
