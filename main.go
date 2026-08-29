// Aeronautes desktop shell: assembles the Wails v3 application around the
// typed services in internal/app (DESIGN.md §55, ADR 0011). This file is
// the only place that touches the webview/CGO layer.
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is stamped by release builds via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	statePath, err := xdg.StateFile("aeronautes/state.sqlite")
	if err != nil {
		log.Fatalf("resolve state dir: %v", err)
	}
	if dev := os.Getenv("AERONAUTES_STATE_DIR"); dev != "" {
		// Dev/test override so scratch runs never touch real state.
		statePath = filepath.Join(dev, "state.sqlite")
	}
	database, err := db.Open(context.Background(), statePath)
	if err != nil {
		log.Fatalf("open state database %s: %v", statePath, err)
	}
	defer database.Close()

	// Session-only secrets for now: credentials die with the process. The
	// OS Secret Service backend is its own later commit (§65 note, ADR 0006).
	bookmarkStore := bookmarks.NewStore(database, secrets.NewSessionStore())
	connections := app.NewConnections(bookmarkStore)

	a := application.New(application.Options{
		Name:        "Aeronautes",
		Description: "Desktop operator workbench for OpenStack Swift",
		Services: []application.Service{
			application.NewService(app.NewAppService(version)),
			application.NewService(app.NewBookmarkService(bookmarkStore)),
			application.NewService(app.NewBrowseService(connections)),
			application.NewService(app.NewInspectorService(connections)),
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
