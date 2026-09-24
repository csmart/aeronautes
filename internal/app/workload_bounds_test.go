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

	settings := NewSettingsService(h.db, h.transfer.runner)
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
		owner.launch(job.ID, bookmark, unpin, func(ctx context.Context) {
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
	// The depth counts owners waiting for a slot, so the refusal needs the
	// queued job inside that wait, not merely launched.
	deadline := time.Now().Add(10 * time.Second)
	for {
		runner.mu.Lock()
		queued := runner.queued
		runner.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runner counts %d queued jobs, want 1", queued)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	if want := "the job queue is full: 1 job is waiting; wait for one to finish or cancel one"; err.Error() != want {
		t.Fatalf("refusal = %q, want %q", err.Error(), want)
	}
	if n := rows(); n != 2 {
		t.Fatalf("the refused start left %d job rows, want 2", n)
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
