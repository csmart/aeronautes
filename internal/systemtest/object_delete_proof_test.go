package systemtest

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
)

// The public-service proof beyond the credited cells (ADR 0122 decision 6):
// the deletion deletes the name whatever kind of object holds it, the literal
// objects it deletes, the record it leaves, and the measured versions-location
// restoration on the owned Swift.

var textType = map[string]string{"Content-Type": "text/plain"}

// literalHeadStatus HEADs a name without following a link or expanding a
// manifest, through the raw client, so what is stored at a name can be shown
// present or gone where a logical read would follow or fail.
func literalHeadStatus(t *testing.T, h *Harness, a *Account, container, name string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead,
		a.storageURL+"/"+container+"/"+name+"?symlink=get&multipart-manifest=get", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", a.token)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func distinctIDs(ids []string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			seen[id] = true
		}
	}
	return len(seen)
}

func deleteFactsFor(t *testing.T, h *Harness, resource string) []mutationfacts.Fact {
	t.Helper()
	all, err := mutationfacts.NewStore(h.database).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []mutationfacts.Fact
	for _, f := range all {
		if f.Operation == "object.delete" && f.Resource == resource {
			out = append(out, f)
		}
	}
	return out
}

func deleteEventsFor(t *testing.T, h *Harness, resource string) []app.AuditEventDTO {
	t.Helper()
	events, err := h.Audit.RecentFor(context.Background(), h.Primary.BookmarkID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []app.AuditEventDTO
	for _, e := range events {
		if e.Operation == "object.delete" && e.Resource == resource {
			out = append(out, e)
		}
	}
	return out
}

func TestObjectDeleteDeletesManifestsAndLinksMock(t *testing.T) {
	testObjectDeleteDeletesManifestsAndLinks(t, findMockHarness(t))
}
func TestObjectDeleteDeletesManifestsAndLinksSwift(t *testing.T) {
	testObjectDeleteDeletesManifestsAndLinks(t, New(t))
}

// The deletion deletes the name it is given, whatever holds it. A manifest of
// either kind is deleted manifest-only, so its segment stays; a link in
// either representation is deleted as the link it is, so its target stays.
func testObjectDeleteDeletesManifestsAndLinks(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "kinds")
	segment := []byte("segment")
	h.Seed(h.Primary, c, "segs/0001", segment, textType)
	h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, map[string]string{"X-Object-Manifest": c + "/segs/", "Content-Type": "video/mp4"})
	sum := md5.Sum(segment)
	h.SeedSLO(h.Primary, c, "backup.slo", fmt.Sprintf(`[{"path": "/%s/segs/0001", "etag": "%s", "size_bytes": %d}]`, c, hex.EncodeToString(sum[:]), len(segment)))
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/plain.txt"})
	members := map[string]string{"video.dlo": c, "backup.slo": c, "link.txt": c}
	// The cross-account link lives in its own container: a logical read of it
	// may fail on a real Swift, which would break the snapshot of the others.
	var far string
	if h.Secondary != nil {
		far = h.Container(h.Secondary, "far")
		lc := h.Container(h.Primary, "links")
		h.Seed(h.Secondary, far, "far.txt", []byte("far"), textType)
		h.rawPut(h.Primary, "/"+lc+"/far-link", nil, map[string]string{
			"X-Symlink-Target": far + "/far.txt", "X-Symlink-Target-Account": path.Base(h.Secondary.storageURL)})
		members["far-link"] = lc
	}
	before := h.Snapshot(h.Primary, c)
	for name, container := range members {
		rec := h.DeleteObject(ctx, container, name)
		if rec.Err() != nil || rec.Outcome() != "applied" {
			t.Fatalf("%s: outcome %q err %v", name, rec.Outcome(), rec.Err())
		}
		if status := literalHeadStatus(t, h, h.Primary, container, name); status != http.StatusNotFound {
			t.Fatalf("%s is still stored after its delete: literal HEAD %d", name, status)
		}
	}
	after := h.Snapshot(h.Primary, c)
	for _, kept := range []string{"segs/0001", "plain.txt"} {
		if b, a := before.Containers[c].Objects[kept], after.Containers[c].Objects[kept]; a.Digest != b.Digest || a.Size != b.Size {
			t.Fatalf("%s changed: %+v -> %+v", kept, b, a)
		}
	}
	if far != "" {
		if status := literalHeadStatus(t, h, h.Secondary, far, "far.txt"); status != http.StatusOK {
			t.Fatalf("the cross-account link's target: literal HEAD %d", status)
		}
	}
	if h.MockPrimary != nil {
		// The segment-expanding DELETE is never sent.
		for _, line := range h.MockPrimary.RequestLog() {
			if strings.HasPrefix(line, "DELETE ") && strings.Contains(line, "multipart-manifest=delete") {
				t.Fatalf("a segment-expanding DELETE was sent: %s", line)
			}
		}
	}
}

func TestObjectDeleteLiteralObjectsMock(t *testing.T) {
	testObjectDeleteLiteralObjects(t, findMockHarness(t))
}
func TestObjectDeleteLiteralObjectsSwift(t *testing.T) { testObjectDeleteLiteralObjects(t, New(t)) }

// A zero-byte object and a directory marker reached as an object are literal
// objects and delete as such; the marker's children survive and still imply
// the folder (ADR 0092 decision 2).
func testObjectDeleteLiteralObjects(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "literal")
	h.Seed(h.Primary, c, "empty", nil, textType)
	h.rawPut(h.Primary, "/"+c+"/dir/", nil, map[string]string{"Content-Type": "application/directory"})
	h.Seed(h.Primary, c, "dir/child.txt", []byte("a child survives"), textType)
	before := h.Snapshot(h.Primary, c)
	for _, name := range []string{"empty", "dir/"} {
		rec := h.DeleteObject(ctx, c, name)
		if rec.Err() != nil || rec.Outcome() != "applied" {
			t.Fatalf("%s: outcome %q err %v", name, rec.Outcome(), rec.Err())
		}
	}
	after := h.Snapshot(h.Primary, c)
	for _, gone := range []string{"empty", "dir/"} {
		if _, still := after.Containers[c].Objects[gone]; still {
			t.Fatalf("%s still listed", gone)
		}
	}
	child, ok := after.Containers[c].Objects["dir/child.txt"]
	if !ok || child.Digest != before.Containers[c].Objects["dir/child.txt"].Digest {
		t.Fatalf("the marker's child did not survive: %+v", child)
	}
	h.AssertNoCollateral(before, after, c+"/empty", c+"/dir/")
}

func TestObjectDeleteIsRecordedMock(t *testing.T)  { testObjectDeleteIsRecorded(t, findMockHarness(t)) }
func TestObjectDeleteIsRecordedSwift(t *testing.T) { testObjectDeleteIsRecorded(t, New(t)) }

// A deletion leaves a fact and an event that carry the DELETE's transaction
// identifier and say what the store answered: accepted for an object that
// was there, and already gone for one that was not, which is counted with
// the deleted and is not a failure.
func testObjectDeleteIsRecorded(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "record")
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)

	applied := h.DeleteObject(ctx, c, "plain.txt")
	if applied.Err() != nil || applied.Outcome() != "applied" {
		t.Fatalf("plain delete: %q %v", applied.Outcome(), applied.Err())
	}
	resource := c + "/plain.txt"
	facts := deleteFactsFor(t, h, resource)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || distinctIDs(facts[0].TransIDs) < 1 {
		t.Fatalf("applied fact = %+v", facts)
	}
	events := deleteEventsFor(t, h, resource)
	if len(events) != 1 || events[0].Result != "applied" || distinctIDs(events[0].TransIDs) < 1 || !strings.Contains(events[0].Summary, "the store accepted the DELETE") {
		t.Fatalf("applied event = %+v", events)
	}
	applied.CreditApplied()

	again := h.DeleteObject(ctx, c, "plain.txt")
	if again.Err() != nil || again.Outcome() != "applied" {
		t.Fatalf("deleting a name that is already gone: %q %v", again.Outcome(), again.Err())
	}
	events = deleteEventsFor(t, h, resource)
	if len(events) != 2 || events[0].Result != "applied" || !strings.Contains(events[0].Summary, "already gone") {
		t.Fatalf("events after the second delete = %+v", events)
	}
}

// On the owned Swift, a versions location restores the archived predecessor
// when the current object is deleted and gives it up from the archive, as the
// measurement of 27 October 2026 recorded. The deletion is reported applied,
// which it is: the store accepted it, and what the store then puts at the
// name is the store's own doing, which the delete dialog says beforehand.
func TestObjectDeleteVersionsLocationRestoresThePredecessorSwift(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	archive := h.Container(h.Primary, "archive")
	c := h.Container(h.Primary, "versioned")
	h.rawPut(h.Primary, "/"+c, nil, map[string]string{"X-Versions-Location": archive})
	h.Seed(h.Primary, c, "v.txt", []byte("one"), textType)
	h.Seed(h.Primary, c, "v.txt", []byte("second"), textType)
	if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n != 1 {
		t.Fatalf("archive holds %d objects before the delete, want the predecessor", n)
	}

	result, err := h.Mutate.DeleteObject(ctx, h.ActiveScope(ctx), c, "v.txt")
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	after := h.Snapshot(h.Primary, c)
	sum := sha256.Sum256([]byte("one"))
	restored, ok := after.Containers[c].Objects["v.txt"]
	if !ok || restored.Size != 3 || restored.Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("the name holds %+v after the delete, want the first version", restored)
	}
	if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n != 0 {
		t.Fatalf("archive holds %d objects after the delete, want none: the predecessor moved back", n)
	}
	facts := deleteFactsFor(t, h, c+"/v.txt")
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied {
		t.Fatalf("fact = %+v", facts)
	}
}
