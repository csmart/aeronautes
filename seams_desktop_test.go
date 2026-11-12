//go:build !uijourney

package main

import (
	"reflect"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The default build wires the desktop's own dialogs and serves nothing of
// its own, so the UI journey stand-ins cannot be in a release (ADR 0112).
func TestDefaultBuildWiresTheDesktopSeams(t *testing.T) {
	if _, ok := nativePicker().(wailsPicker); !ok {
		t.Errorf("picker is %T", nativePicker())
	}
	if _, ok := nativePublicConfirmer().(wailsConfirmer); !ok {
		t.Errorf("public-read confirmer is %T", nativePublicConfirmer())
	}
	if _, ok := nativeOverwriteConfirmer().(wailsOverwriteConfirmer); !ok {
		t.Errorf("overwrite confirmer is %T", nativeOverwriteConfirmer())
	}
	if options, err := serveOptions(journeyServices{}); err != nil || !reflect.DeepEqual(options, application.ServerOptions{}) {
		t.Errorf("server options = %+v, %v", options, err)
	}
}
