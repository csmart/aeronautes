package systemtest

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/jobs"
)

// Large objects through the public service with the pinned engine, on both
// fixtures (ADR 0122 decision 4). An object larger than the chunk size
// arrives as a dynamic large object whose segments lie under its own name in
// the destination's segments container, and the listings cannot compare such
// a pair: the copy reads each side once, and the same length and entity tag
// are the same content. A large object's tag is made from its segments'
// tags, so a copy cut where the source is cut has the source's tag and is
// skipped on a second run, while a pair cut differently is treated as
// different, which costs a refusal the operator can answer.

// largeObjectChunk is the chunk size these copies ask for ("1Mi").
const largeObjectChunk = 1 << 20

// largeObjectParts are a large source's segments, cut at the chunk size.
func largeObjectParts() [][]byte {
	return [][]byte{bytes.Repeat([]byte("A"), largeObjectChunk), bytes.Repeat([]byte("B"), largeObjectChunk), bytes.Repeat([]byte("C"), 100000)}
}

func copyLargeObjects(t *testing.T, h *Harness) {
	t.Helper()
	src, dst := h.Container(h.Primary, "large-src"), h.Container(h.Secondary, "large-dst")
	parts, segments := h.Container(h.Primary, "large-src-parts"), dst+"_segments"
	h.rawPut(h.Secondary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, segments) })
	// Two sources cut at the chunk size, one dynamic and one static, over
	// the same three segments, and one plain source larger than a chunk.
	// Each has headers the engine does not copy, among them an encoding its
	// bytes are not in: an engine that decoded what it carried would fail on
	// them or change them.
	var static []string
	for i, part := range largeObjectParts() {
		name := fmt.Sprintf("cut/%08d", i)
		h.Seed(h.Primary, parts, name, part, nil)
		static = append(static, fmt.Sprintf(`{"path":"/%s/%s","etag":"%x","size_bytes":%d}`, parts, name, md5.Sum(part), len(part)))
	}
	carried := map[string]string{"X-Object-Meta-Team": "ops", "Content-Disposition": `attachment; filename="large.bin"`, "Content-Encoding": "gzip"}
	dynamic := maps.Clone(carried)
	dynamic["X-Object-Manifest"] = parts + "/cut/"
	h.Seed(h.Primary, src, "scope/cut-alike.dlo", nil, dynamic)
	h.rawPutQuery(h.Primary, "/"+src+"/scope/cut-alike.slo", "multipart-manifest=put", []byte("["+strings.Join(static, ",")+"]"), carried)
	h.Seed(h.Primary, src, "scope/plain-large.bin", []byte(strings.Repeat("abcdefg", 160000)), carried)
	names := map[string]int{"cut-alike.dlo": 3, "cut-alike.slo": 3, "plain-large.bin": 2}
	srcBefore, dstBefore := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	source := srcBefore.Containers[src].Objects
	if source["scope/cut-alike.dlo"].Class != "dlo" || source["scope/cut-alike.slo"].Class != "slo" || source["scope/plain-large.bin"].Class != "plain" {
		t.Fatalf("the source fixture is not the three kinds the case needs: %+v", source)
	}
	if h.MockPrimary == nil && source["scope/plain-large.bin"].ContentEncoding != "gzip" {
		// The in-process store keeps neither the encoding nor the
		// disposition, so only the owned Swift can show they are carried.
		t.Fatalf("the source was not seeded with its encoding: %+v", source["scope/plain-large.bin"])
	}
	trace := copyCompletionProxy(t, h, h.Secondary, "")
	storage, err := url.Parse(h.Secondary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	request := app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/", Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}}
	run := func(request app.CopyRequestDTO) copyCompletionState {
		t.Helper()
		id, err := h.Transfer.StartCopy(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		h.AwaitJob(id)
		copyCompletionAwaitPins(t, h)
		return copyCompletionRead(t, h.database, id)
	}

	// The first copy: each arrives dynamic, whole, under its own segment
	// path, with the source's headers.
	first := run(request)
	if first.Job.State != "complete" || first.Job.Counters.ObjectsCopied != 3 || first.Job.Counters.ObjectsVerified != 3 || first.Job.Counters.ObjectsFailed != 0 {
		t.Fatalf("the first copy = %s %+v (%s)", first.Job.State, first.Job.Counters, first.Job.Error)
	}
	arrived := h.SnapshotNamespace(h.Secondary)
	var allowed []string
	wantRows := map[jobs.RecoveryRecord]bool{}
	posts := map[string]int{}
	for _, write := range trace.writeSnapshot() {
		if path, ok := strings.CutPrefix(write, "POST "+storage.Path+"/"+dst+"/scope/"); ok {
			posts[path]++
		}
	}
	for name, count := range names {
		want, got := source["scope/"+name], arrived.Containers[dst].Objects["scope/"+name]
		prefix, under := strings.CutPrefix(got.Manifest, segments+"/scope/"+name+"/")
		if got.Class != "dlo" || got.Size != want.Size || got.Digest != want.Digest || len(got.Segments) != count || !under || prefix == "" {
			t.Fatalf("scope/%s arrived as %+v, want the source's bytes as a dynamic large object of %d segments under its own name", name, got, count)
		}
		// The metadata request to a dynamic manifest repeats the manifest
		// header: one that did not would have emptied the object, which
		// the digest above would show.
		meta := maps.Clone(got.Meta)
		if _, has := want.Meta["Mtime"]; !has {
			delete(meta, "Mtime")
		}
		if !maps.Equal(meta, want.Meta) || got.ContentDisposition != want.ContentDisposition || got.ContentEncoding != want.ContentEncoding || posts[name] != 1 {
			t.Fatalf("scope/%s carries %v, %q and %q after %d metadata requests, want the source's %v, %q and %q after one", name,
				got.Meta, got.ContentDisposition, got.ContentEncoding, posts[name], want.Meta, want.ContentDisposition, want.ContentEncoding)
		}
		allowed = append(allowed, dst+"/scope/"+name)
		for _, segment := range got.Segments {
			allowed = append(allowed, segment.Name)
		}
		// The job's record names where the engine put each object's segments
		// (ADR 0122 decision 8).
		wantRows[jobs.RecoveryRecord{JobID: first.Job.ID, BookmarkID: h.Secondary.BookmarkID, Kind: jobs.RecoveryCaptured,
			Container: segments, Prefix: "scope/" + name + "/" + strings.TrimSuffix(prefix, "/") + "/"}] = true
	}
	gotRows := map[jobs.RecoveryRecord]bool{}
	for _, row := range first.Rows {
		gotRows[row] = true
	}
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("the job recorded the segment paths %v, want %v", gotRows, wantRows)
	}
	h.AssertNoCollateral(srcBefore, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(dstBefore, arrived, allowed...)

	// The same copy again. The two cut where the engine cuts are the same by
	// the store's description of both names and are skipped. The plain
	// source has the tag of its bytes and its copy the tag of its segments,
	// so the pair is treated as different and is refused without the
	// acknowledgement. Nothing is sent to the destination for either.
	sentBefore := len(trace.writeSnapshot())
	second := run(request)
	if second.Job.State != "partial" || second.Job.Counters.ObjectsCopied != 0 || second.Job.Counters.OverwritesRefused != 1 ||
		second.Job.Counters.ObjectsVerified != 2 || second.Job.Counters.ObjectsFailed != 0 || !strings.Contains(second.Job.Error, "1 overwrite refused") {
		t.Fatalf("the second copy = %s %+v (%s); want two skipped and the one cut differently refused", second.Job.State, second.Job.Counters, second.Job.Error)
	}
	results := map[string]jobs.ItemResult{}
	for _, item := range second.Items {
		if item.Execution != nil {
			results[item.Key] = item.Execution.Result
		}
	}
	if want := map[string]jobs.ItemResult{"cut-alike.dlo": jobs.ItemSame, "cut-alike.slo": jobs.ItemSame, "plain-large.bin": jobs.ItemRefused}; !reflect.DeepEqual(results, want) {
		t.Fatalf("the second copy recorded %v, want %v", results, want)
	}
	if sent := trace.writeSnapshot()[sentBefore:]; len(sent) != 0 {
		t.Fatalf("the second copy sent the destination %v, want nothing", sent)
	}
	h.AssertNoCollateral(arrived, h.SnapshotNamespace(h.Secondary))

	// With the acknowledgement the question is about the one name the store
	// describes as different, and the engine, writing over its own earlier
	// copy, removes that copy's segments.
	confirmer := &serviceConfirmer{yes: true}
	app.WireOverwriteConfirmer(h.Transfer, confirmer)
	replaced := arrived.Containers[dst].Objects["scope/plain-large.bin"]
	request.AcknowledgeOverwrite = true
	third := run(request)
	if third.Job.State != "complete" || third.Job.Counters.ObjectsCopied != 1 || third.Job.Counters.Overwrites != 1 || third.Job.Counters.OverwritesRefused != 0 {
		t.Fatalf("the acknowledged copy = %s %+v (%s)", third.Job.State, third.Job.Counters, third.Job.Error)
	}
	if asked := confirmer.questions(); len(asked) != 1 || !reflect.DeepEqual(asked[0].Names, []string{"plain-large.bin"}) {
		t.Fatalf("the operator was asked %+v, want one question about the name that differs", asked)
	}
	for _, segment := range replaced.Segments {
		if status, _, err := rawRequest(h.Secondary, http.MethodHead, "/"+segment.Name, "", nil); err != nil || status != http.StatusNotFound {
			t.Errorf("a segment of the replaced copy answers %d (%v), want it gone: %s", status, err, segment.Name)
		}
	}
	now := h.SnapshotNamespace(h.Secondary).Containers[dst].Objects["scope/plain-large.bin"]
	if want := source["scope/plain-large.bin"]; now.Class != "dlo" || now.Digest != want.Digest || now.Size != want.Size || now.Manifest == replaced.Manifest || now.Meta["Team"] != "ops" {
		t.Fatalf("the replacement is %+v, want the source's bytes under a new segment path", now)
	}
}

func TestCopyLargeObjectsMock(t *testing.T) { copyLargeObjects(t, findMockHarness(t)) }

func TestCopyLargeObjectsRealSwift(t *testing.T) { copyLargeObjects(t, New(t)) }

// TestCopyOfASourceTheStoreWillNotServeRealSwift holds what an object's
// outcome is when the engine cannot read its source whole (ADR 0122 decision
// 4): the engine reports an error for that object, and the object failed with
// that error. The source is a static large object one of whose segments was
// replaced, after the manifest recorded it, with other bytes of the same
// length. The store validates a static manifest's segments as it serves them,
// so the read ends early; the in-process store does not, which is why this is
// held against the owned Swift only.
func TestCopyOfASourceTheStoreWillNotServeRealSwift(t *testing.T) {
	h := New(t)
	src, dst := h.Container(h.Primary, "unserved-src"), h.Container(h.Secondary, "unserved-dst")
	parts, segments := h.Container(h.Primary, "unserved-parts"), dst+"_segments"
	h.rawPut(h.Secondary, "/"+segments, nil, nil)
	t.Cleanup(func() { h.purge(h.Secondary, segments) })
	first, second := bytes.Repeat([]byte("A"), largeObjectChunk), bytes.Repeat([]byte("B"), largeObjectChunk)
	h.Seed(h.Primary, parts, "subject/00000000", first, nil)
	h.Seed(h.Primary, parts, "subject/00000001", second, nil)
	h.SeedSLO(h.Primary, src, "scope/subject", fmt.Sprintf(`[{"path":"/%s/subject/00000000","etag":"%x","size_bytes":%d},{"path":"/%s/subject/00000001","etag":"%x","size_bytes":%d}]`,
		parts, md5.Sum(first), len(first), parts, md5.Sum(second), len(second)))
	h.Seed(h.Primary, src, "scope/whole", []byte("a source that reads whole"), nil)
	srcBefore, dstBefore := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	h.Seed(h.Primary, parts, "subject/00000001", bytes.Repeat([]byte("C"), largeObjectChunk), nil)
	// The witness cannot read a source the store will not serve, so the
	// recorded bytes go back before the source account is read again.
	t.Cleanup(func() { h.Seed(h.Primary, parts, "subject/00000001", second, nil) })

	id, err := h.Transfer.StartCopy(t.Context(), app.CopyRequestDTO{SourceBookmarkID: h.Primary.BookmarkID, SourceContainer: src, SourcePrefix: "scope/",
		DestBookmarkID: h.Secondary.BookmarkID, DestContainer: dst, DestPrefix: "scope/", Settings: app.TransferSettingsDTO{ChunkSize: "1Mi"}})
	if err != nil {
		t.Fatal(err)
	}
	h.AwaitJob(id)
	copyCompletionAwaitPins(t, h)
	saved := copyCompletionRead(t, h.database, id)
	h.Seed(h.Primary, parts, "subject/00000001", second, nil)
	h.AssertNoCollateral(srcBefore, h.SnapshotNamespace(h.Primary))

	// The job: the object that read whole is copied, and the other failed
	// with the engine's own error for it. The engine does not say how far it
	// got, so what the failure left is unknown.
	if saved.Job.State != "partial" || saved.Job.Counters.ObjectsCopied != 1 || saved.Job.Counters.ObjectsFailed != 1 ||
		!strings.Contains(saved.Job.Error, "subject: Failed to copy") || len(saved.Items) != 2 {
		t.Fatalf("the copy = %s %+v (%s)", saved.Job.State, saved.Job.Counters, saved.Job.Error)
	}
	subject, whole := saved.Items[0], saved.Items[1]
	if subject.Key != "subject" || subject.Execution == nil || subject.Verification != nil ||
		subject.Execution.ItemObservation != (jobs.ItemObservation{Result: jobs.ItemFailed, Effect: jobs.EffectUnresolved,
			Diagnostic: jobs.ItemDiagnostic{Stage: jobs.StageEngine, Code: jobs.CodeIO}}) {
		t.Fatalf("the object the engine could not read whole was recorded as %+v", subject)
	}
	if whole.Key != "whole" || whole.Execution == nil || whole.Execution.Result != jobs.ItemCopied || whole.Verification == nil {
		t.Fatalf("the object that read whole was recorded as %+v", whole)
	}

	// The store: no object at the failed name, and nothing removed. The
	// engine leaves the segments it had written, and the job's record names
	// the path of each (ADR 0122 decision 8).
	dstAfter := h.SnapshotNamespace(h.Secondary)
	if left, exists := dstAfter.Containers[dst].Objects["scope/subject"]; exists {
		t.Fatalf("bytes the store would not serve whole became an object: %+v", left)
	}
	delta := diffOf(dstBefore, dstAfter)
	if len(delta.RemovedObjects) != 0 || len(delta.ChangedObjects) != 0 {
		t.Fatalf("the failed copy changed or removed something: %s", delta)
	}
	for _, added := range delta.AddedObjects {
		if added.Ref() == dst+"/scope/whole" {
			continue
		}
		named := false
		for _, row := range saved.Rows {
			named = named || (row.Kind == jobs.RecoveryCaptured && strings.HasPrefix(added.Ref(), row.Container+"/"+row.Prefix))
		}
		if !named || !strings.HasPrefix(added.Ref(), segments+"/scope/subject/") {
			t.Errorf("the failed copy left %s, which is not a segment of its own that the job's record names: %+v", added.Ref(), saved.Rows)
		}
	}
}
