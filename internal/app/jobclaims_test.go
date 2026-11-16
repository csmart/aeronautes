package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// ADR 0105 decision 3: a sweep and a stage conflict on one account and one
// container when one path lies inside the other, in either direction; an
// unreadable account identifier counts as any account; and two claims of one
// kind never conflict.
func TestScopeClaimOverlap(t *testing.T) {
	stage := stageClaims("auth_a", "media", "backup/")
	for _, tc := range []struct {
		name  string
		sweep scopeClaim
		want  bool
	}{
		{"the whole container", sweepClaim("auth_a", "media", ""), true},
		{"a prefix that covers the stage", sweepClaim("auth_a", "media", "backup/"), true},
		{"the stage root itself", sweepClaim("auth_a", "media", "backup/.aeronautes/stage/"), true},
		{"a prefix inside one job's stage", sweepClaim("auth_a", "media", "backup/.aeronautes/stage/1b2c/batch/"), true},
		{"the segments container", sweepClaim("auth_a", "media_segments", ""), true},
		{"a prefix beside the stage", sweepClaim("auth_a", "media", "logs/"), false},
		{"a prefix that only begins like the destination", sweepClaim("auth_a", "media", "backup-old/"), false},
		{"another container", sweepClaim("auth_a", "other", ""), false},
		{"another account, known", sweepClaim("auth_b", "media", ""), false},
		{"an account that could not be read", sweepClaim("", "media", ""), true},
	} {
		got := false
		for _, held := range stage {
			// Asked both ways round: the rule does not depend on which job
			// started first.
			if tc.sweep.overlaps(held) != held.overlaps(tc.sweep) {
				t.Errorf("%s: the overlap depends on which claim asks", tc.name)
			}
			got = got || tc.sweep.overlaps(held)
		}
		if got != tc.want {
			t.Errorf("%s: overlap = %v, want %v", tc.name, got, tc.want)
		}
	}
	if stage[0].overlaps(stageClaims("auth_a", "media", "backup/")[0]) {
		t.Error("two transfers into one place conflict by their claims")
	}
	if sweepClaim("auth_a", "media", "").overlaps(sweepClaim("auth_a", "media", "")) {
		t.Error("two sweeps of one container conflict by their claims")
	}
}

// The check and the claim are one step, so of two starts that conflict exactly
// one wins however they race, and a claim is held without a slot and leaves
// with its owner.
func TestConflictingClaimsRaceToExactlyOneAndLeaveWithTheOwner(t *testing.T) {
	r := newHarness(t).transfer.runner
	for round := range 200 {
		stager, _, err := r.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sweeper, _, err := r.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var stageErr, sweepErr error
		wg.Add(2)
		go func() { defer wg.Done(); stageErr = stager.claim(stageClaims("auth_a", "media", "")...) }()
		go func() { defer wg.Done(); sweepErr = sweeper.claim(sweepClaim("auth_a", "media", "")) }()
		wg.Wait()
		if (stageErr == nil) == (sweepErr == nil) {
			t.Fatalf("round %d: stage claim %v, sweep claim %v; want exactly one refused", round, stageErr, sweepErr)
		}
		if refused := errors.Join(stageErr, sweepErr); !errors.Is(refused, errScopeClaimed) {
			t.Fatalf("round %d: the refusal is %v", round, refused)
		}
		// Neither owner has a slot or a job row: a claim does not need one.
		stager.finishStart()
		sweeper.finishStart()
		// Both owners have left, so either claim is free again.
		next, _, err := r.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := next.claim(sweepClaim("auth_a", "media", "")); err != nil {
			t.Fatalf("round %d: a claim outlived its owner: %v", round, err)
		}
		next.finishStart()
	}
}

// jobCount is how many job rows exist, for the cases that must create none.
func jobCount(t *testing.T, h *harness) int {
	t.Helper()
	list, err := h.jobsSvc.List(context.Background(), "", false, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

// refusedForStage requires a start to have been refused for another job's
// claim with that job named, and to have created no row.
func refusedForStage(t *testing.T, h *harness, what, holder string, rows int, err error) {
	t.Helper()
	if !errors.Is(err, errScopeClaimed) || !strings.Contains(err.Error(), "job "+holder) {
		t.Errorf("%s: error = %v, want a refusal naming job %s", what, err, holder)
	}
	if got := jobCount(t, h); got != rows {
		t.Errorf("%s: %d job rows, want %d: a refused start creates none", what, got, rows)
	}
}

// ADR 0105 decision 3 at the service boundary: while an upload is alive, paused
// included, no sweep of ours starts over its stage, in the container or in its
// segments container, and no deletion of named objects names a staged object;
// deletions beside the stage start; and the claim leaves when the upload's
// body has returned.
func TestNoDeletionStartsOverALiveUploadsStage(t *testing.T) {
	h, bookmark, id, _ := pausedUpload(t, false)
	del := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media_segments", "standard")
	h.mock.AddContainer("other", "standard")
	seedObject(h.mock, "media", "kept.txt", "kept", nil)
	seedObject(h.mock, "media", "logs/old", "old", nil)
	rows := jobCount(t, h)
	staged := ".aeronautes/stage/" + id + "/batch/"

	for _, req := range []DeleteRequestDTO{
		{Container: "media", Mode: ModeEmpty},
		{Container: "media", Mode: ModeDeleteContainer},
		{Container: "media", Mode: ModePrefix, Prefix: ".aeronautes/"},
		{Container: "media", Mode: ModePrefix, Prefix: staged},
		{Container: "media_segments", Mode: ModeEmpty},
	} {
		req.BookmarkID, req.Confirm = bookmark, deleteConfirmation(req)
		_, err := del.StartDelete(ctx, req)
		refusedForStage(t, h, string(req.Mode)+" of "+req.Container+"/"+req.Prefix, id, rows, err)
	}
	_, err := del.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "media", Confirm: "2",
		Members: []ListingRowDTO{{Kind: "object", Name: "kept.txt"}, {Kind: "object", Name: staged + "photos/.hidden"}}})
	refusedForStage(t, h, "a selected set naming a staged object", id, rows, err)

	// A deletion of a manifest with its segments is refused on each of its
	// three grounds: a dynamic object whose segment namespace lies under the
	// stage root in the segments container, as every large object this
	// application uploaded has it; a static object one of whose reviewed
	// entries is a staged name; and a manifest that is itself a staged name.
	static := func(name, segContainer, segment string) {
		seedObject(h.mock, segContainer, segment, "0123456789", nil)
		h.mock.PutObject("media", name, swifttest.Object{
			Bytes: 10, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f", ContentType: "application/octet-stream",
			SLOManifestJSON: fmt.Sprintf(`[{"name":"/%s/%s","hash":"%s","bytes":10}]`, segContainer, segment, md5hex("0123456789")),
		})
	}
	seedObject(h.mock, "media_segments", ".aeronautes/stage/earlier/batch/big/000000", "segment", nil)
	h.mock.PutObject("media", "big.bin", swifttest.Object{Manifest: "media_segments/.aeronautes/stage/earlier/batch/big/"})
	static("big.slo", "media", staged+"part")
	h.mock.AddContainer("parts", "standard")
	static(staged+"held.slo", "parts", "x/000001")
	for _, name := range []string{"big.bin", "big.slo", staged + "held.slo"} {
		_, err := del.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: name,
			Confirm: name, Force: true, Reviewed: reviewOf(t, del, bookmark, "media", name)})
		refusedForStage(t, h, "a segment deletion of "+name, id, rows, err)
	}

	// Beside the stage: a prefix that does not cover it, another container,
	// and a selected set of ordinary names all start.
	for _, req := range []DeleteRequestDTO{
		{Container: "media", Mode: ModePrefix, Prefix: "logs/"},
		{Container: "other", Mode: ModeEmpty},
	} {
		req.BookmarkID, req.Confirm = bookmark, deleteConfirmation(req)
		started, err := del.StartDelete(ctx, req)
		if err != nil {
			t.Fatalf("%s of %s/%s beside the stage was refused: %v", req.Mode, req.Container, req.Prefix, err)
		}
		awaitTerminal(t, h, started)
	}
	started, err := del.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "media", Confirm: "1",
		Members: []ListingRowDTO{{Kind: "object", Name: "kept.txt"}}})
	if err != nil {
		t.Fatalf("a selected set of an ordinary name was refused: %v", err)
	}
	awaitTerminal(t, h, started)

	// The upload ends, and its claim goes when its body has returned, which
	// is a moment after its row is terminal.
	if err := h.transfer.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	awaitTerminal(t, h, id)
	deadline := time.Now().Add(terminalWait)
	for {
		started, err = del.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Confirm: "empty media"})
		if err == nil {
			break
		}
		if !errors.Is(err, errScopeClaimed) || time.Now().After(deadline) {
			t.Fatalf("the claim did not leave with the upload: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	awaitTerminal(t, h, started)
}

// The other direction: while a sweep holds its claim, a copy whose stage would
// lie inside it is refused, through the bookmark the sweep uses and through a
// second bookmark that reaches the same account by another host name; a
// transfer whose stage is beside the sweep starts. Two accounts whose
// identifiers differ are covered by TestScopeClaimOverlap: the fixture store
// gives every account one identifier.
func TestNoTransferStagesInsideALiveSweep(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	del := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/one", "1", nil)

	// The sweep is held at its first DELETE, so it is alive with its claim.
	held, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	letGo := func() { released.Do(func() { close(release) }) }
	// A failed assertion must not leave the sweep, and the fixture's server
	// with it, waiting for ever.
	t.Cleanup(letGo)
	h.mock.BeforeRequest = func(method, path string) {
		if method == "DELETE" && strings.Contains(path, "/media/scope/") {
			once.Do(func() { close(held); <-release })
		}
	}
	sweep, err := del.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModePrefix, Prefix: "scope/", Confirm: "media/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	<-held
	rows := jobCount(t, h)

	// A copy into the swept prefix stages under it.
	_, err = h.transfer.StartCopy(ctx, CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "elsewhere/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "scope/in/"})
	refusedForStage(t, h, "a copy into the swept prefix", sweep, rows, err)

	// The same account through a second bookmark on another host name.
	alias, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "alias",
		AuthURL: strings.Replace(h.authURL, "127.0.0.1", "localhost", 1), Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.transfer.StartCopy(ctx, CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "elsewhere/",
		DestBookmarkID: alias.ID, DestContainer: "media", DestPrefix: "scope/in/"})
	refusedForStage(t, h, "a copy through an alias of the account", sweep, rows, err)

	// An upload stages at the container root, beside a sweep of scope/.
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	uploadID, err := h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", SelectionToken: picked.Token})
	if err != nil {
		t.Fatalf("an upload whose stage is beside the sweep was refused: %v", err)
	}

	letGo()
	for _, id := range []string{sweep, uploadID} {
		awaitTerminal(t, h, id)
	}
}

// A sweep of the whole container refuses an upload into it, and the refusal
// comes before the selection is consumed, so the same pick starts once the
// sweep has ended.
func TestAnUploadRefusedForASweepKeepsItsSelection(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	h.installEngine(nil)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	del := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "old", "1", nil)
	held, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	letGo := func() { released.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	h.mock.BeforeRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/media/old") {
			once.Do(func() { close(held); <-release })
		}
	}
	sweep, err := del.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModeEmpty, Confirm: "empty media"})
	if err != nil {
		t.Fatal(err)
	}
	<-held
	rows := jobCount(t, h)
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	req := UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/", SelectionToken: picked.Token}
	_, err = h.transfer.StartUpload(ctx, req)
	refusedForStage(t, h, "an upload into the swept container", sweep, rows, err)

	letGo()
	awaitTerminal(t, h, sweep)
	deadline := time.Now().Add(terminalWait)
	var uploadID string
	for {
		uploadID, err = h.transfer.StartUpload(ctx, req)
		if err == nil {
			break
		}
		if !errors.Is(err, errScopeClaimed) || time.Now().After(deadline) {
			t.Fatalf("the same selection did not start after the sweep: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if job := awaitTerminal(t, h, uploadID); job.State != string(jobs.StateComplete) {
		t.Fatalf("the upload after the sweep: %s %+v", job.State, job.Counters)
	}
}

// ADR 0105 decision 3: an ordinary live object can name segments that lie in
// the stage directory of another job that is still alive. Replacing that
// object does not reclaim them: they are kept and recorded as kept with the
// job named. The same replacement with no such job removes them, which
// TestReclamationRemovesTheRecordedSegments shows for segments elsewhere.
func TestReclamationKeepsSegmentsInAnotherLiveJobsStage(t *testing.T) {
	for _, alive := range []bool{true, false} {
		t.Run(map[bool]string{true: "the staging job is alive", false: "no job holds that stage"}[alive], func(t *testing.T) {
			f := newOverwriteFixture(t)
			const other = "0f1e2d3c4b5a6978"
			parts, _ := seedStaticLargeObjectOver(t, f.h, "replaceable", "live/taken", ".aeronautes/stage/"+other+"/batch/live/taken")
			f.tag = f.liveTag(t)
			wireReclamation(t, f.h, true)
			f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})
			if alive {
				// Another transfer into the container, admitted and named as a
				// launched job is, holding its stage claim.
				runner := f.h.transfer.runner
				owner, _, err := runner.admit(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer owner.finishStart()
				if err := owner.claim(stageClaims("auth_test", "replaceable", "")...); err != nil {
					t.Fatal(err)
				}
				runner.mu.Lock()
				owner.jobID = other
				runner.mu.Unlock()
			}
			id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
			if err != nil {
				t.Fatal(err)
			}
			if job := awaitTerminal(t, f.h, id); job.State != "complete" {
				t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
			}
			left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts))
			event := reclaimEvent(t, f.h)
			if event == nil {
				t.Fatal("the replacement left no reclamation record")
			}
			if !alive {
				if left != 0 || event.Result != audit.ResultApplied {
					t.Fatalf("with no job holding the stage: %d segments left, record %+v", left, event)
				}
				return
			}
			if left != len(parts) {
				t.Fatalf("%d of %d segments in a live job's stage were removed", len(parts)-left, len(parts))
			}
			for _, want := range []string{"removed none", "2 of the segments it named are in the staging area of job " + other, "still running"} {
				if !strings.Contains(event.Summary, want) {
					t.Errorf("the record does not say %q: %s", want, event.Summary)
				}
			}
		})
	}
}

// When the kept segments lie in the stages of two live jobs, the record names
// both, with how many are in each.
func TestReclamationNamesEveryJobWhoseStageItKept(t *testing.T) {
	f := newOverwriteFixture(t)
	const one, two = "0f1e2d3c4b5a6978", "8796a5b4c3d2e1f0"
	parts, _ := seedStaticLargeObjectParts(t, f.h, "replaceable", "live/taken",
		".aeronautes/stage/"+one+"/batch/live/taken/0000001", ".aeronautes/stage/"+two+"/batch/live/taken/0000002")
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})
	runner := f.h.transfer.runner
	for _, id := range []string{one, two} {
		owner, _, err := runner.admit(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer owner.finishStart()
		if err := owner.claim(stageClaims("auth_test", "replaceable", "")...); err != nil {
			t.Fatal(err)
		}
		runner.mu.Lock()
		owner.jobID = id
		runner.mu.Unlock()
	}
	id, err := f.h.transfer.StartUpload(t.Context(), f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "complete" {
		t.Fatalf("the replacement did not publish: %s %+v", job.State, job.Counters)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", partNames(parts)); left != len(parts) {
		t.Fatalf("%d of %d segments in live jobs' stages were removed", len(parts)-left, len(parts))
	}
	event := reclaimEvent(t, f.h)
	if event == nil {
		t.Fatal("the replacement left no reclamation record")
	}
	for _, want := range []string{"2 of the segments it named are in the staging areas of jobs that are still running", "1 in that of job " + one, "1 in that of job " + two} {
		if !strings.Contains(event.Summary, want) {
			t.Errorf("the record does not say %q: %s", want, event.Summary)
		}
	}
}

// A dynamic large object names every object under its prefix, and its
// inventory is a listing taken when the replacement is about to publish. By
// then this upload has staged its own segments, so a predecessor whose prefix
// covers this job's stage directory would list them, and reclaiming them would
// destroy the object the job had just published. Two things stand in the way.
// Staging under that prefix changes what the predecessor is, so the
// replacement is refused at its last observation and nothing is reclaimed:
// that is this test. And reclamation keeps a segment in any live job's stage
// directory, the reclaiming job's own included, for a store whose reads
// disagree: TestASegmentInAnyLiveJobsStageIsKept states that rule on its own.
func TestAPredecessorCoveringTheJobsOwnStageIsNotReplaced(t *testing.T) {
	f := newOverwriteFixture(t)
	ctx := t.Context()
	f.h.installChunkingEngine(nil, 8)
	f.h.mock.AddContainer("replaceable_segments", "standard")
	old := []string{"old/000000", "old/000001"}
	for _, name := range old {
		seedObject(f.h.mock, "replaceable_segments", name, "old bytes", nil)
	}
	// The whole segments container is this object's prefix.
	f.h.mock.PutObject("replaceable", "live/taken", swifttest.Object{Manifest: "replaceable_segments/"})
	f.tag = f.liveTag(t)
	wireReclamation(t, f.h, true)
	f.h.transfer.setOverwriteConfirmer(&recordingOverwriteConfirmer{yes: true})

	id, err := f.h.transfer.StartUpload(ctx, f.request(true))
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, f.h, id); job.State != "partial" || job.Counters.Overwrites != 0 {
		t.Fatalf("the replacement = %s %+v; want it refused and the other file published", job.State, job.Counters)
	}
	session := f.h.transfer.connections.sessions[f.bookmark].session
	if detail, err := session.ObjectDetail(ctx, "replaceable", "live/taken"); err != nil || detail.Manifest != "replaceable_segments/" {
		t.Fatalf("the predecessor is no longer at the name: %+v %v", detail, err)
	}
	if left := survivors(t, f.h, f.bookmark, "replaceable_segments", old); left != len(old) {
		t.Errorf("%d of the predecessor's %d segments were removed though it was not replaced", len(old)-left, len(old))
	}
	if event := reclaimEvent(t, f.h); event != nil {
		t.Errorf("a refused replacement left a reclamation record: %+v", event)
	}
}

// The second guard, at the rule itself: a name in a live job's own stage
// directory is that job's, whichever job asks, and a name under the stage root
// that is in no live job's directory is nobody's.
func TestASegmentInAnyLiveJobsStageIsKept(t *testing.T) {
	h := newHarness(t)
	runner := h.transfer.runner
	owner, _, err := runner.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.finishStart()
	if err := owner.claim(stageClaims("auth_test", "media", "live/")...); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	owner.jobID = "0f1e2d3c4b5a6978"
	runner.mu.Unlock()
	for name, want := range map[string]bool{
		"live/.aeronautes/stage/0f1e2d3c4b5a6978/batch/big/000000": true,
		"live/.aeronautes/stage/another/batch/big/000000":          false,
		"live/elsewhere/000000":                                    false,
	} {
		holder, held := runner.stagedByLiveJob("auth_test", "media_segments", name)
		if held != want || held && holder != "job 0f1e2d3c4b5a6978" {
			t.Errorf("%s: held by %q = %v, want %v", name, holder, held, want)
		}
	}
}
