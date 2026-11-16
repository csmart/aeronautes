package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// breakStore makes another job's final result impossible to save, which stops
// the job store for the session.
func breakStore(t *testing.T, h *harness) {
	t.Helper()
	broken, err := h.transfer.jobs.Create(context.Background(), jobs.Job{Type: jobs.TypeUpload})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.transfer.jobs.Finish(context.Background(), broken.ID, jobs.Completion{State: jobs.StateFailed, Audit: audit.Event{}}, h.transfer.runner.audit); err == nil {
		t.Fatal("the fixture must stop the store with a result that cannot be saved")
	}
}

// stopStore stops the job store and returns when the runner has been told and
// has cancelled what it holds.
func stopStore(t *testing.T, h *harness) {
	t.Helper()
	told := make(chan struct{})
	runner := h.transfer.runner
	h.transfer.jobs.OnStop(func(failed string) {
		runner.storeStopped(failed)
		close(told)
	})
	breakStore(t, h)
	select {
	case <-told:
	case <-time.After(10 * time.Second):
		t.Fatal("the runner was not told that the store stopped")
	}
}

// ownersGone waits until every job's whole body has returned. A stopped store
// no longer answers for a job, so its end is read from the runner.
func ownersGone(t *testing.T, h *harness) {
	t.Helper()
	runner := h.transfer.runner
	deadline := time.Now().Add(terminalWait)
	for {
		runner.mu.Lock()
		owners := len(runner.owners)
		runner.mu.Unlock()
		if owners == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs were still held after the store stopped", owners)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// executions reads a job's item records past a stopped store, which refuses
// the service's own read: how many items were admitted and how many carry an
// execution record.
func executions(t *testing.T, h *harness, id string) (admitted, executed int) {
	t.Helper()
	page, err := jobs.NewStore(h.db).ReadItems(context.Background(), id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		admitted++
		if item.Execution != nil {
			executed++
		}
	}
	return admitted, executed
}

// twoFileFolder is a folder whose two files share one directory, so an upload
// stages them as one engine unit and publishes them one after the other.
func twoFileFolder(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("bytes of "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func startFolderUpload(t *testing.T, h *harness, folder string) (bookmark, id string) {
	t.Helper()
	bookmark = h.transfer.connections.ActiveBookmarkID()
	h.transfer.setFilePicker(&directoryPicker{dir: folder})
	picked, err := h.transfer.PickUploadDirectory(context.Background(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/"})
	if err != nil || picked == nil || picked.Token == "" {
		t.Fatalf("pick = %+v, %v", picked, err)
	}
	id, err = h.transfer.StartUpload(context.Background(), UploadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "inbox/", SelectionToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	return bookmark, id
}

func publishedUnder(t *testing.T, h *harness, bookmark string, names ...string) int {
	t.Helper()
	published := 0
	for _, name := range names {
		if _, err := h.inspector.Object(context.Background(), bookmark, "media", name); err == nil {
			published++
		}
	}
	return published
}

// A start that comes after the store stopped is refused at admission with the
// store's reason, before it registers or does any I/O. The runner's own
// notice arrives later, on a goroutine of its own, and cancels every start it
// finds, so a start admitted in between used to end as "context canceled"
// rather than say why. The notice is held back here so that window is
// certain.
func TestAStoppedStoreRefusesAStartBeforeTheRunnerIsTold(t *testing.T) {
	h := newHarness(t)
	runner := h.transfer.runner
	release := make(chan struct{})
	h.transfer.jobs.OnStop(func(failed string) {
		<-release
		runner.storeStopped(failed)
	})
	defer close(release)
	breakStore(t, h)

	owner, _, err := runner.admit(context.Background())
	if err == nil {
		owner.finishStart()
		t.Fatal("a start was admitted after the store stopped")
	}
	if !strings.Contains(err.Error(), "new jobs are blocked") {
		t.Fatalf("admission refused with %q, not the store's reason", err)
	}
	runner.mu.Lock()
	held := len(runner.owners)
	runner.mu.Unlock()
	if held != 0 {
		t.Fatalf("a refused start left %d owners registered", held)
	}
}

// ADR 0105 decision 9: an upload admits every item before its engine runs, so
// when another job's record stops the store nothing in the upload's own
// records would stop it publishing. The runner cancels it: held in its engine
// when the store stops, it publishes nothing and deletes nothing, and each
// admitted item still gets its outcome recorded. The same run with no stop
// publishes every file.
func TestAStoppedStoreCancelsAnUploadHeldInItsEngine(t *testing.T) {
	names := []string{"inbox/docs/one.txt", "inbox/docs/two.txt"}
	for _, stopped := range []bool{true, false} {
		t.Run(map[bool]string{true: "the store stops", false: "the store keeps recording"}[stopped], func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			block := newEngineBarrier(t)
			h.installEngine(block)
			h.mock.AddContainer("media", "standard")
			bookmark, id := startFolderUpload(t, h, twoFileFolder(t))
			block.waitUntilEntered(t)
			if !stopped {
				block.unblock()
				if job := awaitTerminal(t, h, id); job.State != "complete" || publishedUnder(t, h, bookmark, names...) != 2 {
					t.Fatalf("with the store recording the upload = %+v", job)
				}
				return
			}
			stopStore(t, h)
			sent := len(h.mock.RequestLog())
			block.unblock()
			ownersGone(t, h)
			if got := publishedUnder(t, h, bookmark, names...); got != 0 {
				t.Errorf("%d files were published after the store stopped", got)
			}
			for _, request := range h.mock.RequestLog()[sent:] {
				if strings.HasPrefix(request, "DELETE ") {
					t.Errorf("the cancelled upload removed something: %s", request)
				}
			}
			if admitted, executed := executions(t, h, id); admitted != 2 || executed != 2 {
				t.Errorf("%d of %d admitted items carry an outcome; want every one recorded", executed, admitted)
			}
		})
	}
}

// The stop reaches a job whose body is running before the failing write
// returns. The runner's notice is given on a goroutine of its own and arrives
// later, so if the stop waited for it an upload could publish in between.
// Here that notice is held back for the whole test: an upload released from
// its engine after the store stopped still publishes nothing.
func TestAStoppedStoreReachesARunningJobBeforeTheRunnerIsTold(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	block := newEngineBarrier(t)
	h.installEngine(block)
	h.mock.AddContainer("media", "standard")
	bookmark, _ := startFolderUpload(t, h, twoFileFolder(t))
	block.waitUntilEntered(t)

	notice := make(chan struct{})
	t.Cleanup(func() { close(notice) })
	runner := h.transfer.runner
	h.transfer.jobs.OnStop(func(failed string) {
		<-notice
		runner.storeStopped(failed)
	})
	breakStore(t, h)
	sent := len(h.mock.RequestLog())
	block.unblock()
	ownersGone(t, h)
	if got := publishedUnder(t, h, bookmark, "inbox/docs/one.txt", "inbox/docs/two.txt"); got != 0 {
		t.Errorf("%d files were published after the store stopped", got)
	}
	for _, request := range h.mock.RequestLog()[sent:] {
		if strings.HasPrefix(request, "PUT ") && strings.Contains(request, "/media/inbox/docs/") {
			t.Errorf("a publication was sent after the store stopped: %s", request)
		}
	}
}

// The download route removes its stage on an ordinary cancel (ADR 0048). A
// cancel made because the store stopped is the recording failure of that
// record's decision 7 for every job at once: the download places nothing
// further and its cleanup is not attempted, so the stage is still there.
func TestAStoppedStoreKeepsADownloadsStage(t *testing.T) {
	for _, stopped := range []bool{true, false} {
		t.Run(map[bool]string{true: "the store stops", false: "an ordinary cancel"}[stopped], func(t *testing.T) {
			h, _, id, dir := pausedDownload(t)
			placed := placedFiles(t, dir)
			stage := stageOf(t, dir)
			if stage == "" {
				t.Fatal("the fixture's download holds no stage while it is paused")
			}
			if stopped {
				stopStore(t, h)
			} else {
				if err := h.transfer.CancelJob(context.Background(), id); err != nil {
					t.Fatal(err)
				}
				awaitTerminal(t, h, id)
			}
			ownersGone(t, h)
			if got := placedFiles(t, dir); got != placed {
				t.Errorf("%d files are placed, %d were before the job ended", got, placed)
			}
			_, err := os.Stat(stage)
			if kept := err == nil; kept != stopped {
				t.Errorf("the stage is kept = %v (%v); want it kept only when the store had stopped", kept, err)
			}
		})
	}
}

// The runner cancels the other jobs, not the one whose record could not be
// written. That job has its own write's error and ends on that account, as it
// did when it was the only job; a cancel beside the error could change what it
// reports. Here the runner is told directly, so the store still records and
// each job's ending can be read.
func TestAStoppedStoreLeavesTheJobWhoseRecordFailedToItsOwnEnding(t *testing.T) {
	h, runner, bookmark, waitCounts := pauseHarness(t)
	runner.setMaxActiveJobs(2)
	failing := launchUnits(t, h, bookmark, 2)
	failing.expectUnit(t, 0)
	sibling := launchUnits(t, h, bookmark, 2)
	sibling.expectUnit(t, 0)

	runner.storeStopped(failing.id)
	waitCounts(runnerCounts{active: 1})
	if state := jobState(t, h, sibling.id); state != jobs.StateCancelled {
		t.Fatalf("the sibling is %s, want cancelled", state)
	}
	failing.finishUnit(t)
	failing.expectUnit(t, 1)
	failing.finishUnit(t)
	waitCounts(runnerCounts{})
	if state := jobState(t, h, failing.id); state != jobs.StateComplete {
		t.Fatalf("the job the stop was attributed to is %s; the runner must leave its ending to it", state)
	}
}

// A deletion job cancelled because another job's record stopped the store
// cannot record what it leaves untouched: the store refuses every deletion
// record from then on. Saved as a terminal row it could not say which members
// were never requested, so it is left open with its status unavailable and
// says why, as a deletion whose own record was lost is, and the restart
// settles it. The stop lands while the job lists, with no member in flight.
func TestAStoppedStoreLeavesADeletionJobOpen(t *testing.T) {
	for _, kind := range []string{"prefix", "segments"} {
		t.Run(kind, func(t *testing.T) {
			h, service, bookmark := deleteHarness(t)
			ctx := context.Background()
			h.mock.AddContainer("media", "standard")
			h.mock.AddContainer("media_segments", "standard")
			for i := range 3 {
				seedObject(h.mock, "media", fmt.Sprintf("logs/%d", i), "x", nil)
				seedObject(h.mock, "media_segments", fmt.Sprintf("owned/pfx/%06d", i), "seg", nil)
			}
			h.mock.PutObject("media", "ours.bin", swifttest.Object{Manifest: "media_segments/owned/pfx/"})
			swept, operation := "/media", "delete.prefix.unavailable"
			start := func() (string, error) {
				return service.StartDelete(ctx, DeleteRequestDTO{BookmarkID: bookmark, Container: "media", Mode: ModePrefix, Prefix: "logs/", Confirm: "media/logs/"})
			}
			if kind == "segments" {
				swept, operation = "/media_segments", "segments.delete.unavailable"
				reviewed := reviewOf(t, service, bookmark, "media", "ours.bin")
				start = func() (string, error) {
					return service.DeleteManifestWithSegments(ctx, SegmentsDeleteRequestDTO{
						BookmarkID: bookmark, Container: "media", Name: "ours.bin", Confirm: "ours.bin", Force: true, Reviewed: reviewed})
				}
			}
			runner := h.transfer.runner
			var once sync.Once
			h.mock.BeforeRequest = func(method, path string) {
				if method != "GET" || !strings.HasSuffix(path, swept) {
					return
				}
				// Only the job's own listing: a start lists too, before its
				// job is launched.
				runner.mu.Lock()
				launched := false
				for owner := range runner.owners {
					launched = launched || owner.jobID != ""
				}
				runner.mu.Unlock()
				if launched {
					once.Do(func() { stopStore(t, h) })
				}
			}
			id, err := start()
			if err != nil {
				t.Fatal(err)
			}
			ownersGone(t, h)
			h.mock.BeforeRequest = nil

			for _, request := range h.mock.RequestLog() {
				if strings.HasPrefix(request, "DELETE ") && strings.Contains(request, swept+"/") {
					t.Errorf("a member was deleted after the store stopped: %s", request)
				}
			}
			// Read past the stopped store, which refuses the service's own read.
			job, err := jobs.NewStore(h.db).Get(ctx, id)
			if err != nil || jobs.Terminal(job.State) {
				t.Fatalf("the job = %s, %v; want it left open, not saved as finished", job.State, err)
			}
			events, err := audit.NewStore(h.db).List(ctx, audit.Query{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			said := 0
			for _, event := range events {
				if event.Operation == operation && strings.Contains(event.Summary, "job "+id) && strings.Contains(event.Summary, "the job store stopped recording") {
					said++
				}
			}
			if said != 1 {
				t.Errorf("the job said that the store stopped beside it %d times, want once: %+v", said, events)
			}
		})
	}
}

// The same for a download that is running and held in its engine when the
// store stops, rather than parked: its stage is kept and the report says
// cleanup was not attempted. This is the case that relies on the route asking
// the store at cleanup, because a running download leaves through its engine
// and not through its pause.
func TestAStoppedStoreKeepsARunningDownloadsStage(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	block := newEngineBarrier(t)
	h.installEngine(block)
	h.mock.AddContainer("media", "standard")
	for _, name := range []string{"scope/a.bin", "scope/b.bin"} {
		seedObject(h.mock, "media", name, "bytes of "+name, nil)
	}
	bookmark := h.transfer.connections.ActiveBookmarkID()
	dir := t.TempDir()
	h.transfer.setFilePicker(fakePicker{dir: dir})
	picked, err := h.transfer.PickDownloadDir(context.Background(), SelectionScopeDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/"})
	if err != nil || picked == nil {
		t.Fatalf("pick: %+v %v", picked, err)
	}
	id, err := h.transfer.StartDownload(context.Background(), DownloadRequestDTO{BookmarkID: bookmark, Container: "media", Prefix: "scope/", DestDirToken: picked.Token})
	if err != nil {
		t.Fatal(err)
	}
	block.waitUntilEntered(t)
	stopStore(t, h)
	block.unblock()
	ownersGone(t, h)
	if _, err := os.Stat(filepath.Join(dir, ".aeronautes-stage-"+id)); err != nil {
		t.Fatalf("the stage of a download stopped with the store was removed: %v", err)
	}
	for _, name := range []string{"a.bin", "b.bin"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was placed after the store stopped", name)
		}
	}
}

// A selected set admits its members before it runs, so one cancelled while
// queued settles each as not attempted. If the store stops during that
// settlement, here because the database refuses its first record, the job is
// left open and says why, as one stopped while running is: a terminal row
// would leave the members unsettled for good, because the restart passes over
// finished jobs. The control is the same cancel with the store recording.
func TestAStoppedStoreLeavesAQueuedSelectedDeletionOpen(t *testing.T) {
	for _, stops := range []bool{true, false} {
		t.Run(map[bool]string{true: "the store stops", false: "the store keeps recording"}[stops], func(t *testing.T) {
			h, service := selectedHarness(t)
			ctx := context.Background()
			runner := h.transfer.runner
			runner.setMaxActiveJobs(1)
			if !runner.acquireSlot(ctx) {
				t.Fatal("could not occupy the slot")
			}
			released := false
			release := func() {
				if !released {
					released = true
					runner.releaseSlot()
				}
			}
			t.Cleanup(release)
			id, err := service.StartSelectedDelete(ctx, selectedRequest(h, rowsOf(t, h, "normal/hello.txt", "empty-object")))
			if err != nil {
				t.Fatal(err)
			}
			if stops {
				refusal := fmt.Sprintf(`CREATE TRIGGER refuse_settlement BEFORE INSERT ON job_item_observations
					WHEN NEW.item_id IN (SELECT id FROM job_items WHERE job_id = '%s')
					BEGIN SELECT RAISE(ABORT, 'fixture settlement refusal'); END`, id)
				if err := h.db.WriteTx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(refusal); return err }); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.transfer.CancelJob(ctx, id); err != nil {
				t.Fatal(err)
			}
			ownersGone(t, h)
			release()

			job, err := jobs.NewStore(h.db).Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got := deletesUnder(h, "fixtures"); len(got) != 0 {
				t.Errorf("a cancelled queued deletion sent %v", got)
			}
			if !stops {
				if job.State != jobs.StateCancelled {
					t.Fatalf("the control = %s, want cancelled", job.State)
				}
				return
			}
			if jobs.Terminal(job.State) {
				t.Fatalf("the job = %s; want it left open for the restart to settle", job.State)
			}
			events, err := audit.NewStore(h.db).List(ctx, audit.Query{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			said := false
			for _, event := range events {
				said = said || event.Operation == "delete.selected.unavailable" && strings.Contains(event.Summary, "job "+id)
			}
			if !said {
				t.Errorf("the job did not say why it was left open: %+v", events)
			}
		})
	}
}
