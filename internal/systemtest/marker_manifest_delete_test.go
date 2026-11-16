package systemtest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
)

func TestMarkerDeleteMock(t *testing.T)  { testMarkerDelete(t, findMockHarness(t)) }
func TestMarkerDeleteSwift(t *testing.T) { testMarkerDelete(t, New(t)) }

var directoryType = map[string]string{"Content-Type": "application/directory"}

// testMarkerDelete produces the marker deletion's two required cells through
// the public service (ADR 0094 decision 2, ADR 0122 decision 6): the deletion
// of one directory marker with the objects under it and beside it unchanged,
// and a refusal that destroys nothing. The name is deleted whatever holds it,
// so a marker replaced by a real object after it was shown is deleted too.
func testMarkerDelete(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "marker-delete")
	h.Seed(h.Primary, c, "dir/", nil, directoryType)
	h.Seed(h.Primary, c, "dir/a.txt", []byte("a child survives"), textType)
	h.Seed(h.Primary, c, "other.txt", []byte("a sibling survives"), textType)
	h.Seed(h.Primary, c, "racy/", nil, directoryType)
	before := h.Snapshot(h.Primary, c)

	// Happy: exactly the marker is gone; its children are untouched.
	rec := h.DeleteMarker(ctx, c, "dir/")
	if rec.Err() != nil || rec.Outcome() != "applied" {
		t.Fatalf("outcome = %q, %v", rec.Outcome(), rec.Err())
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["dir/"]; still {
		t.Fatal("the marker is still listed after the delete")
	}
	for _, name := range []string{"dir/a.txt", "other.txt", "racy/"} {
		b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]
		if a.Digest != b.Digest || a.Size != b.Size {
			t.Fatalf("%s changed: %+v -> %+v", name, b, a)
		}
	}
	h.AssertNoCollateral(before, after, c+"/dir/")
	if facts := factsFor(t, h, "marker.delete", c+"/dir/"); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 1 {
		t.Fatalf("applied fact = %+v", facts)
	}
	if events := eventsFor(t, h, "marker.delete", c+"/dir/"); len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 1 {
		t.Fatalf("applied event = %+v", events)
	}
	rec.CreditApplied()

	// The marker was replaced by a real object after it was shown: the name
	// is deleted all the same.
	if _, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "racy/"); err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, c, "racy/", []byte("real data now"), textType)
	if replaced := h.DeleteMarker(ctx, c, "racy/"); replaced.Err() != nil || replaced.Outcome() != "applied" {
		t.Fatalf("deleting the replaced marker: outcome %q err %v", replaced.Outcome(), replaced.Err())
	}
	afterReplaced := h.Snapshot(h.Primary, c)
	if _, still := afterReplaced.Containers[c].Objects["racy/"]; still {
		t.Fatal("racy/ still listed after its delete")
	}

	// Refusal: an empty name would address the container itself, and is
	// refused before any request; nothing is destroyed.
	refused := h.DeleteMarker(ctx, c, "")
	if refused.Err() == nil {
		t.Fatal("an empty marker name was not refused")
	}
	if d := diffOf(afterReplaced, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refused.CreditRefusal()
	assertDeletesUnder(t, h, c, 2)
}

func TestManifestDeleteMock(t *testing.T)  { testManifestDelete(t, findMockHarness(t)) }
func TestManifestDeleteSwift(t *testing.T) { testManifestDelete(t, New(t)) }

// testManifestDelete produces the manifest deletion's two required cells
// through the public service (ADR 0094 decision 3, ADR 0122 decision 6):
// manifest-only deletion of a dynamic and a static large object with every
// segment and neighbour unchanged, and a refusal that destroys nothing. A
// manifest whose assembled content changed after it was shown is deleted
// like any other.
func testManifestDelete(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "manifest-delete")
	segs := h.Container(h.Primary, "manifest-delete-segs")
	segment := []byte("segment bytes")
	h.Seed(h.Primary, segs, "part-0001", segment, textType)
	h.Seed(h.Primary, segs, "part-0002", segment, textType)
	h.Seed(h.Primary, segs, "racy-0001", segment, textType)
	h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, map[string]string{"X-Object-Manifest": segs + "/part-", "Content-Type": "video/mp4"})
	sum := md5.Sum(segment)
	h.SeedSLO(h.Primary, c, "backup.slo", fmt.Sprintf(`[{"path": "/%s/part-0001", "etag": "%s", "size_bytes": %d}]`, segs, hex.EncodeToString(sum[:]), len(segment)))
	h.rawPut(h.Primary, "/"+c+"/racy.dlo", nil, map[string]string{"X-Object-Manifest": segs + "/racy-", "Content-Type": "video/mp4"})
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)
	before := h.Snapshot(h.Primary, c)

	// Happy: both manifests go; every segment stays.
	for _, name := range []string{"video.dlo", "backup.slo"} {
		rec := h.DeleteManifest(ctx, c, name)
		if rec.Err() != nil || rec.Outcome() != "applied" {
			t.Fatalf("%s: outcome = %q, %v", name, rec.Outcome(), rec.Err())
		}
		if facts := factsFor(t, h, "manifest.delete", c+"/"+name); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 1 {
			t.Fatalf("%s: applied fact = %+v", name, facts)
		}
		if events := eventsFor(t, h, "manifest.delete", c+"/"+name); len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 1 {
			t.Fatalf("%s: applied event = %+v", name, events)
		}
		if name == "video.dlo" {
			rec.CreditApplied()
		}
	}
	after := h.Snapshot(h.Primary, c)
	for _, name := range []string{"video.dlo", "backup.slo"} {
		if _, still := after.Containers[c].Objects[name]; still {
			t.Fatalf("%s is still listed after the delete", name)
		}
	}
	for _, name := range []string{"racy.dlo", "plain.txt"} {
		b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]
		if a.Digest != b.Digest || a.Size != b.Size {
			t.Fatalf("%s changed: %+v -> %+v", name, b, a)
		}
	}
	h.AssertNoCollateral(before, after, c+"/video.dlo", c+"/backup.slo")

	// The assembled content changed after the manifest was shown, because a
	// segment joined its prefix: the manifest is deleted all the same, and
	// every segment stays.
	if _, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "racy.dlo"); err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, segs, "racy-0002", segment, textType)
	segsBefore := h.Snapshot(h.Primary, segs)
	if changed := h.DeleteManifest(ctx, c, "racy.dlo"); changed.Err() != nil || changed.Outcome() != "applied" {
		t.Fatalf("deleting the changed manifest: outcome %q err %v", changed.Outcome(), changed.Err())
	}
	afterChanged := h.Snapshot(h.Primary, c)
	if _, still := afterChanged.Containers[c].Objects["racy.dlo"]; still {
		t.Fatal("racy.dlo still listed after its delete")
	}
	if d := diffOf(segsBefore, h.Snapshot(h.Primary, segs)); !d.Empty() {
		t.Fatalf("a manifest-only delete touched the segments:\n%s", d)
	}

	// Refusal: an empty name would address the container itself, and is
	// refused before any request; nothing is destroyed.
	refused := h.DeleteManifest(ctx, c, "")
	if refused.Err() == nil {
		t.Fatal("an empty manifest name was not refused")
	}
	if d := diffOf(afterChanged, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refused.CreditRefusal()
	assertDeletesUnder(t, h, c, 3)
}

// assertDeletesUnder shows, on the mock, that exactly the applied deletions
// sent an object DELETE under the container: the refusal added none.
func assertDeletesUnder(t *testing.T, h *Harness, c string, want int) {
	t.Helper()
	if h.MockPrimary == nil {
		return
	}
	deletes := 0
	for _, line := range h.MockPrimary.RequestLog() {
		if len(line) > 7 && line[:7] == "DELETE " && containsPath(line, "/"+c+"/") {
			deletes++
		}
	}
	if deletes != want {
		t.Fatalf("object DELETEs under %s = %d, want exactly the %d applied", c, deletes, want)
	}
}

func containsPath(line, path string) bool {
	for i := 0; i+len(path) <= len(line); i++ {
		if line[i:i+len(path)] == path {
			return true
		}
	}
	return false
}

func factsFor(t *testing.T, h *Harness, operation, resource string) []mutationfacts.Fact {
	t.Helper()
	all, err := mutationfacts.NewStore(h.database).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []mutationfacts.Fact
	for _, f := range all {
		if f.Operation == operation && f.Resource == resource {
			out = append(out, f)
		}
	}
	return out
}

func eventsFor(t *testing.T, h *Harness, operation, resource string) []app.AuditEventDTO {
	t.Helper()
	events, err := h.Audit.RecentFor(context.Background(), h.Primary.BookmarkID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []app.AuditEventDTO
	for _, e := range events {
		if e.Operation == operation && e.Resource == resource {
			out = append(out, e)
		}
	}
	return out
}
