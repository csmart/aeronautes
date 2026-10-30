package app

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// An acknowledged copy into a second account replaces the live destination,
// after a single confirmation that names BOTH accounts.
func TestAcknowledgedCopyOverwriteReplaces(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	h.installEngine(nil)

	confirmer := &recordingOverwriteConfirmer{yes: true}
	h.transfer.setOverwriteConfirmer(confirmer)

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" {
		t.Fatalf("acknowledged overwrite did not complete: %+v", job)
	}
	if job.Counters.ObjectsCopied != 1 || job.Counters.Overwrites != 1 || job.Counters.OverwritesRefused != 0 ||
		job.Counters.ObjectsVerified != 1 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("overwrite counters wrong: %+v", job.Counters)
	}

	asked := confirmer.questions()
	if len(asked) != 1 {
		t.Fatalf("expected exactly one confirmation, got %d", len(asked))
	}
	q := asked[0]
	// The two-account path fired: the source side is stated alongside the
	// destination. The mock's two clusters resolve to the same account suffix,
	// so this asserts the source account and bookmark are populated and the
	// copy-from sentence renders, not that the two suffixes differ.
	if q.SourceAccount == "" || q.SourceBookmark == "" || q.Account == "" {
		t.Errorf("confirmation did not name the source alongside the destination: %+v", q)
	}
	if _, lines := OverwriteQuestion(q); len(lines) == 0 || !strings.Contains(lines[0], "REPLACED") || !strings.Contains(lines[0], "copy from") {
		t.Errorf("two-account question did not render the copy-from sentence: %+v", lines)
	}
	if len(q.Names) != 1 || q.Names[0] != "doc" {
		t.Errorf("confirmation did not state the replaced name: %+v", q.Names)
	}
	if !strings.Contains(q.Fragments, "kept") {
		t.Errorf("confirmation did not state that segments are kept: %q", q.Fragments)
	}

	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	obj, ok := snap.Containers["backup"].Objects["doc"]
	if !ok || obj.Size != int64(len("new source content")) {
		t.Fatalf("destination was not replaced with the source content: %+v ok=%t", obj, ok)
	}
}

// A copy the operator declines writes nothing and leaves the destination as it
// was, even though the request acknowledged the possibility of replacement.
func TestDeclinedCopyOverwriteWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	h.installEngine(nil)

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: false})

	_, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("declined copy was not refused: %v", err)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects["doc"]; obj.Size != int64(len("old destination")) {
		t.Fatalf("declined copy changed the destination: %+v", obj)
	}
}

// The object a replacement destroys is kept WITH THE ITEM, not only in the
// audit trail, and it carries the last-modified ADR 0081 decision 7 requires,
// so pruning the audit log cannot lose what was replaced (db:937 finding 4).
func TestAcknowledgedCopyOverwritePersistsPredecessorPastAuditPrune(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	h.installEngine(nil)

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.Overwrites != 1 {
		t.Fatalf("overwrite did not complete: %+v", job)
	}

	want := time.Date(2026, time.August, 28, 10, 0, 0, 0, time.UTC)
	pred, ok, err := h.transfer.jobs.ReadPredecessor(t.Context(), id, "doc")
	if err != nil || !ok {
		t.Fatalf("predecessor was not recorded with the item: ok=%t err=%v", ok, err)
	}
	if !pred.Present || pred.Length != int64(len("old destination")) || !pred.LastModified.Equal(want) {
		t.Fatalf("predecessor record is wrong: %+v", pred)
	}

	if _, err := h.transfer.runner.audit.Prune(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	after, ok, err := h.transfer.jobs.ReadPredecessor(t.Context(), id, "doc")
	if err != nil || !ok || !after.Present || after.Length != int64(len("old destination")) {
		t.Fatalf("predecessor did not survive an audit prune: %+v ok=%t err=%v", after, ok, err)
	}
}

// A replacement refused at the fixed point keeps the identity it FOUND with the
// item too, so an audit prune cannot lose which object blocked it (finding 4).
func TestRefusedCopyOverwritePersistsFoundIdentityPastAuditPrune(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	h.installEngine(nil)

	// A racing writer replaces the destination after the operator was shown the
	// baseline but before the fixed-point comparison, so the overwrite refuses
	// and the found identity is what stood in its way.
	racer := "a racer wrote different content"
	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		seedObject(mock, "backup", "doc", racer, nil)
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.Counters.OverwritesRefused != 1 || job.Counters.Overwrites != 0 {
		t.Fatalf("racing change was not refused at the fixed point: %+v", job.Counters)
	}

	found, ok, err := h.transfer.jobs.ReadPredecessor(t.Context(), id, "doc")
	if err != nil || !ok || !found.Present || found.Length != int64(len(racer)) {
		t.Fatalf("found identity was not recorded with the item: %+v ok=%t err=%v", found, ok, err)
	}
	if _, err := h.transfer.runner.audit.Prune(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	after, ok, err := h.transfer.jobs.ReadPredecessor(t.Context(), id, "doc")
	if err != nil || !ok || !after.Present || after.Length != int64(len(racer)) {
		t.Fatalf("found identity did not survive an audit prune: %+v ok=%t err=%v", after, ok, err)
	}
}

// A destination collision that appears while the operator is answering means the
// collision set changed, so the start is refused and nothing is replaced — not
// just the new member (db:937 finding 2, ADR 0087 decision 4).
func TestCopyOverwriteRefusesWhenCollisionSetChanged(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "approved", "new approved content", nil)
	seedObject(mock, "backup", "approved", "old approved", nil)
	seedObject(h.mock, "media", "sneaked", "new sneaked content", nil)
	h.installEngine(nil)

	// "sneaked" is a plain new name at confirmation and becomes a collision only
	// after it, while the operator is answering about "approved" alone.
	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		seedObject(mock, "backup", "sneaked", "appeared since", nil)
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State == "complete" || job.Counters.Overwrites != 0 {
		t.Fatalf("a changed collision set was carried instead of refused: %+v", job)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects["approved"]; obj.Size != int64(len("old approved")) {
		t.Fatalf("the approved destination was replaced despite the start refusal: %+v", obj)
	}
}

// Removing a confirmed member's source after the confirmation also changes the
// collision set, so the whole start is refused and the other confirmed
// destination is left as it was (db:938 finding 1, ADR 0087 decision 4).
func TestCopyOverwriteRefusesWhenConfirmedMemberRemoved(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "keep", "new keep content", nil)
	seedObject(mock, "backup", "keep", "old keep", nil)
	seedObject(h.mock, "media", "drop", "new drop content", nil)
	seedObject(mock, "backup", "drop", "old drop", nil)
	h.installEngine(nil)

	// Both "keep" and "drop" are confirmed collisions; "drop" loses its source
	// while the operator is answering, so it leaves the executable set.
	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		h.mock.RemoveObject("media", "drop")
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State == "complete" || job.Counters.Overwrites != 0 {
		t.Fatalf("a shrunk collision set was carried instead of refused: %+v", job)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects["keep"]; obj.Size != int64(len("old keep")) {
		t.Fatalf("the kept destination was replaced despite the start refusal: %+v", obj)
	}
}

// The whole-plan admission completes before any item is dispatched, so a
// collision added after the confirmation is caught even when the plan crosses
// the engine batch boundary, and none of the earlier members publish (db:938
// finding 1).
func TestCopyOverwriteAdmitsWholeSetAcrossBatchBoundary(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	// More new names than one engine batch (DefaultBatchNames is 500), plus one
	// confirmed collision so the confirmation dialog fires.
	for i := 0; i < 600; i++ {
		seedObject(h.mock, "media", fmt.Sprintf("new/%04d", i), "filler", nil)
	}
	seedObject(h.mock, "media", "confirmed", "new confirmed", nil)
	seedObject(mock, "backup", "confirmed", "old confirmed", nil)
	// "zzz-appeared" sorts after every filler, so the admission reads the whole
	// large plan (crossing the engine batch boundary) before it reaches this
	// unconfirmed collision. It is a plain new name at confirmation; its
	// destination appears during the dialog.
	seedObject(h.mock, "media", "zzz-appeared", "new late", nil)
	h.installEngine(nil)

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		seedObject(mock, "backup", "zzz-appeared", "appeared during the question", nil)
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State == "complete" || job.Counters.Overwrites != 0 || job.Counters.ObjectsCopied != 0 {
		t.Fatalf("a late collision across the batch boundary was not refused before dispatch: %+v", job)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects["confirmed"]; obj.Size != int64(len("old confirmed")) {
		t.Fatalf("the confirmed destination was replaced despite the start refusal: %+v", obj)
	}
	if _, ok := snap.Containers["backup"].Objects["new/0000"]; ok {
		t.Fatal("a filler create published despite the start refusal")
	}
}

// The job executes the SAME plan it admitted, not a later enumeration: a member
// whose source disappears after the admission reading is still attempted and
// recorded, rather than silently dropping out of the job's history (db:939
// finding 1, ADR 0087 decision 4).
func TestCopyOverwriteRetainsAdmittedMemberWhenSourceVanishes(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "a-keep", "new a content", nil)
	seedObject(mock, "backup", "a-keep", "old a", nil)
	seedObject(h.mock, "media", "b-vanish", "new b content", nil)
	seedObject(mock, "backup", "b-vanish", "old b", nil)
	h.installEngine(nil)

	// The confirmation lists the source before the dialog; the admission lists it
	// after. Remove b's source right after the admission's first reading of it,
	// so b is in the admitted plan but gone before it is copied. A re-enumeration
	// would list the source again and omit b; the retained plan must still
	// attempt it.
	var dialogDone, removed atomic.Bool
	h.mock.AfterRequest = func(method, path string) {
		if method == http.MethodGet && path == "/v1/AUTH_test/media" && dialogDone.Load() {
			if removed.CompareAndSwap(false, true) {
				h.mock.RemoveObject("media", "b-vanish")
			}
		}
	}

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		dialogDone.Store(true)
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	awaitTerminal(t, h, id)
	// Both admitted members are in the job's records; b did not vanish from it.
	totals, err := h.jobsSvc.ItemTotals(t.Context(), source, id)
	if err != nil {
		t.Fatal(err)
	}
	if totals.Items != 2 {
		t.Fatalf("the admitted plan was not executed; member records = %d, want 2 (a-keep and b-vanish)", totals.Items)
	}
}

// Cancelling while the admission listing is in flight produces a cancelled job
// with no writes, not an ordinary planning failure (db:939 finding 2).
func TestCopyOverwriteCancelDuringAdmissionStaysCancelled(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	h.installEngine(nil)

	// Hold the admission listing (the second source listing; the confirmation
	// made the first and returned before StartCopy did), cancel the job while it
	// is held, then release so the blocked handler can unwind.
	entered := make(chan struct{})
	gorelease := make(chan struct{})
	var dialogDone, armed atomic.Bool
	h.mock.BeforeRequest = func(method, path string) {
		// Arm only after the confirmation dialog, so the barrier holds the
		// admission listing (which runs in the background job) rather than the
		// confirmation's own terminating page, which would block StartCopy and
		// let a retry slip past the cancel this test must observe.
		if method == http.MethodGet && path == "/v1/AUTH_test/media" && dialogDone.Load() {
			if armed.CompareAndSwap(false, true) {
				close(entered)
				<-gorelease
			}
		}
	}

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		dialogDone.Store(true)
	}})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(gorelease)
		t.Fatal("the admission listing was never reached")
	}
	if err := h.transfer.CancelJob(t.Context(), id); err != nil {
		close(gorelease)
		t.Fatal(err)
	}
	close(gorelease)

	job := awaitTerminal(t, h, id)
	if job.State != "cancelled" {
		t.Fatalf("a cancel during admission was not classified as cancelled: %+v", job)
	}
	if job.Counters.Overwrites != 0 || job.Counters.ObjectsCopied != 0 {
		t.Fatalf("a cancelled admission published work: %+v", job.Counters)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects["doc"]; obj.Size != int64(len("old destination")) {
		t.Fatalf("a cancelled admission changed the destination: %+v", obj)
	}
}

// An acknowledged copy of an rclone-unsafe name replaces its live destination
// through the native route, which adds a fixed-point HEAD before its single PUT
// and keeps the predecessor with the item, exactly as the staged route does.
func TestAcknowledgedCopyOverwriteReplacesNativeRoute(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	name := "weird\nname" // a newline is rclone-unsafe, so this routes native
	seedObject(h.mock, "media", name, "new native content", nil)
	seedObject(mock, "backup", name, "old destination", nil)
	h.installEngine(nil)

	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
		SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup",
		AcknowledgeOverwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.Overwrites != 1 || job.Counters.NativeRouted != 1 {
		t.Fatalf("native overwrite did not complete via the native route: %+v", job)
	}
	pred, ok, err := h.transfer.jobs.ReadPredecessor(t.Context(), id, name)
	if err != nil || !ok || !pred.Present || pred.Length != int64(len("old destination")) {
		t.Fatalf("native overwrite did not keep the predecessor with the item: %+v ok=%t err=%v", pred, ok, err)
	}
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects[name]; obj.Size != int64(len("new native content")) {
		t.Fatalf("native destination was not replaced: %+v", obj)
	}
}
