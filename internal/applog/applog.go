// Package applog is the application's levelled log.
//
// A desktop launch has no console, so an operator diagnosing a problem needs
// to raise verbosity and capture the output. This package provides that
// through two environment switches read at startup (ADR 0099), and through
// Settings, which can change the level and keep a log file while the
// application runs (ADR 0127).
//
// It is auth-free by construction: no credential is ever passed to it. The
// authentication path does not log, and the transfer engine is handed only a
// per-session gateway key, never a durable secret (ADR 0003, ADR 0012,
// K-SEC-001). Callers keep that invariant: a token, a password, or a storage
// URL carrying embedded credentials must never reach a log call.
package applog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// EnvLevel names the lowest level that is written: debug, info, warn, or
	// error. An unset or unrecognised value is treated as info, which keeps
	// the lines the application emitted before this package existed.
	EnvLevel = "AERONAUTES_LOG_LEVEL"
	// EnvFile, when set, is a path the log is appended to as well as written
	// to standard error. A desktop launch discards standard error, so this is
	// how an operator captures a session for a support request. Unset means
	// standard error only, so an ordinary launch keeps no on-disk log.
	EnvFile = "AERONAUTES_LOG_FILE"
)

// rotateBytes is the size past which a log file kept from Settings is
// renamed to its ".1" name and started again, so a debug level left on cannot
// fill the disk (ADR 0127 decision 3).
const rotateBytes = 10 << 20

// logger holds the active *slog.Logger. It is swapped atomically so the
// one-time startup Configure cannot race a concurrent log call from a job
// goroutine under the race detector.
var logger atomic.Pointer[slog.Logger]

// current is what Settings can change while the application runs: the level
// every line is judged by and the log file written beside standard error.
// The installed logger reads both through it, so a change applies to the
// next line without building a new logger.
var current struct {
	mu    sync.Mutex
	level slog.LevelVar
	file  io.WriteCloser
	// path is the file in use, or the one Settings would open.
	path string
	// location is where Settings keeps a log file; main sets it once the
	// state folder is known.
	location string
	// levelFromEnv and fileFromEnv say a launch switch set that half.
	levelFromEnv, fileFromEnv bool
}

func init() {
	logger.Store(slog.New(newHandler(os.Stderr, slog.LevelInfo)))
}

func newHandler(w io.Writer, level slog.Leveler) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
}

// switchable writes each record to the log file in force, if any, and then
// to standard error, in that order for the reason bothWriter gives.
type switchable struct{}

func (switchable) Write(p []byte) (int, error) {
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.file == nil {
		return os.Stderr.Write(p)
	}
	return bothWriter{sinks: []io.Writer{current.file, os.Stderr}}.Write(p)
}

// rotating is a log file that starts again under the same name once it
// passes its limit, keeping the previous one as name.1.
type rotating struct {
	path string
	f    *os.File
	size int64
}

func openRotating(path string) (*rotating, error) {
	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotating{path: path, f: f, size: info.Size()}, nil
}

func (r *rotating) Write(p []byte) (int, error) {
	if r.size > 0 && r.size+int64(len(p)) > rotateBytes {
		// A failed rotation keeps writing to the file it has, since losing
		// lines is worse than a file over its size.
		if err := os.Rename(r.path, r.path+".1"); err == nil {
			if f, err := openAppend(r.path); err == nil {
				_ = r.f.Close()
				r.f, r.size = f, 0
			}
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) Close() error { return r.f.Close() }

func openAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// bothWriter writes each record to every sink independently, unlike
// io.MultiWriter, which stops at the first sink that fails. Two failure modes
// motivate it, and the sink order matters:
//
//   - A sink can return an error, for example an ordinary closed file. The
//     other sinks must still be written, which MultiWriter would skip.
//   - Writing to the process's real standard error (fd 2) can raise SIGPIPE
//     once its reader has gone, and Go's default disposition terminates the
//     process inside that Write. The caller never receives an error and never
//     reaches a later sink. So the durable capture file is listed first, which
//     puts the record on disk before the standard-error write that might kill
//     the process.
//
// A write counts as successful when any sink accepted it, so a dead standard
// error does not look like a total failure.
type bothWriter struct{ sinks []io.Writer }

func (b bothWriter) Write(p []byte) (int, error) {
	accepted := false
	var firstErr error
	for _, w := range b.sinks {
		switch n, err := w.Write(p); {
		case err == nil && n == len(p):
			accepted = true
		case err != nil:
			if firstErr == nil {
				firstErr = err
			}
		default:
			if firstErr == nil {
				firstErr = io.ErrShortWrite
			}
		}
	}
	if accepted {
		return len(p), nil
	}
	return 0, firstErr
}

// Configure installs the process logger from the environment switches and
// returns the capture-file path in use (empty when only standard error is
// written) and a closer for any file it opened. Call it once, before other
// work logs. slog writes each record with a single unbuffered Write, so a
// missed close on an abrupt exit loses nothing already written.
func Configure() (path string, closeFn func() error, err error) {
	current.mu.Lock()
	defer current.mu.Unlock()
	levelName := os.Getenv(EnvLevel)
	current.level.Set(parseLevel(levelName))
	current.levelFromEnv = levelName != ""
	if current.file != nil {
		_ = current.file.Close()
		current.file = nil
	}
	current.fileFromEnv = false
	logger.Store(slog.New(newHandler(switchable{}, &current.level)))
	closeFn = func() error { return nil }
	if path = os.Getenv(EnvFile); path != "" {
		f, openErr := openAppend(path)
		if openErr != nil {
			// Standard error at the requested level, so the level still
			// takes effect, and report the failure; the app runs on.
			return "", closeFn, fmt.Errorf("open log file %s: %w", path, openErr)
		}
		current.file, current.path, current.fileFromEnv = f, path, true
		return path, closeFile(f), nil
	}
	current.path = current.location
	return path, closeFn, nil
}

// closeFile closes f and, while it is still the file in force, stops
// writing to it.
func closeFile(f io.WriteCloser) func() error {
	return func() error {
		current.mu.Lock()
		defer current.mu.Unlock()
		if current.file == f {
			current.file = nil
		}
		return f.Close()
	}
}

// SetFileLocation names the log file Settings keeps, in the state folder
// (ADR 0127 decision 3). A file named by the launch switch stays in use.
func SetFileLocation(path string) {
	current.mu.Lock()
	defer current.mu.Unlock()
	current.location = path
	if !current.fileFromEnv {
		current.path = path
	}
}

// Status is the log in force, as Settings reports it.
type Status struct {
	Level        string
	FilePath     string
	FileOn       bool
	LevelFromEnv bool
	FileFromEnv  bool
}

// Current reports the log in force.
func Current() Status {
	current.mu.Lock()
	defer current.mu.Unlock()
	return Status{
		Level: levelName(current.level.Level()), FilePath: current.path, FileOn: current.file != nil,
		LevelFromEnv: current.levelFromEnv, FileFromEnv: current.fileFromEnv,
	}
}

// Start applies the saved settings at launch, except a half a launch switch
// already set, which wins at start (ADR 0127 decision 4).
func Start(level string, keepFile bool) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	if !current.levelFromEnv {
		current.level.Set(parseLevel(level))
	}
	if current.fileFromEnv {
		return nil
	}
	return keepFileLocked(keepFile)
}

// SetLevel puts a level chosen in Settings in force at once, over a launch
// switch too, since it is the operator's latest word (ADR 0127 decision 4).
func SetLevel(level string) {
	current.mu.Lock()
	defer current.mu.Unlock()
	current.level.Set(parseLevel(level))
	current.levelFromEnv = false
}

// KeepFile starts or stops writing the log file at once, as Settings asks.
func KeepFile(keep bool) error {
	current.mu.Lock()
	defer current.mu.Unlock()
	return keepFileLocked(keep)
}

func keepFileLocked(keep bool) error {
	switch {
	case keep && current.file == nil:
		if current.path == "" {
			return fmt.Errorf("no folder is known for the log file")
		}
		var f io.WriteCloser
		var err error
		if current.fileFromEnv {
			// The operator chose that path, so it is not rotated.
			f, err = openAppend(current.path)
		} else {
			f, err = openRotating(current.path)
		}
		if err != nil {
			return fmt.Errorf("open log file %s: %w", current.path, err)
		}
		current.file = f
	case !keep && current.file != nil:
		err := current.file.Close()
		current.file = nil
		return err
	}
	return nil
}

// Redirect sends the log to w alone, at the level the environment names, and
// returns a function that puts the previous logger back. It is for a caller
// whose own output must not carry the log: the real-cloud journeys print
// nothing that can name a provider, and the storage gateway's warnings name
// the host they retried.
func Redirect(w io.Writer) (restore func()) {
	previous := logger.Load()
	logger.Store(slog.New(newHandler(w, parseLevel(os.Getenv(EnvLevel)))))
	return func() { logger.Store(previous) }
}

// levelName is the name parseLevel reads for level.
func levelName(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "debug"
	case level <= slog.LevelInfo:
		return "info"
	case level <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// parseLevel maps a level name to its slog level, defaulting to info.
func parseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logAt formats and writes a message at level, skipping the formatting cost
// when the level is suppressed.
func logAt(level slog.Level, format string, args ...any) {
	l := logger.Load()
	if !l.Enabled(context.Background(), level) {
		return
	}
	l.Log(context.Background(), level, fmt.Sprintf(format, args...))
}

// Debugf writes a formatted message at debug level.
func Debugf(format string, args ...any) { logAt(slog.LevelDebug, format, args...) }

// Infof writes a formatted message at info level.
func Infof(format string, args ...any) { logAt(slog.LevelInfo, format, args...) }

// Warnf writes a formatted message at warn level.
func Warnf(format string, args ...any) { logAt(slog.LevelWarn, format, args...) }

// Errorf writes a formatted message at error level.
func Errorf(format string, args ...any) { logAt(slog.LevelError, format, args...) }
