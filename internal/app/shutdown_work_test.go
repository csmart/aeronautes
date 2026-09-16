package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownWorkKeepsBothAdmissionsClosed(t *testing.T) {
	for _, releaseJobFirst := range []bool{false, true} {
		name := "find_exits_first"
		if releaseJobFirst {
			name = "job_exits_first"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			gate := gateFindRequests(t, h, true)
			find := NewFindService(h.transfer.connections)
			var ids []string
			defer cleanupFind(t, find, &ids, gate)
			id, err := find.StartFind(t.Context(), findBM(find), "media", "", "report")
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
			findContext := awaitConnection(t, gate.entered)
			runner := h.transfer.runner
			owner, startContext, err := runner.admit(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.finishStart()
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			assertPending := func() {
				t.Helper()
				if err := ShutdownWork(cancelled, runner, find, h.mutate); !errors.Is(err, context.Canceled) {
					t.Fatalf("incomplete work drain = %v", err)
				}
				if err := h.db.Reader().PingContext(t.Context()); err != nil {
					t.Fatalf("owned database closed before drain: %v", err)
				}
			}
			assertPending()
			if !errors.Is(findContext.Err(), context.Canceled) || !errors.Is(startContext.Err(), context.Canceled) {
				t.Fatalf("work was not cancelled: find=%v start=%v", findContext.Err(), startContext.Err())
			}
			if late, err := find.StartFind(t.Context(), findBM(find), "media", "", "report"); late != "" || err == nil {
				t.Fatalf("Find admission reopened: id=%q err=%v", late, err)
			}
			if late, _, err := runner.admit(t.Context()); late != nil || !errors.Is(err, errJobRunnerStopped) {
				t.Fatalf("job admission reopened: owner=%v err=%v", late, err)
			}
			if err := h.mutate.occupy(); !errors.Is(err, errWritersStopped) {
				t.Fatalf("writer admission reopened: err=%v", err)
			}
			if releaseJobFirst {
				owner.finishStart()
			} else {
				gate.unblock()
				if err := find.ServiceShutdown(); err != nil {
					t.Fatal(err)
				}
			}
			assertPending()
			owner.finishStart()
			gate.unblock()
			if err := ShutdownWork(t.Context(), runner, find, h.mutate); err != nil {
				t.Fatal(err)
			}
			if err := ShutdownWork(cancelled, runner, find, h.mutate); err != nil {
				t.Fatalf("already-drained work rejected repeated wait: %v", err)
			}
		})
	}
}

func TestShutdownWorkWaitErrors(t *testing.T) {
	for _, held := range []string{"job", "find"} {
		for _, reason := range []string{"cancelled", "deadline"} {
			t.Run(held+"/"+reason, func(t *testing.T) {
				h := newHarness(t)
				h.connect(t)
				runner := h.transfer.runner
				find := NewFindService(h.transfer.connections)
				var ids []string
				var gate *findRequestGate
				var finish func()
				if held == "job" {
					owner, _, err := runner.admit(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					finish = owner.finishStart
				} else {
					gate = gateFindRequests(t, h, true)
					id, err := find.StartFind(t.Context(), findBM(find), "media", "", "report")
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, id)
					awaitConnection(t, gate.entered)
					finish = gate.unblock
				}
				defer cleanupFind(t, find, &ids, gate)
				defer finish()
				ctx, cancel := context.WithCancel(context.Background())
				want := context.Canceled
				if reason == "deadline" {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					want = context.DeadlineExceeded
				} else {
					cancel()
				}
				defer cancel()
				if err := ShutdownWork(ctx, runner, find, h.mutate); !errors.Is(err, want) {
					t.Fatalf("wait %s = %v, want %v", held, err, want)
				}
				finish()
				if err := ShutdownWork(t.Context(), runner, find, h.mutate); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
