package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func TestUploadCapturedRefusalStopsAuthenticationRetry(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("refuse_%t", refuse), func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "unselected", "remote sentinel")
			seed(mock, "outside", "keep", "outside sentinel")
			witness := uploadWitness(t, mock)
			before := uploadSnapshot(t, witness)
			root := t.TempDir()
			path := writeFile(t, root, "item", "owned bytes")
			unselected := writeFile(t, root, "unselected", "local sentinel")
			preflight, err := PreflightUpload(t.Context(), s, "media", "live/", []string{path})
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			engine := &uploadRecordingEngine{}
			engine.run = func(ctx context.Context, src, dst string, names []string, event func(rclone.Event)) (*rclone.Result, error) {
				if engine.calls == 1 {
					event(rclone.Event{Msg: `Uploading segment file "probe/segment/00000000" into "media_segments"`})
					return &rclone.Result{ExitCode: 1, Errors: []string{"401 unauthorized"}}, nil
				}
				return (&fsEngine{s}).Copy(ctx, src, dst, names, event)
			}
			admitted, executed, verified, captures := 0, 0, 0, 0
			var execution transfercopy.Observation
			recorder := &UploadRecorder{Admit: func(a UploadAdmission) error {
				admitted++
				if a.Item.Path != path || a.Item.Name != "live/item" || a.StagingObject != ".aeronautes/stage/retry-stop/batch/item" {
					t.Fatalf("retry changed identity: %+v", a)
				}
				return nil
			}, Execute: func(item UploadItem, observation transfercopy.Observation) error {
				executed++
				execution = observation
				if item != preflight.Items[0] {
					t.Fatal("retry lost original execution identity")
				}
				return nil
			}, Verify: func(item UploadItem, observation transfercopy.Observation) error {
				verified++
				if item != preflight.Items[0] || observation != (transfercopy.Observation{Result: transfercopy.ResultVerified, Effect: transfercopy.EffectNotAttempted}) {
					t.Fatal("retry invented verification")
				}
				return nil
			}}
			refusal := errors.New("fixture captured record refused")
			report, runErr := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", "live/", "retry-stop", preflight, nil,
				func(kind, container, prefix, object string) error {
					captures++
					if kind != "captured" || container != "media_segments" || prefix != "probe/segment/" || object != "" {
						t.Fatal("retry lost canonical captured identity")
					}
					if refuse {
						return refusal
					}
					return nil
				}, recorder)
			if captures != 1 || admitted != 1 || executed != 1 || report == nil {
				t.Fatalf("retry boundary counts: capture=%d admit=%d execute=%d report=%+v", captures, admitted, executed, report)
			}
			want := transfercopy.Observation{Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 11}
			if refuse {
				want = transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved, Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeUnknown}}
				if engine.calls != 1 || report.Done != 0 || report.Bytes != 0 || len(report.Failures) != 1 || verified != 0 || !errors.Is(runErr, refusal) {
					t.Fatalf("required capture refusal admitted retry or lost failure: calls=%d report=%+v verified=%d error=%v", engine.calls, report, verified, runErr)
				}
			} else if engine.calls != 2 || report.Done != 1 || report.Bytes != 11 || len(report.Failures) != 0 || verified != 1 || runErr != nil {
				t.Fatalf("bounded authentication retry control failed: calls=%d report=%+v verified=%d error=%v", engine.calls, report, verified, runErr)
			}
			if execution != want {
				t.Fatalf("retry observation differs: %+v want=%+v", execution, want)
			}
			after := uploadSnapshot(t, witness)
			if !refuse {
				live := after.Containers["media"].Objects["live/item"]
				if live.Size != 11 || live.Class != "plain" || live.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("owned bytes"))) {
					t.Fatalf("retry copied fact lacks full bytes: %+v", live)
				}
				delete(after.Containers["media"].Objects, "live/item")
			}
			if delta := oracle.Diff(before, after); !delta.Empty() {
				t.Fatalf("retry boundary changed collateral: %s", delta)
			}
			for file, want := range map[string]string{path: "owned bytes", unselected: "local sentinel"} {
				if body, err := os.ReadFile(file); err != nil || string(body) != want {
					t.Fatalf("retry changed local source: %q %v", body, err)
				}
			}
		})
	}
}
