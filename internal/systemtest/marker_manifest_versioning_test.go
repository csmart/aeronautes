package systemtest

import (
	"context"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
)

// On the owned Swift, through the public marker and manifest deletions
// (ADR 0094 proof): a versions location restores the archived plain
// predecessor when the marker or the manifest at the name is deleted, and
// gives it up from the archive; a history location takes the deleted content
// into its archive and the name stops resolving. Either way the store
// accepted the one DELETE, so the delete is applied. Children, segments and
// neighbours stay as they were.
func TestMarkerManifestDeleteUnderVersioningSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segs := h.Container(h.Primary, "mm-versioning-segs")
	h.Seed(h.Primary, segs, "part-0001", []byte("segment"), textType)
	dlo := map[string]string{"X-Object-Manifest": segs + "/part-", "Content-Type": "video/mp4"}

	t.Run("versions location restores the predecessor", func(t *testing.T) {
		archive := h.Container(h.Primary, "mm-versions-archive")
		c := h.Container(h.Primary, "mm-versions")
		h.rawPut(h.Primary, "/"+c, nil, map[string]string{"X-Versions-Location": archive})
		// Each name held a plain object first; the marker and the manifest
		// replaced it, so the archive holds a predecessor for each.
		h.Seed(h.Primary, c, "dir/", []byte("one"), textType)
		h.Seed(h.Primary, c, "dir/", nil, directoryType)
		h.Seed(h.Primary, c, "dir/child.txt", []byte("a child survives"), textType)
		h.Seed(h.Primary, c, "video.dlo", []byte("two"), textType)
		h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, dlo)
		h.Seed(h.Primary, c, "keep.txt", []byte("a neighbour survives"), textType)
		if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n != 2 {
			t.Fatalf("archive holds %d objects before the deletes, want the two predecessors", n)
		}
		before := h.Snapshot(h.Primary, c)

		marker, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "dir/")
		if err != nil || marker.SelfObject == nil || !marker.MarkerBacked {
			t.Fatalf("marker detail = %+v, %v", marker, err)
		}
		result, err := h.Mutate.DeleteMarker(ctx, h.ActiveScope(ctx), c, "dir/")
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("marker: result = %+v, err = %v; want applied", result, err)
		}
		shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "video.dlo")
		if err != nil || shown.Class != "dlo" {
			t.Fatalf("manifest detail = %+v, %v", shown, err)
		}
		result, err = h.Mutate.DeleteManifest(ctx, h.ActiveScope(ctx), c, "video.dlo")
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("manifest: result = %+v, err = %v; want applied", result, err)
		}
		after := h.Snapshot(h.Primary, c)
		for name, want := range map[string]int64{"dir/": 3, "video.dlo": 3} {
			if got := after.Containers[c].Objects[name]; got.Size != want {
				t.Fatalf("%s holds %+v after the delete, want the restored predecessor of %d bytes", name, got, want)
			}
		}
		for _, name := range []string{"dir/child.txt", "keep.txt"} {
			if b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]; a.Digest != b.Digest || a.Size != b.Size {
				t.Fatalf("%s changed: %+v -> %+v", name, b, a)
			}
		}
		if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n != 0 {
			t.Fatalf("archive holds %d objects after the deletes, want none: the predecessors moved back", n)
		}
		if status := literalHeadStatus(t, h, h.Primary, segs, "part-0001"); status != 200 {
			t.Fatalf("segment: literal HEAD %d", status)
		}
		for operation, name := range map[string]string{"marker.delete": "dir/", "manifest.delete": "video.dlo"} {
			facts := factsFor(t, h, operation, c+"/"+name)
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) != 1 {
				t.Fatalf("%s: facts = %+v", operation, facts)
			}
		}
	})

	t.Run("history location archives the deleted content", func(t *testing.T) {
		archive := h.Container(h.Primary, "mm-history-archive")
		c := h.Container(h.Primary, "mm-history")
		h.rawPut(h.Primary, "/"+c, nil, map[string]string{"X-History-Location": archive})
		h.Seed(h.Primary, c, "dir/", nil, directoryType)
		h.Seed(h.Primary, c, "dir/child.txt", []byte("a child survives"), textType)
		h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, dlo)
		h.Seed(h.Primary, c, "keep.txt", []byte("a neighbour survives"), textType)
		before := h.Snapshot(h.Primary, c)
		archived := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects)

		marker, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "dir/")
		if err != nil || marker.SelfObject == nil {
			t.Fatalf("marker detail = %+v, %v", marker, err)
		}
		result, err := h.Mutate.DeleteMarker(ctx, h.ActiveScope(ctx), c, "dir/")
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("marker: result = %+v, err = %v; want applied", result, err)
		}
		result, err = h.Mutate.DeleteManifest(ctx, h.ActiveScope(ctx), c, "video.dlo")
		if err != nil || result.Outcome != "applied" {
			t.Fatalf("manifest: result = %+v, err = %v; want applied", result, err)
		}
		after := h.Snapshot(h.Primary, c)
		for _, name := range []string{"dir/", "video.dlo"} {
			if _, still := after.Containers[c].Objects[name]; still {
				t.Fatalf("%s still listed after the delete", name)
			}
		}
		for _, name := range []string{"dir/child.txt", "keep.txt"} {
			if b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]; a.Digest != b.Digest || a.Size != b.Size {
				t.Fatalf("%s changed: %+v -> %+v", name, b, a)
			}
		}
		if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n <= archived {
			t.Fatalf("the history archive holds %d objects after the deletes, want more than %d: the content and its deletion markers move there", n, archived)
		}
		if status := literalHeadStatus(t, h, h.Primary, segs, "part-0001"); status != 200 {
			t.Fatalf("segment: literal HEAD %d", status)
		}
	})
}
