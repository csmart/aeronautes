package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/jobs"
)

// ADR 0105: under the default budget two downloads into sibling directories
// run together. Both are held in their engines at the same time while a third
// start waits in the queue; released, each completes with its own file and
// nothing else in its directory.
func TestTwoDownloadsRunTogetherIntoSiblingDirectories(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	h.mock.AddContainer("media", "standard")
	block := newEngineBarrier(t)
	h.installEngine(block)
	base := t.TempDir()
	type download struct{ id, dir, file, content string }
	start := func(prefix, file string) download {
		t.Helper()
		content := "bytes of " + prefix + file
		seedObject(h.mock, "media", prefix+file, content, nil)
		dir := filepath.Join(base, filepath.Base(prefix))
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		h.transfer.setFilePicker(fakePicker{dir: dir})
		picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix})
		if err != nil || picked == nil {
			t.Fatalf("pick %s: %+v %v", dir, picked, err)
		}
		id, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix, DestDirToken: picked.Token})
		if err != nil {
			t.Fatalf("a download into a sibling directory was refused: %v", err)
		}
		return download{id, dir, file, content}
	}
	together := []download{start("one/", "a.bin"), start("two/", "b.bin")}
	block.waitUntilHolding(t, 2)
	waiting := start("three/", "c.bin")
	if job, err := h.jobsSvc.Get(ctx, bookmark, waiting.id); err != nil || job.State != "queued" {
		t.Fatalf("the third download = %+v, %v; want queued behind the two that are running", job, err)
	}
	block.unblock()
	for _, d := range append(together, waiting) {
		if job := awaitTerminal(t, h, d.id); job.State != "complete" {
			t.Fatalf("download %s = %+v", d.id, job)
		}
		entries, err := os.ReadDir(d.dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != d.file {
			t.Fatalf("%s holds %v, %v; want only its own file", d.dir, entries, err)
		}
		if data, err := os.ReadFile(filepath.Join(d.dir, d.file)); err != nil || string(data) != d.content {
			t.Fatalf("%s holds %q, %v; want this download's bytes", d.file, data, err)
		}
	}
}

// ADR 0105: shutdown with two jobs running, one parked and one queued
// returns, and leaves every row cancelled.
func TestShutdownEndsRunningParkedAndQueuedJobsTogether(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	runner.setMaxActiveJobs(defaultMaxActiveJobs)
	parked := launchUnits(t, h, bookmark, 2)
	parked.expectUnit(t, 0)
	runner.requestPause(parked.id)
	parked.finishUnit(t)
	waitCounts(runnerCounts{paused: 1})
	all := []*unitJob{parked}
	for range defaultMaxActiveJobs {
		running := launchUnits(t, h, bookmark, 2)
		running.expectUnit(t, 0)
		all = append(all, running)
	}
	all = append(all, launchUnits(t, h, bookmark, 2))
	waitCounts(runnerCounts{active: defaultMaxActiveJobs, queued: 1, paused: 1})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown with running, parked and queued jobs: %v", err)
	}
	for _, job := range all {
		if state := jobState(t, h, job.id); state != jobs.StateCancelled {
			t.Errorf("job %s is %s after shutdown, want cancelled", job.id, state)
		}
	}
	waitCounts(runnerCounts{})
}
