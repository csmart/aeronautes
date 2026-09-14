package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestDownloadStageCleanupKeepsEarlierOutcome(t *testing.T) {
	for _, test := range []struct {
		name, state string
		done        int64
		failure     bool
		post        bool
		err         error
	}{
		{"copied", "partial", 1, false, false, nil},
		{"failed_name", "partial", 0, true, false, nil},
		{"mixed", "partial", 1, true, false, nil},
		{"post_placement", "partial", 1, false, true, nil},
		{"error", "failed", 0, false, false, errors.New("earlier engine refusal")},
		{"cancelled", "cancelled", 0, false, false, context.Canceled},
		{"error_after_copy", "partial", 1, false, false, errors.New("later engine refusal")},
		{"cancel_after_copy", "partial", 1, false, false, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			base := jobs.Counters{ObjectsPlanned: 2, BytesPlanned: 42}
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, Counters: base})
			if err != nil {
				t.Fatal(err)
			}
			report := &local.Report{Done: test.done, Bytes: test.done * 21,
				StageCleanup: &local.StageCleanupResult{Issues: 3, FirstError: "owned directory retained", Attempted: true}}
			if test.failure {
				report.Failures = []local.Failure{{Name: "selected", Err: "selected placement refused"}}
			}
			if test.post {
				report.PostCommitIssues = 1
				report.PostCommitIssueSamples = []local.Failure{{Name: "selected", Stage: "staging-cleanup", Err: "selected unlink refused"}}
			}
			h.transfer.runLocal(t.Context(), job.ID, "", 0, base, func(context.Context, func(int64, int64, int64)) (*local.Report, error) {
				return report, test.err
			})
			visible, err := h.jobsSvc.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := base
			want.ObjectsCopied, want.ObjectsVerified, want.BytesCopied = test.done, test.done, test.done*21
			if test.failure {
				want.ObjectsFailed = 1
			}
			if visible.State != test.state || visible.Counters != want {
				t.Errorf("cleanup changed the earlier outcome: %+v", visible)
			}
			parts := []string{"download stage cleanup: 3 issues", "owned directory retained"}
			if test.failure {
				parts = append(parts, "selected placement refused")
			}
			if test.post {
				parts = append(parts, "1 post-commit issues", "selected unlink refused")
			}
			if test.err != nil && test.state != "cancelled" {
				parts = append(parts, test.err.Error())
			}
			for _, part := range parts {
				if !strings.Contains(visible.Error, part) {
					t.Errorf("cleanup erased %q from %q", part, visible.Error)
				}
			}
			audits, err := h.auditSvc.Search(t.Context(), AuditQueryDTO{OperationPrefix: "transfer.local.", Limit: 10})
			if err != nil || len(audits) != 1 || audits[0].Operation != "transfer.local."+test.state || audits[0].Resource != "job "+job.ID {
				t.Errorf("cleanup terminal audit differs: %+v %v", audits, err)
			}
			if len(audits) == 1 && (audits[0].Summary != "download stage cleanup: 3 issues" || audits[0].Result != string(terminalAuditResult(jobs.State(test.state)))) {
				t.Errorf("cleanup audit must retain a safe group reason: %+v", audits[0])
			}
		})
	}
}

type downloadStageTestEngine func(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error)

func (run downloadStageTestEngine) Copy(ctx context.Context, src, dst string, names []string, progress func(rclone.Event)) (*rclone.Result, error) {
	return run(ctx, src, dst, names, progress)
}

func TestDownloadStageBoundaryPreservesEngineFailure(t *testing.T) {
	engineErr, boundaryErr := errors.New("engine refusal"), errors.New("boundary refusal")
	result := &rclone.Result{}
	returned, observed := false, false
	engine := downloadStageBoundaryEngine{
		Engine: downloadStageTestEngine(func(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
			returned = true
			return result, engineErr
		}),
		after: func(ctx context.Context, stage string) error {
			if !returned || stage != "owned stage" || ctx != t.Context() {
				t.Error("boundary did not follow its invocation")
			}
			observed = true
			return boundaryErr
		},
	}
	got, err := engine.Copy(t.Context(), "source", "owned stage", []string{"selected"}, nil)
	if !observed || got != result || !errors.Is(err, engineErr) || !errors.Is(err, boundaryErr) {
		t.Errorf("boundary erased an engine result or either error: %+v %v", got, err)
	}
}

// ADR 0048 decision 9: the terminal message and the audit summary name one
// of three outcomes and assert neither of the others. Selecting on Retained
// alone claimed a recording failure whenever cleanup ran and deliberately
// left files behind.
func TestDownloadStageCleanupNamesOneOfThreeOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		cleanup *local.StageCleanupResult
		want    string
		absent  []string
	}{
		{
			name:    "cleanup did not run",
			cleanup: &local.StageCleanupResult{Attempted: false, Retained: true},
			want:    "retained after required item recording failed; cleanup was not attempted",
			absent:  []string{"retained after cleanup", "download stage cleanup:"},
		},
		{
			name:    "cleanup ran and left residue",
			cleanup: &local.StageCleanupResult{Attempted: true, Retained: true, Issues: 2, FirstError: "a link at a planned name"},
			want:    "download stage retained after cleanup: 2 issues",
			absent:  []string{"item recording failed", "cleanup was not attempted"},
		},
		{
			name:    "cleanup ran and left nothing",
			cleanup: &local.StageCleanupResult{Attempted: true, Issues: 1, FirstError: "a name outside the cleanup scope"},
			want:    "download stage cleanup: 1 issues",
			absent:  []string{"item recording failed", "retained after cleanup"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newHarness(t)
			base := jobs.Counters{ObjectsPlanned: 1, BytesPlanned: 21}
			job, err := h.transfer.jobs.Create(t.Context(), jobs.Job{Type: jobs.TypeDownload, Counters: base})
			if err != nil {
				t.Fatal(err)
			}
			report := &local.Report{Done: 1, Bytes: 21, StageCleanup: test.cleanup}
			h.transfer.runLocal(t.Context(), job.ID, "", 0, base, func(context.Context, func(int64, int64, int64)) (*local.Report, error) {
				return report, nil
			})
			finished, err := h.transfer.jobs.Get(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(finished.Error, test.want) {
				t.Errorf("terminal message %q does not name the outcome %q", finished.Error, test.want)
			}
			for _, other := range test.absent {
				if strings.Contains(finished.Error, other) {
					t.Errorf("terminal message %q also asserts %q", finished.Error, other)
				}
			}
		})
	}
}
