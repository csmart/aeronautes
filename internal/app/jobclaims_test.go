package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// ADR 0105 decision 3: a sweep and a transfer conflict on one account and
// one container when one path lies inside the other, in either direction; an
// unreadable account identifier counts as any account; and two claims of one
// kind never conflict.
func TestScopeClaimOverlap(t *testing.T) {
	writing := destinationClaims("auth_a", "media", "backup/")
	for _, tc := range []struct {
		name  string
		sweep scopeClaim
		want  bool
	}{
		{"the whole container", sweepClaim("auth_a", "media", ""), true},
		{"the destination folder itself", sweepClaim("auth_a", "media", "backup/"), true},
		{"a folder inside the destination", sweepClaim("auth_a", "media", "backup/photos/"), true},
		{"the segments container", sweepClaim("auth_a", "media_segments", ""), true},
		{"a prefix beside the destination", sweepClaim("auth_a", "media", "logs/"), false},
		{"a prefix that only begins like the destination", sweepClaim("auth_a", "media", "backup-old/"), false},
		{"another container", sweepClaim("auth_a", "other", ""), false},
		{"another account, known", sweepClaim("auth_b", "media", ""), false},
		{"an account that could not be read", sweepClaim("", "media", ""), true},
	} {
		got := false
		for _, held := range writing {
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
	if writing[0].overlaps(destinationClaims("auth_a", "media", "backup/")[0]) {
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
		writer, _, err := r.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sweeper, _, err := r.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var writeErr, sweepErr error
		wg.Add(2)
		go func() { defer wg.Done(); writeErr = writer.claim(destinationClaims("auth_a", "media", "")...) }()
		go func() { defer wg.Done(); sweepErr = sweeper.claim(sweepClaim("auth_a", "media", "")) }()
		wg.Wait()
		if (writeErr == nil) == (sweepErr == nil) {
			t.Fatalf("round %d: write claim %v, sweep claim %v; want exactly one refused", round, writeErr, sweepErr)
		}
		if refused := errors.Join(writeErr, sweepErr); !errors.Is(refused, errScopeClaimed) {
			t.Fatalf("round %d: the refusal is %v", round, refused)
		}
		// Neither owner has a slot or a job row: a claim does not need one.
		writer.finishStart()
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

// refusedForClaim requires a start to have been refused for another job's
// claim with that job named, and to have created no row.
func refusedForClaim(t *testing.T, h *harness, what, holder string, rows int, err error) {
	t.Helper()
	if !errors.Is(err, errScopeClaimed) || !strings.Contains(err.Error(), "job "+holder) {
		t.Errorf("%s: error = %v, want a refusal naming job %s", what, err, holder)
	}
	if got := jobCount(t, h); got != rows {
		t.Errorf("%s: %d job rows, want %d: a refused start creates none", what, got, rows)
	}
}

// ADR 0105 decision 3 at the service boundary: while an upload is alive, paused
// included, no sweep of ours starts over the folder it writes under, in the
// container or in its segments container, and no deletion of named objects
// names an object there; deletions beside it start; and the claim leaves when
// the upload's body has returned.
func TestNoDeletionStartsOverWhereALiveUploadWrites(t *testing.T) {
	h, bookmark, id, _ := pausedUpload(t, false)
	del := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	ctx := context.Background()
	h.mock.AddContainer("media_segments", "standard")
	h.mock.AddContainer("other", "standard")
	seedObject(h.mock, "media", "kept.txt", "kept", nil)
	seedObject(h.mock, "media", "logs/old", "old", nil)
	rows := jobCount(t, h)
	// The upload writes under inbox/ (ADR 0122 decision 3).
	writing := "inbox/"

	for _, req := range []DeleteRequestDTO{
		{Container: "media", Mode: ModeEmpty},
		{Container: "media", Mode: ModeDeleteContainer},
		{Container: "media", Mode: ModePrefix, Prefix: writing},
		{Container: "media", Mode: ModePrefix, Prefix: writing + "photos/"},
		{Container: "media_segments", Mode: ModeEmpty},
	} {
		req.BookmarkID, req.Confirm = bookmark, deleteConfirmation(req)
		_, err := del.StartDelete(ctx, req)
		refusedForClaim(t, h, string(req.Mode)+" of "+req.Container+"/"+req.Prefix, id, rows, err)
	}
	_, err := del.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "media", Confirm: "delete 2",
		Members: []ListingRowDTO{{Kind: "object", Name: "kept.txt"}, {Kind: "object", Name: writing + "photos/.hidden"}}})
	refusedForClaim(t, h, "a selected set naming an object the upload is writing", id, rows, err)
	// A selected folder is claimed as a prefix deletion claims its prefix
	// (ADR 0093 decision 8), so one holding the upload's folder is refused.
	_, err = del.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "media", Confirm: "delete 2",
		Members: []ListingRowDTO{{Kind: "object", Name: "kept.txt"}, {Kind: "prefix", Name: writing}}})
	refusedForClaim(t, h, "a selected set naming the folder the upload is writing", id, rows, err)

	// A deletion of a manifest with its segments is refused on each of its
	// three grounds: a dynamic object whose segment namespace lies under the
	// upload's folder in the segments container, where the engine puts a
	// large object's segments; a static object one of whose reviewed entries
	// is a name under that folder; and a manifest that is itself one.
	static := func(name, segContainer, segment string) {
		seedObject(h.mock, segContainer, segment, "0123456789", nil)
		h.mock.PutObject("media", name, swifttest.Object{
			Bytes: 10, SLO: true, SLOEtag: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f", ContentType: "application/octet-stream",
			SLOManifestJSON: fmt.Sprintf(`[{"name":"/%s/%s","hash":"%s","bytes":10}]`, segContainer, segment, md5hex("0123456789")),
		})
	}
	seedObject(h.mock, "media_segments", writing+"big/2026-11-16-000000-run/000000", "segment", nil)
	h.mock.PutObject("media", "big.bin", swifttest.Object{Manifest: "media_segments/" + writing + "big/2026-11-16-000000-run/"})
	static("big.slo", "media", writing+"part")
	h.mock.AddContainer("parts", "standard")
	static(writing+"held.slo", "parts", "x/000001")
	for _, name := range []string{"big.bin", "big.slo", writing + "held.slo"} {
		_, err := del.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{BookmarkID: bookmark, Container: "media", Name: name,
			Confirm: name, Force: true, Reviewed: reviewOf(t, del, bookmark, "media", name)})
		refusedForClaim(t, h, "a segment deletion of "+name, id, rows, err)
	}

	// Beside it: a prefix that does not cover it, another container, and a
	// selected set of names elsewhere all start.
	for _, req := range []DeleteRequestDTO{
		{Container: "media", Mode: ModePrefix, Prefix: "logs/"},
		{Container: "other", Mode: ModeEmpty},
	} {
		req.BookmarkID, req.Confirm = bookmark, deleteConfirmation(req)
		started, err := del.StartDelete(ctx, req)
		if err != nil {
			t.Fatalf("%s of %s/%s beside the upload was refused: %v", req.Mode, req.Container, req.Prefix, err)
		}
		awaitTerminal(t, h, started)
	}
	started, err := del.StartSelectedDelete(ctx, SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "media", Confirm: "delete 2",
		Members: []ListingRowDTO{{Kind: "object", Name: "kept.txt"}, {Kind: "prefix", Name: "logs/"}}})
	if err != nil {
		t.Fatalf("a selected set of an ordinary name and a folder beside the upload was refused: %v", err)
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

// The other direction: while a sweep holds its claim, a copy that would write
// inside it is refused, through the bookmark the sweep uses and through a
// second bookmark that reaches the same account by another host name, and so
// is an upload into the swept folder; an upload beside the sweep starts. Two
// accounts whose identifiers differ are covered by TestScopeClaimOverlap: the
// fixture store gives every account one identifier.
func TestNoTransferWritesInsideALiveSweep(t *testing.T) {
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
	sweep, err := del.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModePrefix, Prefix: "scope/", Confirm: "delete media/scope/"})
	if err != nil {
		t.Fatal(err)
	}
	<-held
	rows := jobCount(t, h)

	// A copy into the swept prefix writes under it.
	_, err = h.transfer.StartCopy(ctx, CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "elsewhere/",
		DestBookmarkID: bookmark, DestContainer: "media", DestPrefix: "scope/in/"})
	refusedForClaim(t, h, "a copy into the swept prefix", sweep, rows, err)

	// The same account through a second bookmark on another host name.
	alias, err := h.bookmarks.Save(ctx, BookmarkDTO{DisplayName: "alias",
		AuthURL: strings.Replace(h.authURL, "127.0.0.1", "localhost", 1), Username: "test:tester"}, "testing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.transfer.StartCopy(ctx, CopyRequestDTO{SourceBookmarkID: bookmark, SourceContainer: "media", SourcePrefix: "elsewhere/",
		DestBookmarkID: alias.ID, DestContainer: "media", DestPrefix: "scope/in/"})
	refusedForClaim(t, h, "a copy through an alias of the account", sweep, rows, err)

	// An upload writes under its destination folder: into the swept prefix
	// it is refused, and beside it it starts.
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
	picked := func(prefix string) string {
		t.Helper()
		pick, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix})
		if err != nil || pick == nil || pick.Token == "" {
			t.Fatalf("pick = %+v, %v", pick, err)
		}
		return pick.Token
	}
	_, err = h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", SelectionToken: picked("scope/")})
	refusedForClaim(t, h, "an upload into the swept prefix", sweep, rows, err)
	uploadID, err := h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "beside/", SelectionToken: picked("beside/")})
	if err != nil {
		t.Fatalf("an upload beside the sweep was refused: %v", err)
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
	refusedForClaim(t, h, "an upload into the swept container", sweep, rows, err)

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

// The rule a sweep that takes segments asks of each segment: a name under
// the folder a live upload or copy is writing into is that job's, in the
// container and in its segments container alike, and a name anywhere else is
// nobody's.
func TestASegmentALiveJobIsWritingIsKept(t *testing.T) {
	h := newHarness(t)
	runner := h.transfer.runner
	uploading, _, err := runner.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer uploading.finishStart()
	if err := uploading.claim(destinationClaims("auth_test", "photos", "holiday/")...); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	uploading.jobID = "1a2b3c4d5e6f7089"
	runner.mu.Unlock()
	for _, tc := range []struct {
		container, name string
		want            bool
	}{
		{"photos_segments", "holiday/big.mov/2026-11-16-000000-run/00000000", true},
		{"photos", "holiday/small.jpg", true},
		{"photos_segments", "work/big.mov/2026-11-16-000000-run/00000000", false},
		{"other_segments", "holiday/big.mov/2026-11-16-000000-run/00000000", false},
	} {
		holder, held := runner.claimedByLiveJob("auth_test", tc.container, tc.name)
		if held != tc.want || held && holder != "job 1a2b3c4d5e6f7089" {
			t.Errorf("%s/%s: held by %q = %v, want %v", tc.container, tc.name, holder, held, tc.want)
		}
	}
}
