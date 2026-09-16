package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestShellShutdownJoinsConcurrentCallbacks(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan context.Context, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(unblock)
	var calls, closes, releases atomic.Int64
	shutdown := &shellShutdown{
		drain: func(ctx context.Context) error {
			calls.Add(1)
			entered <- ctx
			<-gate
			return nil
		},
		closeDatabase: func() error { closes.Add(1); return nil },
		releaseLock:   func() { releases.Add(1) },
	}
	done := make(chan error, 8)
	go func() { done <- shutdown.close() }()
	ctx := shellAwait(t, entered)
	if ctx.Done() != nil || ctx.Err() != nil {
		t.Fatal("production drain inherited a cancellable context")
	}
	for range 3 {
		go func() { shutdown.wait(); done <- nil }()
	}
	for range 4 {
		go func() { done <- shutdown.close() }()
	}
	if closes.Load() != 0 || releases.Load() != 0 {
		t.Fatal("held drain permitted resource release")
	}
	select {
	case err := <-done:
		t.Fatalf("held drain returned: %v", err)
	default:
	}
	unblock()
	for range 8 {
		if err := shellAwait(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || closes.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("repeated callbacks: drain=%d close=%d release=%d", calls.Load(), closes.Load(), releases.Load())
	}
}

func TestShellShutdownCloseFailureIsLatched(t *testing.T) {
	failure := errors.New("database close failed")
	var order []string
	shutdown := &shellShutdown{
		drain:         func(context.Context) error { order = append(order, "drain"); return nil },
		closeDatabase: func() error { order = append(order, "close"); return failure },
		releaseLock:   func() { order = append(order, "release") },
	}
	for range 3 {
		if err := shutdown.close(); !errors.Is(err, failure) {
			t.Fatalf("lost close failure: %v", err)
		}
	}
	if !reflect.DeepEqual(order, []string{"drain", "close"}) {
		t.Fatalf("failed database close order: %v", order)
	}
}

func TestDesktopCallerUsesActualServices(t *testing.T) {
	for _, runtimeError := range []bool{false, true} {
		for _, callback := range []bool{false, true} {
			t.Run(fmt.Sprintf("run_error_%t/callback_%t", runtimeError, callback), func(t *testing.T) {
				t.Setenv("AERONAUTES_STATE_DIR", t.TempDir())
				runFailure := errors.New("runtime run failure")
				closeFailure := errors.New("database close failure")
				forceCloseFailure := runtimeError && callback
				shutdown := &shellShutdown{}
				defer func() {
					if forceCloseFailure && shutdown.releaseLock != nil {
						shutdown.releaseLock()
					}
				}()
				var find *app.FindService
				var deleteService *app.DeleteService
				launch := func(options application.Options) error {
					if len(options.Services) != 12 || options.OnShutdown == nil {
						t.Fatal("actual desktop service/callback assembly is incomplete")
					}
					for _, bound := range options.Services {
						switch service := bound.Instance().(type) {
						case *app.FindService:
							find = service
						case *app.DeleteService:
							deleteService = service
						}
					}
					if find == nil || deleteService == nil {
						t.Fatal("actual desktop omitted job or Find services")
					}
					if forceCloseFailure {
						closeDatabase := shutdown.closeDatabase
						shutdown.closeDatabase = func() error {
							return errors.Join(closeDatabase(), closeFailure)
						}
					}
					if callback {
						options.OnShutdown()
						options.OnShutdown()
						assertDesktopRefusesStarts(t, find, deleteService)
					}
					if runtimeError {
						return runFailure
					}
					return nil
				}
				err := runDesktop(buildIdentity(), shutdown, launch, shellSessionSecrets)
				if errors.Is(err, runFailure) != runtimeError || errors.Is(err, closeFailure) != forceCloseFailure {
					t.Fatalf("desktop errors: %v", err)
				}
				assertDesktopRefusesStarts(t, find, deleteService)
				if !forceCloseFailure {
					release, err := app.AcquireInstanceLock(shellStateDir(t))
					if err != nil {
						t.Fatalf("successful close retained instance lock: %v", err)
					}
					release()
				}
			})
		}
	}
}

func TestDesktopStartupFailureCleansAcquiredLock(t *testing.T) {
	for _, failure := range []string{"lock_held", "database_open", "missing_directory"} {
		t.Run(failure, func(t *testing.T) {
			state := t.TempDir()
			if failure == "missing_directory" {
				state = filepath.Join(state, "missing")
			}
			t.Setenv("AERONAUTES_STATE_DIR", state)
			if failure == "lock_held" {
				release, err := app.AcquireInstanceLock(state)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			if failure == "database_open" {
				if err := os.Mkdir(filepath.Join(state, "state.sqlite"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			shutdown := &shellShutdown{}
			err := runDesktop(buildIdentity(), shutdown, func(application.Options) error {
				t.Error("startup failure reached the native runtime")
				return nil
			}, func() (secrets.SecretStore, secrets.Backend) {
				t.Error("startup failure opened the secret backend")
				return shellSessionSecrets()
			})
			if err == nil {
				t.Fatal("startup failure was lost")
			}
			if failure == "database_open" {
				if !strings.Contains(err.Error(), "open state database") || shutdown.releaseLock == nil {
					t.Fatalf("failure did not occur after lock acquisition: %v", err)
				}
				release, err := app.AcquireInstanceLock(state)
				if err != nil {
					t.Fatalf("failed database startup retained lock: %v", err)
				}
				release()
			} else if !strings.Contains(err.Error(), "lock state directory") || shutdown.releaseLock != nil {
				t.Fatalf("failed acquisition changed lock ownership: %v", err)
			}
		})
	}
}

func assertDesktopRefusesStarts(t *testing.T, find *app.FindService, deletion *app.DeleteService) {
	t.Helper()
	if id, err := find.StartFind(t.Context(), "fixture-bookmark", "fixture", "", "match"); id != "" || err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("actual Find remained open: id=%q err=%v", id, err)
	}
	if id, err := deletion.StartDelete(t.Context(), app.DeleteRequestDTO{
		BookmarkID: "fixture", Container: "fixture", Prefix: "scope/", Mode: app.ModePrefix, Confirm: "fixture",
	}); id != "" || err == nil || !strings.Contains(err.Error(), "shutdown") {
		t.Fatalf("actual job admission remained open: id=%q err=%v", id, err)
	}
}

func shellSessionSecrets() (secrets.SecretStore, secrets.Backend) {
	return secrets.NewSessionStore(), secrets.BackendSession
}

func shellStateDir(t *testing.T) string {
	t.Helper()
	return os.Getenv("AERONAUTES_STATE_DIR")
}

func shellAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("shell test boundary did not finish")
		var zero T
		return zero
	}
}
