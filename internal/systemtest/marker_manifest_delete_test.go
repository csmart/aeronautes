package systemtest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestMarkerDeleteMock(t *testing.T)  { testMarkerDelete(t, findMockHarness(t)) }
func TestMarkerDeleteSwift(t *testing.T) { testMarkerDelete(t, New(t)) }

var directoryType = map[string]string{"Content-Type": "application/directory"}

// testMarkerDelete produces ADR 0094 decision 2's three required cells through
// the public service: the deletion of one directory marker with the objects
// under it and beside it unchanged, a refusal by the marker rule that destroys
// nothing, and an identity conflict for a marker replaced by a real object.
func testMarkerDelete(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "marker-delete")
	h.Seed(h.Primary, c, "dir/", nil, directoryType)
	h.Seed(h.Primary, c, "dir/a.txt", []byte("a child survives"), textType)
	h.Seed(h.Primary, c, "other.txt", []byte("a sibling survives"), textType)
	// An empty object in a folder's clothing: every marker's fingerprint, but
	// not a marker.
	h.Seed(h.Primary, c, "suit/", nil, textType)
	h.Seed(h.Primary, c, "racy/", nil, directoryType)
	before := h.Snapshot(h.Primary, c)

	// Happy: exactly the marker is gone; its children are untouched.
	shown, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "dir/")
	if err != nil || shown.SelfObject == nil {
		t.Fatalf("marker detail = %+v, %v", shown, err)
	}
	rec := h.DeleteMarker(ctx, c, "dir/", shown.SelfObject.ContentFingerprint)
	if rec.Err() != nil || rec.Outcome() != "applied" {
		t.Fatalf("outcome = %q, %v", rec.Outcome(), rec.Err())
	}
	after := h.Snapshot(h.Primary, c)
	if _, still := after.Containers[c].Objects["dir/"]; still {
		t.Fatal("the marker is still listed after the delete")
	}
	for _, name := range []string{"dir/a.txt", "other.txt", "suit/", "racy/"} {
		b, a := before.Containers[c].Objects[name], after.Containers[c].Objects[name]
		if a.Digest != b.Digest || a.Size != b.Size {
			t.Fatalf("%s changed: %+v -> %+v", name, b, a)
		}
	}
	h.AssertNoCollateral(before, after, c+"/dir/")
	if facts := factsFor(t, h, "marker.delete", c+"/dir/"); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 2 {
		t.Fatalf("applied fact = %+v", facts)
	}
	if events := eventsFor(t, h, "marker.delete", c+"/dir/"); len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 2 {
		t.Fatalf("applied event = %+v", events)
	}
	rec.CreditApplied()

	// Refusal: the suit shares the marker's fingerprint and is refused by the
	// rule; a blank identity is refused before anything is read; neither
	// destroys and no DELETE is sent.
	suit, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "suit/")
	if err != nil || suit.SelfObject == nil {
		t.Fatalf("suit detail = %+v, %v", suit, err)
	}
	if suit.SelfObject.ContentFingerprint != shown.SelfObject.ContentFingerprint {
		t.Fatalf("the fixture no longer shows two empty objects sharing an identity")
	}
	refused := h.DeleteMarker(ctx, c, "suit/", suit.SelfObject.ContentFingerprint)
	var conflict *swiftcontrol.IdentityConflictError
	if refused.Err() == nil || errors.As(refused.Err(), &conflict) {
		t.Fatalf("the suit was not refused by the rule: %v", refused.Err())
	}
	if blank := h.DeleteMarker(ctx, c, "racy/", ""); blank.Err() == nil {
		t.Fatal("a blank identity was not refused")
	}
	afterRefusals := h.Snapshot(h.Primary, c)
	if d := diffOf(after, afterRefusals); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refused.CreditRefusal()

	// Conflict: the marker was replaced by a real object after it was shown.
	racyShown, err := h.Inspector.Prefix(ctx, h.Primary.BookmarkID, c, "racy/")
	if err != nil || racyShown.SelfObject == nil {
		t.Fatalf("racy detail = %+v, %v", racyShown, err)
	}
	h.Seed(h.Primary, c, "racy/", []byte("real data now"), textType)
	replaced := h.Snapshot(h.Primary, c)
	conflicted := h.DeleteMarker(ctx, c, "racy/", racyShown.SelfObject.ContentFingerprint)
	if !errors.As(conflicted.Err(), &conflict) || conflicted.Outcome() != "conflict" {
		t.Fatalf("err = %v outcome = %q, want an identity conflict", conflicted.Err(), conflicted.Outcome())
	}
	if d := diffOf(replaced, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a conflicting delete changed state:\n%s", d)
	}
	conflicted.CreditConflict()
	assertDeletesUnder(t, h, c, 1)
}

func TestManifestDeleteMock(t *testing.T)  { testManifestDelete(t, findMockHarness(t)) }
func TestManifestDeleteSwift(t *testing.T) { testManifestDelete(t, New(t)) }

// testManifestDelete produces ADR 0094 decision 3's three required cells
// through the public service: manifest-only deletion of a dynamic and a
// static large object with every segment and neighbour unchanged, a refusal
// of a plain object that destroys nothing, and an identity conflict for a
// manifest whose assembled content changed after it was shown.
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
	segsBefore := h.Snapshot(h.Primary, segs)

	// Happy: both manifests go; every segment stays.
	for _, name := range []string{"video.dlo", "backup.slo"} {
		shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, name)
		if err != nil {
			t.Fatal(err)
		}
		rec := h.DeleteManifest(ctx, c, name, shown.ContentFingerprint)
		if rec.Err() != nil || rec.Outcome() != "applied" {
			t.Fatalf("%s: outcome = %q, %v", name, rec.Outcome(), rec.Err())
		}
		if facts := factsFor(t, h, "manifest.delete", c+"/"+name); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 2 {
			t.Fatalf("%s: applied fact = %+v", name, facts)
		}
		if events := eventsFor(t, h, "manifest.delete", c+"/"+name); len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 2 {
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
	if d := diffOf(segsBefore, h.Snapshot(h.Primary, segs)); !d.Empty() {
		t.Fatalf("a manifest-only delete touched the segments:\n%s", d)
	}

	// Refusal: a plain object through the manifest path, and a blank identity.
	plain, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "plain.txt")
	if err != nil {
		t.Fatal(err)
	}
	refused := h.DeleteManifest(ctx, c, "plain.txt", plain.ContentFingerprint)
	var conflict *swiftcontrol.IdentityConflictError
	if refused.Err() == nil || errors.As(refused.Err(), &conflict) {
		t.Fatalf("the plain object was not refused by the class guard: %v", refused.Err())
	}
	if blank := h.DeleteManifest(ctx, c, "racy.dlo", ""); blank.Err() == nil {
		t.Fatal("a blank identity was not refused")
	}
	if d := diffOf(after, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a refused delete changed state:\n%s", d)
	}
	refused.CreditRefusal()

	// Conflict: the assembled content changed after the manifest was shown,
	// because a segment joined its prefix.
	racyShown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "racy.dlo")
	if err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, segs, "racy-0002", segment, textType)
	replaced := h.Snapshot(h.Primary, c)
	conflicted := h.DeleteManifest(ctx, c, "racy.dlo", racyShown.ContentFingerprint)
	if !errors.As(conflicted.Err(), &conflict) || conflicted.Outcome() != "conflict" {
		t.Fatalf("err = %v outcome = %q, want an identity conflict", conflicted.Err(), conflicted.Outcome())
	}
	if d := diffOf(replaced, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a conflicting delete changed state:\n%s", d)
	}
	conflicted.CreditConflict()
	assertDeletesUnder(t, h, c, 2)
}

// assertDeletesUnder shows, on the mock, that exactly the applied deletions
// sent an object DELETE under the container: the refusals and the conflict
// added none.
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
