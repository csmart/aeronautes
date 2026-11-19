package app

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// twoClusters is a connected source with a container "media" and a second
// cluster with a container "backup", with the engine fake installed; seen is
// the options each engine was built with.
func twoClusters(t *testing.T) (h *harness, source, dest string, mock *swifttest.Server, seen *[]rclone.Options) {
	t.Helper()
	h = newHarness(t)
	h.connect(t)
	source = h.transfer.connections.ActiveBookmarkID()
	dest, mock = secondCluster(t, h)
	h.mock.AddContainer("media", "standard")
	mock.AddContainer("backup", "standard")
	return h, source, dest, mock, h.installEngine(nil)
}

func copyToBackup(source, dest string, replace bool) CopyRequestDTO {
	return CopyRequestDTO{SourceBookmarkID: source, SourceContainer: "media",
		DestBookmarkID: dest, DestContainer: "backup", AcknowledgeOverwrite: replace}
}

// sizesIn reads the destination through a session of its own.
func sizesIn(t *testing.T, mock *swifttest.Server, container string) map[string]int64 {
	t.Helper()
	sizes := map[string]int64{}
	for name, object := range mirrorSnapshot(t, mirrorWitness(t, mock, "test2:tester2", "testing2"), container).Containers[container].Objects {
		sizes[name] = object.Size
	}
	return sizes
}

// ADR 0122 decisions 2 and 4: a confirmed copy into a second account replaces
// what the destination holds, after one confirmation that names BOTH
// accounts, the name, and what happens to a replaced large object's old
// segments.
func TestAConfirmedCopyReplacesAfterOneQuestionNamingBothAccounts(t *testing.T) {
	h, source, dest, mock, _ := twoClusters(t)
	seedObject(h.mock, "media", "doc", "new source content", nil)
	seedObject(mock, "backup", "doc", "old destination", nil)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	h.transfer.setOverwriteConfirmer(confirmer)

	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 1 || job.Counters.Overwrites != 1 || job.Counters.OverwritesRefused != 0 ||
		job.Counters.ObjectsVerified != 1 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	asked := confirmer.questions()
	if len(asked) != 1 {
		t.Fatalf("expected exactly one confirmation, got %d", len(asked))
	}
	q := asked[0]
	// The mock's two clusters resolve to the same account suffix, so this
	// asserts the source side is stated and the copy-from sentence renders,
	// not that the two suffixes differ.
	if q.SourceAccount == "" || q.SourceBookmark == "" || q.Account == "" {
		t.Errorf("confirmation did not name the source alongside the destination: %+v", q)
	}
	if _, lines := OverwriteQuestion(q); len(lines) == 0 || !strings.Contains(lines[0], "REPLACED") || !strings.Contains(lines[0], "copy from") {
		t.Errorf("two-account question did not render the copy-from sentence: %+v", lines)
	}
	if !reflect.DeepEqual(q.Names, []string{"doc"}) || !strings.Contains(q.Fragments, "old segments") {
		t.Errorf("confirmation named %v and said %q of old segments", q.Names, q.Fragments)
	}
	if got := sizesIn(t, mock, "backup")["doc"]; got != int64(len("new source content")) {
		t.Fatalf("destination holds %d bytes, want the source's", got)
	}
}

// A copy the operator declines writes nothing.
func TestADeclinedCopyWritesNothing(t *testing.T) {
	h, source, dest, mock, seen := twoClusters(t)
	for i := range 12 {
		name := fmt.Sprintf("doc-%02d", i)
		seedObject(h.mock, "media", name, "new source content", nil)
		seedObject(mock, "backup", name, "old", nil)
	}
	confirmer := &recordingOverwriteConfirmer{yes: false}
	h.transfer.setOverwriteConfirmer(confirmer)

	_, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("declined copy was not refused: %v", err)
	}
	asked := confirmer.questions()
	if len(asked) != 1 || len(asked[0].Names) != 8 {
		t.Fatalf("want one bounded eight-name confirmation, got %+v", asked)
	}
	if title, lines := OverwriteQuestion(asked[0]); title != "Replace 12 objects?" || !strings.HasSuffix(lines[1], " and 4 more") {
		t.Fatalf("the sample lost the whole count: %q %v", title, lines)
	}
	if got := sizesIn(t, mock, "backup"); len(got) != 12 || len(*seen) != 0 {
		t.Fatalf("a declined copy left %d names and built %d engines", len(got), len(*seen))
	} else {
		for name, size := range got {
			if size != 3 {
				t.Fatalf("declined copy changed %s to %d bytes", name, size)
			}
		}
	}
}

// The confirmation asks about what will be replaced and nothing else: a name
// the listings show as different, and not a pair the listings cannot compare
// that the store describes as the same, nor a new name. A copy with nothing
// to replace asks nothing.
func TestTheCopyConfirmationAsksOnlyAboutWhatWillBeReplaced(t *testing.T) {
	h, source, dest, mock, seen := twoClusters(t)
	seedObject(h.mock, "media", "changed", "new source content", nil)
	seedObject(mock, "backup", "changed", "old", nil)
	seedObject(h.mock, "media", "empty", "", nil)
	seedObject(mock, "backup", "empty", "", nil)
	seedObject(h.mock, "media", "new", "only at the source", nil)
	confirmer := &recordingOverwriteConfirmer{yes: true}
	h.transfer.setOverwriteConfirmer(confirmer)

	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 2 || job.Counters.Overwrites != 1 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	if asked := confirmer.questions(); len(asked) != 1 || !reflect.DeepEqual(asked[0].Names, []string{"changed"}) {
		t.Fatalf("the operator was asked %+v, want one question about the changed name alone", asked)
	}
	if rec := itemsOf(t, h, id)["empty"]; rec.Execution == nil || rec.Execution.Result != jobs.ItemSame {
		t.Fatalf("the empty pair was recorded %+v, want skipped as the same", rec.Execution)
	}

	// Everything is now the same or new on neither side: nothing to ask.
	again, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
	if err != nil {
		t.Fatal(err)
	}
	// All three are left as they are and counted as already the same (ADR
	// 0122 decision 12).
	if job := awaitTerminal(t, h, again); job.State != "complete" || job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsSame != 3 {
		t.Fatalf("second job = %s %+v", job.State, job.Counters)
	}
	if asked := confirmer.questions(); len(asked) != 1 {
		t.Fatalf("a copy with nothing to replace asked a question: %+v", asked[1:])
	}

	// Unknown pairs still need their detail read before any confirmation.
	mock.FailNextObjectHEADs(1)
	engines := len(*seen)
	if id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true)); err == nil || id != "" {
		t.Fatalf("copy with unreadable comparison started: %q %v", id, err)
	}
	if len(confirmer.questions()) != 1 || len(*seen) != engines {
		t.Fatal("an unreadable comparison asked for authority or built an engine")
	}
}

// A copy that was not confirmed refuses each name the destination holds
// differently, on the engine's route and on the application's own, leaves
// those names as they were, and copies the rest: the job ends partial.
func TestACopyThatWasNotConfirmedRefusesWhatDiffersAndCopiesTheRest(t *testing.T) {
	h, source, dest, mock, _ := twoClusters(t)
	const unsafe = "weird\nname" // a newline is rclone-unsafe, so this routes native
	seedObject(h.mock, "media", "changed", "new source content", nil)
	seedObject(mock, "backup", "changed", "old", nil)
	seedObject(h.mock, "media", unsafe, "new native content", nil)
	seedObject(mock, "backup", unsafe, "old", nil)
	seedObject(h.mock, "media", "new", "only at the source", nil)

	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, false))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "partial" || job.Counters.OverwritesRefused != 2 || job.Counters.ObjectsCopied != 1 || job.Counters.ObjectsFailed != 0 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	items := itemsOf(t, h, id)
	for _, name := range []string{"changed", unsafe} {
		if rec := items[name]; rec.Execution == nil || rec.Execution.Result != jobs.ItemRefused {
			t.Errorf("%q was recorded %+v, want refused", name, rec.Execution)
		}
	}
	want := map[string]int64{"changed": 3, unsafe: 3, "new": int64(len("only at the source"))}
	if got := sizesIn(t, mock, "backup"); !reflect.DeepEqual(got, want) {
		t.Fatalf("destination sizes = %v, want %v", got, want)
	}
}

// A confirmed copy of a name the engine cannot carry replaces it on the
// application's own route.
func TestAConfirmedCopyReplacesOnTheApplicationsOwnRoute(t *testing.T) {
	h, source, dest, mock, _ := twoClusters(t)
	const unsafe = "weird\nname"
	seedObject(h.mock, "media", unsafe, "new native content", nil)
	seedObject(mock, "backup", unsafe, "old destination", nil)
	h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.Overwrites != 1 || job.Counters.NativeRouted != 1 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	if got := sizesIn(t, mock, "backup")[unsafe]; got != int64(len("new native content")) {
		t.Fatalf("destination holds %d bytes, want the source's", got)
	}
}

// The preview compares the two listings (ADR 0122 decision 4): a name they
// show with the same size and tag is skipped, also when its metadata differs,
// and nothing is written for it.
func TestACopySkipsANameTheListingsShowAsTheSame(t *testing.T) {
	const content = "identical bytes on both sides of the copy"
	h, source, dest, mock, seen := twoClusters(t)
	seedObject(h.mock, "media", "doc", content, map[string]string{"Owner": "alice"})
	seedObject(mock, "backup", "doc", content, map[string]string{"Owner": "bob"})

	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, false))
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 0 || job.Counters.OverwritesRefused != 0 || job.Counters.ObjectsFailed != 0 || job.Counters.ObjectsVerified != 1 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	if rec := itemsOf(t, h, id)["doc"]; rec.Execution == nil || rec.Execution.Result != jobs.ItemSame {
		t.Fatalf("recorded %+v, want skipped as the same", rec.Execution)
	}
	session, err := h.transfer.connections.SessionFor(t.Context(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if detail, err := session.ObjectDetail(t.Context(), "backup", "doc"); err != nil || detail.Meta["Owner"] != "bob" {
		t.Fatalf("the skipped destination reads %+v, %v; want it untouched", detail, err)
	}
	if len(*seen) != 1 {
		t.Fatalf("engines built = %d", len(*seen))
	}
}

// A copy decides once, at its start, whether the engine may remove a replaced
// large object's old segments, by the rule an upload uses (ADR 0122 decisions
// 3 and 4): not where the destination keeps versions. The confirmation says
// which, the engine is started on the same decision, and it is told to write
// at the names themselves.
func TestACopyDecidesOnceWhetherReplacedSegmentsGo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		versions bool
		keeps    bool
		says     string
	}{
		{"no versions", false, false, "the transfer engine REMOVES its old segments in backup_segments"},
		{"a versions location", true, true, "KEPT, because the container keeps versions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, source, dest, mock, seen := twoClusters(t)
			seedObject(h.mock, "media", "doc", "new source content", nil)
			seedObject(mock, "backup", "doc", "old destination", nil)
			if tc.versions {
				mock.AddContainer("archive", "standard")
				mock.EnableVersionArchive("backup", "archive", false)
			}
			confirmer := &recordingOverwriteConfirmer{yes: true}
			h.transfer.setOverwriteConfirmer(confirmer)
			id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, true))
			if err != nil {
				t.Fatal(err)
			}
			if job := awaitTerminal(t, h, id); job.State != "complete" {
				t.Fatalf("job = %s %+v", job.State, job.Counters)
			}
			if asked := confirmer.questions(); len(asked) != 1 || !strings.Contains(asked[0].Fragments, tc.says) {
				t.Fatalf("the question said %+v, want it to say %q", asked, tc.says)
			}
			if len(*seen) != 1 || (*seen)[0].KeepReplacedSegments != tc.keeps {
				t.Fatalf("engine options = %+v, want the names themselves as the destination and replaced segments kept = %v", *seen, tc.keeps)
			}
		})
	}
}

// The decision rests on one read of the destination's versioning, and a read
// that fails keeps the old segments: the engine is started with its deletes
// refused.
func TestACopyKeepsReplacedSegmentsWhenVersioningCannotBeRead(t *testing.T) {
	h, source, dest, mock, seen := twoClusters(t)
	seedObject(h.mock, "media", "doc", "new source content", nil)
	// The start reads the destination container twice: once to learn that it
	// is there, and once for its versioning. The first read's answer arms the
	// fault, so the second is the one that fails.
	var armed atomic.Bool
	mock.AfterRequest = func(method, path string) {
		if method == http.MethodHead && strings.HasSuffix(path, "/backup") && armed.CompareAndSwap(false, true) {
			mock.FailNextContainerHEADs(1)
		}
	}
	id, err := h.transfer.StartCopy(t.Context(), copyToBackup(source, dest, false))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, id); job.State != "complete" || job.Counters.ObjectsCopied != 1 {
		t.Fatalf("job = %s %+v", job.State, job.Counters)
	}
	if !armed.Load() || len(*seen) != 1 || !(*seen)[0].KeepReplacedSegments {
		t.Fatalf("fault armed = %v, engine options = %+v; want replaced segments kept after an unread versioning", armed.Load(), *seen)
	}
}
