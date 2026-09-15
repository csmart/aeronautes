//go:build linux

package systemtest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"golang.org/x/sys/unix"
)

func TestShutdownChildMock(t *testing.T) {
	shutdownChildCases(t, findMockHarness)
}

func TestShutdownChildRealSwift(t *testing.T) {
	shutdownChildCases(t, New)
}

func shutdownChildCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, mode := range []string{"cancel", "escalate"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := exec.LookPath("rclone"); err != nil {
				t.Skip("pinned rclone is required for the shutdown consumer")
			}
			shutdownChild(t, makeHarness(t), mode)
		})
	}
}

func TestShutdownParentHelper(t *testing.T) {
	input := os.Getenv("AERONAUTES_SHUTDOWN_INPUT")
	if input == "" {
		return
	}
	var request lifetimeRequest
	lifetimeReadJSON(t, input, &request)
	release, err := app.AcquireInstanceLock(request.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	database, err := db.Open(t.Context(), filepath.Join(request.StateDir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	store := jobs.NewStore(database)
	bookmarkStore := bookmarks.NewStore(database, secrets.NewSessionStore())
	connections := app.NewConnections(bookmarkStore)
	bookmarkService := app.NewBookmarkService(bookmarkStore, connections, store)
	saved, err := bookmarkService.Save(t.Context(), request.Bookmark, request.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := connections.Connect(t.Context(), saved.ID); err != nil {
		t.Fatal(err)
	}
	auditStore := audit.NewStore(database)
	runner := app.NewJobRunner(store, auditStore)
	find := app.NewFindService(connections)
	mutate := app.NewMutateService(connections, auditStore, evidence.NewStore(database))
	transfer := app.NewTransferServiceWithRunner(connections, store, runner)
	h := &Harness{T: t, ctx: t.Context(), Transfer: transfer, Jobs: app.NewJobService(store), Primary: &Account{BookmarkID: saved.ID}}
	id := downloadCancelStart(t, h, request.Container, "scope/", request.Root)
	lifetimeWriteJSON(t, request.Output, id)
	commands := bufio.NewReader(os.Stdin)
	command, err := commands.ReadString('\n')
	if err != nil || command != "shutdown\n" {
		t.Fatalf("shutdown command: %q %v", command, err)
	}
	child := lifetimeChild(t, os.Getpid())
	if err := app.ShutdownWork(context.Background(), runner, find, mutate); err != nil {
		t.Fatal(err)
	}
	var status unix.WaitStatus
	if pid, err := unix.Wait4(child, &status, unix.WNOHANG, nil); pid != -1 || !errors.Is(err, unix.ECHILD) {
		t.Fatalf("consumer returned before its child was reaped: pid=%d status=%v error=%v", pid, status, err)
	}
	if err := database.Reader().PingContext(t.Context()); err != nil {
		t.Fatalf("database unavailable immediately after drain: %v", err)
	}
	terminal := uploadRecoveryRead(t, database, id)
	lifetimeWriteJSON(t, request.Output+".drained", terminal)
	command, err = commands.ReadString('\n')
	if err != nil || command != "exit\n" {
		t.Fatalf("resource release command: %q %v", command, err)
	}
}

func shutdownStart(t *testing.T, request lifetimeRequest) *lifetimeProcess {
	t.Helper()
	input := request.Output + ".input"
	lifetimeWriteJSON(t, input, request)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestShutdownParentHelper$", "-test.timeout=40s")
	cmd.Env = append(os.Environ(), "AERONAUTES_SHUTDOWN_INPUT="+input)
	log, err := os.OpenFile(request.Output+".log", os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := log.Close(); err != nil {
			t.Error(err)
		}
	})
	cmd.Stdout, cmd.Stderr = log, log
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := in.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &lifetimeProcess{cmd: cmd, input: in, done: make(chan error, 1), log: log.Name()}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() { lifetimeStopParent(t, p) })
	return p
}

func shutdownChild(t *testing.T, h *Harness, mode string) {
	container := h.Container(h.Primary, "shutdown")
	body := bytes.Repeat([]byte("owned shutdown body."), 16384)
	h.Seed(h.Primary, container, "scope/file.bin", body, nil)
	h.Seed(h.Secondary, h.Container(h.Secondary, "collateral"), "untouched", []byte("other account"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	hold, trace := downloadCancelBodyProxy(t, h, container, "scope/file.bin", 64*1024)
	allBookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var bookmark app.BookmarkDTO
	for _, candidate := range allBookmarks {
		if candidate.ID == h.Primary.BookmarkID {
			bookmark = candidate
		}
	}
	bookmark.ID = ""
	root, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	request := lifetimeRequest{Bookmark: bookmark, Key: h.Primary.key, StateDir: state, Root: root,
		Container: container, Output: filepath.Join(state, "started.json")}
	parent := shutdownStart(t, request)
	lifetimeAwait(t, "shutdown parent start", func() bool { _, err := os.Stat(request.Output); return err == nil })
	var id string
	lifetimeReadJSON(t, request.Output, &id)
	select {
	case <-hold.reached:
	case <-time.After(15 * time.Second):
		t.Fatal("real child did not reach the body barrier")
	}
	stage := filepath.Join(root, ".aeronautes-stage-"+id)
	var writing string
	lifetimeAwait(t, "real child partial bytes", func() bool {
		paths, err := filepath.Glob(filepath.Join(stage, "*"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err == nil && len(data) >= 64*1024 && bytes.Equal(data[:64*1024], body[:64*1024]) {
				writing = path
				return true
			}
		}
		return false
	})
	pid := lifetimeChild(t, parent.cmd.Process.Pid)
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			t.Error(err)
		}
		if !lifetimeExited(t, pidfd, 2*time.Second) {
			t.Error("owned shutdown child survived cleanup")
		}
		lifetimeStopParent(t, parent)
		if err := unix.Close(pidfd); err != nil {
			t.Error(err)
		}
	})
	lifetimeCheckChild(t, pid, parent.cmd.Process.Pid, pidfd)
	before := lifetimeSnapshot(t, root)
	partial, ok := before[strings.TrimPrefix(writing, root+string(os.PathSeparator))]
	if !ok || partial.Size != 64*1024 || partial.Hash != fmt.Sprintf("%x", sha256.Sum256(body[:64*1024])) || len(before) != 3 {
		t.Fatalf("write barrier was not exact: %+v", before)
	}
	if release, err := app.AcquireInstanceLock(state); err == nil {
		release()
		t.Fatal("live parent did not exclude another process")
	}
	if mode == "escalate" {
		if err := unix.PidfdSendSignal(pidfd, unix.SIGSTOP, nil, 0); err != nil {
			t.Fatal(err)
		}
		lifetimeAwaitStopped(t, pid)
	}
	start := time.Now()
	if _, err := io.WriteString(parent.input, "shutdown\n"); err != nil {
		t.Fatal(err)
	}
	lifetimeAwait(t, "owned consumer drain", func() bool { _, err := os.Stat(request.Output + ".drained"); return err == nil })
	if !lifetimeExited(t, pidfd, 0) {
		t.Fatal("consumer returned with a surviving child")
	}
	if mode == "escalate" && time.Since(start) < 10*time.Second {
		t.Fatal("consumer bypassed the ten-second child grace")
	}
	if release, err := app.AcquireInstanceLock(state); err == nil {
		release()
		t.Fatal("drain return prematurely released the parent's resources")
	}
	var terminal uploadRecoveryState
	lifetimeReadJSON(t, request.Output+".drained", &terminal)
	if len(terminal.Audit) != 1 {
		t.Fatalf("consumer returned before final persistence: %+v", terminal)
	}
	if _, err := io.WriteString(parent.input, "exit\n"); err != nil {
		t.Fatal(err)
	}
	shutdownWaitParent(t, parent)
	after := lifetimeSnapshot(t, root)
	want := map[string]lifetimeFile{"sentinel": before["sentinel"]}
	if mode == "escalate" {
		want = before
	}
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("consumer altered retained state: got=%+v want=%+v", after, want)
	}
	close(hold.release)
	if !reflect.DeepEqual(after, lifetimeSnapshot(t, root)) {
		t.Fatal("held response caused writes after consumer returned")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("shutdown wrote remote data: %v", writes)
	}
	request.JobID = id
	request.Output = filepath.Join(state, "recovered.json")
	restarted := lifetimeStart(t, request)
	shutdownWaitParent(t, restarted)
	var result lifetimeResult
	lifetimeReadJSON(t, request.Output, &result)
	lifetimeAssertRecords(t, result, mode, id, container, root, int64(len(body)))
	if !reflect.DeepEqual(terminal, result.Before) || !reflect.DeepEqual(after, lifetimeSnapshot(t, root)) {
		t.Fatal("recovery changed the terminal records or retained filesystem")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	t.Logf("consumer mode=%s child=%d reaped before drain; recovery interrupted=%d repeated=%d; retained=%+v", mode, pid, result.Interrupted, result.Repeated, after)
}

func shutdownWaitParent(t *testing.T, parent *lifetimeProcess) {
	t.Helper()
	select {
	case err := <-parent.done:
		parent.waited = true
		if err != nil {
			log, readErr := os.ReadFile(parent.log)
			t.Fatalf("owned parent failed: %v log=%s read=%v", err, log, readErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned parent did not finish")
	}
}
