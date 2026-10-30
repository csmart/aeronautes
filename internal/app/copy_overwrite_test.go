package app

import (
	"strings"
	"testing"
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
