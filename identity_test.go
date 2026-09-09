package main

import (
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestBuildIdentityReachesAbout(t *testing.T) {
	oldVersion, oldRevision, oldDirty := version, sourceRevision, sourceDirty
	t.Cleanup(func() { version, sourceRevision, sourceDirty = oldVersion, oldRevision, oldDirty })
	for _, want := range []app.BuildIdentity{
		{Version: "1.2.3", SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceDirty: "clean"},
		{Version: "2.3.4", SourceRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceDirty: "clean"},
		{Version: "dev", SourceRevision: "cccccccccccccccccccccccccccccccccccccccc", SourceDirty: "dirty"},
	} {
		version, sourceRevision, sourceDirty = want.Version, want.SourceRevision, want.SourceDirty
		got := buildIdentity()
		if got != want || app.NewAppService(got, "session").About().BuildIdentity != want {
			t.Fatalf("About identity differs from binary identity: %+v, want %+v", got, want)
		}
	}
}
