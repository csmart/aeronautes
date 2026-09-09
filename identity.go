package main

import (
	"runtime/debug"

	"github.com/csmart/aeronautes/internal/app"
)

// The supported build tasks stamp these together after validating the source.
// Direct go builds retain Go's VCS information, or explicitly report unknown.
var (
	version        = "dev"
	sourceRevision = "unknown"
	sourceDirty    = "unknown"
)

func buildIdentity() app.BuildIdentity {
	i := app.BuildIdentity{Version: version, SourceRevision: sourceRevision, SourceDirty: sourceDirty}
	if i.SourceRevision != "unknown" {
		return i
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				i.SourceRevision = setting.Value
			case "vcs.modified":
				if setting.Value == "true" {
					i.SourceDirty = "dirty"
				} else if setting.Value == "false" {
					i.SourceDirty = "clean"
				}
			}
		}
	}
	return i
}
