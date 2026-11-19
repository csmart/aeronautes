package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/jobs"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/local"
)

func TestLocalMissingOutcomeRefusesCompletion(t *testing.T) {
	for _, kind := range []jobs.Type{jobs.TypeDownload, jobs.TypeUpload} {
		t.Run(string(kind), func(t *testing.T) {
			h := newHarness(t)
			bm := h.ensureBookmark(t)
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: kind, ItemRecordsVersion: 1, DestBookmarkID: bm})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.transfer.runner.register(job.ID, cancel)
			h.transfer.runLocal(ctx, job.ID, "", job.Counters, func(context.Context, func(int64, int64, int64, int64)) (*local.Report, error) {
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
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeUpload, ItemRecordsVersion: 1, DestBookmarkID: bookmark, Counters: base})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h.transfer.runner.register(job.ID, cancel)
			h.transfer.runLocal(ctx, job.ID, bookmark, base, func(_ context.Context, progress func(int64, int64, int64, int64)) (*local.Report, error) {
				recorder := h.transfer.uploadItemRecorder(ctx, job.ID, UploadRequestDTO{BookmarkID: bookmark, Container: "media"})
				if test.done != 0 {
					item := local.UploadItem{Path: filepath.Join(t.TempDir(), "first"), Name: "live/first", Size: 10}
					if err := recorder.Admit(local.UploadAdmission{Item: item}); err != nil {
						t.Fatal(err)
					}
					if err := recorder.Execute(item, transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 10}); err != nil {
						t.Fatal(err)
					}
					if err := recorder.Verify(item, transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}); err != nil {
						t.Fatal(err)
					}
				}
				if test.failure {
					item := local.UploadItem{Path: filepath.Join(t.TempDir(), "second"), Name: "live/second", Size: 20}
					if err := recorder.Admit(local.UploadAdmission{Item: item}); err != nil {
						t.Fatal(err)
					}
					if err := recorder.Verify(item, transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectNotAttempted,
						Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageVerification, Code: transfercopy.CodeUnknown}}); err != nil {
						t.Fatal(err)
					}
				}
				progress(2, 0, 2, 20)
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
			if test.failure && !strings.Contains(got.Error, "transfer failures (first: live/second: size mismatch)") {
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

// A download that does not finish names the directories it left behind, and a
// directory is not a secret because its name holds a word that often guards
// one: the job's outcome and its trail event are saved whatever it is called.
func TestLocalOutcomeSurvivesADirectoryNamedForACredential(t *testing.T) {
	// Named once so the case cannot drift apart from internal/audit's list.
	const refused = "password"
	if audit.SummaryRefusal(refused) == "" {
		t.Fatalf("this test needs a substring the audit filter refuses; %q is not one", refused)
	}
	const count = "created 1 directories inside the selected directory that were not there before"
	for _, dir := range []string{"letters", refused + "s"} {
		t.Run(dir, func(t *testing.T) {
			h := newHarness(t)
			bookmark := h.ensureBookmark(t)
			base := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 21}
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, ItemRecordsVersion: 1, Counters: base, SourceBookmarkID: bookmark})
			if err != nil {
				t.Fatal(err)
			}
			h.transfer.runLocal(t.Context(), job.ID, bookmark, base, func(context.Context, func(int64, int64, int64, int64)) (*local.Report, error) {
				return &local.Report{
					Failures:           []local.Failure{{Name: dir + "/vault.kdbx", Err: "placement refused"}},
					CreatedDirectories: 1, CreatedDirectorySamples: []string{dir},
				}, nil
			})
			if err := h.transfer.jobs.Available(); err != nil {
				t.Fatalf("the job store was left unavailable: %v", err)
			}
			saved, err := h.transfer.jobs.Get(t.Context(), job.ID)
			if err != nil || saved.State != jobs.StatePartial {
				t.Fatalf("job = %s %v, want partial", saved.State, err)
			}
			if !strings.Contains(saved.Error, count+" ("+dir+")") {
				t.Errorf("the job's message lost the directory it names: %q", saved.Error)
			}
			events, err := h.auditSvc.audit.List(t.Context(), audit.Query{Limit: 10})
			if err != nil || len(events) != 1 || events[0].Operation != "transfer.local.partial" {
				t.Fatalf("trail = %+v, %v; want the one terminal event", events, err)
			}
			summary := events[0].Summary
			if !strings.Contains(summary, count) || !strings.Contains(summary, "item outcomes:") {
				t.Errorf("the terminal record lost its facts: %q", summary)
			}
			if named := strings.Contains(summary, dir); named != (dir == "letters") {
				t.Errorf("directory %q named in the record = %v: %q", dir, named, summary)
			}
		})
	}
}
