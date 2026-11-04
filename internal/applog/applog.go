// Package applog is the application's levelled log.
//
// A desktop launch has no console, so an operator diagnosing a problem needs
// to raise verbosity and capture the output. This package provides that
// through two environment switches read once at startup, replacing the
// standard library's level-less log.Print across main and internal/app.
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

// logger holds the active *slog.Logger. It is swapped atomically so the
// one-time startup Configure cannot race a concurrent log call from a job
// goroutine under the race detector.
var logger atomic.Pointer[slog.Logger]

func init() {
	logger.Store(slog.New(newHandler(os.Stderr, slog.LevelInfo)))
}

func newHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
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
	level := parseLevel(os.Getenv(EnvLevel))
	closeFn = func() error { return nil }
	if path = os.Getenv(EnvFile); path != "" {
		f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if openErr != nil {
			// Fall back to standard error at the requested level, so the level
			// still takes effect, and report the failure; the app runs on.
			logger.Store(slog.New(newHandler(os.Stderr, level)))
			return "", closeFn, fmt.Errorf("open log file %s: %w", path, openErr)
		}
		logger.Store(slog.New(newHandler(bothWriter{sinks: []io.Writer{f, os.Stderr}}, level)))
		return path, f.Close, nil
	}
	logger.Store(slog.New(newHandler(os.Stderr, level)))
	return path, closeFn, nil
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
