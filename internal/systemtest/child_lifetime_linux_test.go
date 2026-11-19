//go:build linux

package systemtest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"golang.org/x/sys/unix"
)

type lifetimeRequest struct {
	Bookmark                                      app.BookmarkDTO
	Key, StateDir, Root, Container, Output, JobID string
}

type lifetimeResult struct {
	Before, After         uploadRecoveryState
	Interrupted, Repeated int64
}

func lifetimeWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

func lifetimeReadJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

func TestLifetimeParentHelper(t *testing.T) {
	input := os.Getenv("AERONAUTES_LIFETIME_INPUT")
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
	if request.JobID != "" {
		result := lifetimeResult{Before: uploadRecoveryRead(t, database, request.JobID)}
		result.Interrupted, err = store.FailInterrupted(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result.Repeated, err = store.FailInterrupted(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result.After = uploadRecoveryRead(t, database, request.JobID)
		lifetimeWriteJSON(t, request.Output, result)
		return
	}
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
	runner := app.NewJobRunner(store, audit.NewStore(database))
	transfer := app.NewTransferServiceWithRunner(connections, store, runner)
	h := &Harness{T: t, ctx: t.Context(), Transfer: transfer, Jobs: app.NewJobService(store), Primary: &Account{BookmarkID: saved.ID}}
	id := downloadCancelStart(t, h, request.Container, "scope/", request.Root)
	lifetimeWriteJSON(t, request.Output, id)
	commands := bufio.NewReader(os.Stdin)
	command, err := commands.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if command == "cancel\n" {
		if err := transfer.CancelJob(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	} else if command != "complete\n" {
		t.Fatalf("unknown command %q", command)
	}
	finished := h.AwaitJob(id)
	var terminal uploadRecoveryState
	// The audit append follows the terminal row; closing the helper's
	// database at the row alone could race that independent write.
	lifetimeAwait(t, "terminal audit", func() bool {
		terminal = uploadRecoveryRead(t, database, id)
		return len(terminal.Audit) == 1 && terminal.Audit[0].Operation == "transfer.local."+finished.State && terminal.Audit[0].Resource == "job "+id
	})
	lifetimeWriteJSON(t, request.Output+".terminal", terminal)
	command, err = commands.ReadString('\n')
	if err != nil || command != "exit\n" {
		t.Fatalf("terminal parent release: %q %v", command, err)
	}
}

type lifetimeProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	done   chan error
	log    string
	waited bool
}

func lifetimeStart(t *testing.T, request lifetimeRequest) *lifetimeProcess {
	t.Helper()
	input := request.Output + ".input"
	lifetimeWriteJSON(t, input, request)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestLifetimeParentHelper$", "-test.timeout=45s")
	cmd.Env = append(os.Environ(), "AERONAUTES_LIFETIME_INPUT="+input)
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

func lifetimeStopParent(t *testing.T, p *lifetimeProcess) {
	t.Helper()
	if p.waited {
		return
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Error(err)
	}
	select {
	case <-p.done:
		p.waited = true
	case <-time.After(5 * time.Second):
		t.Error("owned parent was not reaped during cleanup")
	}
}

// lifetimeWait bounds every wait for the parent or its child to reach a
// state: the helper's start, the harness's held GET body, the child's
// first staged bytes, the terminal records and the stopped state. Each
// exists to fail a stuck fixture, not to measure anything. On a healthy
// hosted runner the five modes take between four and eight seconds each,
// escalate about sixteen with its ten-second grace, and the stage-bytes
// wait alone once exceeded the former fifteen-second bound on a loaded
// runner (PR 157 attempt 1, 22 September 2026, a docs-only change) while
// the identical code had passed on master minutes earlier; that wait
// depends on when the engine child flushes its first 64 KiB, which the
// test does not control. Sixty seconds still fails a stuck fixture far
// inside the package's twenty-five minute budget.
const lifetimeWait = 60 * time.Second

// lifetimeAwaitReporting is lifetimeAwait with a witness: on timeout it reports
// what the condition last observed, so a wait that runs out on a hosted runner is
// diagnosable from its own failure rather than only reproducible.
func lifetimeAwaitReporting(t *testing.T, what string, witness func() []string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(lifetimeWait)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s; last seen: %s", what, strings.Join(witness(), "; "))
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func lifetimeAwait(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(lifetimeWait)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func lifetimeChild(t *testing.T, parent int) int {
	t.Helper()
	paths, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", parent))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(word)
			if err != nil {
				t.Fatal(err)
			}
			exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(exe) == "rclone" {
				return pid
			}
		}
	}
	t.Fatal("held stage writer has no rclone direct child")
	return 0
}

type lifetimeFile struct {
	Size int64
	Hash string
	Mode fs.FileMode
}

// lifetimeTail is the last of a helper's own log, for a failure that needs to
// say what the other process was doing rather than only what the filesystem
// held.
func lifetimeTail(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	if len(data) == 0 {
		return "empty"
	}
	return strings.ReplaceAll(string(data), "\n", " | ")
}

// lifetimeRetained compares a terminal snapshot with the one taken before the
// shutdown, for the modes whose contract is that what was written is kept.
//
// Everything is required to be byte-identical except the partial itself, which
// may have GROWN. The barrier releases a fixed number of bytes and then holds
// the body open, and between the earlier snapshot and the moment the child
// actually stops, bytes already released can still reach the disk. A plain
// equality on the whole snapshot therefore fails on a schedule that is
// perfectly correct, which is a flake rather than a finding — a review named this
// interleaving from the source, and the focused cases beside this function
// exercise it.
//
// What the partial still has to satisfy: it never shrinks, it never exceeds what
// the barrier released, it stays a true prefix of the object, and its mode does
// not change.
func lifetimeRetained(before, after map[string]lifetimeFile, partialKey string, body []byte, released int64) error {
	if len(before) != len(after) {
		return fmt.Errorf("entry count changed: %d then %d", len(before), len(after))
	}
	for name, was := range before {
		now, ok := after[name]
		switch {
		case !ok:
			return fmt.Errorf("%s is gone", name)
		case name != partialKey:
			if was != now {
				return fmt.Errorf("%s changed: %+v then %+v", name, was, now)
			}
		case now.Mode != was.Mode:
			return fmt.Errorf("%s changed mode: %s then %s", name, was.Mode, now.Mode)
		case now.Size < was.Size:
			return fmt.Errorf("%s shrank: %d then %d bytes", name, was.Size, now.Size)
		case now.Size > released:
			return fmt.Errorf("%s is %d bytes, more than the %d the barrier released", name, now.Size, released)
		case now.Hash != fmt.Sprintf("%x", sha256.Sum256(body[:now.Size])):
			return fmt.Errorf("%s of %d bytes is not the object's first %d", name, now.Size, now.Size)
		}
	}
	return nil
}

func lifetimeSnapshot(t *testing.T, root string) map[string]lifetimeFile {
	t.Helper()
	result := map[string]lifetimeFile{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file := lifetimeFile{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Size, file.Hash = int64(len(body)), fmt.Sprintf("%x", sha256.Sum256(body))
		}
		result[rel] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestChildLifetimeMock(t *testing.T) {
	t.Parallel()
	childLifetimeCases(t, findMockHarness)
}

func TestChildLifetimeRealSwift(t *testing.T) {
	t.Parallel()
	childLifetimeCases(t, New)
}

func childLifetimeCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	controller := os.Getenv("AERONAUTES_LIFETIME_CONTROLLER") == "1"
	if controller {
		if os.Getenv("AERONAUTES_LIFETIME_OUTCOME") == "" {
			t.Fatal("isolated controller needs an outcome path")
		}
		// A separate controller keeps subreaping out of the suite process
		// and lets this test distinguish a killed orphan from gateway EOF.
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"complete", "cancel", "escalate", "parent_kill", "stopped_child_parent_kill"} {
		t.Run(mode, func(t *testing.T) {
			if !controller {
				self, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
				defer cancel()
				outcomePath := filepath.Join(t.TempDir(), "outcome.json")
				cmd := exec.CommandContext(ctx, self, "-test.run=^"+strings.ReplaceAll(t.Name(), "/", "$/^")+"$", "-test.v", "-test.timeout=75s")
				cmd.Env = append(os.Environ(), "AERONAUTES_LIFETIME_CONTROLLER=1", "AERONAUTES_LIFETIME_OUTCOME="+outcomePath)
				output, err := cmd.CombinedOutput()
				t.Logf("isolated controller: %s", output)
				if err != nil {
					t.Fatalf("controller failed: %v", err)
				}
				var outcome string
				lifetimeReadJSON(t, outcomePath, &outcome)
				switch outcome {
				case "pass":
				case "skip":
					t.Skip("isolated controller skipped this scenario; see its output")
				default:
					t.Fatalf("unexpected controller outcome %q", outcome)
				}
				return
			}
			// A skipped Go test also exits zero; an explicit outcome prevents
			// an unavailable fixture from being credited as a passing scenario.
			t.Cleanup(func() {
				outcome := "pass"
				if t.Failed() {
					outcome = "fail"
				} else if t.Skipped() {
					outcome = "skip"
				}
				lifetimeWriteJSON(t, os.Getenv("AERONAUTES_LIFETIME_OUTCOME"), outcome)
			})
			childLifetime(t, makeHarness(t), mode)
		})
	}
}

func childLifetime(t *testing.T, h *Harness, mode string) {
	container := h.Container(h.Primary, "lifetime")
	body := bytes.Repeat([]byte("write barrier data."), 16384)
	h.Seed(h.Primary, container, "scope/file.bin", body, nil)
	h.Seed(h.Secondary, h.Container(h.Secondary, "collateral"), "untouched", []byte("other account"), nil)
	beforeA, beforeB := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
	hold, trace := downloadCancelBodyProxy(t, h, container, "scope/file.bin", 64*1024)
	bookmarkList, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var bookmark app.BookmarkDTO
	for _, candidate := range bookmarkList {
		if candidate.ID == h.Primary.BookmarkID {
			bookmark = candidate
		}
	}
	bookmark.ID = ""
	root, state := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	request := lifetimeRequest{Bookmark: bookmark, Key: h.Primary.key, StateDir: state, Root: root, Container: container, Output: filepath.Join(state, "started.json")}
	parent := lifetimeStart(t, request)
	lifetimeAwait(t, "parent start", func() bool { _, err := os.Stat(request.Output); return err == nil })
	var id string
	lifetimeReadJSON(t, request.Output, &id)
	select {
	case <-hold.reached:
	case <-time.After(lifetimeWait):
		t.Fatal("GET body barrier not reached")
	}
	// The engine writes the file in the chosen folder under its own partial
	// name until it has it whole (ADR 0122 decision 5).
	var writing string
	// A timeout here says only "partial bytes"; the sizes last seen say
	// whether the child never wrote, wrote slowly or wrote something else,
	// so log them when the test fails.
	observed, waited := map[string]int{}, time.Now()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("partial bytes: waited %s, last observed %v", time.Since(waited).Round(time.Millisecond), observed)
		}
	})
	lifetimeAwait(t, "partial bytes", func() bool {
		paths, err := filepath.Glob(filepath.Join(root, "*.partial"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			observed[filepath.Base(path)] = len(data)
			// A non-empty prefix, for the reason the sibling shutdown test
			// records: the barrier holds the body open after releasing 64 KiB,
			// so how much of it has been flushed at this instant is not this
			// test's to control.
			if len(data) > 0 && len(data) <= 64*1024 && bytes.Equal(data, body[:len(data)]) {
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
			t.Error("owned child survived cleanup")
		}
		lifetimeStopParent(t, parent)
		if _, err := lifetimeReapChild(pid); err != nil && !errors.Is(err, unix.ECHILD) {
			t.Errorf("owned adopted child was not reaped: %v", err)
		}
		if err := unix.Close(pidfd); err != nil {
			t.Error(err)
		}
	})
	lifetimeCheckChild(t, pid, parent.cmd.Process.Pid, pidfd)
	if unlock, err := app.AcquireInstanceLock(state); err == nil {
		unlock()
		t.Fatal("active parent did not exclude second instance")
	}
	if released := hold.released(); released != 64*1024 {
		t.Fatalf("the body barrier released %d bytes, want exactly %d", released, 64*1024)
	}
	before := lifetimeSnapshot(t, root)
	partial, ok := before[strings.TrimPrefix(writing, root+string(os.PathSeparator))]
	switch {
	case !ok || len(before) != 2:
		t.Fatalf("barrier did not capture one partial and the sentinel: %+v", before)
	case partial.Size <= 0 || partial.Size > 64*1024:
		t.Fatalf("retained partial is %d bytes, want between 1 and the %d released: %+v", partial.Size, 64*1024, before)
	case partial.Hash != fmt.Sprintf("%x", sha256.Sum256(body[:partial.Size])):
		t.Fatalf("retained partial of %d bytes is not the object's first %d: %+v", partial.Size, partial.Size, before)
	}
	t.Logf("barrier mode=%s parent=%d child=%d writing=%s files=%+v", mode, parent.cmd.Process.Pid, pid, filepath.Base(writing), before)
	start := time.Now()
	switch mode {
	case "parent_kill", "stopped_child_parent_kill":
		if mode == "stopped_child_parent_kill" {
			if err := unix.PidfdSendSignal(pidfd, unix.SIGSTOP, nil, 0); err != nil {
				t.Fatal(err)
			}
			lifetimeAwaitStopped(t, pid)
		}
		if err := parent.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	case "escalate":
		if err := unix.PidfdSendSignal(pidfd, unix.SIGSTOP, nil, 0); err != nil {
			t.Fatal(err)
		}
		lifetimeAwaitStopped(t, pid)
		if _, err := io.WriteString(parent.input, "cancel\n"); err != nil {
			t.Fatal(err)
		}
	case "cancel":
		if _, err := io.WriteString(parent.input, "cancel\n"); err != nil {
			t.Fatal(err)
		}
	case "complete":
		close(hold.release)
		if _, err := io.WriteString(parent.input, "complete\n"); err != nil {
			t.Fatal(err)
		}
	}
	if mode != "parent_kill" && mode != "stopped_child_parent_kill" {
		lifetimeAwait(t, "job terminal while parent remains alive", func() bool { _, err := os.Stat(request.Output + ".terminal"); return err == nil })
		if !lifetimeExited(t, pidfd, 0) {
			t.Fatal("terminal job left its child alive")
		}
		if mode == "escalate" && time.Since(start) < 10*time.Second {
			t.Fatal("forced cancellation bypassed the cleanup grace")
		}
		if _, err := io.WriteString(parent.input, "exit\n"); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-parent.done:
		parent.waited = true
		if mode == "parent_kill" || mode == "stopped_child_parent_kill" {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("parent kill result: %v", err)
			}
		} else if err != nil {
			data, readErr := os.ReadFile(parent.log)
			t.Fatalf("parent failed: %v log=%s read=%v", err, data, readErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("parent did not finish")
	}
	if !lifetimeExited(t, pidfd, 2*time.Second) {
		t.Fatal("child still alive after parent terminal")
	}
	if mode == "parent_kill" || mode == "stopped_child_parent_kill" {
		status, err := lifetimeReapChild(pid)
		t.Logf("adopted child wait: pid=%d status=%v signal=%v error=%v", pid, status, status.Signal(), err)
		// Which signal reaps a RUNNING child is not decidable here. A child
		// inside a write to its stderr pipe when the parent dies can be
		// reaped by SIGPIPE before the parent-death signal: observed in 13
		// of 400 probe trials WITH the coupling configured, under a child
		// saturating that pipe. That rate describes the probe and not this
		// suite, but it is enough to show SIGPIPE does not indicate the
		// coupling is missing, so requiring SIGKILL here fails
		// intermittently on a healthy tree. Both signals follow from the
		// parent dying; the list stays closed so an unexpected crash or a
		// normal exit still fails.
		//
		// DO NOT DELETE stopped_child_parent_kill AS DUPLICATE COVERAGE.
		// A stopped child cannot execute a write, so it cannot take
		// SIGPIPE, and only the parent-death signal can reap it: 200 of 200
		// probe trials. It is the ONLY mode that still detects a missing
		// Pdeathsig: with the coupling removed this running mode PASSES,
		// and the stopped mode fails the liveness assertion above before
		// cleanup kills the child. Deleting that mode, or relaxing this
		// requirement for it, removes the proof rather than a duplicate.
		wantExact := mode == "stopped_child_parent_kill"
		reaped := err == nil && status.Signaled() &&
			(status.Signal() == unix.SIGKILL || (!wantExact && status.Signal() == unix.SIGPIPE))
		if !reaped {
			want := "SIGKILL"
			if !wantExact {
				want = "SIGKILL or SIGPIPE"
			}
			t.Fatalf("child did not terminate from parent death by %s: pid=%d status=%v error=%v", want, pid, status, err)
		}
	}
	after := lifetimeSnapshot(t, root)
	switch mode {
	case "escalate", "parent_kill", "stopped_child_parent_kill":
		// Retention, with the partial allowed to have grown: see
		// lifetimeRetained for why equality on the whole snapshot flakes.
		if err := lifetimeRetained(before, after, strings.TrimPrefix(writing, root+string(os.PathSeparator)),
			body, hold.released()); err != nil {
			t.Fatalf("unexpected terminal filesystem: %v; got=%+v before=%+v", err, after, before)
		}
	default:
		wantFiles := map[string]lifetimeFile{"sentinel": before["sentinel"]}
		if mode == "complete" {
			wantFiles["file.bin"] = lifetimeFile{Mode: partial.Mode, Size: int64(len(body)), Hash: fmt.Sprintf("%x", sha256.Sum256(body))}
		}
		if !reflect.DeepEqual(after, wantFiles) {
			t.Fatalf("unexpected terminal filesystem: got=%+v want=%+v", after, wantFiles)
		}
	}
	select {
	case <-hold.release:
	default:
		close(hold.release)
	}
	if !reflect.DeepEqual(after, lifetimeSnapshot(t, root)) {
		t.Fatal("the folder changed after releasing the held body")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	if writes := trace.writeSnapshot(); len(writes) != 0 {
		t.Fatalf("download wrote remote data: %v", writes)
	}
	request.JobID = id
	request.Output = filepath.Join(state, "reopened.json")
	restarted := lifetimeStart(t, request)
	select {
	case err := <-restarted.done:
		restarted.waited = true
		if err != nil {
			data, readErr := os.ReadFile(restarted.log)
			t.Fatalf("restart failed %v log=%s read=%v", err, data, readErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("restart did not finish")
	}
	var result lifetimeResult
	lifetimeReadJSON(t, request.Output, &result)
	lifetimeAssertRecords(t, result, mode, id, container, root, int64(len(body)))
	if !reflect.DeepEqual(after, lifetimeSnapshot(t, root)) {
		t.Fatal("recovery changed retained filesystem state")
	}
	h.AssertNoCollateral(beforeA, h.SnapshotNamespace(h.Primary))
	h.AssertNoCollateral(beforeB, h.SnapshotNamespace(h.Secondary))
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("terminal mode=%s elapsed=%s files=%+v result=%s", mode, time.Since(start), after, encoded)
}

func lifetimeReapChild(pid int) (unix.WaitStatus, error) {
	for {
		var status unix.WaitStatus
		got, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return status, err
		}
		if got != pid {
			return status, fmt.Errorf("child %d is not waitable", pid)
		}
		return status, nil
	}
}

func lifetimeExited(t *testing.T, fd int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := max(0, int((time.Until(deadline)+time.Millisecond-1)/time.Millisecond))
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, remaining)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return n == 1 && fds[0].Revents&unix.POLLIN != 0
	}
}

func lifetimeAwaitStopped(t *testing.T, pid int) {
	t.Helper()
	lifetimeAwait(t, "owned child SIGSTOP", func() bool {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(data)[strings.LastIndex(string(data), ")")+1:])
		return len(fields) > 0 && fields[0] == "T"
	})
}

func lifetimeAssertRecords(t *testing.T, result lifetimeResult, mode, id, container, root string, size int64) {
	t.Helper()
	killed := mode == "parent_kill" || mode == "stopped_child_parent_kill"
	wantState := "cancelled"
	// The file a cancellation stopped is the incomplete part of the failed
	// names (ADR 0129).
	wantCounters := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: size, ObjectsFailed: 1, ObjectsIncomplete: 1}
	wantTotals := jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Failed: 1, Unresolved: 1, Problems: 1, Incomplete: 1}
	if mode == "complete" {
		wantState = "complete"
		wantCounters.ObjectsCopied = 1
		wantCounters.ObjectsVerified = 1
		wantCounters.BytesCopied = size
		wantCounters.ObjectsFailed = 0
		wantCounters.ObjectsIncomplete = 0
		wantTotals = jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1}
	}
	if killed {
		wantState = "failed"
		wantCounters.ObjectsFailed = 0
		wantCounters.ObjectsIncomplete = 0
		wantTotals = jobs.ItemTotals{Items: 1, Planned: 1, Admitted: 1, Unresolved: 1}
	}
	after := result.After
	if after.Job == nil || after.Job.State != wantState || after.Job.ID != id || after.Job.Counters != wantCounters || after.Job.FinishedAt == "" || !after.ItemsCaptured || len(after.Items) != 1 || after.ItemTotals != wantTotals {
		t.Fatalf("terminal records differ from observed effects: %+v want state=%s counters=%+v totals=%+v", after, wantState, wantCounters, wantTotals)
	}
	item := after.Items[0]
	if item.Key != "scope/file.bin" || item.Source.Kind != "swift" || item.Source.Container != container || item.Source.Object != "scope/file.bin" || item.Source.BookmarkID != after.Job.SourceBookmarkID || item.Destination.LocalPath != filepath.Join(root, "file.bin") || !item.Admitted || !item.Planned || item.ListedBytes != size {
		t.Fatalf("retained item identity changed: %+v", item)
	}
	if killed {
		if result.Before.Job == nil || result.Before.Job.State != "running" || result.Interrupted != 1 || result.Repeated != 0 || !reflect.DeepEqual(result.Before.Items, after.Items) || !reflect.DeepEqual(result.Before.ItemTotals, after.ItemTotals) || !reflect.DeepEqual(result.Before.Audit, after.Audit) || item.Execution != nil || item.Verification != nil || !strings.Contains(after.Job.Error, "interrupted by shutdown; data and recovery records retained") {
			t.Fatalf("restart invented or removed interruption facts: %+v", result)
		}
	} else {
		if len(after.Audit) != 1 || after.Audit[0].Operation != "transfer.local."+wantState || after.Audit[0].Resource != "job "+id {
			t.Fatalf("terminal audit does not match the job: %+v", after.Audit)
		}
		if result.Interrupted != 0 || result.Repeated != 0 || !reflect.DeepEqual(result.Before, after) {
			t.Fatal("orderly restart changed terminal facts")
		}
		if mode == "complete" {
			if item.Execution == nil || item.Execution.Result != jobs.ItemCopied || item.Execution.CopiedBytes != size || item.Execution.Effect != jobs.EffectReportedApplied || item.Verification == nil || item.Verification.Result != jobs.ItemVerified || after.Job.Error != "" {
				t.Fatalf("completed item has wrong observations: %+v", item)
			}
		} else {
			if item.Execution == nil || item.Execution.Result != jobs.ItemFailed || item.Execution.Effect != jobs.EffectUnresolved || item.Execution.Diagnostic.Stage != jobs.StageEngine || item.Execution.Diagnostic.Code != jobs.CodeCancelled || item.Execution.CopiedBytes != 0 || item.Verification != nil || !strings.Contains(after.Job.Error, "context canceled") {
				t.Fatalf("cancelled item has wrong observations: %+v error=%s", item, after.Job.Error)
			}
			// A killed engine leaves its partial file, which the job says may
			// be there (ADR 0122 decision 5).
			if mode == "escalate" && !strings.Contains(after.Job.Error, "a name ending .partial") {
				t.Fatalf("forced cancellation did not say a partial file may remain: %q", after.Job.Error)
			}
		}
	}
	if len(after.Rows) != 0 || after.Status.Jobs != 0 || after.Status.Records != 0 {
		t.Fatalf("download invented remote recovery: %+v", after)
	}
}

func lifetimeCheckChild(t *testing.T, pid, parent, fd int) {
	t.Helper()
	binary, err := exec.LookPath("rclone")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(want, actual) {
		t.Fatal("observed child is not the selected pinned engine")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data)[strings.LastIndex(string(data), ")")+1:])
	if len(fields) < 3 || fields[1] != strconv.Itoa(parent) || fields[2] != strconv.Itoa(pid) || lifetimeExited(t, fd, 0) {
		t.Fatal("observed child identity changed before its barrier signal")
	}
}
