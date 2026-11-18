package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0074 decision 2: once as many jobs wait behind the active slots as
// the operator's queue depth allows, a start is refused at the bridge with
// the count named, leaving no job row behind; a cancelled queued job frees
// its place and the same start is then admitted.
func TestQueueDepthRefusesAtTheBoundary(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/o-1.bin", "x", nil)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	// A picked destination is consumed by the start that uses it, so each
	// admitted start picks afresh, and into its own directory, since one
	// directory serves one active download (ADR 0078).
	pick := func() DownloadRequestDTO {
		t.Helper()
		h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
		picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
		if err != nil || picked == nil {
			t.Fatalf("pick: %+v %v", picked, err)
		}
		return DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token}
	}
	req := pick()

	settings := NewSettingsService(settingsFile(t), h.transfer.runner)
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxQueuedJobs: 1}); err != nil {
		t.Fatal(err)
	}
	runner := h.transfer.runner
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runner.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
	})

	// A job launched the way the services launch one: admitted, pinned,
	// created, registered for cancellation and handed to the runner. Its
	// body holds the single active slot until cancelled.
	launch := func(started chan<- struct{}) jobs.Job {
		t.Helper()
		owner, ctx, err := runner.admit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.finishStart()
		unpin, err := h.transfer.connections.pinForJob(ctx, bookmark)
		if err != nil {
			t.Fatal(err)
		}
		job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 1, SourceBookmarkID: bookmark})
		if err != nil {
			unpin()
			t.Fatal(err)
		}
		runner.register(job.ID, owner.cancel)
		finish := func(ctx context.Context) {
			h.transfer.finishLocal(ctx, job.ID, bookmark, 1, job.Counters, jobs.StateCancelled, "", "")
		}
		owner.hold(unpin)
		owner.launch(job.ID, bookmark, func(ctx context.Context) {
			if started != nil {
				close(started)
			}
			<-ctx.Done()
			finish(context.WithoutCancel(ctx))
		}, finish)
		return job
	}
	started := make(chan struct{})
	running := launch(started)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first job did not take the active slot")
	}
	queued := launch(nil)
	waitForOwners := func(n int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			runner.mu.Lock()
			owners := len(runner.owners)
			runner.mu.Unlock()
			if owners == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("runner holds %d owners, want %d", owners, n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForOwners(2)
	// The depth counts owners waiting for a slot, so a refusal needs the
	// queued job INSIDE that wait, not merely launched or merely holding a
	// queued row. A row exists before the owner reaches the wait, so a
	// refusal asserted on the row alone can be asked while the runner still
	// counts nothing queued; the third start below would then be admitted
	// and fail on the selection token it had already consumed, reporting
	// "unknown selection" where the queue bound was the thing under test.
	//
	// This case did fail master once with exactly that message. That
	// ordering is consistent with the failure and is the reason to wait
	// here, but it was not observed directly and is not offered as the
	// established cause.
	waitForQueued := func(n int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			runner.mu.Lock()
			queued := runner.queued
			runner.mu.Unlock()
			if queued == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("runner counts %d queued jobs, want %d", queued, n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForQueued(1)
	rows := func() int {
		t.Helper()
		list, err := h.transfer.jobs.List(ctx, jobs.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if n := rows(); n != 2 {
		t.Fatalf("job rows before the refusal = %d", n)
	}

	_, err := h.transfer.StartDownload(ctx, req)
	if !errors.Is(err, errJobQueueFull) {
		t.Fatalf("a start over the queue depth was not refused: %v", err)
	}
	if want := "the job queue is full: 1 running and 1 waiting; wait for one to finish or cancel one"; err.Error() != want {
		t.Fatalf("refusal = %q, want %q", err.Error(), want)
	}
	if n := rows(); n != 2 {
		t.Fatalf("the refused start left %d job rows, want 2", n)
	}
	// A capacity refusal leaves the operator's selection usable: the runner
	// admits before anything consumes the token, so being turned away for a
	// full queue does not cost a re-pick. The rest of this case relies on
	// that, and a refactor that moved the admission after the selection was
	// taken would break it silently, so it is asserted rather than assumed.
	store := &h.transfer.selections
	store.mu.Lock()
	_, held := store.entries[req.DestDirToken]
	store.mu.Unlock()
	if !held {
		t.Fatal("a queue refusal consumed the selection, so the operator would have to pick again")
	}

	// Cancelling the queued job releases its place; the same start is
	// then admitted and waits behind the running job.
	if err := h.transfer.CancelJob(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	waitForOwners(1)
	if got, err := h.transfer.jobs.Get(ctx, queued.ID); err != nil || got.State != jobs.StateCancelled {
		t.Fatalf("queued job after cancel = %+v %v", got, err)
	}
	admitted, err := h.transfer.StartDownload(ctx, req)
	if err != nil {
		t.Fatalf("a start after the queue freed was refused: %v", err)
	}
	if got, err := h.transfer.jobs.Get(ctx, admitted); err != nil || got.State != jobs.StateQueued {
		t.Fatalf("admitted job = %+v %v", got, err)
	}
	// The same wait as above, for the same reason: the depth is about owners
	// in the wait, so the next refusal needs this job inside it.
	waitForQueued(1)
	if _, err := h.transfer.StartDownload(ctx, req); !errors.Is(err, errJobQueueFull) {
		t.Fatalf("the depth did not apply again: %v", err)
	}
	// A raised depth admits the start that was refused.
	if _, err := settings.Set(ctx, AppSettingsDTO{MaxActiveJobs: 1, DefaultProfile: "auto", MaxQueuedJobs: 2}); err != nil {
		t.Fatal(err)
	}
	second, err := h.transfer.StartDownload(ctx, pick())
	if err != nil {
		t.Fatalf("a raised depth still refused: %v", err)
	}
	for _, id := range []string{admitted, second, running.ID} {
		if err := h.transfer.CancelJob(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	waitForOwners(0)
	for _, id := range []string{admitted, second, running.ID} {
		if got, err := h.transfer.jobs.Get(ctx, id); err != nil || got.State != jobs.StateCancelled {
			t.Fatalf("job %s after cancel = %+v %v", id, got, err)
		}
	}
}

// ADR 0074 decision 1: a page reply names the limit the bridge applied,
// so a caller that asked for more rows than the clamp allows is told so
// rather than left believing its ask.
func TestPageReplyCarriesTheEffectiveLimit(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	h.mock.AddContainer("media", "standard")
	for i := range 3 {
		seedObject(h.mock, "media", fmt.Sprintf("o-%d.bin", i), "x", nil)
	}
	bookmark := h.transfer.connections.ActiveBookmarkID()
	for _, tc := range []struct{ asked, effective int }{
		{asked: maxPageRows + 1, effective: maxPageRows},
		{asked: 0, effective: maxPageRows},
		{asked: 2, effective: 2},
	} {
		containers, err := h.browse.Containers(ctx, bookmark, "", tc.asked)
		if err != nil || containers.EffectiveLimit != tc.effective {
			t.Fatalf("containers asked %d: effective %d %v, want %d", tc.asked, containers.EffectiveLimit, err, tc.effective)
		}
		objects, err := h.browse.Objects(ctx, bookmark, "media", "", "", tc.asked)
		if err != nil || objects.EffectiveLimit != tc.effective || len(objects.Rows) != min(tc.effective, 3) {
			t.Fatalf("objects asked %d: effective %d, %d rows %v, want %d", tc.asked, objects.EffectiveLimit, len(objects.Rows), err, tc.effective)
		}
		if tc.asked == 2 && !strings.HasPrefix(objects.NextMarker, "o-") {
			t.Fatalf("a clamped page did not continue: marker %q", objects.NextMarker)
		}
	}
}

// ADR 0074 decision 6: a page has a next marker only when a row follows it,
// so a listing that ends exactly at a page's end offers no empty next page
// (found by the operator paging 2,000 objects 1,000 at a time).
func TestPageEndingTheListingHasNoNextMarker(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	h.mock.AddContainer("exact", "standard")
	for i := range 4 {
		seedObject(h.mock, "exact", fmt.Sprintf("o-%d.bin", i), "x", nil)
	}
	bookmark := h.transfer.connections.ActiveBookmarkID()

	first, err := h.browse.Objects(ctx, bookmark, "exact", "", "", 2)
	if err != nil || len(first.Rows) != 2 || first.NextMarker != "o-1.bin" {
		t.Fatalf("first object page: %d rows, marker %q, %v; want 2 rows and o-1.bin", len(first.Rows), first.NextMarker, err)
	}
	last, err := h.browse.Objects(ctx, bookmark, "exact", "", first.NextMarker, 2)
	if err != nil || len(last.Rows) != 2 || last.NextMarker != "" {
		t.Fatalf("last object page: %d rows, marker %q, %v; want 2 rows and none", len(last.Rows), last.NextMarker, err)
	}

	all, err := h.browse.Containers(ctx, bookmark, "", maxPageRows)
	if err != nil || all.NextMarker != "" {
		t.Fatalf("whole container listing: marker %q, %v", all.NextMarker, err)
	}
	total := len(all.Rows)
	exact, err := h.browse.Containers(ctx, bookmark, "", total)
	if err != nil || len(exact.Rows) != total || exact.NextMarker != "" {
		t.Fatalf("container page of exactly %d: %d rows, marker %q, %v", total, len(exact.Rows), exact.NextMarker, err)
	}
	short, err := h.browse.Containers(ctx, bookmark, "", total-1)
	if err != nil || len(short.Rows) != total-1 || short.NextMarker != short.Rows[total-2].Name {
		t.Fatalf("container page one short: %d rows, marker %q, %v", len(short.Rows), short.NextMarker, err)
	}
}
