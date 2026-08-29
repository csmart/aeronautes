// Package app holds the typed services the Wails shell exposes to the
// frontend (DESIGN.md §55, ADR 0011). Services here are plain Go — no
// webview/CGO imports — so they stay unit-testable everywhere; main.go
// binds them into the application shell.
package app

import "runtime/debug"

// Info is the shell identity the frontend shows at startup; fetching it
// over the bindings bridge is the hello-window proof that the typed
// service transport works.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Wails   string `json:"wails"`
}

// AppService is the first bound service: shell identity and environment.
// Never expose secrets, tokens, or arbitrary execution here (§55).
type AppService struct {
	info Info
}

// NewAppService builds the service. version is the application version
// stamped into the binary by the release build ("dev" otherwise).
func NewAppService(version string) *AppService {
	return &AppService{info: Info{
		Name:    "Aeronautes",
		Version: version,
		Wails:   wailsVersion(),
	}}
}

// Info returns the shell identity.
func (s *AppService) Info() Info {
	return s.info
}

// wailsVersion reads the linked wails module version from the binary's
// build info, so the reported version always matches the built shell.
// Binaries that do not link wails (unit tests of this package) report
// "unknown".
func wailsVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range bi.Deps {
		if dep.Path == "github.com/wailsapp/wails/v3" {
			return dep.Version
		}
	}
	return "unknown"
}
