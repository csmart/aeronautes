//go:build linux

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type cancelledDownloadEngine struct {
	result       *rclone.Result
	err          error
	cancel       context.CancelFunc
	waitDeadline bool
}

func (e cancelledDownloadEngine) Copy(ctx context.Context, _, destination string, _ []string, _ func(rclone.Event)) (*rclone.Result, error) {
	if err := os.WriteFile(filepath.Join(destination, "a.partial"), []byte("partial bytes"), 0600); err != nil {
		return nil, err
	}
	if e.waitDeadline {
		<-ctx.Done()
	} else if e.cancel != nil {
		e.cancel()
	}
	return e.result, e.err
}

func TestDownloadEngineCancellationClassification(t *testing.T) {
	for _, test := range []struct {
		name                           string
		result                         *rclone.Result
		callErr                        error
		noCancel, deadline, wantCancel bool
	}{
		{name: "interrupt", result: &rclone.Result{Cancelled: true, ExitCode: 130}, wantCancel: true},
		{name: "forced_kill", result: &rclone.Result{Cancelled: true, ExitCode: -1}, wantCancel: true},
		{name: "finished_during_cancel", result: &rclone.Result{Cancelled: true}, wantCancel: true},
		{name: "uncancelled_context", result: &rclone.Result{Cancelled: true, ExitCode: 130}, noCancel: true},
		{name: "deadline", result: &rclone.Result{Cancelled: true, ExitCode: -1}, deadline: true},
		{name: "call_error", result: &rclone.Result{Cancelled: true, ExitCode: 130}, callErr: errors.New("engine wait failed")},
		{name: "missing_result"},
		{name: "noncancel_exit", result: &rclone.Result{ExitCode: 1}},
		{name: "unexpected_exit", result: &rclone.Result{Cancelled: true, ExitCode: 1}},
		{name: "incomplete_log", result: &rclone.Result{Cancelled: true, ExitCode: 130, LogStreamErr: "truncated stream"}},
		{name: "collected_error", result: &rclone.Result{Cancelled: true, ExitCode: 130, Errors: []string{"independent object read failure"}}},
		{name: "counted_error", result: &rclone.Result{Cancelled: true, ExitCode: 130, Stats: rclone.Stats{Errors: 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, mock := session(t)
			for _, name := range []string{"a", "b"} {
				seed(mock, "media", "scope/"+name, "body")
			}
			root := t.TempDir()
			writeFile(t, root, "sentinel", "unchanged")
			plan, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
			if err != nil || plan.Rejected() {
				t.Fatalf("preflight: %+v %v", plan, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			engine := cancelledDownloadEngine{result: test.result, err: test.callErr, cancel: cancel}
			if test.noCancel {
				engine.cancel = nil
			}
			if test.deadline {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(t.Context(), 5*time.Second)
				defer end()
				engine.waitDeadline = true
			}
			admitted := 0
			executed := map[string]transfercopy.Observation{}
			recorder := &DownloadRecorder{
				Admit: func(DownloadAdmission) error { admitted++; return nil },
				Execute: func(item DownloadItem, observation transfercopy.Observation) error {
					executed[item.Name] = observation
					return nil
				},
				Verify: func(DownloadItem, transfercopy.Observation) error { t.Error("failed engine item verified"); return nil },
			}
			report, err := Download(ctx, source, engine, "remote:media", "media", "scope/", root, plan, nil, recorder)
			// The run ends with the context's own state; whether a file
			// failed for the cancel or for an independent engine failure is
			// in its record, which is what the job's state is chosen from.
			if !errors.Is(err, ctx.Err()) {
				t.Fatalf("run ended %v with the context at %v", err, ctx.Err())
			}
			if report == nil || admitted != 2 || len(executed) != 2 || len(report.Failures) != 2 || report.Done != 0 || report.Bytes != 0 {
				t.Fatalf("lost engine outcomes: report=%+v admitted=%d executed=%+v", report, admitted, executed)
			}
			code := transfercopy.CodeUnknown
			if test.wantCancel {
				code = transfercopy.CodeCancelled
			}
			want := transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved, Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: code}}
			for _, name := range []string{"a", "b"} {
				if got := executed["scope/"+name]; got != want {
					t.Errorf("%s observation=%+v want=%+v", name, got, want)
				}
				if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("engine failure left a file at %s: %v", name, err)
				}
			}
			for _, failure := range report.Failures {
				if failure.Cancelled != test.wantCancel {
					t.Errorf("failure cancellation=%v want=%v", failure.Cancelled, test.wantCancel)
				}
			}
			// The application removes nothing the engine left, and says that
			// the engine did not end normally (ADR 0122 decision 5).
			if !report.EngineStopped {
				t.Fatalf("the report does not say the engine stopped: %+v", report)
			}
			for name, want := range map[string]string{"sentinel": "unchanged", "a.partial": "partial bytes"} {
				if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != want {
					t.Errorf("retained file %s: %q %v", name, data, err)
				}
			}
		})
	}
}
