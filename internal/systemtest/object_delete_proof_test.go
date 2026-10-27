package systemtest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// The public-service proof ADR 0092 requires beyond the credited cells: the
// refusals for every kind the action must not touch, the literal objects it
// does delete, a conflict with both identities on record, and the measured
// versions-location restoration on the owned Swift.

var textType = map[string]string{"Content-Type": "text/plain"}

// literalHeadStatus HEADs a name without following a link or expanding a
// manifest, through the raw client, so a refused member can be shown intact
// where a logical read would follow or fail.
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

func TestObjectDeleteRefusesManifestsAndLinksMock(t *testing.T) {
	testObjectDeleteRefusesManifestsAndLinks(t, findMockHarness(t))
}
func TestObjectDeleteRefusesManifestsAndLinksSwift(t *testing.T) {
	testObjectDeleteRefusesManifestsAndLinks(t, New(t))
}

// A manifest of either kind and a link in either representation are refused
// through the public service and left intact. The mock represents a
// cross-account link unfollowed with its target header; a real Swift follows
// it or answers with its own error; both refuse.
func testObjectDeleteRefusesManifestsAndLinks(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "refuse")
	segment := []byte("segment")
	h.Seed(h.Primary, c, "segs/0001", segment, textType)
	h.rawPut(h.Primary, "/"+c+"/video.dlo", nil, map[string]string{"X-Object-Manifest": c + "/segs/", "Content-Type": "video/mp4"})
	sum := md5.Sum(segment)
	h.SeedSLO(h.Primary, c, "backup.slo", fmt.Sprintf(`[{"path": "/%s/segs/0001", "etag": "%s", "size_bytes": %d}]`, c, hex.EncodeToString(sum[:]), len(segment)))
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)
	h.Seed(h.Primary, c, "link.txt", nil, map[string]string{"X-Symlink-Target": c + "/plain.txt"})
	members := []string{"video.dlo", "backup.slo", "link.txt"}
	if h.Secondary != nil {
		sc := h.Container(h.Secondary, "far")
		h.Seed(h.Secondary, sc, "far.txt", []byte("far"), textType)
		h.rawPut(h.Primary, "/"+c+"/far-link", nil, map[string]string{
			"X-Symlink-Target": sc + "/far.txt", "X-Symlink-Target-Account": path.Base(h.Secondary.storageURL)})
		members = append(members, "far-link")
	}
	var credited bool
	for _, name := range members {
		identity := "any"
		if shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, name); err == nil {
			identity = shown.ContentFingerprint
		}
		rec := h.DeleteObject(ctx, c, name, identity)
		if rec.Err() == nil {
			t.Fatalf("%s was not refused", name)
		}
		if status := literalHeadStatus(t, h, h.Primary, c, name); status != http.StatusOK {
			t.Fatalf("%s is no longer stored after a refused delete: literal HEAD %d", name, status)
		}
		if !credited {
			rec.CreditRefusal()
			credited = true
		}
	}
	if status := literalHeadStatus(t, h, h.Primary, c, "segs/0001"); status != http.StatusOK {
		t.Fatalf("the segment is gone: %d", status)
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
		shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, name)
		if err != nil {
			t.Fatal(err)
		}
		if shown.Class != "plain" || shown.ContentLength != 0 {
			t.Fatalf("%s shown as %s of %d bytes", name, shown.Class, shown.ContentLength)
		}
		rec := h.DeleteObject(ctx, c, name, shown.ContentFingerprint)
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

func TestObjectDeleteConflictIsRecordedMock(t *testing.T) {
	testObjectDeleteConflictIsRecorded(t, findMockHarness(t))
}
func TestObjectDeleteConflictIsRecordedSwift(t *testing.T) {
	testObjectDeleteConflictIsRecorded(t, New(t))
}

// A replacement of the same length and another entity tag is refused with both
// identities in the durable fact and the audit event, and a successful delete
// leaves a fact and an event carrying transaction identifiers.
func testObjectDeleteConflictIsRecorded(t *testing.T, h *Harness) {
	t.Helper()
	ctx := context.Background()
	c := h.Container(h.Primary, "record")
	h.Seed(h.Primary, c, "racy.txt", []byte("first"), textType)
	h.Seed(h.Primary, c, "plain.txt", []byte("plain"), textType)
	shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "racy.txt")
	if err != nil {
		t.Fatal(err)
	}
	h.Seed(h.Primary, c, "racy.txt", []byte("fresh"), textType)
	replaced := h.Snapshot(h.Primary, c)

	rec := h.DeleteObject(ctx, c, "racy.txt", shown.ContentFingerprint)
	var conflict *swiftcontrol.IdentityConflictError
	if !errors.As(rec.Err(), &conflict) || rec.Outcome() != "conflict" {
		t.Fatalf("err = %v outcome = %q, want a same-length conflict", rec.Err(), rec.Outcome())
	}
	if d := diffOf(replaced, h.Snapshot(h.Primary, c)); !d.Empty() {
		t.Fatalf("a conflicting delete changed state:\n%s", d)
	}
	resource := c + "/racy.txt"
	facts := deleteFactsFor(t, h, resource)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeConflict ||
		!strings.Contains(facts[0].Summary, shown.ContentFingerprint) || !strings.Contains(facts[0].Summary, conflict.Found) {
		t.Fatalf("conflict fact = %+v", facts)
	}
	events := deleteEventsFor(t, h, resource)
	if len(events) != 1 || events[0].Result != "conflict" || !strings.Contains(events[0].Summary, conflict.Found) || len(events[0].TransIDs) == 0 {
		t.Fatalf("conflict event = %+v", events)
	}
	rec.CreditConflict()

	plain, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "plain.txt")
	if err != nil {
		t.Fatal(err)
	}
	applied := h.DeleteObject(ctx, c, "plain.txt", plain.ContentFingerprint)
	if applied.Err() != nil || applied.Outcome() != "applied" {
		t.Fatalf("plain delete: %q %v", applied.Outcome(), applied.Err())
	}
	resource = c + "/plain.txt"
	facts = deleteFactsFor(t, h, resource)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || len(facts[0].TransIDs) == 0 {
		t.Fatalf("applied fact = %+v", facts)
	}
	events = deleteEventsFor(t, h, resource)
	if len(events) != 1 || events[0].Result != "applied" || len(events[0].TransIDs) == 0 || !strings.Contains(events[0].Summary, "nothing observed") {
		t.Fatalf("applied event = %+v", events)
	}
	applied.CreditApplied()
}

// On the owned Swift, a versions location restores the archived predecessor
// when the current object is deleted and gives it up from the archive, as the
// measurement of 27 October 2026 recorded. The delete is applied with the
// predecessor observed as the successor, never as a plain deletion.
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
	shown, err := h.Inspector.Object(ctx, h.Primary.BookmarkID, c, "v.txt")
	if err != nil {
		t.Fatal(err)
	}
	if shown.ContentLength != 6 {
		t.Fatalf("shown %d bytes, want the second version", shown.ContentLength)
	}

	result, err := h.Mutate.DeleteObject(ctx, h.ActiveScope(ctx), c, "v.txt", shown.ContentFingerprint)
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if result.ObjectAfter == nil || result.ObjectAfter.ContentLength != 3 {
		t.Fatalf("the restored predecessor was not observed as the successor: %+v", result.ObjectAfter)
	}
	after := h.Snapshot(h.Primary, c)
	sum := md5.Sum([]byte("one"))
	restored, ok := after.Containers[c].Objects["v.txt"]
	if !ok || restored.Size != 3 || !strings.EqualFold(strings.Trim(result.ObjectAfter.ETag, `"`), hex.EncodeToString(sum[:])) {
		t.Fatalf("the name holds %+v after the delete, want the first version", restored)
	}
	if n := len(h.Snapshot(h.Primary, archive).Containers[archive].Objects); n != 0 {
		t.Fatalf("archive holds %d objects after the delete, want none: the predecessor moved back", n)
	}
	facts := deleteFactsFor(t, h, c+"/v.txt")
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || !strings.Contains(facts[0].Summary, "archived predecessor") {
		t.Fatalf("fact = %+v", facts)
	}
}
