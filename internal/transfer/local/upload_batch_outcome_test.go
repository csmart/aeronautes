package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestUploadBatchOutcomesSurviveLaterStop(t *testing.T) {
	for _, mode := range []string{"cancel", "returned_error", "returned_failure", "control"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "keep", "remote sentinel")
			seed(mock, "media", ".aeronautes/stage/other-job/keep", "other job sentinel")
			seed(mock, "outside", "keep", "outside sentinel")
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			dirA, dirB := t.TempDir(), t.TempDir()
			paths := []string{writeFile(t, dirA, "first", "alpha"), writeFile(t, dirA, "second", "bravo"), writeFile(t, dirB, "third", "gamma")}
			unselected := writeFile(t, dirA, "unselected", "local sentinel")
			preflight, err := PreflightUpload(t.Context(), s, "media", "live/", paths)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var admitted []string
			executed, verified := map[string]transfercopy.Observation{}, map[string]transfercopy.Observation{}
			recorder := &UploadRecorder{Admit: func(a UploadAdmission) error {
				admitted = append(admitted, a.Item.Path)
				if a.StagingObject != ".aeronautes/stage/batch-outcome/batch/"+filepath.Base(a.Item.Path) {
					t.Fatal("batch admission lost staging identity")
				}
				return nil
			}, Execute: func(item UploadItem, observation transfercopy.Observation) error {
				if _, exists := executed[item.Path]; exists {
					t.Fatal("batch recorded an identity twice")
				}
				executed[item.Path] = observation
				return nil
			}, Verify: func(item UploadItem, observation transfercopy.Observation) error {
				if _, exists := executed[item.Path]; !exists {
					t.Fatal("verification preceded execution acknowledgement")
				}
				verified[item.Path] = observation
				if mode == "cancel" && item.Path == paths[0] {
					cancel()
				}
				return nil
			}}
			engine := &uploadRecordingEngine{}
			engine.run = func(ctx context.Context, src, dst string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
				if !reflect.DeepEqual(admitted, paths) {
					t.Fatal("engine dispatched before complete checked admission")
				}
				result, err := (&fsEngine{s}).Copy(ctx, src, dst, names, event)
				if err != nil {
					return result, err
				}
				if engine.calls == 1 && mode == "returned_error" {
					return result, errors.New("fixture engine failed after staging")
				}
				if engine.calls == 1 && mode == "returned_failure" {
					return &rclone.Result{ExitCode: 3}, nil
				}
				return result, nil
			}
			report, runErr := Upload(ctx, s, engine, "aeronautes_dst:media", "media", "live/", "batch-outcome", preflight, nil, nil, recorder)
			assertUploadBatchOutcome(t, mode, paths, engine.calls, report, runErr, executed, verified)
			after := uploadSnapshot(t, witness)
			for i, path := range paths {
				name := filepath.Base(path)
				live := mode == "control" || mode == "cancel" && i == 0 || mode != "cancel" && i == 2
				staged := mode == "cancel" && i == 1 || mode != "cancel" && mode != "control" && i < 2
				for object, present := range map[string]bool{"live/" + name: live, ".aeronautes/stage/batch-outcome/batch/" + name: staged} {
					observed, found := after.Containers["media"].Objects[object]
					body := []byte([]string{"alpha", "bravo", "gamma"}[i])
					if found != present || found && (observed.Class != "plain" || observed.Size != 5 || observed.Digest != fmt.Sprintf("%x", sha256.Sum256(body))) {
						t.Fatalf("batch stop effect differs for %q: present=%t/%t object=%+v", object, found, present, observed)
					}
					if found {
						delete(after.Containers["media"].Objects, object)
					}
				}
				if body, err := os.ReadFile(path); err != nil || string(body) != []string{"alpha", "bravo", "gamma"}[i] {
					t.Fatalf("batch stop changed source: %q %v", body, err)
				}
			}
			if delta := oracle.Diff(before, after); !delta.Empty() {
				t.Fatalf("batch stop changed collateral: %s", delta)
			}
			if body, err := os.ReadFile(unselected); err != nil || string(body) != "local sentinel" {
				t.Fatalf("batch stop changed unselected file: %q %v", body, err)
			}
		})
	}
}

func assertUploadBatchOutcome(t *testing.T, mode string, paths []string, calls int, report *Report, runErr error, executed, verified map[string]transfercopy.Observation) {
	t.Helper()
	done, failures, engines := int64(1), 2, 2
	if mode == "control" {
		done, failures = 3, 0
	}
	if mode == "cancel" {
		failures, engines = 0, 1
	}
	if report == nil || report.Done != done || report.Bytes != done*5 || len(report.Failures) != failures || report.NativeRouted != 0 || report.PostCommitIssues != 0 || calls != engines ||
		mode == "cancel" && !errors.Is(runErr, context.Canceled) || mode != "cancel" && runErr != nil {
		t.Fatalf("later stop erased earlier outcomes: report=%+v error=%v calls=%d want done=%d failures=%d calls=%d", report, runErr, calls, done, failures, engines)
	}
	for i, failure := range report.Failures {
		if failure.Name != "live/"+filepath.Base(paths[i]) || failure.Stage != "engine" {
			t.Fatalf("failed batch lost original name: %+v", failure)
		}
	}
	wantExecution, wantVerification := map[string]transfercopy.Observation{}, map[string]transfercopy.Observation{}
	for i, path := range paths {
		switch {
		case mode == "control" || mode == "cancel" && i == 0 || mode != "cancel" && i == 2:
			wantExecution[path] = transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5}
			wantVerification[path] = transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}
		case mode == "cancel" && i == 2:
			wantExecution[path] = transfercopy.Observation{Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted,
				Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeCancelled}}
		case mode != "cancel":
			wantExecution[path] = transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
				Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeUnknown}}
		}
	}
	if !reflect.DeepEqual(executed, wantExecution) || !reflect.DeepEqual(verified, wantVerification) {
		t.Fatalf("batch stop invented a phase or erased copied bytes: execution=%+v want=%+v verification=%+v want=%+v", executed, wantExecution, verified, wantVerification)
	}
}
