package main

import (
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestBuildIdentityReachesAbout(t *testing.T) {
	oldVersion, oldRevision, oldDirty, oldFrom := version, sourceRevision, sourceDirty, developedFrom
	t.Cleanup(func() {
		version, sourceRevision, sourceDirty, developedFrom = oldVersion, oldRevision, oldDirty, oldFrom
	})
	for _, want := range []app.BuildIdentity{
		{Version: "1.2.3", SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceDirty: "clean"},
		{Version: "2.3.4", SourceRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceDirty: "clean"},
		{Version: "dev", SourceRevision: "cccccccccccccccccccccccccccccccccccccccc", SourceDirty: "dirty"},
		{Version: "dev", SourceRevision: "dddddddddddddddddddddddddddddddddddddddd", SourceDirty: "clean", DevelopedFrom: "1.2.3"},
	} {
		version, sourceRevision, sourceDirty, developedFrom = want.Version, want.SourceRevision, want.SourceDirty, want.DevelopedFrom
		got := buildIdentity()
		if got != want || app.NewAppService(got, "session").About().BuildIdentity != want {
			t.Fatalf("About identity differs from binary identity: %+v, want %+v", got, want)
		}
	}
}
