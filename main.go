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
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
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

	// OS keychain when reachable; otherwise session-only, surfaced in the
	// UI — never a silent downgrade (§12.1, ADR 0012).
	secretStore, secretBackend := secrets.OpenDefault()
	if secretBackend == secrets.BackendSession {
		log.Print("no OS keychain reachable: credentials are session-only and die with the process")
	}
	bookmarkStore := bookmarks.NewStore(database, secretStore)
	connections := app.NewConnections(bookmarkStore)
	connections.SetCADir(filepath.Join(filepath.Dir(statePath), "ca"))
	evidenceStore := evidence.NewStore(database)
	auditStore := audit.NewStore(database)
	jobStore := jobs.NewStore(database)
	if interrupted, err := jobStore.FailInterrupted(context.Background()); err != nil {
		log.Printf("job crash recovery: %v", err)
	} else if interrupted > 0 {
		log.Printf("job crash recovery: %d interrupted job(s) marked failed", interrupted)
	}
	jobRunner := app.NewJobRunner(jobStore, auditStore)
	transferService := app.NewTransferServiceWithRunner(connections, jobStore, jobRunner)
	transferService.SetEvidenceStore(evidenceStore)
	benchmarkService := app.NewBenchmarkService(connections, jobStore, jobRunner, database)
	transferService.SetRecommendationSource(benchmarkService.RecommendationFor)

	a := application.New(application.Options{
		Name:        "Aeronautes",
		Description: "Desktop operator workbench for OpenStack Swift",
		Services: []application.Service{
			application.NewService(app.NewAppService(version, string(secretBackend))),
			application.NewService(app.NewBookmarkService(bookmarkStore)),
			application.NewService(app.NewBrowseService(connections, evidenceStore)),
			application.NewService(app.NewInspectorService(connections, evidenceStore)),
			application.NewService(app.NewMutateService(connections, auditStore, evidenceStore)),
			application.NewService(app.NewAuditService(auditStore)),
			application.NewService(transferService),
			application.NewService(app.NewDeleteService(connections, jobStore, jobRunner, evidenceStore)),
			application.NewService(app.NewJobService(jobStore)),
			application.NewService(app.NewFindService(connections)),
			application.NewService(app.NewSettingsService(database, jobRunner)),
			application.NewService(benchmarkService),
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
