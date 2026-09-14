//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestDesktopHeldDownload(t *testing.T) {
	if mode := os.Getenv("AERONAUTES_HELD_DESKTOP"); mode != "" {
		testDesktopHeldDownload(t, strings.Contains(mode, "callback"), strings.Contains(mode, "error"))
		return
	}
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone is required for the actual shell download")
	}
	for _, mode := range []string{"return", "callback", "return_error", "callback_error"} {
		t.Run(mode, func(t *testing.T) {
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, self, "-test.run=^TestDesktopHeldDownload$", "-test.v", "-test.timeout=25s")
			cmd.Env = append(os.Environ(), "AERONAUTES_HELD_DESKTOP="+mode)
			output, err := cmd.CombinedOutput()
			t.Logf("isolated actual caller: %s", output)
			if err != nil || !bytes.Contains(output, []byte("held desktop checked cleanup complete")) {
				t.Fatalf("actual caller did not prove cleanup: %v", err)
			}
		})
	}
}

func testDesktopHeldDownload(t *testing.T, callback, runError bool) {
	t.Helper()
	t.Setenv("AERONAUTES_STATE_DIR", t.TempDir())
	root := t.TempDir()
	cluster := swifttest.New("test:tester", "testing")
	body := []byte("the whole job owns final cleanup")
	cluster.PutObject("shutdown", "scope/file", swifttest.Object{
		Data: body, Bytes: int64(len(body)), ETag: fmt.Sprintf("%x", md5.Sum(body)),
	})
	server := httptest.NewServer(cluster)
	defer server.Close()
	gate := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate) }) }
	defer unblock()
	shutdown := &shellShutdown{}
	runFailure := errors.New("injected runtime failure with held download")
	var jobID string
	var jobService *app.JobService
	var checkedClose bool
	observed := make(chan error, 1)
	launch := func(options application.Options) error {
		var bookmarkService *app.BookmarkService
		var browseService *app.BrowseService
		var transfer *app.TransferService
		for _, bound := range options.Services {
			switch service := bound.Instance().(type) {
			case *app.BookmarkService:
				bookmarkService = service
			case *app.BrowseService:
				browseService = service
			case *app.TransferService:
				transfer = service
			case *app.JobService:
				jobService = service
			}
		}
		if bookmarkService == nil || browseService == nil || transfer == nil || jobService == nil || options.OnShutdown == nil {
			t.Fatal("actual caller omitted its services or shutdown hook")
		}
		saved, err := bookmarkService.Save(t.Context(), app.BookmarkDTO{
			DisplayName: "owned shutdown fixture", AuthURL: server.URL + "/auth/v1.0", Username: "test:tester",
		}, "testing")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := browseService.Connect(t.Context(), saved.ID); err != nil {
			t.Fatal(err)
		}
		app.WireFilePicker(transfer, shellPicker{directory: root})
		picked, err := transfer.PickDownloadDir(t.Context(),
			app.SelectionScopeDTO{BookmarkID: saved.ID, Container: "shutdown", Prefix: "scope/"})
		if err != nil {
			t.Fatal(err)
		}
		pickerHandles := shellDirectoryHandles(t, root)
		if pickerHandles != 1 {
			t.Fatalf("picker descriptor baseline = %d, want 1", pickerHandles)
		}
		entered := make(chan context.Context, 1)
		app.WireDownloadStageBoundary(transfer, func(ctx context.Context, _ string) error {
			entered <- ctx
			<-gate
			return ctx.Err()
		})
		caller, cancelCaller := context.WithCancel(t.Context())
		defer cancelCaller()
		jobID, err = transfer.StartDownload(caller, app.DownloadRequestDTO{
			BookmarkID: saved.ID, Container: "shutdown", Prefix: "scope/", DestDirToken: picked.Token,
		})
		if err != nil {
			t.Fatal(err)
		}
		jobContext := shellAwait(t, entered)
		cancelCaller()
		if jobContext.Err() != nil {
			t.Fatal("accepted download inherited caller cancellation")
		}
		// Starting consumes the selection and closes the pick's own
		// handle (ADR 0041), so the job's handle is measured against the
		// settled count rather than the pre-start one.
		settled := pickerHandles - 1
		if held := shellDirectoryHandles(t, root); held <= settled {
			t.Fatalf("no owned download handle: held=%d settled=%d", held, settled)
		}
		closed := make(chan struct{})
		closeDatabase := shutdown.closeDatabase
		shutdown.closeDatabase = func() error {
			defer close(closed)
			job, err := jobService.Get(t.Context(), jobID)
			if err != nil || job.State != "partial" || job.FinishedAt == "" ||
				job.Counters.ObjectsPlanned != 1 || job.Counters.ObjectsFailed != 1 ||
				job.Counters.ObjectsCopied != 0 || job.Counters.ObjectsVerified != 0 ||
				// The cancelled job's terminal record is what this asserts the
				// ordering against. It no longer carries a directory-not-empty
				// rmdir failure, because cleanup removes the files this job
				// staged and the stage goes with them (ADR 0048).
				!strings.Contains(job.Error, "context canceled") {
				t.Errorf("database close preceded terminal job: job=%+v err=%v", job, err)
			}
			if after := shellDirectoryHandles(t, root); after != settled {
				t.Errorf("database close preceded job handle release: got=%d want=%d", after, settled)
			}
			if _, err := bookmarkService.Save(t.Context(), saved, "replacement"); err != nil {
				t.Errorf("database close preceded final bookmark pin release: %v", err)
			}
			checkedClose = true
			return closeDatabase()
		}
		go func() {
			defer unblock()
			select {
			case <-jobContext.Done():
			case <-time.After(5 * time.Second):
				observed <- errors.New("actual shell did not cancel its owned download")
				return
			}
			if _, err := jobService.Get(context.Background(), jobID); err != nil {
				observed <- fmt.Errorf("held owner lost database: %w", err)
				return
			}
			if _, err := bookmarkService.Save(context.Background(), saved, "replacement"); !errors.Is(err, bookmarks.ErrInUse) {
				observed <- fmt.Errorf("held owner lost bookmark pin: %v", err)
				return
			}
			if acquired, err := shellOtherProcessLock(context.Background(), shellStateDir(t)); acquired || err != nil {
				observed <- fmt.Errorf("held owner lost process lock: acquired=%t error=%v", acquired, err)
				return
			}
			select {
			case <-closed:
				observed <- errors.New("database closed while final job boundary was held")
			default:
				observed <- nil
			}
		}()
		if callback {
			options.OnShutdown()
		} else {
			// Run return must perform the same drain even if the runtime did
			// not invoke its callback. The observer releases the final gate.
			return shellRunError(runError, runFailure)
		}
		return shellRunError(runError, runFailure)
	}
	err := runDesktop(buildIdentity(), shutdown, launch, shellSessionSecrets)
	if err := shellAwait(t, observed); err != nil {
		t.Error(err)
	}
	if errors.Is(err, runFailure) != runError {
		t.Fatalf("lost runtime result: %v", err)
	}
	if !checkedClose {
		t.Fatal("actual caller skipped checked database close")
	}
	if _, err := jobService.Get(t.Context(), jobID); err == nil {
		t.Fatal("successful return left the state database open")
	}
	if acquired, err := shellOtherProcessLock(t.Context(), shellStateDir(t)); !acquired || err != nil {
		t.Fatalf("successful return did not release lock: acquired=%t error=%v", acquired, err)
	}
	fmt.Println("held desktop checked cleanup complete")
}

func shellRunError(fail bool, err error) error {
	if fail {
		return err
	}
	return nil
}

type shellPicker struct{ directory string }

func (p shellPicker) PickFiles(string) ([]string, error) {
	return nil, errors.New("files not requested")
}
func (p shellPicker) PickDirectory(string) (string, error) { return p.directory, nil }

func shellDirectoryHandles(t *testing.T, path string) int {
	t.Helper()
	want, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
		if os.IsNotExist(err) {
			continue // ReadDir's own descriptor may already be closed.
		}
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(want, info) {
			count++
		}
	}
	return count
}

func TestShellProcessLockHelper(t *testing.T) {
	path := os.Getenv("AERONAUTES_SHELL_LOCK_PROBE")
	if path == "" {
		return
	}
	release, err := app.AcquireInstanceLock(path)
	if err == nil {
		release()
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal(err)
	}
	fmt.Printf("shell process lock acquired=%t\n", err == nil)
}

func shellOtherProcessLock(ctx context.Context, stateDir string) (bool, error) {
	self, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate lock process: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestShellProcessLockHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "AERONAUTES_SHELL_LOCK_PROBE="+stateDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("second lock process: %w: %s", err, output)
	}
	if bytes.Contains(output, []byte("shell process lock acquired=true")) {
		return true, nil
	}
	if !bytes.Contains(output, []byte("shell process lock acquired=false")) {
		return false, fmt.Errorf("missing lock process result: %s", output)
	}
	return false, nil
}

func TestDesktopCloseFailureRetainsResources(t *testing.T) {
	t.Setenv("AERONAUTES_STATE_DIR", t.TempDir())
	shutdown := &shellShutdown{}
	defer runtime.KeepAlive(shutdown)
	var closeDatabase func() error
	defer func() {
		if closeDatabase != nil {
			if err := closeDatabase(); err != nil {
				t.Error(err)
			}
		}
		if shutdown.releaseLock != nil {
			shutdown.releaseLock()
		}
	}()
	failure := errors.New("injected close failure with an open database")
	var closeCalls int
	var jobService *app.JobService
	err := runDesktop(buildIdentity(), shutdown, func(options application.Options) error {
		for _, bound := range options.Services {
			if service, ok := bound.Instance().(*app.JobService); ok {
				jobService = service
			}
		}
		closeDatabase = shutdown.closeDatabase
		shutdown.closeDatabase = func() error { closeCalls++; return failure }
		return nil
	}, shellSessionSecrets)
	if !errors.Is(err, failure) || jobService == nil {
		t.Fatalf("actual caller lost close failure or services: %v", err)
	}
	for range 3 {
		if err := shutdown.close(); !errors.Is(err, failure) {
			t.Fatalf("repeated cleanup lost original failure: %v", err)
		}
	}
	if closeCalls != 1 {
		t.Fatalf("failed close retried %d times", closeCalls)
	}
	runtime.GC()
	if _, err := jobService.List(t.Context(), "", false, 1); err != nil {
		t.Fatalf("fault did not preserve a real open database: %v", err)
	}
	if acquired, err := shellOtherProcessLock(t.Context(), shellStateDir(t)); acquired || err != nil {
		t.Fatalf("failed close released process lock: acquired=%t error=%v", acquired, err)
	}
}

func TestShellShutdownDrainFailureStaysBlocked(t *testing.T) {
	if os.Getenv("AERONAUTES_DRAIN_ERROR_PROBE") == "1" {
		testShellBlockedDrain(t)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestShellShutdownDrainFailureStaysBlocked$", "-test.v", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "AERONAUTES_DRAIN_ERROR_PROBE=1", "AERONAUTES_STATE_DIR="+state)
	output, err := cmd.CombinedOutput()
	t.Logf("blocked drain subprocess: %s", output)
	if err != nil || !bytes.Contains(output, []byte("failed drain kept database and lock while caller blocked")) {
		t.Fatalf("failed drain did not retain ownership: %v", err)
	}
	if acquired, err := shellOtherProcessLock(t.Context(), state); !acquired || err != nil {
		t.Fatalf("test-process exit did not release lock: acquired=%t error=%v", acquired, err)
	}
}

func testShellBlockedDrain(t *testing.T) {
	t.Helper()
	entered := make(chan struct{})
	log.SetOutput(&shellDrainLog{entered: entered})
	shutdown := &shellShutdown{}
	services := make(chan *app.JobService, 1)
	done := make(chan error, 1)
	go func() {
		done <- runDesktop(buildIdentity(), shutdown, func(options application.Options) error {
			for _, bound := range options.Services {
				if service, ok := bound.Instance().(*app.JobService); ok {
					services <- service
				}
			}
			shutdown.drain = func(context.Context) error { return errors.New("injected unexpected drain failure") }
			return nil
		}, shellSessionSecrets)
	}()
	jobService := shellAwait(t, services)
	shellAwait(t, entered)
	runtime.GC()
	if _, err := jobService.List(t.Context(), "", false, 1); err != nil {
		t.Fatalf("failed drain permitted database closure: %v", err)
	}
	if acquired, err := shellOtherProcessLock(t.Context(), shellStateDir(t)); acquired || err != nil {
		t.Fatalf("failed drain permitted lock release: acquired=%t error=%v", acquired, err)
	}
	select {
	case err := <-done:
		t.Fatalf("failed drain let actual caller return: %v", err)
	default:
	}
	fmt.Println("failed drain kept database and lock while caller blocked")
}

type shellDrainLog struct {
	entered chan struct{}
	once    sync.Once
}

func (w *shellDrainLog) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("owned work did not drain: injected unexpected drain failure")) {
		w.once.Do(func() { close(w.entered) })
	}
	return os.Stdout.Write(data)
}
