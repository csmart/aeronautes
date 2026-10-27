// Aeronautes desktop shell: assembles the Wails v3 application around the
// typed services in internal/app (DESIGN.md §55, ADR 0011). This file is
// the only place that touches the webview/CGO layer.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/adrg/xdg"
	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	identity := buildIdentity()
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if err := json.NewEncoder(os.Stdout).Encode(identity); err != nil {
			log.Fatal(err)
		}
		return
	}
	// What a transfer would spawn, or why it would refuse, found the way
	// a transfer finds it and without opening state or a window
	// (ADR 0089 decision 6). The exit status follows the answer so an
	// install check can gate on it.
	if len(os.Args) == 2 && os.Args[1] == "--engine" {
		report := rclone.Describe()
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			log.Fatal(err)
		}
		if report.Error != "" {
			os.Exit(1)
		}
		return
	}
	shutdown := &shellShutdown{}
	// A failed database close retains the lock through error reporting,
	// including GC, until the kernel releases it on process exit.
	defer runtime.KeepAlive(shutdown)
	if err := runDesktop(identity, shutdown, launchDesktop, secrets.OpenDefault); err != nil {
		log.Fatal(err)
	}
}

func runDesktop(identity app.BuildIdentity, shutdown *shellShutdown,
	launch func(application.Options) error, openSecrets func() (secrets.SecretStore, secrets.Backend)) (runErr error) {

	statePath, err := xdg.StateFile("aeronautes/state.sqlite")
	if err != nil {
		return fmt.Errorf("resolve state dir: %w", err)
	}
	if dev := os.Getenv("AERONAUTES_STATE_DIR"); dev != "" {
		// Dev/test override so scratch runs never touch real state.
		statePath = filepath.Join(dev, "state.sqlite")
	}
	// One process per state directory: a second instance would mark this
	// one's running jobs interrupted and sweep its staging litter.
	releaseLock, err := app.AcquireInstanceLock(filepath.Dir(statePath))
	if err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}
	shutdown.releaseLock = releaseLock
	defer func() { runErr = errors.Join(runErr, shutdown.close()) }()
	database, err := db.Open(context.Background(), statePath)
	if err != nil {
		return fmt.Errorf("open state database %s: %w", statePath, err)
	}
	shutdown.closeDatabase = database.Close

	// The operator's out-of-band capability switch, read once here and
	// nowhere else (ADR 0082). A deferred capability it names is offered
	// by this process although it is not proven; the interface says so.
	for _, line := range app.StartupCapabilityLines(app.ReadEnabledCapabilities()) {
		log.Print(line)
	}

	// OS keychain when reachable; otherwise session-only, surfaced in the
	// UI — never a silent downgrade (§12.1, ADR 0012).
	secretStore, secretBackend := openSecrets()
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
	findService := app.NewFindService(connections)
	mutateService := app.NewMutateService(connections, auditStore, evidenceStore)
	shutdown.drain = func(ctx context.Context) error {
		return app.ShutdownWork(ctx, jobRunner, findService, mutateService)
	}
	transferService := app.NewTransferServiceWithRunner(connections, jobStore, jobRunner)
	app.WireEvidenceStore(transferService, evidenceStore)
	app.WireFilePicker(transferService, wailsPicker{})
	app.WireConfirmer(mutateService, wailsConfirmer{})
	app.WireOverwriteConfirmer(transferService, wailsOverwriteConfirmer{})
	settingsService := app.NewSettingsService(database, jobRunner)
	app.WireSettings(transferService, settingsService)
	deleteService := app.NewDeleteService(connections, jobStore, jobRunner, evidenceStore)
	app.WireDeleteSettings(deleteService, settingsService)
	app.WireConnectionCap(connections, settingsService)
	app.RunStartupRetention(context.Background(), settingsService, jobStore, auditStore)
	benchmarkService := app.NewBenchmarkService(connections, jobStore, jobRunner, database)
	app.WireBenchmarkRecommendations(transferService, benchmarkService)

	options := application.Options{
		Name:        "Aeronautes",
		Description: "Desktop operator workbench for OpenStack Swift",
		OnShutdown:  shutdown.wait,
		Services: []application.Service{
			application.NewService(app.NewAppService(identity, string(secretBackend))),
			application.NewService(app.NewBookmarkService(bookmarkStore, connections, jobStore)),
			application.NewService(app.NewBrowseService(connections, evidenceStore)),
			application.NewService(app.NewInspectorService(connections, evidenceStore)),
			application.NewService(mutateService),
			application.NewService(app.NewAuditService(auditStore)),
			application.NewService(transferService),
			application.NewService(deleteService),
			application.NewService(app.NewJobService(jobStore)),
			application.NewService(findService),
			application.NewService(settingsService),
			application.NewService(benchmarkService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	}
	if err := launch(options); err != nil {
		return fmt.Errorf("run desktop: %w", err)
	}
	return nil
}

func launchDesktop(options application.Options) error {
	a := application.New(options)

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

	return a.Run()
}

// askNative puts one question on the Wails question dialog: a native, modal
// surface the page cannot draw (ADR 0073 option B). On Linux Show returns
// before the answer, so the buttons feed a channel; a closed or dismissed
// dialog is a REFUSAL, which is why refuse is both the cancel button and the
// default and the wait is bounded by ctx. Both confirmers go through here so
// that property is written once rather than once per question.
func askNative(ctx context.Context, title, message, refuse, accept string) (bool, error) {
	shell := application.Get()
	if shell == nil {
		return false, fmt.Errorf("application shell not running")
	}
	answer := make(chan bool, 2)
	dialog := shell.Dialog.Question().SetTitle(title).SetMessage(message)
	no := dialog.AddButton(refuse)
	no.OnClick(func() { answer <- false }).SetAsCancel()
	dialog.AddButton(accept).OnClick(func() { answer <- true })
	dialog.SetDefaultButton(no)
	dialog.Show()
	select {
	case confirmed := <-answer:
		return confirmed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// wailsConfirmer implements app.PublicACLConfirmer over that surface.
type wailsConfirmer struct{}

func (wailsConfirmer) ConfirmPublicRead(ctx context.Context, container, account string) (bool, error) {
	return askNative(ctx, "Make this container public?",
		fmt.Sprintf("Anyone on the internet will be able to read every object in %s (%s).\nThis question comes from the application, not from the page.", container, account),
		"Keep it private", "Make it public")
}

// wailsOverwriteConfirmer implements app.OverwriteConfirmer over the same
// surface. ADR 0081 decision 2 requires the acknowledgement that authorises a
// replacement to be raised by the BACKEND: the field on the request guards
// against an operator's accident, and a page that sets it still cannot answer
// its own question, because this is never reachable as a bridge action.
//
// The wording is app.OverwriteQuestion's, asserted in that package rather
// than described here, so this file holds no sentence of its own.
type wailsOverwriteConfirmer struct{}

func (wailsOverwriteConfirmer) ConfirmOverwrite(ctx context.Context, about app.OverwriteConfirmation) (bool, error) {
	title, lines := app.OverwriteQuestion(about)
	return askNative(ctx, title, strings.Join(lines, "\n"), "Keep what is there", "Replace")
}

// wailsPicker implements app.FilePicker over the Wails dialog manager —
// the only place dialogs touch the webview layer (ADR 0011: internal/app
// stays plain Go).
type wailsPicker struct{}

func (wailsPicker) PickFiles(title string) ([]string, error) {
	shell := application.Get()
	if shell == nil {
		return nil, fmt.Errorf("application shell not running")
	}
	return shell.Dialog.OpenFile().CanChooseFiles(true).SetTitle(title).PromptForMultipleSelection()
}

func (wailsPicker) PickDirectory(title string) (string, error) {
	shell := application.Get()
	if shell == nil {
		return "", fmt.Errorf("application shell not running")
	}
	return shell.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).SetTitle(title).PromptForSingleSelection()
}
