package app

import (
	"strings"
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
