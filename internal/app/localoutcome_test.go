package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func TestLocalMissingOutcomeRefusesCompletion(t *testing.T) {
	for _, test := range []struct {
		kind    jobs.Type
		version int
	}{{jobs.TypeDownload, 0}, {jobs.TypeUpload, 1}} {
		t.Run(string(test.kind), func(t *testing.T) {
			h := newHarness(t)
			bm := h.ensureBookmark(t)
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: test.kind, ItemRecordsVersion: test.version, DestBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.transfer.runner.register(job.ID, cancel)
			h.transfer.runLocal(ctx, job.ID, "", test.version, job.Counters, func(context.Context, func(int64, int64, int64)) (*local.Report, error) {
				return nil, nil
			})
			got, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), job.ID)
			if err != nil || got.State != "failed" || got.Error != "local transfer returned no outcome" || got.Counters != (jobs.Counters{}) {
				t.Fatalf("missing outcome reported as success: %+v %v", got, err)
			}
			events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
			if err != nil || len(events) != 1 || events[0].Operation != "transfer.local.failed" || events[0].Result != "failed" {
				t.Fatalf("missing outcome audit: %+v %v", events, err)
			}
			if _, active := h.transfer.runner.cancel(job.ID); active {
				t.Fatal("finished body still accepts cancellation")
			}
		})
	}
}

func TestLocalPostCommitTerminalOutcome(t *testing.T) {
	for _, test := range []struct {
		name     string
		issues   int64
		failure  bool
		runError error
		state    string
		audit    string
		done     int64
	}{
		{"issue", 1, false, nil, "partial", "verify_mismatch", 1},
		{"many_issues", 201, false, nil, "partial", "verify_mismatch", 1},
		{"later_error", 1, false, errors.New("later engine failure"), "partial", "verify_mismatch", 1},
		{"later_cancel", 1, false, fmt.Errorf("later batch stopped: %w", context.Canceled), "partial", "verify_mismatch", 1},
		{"mixed", 1, true, errors.New("later engine failure"), "partial", "verify_mismatch", 1},
		{"complete_control", 0, false, nil, "complete", "applied", 1},
		{"failure_control", 0, true, nil, "partial", "verify_mismatch", 1},
		{"error_after_completion", 0, false, errors.New("engine failure"), "partial", "verify_mismatch", 1},
		{"cancel_after_completion", 0, false, context.Canceled, "partial", "verify_mismatch", 1},
		{"wrapped_cancel_after_completion", 0, false, fmt.Errorf("later batch stopped: %w", context.Canceled), "partial", "verify_mismatch", 1},
		{"failure_then_error", 0, true, errors.New("later engine failure"), "partial", "verify_mismatch", 0},
		{"failure_then_cancel", 0, true, fmt.Errorf("later batch stopped: %w", context.Canceled), "partial", "verify_mismatch", 0},
		{"no_completion_error_control", 0, false, errors.New("engine failure"), "failed", "failed", 0},
		{"no_completion_cancel_control", 0, false, context.Canceled, "cancelled", "failed", 0},
		{"no_completion_wrapped_cancel_control", 0, false, fmt.Errorf("batch stopped: %w", context.Canceled), "cancelled", "failed", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			bookmark := h.transfer.connections.ActiveBookmarkID()
			base := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 30}
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, DestBookmarkID: bookmark, Counters: base})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.transfer.runner.register(job.ID, cancel)
			h.transfer.runLocal(ctx, job.ID, bookmark, job.ItemRecordsVersion, base, func(_ context.Context, progress func(int64, int64, int64)) (*local.Report, error) {
				progress(2, 2, 20)
				report := &local.Report{Done: test.done, Verified: test.done, Bytes: 10 * test.done, NativeRouted: 1, PostCommitIssues: test.issues}
				if test.issues > 0 {
					report.PostCommitIssueSamples = []local.Failure{{Name: "live/first", Stage: "commit-record", Err: "record refused"}}
				}
				if test.failure {
					report.Failures = []local.Failure{{Name: "live/second", Stage: "verify", Err: "size mismatch"}}
				}
				if errors.Is(test.runError, context.Canceled) {
					cancel()
				}
				return report, test.runError
			})
			got, err := h.jobsSvc.Get(t.Context(), h.bookmarkID(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 30, ObjectsCopied: test.done, ObjectsVerified: test.done, BytesCopied: 10 * test.done, NativeRouted: 1}
			if test.failure {
				want.ObjectsFailed = 1
			}
			if got.State != test.state || got.Counters != want {
				t.Errorf("terminal result lost observed outcomes: %+v; want state=%s counters=%+v", got, test.state, want)
			}
			if test.issues > 0 {
				for _, part := range []string{fmt.Sprintf("%d post-commit issue", test.issues), "live/first", "commit-record", "record refused"} {
					if !strings.Contains(got.Error, part) {
						t.Errorf("terminal issue summary missing %q: %q", part, got.Error)
					}
				}
			}
			if test.failure && !strings.Contains(got.Error, "1 failed (first: live/second: size mismatch)") {
				t.Errorf("terminal transfer failure hidden: %q", got.Error)
			}
			if test.runError != nil && (test.state == "partial" || !errors.Is(test.runError, context.Canceled)) && !strings.Contains(got.Error, test.runError.Error()) {
				t.Errorf("later terminal reason hidden: %q", got.Error)
			}
			if (test.state == "complete" || test.state == "cancelled") && got.Error != "" {
				t.Errorf("control acquired a new error: %q", got.Error)
			}
			events, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
			if err != nil || len(events) != 1 || events[0].Operation != "transfer.local."+test.state || events[0].Resource != "job "+job.ID || events[0].Result != test.audit {
				t.Errorf("terminal audit differs: %+v, %v", events, err)
			}
			if _, active := h.transfer.runner.cancel(job.ID); active {
				t.Error("terminal job still accepts cancellation")
			}
		})
	}
}
