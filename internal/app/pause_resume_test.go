package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func awaitJobState(t *testing.T, h *harness, id string, want jobs.State) {
	t.Helper()
	deadline := time.Now().Add(terminalWait)
	for {
		job, err := h.transfer.jobs.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == want {
			return
		}
		if jobs.Terminal(job.State) || time.Now().After(deadline) {
			t.Fatalf("job %s is %s (%q), want %s", id, job.State, job.Error, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitEngine waits for a job to be inside its engine at the barrier, and says
// what became of the job if it ended without getting there.
func awaitEngine(t *testing.T, h *harness, barrier *engineBarrier, id string) {
	t.Helper()
	deadline := time.Now().Add(terminalWait)
	for {
		select {
		case <-barrier.entered:
			return
		case <-time.After(10 * time.Millisecond):
		}
		job, err := h.transfer.jobs.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if jobs.Terminal(job.State) || time.Now().After(deadline) {
			t.Fatalf("job %s never reached its engine: %s (%q)", id, job.State, job.Error)
		}
	}
}

// executedItems is which of a job's item records carry an execution, by key,
// with the execution's result.
func executedItems(t *testing.T, h *harness, bookmark, id string) (results map[string]string, admitted int) {
	t.Helper()
	results = map[string]string{}
	for cursor := ""; ; {
		page, err := h.jobsSvc.Items(context.Background(), bookmark, id, cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			admitted++
			if item.Execution != nil {
				results[item.Key] = item.Execution.Result
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			return results, admitted
		}
	}
}

// firstUnitFails makes the first engine unit fail once the barrier has let it
// go, and passes every later unit to the engine it wraps.
type firstUnitFails struct {
	transfercopy.Engine
	failed *atomic.Bool
	block  *engineBarrier
}

func (e *firstUnitFails) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	if !e.failed.CompareAndSwap(false, true) {
		return e.Engine.Copy(ctx, src, dst, names, onEvent)
	}
	e.block.enterOnce.Do(func() { close(e.block.entered) })
	select {
	case <-e.block.release:
	case <-ctx.Done():
		return &rclone.Result{Cancelled: true}, nil
	}
	return &rclone.Result{ExitCode: 1, Errors: []string{"the first unit failed"}}, nil
}

// pausedUpload starts a folder upload of three files in three source
// directories, so three engine units, asks it to pause while the first unit's
// child is running, lets that unit end, and returns once the job is paused.
func pausedUpload(t *testing.T, failFirst bool) (h *harness, bookmark, id string, names []string) {
	t.Helper()
	return pausedUploadOver(t, failFirst, nil)
}

// pausedUploadOver is pausedUpload with every destination name already live
// and the replacement acknowledged through the confirmer, when one is given.
func pausedUploadOver(t *testing.T, failFirst bool, confirmer *recordingOverwriteConfirmer) (h *harness, bookmark, id string, names []string) {
	t.Helper()
	h = newHarness(t)
	h.connect(t)
	// One slot, so a job started while this one is parked shows that the
	// parked run gave its slot up.
	h.transfer.runner.setMaxActiveJobs(1)
	block := newEngineBarrier(t)
	h.installEngine(block)
	names = []string{"inbox/photos/.hidden", "inbox/photos/a/image.jpg", "inbox/photos/b/image.jpg"}
	if failFirst {
		inner := h.transfer.engineFactory
		var failed atomic.Bool
		h.transfer.setEngineFactory(func(source transfercopy.RemoteSource, opts rclone.Options) (transfercopy.Engine, error) {
			engine, err := inner(source, opts)
			if err != nil {
				return nil, err
			}
			return &firstUnitFails{Engine: engine, failed: &failed, block: block}, nil
		})
	}
	h.mock.AddContainer("media", "standard")
	if confirmer != nil {
		for _, name := range names {
			seedObject(h.mock, "media", name, "what the operator was shown at "+name, nil)
		}
		h.transfer.setOverwriteConfirmer(confirmer)
	}
	bookmark = h.transfer.connections.ActiveBookmarkID()
	h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
	ctx := context.Background()
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	id, err = h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/",
		SelectionToken: picked.Token, AcknowledgeOverwrite: confirmer != nil})
	if err != nil {
		t.Fatal(err)
	}
	block.waitUntilEntered(t)
	// The first unit's child is running: a pause asked now waits for it.
	if err := h.transfer.PauseJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	if job, err := h.jobsSvc.Get(ctx, bookmark, id); err != nil || job.State != "running" || job.Pause != "pausing" {
		t.Fatalf("a pause asked inside a unit: job %+v, %v; want running and pausing", job, err)
	}
	block.unblock()
	awaitJobState(t, h, id, jobs.StatePaused)
	return h, bookmark, id, names
}

// ADR 0104 on the upload route, at the service boundary: a pause asked while
// a unit's child is running takes effect when that unit has been published and
// verified, and no later unit is started; another job runs while this one
// waits; and a resume carries the same job on to the end, each item once.
func TestUploadPausesAtTheNextUnitAndResumes(t *testing.T) {
	h, bookmark, id, names := pausedUpload(t, false)
	ctx := context.Background()

	results, admitted := executedItems(t, h, bookmark, id)
	if admitted != 3 || len(results) != 1 {
		t.Fatalf("paused after %d of %d admitted items, want exactly the unit in hand: %v", len(results), admitted, results)
	}
	for _, name := range names {
		_, err := h.inspector.Object(ctx, bookmark, "media", name)
		if _, done := results[name]; done != (err == nil) {
			t.Errorf("%s: recorded executed = %v, at the destination = %v (%v)", name, done, err == nil, err)
		}
	}
	// Parked, the job sends nothing: no later unit is started.
	sent := len(h.mock.RequestLog())
	time.Sleep(150 * time.Millisecond)
	if grown := h.mock.RequestLog()[sent:]; len(grown) != 0 {
		t.Fatalf("a paused job sent requests: %v", grown)
	}
	if job, err := h.jobsSvc.Get(ctx, bookmark, id); err != nil || job.State != "paused" || job.Pause != "" {
		t.Fatalf("paused job = %+v, %v", job, err)
	}

	// Another job takes the slot and finishes while this one is paused.
	other := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "note.txt"), []byte("meanwhile"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.transfer.setFilePicker(&directoryPicker{dir: other})
	picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "other/"})
	if err != nil || picked == nil {
		t.Fatalf("second pick = %+v, %v", picked, err)
	}
	second, err := h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "other/", SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, second); job.State != "complete" {
		t.Fatalf("the job started during the pause = %+v", job)
	}
	awaitJobState(t, h, id, jobs.StatePaused)

	if err := h.transfer.ResumeJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 3 || job.Counters.ObjectsVerified != 3 {
		t.Fatalf("resumed job = %+v", job)
	}
	results, admitted = executedItems(t, h, bookmark, id)
	if admitted != 3 || len(results) != 3 {
		t.Fatalf("after the resume %d of %d items carry an execution: %v", len(results), admitted, results)
	}
	for _, name := range names {
		if _, err := h.inspector.Object(ctx, bookmark, "media", name); err != nil {
			t.Errorf("%s is not at the destination after the resume: %v", name, err)
		}
	}
	events, err := h.auditSvc.audit.List(ctx, audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, event := range events {
		if event.Resource == "job "+id {
			seen[event.Operation]++
		}
	}
	if seen["transfer.paused"] != 1 || seen["transfer.resumed"] != 1 {
		t.Errorf("trail for the job = %v, want one pause and one resume", seen)
	}
	if err := h.transfer.ResumeJob(ctx, id); err == nil {
		t.Error("a finished job accepted a resume")
	}
	if err := h.transfer.PauseJob(ctx, id); err == nil || !strings.Contains(err.Error(), "cannot be paused") {
		t.Errorf("a finished job accepted a pause: %v", err)
	}
}

// ADR 0104 decision 4: cancelling a paused upload ends it by the route's own
// rules. What it had admitted and not started is recorded as not attempted,
// nothing is sent after the cancel, and the job is partial whenever something
// had landed or failed before the pause, a run with no success included
// (ADR 0025).
func TestCancellingAPausedUploadKeepsWhatItDidAndDidNot(t *testing.T) {
	for _, c := range []struct {
		name      string
		failFirst bool
		landed    int
		says      string
	}{
		{"after a unit landed", false, 1, ""},
		{"after a unit failed and nothing landed", true, 0, "the first unit failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, bookmark, id, _ := pausedUpload(t, c.failFirst)
			sent := len(h.mock.RequestLog())
			if err := h.transfer.CancelJob(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			if job.State != "partial" || job.Counters.ObjectsCopied != int64(c.landed) {
				t.Fatalf("cancelled paused job = %+v, want partial with %d landed", job, c.landed)
			}
			if c.says != "" && !strings.Contains(job.Error, c.says) {
				t.Errorf("the earlier failure is not in the job's message: %q", job.Error)
			}
			if grown := h.mock.RequestLog()[sent:]; len(grown) != 0 {
				t.Errorf("the cancel sent requests: %v", grown)
			}
			results, admitted := executedItems(t, h, bookmark, id)
			if admitted != 3 || len(results) != 3 {
				t.Fatalf("%d of %d admitted items carry an outcome: %v", len(results), admitted, results)
			}
			notAttempted := 0
			for _, result := range results {
				if result == "not_attempted" {
					notAttempted++
				}
			}
			if notAttempted != 2 {
				t.Errorf("outcomes = %v, want the two units never started recorded as not attempted", results)
			}
		})
	}
}

// pausedDownload starts a download of one natively routed object and two
// engine-routed ones, asks it to pause while the native object is being read,
// and returns once the job is paused: the native item is downloaded, and the
// engine batch waits at its head.
func pausedDownload(t *testing.T) (h *harness, bookmark, id, dir string) {
	t.Helper()
	h = newHarness(t)
	h.connect(t)
	// One slot, so a cancel while another job holds it shows that the
	// cancel does not wait for a slot.
	h.transfer.runner.setMaxActiveJobs(1)
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	// A rune the engine reserves is what routes a plain object natively.
	for _, name := range []string{"scope/sub/n\uff0aative.bin", "scope/sub/e-1.bin", "scope/sub/e-2.bin"} {
		seedObject(h.mock, "media", name, "bytes of "+name, nil)
	}
	bookmark = h.transfer.connections.ActiveBookmarkID()
	dir = t.TempDir()
	ctx := context.Background()
	h.transfer.setFilePicker(fakePicker{dir: dir})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	ready := make(chan string, 1)
	var asked atomic.Bool
	h.mock.BeforeRequest = func(method, path string) {
		// The object's body is read only by the job: the pause is asked
		// inside the native item, which is a unit of its own.
		if method == "GET" && strings.Contains(path, "/media/scope/sub/n") && asked.CompareAndSwap(false, true) {
			if err := h.transfer.PauseJob(ctx, <-ready); err != nil {
				t.Error(err)
			}
		}
	}
	id, err = h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	ready <- id
	awaitJobState(t, h, id, jobs.StatePaused)
	h.mock.BeforeRequest = nil
	return h, bookmark, id, dir
}

func placedFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "sub"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return len(entries)
}

// ADR 0104 on the download route: a pause asked inside a native-routed item
// takes effect at the head of the next unit and not inside this one, so the
// item is downloaded and the engine batch is not given to a child; a resume
// downloads the rest into the same captured directory.
func TestDownloadPausesAtTheNextUnitAndResumes(t *testing.T) {
	h, bookmark, id, dir := pausedDownload(t)
	results, admitted := executedItems(t, h, bookmark, id)
	if admitted != 3 || len(results) != 1 || placedFiles(t, dir) != 1 {
		t.Fatalf("paused with %d of %d items executed and %d files placed, want the native item alone: %v", len(results), admitted, placedFiles(t, dir), results)
	}
	if err := h.transfer.ResumeJob(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "complete" || job.Counters.ObjectsCopied != 3 || placedFiles(t, dir) != 3 {
		t.Fatalf("resumed download = %+v with %d files downloaded", job, placedFiles(t, dir))
	}
}

// ADR 0104 decision 4, for a download: a paused job cancelled while another
// job holds the slot finishes without waiting for it. What was downloaded
// stays, the batch never given to a child is recorded as not attempted,
// nothing is sent, and the directory is free for another download once the
// job has ended.
func TestCancellingAPausedDownloadFinishesWithoutTheSlot(t *testing.T) {
	h, bookmark, id, dir := pausedDownload(t)
	ctx := context.Background()

	// Another download, into a directory that does not overlap,
	// takes the slot and is held inside its engine.
	holding := newEngineBarrier(t)
	h.installEngine(holding)
	seedObject(h.mock, "media", "other/p.bin", "y", nil)
	h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
	picked, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "other/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	second, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "other/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	awaitEngine(t, h, holding, second)

	sent := len(h.mock.RequestLog())
	if err := h.transfer.CancelJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "partial" || job.Counters.ObjectsCopied != 1 || placedFiles(t, dir) != 1 {
		t.Fatalf("cancelled paused download = %+v with %d files placed", job, placedFiles(t, dir))
	}
	if grown := h.mock.RequestLog()[sent:]; len(grown) != 0 {
		t.Errorf("the cancel sent requests: %v", grown)
	}
	results, admitted := executedItems(t, h, bookmark, id)
	if admitted != 3 || results["scope/sub/e-1.bin"] != "not_attempted" || results["scope/sub/e-2.bin"] != "not_attempted" {
		t.Errorf("the batch never given to a child is not recorded as not attempted: %d admitted, %v", admitted, results)
	}

	// The reservation went with the job: the same directory is
	// admitted for a new download while the other job still holds
	// the slot.
	seedObject(h.mock, "media", "third/q.bin", "z", nil)
	h.transfer.setFilePicker(fakePicker{dir: dir})
	again, err := h.transfer.PickDownloadDir(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "third/"})
	if err != nil || again == nil {
		t.Fatalf("pick: %+v %v", again, err)
	}
	third, err := h.transfer.StartDownload(ctx, DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "third/", DestDirToken: again.Token})
	if err != nil {
		t.Fatalf("the cancelled job still holds its directory: %v", err)
	}
	holding.unblock()
	for _, later := range []string{second, third} {
		if job := awaitTerminal(t, h, later); job.State != "complete" {
			t.Errorf("job %s = %+v", later, job)
		}
	}
}

// ADR 0104 on the copy route, whose unit is one item: a pause asked while a
// native-routed item is being copied takes effect at the head of the next
// item and not inside this one; a resume copies the rest; and a cancel
// instead leaves a record for the item copied and none for names the copy
// had not reached.
func TestCopyPausesAtTheNextItem(t *testing.T) {
	for _, ending := range []string{"resume", "cancel"} {
		t.Run(ending, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			h.installEngine(nil)
			source := h.transfer.connections.ActiveBookmarkID()
			dest, destMock := secondCluster(t, h)
			h.mock.AddContainer("media", "standard")
			destMock.AddContainer("backup", "standard")
			// A newline in a name is what routes a plain object natively.
			names := []string{"a\none", "b\ntwo", "c\nthree"}
			for _, name := range names {
				seedObject(h.mock, "media", name, "bytes of "+name, nil)
			}
			ctx := context.Background()
			ready := make(chan string, 1)
			var asked atomic.Bool
			destMock.BeforeRequest = func(method, path string) {
				// The first write to the destination is the first item being
				// copied: the pause is asked inside that unit.
				if method == "PUT" && strings.Contains(path, "/backup/") && asked.CompareAndSwap(false, true) {
					if err := h.transfer.PauseJob(ctx, <-ready); err != nil {
						t.Error(err)
					}
				}
			}
			id, err := h.transfer.StartCopy(ctx, CopyRequestDTO{
				SourceBookmarkID: source, SourceContainer: "media", DestBookmarkID: dest, DestContainer: "backup",
			})
			if err != nil {
				t.Fatal(err)
			}
			ready <- id
			awaitJobState(t, h, id, jobs.StatePaused)
			destMock.BeforeRequest = nil

			results, admitted := executedItems(t, h, source, id)
			if admitted != 1 || len(results) != 1 {
				t.Fatalf("paused with %d executed of %d admitted, want the one item in hand: %v", len(results), admitted, results)
			}
			sent := len(h.mock.RequestLog()) + len(destMock.RequestLog())
			if ending == "cancel" {
				if err := h.transfer.CancelJob(ctx, id); err != nil {
					t.Fatal(err)
				}
				job := awaitTerminal(t, h, id)
				if job.State != "partial" || job.Counters.ObjectsCopied != 1 {
					t.Fatalf("cancelled paused copy = %+v", job)
				}
				if after := len(h.mock.RequestLog()) + len(destMock.RequestLog()); after != sent {
					t.Errorf("the cancel sent %d requests", after-sent)
				}
				if results, admitted := executedItems(t, h, source, id); admitted != 1 || len(results) != 1 {
					t.Errorf("records after the cancel: %d admitted, %v; want none invented for names not reached", admitted, results)
				}
				return
			}
			if err := h.transfer.ResumeJob(ctx, id); err != nil {
				t.Fatal(err)
			}
			job := awaitTerminal(t, h, id)
			if job.State != "complete" || job.Counters.ObjectsCopied != 3 {
				t.Fatalf("resumed copy = %+v", job)
			}
			if results, admitted := executedItems(t, h, source, id); admitted != 3 || len(results) != 3 {
				t.Errorf("records after the resume: %d admitted, %v", admitted, results)
			}
		})
	}
}

// A parked upload has already admitted its remaining groups. If another job's
// result cannot be saved while it waits, the store stops for the session and
// the parked job must not carry on: it would stage and publish with nothing
// able to record it. The runner wakes it, it asks the store at its boundary as
// a launch does, and it ends there with nothing sent and nothing more
// published. A resume after that has nothing to resume.
func TestAStoppedStoreEndsAParkedJobWithoutAFurtherUnit(t *testing.T) {
	h, bookmark, id, names := pausedUpload(t, false)
	ctx := context.Background()
	published := map[string]bool{}
	for _, name := range names {
		_, err := h.inspector.Object(ctx, bookmark, "media", name)
		published[name] = err == nil
	}
	sent := len(h.mock.RequestLog())
	stopStore(t, h)
	// The store no longer answers for the job, so its end is read from the
	// runner: the owner leaves when the whole body has returned.
	ownersGone(t, h)
	if grown := h.mock.RequestLog()[sent:]; len(grown) != 0 {
		t.Errorf("the parked job sent requests after the store stopped: %v", grown)
	}
	for _, name := range names {
		if _, err := h.inspector.Object(ctx, bookmark, "media", name); (err == nil) != published[name] {
			t.Errorf("%s: at the destination = %v, was %v before the store stopped", name, err == nil, published[name])
		}
	}
	if err := h.transfer.ResumeJob(ctx, id); err == nil {
		t.Error("a job the stopped store ended was resumed")
	}
}

// A job this process is not holding cannot be resumed, and a paused row is not
// paused again: nothing resumes a job across a restart (ADR 0104 decision 4).
func TestAJobThisProcessDoesNotHoldRefusesPauseAndResume(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	job, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeUpload, DestBookmarkID: h.transfer.connections.ActiveBookmarkID()})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StatePaused} {
		if err := h.transfer.jobs.Transition(ctx, job.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.transfer.ResumeJob(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "cannot be resumed") {
		t.Errorf("resume of a job no run holds = %v", err)
	}
	if err := h.transfer.PauseJob(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "cannot be paused") {
		t.Errorf("pause of a paused row = %v", err)
	}
	sweep, err := h.transfer.jobs.Create(ctx, jobs.Job{Type: jobs.TypeDeletePrefix, SourceBookmarkID: h.transfer.connections.ActiveBookmarkID()})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.transfer.PauseJob(ctx, sweep.ID); err == nil || !strings.Contains(err.Error(), "only an upload, a download or a copy") {
		t.Errorf("pause of a deletion job = %v", err)
	}
}

// ADR 0104 decision 1: a pause asked while the job is queued takes effect at
// its first unit boundary, when it has had the slot and before it transfers
// anything.
func TestAPauseAskedWhileQueuedParksBeforeTheFirstUnit(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	// One slot, so the second job waits behind the first.
	h.transfer.runner.setMaxActiveJobs(1)
	block := newEngineBarrier(t)
	h.installEngine(block)
	h.mock.AddContainer("media", "standard")
	bookmark := h.transfer.connections.ActiveBookmarkID()
	ctx := context.Background()
	start := func(prefix string) string {
		t.Helper()
		h.transfer.setFilePicker(&directoryPicker{dir: uploadFolder(t)})
		picked, err := h.transfer.PickUploadDirectory(ctx, SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix})
		if err != nil || picked == nil || picked.Token == "" {
			t.Fatalf("pick = %+v, %v", picked, err)
		}
		id, err := h.transfer.StartUpload(ctx, UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: prefix, SelectionToken: picked.Token})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	holding := start("first/")
	block.waitUntilEntered(t)
	queued := start("second/")
	if job, err := h.jobsSvc.Get(ctx, bookmark, queued); err != nil || job.State != "queued" {
		t.Fatalf("the second job = %+v, %v; want queued behind the first", job, err)
	}
	if err := h.transfer.PauseJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	block.unblock()
	if job := awaitTerminal(t, h, holding); job.State != "complete" {
		t.Fatalf("the job that held the slot = %+v", job)
	}
	awaitJobState(t, h, queued, jobs.StatePaused)
	if results, _ := executedItems(t, h, bookmark, queued); len(results) != 0 {
		t.Fatalf("a job paused while queued transferred before parking: %v", results)
	}
	if err := h.transfer.ResumeJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if job := awaitTerminal(t, h, queued); job.State != "complete" || job.Counters.ObjectsCopied != 3 {
		t.Fatalf("the resumed job = %+v", job)
	}
}
