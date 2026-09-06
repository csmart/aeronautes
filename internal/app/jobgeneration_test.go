package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func generationJobStart(t *testing.T, h *harness, kind, source string) (func() (string, error), []string) {
	t.Helper()
	h.installEngine(nil)
	h.mock.AddContainer("media", "standard")
	seedObject(h.mock, "media", "scope/file", "contents", nil)
	ids := []string{source}
	var start func() (string, error)
	switch kind {
	case "copy":
		destination, mock := secondCluster(t, h)
		mock.AddContainer("backup", "standard")
		ids = append(ids, destination)
		request := CopyRequestDTO{
			SourceBookmarkID: source, SourceContainer: "media", SourcePrefix: "scope/",
			DestBookmarkID: destination, DestContainer: "backup", DestPrefix: "scope/",
		}
		start = func() (string, error) { return h.transfer.StartCopy(t.Context(), request) }
	case "upload":
		file := filepath.Join(t.TempDir(), "upload.txt")
		if err := os.WriteFile(file, []byte("upload"), 0o600); err != nil {
			t.Fatal(err)
		}
		h.transfer.setFilePicker(fakePicker{files: []string{file}})
		picked, pickErr := h.transfer.PickUploadFiles(t.Context())
		if pickErr != nil {
			t.Fatal(pickErr)
		}
		start = func() (string, error) {
			return h.transfer.StartUpload(t.Context(), UploadRequestDTO{
				BookmarkID: source, Container: "media", Prefix: "new/", SelectionToken: picked.Token,
			})
		}
	case "download":
		h.transfer.setFilePicker(fakePicker{dir: t.TempDir()})
		picked, pickErr := h.transfer.PickDownloadDir(t.Context())
		if pickErr != nil {
			t.Fatal(pickErr)
		}
		start = func() (string, error) {
			return h.transfer.StartDownload(t.Context(), DownloadRequestDTO{
				BookmarkID: source, Container: "media", Prefix: "scope/", DestDirToken: picked.Token,
			})
		}
	case "delete":
		service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
		start = func() (string, error) {
			return service.StartDelete(t.Context(), DeleteRequestDTO{
				BookmarkID: source, Container: "media", Prefix: "scope/", Mode: ModePrefix, Confirm: "media",
			})
		}
	case "benchmark":
		service := NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
		start = func() (string, error) {
			return service.StartBenchmark(t.Context(), BenchmarkRequestDTO{BookmarkID: source, Confirm: "AUTH_test"})
		}
	default:
		t.Fatalf("unknown job kind %q", kind)
	}
	return start, ids
}

func TestQueuedJobsKeepCredentialGenerations(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			source := h.transfer.connections.ActiveBookmarkID()
			runner := h.transfer.runner
			runner.setMaxActiveJobs(1)
			// An occupied slot acknowledges queueing without another job's
			// pins masking a missing pin in the job under test.
			if !runner.acquireSlot(t.Context()) {
				t.Fatal("could not occupy the scheduler slot")
			}
			t.Cleanup(runner.releaseSlot)
			start, ids := generationJobStart(t, h, kind, source)
			jobID, err := start()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = h.transfer.CancelJob(context.Background(), jobID)
				awaitTerminal(t, h, jobID)
			})
			job, err := h.jobsSvc.Get(t.Context(), jobID)
			if err != nil || job.State != "queued" || job.StartedAt != "" {
				t.Fatalf("job did not remain queued: %+v, %v", job, err)
			}
			store := h.bookmarks.store
			for _, id := range ids {
				bookmark, err := store.Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				bookmark.DisplayName = "renamed while queued"
				if _, err := store.Save(t.Context(), bookmark, nil); err != nil {
					t.Fatalf("cosmetic edit refused: %v", err)
				}
				if _, err := store.Save(t.Context(), bookmark, []byte("replacement")); !errors.Is(err, bookmarks.ErrInUse) {
					t.Errorf("queued key replacement = %v; want job pin refusal", err)
				}
				if err := store.Delete(t.Context(), id); !errors.Is(err, bookmarks.ErrInUse) {
					t.Errorf("queued logical deletion = %v; want job pin refusal", err)
				}
			}
			if err := h.transfer.CancelJob(t.Context(), jobID); err != nil {
				t.Fatal(err)
			}
			if job := awaitTerminal(t, h, jobID); job.State != "cancelled" || job.StartedAt != "" {
				t.Fatalf("queued cancellation started work: %+v", job)
			}
			// Terminal state can precede the runner's final cleanup. Accept
			// only the actual successful edit as evidence of pin release.
			awaitJobBindingsReleased(t, h, ids)
		})
	}
}

func TestFailedJobCreationReleasesGenerations(t *testing.T) {
	for _, kind := range []string{"copy", "upload", "download", "delete", "benchmark"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			start, ids := generationJobStart(t, h, kind, h.transfer.connections.ActiveBookmarkID())
			if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TRIGGER refuse_job BEFORE INSERT ON jobs
					BEGIN SELECT RAISE(ABORT, 'injected job creation failure'); END`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if id, err := start(); id != "" || err == nil || !strings.Contains(err.Error(), "injected job creation failure") {
				t.Fatalf("job creation fault not reached: id=%q, err=%v", id, err)
			}
			if jobs, err := h.jobsSvc.List(t.Context(), "", false, 10); err != nil || len(jobs) != 0 {
				t.Fatalf("failed start created durable jobs: %v", err)
			}
			assertJobBindingsReleased(t, h, ids)
		})
	}
}

func assertJobBindingsReleased(t *testing.T, h *harness, ids []string) {
	t.Helper()
	for _, id := range ids {
		bookmark, err := h.bookmarks.store.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.bookmarks.store.Save(t.Context(), bookmark, []byte("replacement")); err != nil {
			t.Fatalf("job retained a generation pin: %v", err)
		}
	}
}

func TestJobAdmissionPinsBeforeEngineSetup(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	start, ids := generationJobStart(t, h, "copy", h.transfer.connections.ActiveBookmarkID())
	entered := make(chan struct{}, 1)
	gate, release := connectionGate(t)
	h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) {
		entered <- struct{}{}
		<-gate
		return nil, errors.New("injected engine setup failure")
	})
	result := make(chan error, 1)
	go func() {
		_, err := start()
		result <- err
	}()
	awaitConnection(t, entered)
	if jobs, err := h.jobsSvc.List(t.Context(), "", false, 10); err != nil || len(jobs) != 0 {
		t.Fatalf("engine barrier was reached after durable creation: %v", err)
	}
	for _, id := range ids {
		bookmark, err := h.bookmarks.store.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.bookmarks.store.Save(t.Context(), bookmark, []byte("replacement")); !errors.Is(err, bookmarks.ErrInUse) {
			t.Errorf("starting job allowed key replacement: %v", err)
		}
		if err := h.bookmarks.store.Delete(t.Context(), id); !errors.Is(err, bookmarks.ErrInUse) {
			t.Errorf("starting job allowed deletion before its durable row: %v", err)
		}
	}
	release()
	if err := awaitConnection(t, result); err == nil || !strings.Contains(err.Error(), "injected engine setup failure") {
		t.Fatalf("engine setup refusal = %v", err)
	}
	assertJobBindingsReleased(t, h, ids)
}

func TestFailedSecondJobBindingReleasesFirst(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	id := h.transfer.connections.ActiveBookmarkID()
	if release, err := h.transfer.connections.pinForJob(t.Context(), id, "missing"); release != nil || !errors.Is(err, bookmarks.ErrNotFound) {
		t.Fatalf("missing second binding was admitted: %v", err)
	}
	assertJobBindingsReleased(t, h, []string{id})
}

func TestUploadRecoveryFailureReleasesGenerations(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	start, ids := generationJobStart(t, h, "upload", h.transfer.connections.ActiveBookmarkID())
	if err := h.db.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_recovery BEFORE INSERT ON job_recovery
			BEGIN SELECT RAISE(ABORT, 'injected recovery creation failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if id, err := start(); id != "" || err == nil || !strings.Contains(err.Error(), "injected recovery creation failure") {
		t.Fatalf("recovery creation fault not reached: id=%q, err=%v", id, err)
	}
	rows, err := h.jobsSvc.List(t.Context(), "", false, 10)
	if err != nil || len(rows) != 1 || rows[0].State != "failed" || rows[0].StartedAt != "" {
		t.Fatalf("failed upload admission was not recorded: %+v, %v", rows, err)
	}
	if _, registered := h.transfer.runner.cancel(rows[0].ID); registered {
		t.Fatal("failed upload remained cancel-addressable")
	}
	assertJobBindingsReleased(t, h, ids)
}

type cancellationCleanupEngine struct {
	entered   chan struct{}
	cancelled chan struct{}
	cleanup   <-chan struct{}
}

func (e *cancellationCleanupEngine) Copy(ctx context.Context, _, _ string, _ []string, _ func(rclone.Event)) (*rclone.Result, error) {
	close(e.entered)
	<-ctx.Done()
	close(e.cancelled)
	<-e.cleanup
	return &rclone.Result{Cancelled: true}, nil
}

func (e *cancellationCleanupEngine) CopyTo(ctx context.Context, src, dst string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	return e.Copy(ctx, src, dst, nil, onEvent)
}

func TestRunningJobKeepsGenerationsDuringCancellationCleanup(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	start, ids := generationJobStart(t, h, "copy", h.transfer.connections.ActiveBookmarkID())
	cleanup, releaseCleanup := connectionGate(t)
	engine := &cancellationCleanupEngine{entered: make(chan struct{}), cancelled: make(chan struct{}), cleanup: cleanup}
	h.transfer.setEngineFactory(func(transfercopy.RemoteSource, rclone.Options) (transfercopy.Engine, error) {
		return engine, nil
	})
	jobID, err := start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.transfer.CancelJob(context.Background(), jobID)
		releaseCleanup()
		awaitTerminal(t, h, jobID)
	})
	awaitConnection(t, engine.entered)
	assertJobBindingsPinned(t, h, ids)
	if err := h.transfer.CancelJob(t.Context(), jobID); err != nil {
		t.Fatal(err)
	}
	awaitConnection(t, engine.cancelled)
	assertJobBindingsPinned(t, h, ids)
	releaseCleanup()
	awaitTerminal(t, h, jobID)
	awaitJobBindingsReleased(t, h, ids)
}

func assertJobBindingsPinned(t *testing.T, h *harness, ids []string) {
	t.Helper()
	for _, id := range ids {
		bookmark, err := h.bookmarks.store.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.bookmarks.store.Save(t.Context(), bookmark, []byte("replacement")); !errors.Is(err, bookmarks.ErrInUse) {
			t.Errorf("job released its credential before cleanup finished: %v", err)
		}
		if err := h.bookmarks.store.Delete(t.Context(), id); !errors.Is(err, bookmarks.ErrInUse) {
			t.Errorf("job allowed deletion before cleanup finished: %v", err)
		}
	}
}

func awaitJobBindingsReleased(t *testing.T, h *harness, ids []string) {
	t.Helper()
	for _, id := range ids {
		bookmark, err := h.bookmarks.store.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, err = h.bookmarks.store.Save(t.Context(), bookmark, []byte("replacement"))
			if err == nil {
				break
			}
			if !errors.Is(err, bookmarks.ErrInUse) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("finished job did not release its binding: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func TestJobRunnerKeepsPinsThroughFinalBodyCleanup(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	id := h.transfer.connections.ActiveBookmarkID()
	unpin, err := h.transfer.connections.pinForJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unpin)
	job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy, SourceBookmarkID: id})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cleanup, releaseCleanup := connectionGate(t)
	terminal := make(chan error, 1)
	done := make(chan struct{})
	runner := h.transfer.runner
	runner.register(job.ID, cancel)
	runner.launch(ctx, job.ID, id, func() { unpin(); close(done) }, func(context.Context) {
		runner.release(job.ID)
		terminal <- h.transfer.jobs.Transition(context.Background(), job.ID, jobs.StateCancelled)
		<-cleanup
	})
	t.Cleanup(func() { releaseCleanup(); awaitConnection(t, done) })
	if err := awaitConnection(t, terminal); err != nil {
		t.Fatal(err)
	}
	if _, registered := runner.cancel(job.ID); registered {
		t.Fatal("terminal body remained cancel-addressable")
	}
	assertJobBindingsPinned(t, h, []string{id})
	releaseCleanup()
	awaitConnection(t, done)
	assertJobBindingsReleased(t, h, []string{id})
}
