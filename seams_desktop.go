//go:build !uijourney

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/secrets"
)

// The desktop's native surfaces: the dialogs a browser cannot show and the
// OS keychain. The uijourney build replaces exactly these with stand-ins a
// browser-driven test scripts (ADR 0112); every other part of the
// application is the same in both builds.

func nativePicker() app.FilePicker { return wailsPicker{} }

func nativePublicConfirmer() app.PublicACLConfirmer { return wailsConfirmer{} }

func nativeOverwriteConfirmer() app.OverwriteConfirmer { return wailsOverwriteConfirmer{} }

func nativeSecrets() (secrets.SecretStore, secrets.Backend) { return secrets.OpenDefault() }

// serveOptions has nothing to start on the desktop, where the webview is the
// only listener and Wails chooses it.
func serveOptions(journeyServices) (application.ServerOptions, error) {
	return application.ServerOptions{}, nil
}
