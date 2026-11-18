package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/csmart/aeronautes/internal/app"
)

// The operator's GPU setting decides how the window's webview draws: on
// by default, and off draws in software.
func TestWebviewGPUPolicyFollowsTheSetting(t *testing.T) {
	if got := webviewGPUPolicy(app.AppSettingsDTO{GPUAcceleration: true}); got != application.WebviewGpuPolicyAlways {
		t.Fatalf("with acceleration on the policy is %v, want always", got)
	}
	if got := webviewGPUPolicy(app.AppSettingsDTO{GPUAcceleration: false}); got != application.WebviewGpuPolicyNever {
		t.Fatalf("with acceleration off the policy is %v, want never", got)
	}
}
