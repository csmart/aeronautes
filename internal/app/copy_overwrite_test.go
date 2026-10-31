package app

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
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

// A copy whose plan mixes routes — a staged symlink and two rclone-unsafe plain
// objects on the native route — refuses a member on EACH route at the fixed
// point without aborting the unrelated member that still matches what the
// operator acknowledged. The two routes record their refusals in separate
// ascending spools, so a staged refusal flushed after a lexically-later native
// refusal no longer trips the strictly-ascending append that used to fail the
// whole job (db:944 finding 1). The staged refusal also keeps the engine's
// unresolved effect end to end: the admitted-item validator accepts it (the job
// would not complete otherwise), the totals count it, and the DTO the UI reads
// reports it (db:944 finding 2).
func TestCopyOverwriteMixedRouteRefusalsDoNotAbortAdmittedCopies(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	source := h.transfer.connections.ActiveBookmarkID()
	dest, mock := secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")

	// "a-link" is a safe-named Swift symlink, so it routes staged and its refusal
	// runs through the staged symlink commit. 'a' sorts before the native names.
	seedObject(h.mock, "media", "target", "symlink target bytes", nil)
	h.mock.PutObject("media", "a-link", swifttest.Object{
		Bytes: 0, ContentType: "text/plain", SymlinkTarget: "media/target",
	})
	seedObject(mock, "backup", "a-link", "old link dest", nil)

	// Two rclone-unsafe names (a newline) route native. "weird\nkeep" is left
	// untouched so it replaces cleanly; "weird\nrefuse" is changed under the
	// acknowledgement so the native route refuses it, appending its name to the
	// immediate spool before the staged flush appends the smaller "a-link".
	keep := "weird\nkeep"
	refuseNative := "weird\nrefuse"
	seedObject(h.mock, "media", keep, "new keep content", nil)
	seedObject(mock, "backup", keep, "old keep dest", nil)
	seedObject(h.mock, "media", refuseNative, "new refuse content", nil)
	seedObject(mock, "backup", refuseNative, "old refuse dest", nil)
	h.installEngine(nil)

	// While the operator answers, a racing writer changes the two destinations
	// that must refuse. The collision SET is unchanged — the same names still
	// collide — so the whole-plan admission passes and each member reaches its
	// own fixed-point comparison.
	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true, before: func() {
		seedObject(mock, "backup", "a-link", "changed link dest", nil)
		seedObject(mock, "backup", refuseNative, "changed refuse dest", nil)
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
	// Refusals make the job partial, but partial is reached by COMPLETING the
	// admitted members, not by aborting them: the pre-fix broken-spool latch
	// failed the whole job instead. The symlink's target is a fourth member, a
	// plain new name that also copies, so two members copy and two refuse.
	if job.State != "partial" {
		t.Fatalf("mixed-route refusals did not finish as a partial job: %+v", job)
	}
	if job.Counters.Overwrites != 1 || job.Counters.OverwritesRefused != 2 ||
		job.Counters.ObjectsCopied != 2 || job.Counters.ObjectsVerified != 2 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("mixed-route counters wrong: %+v", job.Counters)
	}

	// The unrelated admitted member actually holds its source content, and the
	// two refused destinations were left exactly as the racer wrote them.
	witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
	snap := mirrorSnapshot(t, witness, "backup")
	if obj := snap.Containers["backup"].Objects[keep]; obj.Size != int64(len("new keep content")) {
		t.Fatalf("the unrelated admitted member was not replaced with its source content: %+v", obj)
	}
	if obj := snap.Containers["backup"].Objects["a-link"]; obj.Size != int64(len("changed link dest")) {
		t.Errorf("a refused staged symlink clobbered its destination: %+v", obj)
	}
	if obj := snap.Containers["backup"].Objects[refuseNative]; obj.Size != int64(len("changed refuse dest")) {
		t.Errorf("a refused native overwrite clobbered its destination: %+v", obj)
	}

	// Durable per-item outcomes are coherent: the completed member copied and
	// verified; the staged symlink refusal carries the unresolved effect; the
	// native refusal attempted nothing.
	items := itemsOf(t, h, id)
	if rec := items[keep]; rec.Execution == nil || rec.Execution.Result != jobs.ItemCopied ||
		rec.Verification == nil || rec.Verification.Result != jobs.ItemVerified {
		t.Fatalf("the completed member's durable outcome is wrong: %+v", rec.Execution)
	}
	if rec := items["a-link"]; rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused ||
		rec.Execution.Effect != jobs.EffectUnresolved {
		t.Fatalf("the staged symlink refusal did not record refused+unresolved: %+v", rec.Execution)
	}
	if rec := items[refuseNative]; rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused ||
		rec.Execution.Effect != jobs.EffectNotAttempted {
		t.Fatalf("the native refusal did not record refused+not_attempted: %+v", rec.Execution)
	}

	// The DTO the UI reads reports the same effect, so the unresolved-effects
	// surface keys off a real value rather than a flattened not_attempted.
	page, err := h.jobsSvc.Items(t.Context(), source, id, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	effects := map[string]string{}
	for _, it := range page.Items {
		if it.Execution != nil {
			effects[it.Key] = it.Execution.Effect
		}
	}
	if effects["a-link"] != string(jobs.EffectUnresolved) {
		t.Errorf("the staged symlink refusal effect did not reach the DTO: %q", effects["a-link"])
	}
}

// ADR 0087 decision 8: OverwritesRefused keeps its meaning and counts BOTH kinds
// of refusal under one number, while the item record distinguishes them. A
// replacement nobody acknowledged is a planning-stage refusal that records no
// found identity; a replacement whose fixed-point comparison failed is a
// commit-stage conflict that records the identity that stood in its way. A test
// pins the distinction so a later change cannot quietly merge the two kinds and
// change what every historical count means.
func TestOverwritesRefusedDistinguishesItsTwoKinds(t *testing.T) {
	// Kind one: an unacknowledged copy over a live destination. The planner
	// refuses it before staging, and nothing was acknowledged, so the item
	// carries no found identity.
	t.Run("unacknowledged is a planning refusal with no found identity", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		source := h.transfer.connections.ActiveBookmarkID()
		dest, mock := secondCluster(t, h)
		h.mock.AddContainer("media", "standard")
		mock.AddContainer("backup", "standard")
		seedObject(h.mock, "media", "doc", "source replacement", nil)
		seedObject(mock, "backup", "doc", "live destination", nil)
		h.installEngine(nil)

		id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
			SourceBookmarkID: source, SourceContainer: "media",
			DestBookmarkID: dest, DestContainer: "backup",
		})
		if err != nil {
			t.Fatal(err)
		}
		job := awaitTerminal(t, h, id)
		if job.Counters.OverwritesRefused != 1 {
			t.Fatalf("unacknowledged collision was not counted as one refusal: %+v", job.Counters)
		}
		rec := itemsOf(t, h, id)["doc"]
		if rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused {
			t.Fatalf("unacknowledged collision was not refused: %+v", rec.Execution)
		}
		if rec.Execution.Diagnostic.Stage != jobs.StagePlanning || rec.Execution.Diagnostic.Code != jobs.CodeUnsupported {
			t.Errorf("planning refusal lost its distinguishing diagnostic: %+v", rec.Execution.Diagnostic)
		}
		// itemsOf read and validated the item's predecessor header, so a nil
		// field is a genuine absence. A second ReadPredecessor here would hide a
		// lookup error behind ok=false, which it also returns on a read error.
		if rec.Predecessor != nil {
			t.Errorf("a planning refusal recorded a found identity it never read: %+v", rec.Predecessor)
		}
	})

	// Kind two: an acknowledged copy whose destination changes under the
	// acknowledgement. The fixed-point comparison refuses it at the commit stage
	// and records the identity that stood in its way.
	t.Run("fixed-point is a commit conflict with a found identity", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		source := h.transfer.connections.ActiveBookmarkID()
		dest, mock := secondCluster(t, h)
		h.mock.AddContainer("media", "standard")
		mock.AddContainer("backup", "standard")
		seedObject(h.mock, "media", "doc", "source replacement", nil)
		seedObject(mock, "backup", "doc", "old destination", nil)
		h.installEngine(nil)

		racer := "a racer wrote this instead"
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
		job := awaitTerminal(t, h, id)
		if job.Counters.OverwritesRefused != 1 {
			t.Fatalf("changed destination was not counted as one refusal: %+v", job.Counters)
		}
		rec := itemsOf(t, h, id)["doc"]
		if rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused {
			t.Fatalf("changed destination was not refused: %+v", rec.Execution)
		}
		if rec.Execution.Diagnostic.Stage != jobs.StageCommit || rec.Execution.Diagnostic.Code != jobs.CodeConflict {
			t.Errorf("fixed-point refusal lost its distinguishing diagnostic: %+v", rec.Execution.Diagnostic)
		}
		// The found identity is the validated predecessor header itemsOf already
		// read, so assert that field rather than reading the store a second time.
		if rec.Predecessor == nil || !rec.Predecessor.Present || rec.Predecessor.Length != int64(len(racer)) {
			t.Fatalf("fixed-point refusal did not record the identity it found: %+v", rec.Predecessor)
		}
	})
}

// A copy skips a destination only when it is a full-fidelity replica (P17, ADR
// 0096). The listing cannot see user metadata, so the advisory preview counts a
// content-equal pair Same; the executor then HEADs it and skips a true replica
// but refuses one whose metadata differs, leaving the destination untouched. This
// is the preview-advisory, execution-authoritative contract ADR 0096 decision 6.
func TestCopySkipRequiresFullFidelity(t *testing.T) {
	const content = "identical bytes on both sides of the copy"

	t.Run("a full-fidelity replica is skipped", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		source := h.transfer.connections.ActiveBookmarkID()
		dest, mock := secondCluster(t, h)
		h.mock.AddContainer("media", "standard")
		mock.AddContainer("backup", "standard")
		seedObject(h.mock, "media", "doc", content, map[string]string{"Owner": "alice"})
		seedObject(mock, "backup", "doc", content, map[string]string{"Owner": "alice"})
		h.installEngine(nil)

		id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
			SourceBookmarkID: source, SourceContainer: "media",
			DestBookmarkID: dest, DestContainer: "backup",
		})
		if err != nil {
			t.Fatal(err)
		}
		job := awaitTerminal(t, h, id)
		if job.State != "complete" || job.Counters.ObjectsCopied != 0 || job.Counters.OverwritesRefused != 0 || job.Counters.ObjectsFailed != 0 {
			t.Fatalf("a full-fidelity replica was not skipped cleanly: %+v", job)
		}
		if rec := itemsOf(t, h, id)["doc"]; rec.Execution == nil || rec.Execution.Result != jobs.ItemSame {
			t.Fatalf("the replica was not recorded as skipped-equal: %+v", rec.Execution)
		}
	})

	t.Run("a content-equal object with different metadata refuses", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		source := h.transfer.connections.ActiveBookmarkID()
		dest, mock := secondCluster(t, h)
		h.mock.AddContainer("media", "standard")
		mock.AddContainer("backup", "standard")
		// Same bytes, so the listing counts the pair Same; different user metadata,
		// so the authoritative HEAD finds the destination is not a replica.
		seedObject(h.mock, "media", "doc", content, map[string]string{"Owner": "alice"})
		seedObject(mock, "backup", "doc", content, map[string]string{"Owner": "bob"})
		h.installEngine(nil)

		id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
			SourceBookmarkID: source, SourceContainer: "media",
			DestBookmarkID: dest, DestContainer: "backup",
		})
		if err != nil {
			t.Fatal(err)
		}
		job := awaitTerminal(t, h, id)
		if job.State != "partial" || job.Counters.OverwritesRefused != 1 || job.Counters.ObjectsCopied != 0 {
			t.Fatalf("a content-equal object with different metadata was not refused: %+v", job)
		}
		if rec := itemsOf(t, h, id)["doc"]; rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused {
			t.Fatalf("the fidelity difference was not recorded as a refusal: %+v", rec.Execution)
		}
		// The destination is left exactly as it was: nothing was rewritten.
		witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
		snap := mirrorSnapshot(t, witness, "backup")
		if obj := snap.Containers["backup"].Objects["doc"]; obj.Size != int64(len(content)) {
			t.Fatalf("the refused destination was changed: %+v", obj)
		}
	})
}
