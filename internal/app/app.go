// Package app holds the typed services the Wails shell exposes to the
// frontend (DESIGN.md §55, ADR 0011). Services here are plain Go — no
// webview/CGO imports — so they stay unit-testable everywhere; main.go
// binds them into the application shell.
package app

import (
	"runtime/debug"

	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// Info is the shell identity the frontend shows at startup; fetching it
// over the bindings bridge is the hello-window proof that the typed
// service transport works.
type Info struct {
	BuildIdentity
	Name  string `json:"name"`
	Wails string `json:"wails"`
	// SecretBackend is "keychain" or "session" (§12.1): the UI must say
	// visibly when credentials are session-only.
	SecretBackend string `json:"secretBackend"`
	// EnabledCapabilities names the deferred capabilities this process
	// offers anyway, because the operator asked for them out of band
	// (ADR 0082). Empty in an ordinary run, and the interface says so
	// when it is not, because an enabled capability is not a proven one.
	EnabledCapabilities []string `json:"enabledCapabilities"`
	// UnrecognisedCapabilities names tokens the operator asked for that
	// name no capability, so a typo is visible rather than silent.
	UnrecognisedCapabilities []string `json:"unrecognisedCapabilities"`
}

// BuildIdentity includes unknown source state for binaries built without VCS
// information, so an unstamped binary cannot imply a clean release.
type BuildIdentity struct {
	Version        string `json:"version"`
	SourceRevision string `json:"sourceRevision"`
	SourceDirty    string `json:"sourceDirty"`
}

// AppService is the first bound service: shell identity and environment.
// Never expose secrets, tokens, or arbitrary execution here (§55).
type AppService struct {
	info Info
}

// NewAppService keeps the startup and About identity identical to --version.
func NewAppService(identity BuildIdentity, secretBackend string) *AppService {
	return &AppService{info: Info{
		BuildIdentity: identity,
		Name:          "Aeronautes",
		Wails:         wailsVersion(),
		SecretBackend: secretBackend,
	}}
}

// Info returns the shell identity, with the capability switch's current
// reading so the interface can say what this process offers (ADR 0082).
func (s *AppService) Info() Info {
	info := s.info
	info.EnabledCapabilities, info.UnrecognisedCapabilities = EnabledCapabilitySnapshot()
	return info
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

// LicenseEntry is one dependency's licence, for the About surface.
type LicenseEntry struct {
	Name    string `json:"name"`
	License string `json:"license"`
	URL     string `json:"url"`
}

// About is the §65.6 About document: identity, the pinned engine, and
// the direct dependencies with their licences. The list is curated with
// the go.mod requirements — a new direct dependency updates it in the
// same commit.
type About struct {
	Info
	RclonePin string         `json:"rclonePin"`
	License   string         `json:"license"`
	Licenses  []LicenseEntry `json:"licenses"`
}

// About returns the About document.
func (s *AppService) About() About {
	return About{
		Info:      s.Info(),
		RclonePin: rclone.PinnedVersion,
		License:   "Apache-2.0 (this application)",
		Licenses: []LicenseEntry{
			{Name: "github.com/wailsapp/wails/v3", License: "MIT", URL: "https://github.com/wailsapp/wails"},
			{Name: "github.com/ncw/swift/v2", License: "MIT", URL: "https://github.com/ncw/swift"},
			{Name: "github.com/zalando/go-keyring", License: "MIT", URL: "https://github.com/zalando/go-keyring"},
			{Name: "github.com/adrg/xdg", License: "MIT", URL: "https://github.com/adrg/xdg"},
			{Name: "modernc.org/sqlite", License: "BSD-3-Clause", URL: "https://gitlab.com/cznic/sqlite"},
			{Name: "svelte", License: "MIT", URL: "https://github.com/sveltejs/svelte"},
			{Name: "rclone (pinned subprocess, not linked)", License: "MIT", URL: "https://github.com/rclone/rclone"},
			{Name: "OpenStack Swift (server, not shipped)", License: "Apache-2.0", URL: "https://opendev.org/openstack/swift"},
		},
	}
}
