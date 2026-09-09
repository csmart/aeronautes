package local

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestUploadAdmissionStopsBeforeOpenAndDispatch(t *testing.T) {
	for _, mode := range []string{"first_admission", "buffered_then_refusal", "cancel_native", "cancel_buffered"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := session(t)
			paths := []string{writeFile(t, t.TempDir(), "first", "alpha"), writeFile(t, t.TempDir(), "second", "bravo")}
			preflight, err := PreflightUpload(t.Context(), s, "media", "scope/", paths)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			if mode == "cancel_native" {
				preflight.Items[0].Route = "native"
			} else if mode == "buffered_then_refusal" {
				preflight.Items[1].Route = "native"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			refusal := errors.New("fixture admission refused")
			engine := &uploadRecordingEngine{run: func(context.Context, string, string, []string, func(rclone.Event)) (*rclone.Result, error) {
				t.Error("unadmitted or cancelled upload entered an engine")
				return nil, errors.New("unexpected engine invocation")
			}}
			var admissions, executions []string
			var observations []transfercopy.Observation
			recorder := &UploadRecorder{Admit: func(a UploadAdmission) error {
				admissions = append(admissions, a.Item.Path)
				if mode == "cancel_native" || mode == "cancel_buffered" {
					cancel()
					return nil
				}
				if mode == "first_admission" || a.Item.Path == paths[1] {
					return refusal
				}
				return nil
			}, Execute: func(item UploadItem, observation transfercopy.Observation) error {
				executions = append(executions, item.Path)
				observations = append(observations, observation)
				return nil
			}, Verify: func(UploadItem, transfercopy.Observation) error {
				t.Error("unstarted upload invented verification")
				return nil
			}}
			before := uploadSnapshot(t, uploadWitness(t, mock))
			report, err := Upload(ctx, s, engine, "aeronautes_dst:media", "media", "scope/", "admission-stop", preflight, nil, nil, recorder)
			wantErr := refusal
			if mode == "cancel_native" || mode == "cancel_buffered" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || report == nil || report.Done != 0 || report.Bytes != 0 || report.NativeRouted != 0 || len(report.Failures) != 0 || engine.calls != 0 {
				t.Fatalf("admission/cancellation did not prevent dependent work: %+v %v calls=%d", report, err, engine.calls)
			}
			wantAdmissions := paths[:1]
			if mode == "buffered_then_refusal" {
				wantAdmissions = paths
			}
			if !reflect.DeepEqual(admissions, wantAdmissions) {
				t.Fatalf("later upload admission crossed refusal: %v want=%v", admissions, wantAdmissions)
			}
			if mode == "first_admission" {
				if len(executions) != 0 {
					t.Fatalf("refused admission gained a final observation: %v", executions)
				}
			} else {
				want := transfercopy.Observation{Result: transfercopy.ResultNotAttempted, Effect: transfercopy.EffectNotAttempted,
					Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeCancelled}}
				if mode == "cancel_native" {
					want.Diagnostic.Stage = transfercopy.StageSourceRead
				} else if mode == "buffered_then_refusal" {
					want.Diagnostic.Code = transfercopy.CodeIncomplete
				}
				if !reflect.DeepEqual(executions, paths[:1]) || !reflect.DeepEqual(observations, []transfercopy.Observation{want}) {
					t.Fatalf("unstarted admitted work lost its exact final fact: %v %+v want=%+v", executions, observations, want)
				}
			}
			if delta := oracle.Diff(before, uploadSnapshot(t, uploadWitness(t, mock))); !delta.Empty() {
				t.Fatalf("stopped admission changed owned storage: %s", delta)
			}
			for i, path := range paths {
				if body, err := os.ReadFile(path); err != nil || string(body) != []string{"alpha", "bravo"}[i] {
					t.Fatalf("stopped admission changed source: %q %v", body, err)
				}
			}
		})
	}
}
