package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0078: a download whose picked directory is the same as, inside, or
// containing the destination of an active download is reported by
// preflight and refused by the start, naming that job, and leaves no job
// row behind; a sibling directory is admitted; a cancelled or finished job
// frees its directory.
func TestDownloadRefusesOverlappingDestinations(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	// The two selections carry distinct names, so a directory that has
	// received one can receive the other without a collision.
	for i := range 3 {
		seedObject(h.mock, "media", fmt.Sprintf("scope/o-%d.bin", i), "x", nil)
		seedObject(h.mock, "media", fmt.Sprintf("other/p-%d.bin", i), "x", nil)
	}
	block := newEngineBarrier(t)
	h.installEngine(block)
	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")
	for _, dir := range []string{dirA, filepath.Join(dirA, "sub"), dirB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link-to-a")
	if err := os.Symlink(dirA, link); err != nil {
		t.Fatal(err)
	}
	rows := func() int {
		t.Helper()
		list, err := h.transfer.jobs.List(ctx, jobs.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	pick := func(dir, prefix string) DownloadRequestDTO {
		t.Helper()
		h.transfer.setFilePicker(fakePicker{dir: dir})
		picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix})
		if err != nil || picked == nil {
			t.Fatalf("pick %s: %+v %v", dir, picked, err)
		}
		return DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix, DestDirToken: picked.Token}
	}
	// refused checks preflight and the start for one directory, and that
	// neither left a job row.
	refused := func(dir, prefix, job string) {
		t.Helper()
		before := rows()
		req := pick(dir, prefix)
		view, err := h.transfer.PreflightDownload(ctx, req)
		if err != nil || view == nil || !view.Rejected || len(view.ProblemSamples) == 0 || view.ProblemSamples[0].Kind != destinationOverlapKind ||
			!strings.Contains(view.ProblemSamples[0].Detail, "download job "+job) {
			t.Fatalf("preflight into %s did not report the overlap with %s: %+v %v", dir, job, view, err)
		}
		id, err := h.transfer.StartDownload(ctx, req)
		if id != "" || !errors.Is(err, errDestinationOverlap) || !strings.Contains(err.Error(), "download job "+job) {
			t.Fatalf("start into %s was not refused naming %s: id=%q err=%v", dir, job, id, err)
		}
		if after := rows(); after != before {
			t.Fatalf("a refused start into %s left %d job rows (had %d)", dir, after, before)
		}
	}

	running, err := h.transfer.StartDownload(ctx, pick(dirA, "scope/"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { block.unblock(); awaitTerminal(t, h, running) })
	block.waitUntilEntered(t)
	if n := h.transfer.destinations.activeCount(); n != 1 {
		t.Fatalf("active destinations after one start = %d", n)
	}
	refused(dirA, "other/", running)
	refused(filepath.Join(dirA, "sub"), "other/", running)
	refused(base, "other/", running)
	refused(link, "other/", running)

	// A sibling is admitted; it queues behind the running job and holds
	// its own directory from the start.
	queued, err := h.transfer.StartDownload(ctx, pick(dirB, "other/"))
	if err != nil {
		t.Fatalf("a sibling directory was refused: %v", err)
	}
	if n := h.transfer.destinations.activeCount(); n != 2 {
		t.Fatalf("active destinations with a queued job = %d", n)
	}
	refused(dirB, "scope/", queued)
	if err := h.transfer.CancelJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, queued); job.State != "cancelled" {
		t.Fatalf("queued job after cancel = %+v", job)
	}
	block.unblock()
	if job := awaitTerminal(t, h, running); job.State != "complete" {
		t.Fatalf("running job = %+v", job)
	}
	if n := h.transfer.destinations.activeCount(); n != 0 {
		t.Fatalf("active destinations after both jobs ended = %d", n)
	}
	// Both directories are free again.
	again, err := h.transfer.StartDownload(ctx, pick(dirA, "other/"))
	if err != nil {
		t.Fatalf("the finished job's directory was still refused: %v", err)
	}
	if job := awaitTerminal(t, h, again); job.State != "complete" {
		t.Fatalf("download after release = %+v", job)
	}
	if n := h.transfer.destinations.activeCount(); n != 0 {
		t.Fatalf("active destinations at the end = %d", n)
	}
}

// A start whose preflight saw a free directory can still lose it to a
// concurrent start before it reserves: the reservation runs in the same
// critical section as the check and before the job row, so the loser is
// refused naming the winner and leaves no row (ADR 0078 decision 3).
func TestDownloadStartReservesBeforeTheJobRow(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	for i := range 3 {
		seedObject(h.mock, "media", fmt.Sprintf("scope/o-%d.bin", i), "x", nil)
		seedObject(h.mock, "media", fmt.Sprintf("other/p-%d.bin", i), "x", nil)
	}
	block := newEngineBarrier(t)
	h.installEngine(block)
	dir := t.TempDir()
	pick := func(prefix string) DownloadRequestDTO {
		t.Helper()
		h.transfer.setFilePicker(fakePicker{dir: dir})
		picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix})
		if err != nil || picked == nil {
			t.Fatalf("pick: %+v %v", picked, err)
		}
		return DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix, DestDirToken: picked.Token}
	}
	loser := pick("other/")
	winner := pick("scope/")
	// The loser's start parks after its preflight; the winner starts and
	// reserves the directory meanwhile.
	parked, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	h.transfer.downloadStartBoundary = func(ctx context.Context) error {
		// Only the first start through here parks; the winner passes.
		if !first.CompareAndSwap(false, true) {
			return nil
		}
		close(parked)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	type outcome struct {
		id  string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		id, err := h.transfer.StartDownload(ctx, loser)
		done <- outcome{id, err}
	}()
	<-parked
	running, err := h.transfer.StartDownload(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { block.unblock(); awaitTerminal(t, h, running) })
	block.waitUntilEntered(t)
	close(release)
	result := <-done
	if result.id != "" || !errors.Is(result.err, errDestinationOverlap) || !strings.Contains(result.err.Error(), "download job "+running) {
		t.Fatalf("the losing start was not refused naming the winner: id=%q err=%v", result.id, result.err)
	}
	list, err := h.transfer.jobs.List(ctx, jobs.Filter{})
	if err != nil || len(list) != 1 || list[0].ID != running {
		t.Fatalf("job rows after the race: %+v %v", list, err)
	}
	if n := h.transfer.destinations.activeCount(); n != 1 {
		t.Fatalf("active destinations after the race = %d", n)
	}
}
