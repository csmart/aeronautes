package applog

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseLevelMapsNamesAndDefaultsToInfo(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" debug ": slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"chatty":  slog.LevelInfo,
	}
	for name, want := range cases {
		if got := parseLevel(name); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", name, got, want)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink closed") }

func TestBothWriterWritesHealthySinkWhenAnotherFails(t *testing.T) {
	// A failing standard error must not suppress the capture file, in either
	// order; that is the regression io.MultiWriter would reintroduce.
	for _, order := range []string{"fail-first", "healthy-first"} {
		var buf bytes.Buffer
		var w bothWriter
		if order == "fail-first" {
			w = bothWriter{sinks: []io.Writer{failingWriter{}, &buf}}
		} else {
			w = bothWriter{sinks: []io.Writer{&buf, failingWriter{}}}
		}
		n, err := w.Write([]byte("hello"))
		if err != nil {
			t.Errorf("%s: Write errored with a healthy sink present: %v", order, err)
		}
		if n != len("hello") {
			t.Errorf("%s: Write n = %d, want %d", order, n, len("hello"))
		}
		if buf.String() != "hello" {
			t.Errorf("%s: healthy sink captured %q, want %q", order, buf.String(), "hello")
		}
	}

	// When every sink fails the write reports the failure rather than claiming
	// success.
	all := bothWriter{sinks: []io.Writer{failingWriter{}, failingWriter{}}}
	if _, err := all.Write([]byte("x")); err == nil {
		t.Error("Write reported success when all sinks failed")
	}
}

// TestCaptureSurvivesBrokenStderr runs a child whose standard error is a pipe
// with no reader, so writing to the real fd 2 raises SIGPIPE and the runtime
// can terminate the child inside that write. Because the capture file is the
// first sink, the record must already be on disk. Only a subprocess exercises
// the real fd 2; an in-process fake cannot reproduce the signal.
func TestCaptureSurvivesBrokenStderr(t *testing.T) {
	if os.Getenv("APPLOG_BROKEN_STDERR_CHILD") == "1" {
		_, closeFn, err := Configure()
		if err != nil {
			os.Exit(2)
		}
		Errorf("captured-marker")
		_ = closeFn()
		os.Exit(0)
	}

	capture := filepath.Join(t.TempDir(), "capture.log")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCaptureSurvivesBrokenStderr$")
	cmd.Env = append(os.Environ(),
		"APPLOG_BROKEN_STDERR_CHILD=1",
		EnvLevel+"=info",
		EnvFile+"="+capture,
	)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	// Close the read end before the child starts, so its first write to fd 2
	// meets EPIPE with no possible reader.
	if err := r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	_ = w.Close() // the child keeps its own dup
	// The child may exit via SIGPIPE; that is the condition under test, so its
	// exit status is deliberately not asserted.
	_ = cmd.Wait()

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if !strings.Contains(string(data), "captured-marker") {
		t.Errorf("capture file missing the record after a broken standard error: %q", data)
	}
}

// TestConfigureKeepsLevelWhenFileFails runs a child whose capture path cannot
// be opened (a directory), and confirms the requested level still takes effect
// on standard error rather than falling back to the default. A subprocess
// keeps the global logger and os.Stderr out of the parent's other tests.
func TestConfigureKeepsLevelWhenFileFails(t *testing.T) {
	if os.Getenv("APPLOG_BADFILE_CHILD") == "1" {
		if _, _, err := Configure(); err == nil {
			os.Exit(3) // the path opened after all; not the case under test
		}
		Infof("info-marker")
		Errorf("error-marker")
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigureKeepsLevelWhenFileFails$")
	cmd.Env = append(os.Environ(),
		"APPLOG_BADFILE_CHILD=1",
		EnvLevel+"=error",
		EnvFile+"="+t.TempDir(), // a directory cannot be opened as a log file
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child exited non-zero (%v):\n%s", err, out)
	}
	if bytes.Contains(out, []byte("info-marker")) {
		t.Errorf("info written at error level after the capture file failed:\n%s", out)
	}
	if !bytes.Contains(out, []byte("error-marker")) {
		t.Errorf("error not written after the capture file failed:\n%s", out)
	}
}

// configureToTemp points the log at a fresh temp file at the given level and
// returns the file path, so a test can read back exactly what was written.
func configureToTemp(t *testing.T, level string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.txt")
	t.Setenv(EnvLevel, level)
	t.Setenv(EnvFile, path)
	_, closeFn, err := Configure()
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log %s: %v", path, err)
	}
	return string(b)
}

func TestConfigureWritesAtOrAboveLevel(t *testing.T) {
	path := configureToTemp(t, "info")
	Debugf("dropped-debug-%d", 1)
	Infof("kept-info")
	Warnf("kept-warn")
	Errorf("kept-error")

	out := readFile(t, path)
	if strings.Contains(out, "dropped-debug") {
		t.Errorf("debug line written at info level:\n%s", out)
	}
	for _, want := range []string{"kept-info", "kept-warn", "kept-error"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in log:\n%s", want, out)
		}
	}
	// The level is recorded on the line, which is the point of the package.
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("levels not recorded on log lines:\n%s", out)
	}
}

func TestConfigureAtDebugKeepsDebug(t *testing.T) {
	path := configureToTemp(t, "debug")
	Debugf("kept-debug-%s", "x")
	out := readFile(t, path)
	if !strings.Contains(out, "kept-debug-x") {
		t.Errorf("debug line missing at debug level:\n%s", out)
	}
}

func TestConfigureWithoutFileReportsNoPath(t *testing.T) {
	t.Setenv(EnvLevel, "warn")
	t.Setenv(EnvFile, "")
	path, closeFn, err := Configure()
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	if path != "" {
		t.Errorf("path = %q, want empty when no file is configured", path)
	}
	// The no-op closer is safe to call.
	if err := closeFn(); err != nil {
		t.Errorf("closeFn: %v", err)
	}
}

func TestConfigureReportsAnUnusableFile(t *testing.T) {
	// A directory path cannot be opened as a log file; Configure must report
	// the error rather than silently dropping the log.
	t.Setenv(EnvLevel, "info")
	t.Setenv(EnvFile, t.TempDir())
	if _, _, err := Configure(); err == nil {
		t.Error("Configure accepted a directory as the log file")
	}
}

// TestConfigureIsSafeDuringConcurrentLogging swaps the active logger while job
// goroutines are logging; the atomic pointer must make that race-free, which
// the race detector checks in CI.
func TestConfigureIsSafeDuringConcurrentLogging(t *testing.T) {
	path := configureToTemp(t, "debug")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				Infof("line-%d-%d", i, j)
			}
		}(i)
	}
	_, closeFn, err := Configure()
	if err != nil {
		t.Fatalf("Configure during logging: %v", err)
	}
	wg.Wait()
	if err := closeFn(); err != nil {
		t.Errorf("close second capture: %v", err)
	}
	if readFile(t, path) == "" {
		t.Error("no lines were written during concurrent logging")
	}
}
